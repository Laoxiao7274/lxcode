// probe.go 实现后端验收探针（--probe）：对运行中的后端做协议级检查，
// 是 install/update 脚本的健康验收面（参考项目的 wsprobe 角色的内置版）。
// 刻意不做 LLM 真实对话——那是冒烟（temp/smoke.mjs）的事，装机的验收
// 只关心"服务活着且协议面完整"，不该花模型 token。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/wsclient"
)

// probe 退出码（脚本据此分支）：
const (
	probeOK      = 0 // 全部通过
	probeDead    = 1 // 协议失败——服务死了/没监听（装机脚本应回滚）
	probeUnready = 2 // 服务活着但没配模型（装好了，等用户填配置；热加载会接上）
)

// runProbe 跑全部检查并返回退出码。addr 形如 127.0.0.1:7789。
func runProbe(addr string) int {
	fail := false
	unready := false

	be, err := wsclient.Dial(addr)
	if err != nil {
		fmt.Printf("✗ 连接后端 %s 失败: %v\n", addr, err)
		return probeDead
	}
	defer be.Close()
	fmt.Printf("✓ 连接 %s\n", addr)

	// ready 事件（连接建立即单发）
	if _, ok := waitEvent(be, protocol.EventReady, 3*time.Second); !ok {
		fmt.Println("✗ 未收到 connection.ready")
		return probeDead
	}
	fmt.Println("✓ connection.ready")

	// hello 往返（服务端身份与协议版本）
	var hello protocol.HelloResult
	if err := call(be, protocol.MethodHello, protocol.HelloParams{Client: "probe", Version: protocol.Version}, &hello); err != nil {
		fmt.Printf("✗ hello 失败: %v\n", err)
		return probeDead
	}
	if hello.Server != "lxcode" || hello.Version != protocol.Version {
		fmt.Printf("✗ 服务端身份不符: %+v\n", hello)
		return probeDead
	}
	fmt.Printf("✓ hello（%s 协议 v%s）\n", hello.Server, hello.Version)

	// model.list
	var ml protocol.ModelListResult
	if err := call(be, protocol.MethodModelList, nil, &ml); err != nil {
		fmt.Printf("✗ model.list 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ model.list（%d 个模型）\n", len(ml.Models))
	if ml.Roles[config.RoleDefault] == "" {
		fmt.Println("⚠ default 角色未绑定模型——服务活着但还不能对话（编辑配置后热加载生效）")
		unready = true
	}

	// session.list
	var sessions []protocol.SessionMeta
	if err := call(be, protocol.MethodSessionList, nil, &sessions); err != nil {
		fmt.Printf("✗ session.list 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ session.list（%d 个会话）\n", len(sessions))

	// 会话面：显式开一个探针会话 → 读它的历史 → 归档。
	//
	// chat.history 按 session_id 寻址，服务端**没有**"当前会话"这种全局焦点
	// （server.go 的 lastSessionID 注释明说协议请求绝不读它），空 session_id
	// 会被 server.session() 直接拒绝。所以只发 hello/model.list/session.list
	// 的探针拿不到历史——它必须像真客户端一样先开一个会话。
	//
	// 不这么做过的代价（2026-09-28 实测）：探针恒返回 1（= 协议失败），而
	// install.ps1 把非 0/2 当验收失败并"停服务 + 删除"、update.ps1 直接回滚
	// ——健康的服务被判成坏的。
	var sn protocol.SessionResult
	if err := call(be, protocol.MethodSessionNew, protocol.SessionNewParams{}, &sn); err != nil {
		fmt.Printf("✗ session.new 失败: %v\n", err)
		return probeDead
	}
	if sn.SessionID == "" {
		fmt.Println("✗ session.new 未返回会话 id")
		return probeDead
	}
	// 归档是**清理**而不是验收面。为什么必须归档：探针会话是空会话，
	// 空会话不进 session.list（store.List 刻意滤掉空白草稿），所以它不会
	// 出现在侧栏或归档区——但 Latest() 只滤 archived、不滤空白草稿，
	// 一条没归档的探针会话就是"最近的会话"，**后端重启会把用户恢复到
	// 探针会话上**。归档失败只提示、不改退出码：为一条清理失败把健康的
	// 服务判成坏的，正是上面那个缺陷的同类错误。
	defer func() {
		params := protocol.SessionArchiveParams{ID: sn.SessionID, Archived: true}
		if err := call(be, protocol.MethodSessionArchive, params, nil); err != nil {
			fmt.Printf("⚠ 探针会话归档失败（归档区可能多出一条空会话）: %v\n", err)
		}
	}()

	var hist protocol.ChatHistoryResult
	if err := call(be, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sn.SessionID}, &hist); err != nil {
		fmt.Printf("✗ chat.history 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ chat.history（%d 条消息，busy=%v）\n", len(hist.Messages), hist.Busy)

	if fail {
		return probeDead
	}
	if unready {
		return probeUnready
	}
	return probeOK
}

// call 是带超时的请求封装。
func call(be wsclient.Backend, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return be.Call(ctx, method, params, result)
}

// waitEvent 等待指定事件名（超时返回 false）。
func waitEvent(be wsclient.Backend, name string, timeout time.Duration) (json.RawMessage, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-be.Events():
			if !ok {
				return nil, false
			}
			if ev.Method == name {
				b, _ := json.Marshal(ev.Params)
				return b, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

// main 的入口包装（退出码直达 os.Exit）。
func probeMain(addr string) {
	os.Exit(runProbe(addr))
}
