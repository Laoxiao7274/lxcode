// HistoryBefore（chat.history 的 before_seq 参数）单测：按 seq 定位的前缀窗口、
// 配平回退、检查点平移、has_more / first_seq 语义。与 tailhistory_test.go 同一套
// 钉子纪律：分页窗口同样必须配平，不能切出 assistant 的 tool_calls 悬空的畸形历史。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// beforePairingHistory = tailPairingHistory 加显式 seq（1..6），另加几条尾部消息
// 证明 anchor 之后的内容不进窗口。
func beforePairingHistory() []llm.Message {
	msgs := tailPairingHistory()
	msgs = append(msgs,
		llm.Message{Role: "assistant", Content: "第三答", Seq: 7},
		llm.Message{Role: "user", Content: "第三问", Seq: 8},
	)
	return msgs
}

func TestHistoryBeforeTrimsAtAnchorWithBalancedCut(t *testing.T) {
	s := tailTestSession(t, beforePairingHistory())
	// anchor = seq 5（「回答」那条 assistant）：窗口 = 之前 n=50 条 → 从头到 seq<5。
	got, hasMore, firstSeq, err := s.HistoryBefore(5, 50)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("anchor 前 50 条应得 4 条（seq 1..4），得到 %d", len(got.Messages))
	}
	if got.Messages[len(got.Messages)-1].Seq != 4 {
		t.Fatalf("窗口末条应为 seq 4，得到 %d", got.Messages[len(got.Messages)-1].Seq)
	}
	// anchor 之前从第一条起 → 没有更早的了
	if hasMore {
		t.Fatal("anchor 前已到历史开头，has_more 应为 false")
	}
	if firstSeq != 1 {
		t.Fatalf("first_seq 应为 1，得到 %d", firstSeq)
	}
}

func TestHistoryBeforeRecedesToBalancedCut(t *testing.T) {
	s := tailTestSession(t, beforePairingHistory())
	// anchor = seq 7：窗口 = seq<7 的尾部 3 条 → 起点下标 3（seq 4 的 tool 结果之后）
	// 是不平衡切点（c2 未回填）→ 回退到最近的平衡切点（下标 1 之后）→
	// 窗口 = messages[1..5] 共 5 条，首条是带 2 个 tool_calls 的 assistant。
	got, hasMore, firstSeq, err := s.HistoryBefore(7, 3)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got.Messages) != 5 {
		t.Fatalf("配平回退后窗口应为 5 条，得到 %d", len(got.Messages))
	}
	if got.Messages[0].Role != "assistant" || len(got.Messages[0].ToolCalls) != 2 {
		t.Fatalf("窗口首条应是带 2 个调用的 assistant，得到 %+v", got.Messages[0])
	}
	if !hasMore {
		t.Fatal("起点 1 > 0，has_more 应为 true")
	}
	// 配平回退后首条是 assistant（seq 2）——first_seq 报窗口首条真实 seq
	if firstSeq != 2 {
		t.Fatalf("first_seq 应为 2，得到 %d", firstSeq)
	}
}

func TestHistoryBeforeShiftsCheckpoints(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "旧的", Seq: 1},
		{Role: "assistant", Content: "旧的答", Seq: 2},
		{Role: "user", Content: "<compacted-summary>摘要</compacted-summary>", Seq: 3},
		{Role: "assistant", Content: "新的答", Seq: 4},
		{Role: "user", Content: "后来的问", Seq: 5},
	}
	s := tailTestSession(t, msgs)
	// 全量：检查点在下标 2
	if snap := s.History(); len(snap.Checkpoints) != 1 || snap.Checkpoints[0] != 2 {
		t.Fatalf("前置：检查点应在下标 2，得到 %v", snap.Checkpoints)
	}
	// anchor = seq 5，n=50：窗口 = seq 1..4，检查点平移到下标 2（窗口内原位）
	got, _, _, err := s.HistoryBefore(5, 50)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got.Checkpoints) != 1 || got.Checkpoints[0] != 2 {
		t.Fatalf("窗口内检查点应保持下标 2，得到 %v", got.Checkpoints)
	}
	// anchor = seq 4（检查点之前）：窗口 = seq 1..3（anchor 前的全部 3 条），
	// 检查点（seq 3）是 anchor 前的最后一条 → 在窗口**内**，下标 2 原位保留
	got, _, _, err = s.HistoryBefore(4, 50)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("anchor=seq4 的窗口应 3 条（seq 1..3），得到 %d", len(got.Messages))
	}
	if got.Messages[len(got.Messages)-1].Seq != 3 {
		t.Fatalf("窗口末条应是 seq 3（检查点），得到 %d", got.Messages[len(got.Messages)-1].Seq)
	}
	if len(got.Checkpoints) != 1 || got.Checkpoints[0] != 2 {
		t.Fatalf("窗口内检查点下标应保持 2，得到 %v", got.Checkpoints)
	}
	// 窗口裁到更小（n=2）：起点 = 1（平衡），窗口 = [seq2, seq3]，
	// 检查点平移到下标 1；seq1 被裁掉（has_more=true）
	got2, hasMore2, _, err2 := s.HistoryBefore(4, 2)
	if err2 != nil {
		t.Fatalf("HistoryBefore: %v", err2)
	}
	if !hasMore2 {
		t.Fatal("n=2 时前面还有 seq1，has_more 应为 true")
	}
	if len(got2.Checkpoints) != 1 || got2.Checkpoints[0] != 1 {
		t.Fatalf("n=2 窗口内检查点应平移到下标 1，得到 %v", got2.Checkpoints)
	}
}

func TestHistoryBeforeUnknownAnchorIsError(t *testing.T) {
	s := tailTestSession(t, beforePairingHistory())
	// seq 不在历史里：错误（不静默返回全量——客户端只信任自己记录的 seq，
	// 错误值会错位出重复或空洞）
	if _, _, _, err := s.HistoryBefore(999, 50); err == nil {
		t.Fatal("不存在的 seq 应报错")
	}
	if _, _, _, err := s.HistoryBefore(0, 50); err == nil {
		t.Fatal("seq=0 应报错")
	}
	if _, _, _, err := s.HistoryBefore(-1, 50); err == nil {
		t.Fatal("负 seq 应报错")
	}
	// n<=0 同样是参数错误（分页必须有窗口大小）
	if _, _, _, err := s.HistoryBefore(5, 0); err == nil {
		t.Fatal("n=0 应报错")
	}
}

func TestHistoryBeforeEmptyHistoryIsError(t *testing.T) {
	s := tailTestSession(t, nil)
	if _, _, _, err := s.HistoryBefore(1, 50); err == nil {
		t.Fatal("空历史应报错")
	}
}

func TestHistoryBeforeKeepsMetadataUnchanged(t *testing.T) {
	s := tailTestSession(t, beforePairingHistory())
	s.todos = []tools.TodoItem{{Content: "修 bug", Status: "active"}}
	full := s.History()
	got, _, _, err := s.HistoryBefore(7, 2)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got.Todos) != len(full.Todos) {
		t.Fatalf("todo 不应随窗口裁剪：全量 %d，窗口 %d", len(full.Todos), len(got.Todos))
	}
	if got.Busy != full.Busy {
		t.Fatal("busy 不应随窗口变化")
	}
	if got.Context.Used != full.Context.Used {
		t.Fatal("占用与全量不一致（窗口是传输裁剪，不改占用）")
	}
	if got.SessionID != full.SessionID {
		t.Fatal("session_id 不应随窗口变化")
	}
}
