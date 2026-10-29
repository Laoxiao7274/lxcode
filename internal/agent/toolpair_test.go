package agent

import (
	"reflect"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// callMsg 造一条带 N 个工具调用的 assistant 消息。
func callMsg(ids ...string) llm.Message {
	m := llm.Message{Role: "assistant"}
	for _, id := range ids {
		tc := llm.ToolCall{ID: id}
		tc.Function.Name = "read_file"
		tc.Function.Arguments = "{}"
		m.ToolCalls = append(m.ToolCalls, tc)
	}
	return m
}

// resultMsg 造一条工具结果消息。
func resultMsg(id string) llm.Message {
	return llm.Message{Role: "tool", ToolCallID: id, Content: "ok"}
}

// ---------- 切点平衡 ----------

func TestToolPairingCutsNormalRound(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: "做事"},
		callMsg("c1", "c2"),
		resultMsg("c1"),
		resultMsg("c2"),
		{Role: "assistant", Content: "做完了"},
	}
	p := AnalyzeToolPairing(history)
	want := []bool{true, true, false, false, true, true}
	if !reflect.DeepEqual(p.Cuts, want) {
		t.Fatalf("切点平衡不符:\n got %v\nwant %v", p.Cuts, want)
	}
	if len(p.Unpaired) != 0 || len(p.Orphans) != 0 {
		t.Fatalf("正常轮次不应有配对诊断: unpaired=%v orphans=%v", p.Unpaired, p.Orphans)
	}
	// 语义核对：调用声明之后不可切，两个结果都回填之后才可切
	if p.BalancedAfter(1) {
		t.Fatal("assistant 声明了两个调用之后不可切")
	}
	if !p.BalancedAfter(3) {
		t.Fatal("两个结果都回填之后可切")
	}
}

// 用户库里真实存在的残缺会话：assistant 声明了调用，但用户取消了、没有结果。
func TestToolPairingDetectsUnpairedCall(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: "跑个长命令"},
		callMsg("c1"),
		{Role: "user", Content: "继续"},
	}
	p := AnalyzeToolPairing(history)
	if p.BalancedBefore(len(history)) {
		t.Fatal("有未回填的调用时，历史末尾不是安全切割点")
	}
	if !reflect.DeepEqual(p.Unpaired, []string{"c1"}) {
		t.Fatalf("应精确报出未配对的调用 id: %v", p.Unpaired)
	}
	// 只有开头（空历史）可切——整段历史都横跨着一个未完成的调用
	if got := p.NearestBalancedAtOrBefore(len(history)); got != 1 {
		t.Fatalf("最近平衡切点应是声明调用之前: got %d", got)
	}
}

func TestToolPairingOrphanResultIsRecordedNotFatal(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: "x"},
		resultMsg("ghost"),
	}
	p := AnalyzeToolPairing(history)
	if !reflect.DeepEqual(p.Orphans, []int{1}) {
		t.Fatalf("无前置调用的结果应记录下标: %v", p.Orphans)
	}
	// 钳到 0：游标为负会让之后所有切点失去意义，可用信号优先
	if !p.BalancedBefore(2) {
		t.Fatalf("孤儿结果之后应保持平衡（钳到 0）: %v", p.Cuts)
	}
}

// id 错配：计数（管安全）与 id 诊断（管精确）刻意分叉。
func TestToolPairingIDMismatchKeepsUnpairedDiagnostic(t *testing.T) {
	history := []llm.Message{callMsg("c1"), resultMsg("other")}
	p := AnalyzeToolPairing(history)
	if !p.BalancedBefore(2) {
		t.Fatalf("计数已回填（+1 −1）——切点应平衡: %v", p.Cuts)
	}
	if !reflect.DeepEqual(p.Unpaired, []string{"c1"}) {
		t.Fatalf("id 对不上时那个调用仍算未配对（不静默吞掉）: %v", p.Unpaired)
	}
}

func TestToolPairingEmptyAndNearestCut(t *testing.T) {
	p := AnalyzeToolPairing(nil)
	if !reflect.DeepEqual(p.Cuts, []bool{true}) {
		t.Fatalf("空历史的唯一切点应平衡: %v", p.Cuts)
	}
	if got := p.NearestBalancedAtOrBefore(0); got != 0 {
		t.Fatalf("空历史最近平衡切点应为 0: %d", got)
	}
	// 越界索引钳到尾部；最坏情况回落到 0（Cuts[0] 恒真，不会返回 -1）
	history := []llm.Message{callMsg("c1"), resultMsg("c1")}
	q := AnalyzeToolPairing(history)
	if got := q.NearestBalancedAtOrBefore(99); got != 2 {
		t.Fatalf("越界应钳到末尾切点: %d", got)
	}
	open := AnalyzeToolPairing([]llm.Message{{Role: "user", Content: "x"}, callMsg("c1")})
	if got := open.NearestBalancedAtOrBefore(2); got != 1 {
		t.Fatalf("应回退到声明之前: %d", got)
	}
	// 负索引与非平衡查询的防御语义
	if open.BalancedBefore(-1) || open.BalancedBefore(99) {
		t.Fatal("越界查询应返回 false（不可切）而不是猜")
	}
}
