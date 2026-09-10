//go:build windows

// service_windows.go 实现 Windows 服务（SCM）形态：--serve 在服务上下文里
// 运行时，日志落文件（服务无 stdout），Stop/Shutdown 控制命令转成 ctx 取消
// （优雅停机：断 WS 连接 → Shutdown 等收尾）。
// 这是参考项目 OpenRC 服务（/etc/init.d/myt-agent）的 Windows 等价物。
package main

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

// serviceName 与 install.ps1 里 sc create 的名字一致（改要两处同步）。
const serviceName = "myt-harness"

// isWindowsService 报告当前进程是否由 SCM 启动（服务形态 vs 控制台形态）。
func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// harnessService 实现 svc.Handler：Start 时装配并运行后端，Stop 时取消 ctx。
type harnessService struct {
	path, addr, sessionsDir string
}

func (m *harnessService) Execute(args []string, reqs <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	// 日志先行：服务进程 stdout 是无效句柄，任何 Printf 都要落文件
	if err := setupFileLogging(programDataRoot()); err != nil {
		// 目录都建不了——把错误塞进事件日志（SCM 管理器能看到）再退
		log.Printf("日志初始化失败（继续运行，日志不可用）: %v", err)
	}
	log.Printf("服务启动: 配置=%s 地址=%s", m.path, m.addr)

	const accepts = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe(ctx, m.path, m.addr, m.sessionsDir)
	}()

	status <- svc.Status{State: svc.Running, Accepts: accepts}
	for req := range reqs {
		switch req.Cmd {
		case svc.Interrogate:
			status <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending, WaitHint: 15000}
			cancel()
			if err := <-errCh; err != nil {
				log.Printf("后端退出: %v", err)
			}
			return false, 0
		}
	}
	return false, 0
}

// runAsService 是服务形态入口（main 检测到 SCM 上下文后调用）。
func runAsService(path, addr, sessionsDir string) error {
	return svc.Run(serviceName, &harnessService{path: path, addr: addr, sessionsDir: sessionsDir})
}
