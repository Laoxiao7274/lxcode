//go:build !windows

package tools

import (
	"syscall"

	"github.com/moyunteng/lxcode/internal/winutil"
)

// noWindowAttr 非 Windows 平台无窗口概念，返回 nil（SysProcAttr 留给平台
// 特有设置，如 unix 的 Setpgid——调用方在 nil 时自行设置）。
func noWindowAttr() *syscall.SysProcAttr { return winutil.NoWindowAttr() }
