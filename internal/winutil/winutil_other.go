//go:build !windows

package winutil

import "syscall"

// NoWindowAttr 非 Windows 平台无窗口概念，返回 nil（调用方在 nil 时照旧
// 设置自己的平台属性，如 unix 的 Setpgid）。
func NoWindowAttr() *syscall.SysProcAttr { return nil }
