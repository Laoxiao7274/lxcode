// Package project 承载「项目目录」的业务规则：目录有效性校验与 git 仓库
// 初始化。它不依赖存储与传输层——server（协议侧）与 store（持久侧）都
// 只是它的调用方，规则单处定义不漂移。
package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// ValidateDirectory 校验项目根目录可用（存在且是目录），返回绝对路径。
// 会话绑定项目时调用：失效目录必须显式报错，而不是静默回退到后端进程
// 目录——那会让工具在错误的地方读写文件（用户报告过的原始 bug 形态）。
func ValidateDirectory(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("项目目录为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("路径无效 %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("项目目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("不是文件夹: %s", abs)
	}
	return abs, nil
}

// Registrar 是项目用例所需的持久化边界。
type Registrar interface {
	AddProject(string, string) (sessiondata.ProjectMeta, error)
}

// Add 校验目录并初始化仓库，最后注册；失败不写入项目记录。
func Add(st Registrar, name, path string) (sessiondata.ProjectMeta, error) {
	abs, err := ValidateDirectory(path)
	if err != nil {
		return sessiondata.ProjectMeta{}, err
	}
	if err := EnsureRepo(abs); err != nil {
		return sessiondata.ProjectMeta{}, err
	}
	return st.AddProject(name, abs)
}

// EnsureRepo 保证目录是 git 仓库：已有 .git 直接用；没有则 git init。
// 用户规则：「本地仓库的创建——如果有了那就不用管」。
func EnsureRepo(path string) error {
	abs, err := ValidateDirectory(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
		return nil // 已是仓库，不用管
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = abs
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init 失败（git 未安装?）: %v: %s", err, truncate(string(out), 200))
	}
	return nil
}

// truncate 截断错误信息（自解释但不淹没）。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
