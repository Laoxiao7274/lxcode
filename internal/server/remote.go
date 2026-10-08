// remote.go —— 远程访问的门（token 校验）与本地管理端点。
//
// 门：remote.json Enabled 时，**所有** /rpc 升级都必须携带匹配 token
// （查询参数 token= 或 Authorization: Bearer）——含本机回环：公网隧道最终
// 从 frpc 所在机器的回环进来，回环豁免等于门是假的。
// 管理端点：/remote-access 仅限**回环**访问（壳在本机，读 token / 开关 /
// 轮换都走它）；远程侧永远拿不到管理权。
package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/moyunteng/lxcode/internal/config"
)

// AttachRemoteAccess 装配 remote.json 路径（不装配 = 门不存在，行为与旧版一致）。
func (s *Server) AttachRemoteAccess(path string) {
	s.remoteMu.Lock()
	s.remotePath = path
	s.remoteMu.Unlock()
}

// checkRemoteToken 升级前的凭证校验。返回 error = 拒绝（HTTP 401）。
//
// remote.json 每次升级现读（连接不频繁，文件很小）——开关与轮换即时生效，
// 不需要热加载机制。
func (s *Server) checkRemoteToken(r *http.Request) error {
	s.remoteMu.Lock()
	path := s.remotePath
	s.remoteMu.Unlock()
	if path == "" {
		return nil // 未装配 = 旧版行为
	}
	ra := config.LoadRemoteAccess(path)
	if !ra.Enabled {
		return nil
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if token == "" {
		return errRemoteTokenRequired
	}
	if subtleEqual(token, ra.Token) {
		return nil
	}
	return errRemoteTokenInvalid
}

var (
	errRemoteTokenRequired = remoteErr("缺少访问令牌（后端已开启远程访问）")
	errRemoteTokenInvalid  = remoteErr("访问令牌无效")
)

type remoteErr string

func (e remoteErr) Error() string { return string(e) }

// subtleEqual 常量时间比较（避免逐字节短路的时序侧信道）。长度不同的
// token 本身就无效，先比长度再比较即可。
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleRemoteAccess 是壳的本地管理端点（loopback-only）：
//   - GET    → {enabled, token}
//   - POST   → {"enabled": true|false} 开关 / {"rotate": true} 轮换 token
//     （首次开启时 token 为空则自动生成）
//
// 响应统一回最新状态，调用方不必再 GET 一次。
func (s *Server) handleRemoteAccess(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "远程访问管理仅限本机", http.StatusForbidden)
		return
	}
	s.remoteMu.Lock()
	path := s.remotePath
	s.remoteMu.Unlock()
	if path == "" {
		http.Error(w, "远程访问未装配", http.StatusNotImplemented)
		return
	}
	ra := config.LoadRemoteAccess(path)
	if r.Method == http.MethodPost {
		var body struct {
			Enabled *bool `json:"enabled"`
			Rotate  bool  `json:"rotate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "请求体不是合法 JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.remoteMu.Lock()
		defer s.remoteMu.Unlock()
		ra = config.LoadRemoteAccess(path)
		if body.Enabled != nil {
			ra.Enabled = *body.Enabled
		}
		if body.Rotate || (ra.Enabled && ra.Token == "") {
			token, err := config.GenerateRemoteToken()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ra.Token = token
		}
		if err := config.SaveRemoteAccess(path, ra); err != nil {
			http.Error(w, "保存远程访问配置失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ra)
}
