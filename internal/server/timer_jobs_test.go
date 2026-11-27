// B2 定时/提醒任务的行为用例：timer 触发 / 持久化与重启重建 / 通告措辞。
package server

import (
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/store"
)

// TestTimerFiresAndSettles：timer 到点写 Note 进输出并 settle（completed+self+timer）。
func TestTimerFiresAndSettles(t *testing.T) {
	srv, _, mgr := newJobTestServer(t, immediateStream)
	_ = srv
	j, err := mgr.Start(jobs.Spec{
		Kind: jobs.TimerKind, Label: "提醒我喝水", Note: "该喝水了",
		SessionID: "s1", DueAt: time.Now().Add(50 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 到点（50ms + 余量）内 settle
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := mgr.Snapshot(j.ID())
		if err == nil && snap.Status.Terminal() {
			if snap.Status != jobs.StatusCompleted || snap.EndedBy != jobs.EndedSelf || snap.Detail != jobs.TimerDetail {
				t.Fatalf("settle 形态不对: %+v", snap)
			}
			data, _, _, err := mgr.Read(j.ID(), 0, 0)
			if err != nil || !strings.Contains(data, "该喝水了") {
				t.Fatalf("Note 应写进输出: %q err=%v", data, err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timer 未在预期时间内触发")
}

// TestTimerKillCancels：Kill 停掉到点回调（不到点、不触发）。
func TestTimerKillCancels(t *testing.T) {
	srv, _, mgr := newJobTestServer(t, immediateStream)
	_ = srv
	j, err := mgr.Start(jobs.Spec{
		Kind: jobs.TimerKind, Label: "不会被触发的提醒", Note: "不应出现",
		SessionID: "s1", DueAt: time.Now().Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Kill(j.ID(), jobs.EndedUser); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	snap, err := mgr.Snapshot(j.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Status.Terminal() || snap.EndedBy != jobs.EndedUser {
		t.Fatalf("Kill 后应终止: %+v", snap)
	}
}

// TestTimerPersistenceRoundTrip：SaveTimer 落库 → PendingTimers 读回 →
// MarkTimerDone 后不再出现（重启重建的数据面）。
func TestTimerPersistenceRoundTrip(t *testing.T) {
	srv, _, _ := newTestServer(t, immediateStream)
	if srv.st == nil {
		t.Fatal("测试服务器应挂载会话存储")
	}
	due := time.Now().Add(time.Hour)
	row := store.TimerRow{
		JobID: "job-t1", SessionID: "s1", OwnerSessionID: "s1",
		Workspace: "p1", Label: "下午三点提醒", Note: "开会", DueAt: due,
	}
	if err := srv.st.SaveTimer(row); err != nil {
		t.Fatalf("SaveTimer: %v", err)
	}
	rows, err := srv.st.PendingTimers()
	if err != nil {
		t.Fatalf("PendingTimers: %v", err)
	}
	if len(rows) != 1 || rows[0].JobID != "job-t1" || rows[0].Note != "开会" {
		t.Fatalf("读回不对: %+v", rows)
	}
	if !rows[0].DueAt.Equal(due.Truncate(time.Millisecond)) && !rows[0].DueAt.Equal(due) {
		// RFC3339Nano 往返应精确到纳秒
		if rows[0].DueAt.Sub(due).Abs() > time.Millisecond {
			t.Fatalf("due_at 往返精度丢失: %v vs %v", rows[0].DueAt, due)
		}
	}
	if err := srv.st.MarkTimerDone("job-t1"); err != nil {
		t.Fatalf("MarkTimerDone: %v", err)
	}
	rows, err = srv.st.PendingTimers()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("done 后不应再出现在重建清单: %+v", rows)
	}
}

// TestJobNoticeTextTimer：timer 的通告措辞带提醒正文（模型醒来即行动）。
func TestJobNoticeTextTimer(t *testing.T) {
	snap := jobs.Snapshot{
		Kind: jobs.TimerKind, Label: "提醒我喝水",
		Status: jobs.StatusCompleted, EndedBy: jobs.EndedSelf, Detail: jobs.TimerDetail,
		SessionID: "s1", OwnerSessionID: "s1",
	}
	text, ok := jobNoticeText(snap)
	if !ok {
		t.Fatal("timer 到点应投递通告")
	}
	if !strings.Contains(text, "定时提醒") {
		t.Fatalf("措辞应带「定时提醒」: %q", text)
	}
}
