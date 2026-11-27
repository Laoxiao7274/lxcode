// killProcessTreeSys 杀掉整棵进程树（进程残留治理 B5）。
//
// 与 tools.killProcessTree 同一份语义，不引包的原因：tools import mcp（工具
// 面管理 MCP），反向 import 会成环——这层重复是分层约束下的最小代价，两处
// 漂移的后果只是平台降级方式不同，且兜底仍有 Job Object（stdio.go 启动时入组）。
package mcp

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/moyunteng/lxcode/internal/winutil"
)

func killProcessTreeSys(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	if runtime.GOOS == "windows" {
		// taskkill /T 按父子关系整棵树杀；必须在父进程活着时调用。
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
		if attr := winutil.NoWindowAttr(); attr != nil {
			kill.SysProcAttr = attr
		}
		if err := kill.Run(); err != nil {
			return p.Kill()
		}
		return nil
	}
	return killProcessTreeGroup(p)
}
