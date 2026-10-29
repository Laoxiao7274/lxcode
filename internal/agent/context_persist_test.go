// 上下文占用的**落库与恢复**（用户实测问题：查看一条旧会话，上下文信息是空的）。
//
// 根因：ContextUsage 原先只在内存里（由一轮的 prompt_tokens 写入），进程一重启就是零值 →
// chat.history 的 context 整键缺席 → 前端显示中性态。而那条会话的占用其实是**已知事实**
// （历史就在库里）。两条路互补：① 每轮主轮把测量落库（权威值）；② 库里没有就按已加载的
// 历史回落估算，并照实标注 Estimated（老会话/从没跑过主轮）。
//
// 这一组测试钉住四件事：重启后数字**一模一样**（live 与 replay 不分叉）、老会话有估算值、
// 两个来源都没有时不编数、落库失败不打断一轮。
package agent

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newUsageEnv 造"挂了存储 + 带窗口 default 模型"的会话（占用落库/恢复的测试台）。
// 窗口必须配（32768）：没有窗口就算不出占比，前端仍然显示中性态——那样这个测试
// 就测不到用户报的那个现象。
func newUsageEnv(t *testing.T, dir string) (*Session, *store.Store) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	bindDefaultWithWindow(t, reg, 32768)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) // SQLite 连接必须显式关（Windows 句柄挡 TempDir 删除）
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	return s, st
}

// restartSession 模拟"后端重启"：全新 Session 附着同一个会话 id（同一份库、全新的内存）。
func restartSession(t *testing.T, reg *config.Registry, st *store.Store, id string) *Session {
	t.Helper()
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	if err := s.AttachTo(st, id); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestContextUsagePersistsAcrossRestart：跑一轮主轮 → 重启（新 Session 附着同一 id）→
// 占用与重启前**逐字段一致**。
//
// 为什么必须逐字段一致：chat.done 的实时值与 chat.history 的回放值来自两处（内存 vs 库），
// 各算一遍就会分叉——本仓库为"两条路径不一致"吃过三次亏。
func TestContextUsagePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s1, st := newUsageEnv(t, dir)
	s1.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("好", 12, 5180)}}).stream)
	if err := s1.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s1.Busy() })

	live := s1.ContextUsage()
	if live.Used != 5180 || live.Window != 32768 || live.Estimated {
		t.Fatalf("前置条件：主轮应记录 provider 回报的真实用量: %+v", live)
	}
	id := s1.SessionID()
	if id == "" {
		t.Fatal("前置条件：会话应已落库")
	}

	s2 := restartSession(t, s1.reg, st, id)
	if got := s2.ContextUsage(); got != live {
		t.Fatalf("重启后**锚点**应与实时逐字段一致: live=%+v replay=%+v", live, got)
	}
	// chat.history 读的就是 Snapshot.Context（server 从这里转 wire）。它是**投影值**
	//（锚点 + 测量之后历史的变化量），所以要和重启前的同一份口径比——拿锚点比会
	// 差出那条助手回复的估算量。
	liveSnap := s1.History().Context
	if got := s2.History().Context; got != liveSnap {
		t.Fatalf("快照里的占用应与重启前一致（同一份投影口径）: live=%+v snap=%+v", liveSnap, got)
	}
	if liveSnap.Used <= live.Used {
		t.Fatalf("前置条件：投影值应大于锚点（助手回复在测量之后才进历史）: 锚点=%+v 投影=%+v", live, liveSnap)
	}
}

// TestContextUsageFallsBackToHistoryEstimate：库里没有落库测量（老会话/从没跑过主轮）时，
// 按**已加载的历史**回落估算——复用同一份估算实现，并照实标注 Estimated。
func TestContextUsageFallsBackToHistoryEstimate(t *testing.T) {
	dir := t.TempDir()
	s1, st := newUsageEnv(t, dir)
	// 老会话：直接往库里塞历史（模拟"上一次跑是旧版本后端，没有 context_usage 列"）
	id, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range bigHistory(4) {
		if _, err := st.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := st.ContextUsageOf(id); err != nil || ok {
		t.Fatalf("前置条件：这条会话库里不该有占用测量（ok=%v err=%v）", ok, err)
	}

	s2 := restartSession(t, s1.reg, st, id)
	got := s2.ContextUsage()
	if got.Used <= 0 {
		t.Fatalf("库里没有测量时应按历史回落估算（不是空——用户报的就是这个空）: %+v", got)
	}
	if !got.Estimated {
		t.Fatalf("回落估算必须照实标注 estimated（不许当真实用量展示）: %+v", got)
	}
	if got.Window != 32768 {
		t.Fatalf("估算的窗口应按当前模型解析: %+v", got)
	}
	if got.Used != usageTotal(got) {
		t.Fatalf("估算路径下分类之和应等于 Used: %+v", got)
	}
}

// TestContextUsageAbsentWhenNothingKnown：两个来源都没有（空历史）→ 保持零值
// （wire 上整键缺席、前端中性态）。**不编数**——空会话显示 0% 比显示「—」更坏。
func TestContextUsageAbsentWhenNothingKnown(t *testing.T) {
	dir := t.TempDir()
	s1, st := newUsageEnv(t, dir)
	id, err := st.Create() // 空会话行：没有任何消息，也没有任何测量
	if err != nil {
		t.Fatal(err)
	}
	s2 := restartSession(t, s1.reg, st, id)
	if got := s2.ContextUsage(); got.Used != 0 {
		t.Fatalf("空历史两个来源都没有时应保持零值: %+v", got)
	}
}

// TestContextUsageSaveFailureDoesNotBreakTurn：占用落库失败**不打断一轮**（只记日志）。
// 占用是展示信息、不是业务不变量——落库失败让整轮对话失败是本末倒置。
func TestContextUsageSaveFailureDoesNotBreakTurn(t *testing.T) {
	s, st := newUsageEnv(t, t.TempDir())
	failing := &failingUsageStore{Persistence: st, err: errors.New("磁盘写满（模拟）")}
	s.st = failing // 只让"占用落库"这一步失败，其余照旧

	var done ContextUsage
	s.emit = func(ev Event) {
		if e, ok := ev.(TurnDoneEvent); ok {
			done = e.Context
		}
	}
	s.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("好", 5, 4321)}}).stream)
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	if failing.calls == 0 {
		t.Fatal("前置条件：这一轮应真的尝试过落库（否则这个测试什么也没证明）")
	}
	if got := s.ContextUsage(); got.Used != 4321 {
		t.Fatalf("落库失败不该影响内存里的测量（锚点 = 真实 prompt_tokens）: %+v", got)
	}
	// 事件带的是**投影值**（锚点 + 助手回复那条的估算量）——判定用锚点、展示用投影，
	// 两者都不该被落库失败影响
	if want := 4321 + estimateMessageTokens(llm.Message{Role: "assistant", Content: "好"}); done.Used != want {
		t.Fatalf("这一轮应正常收尾（chat.done 带投影后的测量，want used=%d）: %+v", want, done)
	}
	// 消息落库走的是另一条路径（AppendMsg），不该被占用落库失败牵连
	msgs, err := st.Load(s.SessionID())
	if err != nil || len(msgs) != 2 {
		t.Fatalf("占用落库失败不该影响消息落库: err=%v msgs=%+v", err, msgs)
	}
}

// failingUsageStore 只让"占用落库"失败：其余方法原样委托给真 store。
type failingUsageStore struct {
	Persistence
	err   error
	calls int
}

func (f *failingUsageStore) SaveContextUsage(string, sessiondata.ContextUsage) error {
	f.calls++
	return f.err
}
