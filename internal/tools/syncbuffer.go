package tools

import "sync"

// syncBuffer 是并发安全的字节缓冲（Start/Wait 期 Stdout/Stderr 共用——
// 进程残留治理 B1 把 CombinedOutput 拆成 Start+Wait 后，exec 的输出拷贝
// goroutine 与 Wait 并发写缓冲，bytes.Buffer 不是并发安全的）。
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	b.mu.Unlock()
	return len(p), nil
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}
