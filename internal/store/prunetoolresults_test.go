// prunetoolresults_test.go —— 确定性工具结果裁剪的落库口契约（agent 层 prune_test.go
// 的存储侧钉）：按 seq 原地 UPDATE、数量不匹配拒绝、目标行不存在/非 tool 行拒绝
// （不产生半裁中间态）、session 不存在拒绝、回放读回的是占位。
package store

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func TestPruneToolResultsUpdatesRows(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	// 3 轮：assistant(调用) + tool(长结果)
	var seqs []int64
	var contents []string
	for i := 0; i < 3; i++ {
		var tc llm.ToolCall
		tc.ID, tc.Function.Name = "c"+string(rune('a'+i)), "read_file"
		tc.Function.Arguments = `{}`
		if _, err := s.AppendMsg(id, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}); err != nil {
			t.Fatal(err)
		}
		seq, err := s.AppendMsg(id, llm.Message{Role: "tool", ToolCallID: tc.ID,
			Content: strings.Repeat("结果内容", 2048)})
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
		contents = append(contents, "[工具结果已裁剪：原 8192 字节，read_file。]")
	}
	if err := s.PruneToolResults(id, seqs, contents); err != nil {
		t.Fatalf("裁剪应成功: %v", err)
	}
	msgs, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 6 {
		t.Fatalf("裁剪不得改变行数: %d", len(msgs))
	}
	pruned := 0
	for _, m := range msgs {
		if m.Role == "tool" && strings.HasPrefix(m.Content, "[工具结果已裁剪：原 ") {
			pruned++
		}
	}
	if pruned != 3 {
		t.Fatalf("回放应读到 3 条占位: %d", pruned)
	}
	// idempotent：同一批再裁一遍不报错（占位覆盖占位）
	if err := s.PruneToolResults(id, seqs, contents); err != nil {
		t.Fatalf("重复裁剪应幂等: %v", err)
	}
}

func TestPruneToolResultsRejects(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	var tc llm.ToolCall
	tc.ID, tc.Function.Name = "c1", "read_file"
	if _, err := s.AppendMsg(id, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}); err != nil {
		t.Fatal(err)
	}
	toolSeq, err := s.AppendMsg(id, llm.Message{Role: "tool", ToolCallID: "c1", Content: "结果"})
	if err != nil {
		t.Fatal(err)
	}

	// 数量不匹配
	if err := s.PruneToolResults(id, []int64{toolSeq}, nil); err == nil {
		t.Fatal("seqs 与 contents 数量不一致应拒绝")
	}
	// 目标行不存在
	if err := s.PruneToolResults(id, []int64{99999}, []string{"占位"}); err == nil {
		t.Fatal("目标行不存在应拒绝（不静默跳过）")
	}
	// 目标行不是 tool（assistant 行）
	var asstSeq int64
	msgs, _ := s.Load(id)
	for _, m := range msgs {
		if m.Role == "assistant" {
			asstSeq = m.Seq
		}
	}
	if err := s.PruneToolResults(id, []int64{asstSeq}, []string{"占位"}); err == nil {
		t.Fatal("非 tool 行应拒绝")
	}
	// session 不存在
	if err := s.PruneToolResults("no-such-session", []int64{toolSeq}, []string{"占位"}); err == nil {
		t.Fatal("session 不存在应拒绝")
	}
	// 空批 = no-op（合法：候选为空时调用方短路，但空批本身不报错）
	if err := s.PruneToolResults(id, nil, nil); err != nil {
		t.Fatalf("空批应是 no-op: %v", err)
	}
	// 失败路径之后原行未被动过（半裁中间态防护的可见性）
	msgs2, _ := s.Load(id)
	for _, m := range msgs2 {
		if m.Role == "tool" && m.Content != "结果" {
			t.Fatalf("拒绝路径不得改动任何行: %q", m.Content)
		}
	}
}
