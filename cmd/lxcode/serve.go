// serve.go 实现后端运行：服务/控制台两形态共用的装配流程（配置解析 →
// 存储 → 服务）与注册表周期热加载。服务形态的差异只在"谁来给 ctx 发
// 取消信号"（SCM Stop vs Ctrl+C），装配本身完全一致。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/server"
	"github.com/moyunteng/lxcode/internal/store"
)

// reloadInterval 是注册表热加载周期。抽成变量纯粹是为了测试能缩短它
// （30s 的真实周期没法在单测里等）。
var reloadInterval = 30 * time.Second

// resolveConfigPath 决定配置文件路径。优先级：
//  1. --config 显式指定
//  2. LXCODE_CONFIG 环境变量
//  3. .\config\models.json（存在时——开发仓库形态）
//  4. %ProgramData%\lxcode\config\models.json（安装形态的标准位置）
//
// 3→4 的内置回退就是参考项目 /usr/local/bin/myt-agent 包装器的 Windows 等价物
// （local-myt-agent 踩过配置分叉坑后用包装器强制路径；我们把规则做进解析顺序，
// 服务装好后从任何目录跑 CLI/后端都能找到同一份配置）。
func resolveConfigPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("LXCODE_CONFIG"); env != "" {
		return env
	}
	if _, err := os.Stat(filepath.Join("config", "models.json")); err == nil {
		return filepath.Join("config", "models.json")
	}
	return filepath.Join(programDataRoot(), "config", "models.json")
}

// programDataRoot 返回安装形态的根目录（%ProgramData%\lxcode）。
func programDataRoot() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData` // 服务会话里环境变量缺失的兜底（理论上不会）
	}
	return filepath.Join(root, "lxcode")
}

// runServe 运行后端：装配（config → store → server）+ 热加载 + 监听。
// ctx 取消即优雅退出（控制台形态接 Ctrl+C，服务形态接 SCM Stop）。
func runServe(ctx context.Context, path, addr, sessionsDir string) error {
	reg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	// 空注册表不拒绝启动（服务形态崩溃重启循环比明确报错更糟）：
	// 记警告，chat.send 会按请求返回 CodeNoDefaultModel。
	if len(reg.List()) == 0 {
		log.Printf("警告: 配置 %s 没有任何模型——chat.send 将报「未绑定」，请编辑配置（%d 秒内热加载生效）",
			path, int(reloadInterval.Seconds()))
	}

	srv := server.NewServer(reg)
	st, err := store.Open(sessionsDir)
	if err != nil {
		return fmt.Errorf("会话存储初始化失败: %w", err)
	}
	defer st.Close() // 库连接随进程退出收尾（WAL 落盘）
	if err := srv.AttachSessionStore(st); err != nil {
		return fmt.Errorf("恢复会话失败: %w", err)
	}

	go reloadLoop(ctx, reg, srv)
	return srv.Run(ctx, addr)
}

// reloadLoop 周期热加载注册表：手改 models.json 后无需重启服务。
// 变更才广播（model.changed）；失败保留旧状态（降级可用）。
func reloadLoop(ctx context.Context, reg *config.Registry, srv *server.Server) {
	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()
	last := snapshot(reg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := reg.Reload(); err != nil {
				log.Printf("注册表热加载失败（保留旧状态）: %v", err)
				continue
			}
			if now := snapshot(reg); now != last {
				last = now
				srv.NotifyModels()
				log.Printf("注册表变更已广播: %s", now)
			}
		}
	}
}

// snapshot 生成注册表状态的确定性摘要（用于变更检测日志）。
func snapshot(reg *config.Registry) string {
	b := reg.RoleBindings()
	return fmt.Sprintf("模型数=%d default=%s", len(reg.List()), orDash(b["default"]))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// resolveSessionsDir 决定会话目录：flag > 环境变量 > 从 config 路径推导
// （config 在 X/config/ 下则会话在 X/sessions/——安装形态即
// %ProgramData%\lxcode\sessions\）。
func resolveSessionsDir(flagVal, configPath string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("LXCODE_SESSIONS"); env != "" {
		return env
	}
	return filepath.Join(filepath.Dir(filepath.Dir(configPath)), "sessions")
}

// ensureRunnable 校验：空配置警告已在 runServe 内记日志，这里只挡
// 明确的互斥用法错误（--probe 与 --serve 同给）。
func ensureRunnable(serve, probe bool) error {
	if serve && probe {
		return errors.New("--probe 与 --serve 不能同时使用")
	}
	return nil
}
