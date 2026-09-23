// childsession_test.go —— 子会话（派发给子 Agent 开的独立会话）的存储契约（2026-09-22）。
// 断言的是对外行为：子会话有自己的行与消息历史、不进侧栏列表、不参与"恢复最近会话"、
// 归档时随父级联；不关心表结构细节。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func TestCreateChildAndListFilters(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	if err := s.AppendMsg(parent, llm.Message{Role: "user", Content: "父会话第一句"}); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateChild(parent, "coder", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMsg(child, llm.Message{Role: "user", Content: "子任务"}); err != nil {
		t.Fatal(err)
	}

	// 子会话有自己的历史（与父会话完全独立）
	msgs, err := s.Load(child)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "子任务" {
		t.Fatalf("子会话历史不符: %+v", msgs)
	}
	pmsgs, _ := s.Load(parent)
	if len(pmsgs) != 1 || pmsgs[0].Content != "父会话第一句" {
		t.Fatalf("父会话历史应独立: %+v", pmsgs)
	}

	// 侧栏列表只列顶层会话（子会话不进）
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != parent {
		t.Fatalf("List 应只列顶层会话: %+v", list)
	}

	// 重启恢复（Latest）不能被子会话抢走：先动子会话（更新 updated_at），Latest 仍应是父
	if err := s.AppendMsg(child, llm.Message{Role: "assistant", Content: "子回复"}); err != nil {
		t.Fatal(err)
	}
	lid, lmsgs, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if lid != parent {
		t.Fatalf("Latest 不应恢复到子会话: got %s want %s", lid, parent)
	}
	if len(lmsgs) != 1 || lmsgs[0].Content != "父会话第一句" {
		t.Fatalf("Latest 应返回父会话历史: %+v", lmsgs)
	}

	// 按父查子（子会话视图/对账用）
	kids, err := s.ChildrenOf(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].ID != child || kids[0].ParentID != parent || kids[0].AgentID != "coder" {
		t.Fatalf("ChildrenOf 不符: %+v", kids)
	}
	if kids[0].Messages != 2 {
		t.Fatalf("子会话消息数不符: %+v", kids[0])
	}
}

// 归档级联：子会话不进侧栏、用户没法单独操作，父归档必须带上它们。
func TestArchiveCascadesToChildren(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	child1, _ := s.CreateChild(parent, "coder", "call-1")
	child2, _ := s.CreateChild(parent, "scout", "call-2")
	other, _ := s.Create()

	if err := s.Archive(parent, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent, child1, child2} {
		var archived int
		if err := s.db.QueryRow(`SELECT archived FROM sessions WHERE id = ?`, id).Scan(&archived); err != nil {
			t.Fatal(err)
		}
		if archived != 1 {
			t.Fatalf("会话 %s 应随父归档", id)
		}
	}
	// 别的顶层会话不受影响
	var otherArchived int
	if err := s.db.QueryRow(`SELECT archived FROM sessions WHERE id = ?`, other).Scan(&otherArchived); err != nil {
		t.Fatal(err)
	}
	if otherArchived != 0 {
		t.Fatal("归档一个会话不应影响别的会话")
	}

	// 恢复父会话：子会话一并恢复（dispatch 卡回放要用它们的历史）
	if err := s.Archive(parent, false); err != nil {
		t.Fatal(err)
	}
	kids, _ := s.ChildrenOf(parent)
	if len(kids) != 2 {
		t.Fatalf("应有两个子会话: %+v", kids)
	}
	for _, k := range kids {
		if k.Archived {
			t.Fatalf("恢复父会话后子会话应一并恢复: %+v", k)
		}
	}
	_ = child1
	_ = child2
}
