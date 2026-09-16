// lxcode 入口：--serve 后端（控制台或 Windows 服务）/ --probe 验收 /
// 默认 CLI 客户端。桌面壳（Electron）经 WS 协议接入，与本 CLI 同权。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/moyunteng/lxcode/internal/cli"
	"github.com/moyunteng/lxcode/internal/wsclient"
)

// version 是应用版本号。唯一版本源是 shell/package.json（electron-builder
// 原生读它出安装包名），scripts/build.mjs 构建时经 -ldflags 烙进来；
// 开发形态（dev.mjs 无烙印）显示 "dev"。更新 manifest 用同一版本号。
var version = "dev"

func main() {
	configPath := flag.String("config", "", "配置路径（--serve：后端读；默认 ./config/models.json，不存在则 %ProgramData%\\lxcode\\config\\models.json；或环境变量 LXCODE_CONFIG）")
	serve := flag.Bool("serve", false, "后端模式：独立运行 WS JSON-RPC 服务（注册表 + 会话运行时 + 工具循环）；服务上下文里自动切 Windows 服务形态")
	probe := flag.Bool("probe", false, "验收模式：对运行中的后端做协议级检查（装机/升级脚本的健康验收面）")
	addr := flag.String("addr", "127.0.0.1:7789", "后端监听地址（--serve/--probe）/ 连接地址（客户端，或 --backend）")
	backendAddr := flag.String("backend", "", "客户端连接的后端地址（默认取 --addr）")
	sessionsDir := flag.String("sessions", "", "会话存储目录（默认 <config 上级>/sessions，或环境变量 LXCODE_SESSIONS）")
	showVersion := flag.Bool("version", false, "打印版本号并退出（构建时烙入，dev 构建显示 dev）")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := ensureRunnable(*serve, *probe); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	if *probe {
		probeMain(*addr)
		return
	}

	if *serve {
		path := resolveConfigPath(*configPath)
		sdir := resolveSessionsDir(*sessionsDir, path)

		// 服务形态（SCM 启动）：Stop → ctx 取消 → 优雅退出
		if isWindowsService() {
			if err := runAsService(path, *addr, sdir); err != nil {
				fmt.Fprintf(os.Stderr, "服务运行失败: %v\n", err)
				os.Exit(1)
			}
			return
		}

		// 控制台形态：Ctrl+C → ctx 取消 → 优雅退出
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runServe(ctx, path, *addr, sdir); err != nil {
			fmt.Fprintf(os.Stderr, "后端退出: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// CLI 客户端
	if *backendAddr == "" {
		*backendAddr = *addr
	}
	be, err := wsclient.Dial(*backendAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, `连接后端失败: %v

后端进程未运行？先启动它：
  lxcode --serve            # 控制台前台跑
  sc start lxcode           # 已安装的 Windows 服务
若后端监听在其它地址，用 --backend <host:port> 指定。
`, err)
		os.Exit(1)
	}
	defer be.Close()

	fmt.Printf("lxcode · /help 查看命令\n")
	if err := cli.New(be).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "CLI 退出: %v\n", err)
		os.Exit(1)
	}
}
