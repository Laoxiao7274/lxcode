// screen.go —— 投屏协议域（screen.* 方法 + 手机端二进制视频帧通道）。
//
// 职责：方法分派、接收者（手机 WS 客户端）注册、二进制帧从壳侧通道取出后
// 经 wsClient 的二进制路径发到手机。帧格式与角色划分见 internal/protocol/screen.go。
package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// dispatchScreen 投屏方法域（未命中回落 nil）。
func (s *Server) dispatchScreen(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodScreenStart:
		resp := s.startScreen(req, params)
		if resp.Error == nil {
			// start 成功：本连接成为视频接收者（订阅在应答**之前**挂上，
			// 视频帧随应答之后立刻开始到达——不丢首帧）。
			s.shell.setReceiver(c)
		}
		return resp

	case protocol.MethodScreenStop:
		resp := s.stopScreen(req, params)
		if resp.Error == nil {
			s.shell.clearReceiver(c)
		}
		return resp

	case protocol.MethodScreenState:
		return s.screenStateQuery(req, params)

	case protocol.MethodScreenMonitors:
		return s.screenMonitors(req, params)
	}
	return nil
}

// setReceiver / clearReceiver（receiverId 在 screen_shell.go 的锁内维护）。
func (sh *screenShell) setReceiver(c *wsClient) {
	sh.mu.Lock()
	sh.receiverId = clientKey(c)
	sh.mu.Unlock()
	// 旧接收者退出订阅：订阅表以「channel」为单位，接收者切换由 sendBinary
	// 的目标判定兜底——Stage 1 单接收者，直接全量订阅新接收者即可。
}

func (sh *screenShell) clearReceiver(c *wsClient) {
	sh.mu.Lock()
	if sh.receiverId == clientKey(c) {
		sh.receiverId = ""
	}
	sh.mu.Unlock()
}

// clientKey 客户端标识：指针地址即身份（wsClient 生命周期内唯一；清理由
// Handler 的 defer 统一做，不存在复用）。连接远端地址在 NAT/代理下可能重复，
// 不用它做身份。
func clientKey(c *wsClient) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%p", c)
}

// deliverBinary 把一帧视频（含 13 字节头）发给接收者客户端（二进制 WS 帧）。
// 由壳推流端点的转发 goroutine 调用——注意：wsClient 的单写者纪律是 writeLoop
// 拥有 conn 的写权，这里同样只能经队列入队（enqueueBinary），绝不直接写 conn。
func (s *Server) deliverBinary(target string, frame []byte) {
	s.mu.Lock()
	var targetClient *wsClient
	for c := range s.clients {
		if clientKey(c) == target {
			targetClient = c
			break
		}
	}
	s.mu.Unlock()
	if targetClient == nil {
		return
	}
	targetClient.enqueueBinary(frame)
}

// enqueueBinary 把一帧二进制数据排进客户端发送队列（与文本帧同一条队列、
// 同一个 writeLoop 按序写——WS 帧类型不同但顺序语义一致）。队列满 = 手机端
// 跟不上：**丢这条帧**而不是 fast-fail 关连接——视频流丢一帧只是卡顿一下，
// 关连接是灾难（重连+重协商）。文本帧的 fast-fail 语义不变。
func (c *wsClient) enqueueBinary(frame []byte) bool {
	select {
	case c.bin <- frame:
		return true
	case <-c.done:
		return false
	default:
		return false // 队列满：丢帧（视频流的背压丢弃语义）
	}
}

// writeLoop 的二进制分支（改动点在 server.go 的 writeLoop——帧类型判定在这里
// 做不了（[]byte 无类型标记），所以文本/二进制区分放在队列元素上：见
// wsClient.enqueue 与 enqueueBinary 的注释；writeLoop 统一写 TextMessage。
// 本文件保留说明注释：若未来需要区分，再给 out 换成结构化元素。）

// screenFrameForwarder 把壳推流端点收到的原始字节流按帧边界切开后转发。
// 壳侧已按 AU 切帧（13 字节头 + 载荷逐帧 POST），这里只做透传到接收者。
// 由 handleScreenStream 的循环调用。
func (s *Server) screenFrameForwarder(frame []byte) {
	s.shell.mu.Lock()
	target := s.shell.receiverId
	s.shell.mu.Unlock()
	if target == "" {
		return // 无人接收：壳侧仍可推流（进程已起），后端直接丢弃
	}
	s.deliverBinary(target, frame)
}

// screenDeframer 把壳推流端点读到的原始字节块按帧边界切开（壳逐帧 POST，但
// Go 的 http 服务端 Read 不保证按写边界返回——一次 Read 可能含多帧或半帧）。
// 原先整块透传，接收端（安卓 parseFrame）按「一帧一消息」假设解析：
//   - 多帧粘连 → 除首帧外全部静默丢弃，丢的若是关键帧，解码器拿不到 csd
//     → 黑屏到下一个 GOP（2s 后）才恢复；
//   - 半帧（尾部不完整）→ 整条 WS 消息判非法丢弃，同样可能丢关键帧。
//
// 现在按帧头 len24 精确切分：完整帧逐帧转发，残尾留到下一块拼上。
// 返回本块发了几帧（诊断用）。防御：缓冲超过 FrameMaxPayload+头 = 坏流，
// 丢弃重来（正常单帧远小于这个上限）。
type screenDeframer struct {
	buf []byte
}

func (d *screenDeframer) feed(chunk []byte, deliver func(frame []byte)) int {
	d.buf = append(d.buf, chunk...)
	sent := 0
	for {
		if len(d.buf) < protocol.FrameHeaderSize {
			return sent
		}
		h, ok := protocol.UnmarshalFrameHeader(d.buf)
		if !ok {
			// 头非法（版本不符）：丢一个字节重新对齐（帧内数据出现头字节序列
			// 也只是多扫几步；正常流不会走到这里）
			d.buf = d.buf[1:]
			continue
		}
		n := protocol.FrameHeaderSize + protocol.GetLen24(h.Len24)
		if n > protocol.FrameMaxPayload+protocol.FrameHeaderSize {
			// 长度字段坏（> 理论上限）：坏流，整体丢弃重来
			d.buf = d.buf[:0]
			return sent
		}
		if len(d.buf) < n {
			return sent // 半帧：等下一块拼上
		}
		deliver(d.buf[:n])
		sent++
		d.buf = d.buf[n:]
		if len(d.buf) == 0 {
			d.buf = nil // 释放底层数组，避免长期持有大缓冲
		}
	}
}

// receiverOf 当前接收者 id（测试用）。
func (s *Server) receiverOf() string {
	s.shell.mu.Lock()
	defer s.shell.mu.Unlock()
	return s.shell.receiverId
}

// 屏幕接收者断连：连接清理钩子（Handler 的 defer 清理里调用）。接收者断开
// 即停投屏——手机不在看了，桌面还继续捕屏是隐私反模式（自己电脑被自己手机
// 看，指示灯还在但没人看）。
func (s *Server) onScreenClientGone(c *wsClient) {
	if s.shell == nil {
		return
	}
	s.shell.mu.Lock()
	wasReceiver := s.shell.receiverId == clientKey(c)
	if wasReceiver {
		s.shell.receiverId = ""
		s.shell.cmd.Want = false
	}
	s.shell.mu.Unlock()
	if wasReceiver {
		log.Printf("投屏接收者断开，已停止投屏命令")
		s.broadcastScreenChanged()
	}
}
