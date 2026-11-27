//go:build windows

package winutil

import "golang.org/x/sys/windows"

// Handle 是进程句柄的类型别名（调用方从 os.Process.WithHandle 拿到的是
// uintptr；Windows 版 API 收 windows.Handle——这层别名让跨平台调用方写
// 一份代码）。
type Handle = windows.Handle
