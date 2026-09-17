// Package agent 实现会话运行时：内存历史 + agent 工具循环 + 确认门 +
// todo 状态。这是内核的核心循环——上层（CLI、桌面壳）通过 Send 发起一轮，
// 经 Emitter 收到类型化事件流（流式增量、工具调用、确认请求……），
// 经 Confirm 裁决高危操作。内核不依赖任何 UI 框架与网络协议。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

const (
	// maxToolRounds 单次用户消息的工具循环轮数上限——防小模型工具死循环。
	maxToolRounds = 16
	// maxToolResultBytes 工具结果进入对话历史的上限（执行层 32KB 全文上限，
	// 历史层再收口——上下文有限）。编程 agent 的轮数上限比设备 agent 放宽
	// （8→16）：真实编码任务里"读→搜→改→验证"链路常态就是十几个来回。
	maxToolResultBytes = 8 * 1024
)

// StreamFn 是 LLM 调用的抽象缝（单测注入假实现，不碰网络）。
type StreamFn func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error)

// 哨兵错误：服务端按类别映射协议错误码（errors.Is 判断，不做字符串匹配）。
var (
	// ErrBusy 会话正在生成中（Send/切换会话被拒）。
	ErrBusy = errors.New("会话正在生成中")
	// ErrPendingConfirm 有挂起的确认未处理（切换会话被拒）。
	ErrPendingConfirm = errors.New("有挂起的确认，先处理确认再切换会话")
	// ErrNoDefaultModel default 角色未绑定模型。
	ErrNoDefaultModel = errors.New("default 模型未配置")
	// ErrModelDisabled default 模型已停用。
	ErrModelDisabled = errors.New("default 模型已停用")
)

// Emitter 是事件出口：内核把类型化事件推给宿主（CLI 打印 / 壳渲染）。
type Emitter func(ev Event)

// Session 是单会话 agent 运行时：内存历史 + 串行工具循环 + 确认门 + todo。
// 会话级别的并发语义：同一时刻只有一轮生成（busy），Send 忙时拒绝。
type Session struct {
	reg    *config.Registry
	tools  *tools.Registry
	stream StreamFn
	emit   Emitter

	mu      sync.Mutex
	history []llm.Message
	busy    bool
	cancel  context.CancelFunc
	pending *ConfirmRequest
	confirm chan bool
	todos   []tools.TodoItem

	// 持久化（st 为 nil = 纯内存模式，兼容不接存储的调用方/单测）。
	// SQLite 版无句柄概念：会话 = sessions 表一行，按 s.id 追加写。
	st Persistence
	id string // 当前会话 id（空 = 尚未创建行）
	// pendingWorkspace 是「下一个新会话」的归属项目（session.new 时设置，
	// 会话行首条消息懒建时落库并清空）。单会话架构下的过渡设计——
	// 多会话并发时归属直接挂在会话对象上。
	pendingWorkspace string
	// workDir 是当前会话的工作目录（归属项目的根目录；空 = 后端进程
	// 目录）。工具的相对路径、bash 默认目录与系统提示词的工作目录说明
	// 都以它为准——每轮开始时快照进 ctx（tools.WithWorkDir）。
	workDir string
}

// New 创建会话；emit 为 nil 时事件被丢弃（单测可只调方法）。
func New(reg *config.Registry, toolReg *tools.Registry, emit Emitter) *Session {
	if emit == nil {
		emit = func(Event) {}
	}
	s := &Session{reg: reg, tools: toolReg, stream: streamWithLLM, emit: emit}
	// todo 工具写清单时回写会话状态并广播（UI 的 TodoList 数据源）
	toolReg.SetTodoSink(func(items []tools.TodoItem) {
		s.mu.Lock()
		s.todos = items
		s.mu.Unlock()
		s.emit(TodoUpdatedEvent{Items: items})
	})
	return s
}

// SetEmitter 替换事件出口（宿主构造晚于 Session 时接线用）。
// 并发安全：emit 只在事件 goroutine 里读——替换发生在任何 Send 之前是
// 期望用法；startEvents 的锁由宿主自己保证。
func (s *Session) SetEmitter(emit Emitter) {
	if emit == nil {
		emit = func(Event) {}
	}
	s.mu.Lock()
	s.emit = emit
	s.mu.Unlock()
}

// SetStream 替换 LLM 调用实现（测试注入假实现，不碰网络；须在 Send 前调用）。
func (s *Session) SetStream(fn StreamFn) {
	s.mu.Lock()
	s.stream = fn
	s.mu.Unlock()
}

// streamWithLLM 默认 LLM 调用：按 default 角色配置建客户端，经 ChatAuto
// （anthropic 永远流式；openai 带工具走非流式回放，见 llm.ChatAuto 注释——
// 该策略来自真机端点实测：部分 openai 兼容端点的流式会丢 tool_calls）。
func streamWithLLM(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
	client, err := llm.New(llm.Config{
		BaseURL: m.BaseURL,
		APIKey:  m.APIKey,
		Model:   m.Model,
		Format:  m.EffectiveFormat(),
	})
	if err != nil {
		return nil, err
	}
	return client.ChatAuto(ctx, msgs, opts...)
}

// Send 发起一轮对话（异步）：校验模型 → 入历史 → 后台跑工具循环。
// 忙时返回 ErrBusy；模型未绑定/停用返回对应哨兵（服务端按类别映射错误码）。
func (s *Session) Send(text string) error {
	if text == "" {
		return errors.New("text 不能为空")
	}
	m, err := s.reg.ModelForRole(config.RoleDefault)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoDefaultModel, err)
	}
	if !m.Enabled {
		return fmt.Errorf("%w: %s", ErrModelDisabled, m.ID)
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	userMsg := llm.Message{Role: "user", Content: text}
	s.history = append(s.history, userMsg)
	s.persistLocked(userMsg)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	s.emit(UserMsgEvent{Message: userMsg})
	s.emit(BusyEvent{Busy: true})
	go s.runTurn(ctx)
	return nil
}

// Cancel 取消当前生成（aborted 语义：已生成部分保留入历史）。
func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Busy 返回当前忙闲状态。
func (s *Session) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// History 返回会话快照（消息 + 忙闲 + 挂起的确认 + todo），供宿主初始化视图。
func (s *Session) History() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := append([]llm.Message(nil), s.history...)
	var pending *ConfirmRequest
	if s.pending != nil {
		p := *s.pending
		pending = &p
	}
	return Snapshot{
		Messages: msgs, Busy: s.busy, Pending: pending,
		SessionID: s.id, Todos: append([]tools.TodoItem(nil), s.todos...),
	}
}

// Todos 返回当前任务清单（UI 渲染用）。
func (s *Session) Todos() []tools.TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tools.TodoItem(nil), s.todos...)
}

// runTurn 跑完整一轮：流式生成 → 工具调用 → 确认 → 执行 → 回填续轮。
// 文件改动（edit/write_file）被收集，轮末汇总发 FilesChangedEvent（产物视图）。
func (s *Session) runTurn(ctx context.Context) {
	// 快照工作目录并注入工具执行（busy 期间不可变——切换与归属入口
	// 都有 busy 守卫，这是单会话架构下的一致性来源）：项目会话的相对
	// 路径、bash 默认目录、确认门解析基准全部对齐项目根。
	s.mu.Lock()
	workDir := s.workDir
	s.mu.Unlock()
	ctx = tools.WithWorkDir(ctx, workDir)

	var fileChanges []FileChange
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.cancel = nil
		s.pending = nil
		s.confirm = nil
		s.mu.Unlock()
		s.emit(BusyEvent{Busy: false})
		if len(fileChanges) > 0 {
			s.emit(FilesChangedEvent{Files: fileChanges})
		}
	}()

	for round := 0; round < maxToolRounds; round++ {
		res, err := s.streamRound(ctx, workDir)
		if err != nil {
			aborted := errors.Is(err, context.Canceled) || ctx.Err() != nil
			note := err.Error()
			if aborted {
				note = "已取消（保留已生成部分）"
			}
			// 中断也保留已生成的部分内容：入历史并随事件带给宿主
			var partial *llm.Message
			if res != nil {
				s.append(res.Message)
				m := res.Message
				partial = &m
			}
			s.emit(TurnErrorEvent{Message: note, Aborted: aborted, Partial: partial})
			return
		}
		s.append(res.Message)
		s.emit(TurnDoneEvent{
			Message: res.Message, UsageTokens: res.UsageTokens, FinishReason: res.FinishReason,
		})
		if len(res.Message.ToolCalls) == 0 {
			return
		}
		if !s.runTools(ctx, res.Message.ToolCalls, &fileChanges) {
			return // 取消
		}
	}
	s.emit(TurnErrorEvent{
		Message: fmt.Sprintf("工具循环达上限（%d 轮），已停止", maxToolRounds),
	})
}

// streamRound 跑一轮流式生成，把增量事件转发给宿主，返回最终结果。
// workDir 是本轮快照的会话工作目录（系统提示词里的工作目录说明用它）。
func (s *Session) streamRound(ctx context.Context, workDir string) (*llm.ChatResult, error) {
	m, err := s.reg.ModelForRole(config.RoleDefault)
	if err != nil {
		return nil, err
	}
	var opts []llm.Option
	if m.MaxOutputTokens > 0 {
		opts = append(opts, llm.WithMaxTokens(m.MaxOutputTokens))
	}
	// 快照：快照后新消息（若有）不影响本轮请求
	s.mu.Lock()
	msgs := append([]llm.Message{{Role: "system", Content: BuildSystemPrompt(s.tools, workDir)}}, s.history...)
	s.mu.Unlock()

	opts = append([]llm.Option{llm.WithTools(s.tools.LLMTools())}, opts...)
	ch, err := s.stream(ctx, m, msgs, opts)
	if err != nil {
		return nil, err
	}
	var lastErr error
	var liveContent, liveReasoning strings.Builder
	var liveTools []llm.ToolCall
	for ev := range ch {
		switch ev.Type {
		case llm.EventText:
			liveContent.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "text", Text: ev.TextDelta})
		case llm.EventReasoning:
			liveReasoning.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "reasoning", Text: ev.TextDelta})
		case llm.EventToolCall:
			liveTools = append(liveTools, ev.ToolCall)
			tc := ev.ToolCall
			s.emit(ToolCallEvent{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		case llm.EventDone:
			return ev.Result, nil
		case llm.EventError:
			if ev.Err != nil {
				lastErr = ev.Err
			} else {
				lastErr = errors.New("流式错误")
			}
			if ev.Result != nil {
				return ev.Result, lastErr
			}
			return nil, lastErr
		}
	}
	// 流结束无 done 事件：可能是取消（ctx 已断流）——把已生成的部分作为
	// partial 返回给 runTurn 的错误路径（保留部分内容的语义）。
	if liveContent.Len() > 0 || liveReasoning.Len() > 0 || len(liveTools) > 0 {
		partial := &llm.ChatResult{
			Message: llm.Message{
				Role: "assistant", Content: liveContent.String(),
				ReasoningContent: liveReasoning.String(), ToolCalls: liveTools,
			},
		}
		if lastErr == nil {
			lastErr = errors.New("流意外结束（无 done 事件）")
		}
		return partial, lastErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("流意外结束（无 done 事件）")
}

// runTools 执行本轮工具调用（高危先确认）；返回 false 表示被取消。
// fileChanges 收集文件改动（edit/write_file）供轮末产物汇总。
func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall, fileChanges *[]FileChange) bool {
	for _, tc := range calls {
		if ctx.Err() != nil {
			return false
		}
		if prompt := s.tools.Confirm(ctx, tc); prompt != "" {
			req := &ConfirmRequest{
				ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, Prompt: prompt,
			}
			allow, ok := s.awaitConfirm(ctx, req)
			if !ok {
				return false // 取消
			}
			if !allow {
				s.append(llm.Message{Role: "tool", ToolCallID: tc.ID,
					Content: "用户拒绝了这次工具调用（未执行）。请改用其他方式完成任务，或向用户说明需要该操作的原因。"})
				s.emit(ToolResultEvent{
					ID: tc.ID, Name: tc.Function.Name, Content: "用户拒绝执行", IsError: true,
				})
				continue
			}
		}
		result := s.tools.Execute(ctx, tc)
		collectFileChange(fileChanges, tc, result)
		if r := []rune(result); len(r) > maxToolResultBytes {
			result = string(r[:maxToolResultBytes]) +
				fmt.Sprintf("\n…（结果过长已截断，全文共 %d 字符）", len(r))
		}
		s.append(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
		s.emit(ToolResultEvent{ID: tc.ID, Name: tc.Function.Name, Content: result})
	}
	return true
}

// collectFileChange 从 edit/write_file 调用提取改动摘要（同文件多次改动
// 逐步合并——产物卡按文件聚合，行数累计，diff 追加最新块）。
func collectFileChange(out *[]FileChange, tc llm.ToolCall, _ string) {
	var path, oldStr, newStr string
	switch tc.Function.Name {
	case "edit":
		var a struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, oldStr, newStr = a.Path, a.OldString, a.NewString
	case "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, newStr = a.Path, a.Content
	default:
		return
	}
	added, deleted := 0, 0
	if oldStr != "" {
		deleted = len(strings.Split(strings.TrimSuffix(oldStr, "\n"), "\n"))
	}
	if newStr != "" {
		added = len(strings.Split(strings.TrimSuffix(newStr, "\n"), "\n"))
	}
	var diff strings.Builder
	diff.WriteString("@@ " + path + "\n")
	for _, l := range strings.Split(oldStr, "\n") {
		diff.WriteString("-" + l + "\n")
	}
	for _, l := range strings.Split(newStr, "\n") {
		diff.WriteString("+" + l + "\n")
	}
	// 同文件合并：行数累计 + diff 换块
	for i := range *out {
		if (*out)[i].Path == path {
			(*out)[i].Added += added
			(*out)[i].Deleted += deleted
			(*out)[i].Diff += "\n" + diff.String()
			return
		}
	}
	*out = append(*out, FileChange{Path: path, Added: added, Deleted: deleted, Diff: diff.String()})
}

// awaitConfirm 挂起等宿主裁决；取消返回 ok=false。
func (s *Session) awaitConfirm(ctx context.Context, req *ConfirmRequest) (allow bool, ok bool) {
	s.mu.Lock()
	s.pending = req
	s.confirm = make(chan bool, 1)
	ch := s.confirm
	s.mu.Unlock()

	s.emit(ConfirmRequestEvent{Request: req})
	select {
	case a := <-ch:
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return a, true
	case <-ctx.Done():
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return false, false
	}
}

// Confirm 是确认门裁决；id 必须匹配当前挂起的调用。
func (s *Session) Confirm(id string, allow bool) error {
	s.mu.Lock()
	pending := s.pending
	ch := s.confirm
	s.mu.Unlock()
	if pending == nil || ch == nil {
		return errors.New("没有待确认的工具调用")
	}
	if pending.ID != id {
		return fmt.Errorf("确认 id 不匹配（当前挂起: %s）", pending.ID)
	}
	select {
	case ch <- allow:
	default: // 已投递过（重复确认）
	}
	return nil
}

func (s *Session) append(m llm.Message) {
	s.mu.Lock()
	s.history = append(s.history, m)
	s.persistLocked(m)
	s.mu.Unlock()
}

// persistLocked 落盘（调用方持锁；store 挂了才做，失败只记日志不回滚内存——
// 内存才是运行真源，磁盘落后最多丢"最后几条"，比让整轮对话因 IO 抖动失败好）。
func (s *Session) persistLocked(m llm.Message) {
	if s.st == nil {
		return
	}
	if s.ensureSessionLocked() == nil {
		if err := s.st.AppendMsg(s.id, m); err != nil {
			log.Printf("会话落盘失败（继续运行）: %v", err)
		}
	}
}

// ensureSessionLocked 懒创建当前会话行（首条消息才建，避免空会话记录）。调用方持锁。
func (s *Session) ensureSessionLocked() error {
	if s.id != "" || s.st == nil {
		return nil
	}
	id, err := s.st.Create()
	if err != nil {
		return err
	}
	s.id = id
	// 归属项目落库（session.new 时预约的）
	if s.pendingWorkspace != "" {
		if err := s.st.SessionWorkspace(id, s.pendingWorkspace); err != nil {
			log.Printf("会话归属落库失败（继续运行）: %v", err)
		}
		s.pendingWorkspace = ""
	}
	// 通知宿主：会话行已建（侧栏「发消息 → 新对话出现」的信号）
	s.emit(SessionStartedEvent{ID: id})
	return nil
}

// resolveWorkspaceDir 区分未分组与失效项目：只有未分组才允许默认目录。
func resolveWorkspaceDir(st Persistence, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if st == nil {
		return "", errors.New("未启用项目存储")
	}
	meta, found, err := st.ProjectByID(id)
	if err != nil {
		return "", fmt.Errorf("查询项目 %s: %w", id, err)
	}
	if !found {
		return "", fmt.Errorf("项目 %s 不存在", id)
	}
	return project.ValidateDirectory(meta.Path)
}

// WorkDir 返回当前会话的工作目录（空 = 后端进程目录）。
func (s *Session) WorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
}

// EnablePersistence 挂载磁盘存储并恢复最近会话（启动时调用）。
// 库里没有任何会话时保持空历史（全新开始）。幂等：重复调用是 no-op。
func (s *Session) EnablePersistence(st Persistence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		return nil
	}
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
	id, msgs, err := st.Latest()
	if err != nil {
		return err
	}
	var dir string
	if id != "" {
		ws, err := st.WorkspaceOf(id)
		if err != nil {
			return err
		}
		dir, err = resolveWorkspaceDir(st, ws)
		if err != nil {
			return err
		}
	}
	// 全部读取和校验成功后才提交，失败可修复存储后重试。
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	return nil
}

// SessionID 返回当前会话 id（无存储模式为空串）。
func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Close 关闭会话资源（进程退出/测试清理时调用；幂等）。
// SQLite 版句柄概念消失，连接由 store 持有——这里保留空实现维持
// 宿主的 defer Close() 调用面不变。
func (s *Session) Close() {}

// SwitchNew 结束当前会话（记录保留，可后续 resume），从空历史开始。
// 生成中/有挂起确认时拒绝——切换会撕裂运行中的工具循环。
func (s *Session) SwitchNew(workspace string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.switchGuardLocked(); err != nil {
		return "", err
	}
	dir, err := resolveWorkspaceDir(s.st, workspace)
	if err != nil {
		return "", err
	}
	// 历史、todo 和项目归属必须在同一锁内提交，Send 不能插入两步之间。
	s.id, s.history, s.todos = "", nil, nil
	s.pendingWorkspace, s.workDir = workspace, dir
	return "", nil
}

// SwitchTo 恢复指定会话：加载其历史并继续追加。当前会话记录保留。
func (s *Session) SwitchTo(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return errors.New("未启用会话存储")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
	msgs, err := s.st.Load(id)
	if err != nil {
		return err
	}
	ws, err := s.st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(s.st, ws)
	if err != nil {
		return err
	}
	// 校验完成再提交；todo 尚未持久化，不能沿用上一会话的内存清单。
	s.id, s.history, s.workDir = id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	return nil
}

// SessionList 列出全部持久会话（无存储模式返回空）。
func (s *Session) SessionList() []sessiondata.SessionMeta {
	if s.st == nil {
		return nil
	}
	metas, err := s.st.List()
	if err != nil {
		log.Printf("列出会话失败: %v", err)
		return nil
	}
	return metas
}

// AttachSessionSearch 把会话搜索接进工具注册表（EnablePersistence 后调用）：
// JSONL 格式归 store 包所有，工具层只拿函数——格式单源不漂移。
func (s *Session) AttachSessionSearch() {
	s.tools.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
		if s.st == nil {
			return "", errors.New("会话存储未启用，无法搜索历史")
		}
		hits, err := s.st.Search(pattern, max)
		if err != nil {
			return "", err
		}
		// Search 的 max 已被钳制；total 用同值（Search 不超发）
		return sessiondata.FormatSearchHits(hits, len(hits)), nil
	})
}

// switchGuardLocked 是切换会话的前置检查（调用方持锁）：
// busy → ErrBusy；挂起确认 → ErrPendingConfirm。
func (s *Session) switchGuardLocked() error {
	if s.busy {
		return fmt.Errorf("%w（先取消当前生成）", ErrBusy)
	}
	if s.pending != nil {
		return ErrPendingConfirm
	}
	return nil
}
