//go:build windows

package mcp

import (
	"os"
	"os/exec"
)

// killProcessTreeGroup 是 killProcessTreeSys 非 windows 分支的占位（Windows
// 构建里运行期不可达：runtime.GOOS=="windows" 分支恒先走，见 proctree.go），
// 只为让 Windows 构建编译过——行为退回单杀。真 Unix 实现在 proctree_unix.go
// 的同名函数（//go:build unix，负 pid 杀整组）。旧命名 killProcessTreeUnix
// 与 unix 实现同名，在 windows 构建里让人误读为「Unix 树杀的可用实现」
//（实际只会 p.Kill() 单杀）——改成按行为命名（组杀语义），两文件同名配对。
func killProcessTreeGroup(p *os.Process) error { return p.Kill() }

// configureProcTree Windows 侧无需自成进程组：树杀走 taskkill /T（见
// proctree.go），非空占位与 unix/other 两文件配对。
func configureProcTree(cmd *exec.Cmd) {}
