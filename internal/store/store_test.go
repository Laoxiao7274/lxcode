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
		if _, err := s.AppendMsg(id, m); err != nil {
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
	if _, err := s.AppendMsg(id1, llm.Message{Role: "user", Content: "先"}); err != nil {
		t.Fatal(err)
	}
	// id2 更晚写入 → Latest 应指向 id2
	id2, _ := s.Create()
	if _, err := s.AppendMsg(id2, llm.Message{Role: "user", Content: "后"}); err != nil {
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
	if _, err := s.AppendMsg(idA, llm.Message{Role: "user", Content: "标题来源\n第二行"}); err != nil {
		t.Fatal(err)
	}
	idB, _ := s.Create()
	if _, err := s.AppendMsg(idB, llm.Message{Role: "user", Content: "更近的会话"}); err != nil {
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
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "hello"}); err != nil {
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
	if _, err := s.AppendMsg(id1, llm.Message{Role: "user", Content: "Go 语言怎么样"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id1, llm.Message{Role: "assistant", Content: "很好"}); err != nil {
		t.Fatal(err)
	}
	id2, _ := s.Create()
	if _, err := s.AppendMsg(id2, llm.Message{Role: "user", Content: "聊聊 Go 的并发"}); err != nil {
		t.Fatal(err)
	}
	hits, _, err := s.Search(SearchQuery{Pattern: "Go", Max: 10})
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
	if _, _, err := s.Search(SearchQuery{Pattern: "[", Max: 10}); err == nil {
		t.Fatal("坏正则应报错")
	}
	// 无命中
	hits, _, _ = s.Search(SearchQuery{Pattern: "不存在的词", Max: 10})
	if len(hits) != 0 {
		t.Fatalf("无命中: %d", len(hits))
	}
	// max 钳制
	hits, _, _ = s.Search(SearchQuery{Pattern: "Go", Max: 1})
	if len(hits) != 1 {
		t.Fatalf("max=1: %d", len(hits))
	}
}

// TestSearchExcludesArchived 归档会话不进搜索结果。
func TestSearchExcludesArchived(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "唯一关键词"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	hits, _, _ := s.Search(SearchQuery{Pattern: "唯一关键词", Max: 10})
	if len(hits) != 0 {
		t.Fatalf("归档会话不应被搜到: %+v", hits)
	}
}

// TestSearchContextAndRole：命中上下文的组装、role 过滤、总命中数。
//
// 三件事各自的价值：
//
//	① 上下文——命中行常常只是「提问」，「怎么修的」在它后面几条；只给一行
//	   的话模型还得再搜一次才拼得出前因后果；
//	② role 过滤只作用于命中判定、不作用于上下文——过滤掉的行仍要出现在上下文里
//	   （否则 role=user 时上下文里只剩用户自己的话，恰好丢掉最该看的助手答复）；
//	③ total 要数全部命中而不是 len(命中)——否则「还有更多，缩小 pattern」那句
//	   提示永远不会出现。
func TestSearchContextAndRole(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	for _, m := range []llm.Message{
		{Role: "user", Content: "第一句 开场"},
		{Role: "assistant", Content: "第二句 回应"},
		{Role: "tool", Content: "第三句 工具输出"},
		{Role: "user", Content: "第四句 关键词在这里"},
		{Role: "assistant", Content: "第五句 修好了"},
		{Role: "user", Content: "第六句 收尾"},
	} {
		if _, err := s.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
	}

	// ① 上下文：命中第 4 条，前后各 1 条 → 应含 #3 #4 #5，按时间顺序
	hits, total, err := s.Search(SearchQuery{Pattern: "关键词", Max: 10, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || total != 1 {
		t.Fatalf("应 1 条命中: hits=%d total=%d", len(hits), total)
	}
	got := hits[0].Context
	if len(got) != 3 {
		t.Fatalf("前后各 1 条应得 3 行（含命中自身）: %+v", got)
	}
	for i, wantIdx := range []int{3, 4, 5} {
		if got[i].Index != wantIdx {
			t.Fatalf("上下文顺序错（应 #3 #4 #5）: %+v", got)
		}
	}
	if got[1].Role != "user" || got[2].Role != "assistant" {
		t.Fatalf("命中/后文角色不符: %+v", got)
	}
	// 会话标题与时间要带出来（模型据此判断是哪个会话）
	if hits[0].SessionTitle == "" || hits[0].UpdatedAt == "" {
		t.Fatalf("命中应带会话标题与时间: %+v", hits[0])
	}

	// ② role 过滤只作用于命中判定：role=user 命中两条（#1 与 #4），
	//    但上下文里仍应看得到 assistant/tool 的行。
	hits, total, err = s.Search(SearchQuery{Pattern: "句", Max: 10, Role: "user", Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("role=user 应命中 3 条（#1 #4 #6），实际 %d", total)
	}
	if len(hits) != 3 {
		t.Fatalf("应返回 3 条: %d", len(hits))
	}
	roles := map[string]bool{}
	for _, c := range hits[0].Context {
		roles[c.Role] = true
	}
	if !roles["assistant"] {
		t.Fatalf("上下文不该被 role 过滤掉（否则丢掉最该看的答复）: %+v", hits[0].Context)
	}

	// ③ 总数不受 max 限制：max=1 时只回 1 条，但 total 仍是全部命中数。
	hits, total, err = s.Search(SearchQuery{Pattern: "句", Max: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("max=1 应只回 1 条: %d", len(hits))
	}
	if total != 6 {
		t.Fatalf("total 应数全部命中（否则「还有更多」提示是死的）: %d", total)
	}
}

// TestProjectByID 按项目 id 查询（归属 → 项目根目录的解析源）。
func TestProjectByID(t *testing.T) {
	s := openTestStore(t)
	saved, err := s.AddProject("lxcode", `C:\proj\lxcode`)
	if err != nil {
		t.Fatal(err)
	}
	meta, found, err := s.ProjectByID(saved.ID)
	if err != nil || !found {
		t.Fatalf("应查到项目: found=%v err=%v", found, err)
	}
	if meta.Path != `C:\proj\lxcode` || meta.Name != "lxcode" {
		t.Fatalf("项目字段不符: %+v", meta)
	}
	// 不存在是 found=false 而不是错误
	_, found, err = s.ProjectByID("不存在")
	if err != nil || found {
		t.Fatalf("不存在的项目应 found=false 无错: found=%v err=%v", found, err)
	}
}

// TestWorkspaceOf 查会话归属项目 id（resume/重启恢复工作目录的来源）。
func TestWorkspaceOf(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	if ws, err := s.WorkspaceOf(id); err != nil || ws != "" {
		t.Fatalf("未分组会话应返回空串: %q %v", ws, err)
	}
	if err := s.SessionWorkspace(id, "proj-1"); err != nil {
		t.Fatal(err)
	}
	if ws, err := s.WorkspaceOf(id); err != nil || ws != "proj-1" {
		t.Fatalf("归属应往返: %q %v", ws, err)
	}
	if _, err := s.WorkspaceOf("不存在"); err == nil {
		t.Fatal("不存在的会话应报错")
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
			if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: fmt.Sprintf("m%d", i)}); err != nil {
				errs <- err
			}
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
	if _, err := s1.AppendMsg(id, llm.Message{Role: "user", Content: "重启前的消息"}); err != nil {
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

// TestRoundTimingRoundTrip：每轮计时/用量/模型随消息落库并**原样**读回。
//
// 为什么在 store 这一层单独钉一次：这是"刷新后数字还在"的物理保证——回放（Load）
// 与实时（chat.done）是两条路径，本仓库为两条路径不一致吃过三次亏。零值（未知：
// 工具轮没有首 token / provider 不回报用量）必须原样保留 0，回读不能变成别的值。
func TestRoundTimingRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		{Role: "user", Content: "问"},
		{Role: "assistant", Content: "答", FirstTokenMs: 210, DurationMs: 1500, Model: "m1", UsageTokens: 42},
		// 未知形态：工具轮没有首 token、provider 没回报用量
		{Role: "assistant", Content: "工具轮", DurationMs: 900, Model: "m1"},
	}
	for _, m := range msgs {
		if _, err := s.AppendMsg(id, m); err != nil {
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
	// 逐字段一致（含未知的 0 与空模型名——不能被"默认值"顶掉）
	if got[1].FirstTokenMs != 210 || got[1].DurationMs != 1500 || got[1].Model != "m1" || got[1].UsageTokens != 42 {
		t.Fatalf("计时/用量/模型未原样读回: %+v", got[1])
	}
	if got[2].FirstTokenMs != 0 || got[2].UsageTokens != 0 || got[2].DurationMs != 900 || got[2].Model != "m1" {
		t.Fatalf("未知字段应保持 0/空（不是编出来的值）: %+v", got[2])
	}

	// 检查点行（压缩摘要）走同一张表：它没有每轮计时，字段必须是零值而不是垃圾
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "<compacted-summary>摘要</compacted-summary>"}, 0, 3); err != nil {
		t.Fatal(err)
	}
	got2, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("压缩后历史应只剩检查点: %+v", got2)
	}
	if got2[0].FirstTokenMs != 0 || got2[0].DurationMs != 0 || got2[0].Model != "" || got2[0].UsageTokens != 0 {
		t.Fatalf("检查点不该带每轮计时: %+v", got2[0])
	}
}
