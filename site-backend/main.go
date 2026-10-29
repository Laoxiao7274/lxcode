// lxcode 官网后端：一个进程 = SQLite + JSON over HTTP（默认 127.0.0.1:5201）。
//
// 为什么是「一个二进制 + 一个数据目录」：站点是单机部署的小服务，
// 没有集群也没有 DBA；数据目录里只有 site.db 与 releases/ 两个东西，
// 备份 = 复制目录，迁移 = 搬目录。
//
// 用法：
//   go run . --data ./data --token <管理token>           启动（自动建表；**不导入任何数据**）
//   go run . --data ./data --token <管理token> --seed     显式导入种子（幂等，仅本地 dev 用）
//
// 为什么不自动导种子：线上库的「空」是**真实状态**（还没发布过版本），
// 自动补种子会把编造数据混进真实库，症状是「线上出现了没人发过的版本」。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/moyunteng/lxcode-site-backend/internal/api"
	"github.com/moyunteng/lxcode-site-backend/internal/seed"
	"github.com/moyunteng/lxcode-site-backend/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5201", "监听地址")
	dataDir := flag.String("data", "./data", "数据目录（site.db 与 releases/ 都放这里）")
	token := flag.String("token", "", "管理接口 token（也可用环境变量 LXCODE_SITE_TOKEN）")
	doSeed := flag.Bool("seed", false, "导入种子（幂等：先清空再写）")
	staticDir := flag.String("static", "", "前端 dist 目录（配置后托管站点：未匹配路径回落 index.html）")
	cors := flag.String("cors", "http://127.0.0.1:5200,http://localhost:5200",
		"允许的 CORS origin（逗号分隔；走 vite proxy 时其实用不到）")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	adminToken := strings.TrimSpace(*token)
	if adminToken == "" {
		adminToken = strings.TrimSpace(os.Getenv("LXCODE_SITE_TOKEN"))
	}
	if adminToken == "" {
		logger.Printf("警告：未配置管理 token（--token 或 LXCODE_SITE_TOKEN）——所有写接口将返回 503")
	}

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		logger.Fatalf("数据目录不合法: %v", err)
	}
	st, err := store.Open(absData)
	if err != nil {
		logger.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	absStatic := ""
	if trimmed := strings.TrimSpace(*staticDir); trimmed != "" {
		absStatic, err = filepath.Abs(trimmed)
		if err != nil {
			logger.Fatalf("前端目录不合法: %v", err)
		}
	}

	seeded, found, err := st.Meta("seeded_at")
	if err != nil {
		logger.Fatalf("读 meta 失败: %v", err)
	}
	if *doSeed {
		// 只有显式 --seed 才导入（幂等：先清空再写）；本地 dev 想要数据就用它
		logger.Printf("导入种子（--seed）到 %s", absData)
		if err := seed.Run(st, logger); err != nil {
			logger.Fatalf("导入种子失败: %v", err)
		}
	} else if !found {
		// 库为空**不再**自动补数据：线上要的是真实数据，不是编造的版本
		logger.Printf("库为空（%s）：不自动导入种子；本地 dev 要样例数据请加 --seed", absData)
	} else {
		logger.Printf("已有数据（seed 于 %s），跳过导入；要重导请加 --seed", seeded)
	}

	server := api.New(st, api.Config{
		Token:       adminToken,
		CORSOrigins: splitList(*cors),
		StaticDir:   absStatic,
	}, logger)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// 上传 90MB 的安装包要给足时间；其余接口都很快
		WriteTimeout: 10 * time.Minute,
		ReadTimeout:  10 * time.Minute,
	}

	go func() {
		if absStatic != "" {
			logger.Printf("托管前端 %s（未匹配路径回落 index.html）", absStatic)
		}
		logger.Printf("监听 %s（数据目录 %s）", *addr, absData)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("监听失败: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Printf("收到退出信号，正在收尾")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Printf("收尾超时: %v", err)
	}
}

func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
