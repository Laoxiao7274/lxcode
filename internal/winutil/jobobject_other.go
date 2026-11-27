//go:build !windows

package winutil

// AssignToBackendJob 非 Windows 无 Job Object 概念（Unix 上同款兜底是进程组：
// configureProcTree 的 Setpgid + killProcessTree 的负 pid 整组杀，已覆盖）。
// 参数收 uintptr 以便与 Windows 版同签名调用（exec.Cmd.Process 的句柄在
// Windows 才拿得到，非 Windows 调用方传 0 即可）。
func AssignToBackendJob(handle uintptr) error { return nil }
