package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newTestSession 造测试用会话：空 config 注册表（不读磁盘）+ 可注入假 stream。
func newTestSession(t *testing.T) (*Session, *config.Registry) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	return s, reg
}

// bindDefault 注册并绑定一个 default 模型（不校验可达性——假 stream 不联网）。
func bindDefault(t *testing.T, reg *config.Registry) {
	t.Helper()
	m := config.ModelConfig{
		ID: "m1", BaseURL: "http://localhost:1", Model: "test-model",
		Format: config.FormatOpenAI, Enabled: true, Capabilities: config.Capabilities{Tools: true},
	}
	if err := reg.Add(m); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
}

// fakeStream 是可编排的假 LLM：按脚本顺序回放事件序列。
type fakeStream struct {
	mu     sync.Mutex
	script [][]llm.StreamEvent // 每次 Send 消耗一段
	calls  int
}

func (f *fakeStream) stream(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i >= len(f.script) {
		return nil, errors.New("脚本耗尽（未预期的多轮调用）")
	}
	ch := make(chan llm.StreamEvent, len(f.script[i]))
	for _, ev := range f.script[i] {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func textResult(content string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.EventText, TextDelta: content},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: content}, FinishReason: llm.FinishStop,
		}},
	}
}

// waitFor 轮询条件成立（事件是异步投递的）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// ---------- 基本循环 ----------

func TestSendPlainTextTurn(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{textResult("你好，我是测试回复")}}
	s.stream = fake.stream

	var mu sync.Mutex
	var gotDelta, gotDone bool
	s.emit = func(ev Event) {
		switch ev.(type) {
		case DeltaEvent:
			mu.Lock()
			gotDelta = true
			mu.Unlock()
		case TurnDoneEvent:
			mu.Lock()
			gotDone = true
			mu.Unlock()
		}
	}

	if err := s.Send("打个招呼"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if !gotDelta || !gotDone {
		t.Fatalf("应收到增量与完成事件: delta=%v done=%v", gotDelta, gotDone)
	}
	h := s.History()
	if len(h.Messages) != 2 { // user + assistant
		t.Fatalf("历史应含 user+assistant: %+v", h.Messages)
	}
}

func TestSendBusyRejected(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	block := make(chan struct{})
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		<-block // 挂住第一轮
		return nil, errors.New("不应走到这")
	}
	if err := s.Send("第一条"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s.Busy)
	if err := s.Send("第二条"); err == nil || !strings.Contains(err.Error(), "生成中") {
		t.Fatalf("忙时应拒绝: %v", err)
	}
	close(block)
}

func TestSendNoModelRejected(t *testing.T) {
	s, _ := newTestSession(t)
	if err := s.Send("hi"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("未绑定模型应报错: %v", err)
	}
}

// ---------- 工具循环与确认门 ----------

// toolCallResult 造一段"模型要求调用工具"的脚本。
func toolCallResult(name, args string) []llm.StreamEvent {
	var tc llm.ToolCall
	tc.ID = "call-1"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return []llm.StreamEvent{
		{Type: llm.EventText, TextDelta: "我先看一下"},
		{Type: llm.EventToolCall, ToolCall: tc},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:      llm.Message{Role: "assistant", Content: "我先看一下", ToolCalls: []llm.ToolCall{tc}},
			FinishReason: llm.FinishToolCalls,
		}},
	}
}

func TestToolLoopLowRiskAutoExecutes(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", `{"path":"no-such-file.txt"}`), // 低危：直接执行（报文件不存在）
		textResult("文件不存在，我换个方法"),
	}}
	s.stream = fake.stream

	var mu sync.Mutex
	var toolRslt string
	s.emit = func(ev Event) {
		if e, ok := ev.(ToolResultEvent); ok {
			mu.Lock()
			toolRslt = e.Content
			mu.Unlock()
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	_ = os.WriteFile(path, []byte("你好内容"), 0o644)
	fake.script[0] = toolCallResult("read_file", `{"path":`+jsonQuote(path)+`}`)

	if err := s.Send("读一下文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(toolRslt, "你好内容") {
		t.Fatalf("低危工具应自动执行并回填: %q", toolRslt)
	}
	// 历史含 user + assistant(工具调用) + tool + assistant(最终)
	if len(s.History().Messages) != 4 {
		t.Fatalf("工具循环历史不符: %+v", s.History().Messages)
	}
}

func TestConfirmGateAllowsAndRejects(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)

	// 用一个临时文件覆盖：write_file 覆盖已有文件触发确认
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	_ = os.WriteFile(path, []byte("旧内容"), 0o644)

	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("write_file", `{"path":`+jsonQuote(path)+`,"content":"新内容"}`),
		textResult("写好了"),
	}}
	s.stream = fake.stream

	var mu sync.Mutex
	var pending *ConfirmRequest
	s.emit = func(ev Event) {
		if e, ok := ev.(ConfirmRequestEvent); ok {
			mu.Lock()
			pending = e.Request
			mu.Unlock()
		}
	}

	if err := s.Send("覆盖那个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pending != nil
	})
	if pending == nil || pending.Name != "write_file" {
		t.Fatalf("应收到 write_file 确认请求: %+v", pending)
	}
	// 批准 → 执行落盘
	if err := s.Confirm(pending.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	data, _ := os.ReadFile(path)
	if string(data) != "新内容" {
		t.Fatalf("批准后应执行写入: %q", data)
	}

	// 再来一轮：拒绝 → 工具不执行，模型收到拒绝说明
	// （新建 fake 重置调用计数——旧 fake 已消耗一段脚本，续用会错位取段）
	fake2 := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("write_file", `{"path":`+jsonQuote(path)+`,"content":"再写一次"}`),
		textResult("好的，那我换个方式"),
	}}
	s.stream = fake2.stream
	pending = nil
	if err := s.Send("再覆盖一次"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pending != nil
	})
	if err := s.Confirm(pending.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	data, _ = os.ReadFile(path)
	if string(data) != "新内容" {
		t.Fatalf("拒绝后不应执行: %q", data)
	}
	// 历史里 tool 消息说明拒绝
	found := false
	for _, m := range s.History().Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "拒绝") {
			found = true
		}
	}
	if !found {
		t.Fatal("拒绝应回填 tool 消息给模型")
	}
}

func TestConfirmIDMismatch(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("x"), 0o644)

	s.stream = (&fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("write_file", `{"path":`+jsonQuote(path)+`,"content":"y"}`),
	}}).stream

	var pending *ConfirmRequest
	s.emit = func(ev Event) {
		if e, ok := ev.(ConfirmRequestEvent); ok {
			pending = e.Request
		}
	}
	if err := s.Send("写"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return pending != nil })
	if err := s.Confirm("bogus-id", true); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("id 不匹配应报错: %v", err)
	}
	// 清理：取消这轮
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })
}

// ---------- 取消保留部分内容 ----------

func TestCancelPreservesPartial(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	// 流挂住：发一半 delta 后阻塞
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "已经生成了"}
			<-ctx.Done() // 取消前挂住
			// 真实客户端的中断语义（llm.emitFinal）：error 事件携带已生成的
			// 部分内容，内核据此保留——这里对齐该契约
			ch <- llm.StreamEvent{Type: llm.EventError, Err: context.Canceled,
				Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "已经生成了"}}}
		}()
		return ch, nil
	}

	var aborted bool
	var partial string
	s.emit = func(ev Event) {
		if e, ok := ev.(TurnErrorEvent); ok && e.Aborted {
			aborted = true
			if e.Partial != nil {
				partial = e.Partial.Content
			}
		}
	}

	if err := s.Send("慢慢说"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Busy() })
	time.Sleep(50 * time.Millisecond) // 让 delta 先流出
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })
	if !aborted {
		t.Fatal("取消应以 aborted 错误事件收尾")
	}
	if partial != "已经生成了" {
		t.Fatalf("部分内容应保留: %q", partial)
	}
	// 部分内容入历史
	found := false
	for _, m := range s.History().Messages {
		if m.Role == "assistant" && m.Content == "已经生成了" {
			found = true
		}
	}
	if !found {
		t.Fatal("部分内容应入历史")
	}
}

// ---------- 取消/失败必须补齐工具配对 ----------
//
// 缺配对不是"不好看"：严格端点会 400 拒收整轮，而且压缩的切点会永久卡在缺配对
// 处之前（那个会话再也压不动）。所以取消与流式失败两条路径都要给未执行的调用
// 补上合成结果。

// twoToolCallResult 造一条声明两个工具调用的 assistant 回合（取消补齐测试用）。
func twoToolCallResult(name1, args1, name2, args2 string) []llm.StreamEvent {
	var a, b llm.ToolCall
	a.ID, a.Function.Name, a.Function.Arguments = "call-a", name1, args1
	b.ID, b.Function.Name, b.Function.Arguments = "call-b", name2, args2
	calls := []llm.ToolCall{a, b}
	return []llm.StreamEvent{
		{Type: llm.EventToolCall, ToolCall: a},
		{Type: llm.EventToolCall, ToolCall: b},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:      llm.Message{Role: "assistant", Content: "我读两个文件", ToolCalls: calls},
			FinishReason: llm.FinishToolCalls,
		}},
	}
}

// toolResultsByID 把历史里的 tool 结果按调用 id 收起来（断言用）。
func toolResultsByID(msgs []llm.Message) map[string]string {
	got := make(map[string]string)
	for _, m := range msgs {
		if m.Role == "tool" {
			got[m.ToolCallID] = m.Content
		}
	}
	return got
}

// TestCancelInToolBatchFillsResults：一批工具跑到一半取消——没执行的调用也必须
// 拿到结果（否则历史里留下没有回应的喊话）。
func TestCancelInToolBatchFillsResults(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.txt")
	p2 := filepath.Join(dir, "b.txt")
	_ = os.WriteFile(p1, []byte("内容A"), 0o644)
	_ = os.WriteFile(p2, []byte("内容B"), 0o644)

	s.stream = (&fakeStream{script: [][]llm.StreamEvent{
		twoToolCallResult("read_file", `{"path":`+jsonQuote(p1)+`}`, "read_file", `{"path":`+jsonQuote(p2)+`}`),
	}}).stream

	// 第一个工具的结果一出来就取消：第二个调用的执行位会发现 ctx 已断。
	// 这条路径同时验证「在 emit 回调里取消会话」不会死锁（emit 在锁外调用）。
	s.emit = func(ev Event) {
		if _, ok := ev.(ToolResultEvent); ok {
			s.Cancel()
		}
	}

	if err := s.Send("读两个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs := s.History().Messages
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 {
		t.Fatalf("取消后不应留下未配对的调用: %v（历史 %+v）", p.Unpaired, msgs)
	}
	got := toolResultsByID(msgs)
	if len(got) != 2 {
		t.Fatalf("两个调用都应有结果: %+v（历史 %+v）", got, msgs)
	}
	if !strings.Contains(got["call-a"], "内容A") {
		t.Fatalf("已执行的调用应保留真实结果: %q", got["call-a"])
	}
	if !strings.Contains(got["call-b"], "取消") {
		t.Fatalf("未执行的调用应有取消说明: %q", got["call-b"])
	}
}

// TestCancelWhileConfirmPendingFillsResults：确认门等待期间取消——该调用与它
// 后面的调用都没执行，必须都补上结果（这是最容易踩的一条：用户在确认框前点了停止）。
func TestCancelWhileConfirmPendingFillsResults(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	_ = os.WriteFile(path, []byte("旧内容"), 0o644)

	s.stream = (&fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("write_file", `{"path":`+jsonQuote(path)+`,"content":"新内容"}`),
	}}).stream

	var mu sync.Mutex
	var pending *ConfirmRequest
	s.emit = func(ev Event) {
		if e, ok := ev.(ConfirmRequestEvent); ok {
			mu.Lock()
			pending = e.Request
			mu.Unlock()
		}
	}

	if err := s.Send("覆盖那个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pending != nil
	})
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })

	msgs := s.History().Messages
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 {
		t.Fatalf("确认门取消后不应留下未配对调用: %v（历史 %+v）", p.Unpaired, msgs)
	}
	if got := toolResultsByID(msgs); !strings.Contains(got["call-1"], "取消") {
		t.Fatalf("被取消的调用应有取消说明: %+v", got)
	}
	// 工具确实没执行
	if data, _ := os.ReadFile(path); string(data) != "旧内容" {
		t.Fatalf("取消后不应执行写入: %q", data)
	}
}

// TestStreamFailureFillsToolResults：流式失败（非用户取消）时，已经声明的工具
// 调用同样不能悬空——失败路径与取消路径共用同一份补齐逻辑。
func TestStreamFailureFillsToolResults(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)

	var tc llm.ToolCall
	tc.ID, tc.Function.Name, tc.Function.Arguments = "call-x", "read_file", `{"path":"x"}`
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
		// 端点报错，但已生成的调用声明随 Result 带回（llm.emitFinal 的中断契约）
		ch <- llm.StreamEvent{Type: llm.EventError, Err: errors.New("端点 500"),
			Result: &llm.ChatResult{Message: llm.Message{
				Role: "assistant", Content: "我先看一下", ToolCalls: []llm.ToolCall{tc},
			}}}
		close(ch)
		return ch, nil
	}

	if err := s.Send("读文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs := s.History().Messages
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 {
		t.Fatalf("流式失败后不应留下未配对调用: %v（历史 %+v）", p.Unpaired, msgs)
	}
	if got := toolResultsByID(msgs); !strings.Contains(got["call-x"], "失败") {
		t.Fatalf("失败路径的合成结果应说明生成失败: %+v", got)
	}
}

// TestPairingRepairUnfreezesCompaction 钉住"一次取消锁死压缩"这件事本身：缺配对的
// 调用落在历史中间时，可压区间被永久卡在它之前；补齐配对后区间就能覆盖它。
// （既有的 TestSelectCompactRangeNothingWhenTooShort 只覆盖"未配对调用在结尾"。）
func TestPairingRepairUnfreezesCompaction(t *testing.T) {
	var call llm.ToolCall
	call.ID, call.Function.Name = "c1", "read_file"
	dangling := []llm.Message{
		{Role: "user", Content: "第一段"},
		{Role: "assistant", Content: "我先看一下", ToolCalls: []llm.ToolCall{call}},
		{Role: "user", Content: "第二段"},
		{Role: "assistant", Content: "回复"},
	}
	_, endBad, okBad := selectCompactRange(dangling, 1)
	if !okBad || endBad != 0 {
		t.Fatalf("缺配对时切点应退到调用之前（只能压 1 条）: ok=%v end=%d", okBad, endBad)
	}
	repaired := []llm.Message{
		dangling[0], dangling[1],
		{Role: "tool", ToolCallID: "c1", Content: skippedCancelNote},
		dangling[2], dangling[3],
	}
	_, endGood, okGood := selectCompactRange(repaired, 1)
	if !okGood || endGood < 2 {
		t.Fatalf("补齐配对后区间应能覆盖该调用: ok=%v end=%d", okGood, endGood)
	}
}

// ---------- todo ----------

func TestTodoSinkWired(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("todo", `{"items":[{"content":"第一步","status":"active"},{"content":"第二步","status":"pending"}]}`),
		textResult("清单建好了"),
	}}
	s.stream = fake.stream

	var mu sync.Mutex
	var todoN int
	s.emit = func(ev Event) {
		if _, ok := ev.(TodoUpdatedEvent); ok {
			mu.Lock()
			todoN++
			mu.Unlock()
		}
	}

	if err := s.Send("建个清单"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if todoN != 1 {
		t.Fatalf("todo 工具应触发 TodoUpdated 事件: %d", todoN)
	}
	todos := s.Todos()
	if len(todos) != 2 || todos[0].Status != "active" {
		t.Fatalf("会话应持有 todo 状态: %+v", todos)
	}
}

// ---------- 持久化 ----------

// newPersistSession 造一个挂了临时存储的 Session（事件丢弃，不碰网络）。
func newPersistSession(t *testing.T, dir string) *Session {
	t.Helper()
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) // SQLite 连接必须显式关（Windows 句柄挡 TempDir 删除）
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPersistenceMessagesWrittenToFile(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	s.append(llm.Message{Role: "user", Content: "落盘测试"})

	if len(s.History().Messages) != 1 {
		t.Fatalf("内存历史不符: %+v", s.History())
	}
	id := s.SessionID()
	if id == "" {
		t.Fatal("首条消息后应已创建会话文件（懒创建）")
	}
	// 直接从 store 读回
	msgs, err := s.st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "落盘测试" {
		t.Fatalf("文件内容与内存不符: %+v", msgs)
	}
}

func TestRestartRestoresLatestSession(t *testing.T) {
	dir := t.TempDir()
	s1 := newPersistSession(t, dir)
	s1.append(llm.Message{Role: "user", Content: "重启前的对话"})
	id1 := s1.SessionID()

	// 模拟重启：全新 Session 挂同一存储
	s2 := newPersistSession(t, dir)
	if got := s2.SessionID(); got != id1 {
		t.Fatalf("应恢复最近会话 id=%s, got %s", id1, got)
	}
	h := s2.History()
	if len(h.Messages) != 1 || h.Messages[0].Content != "重启前的对话" {
		t.Fatalf("重启后历史应恢复: %+v", h.Messages)
	}
	// 恢复后继续追加，写到同一个文件
	s2.append(llm.Message{Role: "assistant", Content: "重启后的回复"})
	msgs, _ := s2.st.Load(id1)
	if len(msgs) != 2 {
		t.Fatalf("续写应进同一会话文件: %+v", msgs)
	}
}

func TestSwitchNewArchivesOld(t *testing.T) {
	dir := t.TempDir()
	s := newPersistSession(t, dir)
	s.append(llm.Message{Role: "user", Content: "旧会话内容"})
	oldID := s.SessionID()

	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	if s.SessionID() != "" || len(s.History().Messages) != 0 {
		t.Fatal("新会话应为空历史")
	}
	// 旧文件还在，可 resume
	if err := s.SwitchTo(oldID); err != nil {
		t.Fatal(err)
	}
	if len(s.History().Messages) != 1 {
		t.Fatal("resume 应恢复历史")
	}
}

func TestSessionList(t *testing.T) {
	dir := t.TempDir()
	s := newPersistSession(t, dir)
	s.append(llm.Message{Role: "user", Content: "第一个会话"})
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	s.append(llm.Message{Role: "user", Content: "第二个会话"})

	// SQLite 版排序依据 updated_at（纳秒精度），无需文件版时代的 mtime 拨弄
	list := s.SessionList()
	if len(list) != 2 {
		t.Fatalf("应列出 2 个会话: %+v", list)
	}
	if !strings.Contains(list[0].Title, "第二个会话") {
		t.Fatalf("最近的排最前: %+v", list)
	}
}

// ---------- 工作目录（项目归属 → 工具执行目录） ----------

// TestSwitchNewResolvesWorkDir：session.new 带项目 id 时，工作目录
// 解析为项目根（侧栏分组之外的实际语义）；未知项目显式失败且保留原状。
func TestSwitchNewResolvesWorkDir(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	dir := t.TempDir()
	saved, err := s.st.(*store.Store).AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchNew(saved.ID); err != nil {
		t.Fatal(err)
	}
	if s.WorkDir() != dir {
		t.Fatalf("工作目录应为项目根: got %q want %q", s.WorkDir(), dir)
	}
	// 未知项目必须失败且保留已有目录，不能悄悄去进程目录执行。
	if _, err := s.SwitchNew("不存在的项目"); err == nil {
		t.Fatal("未知项目应被拒绝")
	}
	if s.WorkDir() != dir {
		t.Fatalf("失败应保留目录: %q", s.WorkDir())
	}
}

// TestSwitchNewClearsWorkDir：不带 workspace 的 session.new 回到未分组
// （默认目录）——上一个项目的归属不得残留。
func TestSwitchNewClearsWorkDir(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	dir := t.TempDir()
	saved, err := s.st.(*store.Store).AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchNew(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	if s.WorkDir() != "" {
		t.Fatalf("SwitchNew 应清空工作目录: %q", s.WorkDir())
	}
}

// TestSwitchToRestoresWorkDir：resume 项目会话时工作目录跟随恢复。
func TestSwitchToRestoresWorkDir(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	dir := t.TempDir()
	saved, err := s.st.(*store.Store).AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	// 项目会话：落一行消息（建会话行）→ 归属落库
	if _, err := s.SwitchNew(saved.ID); err != nil {
		t.Fatal(err)
	}
	s.append(llm.Message{Role: "user", Content: "项目里的问题"})
	id := s.SessionID()
	// 新会话（默认目录）→ resume 回项目会话
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	if s.WorkDir() != "" {
		t.Fatal("新会话应为默认目录")
	}
	if err := s.SwitchTo(id); err != nil {
		t.Fatal(err)
	}
	if s.WorkDir() != dir {
		t.Fatalf("resume 应恢复项目工作目录: got %q want %q", s.WorkDir(), dir)
	}
}

// TestRestartRestoresWorkDir：重启恢复最近会话时，项目归属的工作目录
// 一并恢复（EnablePersistence 路径）。
func TestRestartRestoresWorkDir(t *testing.T) {
	dir := t.TempDir()
	dbDir := t.TempDir()
	s1 := newPersistSession(t, dbDir)
	proj, err := s1.st.(*store.Store).AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.SwitchNew(proj.ID); err != nil {
		t.Fatal(err)
	}
	s1.append(llm.Message{Role: "user", Content: "重启前的项目会话"})

	// 模拟重启：全新 Session 挂同一存储（恢复最近会话 + 归属）
	s2 := newPersistSession(t, dbDir)
	if s2.WorkDir() != dir {
		t.Fatalf("重启应恢复项目工作目录: got %q want %q", s2.WorkDir(), dir)
	}
}

// TestTurnToolsRunInWorkDir：项目会话的工具循环里，相对路径落在项目根
// （用户可感知的端到端行为：模型在项目目录里干活）。
func TestTurnToolsRunInWorkDir(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	dir := t.TempDir()
	proj, err := s.st.(*store.Store).AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("项目里的文件"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchNew(proj.ID); err != nil {
		t.Fatal(err)
	}

	// 假流第一轮回一个 read_file 调用（相对路径），第二轮收尾
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", `{"path":"hello.txt"}`),
		textResult("读到了"),
	}}
	s.stream = fake.stream

	var mu sync.Mutex
	var toolResult string
	s.emit = func(ev Event) {
		if r, ok := ev.(ToolResultEvent); ok {
			mu.Lock()
			toolResult = r.Content
			mu.Unlock()
		}
	}
	if err := s.Send("读一下 hello.txt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if !strings.Contains(toolResult, "项目里的文件") {
		t.Fatalf("read_file 相对路径应落在项目根: %q", toolResult)
	}
	// 会话行已建且归属落库
	if ws, err := s.st.WorkspaceOf(s.SessionID()); err != nil || ws != proj.ID {
		t.Fatalf("归属应落库: %q %v", ws, err)
	}
}

// ---------- 系统提示词 ----------

func TestSystemPromptListsAllTools(t *testing.T) {
	s, _ := newTestSession(t)
	prompt := BuildSystemPrompt(s.tools, "", ProjectDocs{})
	for _, name := range s.tools.Order() {
		// 清单行形如 "- read_file：…"（中文冒号分隔）
		if !strings.Contains(prompt, name+"：") {
			t.Fatalf("提示词应列出工具 %s（清单与注册表不漂移）", name)
		}
	}
	// 九个工具的清单行数（read_skill + agent.dispatch——渐进披露与调度）
	if got := strings.Count(prompt, "\n- "); got != 9 {
		t.Fatalf("工具清单应 9 行, got %d", got)
	}
}

func TestSystemPromptDeterministic(t *testing.T) {
	s, _ := newTestSession(t)
	a, b := BuildSystemPrompt(s.tools, "", ProjectDocs{}), BuildSystemPrompt(s.tools, "", ProjectDocs{})
	if a != b {
		t.Fatal("提示词应确定性生成")
	}
}

// TestSystemPromptWorkDir：项目会话的提示词必须写明工作目录（模型
// 按它解析相对路径、决定在哪跑命令）。
func TestSystemPromptWorkDir(t *testing.T) {
	s, _ := newTestSession(t)
	prompt := BuildSystemPrompt(s.tools, `C:\proj\demo`, ProjectDocs{})
	for _, want := range []string{`C:\proj\demo`, "项目根目录即工作目录", "相对路径"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("项目会话提示词应含 %q", want)
		}
	}
	if strings.Contains(BuildSystemPrompt(s.tools, "", ProjectDocs{}), "项目根") {
		t.Fatal("未分组会话不应有项目根话术")
	}
}

// jsonQuote 生成 JSON 字符串字面量（路径含反斜杠时转义）。
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
