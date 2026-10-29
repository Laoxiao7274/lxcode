// TailHistory（chat.history 的 tail 参数）单测：配平回退、检查点平移、
// 全量等价、投影占用不变。背景：公网 frp 隧道下全量大会话历史（1.4~1.7MB）
// 过 10 Mbit/s 限速隧道要秒级，慢链路客户端先拉尾部。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// tailPairingHistory 是带工具配对的历史：user → assistant(tool_calls×2) →
// tool → tool → assistant → user。
func tailPairingHistory() []llm.Message {
	return []llm.Message{
		{Role: "user", Content: "第一问", Seq: 1},
		{Role: "assistant", Content: "", Seq: 2, ToolCalls: []llm.ToolCall{
			{ID: "c1", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "read_file", Arguments: "{}"}},
			{ID: "c2", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "search", Arguments: "{}"}},
		}},
		{Role: "tool", Content: "结果1", Seq: 3, ToolCallID: "c1"},
		{Role: "tool", Content: "结果2", Seq: 4, ToolCallID: "c2"},
		{Role: "assistant", Content: "回答", Seq: 5},
		{Role: "user", Content: "第二问", Seq: 6},
	}
}

func tailTestSession(t *testing.T, msgs []llm.Message) *Session {
	t.Helper()
	s := New(nil, tools.New(), func(Event) {})
	s.history = msgs
	return s
}

func TestTailHistoryZeroOrOversizeIsFull(t *testing.T) {
	s := tailTestSession(t, tailPairingHistory())
	for _, n := range []int{0, -1, 100} {
		got := s.TailHistory(n)
		if len(got.Messages) != 6 {
			t.Fatalf("tail=%d: 期望全量 6 条，得到 %d", n, len(got.Messages))
		}
		full := s.History()
		if got.Context.Used != full.Context.Used {
			t.Fatalf("tail=%d: 占用与全量不一致（窗口是传输裁剪，不改占用）", n)
		}
	}
}

func TestTailHistoryTrimsAndShiftsCheckpoints(t *testing.T) {
	msgs := append([]llm.Message{
		// 头部一个检查点（窗口外）+ 一条普通消息
		{Role: "user", Content: "<compacted-summary>摘要</compacted-summary>", Seq: 1},
		{Role: "assistant", Content: "旧回答", Seq: 2},
	}, tailPairingHistory()...)
	s := tailTestSession(t, msgs)
	snap := s.History()
	if len(snap.Checkpoints) == 0 || snap.Checkpoints[0] != 0 {
		t.Fatalf("前置：检查点应标在下标 0，得到 %v", snap.Checkpoints)
	}
	got := s.TailHistory(5)
	if len(got.Messages) != 5 {
		t.Fatalf("期望尾部 5 条，得到 %d", len(got.Messages))
	}
	// 检查点（下标 0）在窗口外：平移后不得出现
	if len(got.Checkpoints) != 0 {
		t.Fatalf("窗口外的检查点应被丢弃，得到 %v", got.Checkpoints)
	}
	// n=5 → start=3 = 前 3 条之后（tool 结果1 之后）open=1（c2 未回填）不平衡
	// → 回退到最近的平衡切点（前 1 条之后，open=0）→ 窗口 = messages[1:] 共 5 条，
	// 首条是带 2 个 tool_calls 的 assistant。
	if len(got.Messages) != 5 {
		t.Fatalf("配平回退后窗口应为 5 条，得到 %d", len(got.Messages))
	}
	if got.Messages[0].Role != "assistant" || len(got.Messages[0].ToolCalls) != 2 {
		t.Fatalf("窗口首条应是带 2 个调用的 assistant，得到 %+v", got.Messages[0])
	}
}

func TestTailHistoryRecedesToBalancedCut(t *testing.T) {
	msgs := tailPairingHistory()
	s := tailTestSession(t, msgs)
	// n=3 → start=3 正好落在 assistant(tool_calls) 与 tool 结果之间的不平衡切点：
	// 必须回退到 start=2（user 与 assistant 之间？不——平衡切点是 2 之后：
	// [user, assistant, tool] 各切点里 2 之后 open=1 不平衡；正确回退点是 1 之后
	// 或 3 之后。start=3 的切点 = 前 3 条之后 = tool 结果1 之后，open=1（c2 未回）
	// 不平衡 → 回退到最近的平衡点 = 1 之后（open=0）。窗口 = [user, assistant]——
	// 仍带着未回填的 c1/c2 调用……不对：回退到 1 之后窗口是 5 条，不是 3。
	got := s.TailHistory(3)
	// 配平回退让窗口可能比 n 大（保证起点平衡）——这里 1 之后平衡，窗口 = 下标
	// 1..5 共 5 条？不：回退的是**起点**，窗口 = messages[start:]，起点 1 → 5 条。
	if len(got.Messages) != 5 {
		t.Fatalf("配平回退后窗口应为 5 条（起点回退到平衡切点 1），得到 %d", len(got.Messages))
	}
	// 起点是 assistant（带 tool_calls），它有 2 个调用；历史里调用在窗口内的
	// 配对完整性由 AnalyzeToolPairing 判定：窗口内 assistant 开了 2 个调用但
	// tool 结果不在窗口里——**这是允许的**：切点平衡性看的是「切点处」没有
	// 悬空调用横跨，起点在 assistant 之前意味着调用整体在窗口内……重新核对：
	// start=1 之后窗口含 assistant(c1,c2)+tool+tool+assistant+user？n=3 start=3
	// → 平衡点 1 → messages[1:6] = 5 条，首条是 assistant(c1,c2)。
	if got.Messages[0].Role != "assistant" || len(got.Messages[0].ToolCalls) != 2 {
		t.Fatalf("窗口首条应是带 2 个调用的 assistant，得到 %v", got.Messages[0])
	}
}

func TestTailHistoryKeepsPendingAndTodos(t *testing.T) {
	s := tailTestSession(t, tailPairingHistory())
	s.todos = []tools.TodoItem{{Content: "修 bug", Status: "active"}}
	full := s.History()
	tail := s.TailHistory(2)
	if len(tail.Todos) != len(full.Todos) {
		t.Fatalf("todo 不应随窗口裁剪：全量 %d，尾部 %d", len(full.Todos), len(tail.Todos))
	}
	if tail.Busy != full.Busy {
		t.Fatalf("busy 不应随窗口变化")
	}
}

func TestTailHistoryCheckpointInsideWindowShiftsToZero(t *testing.T) {
	// 检查点在窗口内：下标平移后应为 0
	msgs := []llm.Message{
		{Role: "user", Content: "旧的", Seq: 1},
		{Role: "assistant", Content: "旧的答", Seq: 2},
		{Role: "user", Content: "<compacted-summary>摘要</compacted-summary>", Seq: 3},
		{Role: "assistant", Content: "新的答", Seq: 4},
	}
	s := tailTestSession(t, msgs)
	got := s.TailHistory(2)
	if len(got.Messages) != 2 || got.Messages[0].Role != "user" {
		t.Fatalf("窗口应为 [检查点, assistant]，得到 %d 条", len(got.Messages))
	}
	if len(got.Checkpoints) != 1 || got.Checkpoints[0] != 0 {
		t.Fatalf("窗口内检查点下标应平移为 0，得到 %v", got.Checkpoints)
	}
}
