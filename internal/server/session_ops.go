// 会话的建/查/发：运行时生命周期 + 历史回放 + 发送参数解析。
package server

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) session(id string) (*agent.Session, error) {
	if id == "" {
		return nil, errors.New("session_id 不能为空")
	}
	s.sessionsMu.RLock()
	sess := s.sessions[id]
	s.sessionsMu.RUnlock()
	if sess != nil {
		return sess, nil
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if sess = s.sessions[id]; sess != nil {
		return sess, nil
	}
	if s.st == nil {
		return nil, errors.New("会话存储未启用")
	}
	var err error
	sess, err = s.newRuntime(id)
	if err != nil {
		return nil, err
	}
	s.sessions[id] = sess
	s.lastSessionID = id
	return sess, nil
}

func (s *Server) createSession(workspace string) (string, *agent.Session, error) {
	if s.st == nil {
		if workspace != "" {
			return "", nil, errors.New("项目会话需要持久化存储")
		}
		id := fmt.Sprintf("memory-%d", atomic.AddUint64(&s.memoryID, 1))
		sess, err := s.newRuntime(id)
		if err != nil {
			return "", nil, err
		}
		s.sessionsMu.Lock()
		s.sessions[id], s.lastSessionID = sess, id
		s.sessionsMu.Unlock()
		return id, sess, nil
	}
	if workspace != "" {
		meta, found, err := s.st.ProjectByID(workspace)
		if err != nil {
			return "", nil, err
		}
		if !found {
			return "", nil, fmt.Errorf("项目 %s 不存在", workspace)
		}
		if _, err := project.ValidateDirectory(meta.Path); err != nil {
			return "", nil, err
		}
	}
	id, err := s.st.Create()
	if err != nil {
		return "", nil, err
	}
	if workspace != "" {
		if err := s.st.SessionWorkspace(id, workspace); err != nil {
			return "", nil, err
		}
	}
	sess, err := s.newRuntime(id)
	if err != nil {
		return "", nil, err
	}
	s.sessionsMu.Lock()
	s.sessions[id], s.lastSessionID = sess, id
	s.sessionsMu.Unlock()
	return id, sess, nil
}

func (s *Server) sendSession(sessionID string, sess *agent.Session, text string, opts ...agent.SendOpt) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.prepareWorktreeLocked(sessionID, sess); err != nil {
		return err
	}
	return sess.Send(text, opts...)
}

func chatSendOptions(p protocol.ChatSendParams) []agent.SendOpt {
	var opts []agent.SendOpt
	if p.Effort != "" {
		opts = append(opts, agent.WithEffort(p.Effort))
	}
	if p.Approval != "" {
		opts = append(opts, agent.WithApproval(p.Approval))
	}
	if p.Agent != "" {
		opts = append(opts, agent.WithAgent(p.Agent))
	}
	return opts
}

func errorCode(err error) int {
	switch {
	case errors.Is(err, agent.ErrBusy):
		return protocol.CodeBusy
	case errors.Is(err, agent.ErrNoDefaultModel):
		return protocol.CodeNoDefaultModel
	case errors.Is(err, agent.ErrModelDisabled):
		return protocol.CodeModelDisabled
	case errors.Is(err, agent.ErrPendingConfirm):
		return protocol.CodeBusy // 挂起确认占着会话，语义同忙
	case errors.Is(err, agent.ErrAgentNotFound):
		return protocol.CodeAgentNotFound
	case errors.Is(err, agent.ErrAgentDisabled):
		return protocol.CodeAgentDisabled
	default:
		return protocol.CodeInvalidParams
	}
}

func (s *Server) history(sessionID string, sess *agent.Session) protocol.ChatHistoryResult {
	snap := sess.History()
	var pending *protocol.ConfirmRequest
	if snap.Pending != nil {
		pending = toProtocolConfirm(sessionID, snap.Pending)
	}
	return protocol.ChatHistoryResult{
		Messages: snap.Messages, Busy: snap.Busy, Pending: pending,
		SessionID: sessionID, Todos: snap.Todos,
		Context:     toProtocolContext(snap.Context),
		Checkpoints: snap.Checkpoints,
		// 会话实际用的模型（子会话 = 它自己 Agent 的）：解析不出来时是空串 →
		// wire 上整键缺席，前端显示中性态（不编一个模型名）。
		Model: sess.ModelID(),
	}
}
