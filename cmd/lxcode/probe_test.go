package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// TestProbePassesOnHealthyBackend：服务活着且协议面完整时，探针必须返回 0。
//
// 这是 install.ps1 / update.ps1 的验收面，退出码错一位就是灾难：install.ps1
// 把非 0/2 当验收失败并"停服务 + sc delete"，update.ps1 直接回滚到旧版本
// ——健康的服务被判成坏的、装完立刻自我删除。
//
// 曾经的真实缺陷（2026-09-28 实测）：多活跃会话重构后 chat.history 按
// session_id 寻址（服务端不再有"当前会话"这种全局焦点），而探针仍传 nil
// 参数，于是恒返回 1。它没被测试拦住的原因是：server_test.go 里同样传 nil
// 的 chat.history 断言前面先跑过一次 chat.send，顺带绑上了连接焦点——测试的
// 通过路径和探针的调用路径不是同一条。所以这条测试必须**从探针的入口跑**
// （runProbe），而不是复刻它的请求序列。
func TestProbePassesOnHealthyBackend(t *testing.T) {
	addr := freeAddr(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config", "models.json")
	writeConfig(t, cfg, "m1")
	sessDir := filepath.Join(dir, "sessions")

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("ctx 取消后 5s 未退出（优雅停机挂死）")
		}
	}()
	go func() { done <- runServe(ctx, cfg, addr, sessDir) }()
	dialWait(t, addr).Close() // 等后端可连（有启动窗口期）

	if code := runProbe(addr); code != probeOK {
		t.Fatalf("健康后端的探针应返回 probeOK(0)，实得 %d", code)
	}

	// 探针会话必须已归档。核对必须直接查库：未发过消息的会话不进
	// session.list（store.List 刻意滤掉空白草稿），协议面看不见它——而
	// "没归档"的后果恰恰是协议面看得见的：Latest() 按 updated_at 取最近
	// 的未归档会话，一条没归档的探针会话会让**后端重启后把用户恢复到
	// 探针会话上**。所以断言的是 Latest() 而不是 session.list 的条数。
	st, err := store.Open(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _, err := st.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("探针会话没被归档——它现在是最近会话（%s），后端重启会把用户恢复到这里", id)
	}

	// 第二道确认：用户侧栏也不该多出条目。
	be := dialWait(t, addr)
	defer be.Close()
	var sessions []protocol.SessionMeta
	if err := be.Call(context.Background(), protocol.MethodSessionList, nil, &sessions); err != nil {
		t.Fatalf("session.list 失败: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("探针在会话列表里留下了 %d 条可见会话", len(sessions))
	}
}
