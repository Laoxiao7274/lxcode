// behavior.go —— 行为开关（config/behavior.json）：todo-first 提示词档位的
// 读取 / 设置 / 变更广播。与 remote.json 同一条「现读」模式：文件只有几十字节，
// 服务端每次 chat.send 现读——手改或 set 后**下一轮**立即生效，不需要 reloadLoop
// 替它做状态替换；reloadLoop 只负责「手改后给其它客户端广播一次变更」。
package server

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// AttachBehavior 装配行为配置路径（config/behavior.json，与 models.json 同目录；
// 未装配 = 恒默认档——与 remote.json 未装配时 Enabled=false 同一条降级语义）。
func (s *Server) AttachBehavior(path string) {
	s.behaviorMu.Lock()
	s.behaviorPath = path
	s.behaviorLast = config.LoadBehavior(path)
	s.behaviorMu.Unlock()
}

// behaviorConfig 现读行为配置（每次调用读一次文件——文件极小，开销可忽略；
// 未装配 = 默认档）。
func (s *Server) behaviorConfig() config.Behavior {
	s.behaviorMu.Lock()
	path := s.behaviorPath
	s.behaviorMu.Unlock()
	if path == "" {
		return config.Behavior{}
	}
	return config.LoadBehavior(path)
}

// todoPromptResult 生成 get 应答 / changed 广播的载荷（档位已归一，恒非空）。
func (s *Server) todoPromptResult() protocol.TodoPromptResult {
	return protocol.TodoPromptResult{TodoPrompt: s.behaviorConfig().TodoPrompt}
}

// BehaviorTodoPrompt 返回当前档位（启动日志用）。
func (s *Server) BehaviorTodoPrompt() string { return s.behaviorConfig().TodoPrompt }

// broadcastBehavior 通知全部客户端刷新档位显示（set 后 / reloadLoop 检出手改后）。
func (s *Server) broadcastBehavior() {
	s.broadcast(protocol.EventTodoPromptChanged, s.todoPromptResult())
}

// CheckBehaviorChanged 供热加载周期调用：手改 behavior.json 后把变更广播给
// 其它客户端（set 路径自己已广播并同步 behaviorLast，这里不会重复）。
func (s *Server) CheckBehaviorChanged() {
	s.behaviorMu.Lock()
	path := s.behaviorPath
	if path == "" {
		s.behaviorMu.Unlock()
		return
	}
	now := config.LoadBehavior(path)
	changed := now != s.behaviorLast
	if changed {
		s.behaviorLast = now
	}
	s.behaviorMu.Unlock()
	if changed {
		s.broadcastBehavior()
	}
}

// handleBehavior 分发 settings.todoPrompt.*；返回 nil 表示方法不在此域。
func (s *Server) handleBehavior(req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodTodoPromptGet:
		return protocol.NewResult(req.ID, s.todoPromptResult())

	case protocol.MethodTodoPromptSet:
		var p protocol.TodoPromptSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if !config.ValidTodoPromptMode(p.TodoPrompt) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"todoPrompt 必须是 strict/normal/off 之一")
		}
		s.behaviorMu.Lock()
		path := s.behaviorPath
		s.behaviorMu.Unlock()
		if path == "" {
			return protocol.NewError(req.ID, protocol.CodeInternal, "行为配置未装配")
		}
		if err := config.SaveBehavior(path, config.Behavior{TodoPrompt: p.TodoPrompt}); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "保存行为配置失败: "+err.Error())
		}
		// 同步热加载比对基线（否则下一次 reloadLoop 巡检会把它当「手改」再广播一遍），
		// 然后广播（含发请求的这个客户端——多端显示一致）。
		s.behaviorMu.Lock()
		s.behaviorLast = config.LoadBehavior(path)
		s.behaviorMu.Unlock()
		s.broadcastBehavior()
		return protocol.NewResult(req.ID, s.todoPromptResult())
	}
	return nil
}
