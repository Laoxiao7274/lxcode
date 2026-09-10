package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/myt-harness/internal/llm"
)

// TestRoundTrip：Create → AppendMsg → Load 往返。
func TestRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, f, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	msgs := []llm.Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！", ToolCalls: []llm.ToolCall{{
			ID: "c1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "bash", Arguments: `{"command":"ls"}`},
		}}},
		{Role: "tool", ToolCallID: "c1", Content: "file1\nfile2"},
	}
	for _, m := range msgs {
		if err := s.AppendMsg(f, m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("往返丢消息: got %d want %d", len(got), len(msgs))
	}
	if got[0].Content != "你好" || got[2].ToolCallID != "c1" {
		t.Fatalf("往返内容不符: %+v", got)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("工具调用往返不符: %+v", got[1].ToolCalls)
	}
}

// TestTruncatedTail：断电写了一半的尾行 → 丢弃，前面的消息完好。
func TestTruncatedTail(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, f, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.AppendMsg(f, llm.Message{Role: "user", Content: "完整的第一条"})
	_ = s.AppendMsg(f, llm.Message{Role: "assistant", Content: "完整的第二条"})
	f.Close()

	// 把最后一行截掉一段（模拟断电写一半）
	path := filepath.Join(s.Dir(), id+".jsonl")
	data, _ := os.ReadFile(path)
	cut := data[:len(data)-15] // 砍进第二条消息内部
	if err := os.WriteFile(path, cut, 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.Load(id)
	if err != nil {
		t.Fatalf("截断的文件应可加载: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "完整的第一条" {
		t.Fatalf("截断后应保留完整的第一条: %+v", msgs)
	}
}

// TestList：List 返回元信息（标题、条数），按更新时间倒序。
func TestList(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)

	oldID, f1, _ := s.Create()
	_ = s.AppendMsg(f1, llm.Message{Role: "user", Content: "旧会话的问题"})
	f1.Close()

	newID, f2, _ := s.Create()
	_ = s.AppendMsg(f2, llm.Message{Role: "user", Content: "新会话的问题"})
	_ = s.AppendMsg(f2, llm.Message{Role: "assistant", Content: "回答"})
	f2.Close()

	// 显式改 mtime 让排序确定（文件系统时间戳可能同秒）
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, oldID+".jsonl"), past, past)

	items, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("应列出 2 个会话: %+v", items)
	}
	if items[0].ID != newID || items[1].ID != oldID {
		t.Fatalf("应按更新时间倒序（新的在前）: %+v", items)
	}
	if items[0].Title != "新会话的问题" || items[0].Messages != 2 {
		t.Fatalf("元信息不符: %+v", items[0])
	}
	if items[1].Messages != 1 {
		t.Fatalf("旧会话消息数不符: %+v", items[1])
	}
}

// TestLatest：最近会话被找到；空目录返回空。
func TestLatest(t *testing.T) {
	// 空目录 → 空结果（全新开始路径）
	empty, _ := Open(t.TempDir())
	id, msgs, err := empty.Latest()
	if err != nil || id != "" || msgs != nil {
		t.Fatalf("空目录应返回空: id=%q msgs=%v err=%v", id, msgs, err)
	}

	// 有会话 → 最近那个
	s, _ := Open(t.TempDir())
	oldID, f1, _ := s.Create()
	_ = s.AppendMsg(f1, llm.Message{Role: "user", Content: "old"})
	f1.Close()
	newID, f2, _ := s.Create()
	_ = s.AppendMsg(f2, llm.Message{Role: "user", Content: "new"})
	f2.Close()
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(s.Dir(), oldID+".jsonl"), past, past)

	id, msgs, err = s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if id != newID || len(msgs) != 1 || msgs[0].Content != "new" {
		t.Fatalf("应恢复最近会话: id=%s msgs=%+v", id, msgs)
	}
}

// TestNonSessionFilesIgnored：目录里的非会话文件（无 meta 行）不进列表。
func TestNonSessionFilesIgnored(t *testing.T) {
	s, _ := Open(t.TempDir())
	// 一个正常会话
	id, f, _ := s.Create()
	_ = s.AppendMsg(f, llm.Message{Role: "user", Content: "x"})
	f.Close()
	// 一个垃圾文件（不是会话格式）
	_ = os.WriteFile(filepath.Join(s.Dir(), "garbage.jsonl"), []byte("not json\n"), 0o644)
	// 一个非 .jsonl 文件
	_ = os.WriteFile(filepath.Join(s.Dir(), "readme.txt"), []byte("x"), 0o644)

	items, _ := s.List()
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("应只列出 1 个有效会话: %+v", items)
	}
}

// TestSearch：跨会话正则搜索，按会话新旧排序返回。
func TestSearch(t *testing.T) {
	s, _ := Open(t.TempDir())
	id1, f1, _ := s.Create()
	_ = s.AppendMsg(f1, llm.Message{Role: "user", Content: "上次说端口被占了"})
	_ = s.AppendMsg(f1, llm.Message{Role: "assistant", Content: "7788 被 myt-agent 占着"})
	f1.Close()

	hits, err := s.Search("端口", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != id1 {
		t.Fatalf("搜索命中不符: %+v", hits)
	}

	out := FormatSearchHits(hits, len(hits))
	if !strings.Contains(out, "端口") {
		t.Fatalf("格式化输出应含命中内容: %q", out)
	}

	// 无命中
	hits, err = s.Search("不存在的词xyz", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("无命中应返回空: %+v err=%v", hits, err)
	}
}
