//go:build windows

package server

import "syscall"

// procProbeAttr 是测试探活命令的 Windows 属性：无窗口（GUI 形态后端下测试
// 同样不该闪黑窗——与生产路径的 noWindowAttr 同一语义）。
func procProbeAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
