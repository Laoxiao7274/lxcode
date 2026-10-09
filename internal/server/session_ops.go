// 会话的建/查/发：运行时生命周期 + 历史回放 + 发送参数解析。
package server

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/llm"
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

// peekSession 只看内存里已存在的运行时，**不创建**（对比 session：后者会为未打开的
// 会话建运行时）。用于纯读场景——如释放工作区时的忙闲判定，不该为此拉起一个运行时。
func (s *Server) peekSession(id string) *agent.Session {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[id]
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
	// 项目会话异步预热 worktree：把「串行 3 个 git 子进程建工作树」（首条消息
	// ~200ms 起、冷态秒级）挪出用户等待路径——session.new 到首条发送之间有几秒
	// 间隙，正好用它把工作树建好，首条 chat.send 命中快路径 ~0ms。
	// 预热纯异步：session.new 的响应不等它；失败只记日志（fail-open），send 时
	// 会按既有行为再试。
	if workspace != "" {
		s.preheatWorktree(id, sess)
	}
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

// sendAttachments 是 chat.send 的统一发送路径（带图 / 带文件 / 纯文本共用）：
// 带图时先做 vision 能力校验、把图片落盘成附件，再把**文件引用**经 WithImages
// 传给内核；带文件时走 SaveFileAttachment 落盘（无 mime 白名单），并把
// 「[附件] 名字 → attachments/...」行追加进消息文本——Agent 用 read_file 读。
// base64 在这里读完参数即弃（落盘的是原始字节），不进历史、不进事件。
func (s *Server) sendAttachments(sessionID string, sess *agent.Session, p protocol.ChatSendParams, decodedImgs, decodedFiles [][]byte, fileNames []string) error {
	var refs []llm.ImageRef
	if len(p.Images) > 0 {
		// vision 能力校验：解析「这轮实际会用的模型」——与 Send 同一条解析路径
		//（Agent 绑定优先，回落 default 角色，agentID 语义一致），保证校验的
		// 模型就是这轮要用的模型。没声明 vision 就拒绝：不静默把图发给不识图
		// 的模型（严格端点会 400 拒收整轮，宽容端点会无视图片——两种都坏）。
		m, err := sess.ModelFor(p.Agent)
		if err != nil {
			return err
		}
		if !m.Capabilities.Vision {
			return fmt.Errorf("当前模型不支持视觉（%s 未声明 vision 能力）：先在设置里为模型勾选「视觉」能力，或改用不带图片的消息", m.ID)
		}
		if s.st == nil {
			return errors.New("会话存储未启用，无法保存图片附件")
		}
		if refs, err = s.saveChatImages(sessionID, p.Images, decodedImgs); err != nil {
			return err
		}
	}
	// 文件附件：不进视觉通道（vision 校验只看图片），纯落盘 + 文本行。
	var fileLines []string
	if len(p.Files) > 0 {
		if s.st == nil {
			return errors.New("会话存储未启用，无法保存文件附件")
		}
		rels, err := s.saveChatFiles(sessionID, decodedFiles, fileNames)
		if err != nil {
			return err
		}
		for i, rel := range rels {
			// rel = <sessionID>/<落盘文件名>（SaveFileAttachment 的返回口径）
			fileLines = append(fileLines, fmt.Sprintf("[附件] %s → attachments/%s", fileNames[i], rel))
		}
	}
	text := p.Text
	if len(fileLines) > 0 {
		if strings.TrimSpace(text) == "" {
			text = strings.Join(fileLines, "\n")
		} else {
			text += "\n\n" + strings.Join(fileLines, "\n")
		}
	}
	opts := chatSendOptions(p)
	if len(refs) > 0 {
		opts = append(opts, agent.WithImages(refs))
	}
	return s.sendSession(sessionID, sess, text, opts...)
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
		// 整段会话统计（与 chat.done 的 stats 同一份口径、同一个来源）：
		// 回放与实时必须是同一组数字——各算一遍就会分叉（本仓库的老坑）
		Stats: s.sessionStatsOf(sessionID),
		// 会话实际用的模型（子会话 = 它自己 Agent 的）：解析不出来时是空串 →
		// wire 上整键缺席，前端显示中性态（不编一个模型名）。
		Model: sess.ModelID(),
		// 会话**此刻**的权限档（子会话 = 取严(父实时, 自身默认)）：切会话时前端
		// 按它同步显示——档位显示必须跟随后端事实，而不是上一会话的本地残留。
		Approval: sess.LiveApproval(),
	}
}
