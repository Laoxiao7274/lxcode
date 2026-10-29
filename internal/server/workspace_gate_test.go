// workspace_sync 的仓库级推送闸门（lxcode 本体）验收用例：
//   - 本体仓库 push=true → 推送被拒、错误含发版指引、**远端没收到任何东西**、
//     但本地提交照常完成；
//   - 本体仓库 push=false → 完全不受影响（正常提交）；
//   - 普通项目 push=true → 行为不变（照常推到 bare origin）；
//   - 确认门文案：本体仓库在确认之前就说清「只提交、不推送」。
//
// 判据见 project.IsSelfRepo（两个指纹文件同时在场）；这里用临时目录伪造指纹，
// 不碰真实仓库。
package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/tools"
)

// markAsSelfRepo 在仓库根放上 lxcode 本体的两个指纹文件。
func markAsSelfRepo(t *testing.T, repo string) {
	t.Helper()
	for _, rel := range []string{"scripts/push-release.mjs", "docs/git-history.md"} {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// lxcode marker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// newBareOrigin 建一个本地 bare 仓库当 origin 并挂到 repo 上。
func newBareOrigin(t *testing.T, repo string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, bare, "init", "--bare")
	gitServerTest(t, repo, "remote", "add", "origin", bare)
	return bare
}

// TestWorkspaceSyncBlocksSelfRepoPush：lxcode 本体仓库 push=true →
// 明确拒绝 + 发版指引；提交已完成（本地提交照常）；远端分支列表为空。
func TestWorkspaceSyncBlocksSelfRepoPush(t *testing.T) {
	repo := initGitProject(t)
	markAsSelfRepo(t, repo)
	bare := newBareOrigin(t, repo)

	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "闸门测试", "push": true})
	// 不是失败退出（提交已完成），但必须明确说明推送被拒绝并给出指引。
	if !strings.Contains(out, "推送被拒绝") || !strings.Contains(out, "lxcode 本体") {
		t.Fatalf("本体仓库的 push 应被拒绝: %q", out)
	}
	if !strings.Contains(out, "node scripts/push-release.mjs") {
		t.Fatalf("拒绝文案必须给出唯一正确的发版通道: %q", out)
	}
	if !strings.Contains(out, "检查点") {
		t.Fatalf("拒绝文案必须说清原因（本地 master 含逐轮检查点）: %q", out)
	}
	// 本地提交照常完成：工作区干净、HEAD 顶部就是这次提交。
	if lines, err := project.StatusLines(srv.Ctx(), repo); err != nil || len(lines) != 0 {
		t.Fatalf("提交应照常完成（工作区干净）: lines=%v err=%v", lines, err)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "闸门测试" {
		t.Fatalf("本地提交应照常落盘: HEAD=%q", got)
	}
	// 远端一根毫毛都没动：bare 仓库里没有任何分支。
	if branches := strings.TrimSpace(gitServerTest(t, bare, "branch")); branches != "" {
		t.Fatalf("闸门命中时远端不该收到任何分支: %q", branches)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceSyncSelfRepoCommitStillWorks：本体仓库 push=false → 正常提交，
// 返回不提闸门（本地提交是正常流程，不该被拦）。
func TestWorkspaceSyncSelfRepoCommitStillWorks(t *testing.T) {
	repo := initGitProject(t)
	markAsSelfRepo(t, repo)

	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "本地提交"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("push=false 不该报错: %q", out)
	}
	if strings.Contains(out, "推送被拒绝") {
		t.Fatalf("push=false 不该触发推送闸门: %q", out)
	}
	if !strings.Contains(out, "本次不推送") {
		t.Fatalf("push=false 应说明本次不推送: %q", out)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "本地提交" {
		t.Fatalf("提交应完成: HEAD=%q", got)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceSyncOrdinaryRepoPushUnchanged：普通项目（无指纹文件）push=true
// 行为完全不变——照常推到 bare origin。
func TestWorkspaceSyncOrdinaryRepoPushUnchanged(t *testing.T) {
	repo := initGitProject(t)
	bare := newBareOrigin(t, repo)
	if project.IsSelfRepo(repo) {
		t.Fatal("前置：临时项目不该被判定为 lxcode 本体")
	}

	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "普通项目推送", "push": true})
	if strings.Contains(out, "推送被拒绝") {
		t.Fatalf("普通项目不该被闸门拦下: %q", out)
	}
	if !strings.Contains(out, "推送到远程 origin") {
		t.Fatalf("普通项目应照常推送: %q", out)
	}
	branch := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	if branches := gitServerTest(t, bare, "branch"); !strings.Contains(branches, branch) {
		t.Fatalf("bare origin 应有当前分支 %s:\n%s", branch, branches)
	}
	waitSessionIdle(t, srv, id)
}

// TestSyncConfirmTextWarnsSelfRepo：确认门文案在**确认之前**就说清推送会被拒
// 以及发版该走哪条路（不能让用户确认一个不会发生的推送）。
func TestSyncConfirmTextWarnsSelfRepo(t *testing.T) {
	repo := initGitProject(t)
	markAsSelfRepo(t, repo)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)

	text := srv.syncConfirmText(id, true)
	if !strings.Contains(text, "推送会被拒绝") || !strings.Contains(text, "node scripts/push-release.mjs") {
		t.Fatalf("本体仓库的确认文案必须提前说明推送会被拒绝并给出指引: %q", text)
	}
	// push=false 时文案不谈推送。
	if plain := srv.syncConfirmText(id, false); strings.Contains(plain, "推送") {
		t.Fatalf("push=false 的确认文案不该提推送: %q", plain)
	}
	waitSessionIdle(t, srv, id)
}
