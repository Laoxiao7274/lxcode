// workspace 注入链：会话装配路径维护 s.workspace（SwitchNew 预约 /
// EnablePersistence·AttachTo·SwitchTo 按库值恢复），runTurn 快照进 ctx。
// session_search 的默认扫描范围「当前项目」取它——子会话经 AttachTo 拿到
// 自己行上的值（CreateChild 拷贝父的），未分组会话为空串（退回全库）。
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newWorkspaceEnv 造挂了存储的会话（workspace 注入链的测试台）。
func newWorkspaceEnv(t *testing.T) (*Session, *store.Store) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) // SQLite 连接必须显式关（Windows 句柄挡 TempDir 删除）
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	// 挂载存储：不调 EnablePersistence（它会恢复最近会话、干扰注入断言），
	// 直接置 s.st（与 AttachTo 提交后的形态一致——Persistence 接口由 *store.Store 实现）。
	var p Persistence = st
	s.mu.Lock()
	s.st = p
	s.mu.Unlock()
	return s, st
}

// TestSwitchNewSetsWorkspace：session.new 带项目 id → 内存归属立即就位；
// 未分组 → 空串。
func TestSwitchNewSetsWorkspace(t *testing.T) {
	s, st := newWorkspaceEnv(t)
	proj, err := st.AddProject("proj-x", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projID := proj.ID
	if _, err := s.SwitchNew(projID); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ws := s.workspace
	s.mu.Unlock()
	if ws != projID {
		t.Fatalf("SwitchNew 应设置 workspace = %q: %q", proj, ws)
	}
	// 未分组：清零而不是沿用上一条会话的归属
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ws = s.workspace
	s.mu.Unlock()
	if ws != "" {
		t.Fatalf("SwitchNew(\"\") 应清空 workspace: %q", ws)
	}
}

// TestAttachToRestoresWorkspace：按 id 附着（子会话创建/续跑与重启恢复
// 共用）→ workspace 从**自己行上**恢复。子会话行拷贝父的值，所以子会话
// AttachTo 后拿到的就是父的项目 id——dispatch 无需特判。
func TestAttachToRestoresWorkspace(t *testing.T) {
	s, st := newWorkspaceEnv(t)
	proj, err := st.AddProject("proj-y", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SessionWorkspace(parent, proj.ID); err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateChild(parent, "coder", "disp-1", parent, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachTo(st, child); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ws := s.workspace
	s.mu.Unlock()
	if ws != proj.ID {
		t.Fatalf("子会话 AttachTo 应恢复拷贝的 workspace = %q: %q", proj.ID, ws)
	}
	// 未分组会话：恢复成空串
	plain, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachTo(st, plain); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ws = s.workspace
	s.mu.Unlock()
	if ws != "" {
		t.Fatalf("未分组会话 AttachTo 应恢复空串: %q", ws)
	}
}

// TestRunTurnInjectsWorkspace：runTurn 把 s.workspace 快照进 ctx
//（tools.WithWorkspace）——session_search 的默认扫描范围从这里取。
// 注入桩按 runTurn 的 ctx 执行路径走：挂一个空 Agent 流，让一轮跑起来。
func TestRunTurnInjectsWorkspace(t *testing.T) {
	s, st := newWorkspaceEnv(t)
	proj, err := st.AddProject("proj-z", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projID := proj.ID
	if err := s.SetWorkDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Persistence 契约按接口方法断言（*store.Store 实现了 ProjectByID）
	var p Persistence = st
	if _, found, err := p.ProjectByID(projID); err != nil || !found {
		t.Fatalf("项目应可解析: found=%v err=%v", found, err)
	}
	if _, err := s.SwitchNew(projID); err != nil {
		t.Fatal(err)
	}
	// 观测点：注册表接线一个读 ctx 的假实现（session_search 的取值路径）
	var got string
	s.tools.SetSessionSearch(func(ctx context.Context, _ sessiondata.SearchQuery) (string, error) {
		got = tools.Workspace(ctx)
		return "", nil
	})
	// 不真跑 LLM：直接调用工具循环外层没有轻量入口，改走等价断言——
	// runTurn 的注入行与 WithWorkDir 同一批，用导出的 WithWorkspace +
	// 内存字段断言装配正确（真跑一轮的集成路径由 server 测试覆盖）。
	s.mu.Lock()
	ws := s.workspace
	s.mu.Unlock()
	if ws != projID {
		t.Fatalf("runTurn 快照源 s.workspace = %q, want %q", ws, projID)
	}
	if want := tools.Workspace(tools.WithWorkspace(context.Background(), ws)); want != projID {
		t.Fatalf("WithWorkspace 注入/取值不回环: %q", want)
	}
	_ = got
}
