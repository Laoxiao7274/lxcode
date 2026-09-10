package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moyunteng/myt-harness/internal/config"
	"github.com/moyunteng/myt-harness/internal/protocol"
	"github.com/moyunteng/myt-harness/internal/wsclient"
)

// freeAddr 预留一个临时端口（监听后关闭再返回——存在微小竞态，
// 本地测试可接受）。
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// writeConfig 写一份带默认模型的配置（⚠ 必须带 "version": 1——
// Load 校验磁盘格式版本，缺了整份拒绝且只记日志，表现为"广播没来"）。
func writeConfig(t *testing.T, path string, ids ...string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := make([]config.ModelConfig, 0, len(ids))
	for _, id := range ids {
		models = append(models, config.ModelConfig{
			ID: id, BaseURL: "http://127.0.0.1:1/v1", Model: id, Enabled: true,
		})
	}
	f := map[string]any{
		"version": 1,
		"models":  models,
		"roles":   map[string]string{"default": ids[0]},
	}
	b, _ := json.Marshal(f)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunServeLifecycle：启动 → 监听可连 → ctx 取消即退出（不挂）。
func TestRunServeLifecycle(t *testing.T) {
	addr := freeAddr(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config", "models.json")
	writeConfig(t, cfg, "m1")

	done := make(chan error, 1)
	ctx, stop := context.WithCancel(context.Background())
	go func() { done <- runServe(ctx, cfg, addr, filepath.Join(dir, "sessions")) }()

	// 可连接且 ready 到达
	be := dialWait(t, addr)
	defer be.Close()

	// 取消即退出（优雅停机不挂——WS 连接被强制关闭是预期路径）
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 5s 未退出（优雅停机挂死）")
	}
}

// TestRunServeReloadBroadcast：手改配置文件 → 热加载 → model.changed 广播。
func TestRunServeReloadBroadcast(t *testing.T) {
	// 缩短热加载周期（30s 真实周期没法等）
	old := reloadInterval
	reloadInterval = 50 * time.Millisecond
	t.Cleanup(func() { reloadInterval = old })

	addr := freeAddr(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config", "models.json")
	writeConfig(t, cfg, "m1")

	done := make(chan error, 1)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { done <- runServe(ctx, cfg, addr, filepath.Join(dir, "sessions")) }()

	be := dialWait(t, addr)
	defer be.Close()

	// 手改配置：m1 → m1+m2（落盘走同一格式）
	writeConfig(t, cfg, "m1", "m2")

	// 期望 model.changed 广播（载荷含 2 个模型）
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-be.Events():
			if !ok {
				t.Fatal("事件流提前关闭")
			}
			if ev.Method != protocol.EventModels {
				continue
			}
			var ml protocol.ModelListResult
			b, _ := json.Marshal(ev.Params)
			if err := json.Unmarshal(b, &ml); err != nil {
				t.Fatalf("model.changed 载荷解析失败: %v", err)
			}
			if len(ml.Models) == 2 {
				return // 收到变更广播
			}
		case <-deadline:
			t.Fatal("5s 内未收到注册表变更广播（热加载失效？）")
		}
	}
}

// TestResolveConfigPath：优先级 flag > env > ./config > ProgramData。
func TestResolveConfigPath(t *testing.T) {
	// flag 最优先
	if got := resolveConfigPath(`X:\explicit.json`); got != `X:\explicit.json` {
		t.Fatalf("flag 应最优先: %s", got)
	}
	// env 次之
	t.Setenv("MYT_HARNESS_CONFIG", `X:\env.json`)
	if got := resolveConfigPath(""); got != `X:\env.json` {
		t.Fatalf("env 应次之: %s", got)
	}
	t.Setenv("MYT_HARNESS_CONFIG", "")

	// ./config/models.json 存在时用本地（开发形态）
	restore := chdirTemp(t)
	os.MkdirAll("config", 0o755)
	os.WriteFile(filepath.Join("config", "models.json"), []byte("{}"), 0o644)
	if got := resolveConfigPath(""); got != filepath.Join("config", "models.json") {
		t.Fatalf("本地 config 应命中: %s", got)
	}
	restore()

	// 都没有 → ProgramData 安装形态
	t.Setenv("ProgramData", `C:\PD`)
	want := filepath.Join(`C:\PD`, "myt-harness", "config", "models.json")
	if got := resolveConfigPath(""); got != want {
		t.Fatalf("应回退 ProgramData: %s ≠ %s", got, want)
	}
}

// ---------- 测试工具 ----------

// chdirTemp 切到临时目录并返回恢复函数（避免污染仓库工作目录的判断）。
func chdirTemp(t *testing.T) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(old) }
}

// dialWait 重试连接直到成功（服务启动有窗口期）。
func dialWait(t *testing.T, addr string) wsclient.Backend {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		be, err := wsclient.Dial(addr)
		if err == nil {
			t.Cleanup(be.Close)
			return be
		}
		if time.Now().After(deadline) {
			t.Fatalf("5s 内连不上后端 %s: %v", addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
