// 会话的建/查/发：运行时生命周期 + 历史回放 + 发送参数解析。
package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
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
	// 子会话（parent_id 非空）**现建现用、不进运行时缓存**（2026-12 用户报：关闭
	// 子会话视图再重开，历史丢了）：子会话运行时的生命周期跟着派发走——
	// openChildSession 每次 New + AttachTo，从不查 s.sessions；这里为 chat.history
	// 现建的那份一旦缓存，就成了 AttachTo 时刻的**静态快照**（收不到事件、不再
	// 追加），之后每次按 id 读历史都命中陈旧缓存——重开视图看到的是第一次打开
	// 那一刻的历史，之后的轮次全部「丢」。所以子会话每次重新 AttachTo（读库即
	// 最新状态），不注册。会话不在库里（内存会话 id / 坏 id）时按原路径走——
	// newRuntime 的 AttachTo 会报出准确的错误。
	if s.st != nil {
		if parent, err := s.st.SessionParentID(id); err == nil && parent != "" {
			return s.newRuntime(id)
		}
	}
	s.sessionsMu.Lock()
	if sess = s.sessions[id]; sess != nil {
		return sess, nil
	}
	if s.st == nil {
		s.sessionsMu.Unlock()
		return nil, errors.New("会话存储未启用")
	}
	// **慢操作必须移出全局锁**（2026-12 性能审计钉子
	// TestConcurrentResumeOfBigSessionsDoesNotSerialize）：newRuntime 含全量历史
	// 加载（AttachTo → Load）+ 工作区解析 + 占用/审批档回读，大会话是几十毫秒到
	// 秒级——原来在 sessionsMu 写锁内执行，一个会话的慢加载把所有其他会话的
	// resume/send/history 串成一列，前端 10s 超时（「恢复会话失败: 请求超时」）。
	// 构建放在锁外：并发对手同 id 同建时，后注册者发现已有即弃置自己那份
	//（弃置的运行时从未 Send，无落库、无事件，纯垃圾可回收）。
	s.sessionsMu.Unlock()
	sess, err := s.newRuntime(id)
	if err != nil {
		return nil, err
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	// 并发对手可能已先注册（同一 id 双建）：用它的，弃置自己这份。
	if existing := s.sessions[id]; existing != nil {
		return existing, nil
	}
	s.sessions[id] = sess
	s.lastSessionID = id
	return sess, nil
}

// peekSession 只看内存里已存在的运行时，**不创建**（对比 session：后者会为未打开的
// 会话建运行时）。用于纯读场景——不该为此拉起一个运行时。
func (s *Server) peekSession(id string) *agent.Session {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[id]
}

// cancelChildAnywhere 在当前活跃的会话运行时里找一个「正在跑的子会话」并取消它，
// 返回是否命中。子会话运行时不注册进 s.sessions（生命周期跟着派发走，跑完即注销
// ——agent.Session.registerChild/unregisterChild），对子会话 id 直接 s.session(id)
// 会 newRuntime 建一个空对象、Cancel 是空操作——停不掉真正在跑的子轮。所以
// chat.cancel 先在这里找：命中即真停；未命中（普通会话 / 子会话已结束 / 重启恢复）
// 由调用方走原路径，行为与现在一致。
func (s *Server) cancelChildAnywhere(childID string) bool {
	s.sessionsMu.RLock()
	sessions := make([]*agent.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessionsMu.RUnlock()
	for _, sess := range sessions {
		if sess.CancelChild(childID) {
			return true
		}
	}
	return false
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
	// 新会话的初始审批档 = behavior 默认档（不是写死 confirm）：设置面板的
	// 「新会话默认审批档」改的是这个值。SetApproval 会把它落库——重启后恢复
	// 同一条会话读的就是这个列，不依赖「创建时的默认档」还成立。
	sess.SetApproval(s.behaviorConfig().Approval)
	s.sessionsMu.Lock()
	s.sessions[id], s.lastSessionID = sess, id
	s.sessionsMu.Unlock()
	// 直连模式（2026-10 架构修订）：主检出路径恒定、准备只剩 os.Stat 与一次性的
	// 基线记录——旧的 worktree 异步预热没有意义，已随 per-session worktree 一起退役。
	return id, sess, nil
}

func (s *Server) sendSession(sessionID string, sess *agent.Session, text string, opts ...agent.SendOpt) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.prepareWorkspaceLocked(sessionID, sess); err != nil {
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
//
// 落盘的每一份都同时生成**展示元数据**（llm.AttachmentRef——前端渲染附件卡），
// 经 WithAttachments 随 user 消息落库与广播：实时事件与 chat.history 回放
// 是同一份，两条路径渲染同一张卡。
func (s *Server) sendAttachments(sessionID string, sess *agent.Session, p protocol.ChatSendParams, decodedImgs, decodedFiles [][]byte, fileNames, fileRelPaths []string) error {
	var refs []llm.ImageRef
	var atts []llm.AttachmentRef
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
		for i, im := range p.Images {
			// path 形态没带展示名 → 回落路径 basename（与 base64 形态的「图片」
			// 兜底同一层净化，名字只做展示，永不成为磁盘文件名）。
			name := im.Name
			if name == "" && im.Path != "" {
				name = filepath.Base(im.Path)
			}
			atts = append(atts, llm.AttachmentRef{
				Path: refs[i].Path,
				Name: attachmentDisplayName(name, "图片"),
				Size: int64(len(decodedImgs[i])),
				Mime: im.Mime,
				Kind: llm.AttachmentKindImage,
			})
		}
	}
	// 文件附件：不进视觉通道（vision 校验只看图片），纯落盘 + 文本行。
	// 展示名 = 文件夹相对路径优先（拖文件夹的内部文件按 `docs/api/auth.md`
	// 呈现——用户要的「内部应该是地址」），散文件回落净化后的原名。落盘名
	// 恒为随机 hex，展示名/相对路径与磁盘名解耦。
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
			display := fileNames[i]
			if fileRelPaths[i] != "" {
				display = fileRelPaths[i]
			}
			fileLines = append(fileLines, fmt.Sprintf("[附件] %s → attachments/%s", display, rel))
			atts = append(atts, llm.AttachmentRef{
				Path: rel,
				Name: display,
				Size: int64(len(decodedFiles[i])),
				Kind: llm.AttachmentKindFile,
			})
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
	// todo-first 档位每次现读 behavior.json（文件只有几十字节）：设置里切换或
	// 手改后**下一轮**立即生效——不需要等热加载巡检，也不需要重开会话。
	opts = append(opts, agent.WithTodoPrompt(s.behaviorConfig().TodoPrompt))
	if len(refs) > 0 {
		opts = append(opts, agent.WithImages(refs))
	}
	if len(atts) > 0 {
		opts = append(opts, agent.WithAttachments(atts))
	}
	return s.sendSession(sessionID, sess, text, opts...)
}

// attachmentDisplayName 是图片附件的展示名兜底：客户端给了就用（净化——名字
// 只做展示，永不成为磁盘文件名），没给（老客户端）回落一个中性名——附件卡
// 不显示"（空）"这种假缺陷。
func attachmentDisplayName(name, fallback string) string {
	if n := store.SanitizeFileName(name); n != "" && n != "file" {
		return n
	}
	return fallback
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

// history 组装 chat.history 应答。两种窗口（互斥，before_seq > 0 时优先）：
//   - before_seq > 0：前缀分页窗口——anchor（before_seq）之前最多 tail 条
//     （内核 HistoryBefore 负责配平回退与检查点平移），应答带 has_more /
//     first_seq 分页元数据；tail ≤ 0 报参数错误（分页必须有窗口大小）；
//   - 否则 tail > 0 = 尾部窗口（慢链路分页：内核 TailHistory 负责配平与
//     检查点平移）；0 = 全量，老语义不变。
func (s *Server) history(sessionID string, sess *agent.Session, tail int, beforeSeq int64) (protocol.ChatHistoryResult, error) {
	if beforeSeq > 0 {
		snap, hasMore, firstSeq, err := sess.HistoryBefore(beforeSeq, tail)
		if err != nil {
			// 错误直接上抛（dispatch 层映射 CodeInvalidParams）：before_seq
			// 是客户端记录的 seq，错误值会切出重复或空洞的窗口——静默回落
			// 全量只会把错位藏起来（安卓端的「重试」依赖它收到这个错误）。
			return protocol.ChatHistoryResult{}, err
		}
		return s.historyResult(sessionID, sess, snap, &hasMore, &firstSeq), nil
	}
	if tail <= 0 {
		return s.historyResult(sessionID, sess, sess.History(), nil, nil), nil
	}
	snap := sess.TailHistory(tail)
	return s.historyResult(sessionID, sess, snap, nil, nil), nil
}

// historyResult 把快照装成 wire 应答（三种窗口共用：尾部 / 前缀 / 全量）。
// hasMore/firstSeq 非空 = 分页应答（wire 带这两个键）；nil = 非分页（缺席）。
func (s *Server) historyResult(sessionID string, sess *agent.Session, snap agent.Snapshot, hasMore *bool, firstSeq *int64) protocol.ChatHistoryResult {
	var pending *protocol.ConfirmRequest
	if snap.Pending != nil {
		pending = toProtocolConfirm(sessionID, snap.Pending)
	}
	res := protocol.ChatHistoryResult{
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
	if hasMore != nil {
		res.HasMore = *hasMore
	}
	if firstSeq != nil {
		res.FirstSeq = *firstSeq
	}
	return res
}
