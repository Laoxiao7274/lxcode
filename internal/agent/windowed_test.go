// 窗口化装载（会话切换优化批 4）的 agent 层钉子：
//
// ① 窗口化附着装载尾部窗口 + 配对回退（与 TailHistory 同一套不变量）；
// ② 首次 Send / Rewind 前补载全量（fail-closed：补载失败拒绝动作）；
// ③ 窗口态的 HistoryBefore 走存储侧分页（窗口全部在锚点之前 + 元数据同口径）；
// ④ 非 TailLoader 实现降级全量（零破坏内存测试桩）；
// ⑤ 非窗口路径零行为变化。
package agent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// windowedStoreStub：内存历史的 Persistence 替身（实现 Load + TailLoader +
// 附着路径用到的方法；内嵌 Persistence 为 nil，未预期调用 panic——与
// persistenceStub 同一条纪律：未预期的假成功比 panic 更难查）。
type windowedStoreStub struct {
	Persistence
	history  map[string][]llm.Message
	failLoad bool // 补载失败注入（fail-closed 钉子用）
}

func newWindowedStoreStub() *windowedStoreStub {
	return &windowedStoreStub{history: map[string][]llm.Message{}}
}

func (f *windowedStoreStub) seed(id string, msgs []llm.Message) {
	for i := range msgs {
		msgs[i].Seq = int64(i + 1)
	}
	f.history[id] = msgs
}

func (f *windowedStoreStub) Load(id string) ([]llm.Message, error) {
	if f.failLoad {
		return nil, errors.New("注入的装载失败")
	}
	msgs := f.history[id]
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	return out, nil
}

func (f *windowedStoreStub) LoadTail(id string, before int64, n int) ([]llm.Message, bool, error) {
	msgs := f.history[id]
	start := len(msgs)
	if before > 0 {
		found := false
		for i, m := range msgs {
			if m.Seq == before {
				start, found = i, true // 不含锚点
				break
			}
		}
		if !found {
			return nil, false, errors.New("锚点不在历史里")
		}
	}
	lo := start - n
	if lo < 0 {
		lo = 0
	}
	out := make([]llm.Message, start-lo)
	copy(out, msgs[lo:start])
	return out, lo == 0, nil
}

func (f *windowedStoreStub) WorkspaceOf(string) (string, error) { return "", nil }

// AppendMsg：Send 路径的写口（seq = 内存长度 + 1——单链测试历史的同款语义）。
func (f *windowedStoreStub) AppendMsg(id string, m llm.Message) (int64, error) {
	h := f.history[id]
	m.Seq = int64(len(h) + 1)
	f.history[id] = append(h, m)
	return m.Seq, nil
}

// RewindTo：叶指针回退的测试语义 = 从内存历史里截掉锚点及其后（单链退化的
// 「移叶」，与 store.RewindTo 的可观察行为一致：Load 变成锚点之前的前缀）。
func (f *windowedStoreStub) RewindTo(id string, anchor int64) (removed int, leaf int64, err error) {
	msgs := f.history[id]
	for i, m := range msgs {
		if m.Seq == anchor {
			f.history[id] = append([]llm.Message(nil), msgs[:i]...)
			return len(msgs) - i, int64(i), nil // 新叶 = 锚点前一条（i==0 → 0）
		}
	}
	return 0, int64(len(msgs)), nil // 幂等：锚点不在历史里
}

func (f *windowedStoreStub) ContextUsageOf(string) (sessiondata.ContextUsage, bool, error) {
	return sessiondata.ContextUsage{}, false, nil
}
func (f *windowedStoreStub) SaveContextUsage(string, sessiondata.ContextUsage) error { return nil }
func (f *windowedStoreStub) ApprovalOf(string) (string, error)                       { return "", nil }
func (f *windowedStoreStub) SaveApproval(string, string) error                       { return nil }
func (f *windowedStoreStub) SessionModelOf(string) (string, error)                   { return "", nil }
func (f *windowedStoreStub) SaveSessionModel(string, string) error                   { return nil }
func (f *windowedStoreStub) ProjectByID(string) (sessiondata.ProjectMeta, bool, error) {
	return sessiondata.ProjectMeta{}, false, nil
}
func (f *windowedStoreStub) TodosOf(string) ([]sessiondata.TodoItem, bool, error) {
	return nil, false, nil
}
func (f *windowedStoreStub) SaveTodos(string, []sessiondata.TodoItem) error { return nil }
func (f *windowedStoreStub) SessionParentID(string) (string, error) {
	return "", errors.New("not a session")
}

// windowedHistory：带完整工具配对的两轮历史（user → assistant(tool_calls) →
// tool → assistant → user → assistant ×2 轮）。
func windowedHistory() []llm.Message {
	return []llm.Message{
		{Role: "user", Content: "问一"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "read_file", Arguments: "{}"},
		}}},
		{Role: "tool", Content: "结果", ToolCallID: "c1"},
		{Role: "assistant", Content: "答一"},
		{Role: "user", Content: "问二"},
		{Role: "assistant", Content: "答二"},
		{Role: "user", Content: "问三"},
		{Role: "assistant", Content: "答三"},
	}
}

// newWindowedSession：AttachToWindowed 的测试装配（带注册表——Send 要解析模型；
// 假 stream 不联网，见 session_test.go 的同款装配）。空 workspace → workDir 空。
func newWindowedSession(t *testing.T, st *windowedStoreStub, id string) *Session {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	bindDefault(t, reg)
	s := New(reg, tools.New(), func(Event) {})
	t.Cleanup(s.Close)
	if err := s.AttachToWindowed(st, id); err != nil {
		t.Fatalf("AttachToWindowed: %v", err)
	}
	return s
}

func TestAttachToWindowedLoadsTail(t *testing.T) {
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	s := newWindowedSession(t, st, "w1")
	s.mu.Lock()
	windowed, n := s.windowed, len(s.history)
	s.mu.Unlock()
	if !windowed {
		t.Fatal("附着后应处于窗口态")
	}
	// 小历史：窗口覆盖全量（tailWindowRows=280 > 8）——窗口非空 + 探测正确
	if n != 8 {
		t.Fatalf("小历史的窗口应为全量 8 条，得到 %d", n)
	}
	if !s.Windowed() || !s.HasMessages() {
		t.Fatal("Windowed/HasMessages 探测失真")
	}
}

func TestLoadWindowBalancedRecedesToBalancedCut(t *testing.T) {
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	// want=7：start=1（cuts[1]=true，本就平衡）→ 窗口 = seq2..8 共 7 条
	msgs, atRoot, err := loadWindowBalanced(st, "w1", 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 7 {
		t.Fatalf("want=7 的窗口应为 7 条（start 本就平衡），得到 %d", len(msgs))
	}
	if msgs[0].Seq != 2 || msgs[0].Role != "assistant" {
		t.Fatalf("窗口首条应为 seq2 的 assistant（带 tool_calls），得到 %+v", msgs[0])
	}
	if atRoot {
		t.Fatal("窗口未到根（seq1 在外），atRoot 应为 false")
	}
	// want=6：start=2 是**不平衡切点**（assistant 的 c1 未回填）→ 回退到最近
	// 平衡切点 1 → 窗口 = seq2..8 共 7 条（可大于 want——与 TailHistory 的
	// 回退语义一致：起点配平优先于窗口大小）。
	msgs, _, err = loadWindowBalanced(st, "w1", 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 7 || msgs[0].Seq != 2 {
		t.Fatalf("配平回退后窗口应为 seq2 起的 7 条，得到 %d 条 seq=%v", len(msgs), seqOfWindow(msgs))
	}
	// 窗口内部配对自洽：起点平衡 + Unpaired 为空（c1 的结果 seq3 在窗口里）
	pairing := AnalyzeToolPairing(msgs)
	if !pairing.BalancedBefore(0) || len(pairing.Unpaired) != 0 {
		t.Fatalf("窗口配对不自洽：cuts[0]=%v unpaired=%v", pairing.BalancedBefore(0), pairing.Unpaired)
	}
}

// seqOfWindow 是窗口测试的 seq 列表辅助（store 层的 seqOf 在别处定义，这里
// 独立一份避免测试文件互相依赖）。
func seqOfWindow(msgs []llm.Message) []int64 {
	out := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Seq)
	}
	return out
}

func TestWindowedSendTriggersFullLoad(t *testing.T) {
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	s := newWindowedSession(t, st, "w1")
	// 补载失败 → Send 拒绝（fail-closed，不留半截轮次）
	st.failLoad = true
	if err := s.Send("新消息"); err == nil {
		t.Fatal("补载失败时 Send 必须拒绝（fail-closed）")
	}
	st.failLoad = false
	if err := s.Send("新消息"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	s.mu.Lock()
	windowed, n := s.windowed, len(s.history)
	first := ""
	if len(s.history) > 0 {
		first = s.history[0].Content
	}
	s.mu.Unlock()
	if windowed {
		t.Fatal("Send 后窗口态应结束（全量已补载）")
	}
	if n != 9 {
		t.Fatalf("补载 + 新消息应为 9 条，得到 %d", n)
	}
	if first != "问一" {
		t.Fatalf("补载后的内存历史应从「问一」开始，得到 %q", first)
	}
}

func TestWindowedRewindTriggersFullLoad(t *testing.T) {
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	s := newWindowedSession(t, st, "w1")
	// 撤回到第 5 条（user「问二」seq=5）之前
	if _, err := s.Rewind(5); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	s.mu.Lock()
	windowed, n := s.windowed, len(s.history)
	s.mu.Unlock()
	if windowed {
		t.Fatal("Rewind 后窗口态应结束（Rewind 内部全量重载）")
	}
	if n != 4 {
		t.Fatalf("撤回到锚点之前应为 4 条，得到 %d", n)
	}
}

func TestWindowedHistoryBeforeHitsStore(t *testing.T) {
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	s := newWindowedSession(t, st, "w1")
	// 手工构造小窗口（want=2 → 配平回退后 = 尾部 2 条：问三 + 答三），
	// 分页锚点 = 窗口首条 seq——锚点之前的历史**不在内存里**，只能走存储。
	msgs, _, err := loadWindowBalanced(st, "w1", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.history = msgs
	s.windowed = true
	s.bumpHistoryLocked()
	s.mu.Unlock()
	first := msgs[0].Seq
	snap, _, firstSeq, err := s.HistoryBefore(first, 2)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(snap.Messages) == 0 {
		t.Fatal("分页窗口为空")
	}
	if last := snap.Messages[len(snap.Messages)-1].Seq; last >= first {
		t.Fatalf("分页窗口应全部在锚点 %d 之前，末条 seq %d", first, last)
	}
	if firstSeq != snap.Messages[0].Seq {
		t.Fatalf("firstSeq %d 与窗口首条 %d 不一致", firstSeq, snap.Messages[0].Seq)
	}
	if snap.Todos == nil {
		t.Fatal("快照 Todos 恒非 nil（wire 契约）")
	}
}

func TestHistoryBeforeNonWindowedUnchanged(t *testing.T) {
	// 非窗口路径零行为变化：错误文案原样（server 侧映射 CodeInvalidParams 依赖它）
	s := New(nil, tools.New(), func(Event) {})
	s.history = windowedHistory()
	_, _, _, err := s.HistoryBefore(999, 5)
	if err == nil || !strings.Contains(err.Error(), "不在这条会话的历史里") {
		t.Fatalf("锚点不在历史里的错误应保留原文案，得到 %v", err)
	}
}

func TestWindowedAppendPanics(t *testing.T) {
	// 结构性防线（审查）：append 是所有历史写收敛点，窗口态写历史 = 撕裂内存
	// 与库的一致性。合法入口（Send/Rewind/SetLeaf/Compact）都先 ensureFullHistory
	// 补载；守卫只在「新增入口忘补载」的 bug 场景触发——从静默撕裂变当轮 panic。
	// 正常路径（Send/Rewind）不触发守卫由上面两条测试覆盖。
	st := newWindowedStoreStub()
	st.seed("w1", windowedHistory())
	s := newWindowedSession(t, st, "w1")
	s.mu.Lock()
	windowed := s.windowed
	s.mu.Unlock()
	if !windowed {
		t.Fatal("前置：会话应处于窗口态")
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("窗口态 append 必须显式失败（静默写入 = 撕裂内存与库）")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "ensureFullHistory") {
			t.Fatalf("panic 信息应指明需先 ensureFullHistory，得到 %v", r)
		}
		// 守卫不产生半截状态：历史与库都不得被写入
		s.mu.Lock()
		n := len(s.history)
		s.mu.Unlock()
		if n != len(windowedHistory()) {
			t.Fatalf("守卫触发时历史不得变化，得到 %d 条", n)
		}
	}()
	s.append(llm.Message{Role: "user", Content: "窗口态不该进来的消息"})
}

func TestAttachToWindowedFallsBackWithoutTailLoader(t *testing.T) {
	// persistenceStub 没有 LoadTail → 与 AttachTo 等价（全量装载，非窗口态）
	st := &persistenceStub{dir: t.TempDir()}
	s := New(nil, tools.New(), func(Event) {})
	if err := s.AttachToWindowed(st, "saved"); err != nil {
		t.Fatalf("降级附着: %v", err)
	}
	s.mu.Lock()
	windowed, n := s.windowed, len(s.history)
	s.mu.Unlock()
	if windowed {
		t.Fatal("无 TailLoader 的实现应降级全量（非窗口态）")
	}
	if n != 1 {
		t.Fatalf("降级应全量装载 stub 的 1 条历史，得到 %d", n)
	}
}
