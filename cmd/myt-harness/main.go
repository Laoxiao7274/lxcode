// myt-harness 是内核的 CLI 入口：加载配置 → 挂会话存储 → 进入 REPL。
// 桌面壳（规划中）会以库形态直接嵌入 internal/agent，本入口届时保留为
// 调试/远程使用面。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moyunteng/myt-harness/internal/agent"
	"github.com/moyunteng/myt-harness/internal/cli"
	"github.com/moyunteng/myt-harness/internal/config"
	"github.com/moyunteng/myt-harness/internal/store"
	"github.com/moyunteng/myt-harness/internal/tools"
)

func main() {
	configPath := flag.String("config", "", "配置路径（默认 ./config/models.json，或环境变量 MYT_HARNESS_CONFIG）")
	sessionsDir := flag.String("sessions", "", "会话存储目录（默认 <config 上级>/sessions，或环境变量 MYT_HARNESS_SESSIONS）")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = os.Getenv("MYT_HARNESS_CONFIG")
	}
	if path == "" {
		path = "config/models.json"
	}

	reg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	// 没有任何模型：给出可复制的起步配置，别让人猜 schema
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

	// 会话目录：flag > 环境变量 > 从 config 路径推导
	sdir := *sessionsDir
	if sdir == "" {
		sdir = os.Getenv("MYT_HARNESS_SESSIONS")
	}
	if sdir == "" {
		sdir = filepath.Join(filepath.Dir(filepath.Dir(path)), "sessions")
	}

	sess := agent.New(reg, tools.New(), nil)
	if st, err := store.Open(sdir); err != nil {
		fmt.Fprintf(os.Stderr, "会话存储初始化失败: %v\n", err)
		os.Exit(1)
	} else if err := sess.EnablePersistence(st); err != nil {
		fmt.Fprintf(os.Stderr, "恢复会话失败: %v\n", err)
		os.Exit(1)
	}
	sess.AttachSessionSearch()
	defer sess.Close()

	fmt.Printf("myt-harness · 工作目录 %s · /help 查看命令\n", mustWd())
	if err := cli.New(sess, reg).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "REPL 退出: %v\n", err)
		os.Exit(1)
	}
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return wd
}
