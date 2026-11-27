//go:build !windows

package server

import "syscall"

// procProbeAttr 非 Windows 无窗口概念（见 procprobe_windows_test.go）。
func procProbeAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
