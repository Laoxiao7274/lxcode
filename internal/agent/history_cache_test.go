// 会话级派生值缓存的失效纪律测试（2026-12 会话切换优化批 3）。
//
// 缓存本身在 history_cache.go；这里钉住的是「写后读一致性」——每个历史改写点
// 之后，History() 的消息 / 占用投影 / 配对切点必须立刻反映新历史，绝不允许
// 缓存里留着旧值（那会让 UI 显示不存在的消息、让切点卡在已撤销的位置）。
// 失效点清单见 history_cache.go 的包注释——每条对应一个（或一组）用例。
package agent

import (
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// appendToolCallMsg / appendToolResultMsg：造一条带工具调用的 assistant 与它的结果。
func appendToolCallMsg(t *testing.T, s *Session, id string) {
	t.Helper()
	tc := llm.ToolCall{ID: id}
	tc.Function.Name = "read_file"
	tc.Function.Arguments = "{}"
	s.AppendForTest(llm.Message{
		Role: "assistant", Content: "",
		ToolCalls: []llm.ToolCall{tc},
	})
}

func appendToolResultMsg(t *testing.T, s *Session, id string) {
	t.Helper()
	s.AppendForTest(llm.Message{Role: "tool", ToolCallID: id, Content: "结果"})
}

// TestCacheHistoryInvalidatedOnAppend：append 之后 History 立刻带新消息
// （缓存命中过的会话也不能漏——先读一次建立缓存，再 append，再读）。
func TestCacheHistoryInvalidatedOnAppend(t *testing.T) {
	s, _ := newTestSession(t)
	s.AppendForTest(llm.Message{Role: "user", Content: "一"})
	first := s.History()
	if len(first.Messages) != 1 {
		t.Fatalf("首读应 1 条，得到 %d", len(first.Messages))
	}
	// 缓存已建立：append 后再读必须看到第二条
	s.AppendForTest(llm.Message{Role: "assistant", Content: "二"})
	second := s.History()
	if len(second.Messages) != 2 {
		t.Fatalf("append 后 History 应 2 条（缓存失效），得到 %d", len(second.Messages))
	}
	if second.Messages[1].Content != "二" {
		t.Fatalf("新消息内容不对: %q", second.Messages[1].Content)
	}
}

// TestCacheContextProjectionTracksHistory：占用投影随历史增长——
// 先建立缓存（第一次 History），append 之后投影必须按新历史重算
// （SampledTokens 基线不变，但 now 增大 ⇒ 投影值变化）。
func TestCacheContextProjectionTracksHistory(t *testing.T) {
	s, _ := newTestSession(t)
	s.AppendForTest(llm.Message{Role: "user", Content: "问题"})
	// 造一次真实测量锚点（模拟主轮记录的占用）
	s.mu.Lock()
	s.context = ContextUsage{Used: 1000, Window: 8192, SampledTokens: historyTokens(s.history)}
	s.mu.Unlock()
	before := s.History()
	if before.Context.Used != 1000 {
		t.Fatalf("投影基准应是测量值 1000，得到 %d", before.Context.Used)
	}
	// 长长的工具结果进历史：投影要变大（缓存失效后才可能变对）
	s.AppendForTest(llm.Message{Role: "tool", ToolCallID: "t1", Content: string(make([]byte, 4000))})
	after := s.History()
	if after.Context.Used <= before.Context.Used {
		t.Fatalf("历史变长后投影必须变大：before=%d after=%d（缓存没失效）", before.Context.Used, after.Context.Used)
	}
}

// TestCachePairingTracksHistory：配对切点随历史变化——未配对的 assistant 之后
// 补上 tool 结果，同一个 TailHistory 请求的窗口必须更新（缓存命中过的会话也不能
// 拿旧配对切点切窗口）。
func TestCachePairingTracksHistory(t *testing.T) {
	s, _ := newTestSession(t)
	s.AppendForTest(llm.Message{Role: "user", Content: "一"})
	s.AppendForTest(llm.Message{Role: "user", Content: "二"})
	s.AppendForTest(llm.Message{Role: "user", Content: "三"})
	// 建缓存（配对切点：三条独立 user，处处平衡）
	_ = s.TailHistory(2)
	// 挂一个未配对的工具调用：窗口起点不再能切在它之前与之后的边界
	appendToolCallMsg(t, s, "t1")
	s.AppendForTest(llm.Message{Role: "user", Content: "四"})
	before := s.TailHistory(2)
	// 补上结果：平衡恢复
	appendToolResultMsg(t, s, "t1")
	after := s.TailHistory(2)
	// 两次窗口不同 = 配对缓存失效后按新历史重算（旧切点会把「结果已补上」
	// 的历史仍然当悬空处理，窗口卡在更早的位置）
	same := len(before.Messages) == len(after.Messages)
	if same {
		same = true
		for i := range before.Messages {
			if before.Messages[i].Content != after.Messages[i].Content {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatalf("补上结果前后窗口必须变化（配对缓存没失效）: before=%d 条 after=%d 条", len(before.Messages), len(after.Messages))
	}
	// 补上结果后的窗口必须包含结果消息（新历史的事实）
	found := false
	for _, m := range after.Messages {
		if m.Role == "tool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("补上结果后的尾部窗口必须包含配对结果")
	}
}

// TestCacheInvalidatedOnSwitch：SwitchTo/SwitchNew 装载新历史后缓存必须重建
// （切会话正是本优化要加速的路径——命中错账等于把上一条会话的消息带给下一条）。
func TestCacheInvalidatedOnSwitch(t *testing.T) {
	s, _ := newTestSession(t)
	s.AppendForTest(llm.Message{Role: "user", Content: "旧会话消息"})
	_ = s.History() // 建缓存

	// 切新会话：历史清空
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatalf("SwitchNew: %v", err)
	}
	if got := s.History(); len(got.Messages) != 0 {
		t.Fatalf("切新会话后历史应为空（缓存失效），得到 %d 条", len(got.Messages))
	}
}

// 纯内存模式没有可独立验证的「撤回重载」路径（Rewind 需要 persist 取 seq，
// 内存模式 AppendForTest 不落库、Seq=0）——撤回后缓存失效由 store 版测试钉住
//（TestCacheInvalidatedOnStoreRewind），SwitchNew 清空覆盖「装载新历史」路径。

// TestCacheInvalidatedOnStoreRewind：带存储的撤回（重载历史）与切叶（SetLeaf
// 重载）之后 History 立刻反映新路径——重载是 s.history 整体换对象，缓存必须跟。
func TestCacheInvalidatedOnStoreRewind(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(nil, tools.New(), nil)
	s.SetEmitter(func(Event) {})
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	s.AppendForTest(llm.Message{Role: "user", Content: "一"})
	s.AppendForTest(llm.Message{Role: "assistant", Content: "答"})
	_ = s.History() // 建缓存
	seq := s.History().Messages[0].Seq

	if _, err := s.Rewind(seq); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if got := s.History(); len(got.Messages) != 0 {
		t.Fatalf("撤回后历史应为空（缓存失效），得到 %d 条", len(got.Messages))
	}
}
