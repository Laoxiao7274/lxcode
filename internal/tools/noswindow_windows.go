//go:build windows

package tools

import (
	"syscall"

	"github.com/moyunteng/lxcode/internal/winutil"
)

// noWindowAttr 是 Windows 子进程的「无窗口」SysProcAttr（tools 包内便捷别名，
// 实现共享在 internal/winutil——mcp/project 包也用同一原语）：
// CREATE_NO_WINDOW = 子进程拿到一个不可见的控制台（后台形态），它自己再拉起的
// 控制台子命令同样不可见（继承同一控制台），不会往用户桌面上弹黑窗。
//
// 为什么不用只设 HideWindow:true：那等价于 SW_HIDE 只作用于直接子进程的**首个窗口**，
// 管不到孙进程新分配的控制台；CREATE_NO_WINDOW 是创建期旗标，整棵树继承。
// （bash 工具的 shellName 可能落到 cmd.exe，cmd 会为 tasklist 这类控制台子命令
// 分配控制台——没有这个旗标时在 GUI 形态的后端下就是用户桌面上的一闪黑窗。）
func noWindowAttr() *syscall.SysProcAttr {
	return winutil.NoWindowAttr()
}
