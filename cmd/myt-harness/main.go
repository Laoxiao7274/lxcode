// myt-harness 入口：--serve 运行独立后端进程（WS JSON-RPC 服务）；
// 默认作为 CLI 客户端连接后端。桌面壳（规划中）经同一协议接入。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/moyunteng/myt-harness/internal/cli"
	"github.com/moyunteng/myt-harness/internal/config"
	"github.com/moyunteng/myt-harness/internal/server"
	"github.com/moyunteng/myt-harness/internal/store"
	"github.com/moyunteng/myt-harness/internal/wsclient"
)

func main() {
	configPath := flag.String("config", "", "配置路径（--serve：后端读；默认 ./config/models.json，或环境变量 MYT_HARNESS_CONFIG）")
	serve := flag.Bool("serve", false, "后端模式：独立运行 WS JSON-RPC 服务（注册表 + 会话运行时 + 工具循环）")
	addr := flag.String("addr", "127.0.0.1:7789", "后端监听地址（--serve）/ 连接地址（客户端，或 --backend）")
	backendAddr := flag.String("backend", "", "客户端连接的后端地址（默认取 --addr）")
	sessionsDir := flag.String("sessions", "", "会话存储目录（默认 <config 上级>/sessions，或环境变量 MYT_HARNESS_SESSIONS）")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = os.Getenv("MYT_HARNESS_CONFIG")
	}
	if path == "" {
		path = "config/models.json"
	}

	if *serve {
		// 会话目录：flag > 环境变量 > 从 config 路径推导
		sdir := *sessionsDir
		if sdir == "" {
			sdir = os.Getenv("MYT_HARNESS_SESSIONS")
		}
		if sdir == "" {
			sdir = filepath.Join(filepath.Dir(filepath.Dir(path)), "sessions")
		}
		runServe(path, *addr, sdir)
		return
	}
	runClient(*backendAddr, *addr)
}

// runServe 运行后端进程：加载配置 → 挂会话存储（恢复最近会话）→ 监听。
func runServe(path, addr, sessionsDir string) {
	reg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	if len(reg.List()) == 0 && reg.RoleBindings()[config.RoleDefault] == "" {
		fmt.Fprintf(os.Stderr, `配置 %s 还没有任何模型。写入如下内容后重跑（示例为 OpenAI 兼容端点）：

{
  "version": 1,
  "models": [
    {
      "id": "my-model",
      "base_url": "http://localhost:11434",
      "model": "qwen3:8b",
      "format": "openai",
      "capabilities": {"tools": true},
      "enabled": true
    }
  ],
  "roles": {"default": "my-model"}
}

format 可选 "openai"（chat completions，默认）或 "anthropic"（/v1/messages）。
`, path)
		os.Exit(1)
	}

	srv := server.NewServer(reg)
	st, err := store.Open(sessionsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "会话存储初始化失败: %v\n", err)
		os.Exit(1)
	}
	if err := srv.AttachSessionStore(st); err != nil {
		fmt.Fprintf(os.Stderr, "恢复会话失败: %v\n", err)
		os.Exit(1)
	}

	// 信号→ctx：优雅退出（在途连接关闭，进程结束）
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("后端启动: 配置=%s 模型数=%d", path, len(reg.List()))
	if err := srv.Run(ctx, addr); err != nil {
		log.Printf("后端退出: %v", err)
		os.Exit(1)
	}
}

// runClient 运行 CLI 客户端：连接后端，失败给出明确指引。
func runClient(backendAddr, fallbackAddr string) {
	if backendAddr == "" {
		backendAddr = fallbackAddr
	}
	be, err := wsclient.Dial(backendAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, `连接后端失败: %v

后端进程未运行？先启动它：
  myt-harness --serve
若后端监听在其它地址，用 --backend <host:port> 指定。
`, err)
		os.Exit(1)
	}
	defer be.Close()

	fmt.Printf("myt-harness · /help 查看命令\n")
	if err := cli.New(be).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "CLI 退出: %v\n", err)
		os.Exit(1)
	}
}
