// store_test.go —— SQLite 会话存储的行为测试（从 JSONL 版平移 + SQLite 特有场景）。
// 断言的是对外契约：创建/追加/加载/列表排序/最近恢复/搜索/重命名/归档——
// 不关心表结构细节。
package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndLoad(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		{Role: "user", Content: "第一句"},
		{Role: "assistant", Content: "回复", ReasoningContent: "思考", ToolCalls: []llm.ToolCall{
			{ID: "t1", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "read_file", Arguments: `{"path":"a.txt"}`}},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "内容"},
	}
	for _, m := range msgs {
		if err := s.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("消息数: got %d want %d", len(got), len(msgs))
	}
	// 字段往返：顺序 + 内容 + 思考链 + 工具调用（含嵌套 function）
	if got[0].Content != "第一句" || got[1].ReasoningContent != "思考" {
		t.Fatalf("字段往返不符: %+v %+v", got[0], got[1])
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Function.Name != "read_file" ||
		got[1].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("工具调用往返不符: %+v", got[1].ToolCalls)
	}
	if got[2].ToolCallID != "t1" {
		t.Fatalf("tool_call_id 丢失: %+v", got[2])
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs, err := s.Load(id)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("空会话应无消息: %v %d", err, len(msgs))
	}
	if _, err := s.Load("不存在"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
}

func TestLatest(t *testing.T) {
	s := openTestStore(t)
	// 空库：全新开始
	if id, _, _ := s.Latest(); id != "" {
		t.Fatalf("空库应无最近会话, got %q", id)
	}
	id1, _ := s.Create()
	if err := s.AppendMsg(id1, llm.Message{Role: "user", Content: "先"}); err != nil {
		t.Fatal(err)
	}
	// id2 更晚写入 → Latest 应指向 id2
	id2, _ := s.Create()
	if err := s.AppendMsg(id2, llm.Message{Role: "user", Content: "后"}); err != nil {
		t.Fatal(err)
	}
	got, msgs, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got != id2 {
		t.Fatalf("最近会话: got %s want %s", got, id2)
	}
	if len(msgs) != 1 || msgs[0].Content != "后" {
		t.Fatalf("最近会话消息不符: %+v", msgs)
	}
	// 归档的会话不作为恢复目标
	if err := s.Archive(id2, true); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := s.Latest()
	if got2 != id1 {
		t.Fatalf("归档后最近应为 id1: got %s", got2)
	}
}

func TestListOrderAndTitle(t *testing.T) {
	s := openTestStore(t)
	idA, _ := s.Create()
	if err := s.AppendMsg(idA, llm.Message{Role: "user", Content: "标题来源\n第二行"}); err != nil {
		t.Fatal(err)
	}
	idB, _ := s.Create()
	if err := s.AppendMsg(idB, llm.Message{Role: "user", Content: "更近的会话"}); err != nil {
		t.Fatal(err)
	}
	metas, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("列表数: %d", len(metas))
	}
	if metas[0].ID != idB {
		t.Fatalf("最近应排最前: %s", metas[0].ID)
	}
	if metas[1].Title != "标题来源" {
		t.Fatalf("标题=首条 user 消息首行: %q", metas[1].Title)
	}
	if metas[0].Messages != 1 || metas[1].Messages != 1 {
		t.Fatalf("消息计数: %+v", metas)
	}
}

func TestRenameAndArchive(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	if err := s.AppendMsg(id, llm.Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(id, "  新名字  "); err != nil {
		t.Fatal(err)
	}
	metas, _ := s.List()
	if metas[0].Title != "新名字" {
		t.Fatalf("重命名生效: %q", metas[0].Title)
	}
	if err := s.Rename(id, "  "); err == nil {
		t.Fatal("空标题应拒绝")
	}
	if err := s.Rename("不存在", "x"); err == nil {
		t.Fatal("不存在会话应拒绝")
	}
	if err := s.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	metas, _ = s.List()
	if len(metas) != 1 {
		t.Fatalf("归档后仍应列出（沉底）: %+v", metas)
	}
	if err := s.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	metas, _ = s.List()
	if len(metas) != 1 || metas[0].Title != "新名字" {
		t.Fatalf("取消归档应恢复: %+v", metas)
	}
}

func TestSearch(t *testing.T) {
	s := openTestStore(t)
	id1, _ := s.Create()
	if err := s.AppendMsg(id1, llm.Message{Role: "user", Content: "Go 语言怎么样"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMsg(id1, llm.Message{Role: "assistant", Content: "很好"}); err != nil {
		t.Fatal(err)
	}
	id2, _ := s.Create()
	if err := s.AppendMsg(id2, llm.Message{Role: "user", Content: "聊聊 Go 的并发"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search("Go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("命中数: %d", len(hits))
	}
	// 按会话更新时间从近到远：id2 的在前
	if hits[0].SessionID != id2 || hits[0].Index != 1 || hits[0].Role != "user" {
		t.Fatalf("命中排序/序号不符: %+v", hits[0])
	}
	// 坏正则报错
	if _, err := s.Search("[", 10); err == nil {
		t.Fatal("坏正则应报错")
	}
	// 无命中
	hits, _ = s.Search("不存在的词", 10)
	if len(hits) != 0 {
		t.Fatalf("无命中: %d", len(hits))
	}
	// max 钳制
	hits, _ = s.Search("Go", 1)
	if len(hits) != 1 {
		t.Fatalf("max=1: %d", len(hits))
	}
}

// TestSearchExcludesArchived 归档会话不进搜索结果。
func TestSearchExcludesArchived(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	if err := s.AppendMsg(id, llm.Message{Role: "user", Content: "唯一关键词"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search("唯一关键词", 10)
	if len(hits) != 0 {
		t.Fatalf("归档会话不应被搜到: %+v", hits)
	}
}

// TestConcurrentAppend 并发写：WAL + busy_timeout 下多 goroutine 追加不炸、不丢。
// （单会话的写入在 agent 层由 s.mu 串行；这里是存储层自身的并发面。）
func TestConcurrentAppend(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.AppendMsg(id, llm.Message{Role: "user", Content: fmt.Sprintf("m%d", i)})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发追加失败: %v", err)
		}
	}
	msgs, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != n {
		t.Fatalf("并发写丢失: got %d want %d", len(msgs), n)
	}
}

// TestReopenPersistence 重启恢复：关库重开后数据仍在（WAL 回放语义）。
func TestReopenPersistence(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s1.Create()
	if err := s1.AppendMsg(id, llm.Message{Role: "user", Content: "重启前的消息"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	// 重开（同目录）——durable 语义
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	msgs, err := s2.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "重启前的消息" {
		t.Fatalf("重启恢复失败: %+v", msgs)
	}
	got, _, _ := s2.Latest()
	if got != id {
		t.Fatalf("重启后最近会话丢失: %q", got)
	}
}

// TestOpenCreatesDir 会话目录不存在时自动建（安装形态首启）。
func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("应自动创建目录: %v", err)
	}
	defer s.Close()
	if _, err := s.Create(); err != nil {
		t.Fatal(err)
	}
}
