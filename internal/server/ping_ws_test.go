// 体验修复批次 3：connection.ping 心跳方法 + 广播写失败清理路径的单测。
//
// 写超时本身（TCP 缓冲满拖满 5s）无法在单测里稳定模拟——需要灌满对端接收
// 窗口且依赖平台 TCP 行为，为它引入 conn 接口抽象不值得（任务约束：不为测试
// 大改）。这里钉住的是同一条错误路径：send 报错 → 广播不炸 → 客户端被清理出
// 注册表；写超时防护由 send 内的 SetWriteDeadline 代码保证（超时后 WriteJSON
// 返回错误，走的正是这条路径）。
package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestConnectionPingOverWS：心跳应答 {pong:true}，纯内存往返（~0ms）。
func TestConnectionPingOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	resp := client.call(protocol.MethodPing, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("connection.ping 失败: %+v", resp)
	}
	var result map[string]any
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("应答不是对象: %v (%s)", err, b)
	}
	if result["pong"] != true {
		t.Fatalf("应答应为 {pong:true}，实际: %s", b)
	}
}

// TestPingNotificationHasNoResponse：通知形态（无 id）不回应答——与 hello 同款纪律。
func TestPingNotificationHasNoResponse(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	resp := srv.dispatch(&wsClient{}, &protocol.Request{Method: protocol.MethodPing})
	if resp != nil {
		t.Fatalf("通知形态不应回响应，实际: %+v", resp)
	}
}

// TestSendErrorClosesClient：写失败 → 广播不 panic → 客户端被清理出注册表。
func TestSendErrorClosesClient(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)

	// 取出服务端侧的 wsClient（唯一的客户端连接）
	srv.mu.Lock()
	var wc *wsClient
	for c := range srv.clients {
		wc = c
	}
	srv.mu.Unlock()
	if wc == nil {
		t.Fatal("客户端未注册进服务端")
	}

	// 服务端侧关闭底层连接：下一次 send 必然报错（写失败路径的最小等价物）
	_ = wc.conn.Close()
	if err := wc.send(protocol.NewEvent(protocol.EventModels, protocol.ModelListResult{})); err == nil {
		t.Fatal("已关闭的连接 send 应报错")
	}

	// 广播遇到写失败不应 panic；该客户端最终被清理出注册表（清理由读循环
	// 的 defer 完成——连接已死，读循环随即退出）
	srv.broadcast(protocol.EventModels, protocol.ModelListResult{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.mu.Lock()
		n := len(srv.clients)
		srv.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("写失败的客户端应被移出注册表，仍剩 %d 个", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 客户端侧的读循环也应随连接关闭而退出（events 通道被 close——先排干
	// 连接建立时缓冲的 ready 事件）
	drained := make(chan struct{})
	go func() {
		for range client.events {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("客户端读循环未随连接关闭退出")
	}
}
