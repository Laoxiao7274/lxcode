// 「worktree 指针断裂」的自动修复。
//
// 断裂机制（真实数据上发生过）：sessions 根目录迁移后 git prune 掉了主仓库
// .git/worktrees/<name> 管理目录，worktree 目录里的 .git gitfile 变成悬空指针；
// 另一类是管理目录还在、但其 gitdir 回指针指向旧 sessions 根路径。两种情况
// rev-parse 都会把「目录里明明有用户文件」的工作树判成无效，恢复会话直接失败。
//
// 修复只动指针（管理目录三件套 + git worktree repair），绝不改动/覆盖工作区
// 里的任何文件，绝不跑 git worktree prune / gc / checkout / reset / clean；
// 修不了的（分支被删、无 gitfile、指向其他主仓库、修后校验仍失败）返回错误，
// 调用方保留原报错——fail-closed 语义不变。
package project

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// repairBrokenWorktree 尝试修复指针断裂的 worktree。返回 nil 表示修复动作已
// 执行（调用方必须重新校验确认，修复动作本身不等于修复成功）；返回错误表示
// 无法修复或判断为不属于可修范围，调用方保留原报错。
//
// 前置事实（任一不满足即不可修）：
//   - 分支仍在主仓库（分支被删 = 无法把工作树挂回任何地方）；
//   - worktree 目录下有 .git gitfile（纯目录不是断裂的 worktree）；
//   - gitfile 指向的管理目录确实位于本主仓库（别人的工作树绝不碰）。
func repairBrokenWorktree(repo, path, branch string) error {
	if _, err := gitOutput(repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("分支 %s 已不在主仓库中，不做指针修复: %v", branch, err)
	}
	gitfilePath := filepath.Join(path, ".git")
	raw, err := os.ReadFile(gitfilePath)
	if err != nil {
		return fmt.Errorf("读取 worktree gitfile 失败: %w", err)
	}
	adminDir, mainRoot, err := parseWorktreeGitfile(path, string(raw))
	if err != nil {
		return err
	}
	if !samePath(mainRoot, repo) {
		return fmt.Errorf("worktree gitfile 指向其他主仓库 %s，不做自动修复", mainRoot)
	}
	// 诊断口：原始 gitfile 内容进日志——修复把指针改写后，事后排查要靠它还原现场。
	log.Printf("worktree 指针断裂，尝试自动修复（branch=%s, admin=%s, gitfile=%q）",
		branch, adminDir, truncate(strings.TrimSpace(string(raw)), 200))

	_, statErr := os.Stat(adminDir)
	if statErr == nil {
		// 修复 A：管理目录还在，多半只是 gitdir 回指针过期——repair 双向对齐即可。
		if out, rerr := runGit(repo, "worktree", "repair", absWorktreePath(path)); rerr != nil {
			return fmt.Errorf("git worktree repair 失败: %v: %s", rerr, truncate(string(out), 300))
		}
		return nil
	}
	if !os.IsNotExist(statErr) {
		return fmt.Errorf("检查 worktree 管理目录失败: %w", statErr)
	}
	// 修复 B：管理目录被 prune 掉但分支还在——重建管理目录三件套
	// （HEAD / commondir / gitdir，git 在 Windows 下惯例用正斜杠），
	// 再 repair 修回指针。目录已存在时只补缺失文件，绝不整体重建。
	if err := rebuildWorktreeAdminDir(repo, adminDir, path, branch); err != nil {
		return err
	}
	if out, rerr := runGit(repo, "worktree", "repair", absWorktreePath(path)); rerr != nil {
		return fmt.Errorf("重建管理目录后 git worktree repair 失败: %v: %s", rerr, truncate(string(out), 300))
	}
	return nil
}

// parseWorktreeGitfile 解析 worktree 目录下 .git gitfile 的内容
// （形如 "gitdir: <主仓库>/.git/worktrees/<name>"），返回管理目录绝对路径与
// 主仓库根（gitfile 路径去掉尾部 "/.git/worktrees/<name>"）。
func parseWorktreeGitfile(worktreePath, raw string) (adminDir, mainRoot string, err error) {
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
			break
		}
	}
	if line == "" {
		return "", "", fmt.Errorf("worktree gitfile 为空")
	}
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", "", fmt.Errorf("worktree gitfile 内容异常: %q", truncate(line, 120))
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if target == "" {
		return "", "", fmt.Errorf("worktree gitfile 内容异常: %q", truncate(line, 120))
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktreePath, target)
	}
	slash := filepath.ToSlash(filepath.Clean(target))
	const marker = "/.git/worktrees/"
	idx := strings.LastIndex(slash, marker)
	if idx < 0 {
		return "", "", fmt.Errorf("worktree gitfile 指向 %s，不是主仓库管理目录布局", target)
	}
	name := slash[idx+len(marker):]
	if name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("worktree 管理目录名异常: %s", name)
	}
	return filepath.FromSlash(slash), filepath.FromSlash(slash[:idx]), nil
}

// rebuildWorktreeAdminDir 重建 worktree 管理目录三件套。已存在的文件一律跳过
// （只补缺失、不覆盖——残留内容哪怕过期也交给 repair 对齐，不替 git 做主）。
func rebuildWorktreeAdminDir(repo, adminDir, worktreePath, branch string) error {
	common, err := gitCommonDir(repo)
	if err != nil {
		return fmt.Errorf("读取主仓库公共 Git 目录失败: %w", err)
	}
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		return fmt.Errorf("重建 worktree 管理目录失败: %w", err)
	}
	files := []struct{ name, content string }{
		{"HEAD", "ref: refs/heads/" + branch + "\n"},
		{"commondir", filepath.ToSlash(common) + "\n"},
		{"gitdir", filepath.ToSlash(filepath.Join(worktreePath, ".git")) + "\n"},
	}
	for _, f := range files {
		p := filepath.Join(adminDir, f.name)
		if _, err := os.Stat(p); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查管理目录文件 %s 失败: %w", f.name, err)
		}
		if err := os.WriteFile(p, []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("写入管理目录文件 %s 失败: %w", f.name, err)
		}
	}
	return nil
}

func absWorktreePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
