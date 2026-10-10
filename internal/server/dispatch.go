// 方法分派：按方法名前缀依次问各域处理器，命中即处理。
// 纪律（§5 坑 14）：每个域处理器**先判「是不是我的方法」，不是就返回 nil 让给下一个**，
// 再做自己的前置校验——把前置校验写在方法匹配之前会吞掉它之后的所有方法域。
//
// 拆成域函数之前这里是一个 354 行的 switch；现在 dispatch 只剩这条链（见 dispatch_*.go）。
package server

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) dispatch(c *wsClient, req *protocol.Request) *protocol.Response {
	params := req.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	// 通知（无 id）不回应答
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	// hello 内联而不进域处理器：它是唯一「命中后可能合法地返回 nil」的方法
	// （通知形态不回响应），与「nil = 不是我的方法」的回落约定撞形。
	if req.Method == protocol.MethodHello {
		return s.handleHello(req, params, isNotification)
	}
	// connection.ping 心跳：同 hello 一样「通知形态合法地返回 nil」（无 id 不回
	// 应答），所以也内联在域处理器链之前。纯内存应答，供客户端判定连接活性。
	if req.Method == protocol.MethodPing {
		if isNotification {
			return nil
		}
		return protocol.NewResult(req.ID, map[string]any{"pong": true})
	}
	if resp := s.dispatchModels(req, params); resp != nil {
		return resp
	}
	if resp := s.dispatchChat(c, req, params); resp != nil {
		return resp
	}
	if resp := s.dispatchSessions(c, req, params); resp != nil {
		return resp
	}
	if resp := s.dispatchProjects(req, params); resp != nil {
		return resp
	}

	// git.overview / git.diff（Git 管理页只读查询——独立分发函数，未命中回落 unknown）
	if resp := s.dispatchGit(c, req, params); resp != nil {
		return resp
	}

	// agent.*/catalog.*（M1——独立分发函数，未命中回落 unknown）
	if resp := s.dispatchAgentCatalog(req.ID, req.Method, params); resp != nil {
		return resp
	}

	// search.*（M4 网页搜索渠道——独立分发函数，未命中回落 unknown）
	if resp := s.handleSearch(req, params); resp != nil {
		return resp
	}

	// job.*（后台任务——独立分发函数，未命中回落 unknown）
	if resp := s.dispatchJobs(req, params); resp != nil {
		return resp
	}

	return protocol.NewError(req.ID, protocol.CodeMethodNotFound, "未知方法: "+req.Method)
}
