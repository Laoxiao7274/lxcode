//go:build !windows

package winutil

// Handle 非 Windows 无进程句柄（Unix 的 AssignToBackendJob 是 no-op，见
// jobobject_other.go）；类型对齐 Windows 版让调用方写一份代码。
type Handle = uintptr
