// AI 自动命名（B1）的行为用例：触发条件 / 失败回落 / 只命名一次 / 用户改名不覆盖。
//
// 命名调用经会话的 stream（假流按提示词内容分流——与 turn 测试同一编排方式）；
// store 用真 SQLite（标题读写与 Rename 走同一条落库路径）。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// openTitleStore 开一个真库（测试隔离目录）。
func openTitleStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// namingStubErr 是命名调用的可注入失败（与真端点错误同语义：走静默回落）。
var namingStubErr = errStub("命名调用失败")

type errStub string

func (e errStub) Error() string { return string(e) }

// newTitleSession 起一个带假流的会话：主对话轮回 stop 收尾；命名调用
//（system 提示词含「会话命名器」）回指定标题或失败。返回命名调用计数指针。
func newTitleSession(t *testing.T, st *store.Store, namingTitle string, namingErr error) (*Session, *int) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := config.ModelConfig{
		ID: "m1", BaseURL: "http://localhost:1", Model: "test-model",
		Format: config.FormatOpenAI, Enabled: true, Capabilities: config.Capabilities{Tools: true},
	}
	if err := reg.Add(m); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	naming := 0
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	var fn StreamFn = func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "会话命名器") {
			naming++
			ch := make(chan llm.StreamEvent, 2)
			if namingErr != nil {
				ch <- llm.StreamEvent{Type: llm.EventError, Err: namingErr}
				close(ch)
				return ch, nil
			}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: namingTitle}, FinishReason: llm.FinishStop}}
			close(ch)
			return ch, nil
		}
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "好的"}, FinishReason: llm.FinishStop}}
		close(ch)
		return ch, nil
	}
	s.SetStream(fn)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	return s, &naming
}

// waitTitle 轮询等待标题落库为期望值。
func waitTitle(t *testing.T, st *store.Store, id, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		title, ok, err := st.TitleOf(id)
		if err == nil && ok && title == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	title, _, _ := st.TitleOf(id)
	t.Fatalf("等待标题 %q 超时（当前 %q）", want, title)
}

// TestAutoTitleRenamesOnFirstStop：首轮 stop 收尾后自动命名并落库；第二轮不再命名。
func TestAutoTitleRenamesOnFirstStop(t *testing.T) {
	st := openTitleStore(t)
	s, naming := newTitleSession(t, st, "整理下载目录", nil)
	s.SetAutoTitle(true)
	if err := s.Send("帮我把下载目录整理一下，文件实在太多啦，按类型分文件夹"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	waitTitle(t, st, s.SessionID(), "整理下载目录")
	if *naming != 1 {
		t.Fatalf("命名调用应恰好一次，got %d", *naming)
	}
	// 第二轮：标题已非默认 → 不再命名
	if err := s.Send("顺便看看别的"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	time.Sleep(100 * time.Millisecond)
	if *naming != 1 {
		t.Fatalf("只命名一次，got %d", *naming)
	}
}

// TestAutoTitleFailureKeepsDefault：命名调用失败 → 标题保持默认截断值，不重试。
func TestAutoTitleFailureKeepsDefault(t *testing.T) {
	st := openTitleStore(t)
	s, naming := newTitleSession(t, st, "忽略", namingStubErr)
	s.SetAutoTitle(true)
	first := "帮我把下载目录整理一下，文件实在太多啦，按类型分文件夹"
	if err := s.Send(first); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	time.Sleep(200 * time.Millisecond)
	want := sessiondata.ClipTitle(first)
	title, _, err := st.TitleOf(s.SessionID())
	if err != nil || title != want {
		t.Fatalf("失败应回落默认标题 %q，got %q (err=%v)", want, title, err)
	}
	if *naming != 1 {
		t.Fatalf("命名调用一次，got %d", *naming)
	}
	// 再来一轮：失败也不重试
	if err := s.Send("再来一轮"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	time.Sleep(200 * time.Millisecond)
	if *naming != 1 {
		t.Fatalf("失败后不重试（只命名一次），got %d", *naming)
	}
}

// TestClipTitleShared：agent 侧默认标题判定与 store 落库同一份实现
//（sessiondata.ClipTitle 收口——两侧各写一份必然漂移）。
func TestClipTitleShared(t *testing.T) {
	long := strings.Repeat("长", 45)
	got := sessiondata.ClipTitle(long)
	if len([]rune(got)) != 41 || !strings.HasSuffix(got, "…") {
		t.Fatalf("40 字截断 + 省略号: %q (len=%d)", got, len([]rune(got)))
	}
	if sessiondata.ClipTitle("第一行\n第二行") != "第一行" {
		t.Fatal("应取首行")
	}
}
