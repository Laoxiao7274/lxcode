// todo_persist_test.go —— todo 契约批①（内核）的钉子：
//   - todo 调用经 Persistence 接缝落库（SaveTodos），TodosOf 恢复读回；
//   - 轮首清单快照注入：无清单不注入 / 未完成注入现状 / 全 done 注入收敛提示；
//   - 轮收尾校验：未完成项 → 被动通告入队；全 done / 无清单是干净收尾；
//   - strict 档文案含轮次纪律关键句（每轮审视 / 不攒批 / 收尾一致）。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// todoStub 记录 SaveTodos 的调用（其余 Persistence 方法内嵌 nil——本文件只走
// todo 相关路径，未预期调用 panic 即失败）。
type todoStub struct {
	Persistence
	saved    map[string][]sessiondata.TodoItem
	loadMode []sessiondata.TodoItem // TodosOf 返回值（nil = 没落过）
}

func (p *todoStub) Load(string) ([]llm.Message, error) { return nil, nil }
func (p *todoStub) WorkspaceOf(string) (string, error) { return "", nil }
func (p *todoStub) ProjectByID(string) (sessiondata.ProjectMeta, bool, error) {
	return sessiondata.ProjectMeta{}, false, nil
}
func (p *todoStub) ContextUsageOf(string) (sessiondata.ContextUsage, bool, error) {
	return sessiondata.ContextUsage{}, false, nil
}
func (p *todoStub) SaveContextUsage(string, sessiondata.ContextUsage) error { return nil }
func (p *todoStub) ApprovalOf(string) (string, error)                       { return "", nil }
func (p *todoStub) SaveApproval(string, string) error                       { return nil }
func (p *todoStub) SaveSessionModel(string, string) error                   { return nil }
func (p *todoStub) SessionModelOf(string) (string, error)                   { return "", nil }
func (p *todoStub) TodosOf(string) ([]sessiondata.TodoItem, bool, error) {
	if p.loadMode == nil {
		return nil, false, nil
	}
	return p.loadMode, true, nil
}
func (p *todoStub) SaveTodos(id string, items []sessiondata.TodoItem) error {
	if p.saved == nil {
		p.saved = map[string][]sessiondata.TodoItem{}
	}
	p.saved[id] = items
	return nil
}

// todo 调用走 Session 的 sink（落库 + 事件）——辅助函数。
// sink 逻辑与 turn.go 的注入逐字同款：清单入内存 + SaveTodos 落库。
func execTodo(t *testing.T, s *Session, args string) string {
	t.Helper()
	r := tools.New()
	ctx := tools.WithTodoSink(context.Background(), func(items []tools.TodoItem) {
		s.mu.Lock()
		s.todos = items
		st, id := s.st, s.id
		s.mu.Unlock()
		if st != nil && id != "" {
			if err := st.SaveTodos(id, items); err != nil {
				t.Fatalf("落库失败: %v", err)
			}
		}
	})
	var c llm.ToolCall
	c.Function.Name = "todo"
	c.Function.Arguments = args
	return r.Execute(ctx, c)
}

func TestTodoSaveViaSeam(t *testing.T) {
	p := &todoStub{}
	s := New(nil, tools.New(), nil)
	s.st = p
	s.id = "sess-1"
	execTodo(t, s, `{"items":[{"content":"盘点","status":"done"},{"content":"实现","status":"active"}]}`)
	if len(p.saved["sess-1"]) != 2 || p.saved["sess-1"][1].Status != "active" {
		t.Fatalf("todo 调用应落库，saved=%+v", p.saved)
	}
}

func TestAttachToRestoresTodos(t *testing.T) {
	p := &todoStub{loadMode: []sessiondata.TodoItem{
		{Content: "残留项", Status: "active"}, {Content: "已完成", Status: "done"},
	}}
	s := New(nil, tools.New(), nil)
	if err := s.AttachTo(p, "sess-1"); err != nil {
		t.Fatal(err)
	}
	got := s.Todos()
	if len(got) != 2 || got[0].Content != "残留项" || got[0].Status != "active" {
		t.Fatalf("附着后应恢复落库的清单，got %+v", got)
	}
}

func TestAttachToEmptyTodosKeepsNone(t *testing.T) {
	p := &todoStub{}
	s := New(nil, tools.New(), nil)
	if err := s.AttachTo(p, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Todos(); len(got) != 0 {
		t.Fatalf("空列 = 无清单，不编一个空清单，got %+v", got)
	}
}

func TestInjectTodoSnapshotBranches(t *testing.T) {
	// 无清单：不注入
	s := New(nil, tools.New(), nil)
	before := len(s.History().Messages)
	s.injectTodoSnapshot()
	if len(s.History().Messages) != before {
		t.Fatalf("无清单不应注入")
	}

	// 未完成：注入现状摘要
	s = New(nil, tools.New(), nil)
	s.todos = []tools.TodoItem{
		{Content: "改代码", Status: "active"}, {Content: "跑测试", Status: "pending"},
	}
	s.injectTodoSnapshot()
	msgs := s.History().Messages
	if len(msgs) != before+1 {
		t.Fatalf("未完成清单应注入一条")
	}
	m := msgs[len(msgs)-1]
	if m.Role != "user" || !m.Notice || !strings.Contains(m.Content, "进行中：改代码") {
		t.Fatalf("注入应带清单现状摘要: %+v", m)
	}

	// 全 done：注入收敛提示（防旧清单误导本轮）
	s = New(nil, tools.New(), nil)
	s.todos = []tools.TodoItem{{Content: "已完事项", Status: "done"}}
	s.injectTodoSnapshot()
	msgs = s.History().Messages
	m = msgs[len(msgs)-1]
	if !strings.Contains(m.Content, "已全部完成") || !strings.Contains(m.Content, "重建清单") {
		t.Fatalf("全 done 应注入收敛提示: %+v", m)
	}
}

func TestAuditTodosAtRoundEnd(t *testing.T) {
	// 未完成项 → 被动通告入队
	s := New(nil, tools.New(), nil)
	s.todos = []tools.TodoItem{
		{Content: "跑测试", Status: "active"}, {Content: "写文档", Status: "pending"},
	}
	s.auditTodosAtRoundEnd()
	s.mu.Lock()
	n := len(s.passiveNotices)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("未完成清单收尾应入队一条被动通告，got %d", n)
	}

	// 全 done / 无清单：干净收尾，不入队
	s = New(nil, tools.New(), nil)
	s.todos = []tools.TodoItem{{Content: "x", Status: "done"}}
	s.auditTodosAtRoundEnd()
	s.mu.Lock()
	n = len(s.passiveNotices)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("全 done 是干净收尾，不应入队")
	}
}

func TestStrictTodoPromptRoundsDiscipline(t *testing.T) {
	p := BuildSystemPrompt(tools.New(), "", WorktreeInfo{}, false, ProjectDocs{}, "strict")
	for _, key := range []string{"每轮开始先审视任务清单", "不得攒到轮末一次性补写", "轮收尾清单必须与实际进度一致"} {
		if !strings.Contains(p, key) {
			t.Fatalf("strict 档提示词缺少轮次纪律关键句 %q", key)
		}
	}
	// off 档整体缺席（既有契约不变）：守则里没有 todo-first 条目（工具目录里
	// "todo：维护任务清单"一行是工具摘要，本来就在，不算）。
	off := BuildSystemPrompt(tools.New(), "", WorktreeInfo{}, false, ProjectDocs{}, "off")
	if strings.Contains(off, "先审视任务清单") {
		t.Fatalf("off 档不应含 todo-first 守则条目")
	}
}
