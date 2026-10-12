// behavior.go —— 行为开关（config/behavior.json）：todo-first 提示词档位的
// 读取 / 设置 / 变更广播。与 remote.json 同一条「现读」模式：文件只有几十字节，
// 服务端每次 chat.send 现读——手改或 set 后**下一轮**立即生效，不需要 reloadLoop
// 替它做状态替换；reloadLoop 只负责「手改后给其它客户端广播一次变更」。
package server

import (
	"encoding/json"
	"errors"

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

// defaultApprovalResult 生成 get 应答 / changed 广播的载荷（档位已归一，恒非空）。
func (s *Server) defaultApprovalResult() protocol.DefaultApprovalResult {
	return protocol.DefaultApprovalResult{Approval: s.behaviorConfig().Approval}
}

// BehaviorTodoPrompt 返回当前档位（启动日志用）。
func (s *Server) BehaviorTodoPrompt() string { return s.behaviorConfig().TodoPrompt }

// BehaviorDefaultApproval 返回当前新会话默认审批档（启动日志用）。
func (s *Server) BehaviorDefaultApproval() string { return s.behaviorConfig().Approval }

// broadcastTodoPrompt / broadcastDefaultApproval 通知全部客户端刷新对应设置显示
//（set 后 / reloadLoop 检出手改后）。分字段广播而不是一个事件带全部字段：
// 事件载荷即 get 应答（一份形状），消费方按事件各取所需，不因无关字段变化重渲染。
func (s *Server) broadcastTodoPrompt() {
	s.broadcast(protocol.EventTodoPromptChanged, s.todoPromptResult())
}

func (s *Server) broadcastDefaultApproval() {
	s.broadcast(protocol.EventDefaultApprovalChanged, s.defaultApprovalResult())
}

// broadcastBehaviorChanges 按字段粒度广播变更（reloadLoop 巡检 / set 共用）。
// set 路径自己已广播并同步 behaviorLast——这里只在巡检路径调用。
func (s *Server) broadcastBehaviorChanges(before, after config.Behavior) {
	if before.TodoPrompt != after.TodoPrompt {
		s.broadcastTodoPrompt()
	}
	if before.Approval != after.Approval {
		s.broadcastDefaultApproval()
	}
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
	before := s.behaviorLast
	changed := now != before
	if changed {
		s.behaviorLast = now
	}
	s.behaviorMu.Unlock()
	if changed {
		s.broadcastBehaviorChanges(before, now)
	}
}

// saveBehavior 写回 behavior.json（保留**未在本次修改**的字段——磁盘上两个
// 字段并存，set 只改一个时把另一个一起归零是数据丢失），同步热加载比对基线
//（否则下一次 reloadLoop 巡检会把它当「手改」再广播一遍），并按字段粒度广播。
func (s *Server) saveBehavior(b config.Behavior) error {
	s.behaviorMu.Lock()
	path := s.behaviorPath
	s.behaviorMu.Unlock()
	if path == "" {
		return errors.New("行为配置未装配")
	}
	if err := config.SaveBehavior(path, b); err != nil {
		return err
	}
	s.behaviorMu.Lock()
	s.behaviorLast = config.LoadBehavior(path)
	s.behaviorMu.Unlock()
	return nil
}

// handleBehavior 分发 settings.todoPrompt.* 与 settings.defaultApproval.*；
// 返回 nil 表示方法不在此域。
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
		// 只改 todoPrompt，approval 原样保留（Normalized 会补各自缺省）。
		cur := s.behaviorConfig()
		if err := s.saveBehavior(config.Behavior{TodoPrompt: p.TodoPrompt, Approval: cur.Approval}); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "保存行为配置失败: "+err.Error())
		}
		s.broadcastTodoPrompt()
		return protocol.NewResult(req.ID, s.todoPromptResult())

	case protocol.MethodDefaultApprovalGet:
		return protocol.NewResult(req.ID, s.defaultApprovalResult())

	case protocol.MethodDefaultApprovalSet:
		var p protocol.DefaultApprovalSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if !config.ValidApprovalMode(p.Approval) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"approval 必须是 auto/confirm/strict 之一")
		}
		// 只改 approval，todoPrompt 原样保留。
		cur := s.behaviorConfig()
		if err := s.saveBehavior(config.Behavior{TodoPrompt: cur.TodoPrompt, Approval: p.Approval}); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "保存行为配置失败: "+err.Error())
		}
		s.broadcastDefaultApproval()
		return protocol.NewResult(req.ID, s.defaultApprovalResult())
	}
	return nil
}
