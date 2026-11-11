// logrotate.go 实现服务形态的文件日志：按大小轮转（16MB × 保留 3 份，
// LX-DSH 的惯例数值）。服务进程没有 stdout——日志落文件是服务形态的
// 唯一可观测面；控制台形态继续走 stderr。
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	logMaxBytes = 16 << 20 // 16MB
	logKeep     = 3        // 保留 lxcode.log.1/.2/.3
)

// currentLogPath 是当前日志文件的绝对路径（setupFileLogging 设置；空 =
// 文件日志未初始化——logs.tail 用它判「日志不可用」，不猜路径）。
// 包级变量的并发性：写只发生在 runServe 装配期（listen 之前），读发生在
// 协议请求里——无竞争窗口，不需要锁。
var currentLogPath string

// setupFileLogging 把标准 log 输出切到轮转文件并**同时**回显 stderr
//（io.MultiWriter——2026-12 错误日志查看器）：服务形态没有 stdout，
// 文件是唯一落点；控制台/壳内直跑形态的 stderr 行为必须不变，MultiWriter
// 让两处同时拿到同一条日志。返回错误时（目录不可写）退回 stderr——服务
// 形态下 stderr 无人看，但至少 SCM 事件日志里能留下启动失败痕迹。
func setupFileLogging(root string) error {
	dir := filepath.Join(root, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.SetOutput(os.Stderr)
		return fmt.Errorf("创建日志目录 %s 失败: %w", dir, err)
	}
	w := &rotatingWriter{path: filepath.Join(dir, "lxcode.log")}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	currentLogPath = w.path
	return nil
}

// rotatingWriter 是按大小轮转的 io.Writer：超限即把现有文件改名成 .1
// （逐级下移 .2/.3，最旧的丢弃），再开新文件。轮转失败不挡写日志
// （宁可往大文件里继续写，也不能丢运行痕迹）。
type rotatingWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	// 写前判轮转：单条日志 < 16MB（log 行有界），写后不会超太多
	if w.size+int64(len(p)) > logMaxBytes {
		w.rotate()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file, w.size = f, st.Size()
	return nil
}

// rotate 关闭当前文件并逐级改名（.2→.3、.1→.2、当前→.1），失败静默——
// 下次 Write 会重开原文件继续追加。
func (w *rotatingWriter) rotate() {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	for i := logKeep - 1; i >= 1; i-- {
		older := fmt.Sprintf("%s.%d", w.path, i)
		newer := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Rename(older, newer) // 不存在即 no-op
	}
	_ = os.Rename(w.path, w.path+".1")
	w.size = 0
	// 懒开：下次 Write 时 open()
}
