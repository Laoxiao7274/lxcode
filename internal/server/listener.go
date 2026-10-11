// listener.go —— 服务监听生命周期：按 remote.json 的 Enabled 在「回环」与
// 「0.0.0.0:<port>」之间**热切换**。
//
// 为什么热切换而不是要求重启：开关在壳的「远程访问」页上，用户开完就该能被连；
// 反过来关掉也该立即回到回环——要求重启的话「关闭远程」在监听层是假的
// （0.0.0.0 仍开着，token 门只保护 /rpc 与 /attachments，/health 依旧裸奔）。
//
// 切换语义（Windows 实测约束）：
//   - 127.0.0.1:port 已绑时再绑 0.0.0.0:port 会 WSAEADDRINUSE——必须**先关旧
//     listener 再开新 listener**（顺序不可反）；刚关的端口可能有一瞬间的绑定
//     残留，重绑按短退避重试几次而不是一次失败就放弃；
//   - 关旧 listener 只停「接受新连接」，已接受的连接继续由各自 serve goroutine
//     服务——本机正在用的会话不因开关翻转而断线；
//   - 关闭远程时额外断开所有非回环客户端（关了还留着远端连接 = 开关是假的）；
//   - addr 本身已是通配（0.0.0.0 / :: / 空 host）或未装配 remote.json 时，不
//     改写绑定地址（显式 --addr 0.0.0.0 是用户的明确意愿）。
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// remoteWatchInterval 是 remote.json 开关的巡检周期。token 校验本来就是每次
// 升级现读文件，巡检只为监听地址翻转（Enabled 开/关）；2s 足够跟手，开销是
// 一次小 JSON 文件读。变量纯粹为了测试能缩短它。
var remoteWatchInterval = 2 * time.Second

// 重绑的尝试次数与间隔（翻转后先关旧再开新之间可能的短暂绑定残留）。
const (
	bindRetry      = 10
	bindRetryDelay = 300 * time.Millisecond
)

// remoteConfig 现读 remote.json（未装配 = 零值，Enabled=false——行为与旧版一致）。
func (s *Server) remoteConfig() config.RemoteAccess {
	s.remoteMu.Lock()
	path := s.remotePath
	s.remoteMu.Unlock()
	if path == "" {
		return config.RemoteAccess{}
	}
	return config.LoadRemoteAccess(path)
}

// desiredBindAddr 由 Enabled 算出当前应绑定的地址（理由见文件头）。
func desiredBindAddr(addr string, enabled bool) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return addr
	}
	if enabled {
		return net.JoinHostPort("0.0.0.0", port)
	}
	return addr
}

// boundListener 是一次绑定：listener + 服务它的 http.Server。closed 标记监听
// 被有意关闭（翻转/停机）——Serve 随后的退出错误不再上报（那不是故障）。
type boundListener struct {
	ln     net.Listener
	srv    *http.Server
	closed bool
}

// Run 监听并服务（生命周期 + remote.json 开关热切换）。ctx 取消即优雅退出
// （关所有客户端连接 → 关 MCP 子进程 → 逐个 Shutdown——与旧版 Run 同一收尾序）。
// 状态（cur/active/servers）全部归本 goroutine 独占，无锁。
func (s *Server) Run(ctx context.Context, addr string) error {
	s.setBaseCtx(ctx)
	handler := s.routes()

	dead := make(chan *boundListener, 1)
	var servers []*http.Server
	start := func(bind string) (*boundListener, error) {
		ln, err := net.Listen("tcp", bind)
		if err != nil {
			return nil, err
		}
		srv := &http.Server{Handler: handler}
		b := &boundListener{ln: ln, srv: srv}
		servers = append(servers, srv)
		go func() {
			if err := srv.Serve(ln); err != nil && !b.closed && err != http.ErrServerClosed {
				// 接受循环意外死亡（罕见）：交给主循环重绑。缓冲 1 + 丢弃式
				// 发送——主循环忙时也不能卡住这个 goroutine。
				log.Printf("监听 %s 的服务循环意外退出: %v", bind, err)
				select {
				case dead <- b:
				default:
				}
			}
		}()
		log.Printf("后端 WS 服务监听 %s%s", bind, protocol.Path)
		return b, nil
	}

	// 启动绑定：失败 = 启动失败（与旧版 ListenAndServe 返回错误一致——端口被
	// 占这类错误要在启动时就报，不能拖到巡检里默默重试）。
	cur := desiredBindAddr(addr, s.remoteConfig().Enabled)
	first, err := start(cur)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cur, err)
	}
	active := first

	// reconcile 把监听拉到「当前应绑的地址」：健康且地址没变 = 无操作；
	// 翻转 = 先关旧 listener（既有连接不断）再重绑新地址（短退避重试）。
	reconcile := func() {
		ra := s.remoteConfig()
		want := desiredBindAddr(addr, ra.Enabled)
		if active != nil && want == cur {
			return
		}
		if active != nil {
			active.closed = true
			_ = active.ln.Close()
			active = nil
		}
		if want != cur {
			if !ra.Enabled {
				// 关闭远程：远端已建立的连接立即断开（关了还留着 = 开关是假的）。
				s.closeRemoteClients()
			}
			log.Printf("监听地址切换: %s → %s（远程访问 Enabled=%v）", cur, want, ra.Enabled)
			cur = want
		}
		for i := 1; i <= bindRetry; i++ {
			b, err := start(cur)
			if err == nil {
				active = b
				return
			}
			log.Printf("重绑 %s 失败（第 %d/%d 次）: %v", cur, i, bindRetry, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(bindRetryDelay):
			}
		}
		log.Printf("重绑 %s 持续失败——随下一次巡检再试", cur)
	}

	ticker := time.NewTicker(remoteWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closeAllClients()
			// MCP stdio 服务器是子进程：不显式关会留孤儿（Windows 上尤其如此）。
			s.CloseMCP()
			for _, srv := range servers {
				_ = srv.Shutdown(context.Background())
			}
			return nil
		case b := <-dead:
			if b == active {
				active = nil
				reconcile()
			}
		case <-ticker.C:
			reconcile()
		}
	}
}
