// workspace_publish 的 server 层用例：参数校验（缺 source / 绝对路径 / target
// 穿越）、确认门文案（覆盖说明）、执行后主检出与工作树内容一致、源不存在。
// git 与会话装配复用 workspace_test.go 的既有夹具。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// callPublishConfirm 走注册表的确认门（与 runTools 弹确认的同一条路径）。
func callPublishConfirm(srv *Server, sessionID, rawArgs string) string {
	ctx := tools.WithSessionID(context.Background(), sessionID)
	return srv.treg.Confirm(ctx, llm.ToolCall{ID: "call-pub", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: tools.WorkspacePublishToolName, Arguments: rawArgs}})
}

// TestWorkspacePublishCopiesToMainCheckout（用例 1）：worktree 里写产物 →
// workspace_publish{source:"dist/app.exe"} → 主检出出现同名文件且内容一致；
// 返回摘要含「已发布 N 个文件到主检出」。
func TestWorkspacePublishCopiesToMainCheckout(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)
	wt, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt.Path, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "dist", "app.exe"), []byte("v1-binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 确认门：目标位置无同名文件 → 文案不含覆盖说明。
	confirm := callPublishConfirm(srv, id, `{"source":"dist/app.exe"}`)
	if strings.Contains(confirm, "将覆盖主检出已有") {
		t.Fatalf("无同名文件时确认文案不该含覆盖说明: %q", confirm)
	}
	if !strings.Contains(confirm, "dist/app.exe") || !strings.Contains(confirm, "确认执行？") {
		t.Fatalf("确认文案应说清发布对象: %q", confirm)
	}

	// 预置主检出同名文件 → 确认文案必须写明覆盖（含文件名）。
	if err := os.MkdirAll(filepath.Join(repo, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dist", "app.exe"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	confirm = callPublishConfirm(srv, id, `{"source":"dist/app.exe"}`)
	if !strings.Contains(confirm, "将覆盖主检出已有的 dist/app.exe 下 1 个文件") {
		t.Fatalf("确认文案应列明将覆盖: %q", confirm)
	}
	if !strings.Contains(confirm, "app.exe") {
		t.Fatalf("覆盖清单应含文件名: %q", confirm)
	}

	// 执行：主检出内容与工作树一致。
	out := callWorkspaceTool(srv, id, tools.WorkspacePublishToolName,
		map[string]any{"source": "dist/app.exe"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_publish 不该报错: %q", out)
	}
	if !strings.Contains(out, "已发布 1 个文件到主检出 dist/app.exe") {
		t.Fatalf("返回应是人话摘要: %q", out)
	}
	got, err := os.ReadFile(filepath.Join(repo, "dist", "app.exe"))
	if err != nil {
		t.Fatalf("主检出应出现产物: %v", err)
	}
	if string(got) != "v1-binary" {
		t.Fatalf("主检出内容应与工作树一致: %q", string(got))
	}
}

// TestWorkspacePublishDirWithExclude（用例 2）：目录发布递归复制、exclude 生效。
func TestWorkspacePublishDirWithExclude(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)
	wt, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ rel, content string }{
		{"release/index.html", "<html></html>"},
		{"release/assets/a.js", "console.log(1)"},
		{"release/node_modules/react/index.js", "react"},
	} {
		p := filepath.Join(wt.Path, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := callWorkspaceTool(srv, id, tools.WorkspacePublishToolName, map[string]any{
		"source": "release", "exclude": []string{"node_modules"},
	})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_publish 不该报错: %q", out)
	}
	if !strings.Contains(out, "已发布 2 个文件到主检出 release") {
		t.Fatalf("应发布 2 个文件（node_modules 排除）: %q", out)
	}
	if !strings.Contains(out, "跳过 1 个排除项") {
		t.Fatalf("摘要应注明跳过数: %q", out)
	}
	b, err := os.ReadFile(filepath.Join(repo, "release", "assets", "a.js"))
	if err != nil || string(b) != "console.log(1)" {
		t.Fatalf("主检出应有 release/assets/a.js（内容一致）: %q err=%v", string(b), err)
	}
	if _, err := os.Stat(filepath.Join(repo, "release", "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules 应被排除")
	}
}

// TestWorkspacePublishValidation（用例 3）：参数校验——缺 source、绝对路径
// source、target 穿越、target 绝对路径，全部拒绝且报自解释错误。
func TestWorkspacePublishValidation(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"缺 source", map[string]any{}, "路径不能为空"},
		{"绝对路径 source", map[string]any{"source": repo}, "拒绝"},
		{"target 穿越", map[string]any{"source": "dist", "target": "../escape"}, "拒绝"},
		{"target 绝对路径", map[string]any{"source": "dist", "target": `C:\Temp`}, "拒绝"},
	} {
		out := callWorkspaceTool(srv, id, tools.WorkspacePublishToolName, tc.args)
		if !strings.Contains(out, "错误:") || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: 应报自解释错误（含 %q）: %q", tc.name, tc.want, out)
		}
		// 主检出不该有任何写入。
		if _, err := os.Stat(filepath.Join(repo, "dist")); !os.IsNotExist(err) {
			t.Fatalf("%s: 校验失败后主检出不该有写入", tc.name)
		}
	}
}

// TestWorkspacePublishMissingSource（用例 4）：工作树无改动/源不存在场景 →
// 报「不存在」，主检出不动。
func TestWorkspacePublishMissingSource(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)

	out := callWorkspaceTool(srv, id, tools.WorkspacePublishToolName,
		map[string]any{"source": "dist/app.exe"})
	if !strings.Contains(out, "错误:") || !strings.Contains(out, "不存在") {
		t.Fatalf("源不存在应报自解释错误: %q", out)
	}
	// 未分组/无归属项目的会话报「没有归属项目」。
	out = callWorkspaceTool(srv, "no-such-session", tools.WorkspacePublishToolName,
		map[string]any{"source": "dist"})
	if !strings.Contains(out, "错误:") {
		t.Fatalf("未知会话应报错: %q", out)
	}
}

// TestWorkspacePublishConfirmFallback：plan 失败（源不存在）时确认门仍生效
// （回兜底文案，不让确认环节吞掉错误）。
func TestWorkspacePublishConfirmFallback(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)

	confirm := callPublishConfirm(srv, id, `{"source":"nope.txt"}`)
	if confirm == "" || !strings.Contains(confirm, "确认执行？") {
		t.Fatalf("plan 失败时确认门仍应生效（兜底文案）: %q", confirm)
	}
	// 非法 JSON 参数走 Execute 的错误路径（Confirm 对解析失败回兜底文案）。
	if confirm := callPublishConfirm(srv, id, `{`); confirm == "" {
		t.Fatal("非法参数时确认门不该静默放行")
	}
	var _ = json.Marshal // 保持 import json（用例结构与其他文件一致）
}
