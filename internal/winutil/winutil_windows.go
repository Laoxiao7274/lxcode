// Package winutil —— Windows 进程创建的平台细节（跨 internal 子包共享）。
//
// 目前只有一件事：给 exec.Cmd 一个「无控制台窗口」的 SysProcAttr。GUI/服务形态
// 的后端没有控制台，它 spawn 的控制台程序（git、taskkill、shell 的子命令、MCP
// 服务器……）默认会新建**可见**控制台——用户桌面上闪黑窗。CREATE_NO_WINDOW 让
// 子进程拿到不可见控制台，且整棵子树继承（孙进程的孙进程同样不弹）。
//
// 为什么不用只设 HideWindow:true：那只是把直接子进程的窗口以 SW_HIDE 显示——
// 管不到子进程自己再分配的控制台（如 cmd 跑 tasklist）；且 detached 语义的
// 孙进程根本不吃这个旗标。创建期旗标 CREATE_NO_WINDOW 是唯一可靠解。
//
// 非 Windows 构建返回 nil（调用方在 nil 时照旧设置自己的平台属性），实现见
// winutil_other.go——拆开文件是因为 x/sys/windows 在非 Windows 构建下没有
// Go 文件（build constraints exclude），无条件 import 会让 darwin/linux
// 交叉编译直接失败。
package winutil

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// NoWindowAttr 返回无窗口的 SysProcAttr（仅 Windows 构建）。
func NoWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
