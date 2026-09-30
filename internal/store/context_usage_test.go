// 上下文占用的落库契约：往返 + **不动 updated_at** + "没有测量"与"测量为 0"要分得开。
//
// 为什么要单独钉 updated_at：它是侧栏排序与「重启恢复最近会话」（Latest）的依据。
// 占用测量是会话的**附属信息**，不是"用户刚用过这个会话"——每轮写它把会话顶到列表
// 最前面是错的（用户在侧栏里会看到会话莫名地跳来跳去）。
package store

import (
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestContextUsageRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	// 库里还没有测量：ok=false（不是"0"——调用方据此回落估算，不编数）
	if _, ok, err := s.ContextUsageOf(id); err != nil || ok {
		t.Fatalf("没有测量时 ok 应为 false: ok=%v err=%v", ok, err)
	}

	want := sessiondata.ContextUsage{
		Used: 5180, Window: 32768, System: 4, ToolResults: 400, Messages: 4183, Reasoning: 593,
		Estimated: true,
	}
	if err := s.SaveContextUsage(id, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ContextUsageOf(id)
	if err != nil || !ok {
		t.Fatalf("应读回测量: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("字段必须逐个往返: got %+v want %+v", got, want)
	}

	// 会话不存在时报错（与 Load/WorkspaceOf 同语义），不是静默成功
	if err := s.SaveContextUsage("没有这个会话", want); err == nil {
		t.Fatal("写不存在的会话应报错")
	}
	if _, ok, err := s.ContextUsageOf("没有这个会话"); err == nil || ok {
		t.Fatalf("读不存在的会话应报错: ok=%v err=%v", ok, err)
	}
}

func TestContextUsageDoesNotTouchUpdatedAt(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.WorkspaceOf(id) // 只用来确认会话存在
	if err != nil || before != "" {
		t.Fatalf("前置条件：会话应存在且未分组: %q err=%v", before, err)
	}
	var t0 string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&t0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // 时间戳是纳秒精度：睡一下才能区分"改了"与"没改"
	if err := s.SaveContextUsage(id, sessiondata.ContextUsage{Used: 42, Window: 100}); err != nil {
		t.Fatal(err)
	}
	var t1 string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&t1); err != nil {
		t.Fatal(err)
	}
	if t0 != t1 {
		t.Fatalf("占用落库不该动 updated_at（侧栏排序/重启恢复最近会话的依据）: %s → %s", t0, t1)
	}
}

// 落了一条 Used=0 的记录 = "没有测量"（历史为空时的估算）：按 ok=false 处理，
// 让调用方走回落估算，而不是把 0 当成一个测量值（0 会被 UI 显示成 0%）。
func TestContextUsageZeroRecordIsAbsent(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveContextUsage(id, sessiondata.ContextUsage{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ContextUsageOf(id); err != nil || ok {
		t.Fatalf("Used=0 的记录应视为没有测量: ok=%v err=%v", ok, err)
	}
}
