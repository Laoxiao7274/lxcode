// panic 防护的钉子（健壮性加固，2026-10）：请求处理 goroutine 里的 panic 转成
// JSON-RPC internal error 应答（该请求失败），进程与其他请求照常运行。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestRequestPanicReturnsInternalError：dispatch panic → 该请求得到 internal error
// 应答（id 配对不丢），进程存活，后续请求正常。
func TestRequestPanicReturnsInternalError(t *testing.T) {
	_, client, _ := newTestServer(t, nil)

	dispatchPanicHook = func() { panic("boom: 注入的处理器 panic") }
	t.Cleanup(func() { dispatchPanicHook = nil })

	resp := client.call(protocol.MethodPing, nil)
	if resp == nil {
		t.Fatal("panic 的请求应得到错误应答（而不是连接死掉/无应答）")
	}
	if resp.Error == nil {
		t.Fatalf("panic 的请求应回 error 应答: %+v", resp)
	}
	if resp.Error.Code != protocol.CodeInternal {
		t.Fatalf("错误码应为 internal error（%d），实际 %d", protocol.CodeInternal, resp.Error.Code)
	}

	// 钩子摘掉后：同一连接上后续请求正常（进程与读循环都活着）
	dispatchPanicHook = nil
	if resp := client.call(protocol.MethodPing, nil); resp == nil || resp.Error != nil {
		t.Fatalf("panic 后续请求应正常: %+v", resp)
	}

	// 通知形态（无 id）的 panic：无可回的应答，但进程照常存活
	dispatchPanicHook = func() { panic("boom: 通知形态的 panic") }
	b, _ := json.Marshal(map[string]any{})
	if err := client.conn.WriteJSON(protocol.Request{JSONRPC: "2.0", Method: protocol.MethodPing, Params: b}); err != nil {
		t.Fatalf("写通知失败: %v", err)
	}
	dispatchPanicHook = nil
	if resp := client.call(protocol.MethodPing, nil); resp == nil || resp.Error != nil {
		t.Fatalf("通知形态 panic 后进程应照常运行: %+v", resp)
	}
}
