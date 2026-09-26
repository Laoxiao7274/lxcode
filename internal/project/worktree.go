package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Worktree 是一个顶层会话独占的 Git 工作树。BaseCommit 固定记录创建时 HEAD；
// 后续恢复沿用同一分支，不会把新提交或其他会话的改动混进来。
type Worktree struct {
	Path       string
	Branch     string
	BaseCommit string
}

var safeWorktreeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// CreateWorktree 从项目当前 HEAD 建新分支与独立工作目录。
// 未提交改动不会复制：用户已选择 HEAD 作为隔离基线，不能暗中把主工作树改动带入。
func CreateWorktree(repoPath, worktreeRoot, projectID, sessionID string) (Worktree, error) {
	repo, err := ValidateDirectory(repoPath)
	if err != nil {
		return Worktree{}, err
	}
	if !safeWorktreeID.MatchString(projectID) || !safeWorktreeID.MatchString(sessionID) {
		return Worktree{}, fmt.Errorf("项目或会话 id 含不安全字符")
	}
	branch := "lxcode/session-" + sessionID
	path := filepath.Join(worktreeRoot, projectID, sessionID)
	_, pathErr := os.Stat(path)
	_, branchErr := gitOutput(repo, "show-ref", "--verify", "refs/heads/"+branch)
	if pathErr == nil || branchErr == nil {
		if err := RestoreWorktree(repo, path, branch); err != nil {
			return Worktree{}, err
		}
		base, err := gitOutput(repo, "merge-base", "HEAD", branch)
		if err != nil {
			return Worktree{}, fmt.Errorf("读取已有 worktree 的共同基线失败: %w", err)
		}
		return Worktree{Path: path, Branch: branch, BaseCommit: strings.TrimSpace(base)}, nil
	}
	if pathErr != nil && !os.IsNotExist(pathErr) {
		return Worktree{}, fmt.Errorf("检查 worktree 路径失败: %w", pathErr)
	}
	base, err := gitOutput(repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Worktree{}, fmt.Errorf("项目仓库还没有可用的 HEAD；请先创建一次提交: %w", err)
	}
	base = strings.TrimSpace(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("创建 worktree 父目录失败: %w", err)
	}
	if out, err := runGit(repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return Worktree{}, fmt.Errorf("创建 worktree 失败: %v: %s", err, truncate(string(out), 300))
	}
	return Worktree{Path: path, Branch: branch, BaseCommit: strings.TrimSpace(base)}, nil
}

// RestoreWorktree 重新验证已有工作树；若目录被移动/删除但分支仍在，则按原路径重新挂载。
// 分支丢失、目录指向错误仓库等情况显式失败，不以 HEAD 重建覆盖用户进度。
func RestoreWorktree(repoPath, path, branch string) error {
	repo, err := ValidateDirectory(repoPath)
	if err != nil {
		return err
	}
	if path == "" || branch == "" {
		return fmt.Errorf("worktree 元数据不完整")
	}
	if _, err := os.Stat(path); err == nil {
		return validateExistingWorktree(repo, path, branch)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查 worktree 路径失败: %w", err)
	}
	if _, err := gitOutput(repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("worktree 目录与分支均不可恢复（%s）: %w", branch, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("重建 worktree 父目录失败: %w", err)
	}
	args := []string{"worktree", "add"}
	registeredPath, registered, err := registeredBranchPath(repo, branch)
	if err != nil {
		return err
	}
	if registered {
		wantPath, absErr := filepath.Abs(path)
		if absErr != nil {
			return fmt.Errorf("解析 worktree 路径失败: %w", absErr)
		}
		if !samePath(registeredPath, wantPath) {
			return fmt.Errorf("分支 %s 已登记在另一工作树 %s，拒绝共享", branch, registeredPath)
		}
		// Git 留有同路径的 prunable 登记；force 只修复该登记，不允许分支跨目录共享。
		args = append(args, "--force")
	}
	args = append(args, path, branch)
	if out, err := runGit(repo, args...); err != nil {
		return fmt.Errorf("恢复 worktree 失败: %v: %s", err, truncate(string(out), 300))
	}
	return nil
}

func validateExistingWorktree(repo, path, branch string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("检查 worktree 目录失败: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree 路径不是目录: %s", path)
	}
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("worktree 路径存在但不是有效 Git 工作树: %w", err)
	}
	gotRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return fmt.Errorf("解析 worktree 根目录失败: %w", err)
	}
	wantRoot, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析 worktree 路径失败: %w", err)
	}
	if !samePath(gotRoot, wantRoot) {
		return fmt.Errorf("worktree 路径指向其他目录: %s", gotRoot)
	}
	repoCommon, err := gitCommonDir(repo)
	if err != nil {
		return err
	}
	worktreeCommon, err := gitCommonDir(path)
	if err != nil {
		return err
	}
	if !samePath(repoCommon, worktreeCommon) {
		return fmt.Errorf("worktree 不属于预期项目仓库: %s", worktreeCommon)
	}
	gotBranch, err := gitOutput(path, "branch", "--show-current")
	if err != nil || strings.TrimSpace(gotBranch) != branch {
		return fmt.Errorf("worktree 分支不匹配（期望 %s，实际 %s）", branch, strings.TrimSpace(gotBranch))
	}
	return nil
}

// ReleaseWorktree 只移除干净的检出目录，保留分支供后续恢复；不会强制丢弃改动。
func ReleaseWorktree(repoPath string, wt Worktree) error {
	repo, err := ValidateDirectory(repoPath)
	if err != nil {
		return err
	}
	if wt.Path == "" || wt.Branch == "" {
		return fmt.Errorf("worktree 元数据不完整")
	}
	info, err := os.Stat(wt.Path)
	if os.IsNotExist(err) {
		if _, branchErr := gitOutput(repo, "show-ref", "--verify", "refs/heads/"+wt.Branch); branchErr != nil {
			return fmt.Errorf("工作区目录已不存在，保留分支 %s 也无法找到，不能安全确认释放: %w", wt.Branch, branchErr)
		}
		registeredPath, registered, listErr := registeredBranchPath(repo, wt.Branch)
		if listErr != nil {
			return listErr
		}
		if registered {
			wantPath, absErr := filepath.Abs(wt.Path)
			if absErr != nil {
				return fmt.Errorf("解析 worktree 路径失败: %w", absErr)
			}
			if !samePath(registeredPath, wantPath) {
				return fmt.Errorf("分支 %s 已登记在另一工作树 %s", wt.Branch, registeredPath)
			}
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查 worktree 路径失败: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree 路径不是目录: %s", wt.Path)
	}
	if err := validateExistingWorktree(repo, wt.Path, wt.Branch); err != nil {
		return err
	}
	status, err := gitOutput(wt.Path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("检查 worktree 改动失败: %w", err)
	}
	status = strings.TrimSpace(status)
	if status != "" {
		return fmt.Errorf("工作区有未提交或未跟踪改动，先提交后再释放:\n%s", truncate(status, 600))
	}
	if out, err := runGit(repo, "worktree", "remove", wt.Path); err != nil {
		return fmt.Errorf("释放 worktree 失败: %v: %s", err, truncate(string(out), 300))
	}
	return nil
}

func registeredBranchPath(repo, branch string) (string, bool, error) {
	out, err := gitOutput(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	var currentPath string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			currentPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			continue
		}
		if strings.TrimSpace(line) == "branch refs/heads/"+branch {
			abs, err := filepath.Abs(currentPath)
			if err != nil {
				return "", false, fmt.Errorf("解析已登记的 worktree 路径失败: %w", err)
			}
			return abs, true, nil
		}
	}
	return "", false, nil
}

// RemoveWorktree 回滚新建失败的工作树与分支；正常生命周期不自动调用，不丢弃用户改动。
func RemoveWorktree(repoPath string, wt Worktree) error {
	repo, err := ValidateDirectory(repoPath)
	if err != nil {
		return err
	}
	if wt.Path != "" {
		if out, err := runGit(repo, "worktree", "remove", "--force", wt.Path); err != nil {
			return fmt.Errorf("移除 worktree 失败: %v: %s", err, truncate(string(out), 300))
		}
	}
	if wt.Branch != "" {
		if out, err := runGit(repo, "branch", "-D", wt.Branch); err != nil {
			return fmt.Errorf("移除 worktree 分支失败: %v: %s", err, truncate(string(out), 300))
		}
	}
	return nil
}

func gitCommonDir(dir string) (string, error) {
	common, err := gitOutput(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", fmt.Errorf("解析 Git 公共目录失败: %w", err)
	}
	return abs, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := runGit(dir, args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), truncate(string(out), 200))
	}
	return string(out), nil
}

func runGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func samePath(a, b string) bool {
	if os.PathSeparator == '\\' {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
