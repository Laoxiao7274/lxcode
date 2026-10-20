// read_file 去重的会话级接线（tools 层单测之外的那一半）：
// runTurn 必须把**会话自己那份** ReadState 注入 ctx——否则去重只在单测里
// 成立，真链路里每轮都是新的空记录，一次也不会命中。
//
// 这里跑两轮真实工具循环（同一个文件、同一分页参数），断言第二轮拿到的是
// 摘要而不是全文；同时断言父会话的观测不会外溢到子会话（子会话是独立
// Session，自己那份 ReadState 是空的）。
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func writeDedupFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dedup.txt")
	var sb strings.Builder
	for i := 1; i <= 6; i++ {
		sb.WriteString("row-")
		sb.WriteByte(byte('0' + i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// lastToolResult 从历史里取最后一次工具结果的正文。
func lastToolResult(t *testing.T, s *Session) string {
	t.Helper()
	h := s.History()
	for i := len(h.Messages) - 1; i >= 0; i-- {
		if h.Messages[i].Role == "tool" {
			return h.Messages[i].Content
		}
	}
	t.Fatal("历史里没有工具结果")
	return ""
}

func TestReadDedupAcrossTurnsInSameSession(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	path := writeDedupFile(t)
	args := `{"path":` + quoteJSON(path) + `}`
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", args),
		textResult("第一轮读完"),
		toolCallResult("read_file", args),
		textResult("第二轮再读"),
	}}
	s.stream = fake.stream

	if err := s.Send("读一下这个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := lastToolResult(t, s); !strings.Contains(got, "1→row-1") {
		t.Fatalf("第一轮应拿到全文: %q", got)
	}

	if err := s.Send("再读一遍同一个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	got := lastToolResult(t, s)
	if !strings.Contains(got, "内容未变") {
		t.Fatalf("同一会话第二轮同区间重读应回摘要（ReadState 没接进 ctx？）: %q", got)
	}
	if strings.Contains(got, "1→row-1") {
		t.Fatalf("摘要不该再带全文: %q", got)
	}
}

// 子会话是独立 Session：父会话读过的文件不能替它满足「读过了」，
// 它第一次读必须拿到全文（去重的作用域纪律）。
func TestReadDedupDoesNotLeakToChildSession(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	path := writeDedupFile(t)
	args := `{"path":` + quoteJSON(path) + `}`
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", args),
		textResult("父会话读完"),
	}}
	s.stream = fake.stream
	if err := s.Send("读一下"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := lastToolResult(t, s); !strings.Contains(got, "row-1") {
		t.Fatalf("父会话首读应拿到全文: %q", got)
	}

	child := New(reg, s.tools, nil)
	childFake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", args),
		textResult("子会话读完"),
	}}
	child.stream = childFake.stream
	if err := child.Send("子任务：读同一个文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !child.Busy() })
	got := lastToolResult(t, child)
	if !strings.Contains(got, "row-1") {
		t.Fatalf("子会话首读不该被父会话的观测去重: %q", got)
	}
}

// quoteJSON 生成 JSON 字符串字面量（路径含反斜杠时转义）。
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
