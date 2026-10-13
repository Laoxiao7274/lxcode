// 每客户端广播队列的行为钉子（性能审查 P1-2，2026-10）。
//
// 钉住三件事：
//  1. 队列满 = fast-fail：该客户端被关闭并清理出注册表（不泄漏、不阻塞广播方）；
//  2. 慢客户端（不读 TCP）存在时，其他客户端的收件与广播调用本身**不受影响**
//     （原先同步逐客户端写，慢客户端一次写满 TCP 缓冲就把生成流卡满 5s deadline）；
//  3. 正常客户端的事件**按序完整到达**（单 writer 保序不变）。
package server

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestEnqueueFastFailsWhenQueueFull：队列满 → close 被触发、enqueue 返回 false。
// 不启动 writeLoop（不消费队列），直接把队列灌满——确定性验证 fast-fail 语义本身。
func TestEnqueueFastFailsWhenQueueFull(t *testing.T) {
	srv, _, url := newResumeTestServer(t, nil)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer conn.Close()
	c := &wsClient{
		conn: conn, out: make(chan []byte, 2), done: make(chan struct{}),
	}
	t.Cleanup(func() { c.close() })

	if !c.enqueue(protocol.NewEvent(protocol.EventModels, protocol.ModelListResult{})) {
		t.Fatal("未满的队列不应拒绝入队")
	}
	if !c.enqueue(protocol.NewEvent(protocol.EventModels, protocol.ModelListResult{})) {
		t.Fatal("未满的队列不应拒绝入队")
	}
	if c.enqueue(protocol.NewEvent(protocol.EventModels, protocol.ModelListResult{})) {
		t.Fatal("队列满应 fast-fail（返回 false）")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("队列满应触发 close（done 关闭）")
	}
	_ = srv // newTestServer 仅为复用装配（本测试不经过它）
}

// TestSlowClientDoesNotBlockOthers：一个不读 TCP 的慢客户端 + 一个正常客户端，
// 灌超过队列容量的大事件：广播循环必须快速返回（不卡 5s deadline）、正常客户端
// 的事件按序完整到达、慢客户端最终被 fast-fail 并清理出注册表。
func TestSlowClientDoesNotBlockOthers(t *testing.T) {
	srv, _, url := newResumeTestServer(t, nil)

	// 慢客户端：拨号后**不启动读循环**（TCP 接收窗口很快被灌满，writer 阻塞）
	slow, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("慢客户端拨号失败: %v", err)
	}
	defer slow.Close()

	// 正常客户端：完整读循环
	fast := dialTest(t, url)
	defer fast.conn.Close()

	// 两个连接都注册好了再开始灌
	waitRegistered := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			srv.mu.Lock()
			got := len(srv.clients)
			srv.mu.Unlock()
			if got >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("客户端未注册齐：%d/%d", got, n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitRegistered(2)

	// 快客户端的事件**在洪水期间**就并发收集（events 通道只有 256 缓冲，
	// 洪水后再排干会丢）——收集到的序号必须严格为 0..total-1（按序、无丢失）。
	var mu sync.Mutex
	var gotSeqs []int
	collectQuit := make(chan struct{})
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for {
			select {
			case ev := <-fast.events:
				if ev.Method != "test.flood" {
					continue
				}
				var p struct {
					I int `json:"i"`
				}
				raw, ok := ev.Params.([]byte)
				if !ok {
					b, merr := json.Marshal(ev.Params)
					if merr != nil {
						return
					}
					raw = b
				}
				if err := json.Unmarshal(raw, &p); err != nil {
					return
				}
				mu.Lock()
				gotSeqs = append(gotSeqs, p.I)
				mu.Unlock()
			case <-collectQuit:
				return
			}
		}
	}()
	defer close(collectQuit)

	// 灌事件：每条 ~8KB、共 400 条 ≈ 3.2MB——慢客户端（不读 TCP）的接收窗口
	// 几十条内被灌满，writer 随即阻塞，队列（256 条）紧跟着满 → fast-fail；
	// 正常客户端的 writer 能跟上入队速度，事件按序完整到达。原实现下光是
	// 慢客户端的第一次写卡满 5s deadline 就足以让这个循环卡住数分钟。
	const total = 400
	pad := strings.Repeat("x", 8*1024)
	start := time.Now()
	for i := 0; i < total; i++ {
		srv.broadcast("test.flood", map[string]any{"i": i, "pad": pad})
		if i%5 == 4 {
			time.Sleep(time.Millisecond) // 轻节流：给快客户端的 writer 跟上的机会
		}
	}
	elapsed := time.Since(start)
	t.Logf("400 条广播（~3.2MB）入队耗时 %v", elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("广播入队被慢客户端阻塞：%v（慢客户端不得阻塞广播方）", elapsed)
	}

	// 慢客户端被 fast-fail 并清理出注册表：注册表最终只剩快客户端一个
	//（fast-fail 触发 close 的语义由 TestEnqueueFastFailsWhenQueueFull 钉住，
	// 清理由读循环 defer 完成——TestSendErrorClosesClient 钉住同一条路径）
	deadline := time.Now().Add(10 * time.Second)
	for {
		srv.mu.Lock()
		n := len(srv.clients)
		srv.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("慢客户端应被清理出注册表（剩 %d 个）", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 正常客户端：连接仍活着（ping 有应答）
	if resp := fast.call(protocol.MethodPing, nil); resp == nil || resp.Error != nil {
		t.Fatalf("正常客户端应不受慢客户端影响: %+v", resp)
	}
	// 收齐 total 条且按序
	waitDeadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		seen := len(gotSeqs)
		mu.Unlock()
		if seen == total {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("正常客户端 %d/%d 秒内未收齐（应不受慢客户端影响）", seen, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, seq := range gotSeqs {
		if seq != i {
			t.Fatalf("事件乱序/丢失：第 %d 条的序号是 %d", i, seq)
		}
	}
}
