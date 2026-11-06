// AssistantTasksOfProject（项目模式侧栏「来自助手」小节的查询，2026-12）：
// 口径 = space='assistant' AND parent_id!='' AND workspace=项目，updated_at
// 倒序；空 = 空（nil/空切片，小节不渲染）；域/父子/归属三重过滤各自有负例。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TestAssistantTasksOfProject：多任务/空/排序/过滤口径。
func TestAssistantTasksOfProject(t *testing.T) {
	s := openTestStore(t)
	// 项目与它的主域会话（负例：主域会话不出）
	p1, err := s.AddProject("demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mainTop, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(mainTop, p1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(mainTop, llm.Message{Role: "user", Content: "主域的会话"}); err != nil {
		t.Fatal(err)
	}

	// 空口径：项目还没有助手任务 → 空结果（小节不渲染的依据）
	tasks, err := s.AssistantTasksOfProject(p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("无任务时应返回空: %+v", tasks)
	}

	// 助手父会话 + 两个任务子会话（继承 space='assistant'；workspace 由
	// RunProjectTask 的落库路径显式覆盖为项目 id——测试照抄该链路，
	// CreateChildIn 的 SQL 继承只会拷到助手父的空 workspace）
	parent, err := s.CreateIn("assistant")
	if err != nil {
		t.Fatal(err)
	}
	older, err := s.CreateChildIn(parent, "coder", "call-1", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(older, p1.ID); err != nil {
		t.Fatal(err)
	}
	// 创建时间同纳秒粒度无保证——用 AppendMsg 的时间戳差保证 updated_at 可比
	if _, err := s.AppendMsg(older, llm.Message{Role: "user", Content: "旧任务"}); err != nil {
		t.Fatal(err)
	}
	newer, err := s.CreateChildIn(parent, "coder", "call-2", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(newer, p1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(newer, llm.Message{Role: "user", Content: "新任务"}); err != nil {
		t.Fatal(err)
	}

	tasks, err = s.AssistantTasksOfProject(p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("应出两个任务子会话: %+v", tasks)
	}
	// 排序：updated_at 倒序（新任务在前）
	if tasks[0].ID != newer || tasks[1].ID != older {
		t.Fatalf("排序应为新任务在前: %+v", tasks)
	}
	// 负例①：主域顶层会话（workspace 相同但 space='main'/parent_id=''）不出
	// 负例②：助手父会话自身（space 对、parent_id=''）不出
	for _, m := range tasks {
		if m.ID == mainTop || m.ID == parent {
			t.Fatalf("顶层会话不应出现在任务列表: %+v", tasks)
		}
	}
	// 负例③：别的项目的任务不出（归属过滤）
	other, err := s.AddProject("other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateChildIn(parent, "coder", "call-3", "", 0, "main"); err != nil {
		t.Fatal(err)
	}
	tasksOther, err := s.AssistantTasksOfProject(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksOther) != 0 {
		t.Fatalf("别的项目不应有任务: %+v", tasksOther)
	}
	// 落 main 域的旧路径子会话（显式覆盖）不在 assistant 口径里
	tasks, err = s.AssistantTasksOfProject(p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("落 main 域的子会话不应计入助手任务: %+v", tasks)
	}
}

// TestAssistantTasksOfProjectVisibility：可见性判据与 List 同源——「messages
// 表里有任意行」（空白草稿不进列表），不用 leaf_len（撤回后空路径的会话
// 必须留在入口）。
func TestAssistantTasksOfProjectVisibility(t *testing.T) {
	s := openTestStore(t)
	p1, err := s.AddProject("demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateIn("assistant")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateChildIn(parent, "coder", "call-1", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(child, p1.ID); err != nil {
		t.Fatal(err)
	}
	// 无消息（空白草稿）：行在 sessions 表但 messages 无行 → 不可见（与 List
	// 的可见性判据同源——「表里有任意行」，不用 leaf_len，撤回后空路径不消失）
	tasks, err := s.AssistantTasksOfProject(p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("无消息的任务子会话应不可见（空白草稿不进列表）: %+v", tasks)
	}
	if _, err := s.AppendMsg(child, llm.Message{Role: "user", Content: "任务"}); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.AssistantTasksOfProject(p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("有消息的任务应可见: %+v", tasks)
	}
}
