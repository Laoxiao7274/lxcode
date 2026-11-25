// screen.go —— 投屏协议域（screen.* 方法 + 手机端二进制视频帧通道）。
//
// 职责：方法分派、接收者（手机 WS 客户端）注册、二进制帧从壳侧通道取出后
// 经 wsClient 的二进制路径发到手机。帧格式与角色划分见 internal/protocol/screen.go。
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"runtime"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// dispatchScreen 投屏方法域（未命中回落 nil）。
//
// 平台门（2026-10 用户拍板）：macOS 暂不做投屏（捕获链 ddagrab/NVENC 是
// Windows 专属），darwin 上手机端请求一律回明确的 CodeScreenUnavailable，
// 不进入命令电平/接收者状态机——「按功能不存在处理」。Windows/Linux 不变。
func (s *Server) dispatchScreen(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	if runtime.GOOS == "darwin" {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, "投屏暂不支持此平台（macOS 版暂未包含投屏功能）")
	}
	switch req.Method {
	case protocol.MethodScreenStart:
		// 调用者门（2026-12）：只有观看端（安卓 App）能成为投屏接收者。
		// 此前任何连接（探活 probe 等）调 screen.start 都会抢占接收者位，它断开
		// 即停投屏——手机端收到 active=false 自动退回（真机「已连接就断开」根因）。
		if !screenReceiverAllowed(c) {
			log.Printf("[screen] start denied (非安卓观看端) by %s", clientTag(c))
			return protocol.NewError(req.ID, protocol.CodeScreenReceiverDenied,
				"此客户端类型不能开始投屏——投屏接收者须为安卓观看端")
		}
		resp := s.startScreen(req, params)
		if resp.Error == nil {
			// start 成功：本连接成为视频接收者（订阅在应答**之前**挂上，
			// 视频帧随应答之后立刻开始到达——不丢首帧）。
			s.shell.setReceiver(c)
		}
		log.Printf("[screen] start %s by %s%s",
			resultWord(resp.Error == nil), clientTag(c), screenStartDetail(params))

		return resp

	case protocol.MethodScreenStop:
		// 诊断钉子：stop 的发起者此前不可指认（2026-12 投屏被拉停观测盲区）。
		// stop 无参数、无成功/失败之分（壳不在线也回成功），收到即记。
		log.Printf("[screen] stop by %s", clientTag(c))
		resp := s.stopScreen(req, params)
		if resp.Error == nil {
			s.shell.clearReceiver(c)
		}
		return resp

	case protocol.MethodScreenState:
		return s.screenStateQuery(req, params)

	case protocol.MethodScreenMonitors:
		return s.screenMonitors(req, params)

	case protocol.MethodScreenInput:
		return s.screenInput(c, req, params)
	}
	return nil
}

// setReceiver / clearReceiver（receiverId 在 screen_shell.go 的锁内维护）。
// c 可为 nil（清接收者路径沿用同一函数——测试即如此调用）：nil 只跳过队列
// 加深，换人清队列（dropInputQueue）照常。
func (sh *screenShell) setReceiver(c *wsClient) {
	sh.mu.Lock()
	prev := sh.receiverId
	sh.receiverId = clientKey(c)
	fps := sh.cmd.Fps
	sh.mu.Unlock()
	// 高帧率下加深接收者的二进制队列（144fps@8 帧 = 55ms 太浅，抖动即丢帧；
	// 见 binaryQueueDepth）。连接创建时还不知道 fps，接收者确定时按当前命令
	// 深度重建（chan 容量不可变——换新的换进 wsClient.bin，writeLoop 消费的
	// 是同一个 channel 变量，原子替换后旧 channel 里残帧自然排空）。
	if c != nil {
		if depth := binaryQueueDepth(fps); depth > cap(c.bin) {
			c.rebindBinQueue(depth)
		}
	}
	// 旧接收者退出订阅：订阅表以「channel」为单位，接收者切换由 sendBinary
	// 的目标判定兜底——Stage 1 单接收者，直接全量订阅新接收者即可。
	// 换接收者：旧接收者积压的控制事件清掉（事件是针对「看时的画面」发出的，
	// 投给新人 = 错投）。
	if prev != sh.receiverId {
		sh.dropInputQueue()
		log.Printf("投屏接收者切换（%s）", clientTag(c))
	}
}

func (sh *screenShell) clearReceiver(c *wsClient) {
	sh.mu.Lock()
	if sh.receiverId == clientKey(c) {
		sh.receiverId = ""
		sh.mu.Unlock()
		sh.dropInputQueue()
		return
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

// screenReceiverAllowed screen.start 调用者白名单（dispatchScreen 的接收者门）：
// 只有安卓观看端（hello 的 client=android）能成为接收者。无 hello 的旧/异常连接
// 视为未识别，一律拒绝（fail-closed——放行未识别客户端等于没修）。将来加 web
// 观看端时扩展此函数（web 前端目前自报 "lxcode-web"，但它不调 screen.start）。
func screenReceiverAllowed(c *wsClient) bool {
	return c != nil && c.clientID == "android"
}

// clientTag 接收者日志定位用：client 标识 + 远端地址（指针 %p 无法对应到具体
// 客户端来源——真机排查需要知道是安卓还是探活连接断开）。
func clientTag(c *wsClient) string {
	if c == nil {
		return "unknown"
	}
	remote := "unknown"
	if c.conn != nil {
		if addr := c.conn.RemoteAddr(); addr != nil {
			remote = addr.String()
		}
	}
	tag := c.clientID
	if tag == "" {
		tag = "unidentified"
	}
	return tag + "/" + remote
}

// resultWord 日志用成功/失败词。
func resultWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

// screenStartDetail start 日志的参数段（解析失败回空串——失败详情已由
// 应答错误码携带，日志只求指认发起者）。
func screenStartDetail(params json.RawMessage) string {
	var p protocol.ScreenStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return fmt.Sprintf(" (monitor=%d quality=%s fps=%d)",
		p.Monitor, normalizeQuality(p.Quality), normalizeFPS(p.Fps))
}

// screenInput 手机端注入一次输入事件（screen.input）。
//
// 安全门（三层，缺一不可——这是把手机变成键鼠的通道，错一层就是远程键注）：
//  1. 只有**当前投屏接收者**的会话可调（receiverId 单接收者不变量——没在看
//     就没有控制，投屏停止/断开即自动失权，无需单独撤销通道）；
//  2. 「允许控制」开关（remote.json 的 AllowControl，默认关）——用户未显式
//     授权前手机只能看；
//  3. 壳在线（离线时事件无去处，拒绝而不是入队积压）。
//
// 通过后事件入队列（按接收者会话），由壳的长轮询取走注入。darwin 回
// CodeScreenUnavailable（平台门在 dispatchScreen 入口已挡，这里不重复）。
func (s *Server) screenInput(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	if s.shell == nil {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, "投屏未装配")
	}
	var p protocol.ScreenInputParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
	}
	if msg := validateScreenInput(p); msg != "" {
		return protocol.NewError(req.ID, protocol.CodeInvalidParams, msg)
	}
	if !s.remoteConfig().AllowControl {
		return protocol.NewError(req.ID, protocol.CodeScreenControlDisabled, "控制未开启——请在电脑端 Lxcode 的远程访问设置里打开「允许控制」")
	}
	if !s.shell.shellOnline() {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, errScreenShellOffline.Error())
	}
	if !s.shell.enqueueInput(clientKey(c), p) {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, "当前不是投屏接收者——请先开始投屏")
	}
	return protocol.NewResult(req.ID, map[string]bool{"queued": true})
}

// isScreenKeyName 键名白名单：命名键 + 单字符（壳侧 VK_MAP 与 Unicode 注入能
// 执行的名字，与 input.ts 的 VK_MAP 保持同一集合；F1-F24 由 fN 模式覆盖）。
func isScreenKeyName(k string) bool {
	switch k {
	case "backspace", "tab", "enter", "return", "esc", "escape", "space",
		"pgup", "pgdn", "end", "home", "left", "up", "right", "down",
		"insert", "delete", "win", "lwin", "rwin",
		"ctrl", "control", "alt", "shift",
		"volumeup", "volumedown", "volumemute",
		"medianext", "mediaprev", "mediaplay":
		return true
	}
	if (k[0] == 'f' || k[0] == 'F') && len(k) >= 2 && len(k) <= 3 {
		n := 0
		for _, r := range k[1:] {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		return n >= 1 && n <= 24
	}
	return len([]rune(k)) == 1
}

// validateScreenInput 参数域校验（安全门的一部分：畸形事件在入口拒掉，
// 不进队列——壳侧注入器收到的每条事件都必须是可直接执行的）。
func validateScreenInput(p protocol.ScreenInputParams) string {
	switch p.Type {
	case "move", "click", "dblclick":
		if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			return "x/y 必须在 0-1 范围内"
		}
		switch p.Button {
		case "", "left", "right", "middle":
		default:
			return "未知按键: " + p.Button
		}
	case "down", "up":
		// key 非空 = 键盘事件（修饰键锁定链路：安卓端点 CTRL 发 type=down/up +
		// key=ctrl，锁定语义 down=按下不抬 / up=抬起），校验 key/modifiers 而非
		// x/y；key 空 = 鼠标按下/抬起，仍按坐标域校验。
		if p.Key != "" {
			if !isScreenKeyName(p.Key) {
				return "未知键名: " + p.Key
			}
			// down/up 的 modifiers：只认修饰键（与 key 事件同一份白名单）——
			// 常规键名单字符等非修饰键无意义，拒绝畸形组合。
			for _, m := range p.Modifiers {
				switch m {
				case "ctrl", "alt", "shift", "win":
				default:
					return "未知修饰键: " + m
				}
			}
		} else {
			if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
				return "x/y 必须在 0-1 范围内"
			}
			switch p.Button {
			case "", "left", "right", "middle":
			default:
				return "未知按键: " + p.Button
			}
		}
	case "scroll":
		if p.Dx == 0 && p.Dy == 0 {
			return "scroll 的 dx/dy 不能同时为 0"
		}
	case "key":
		if p.Key == "" {
			return "key 事件的 key 不能为空"
		}
		for _, m := range p.Modifiers {
			switch m {
			case "ctrl", "alt", "shift", "win":
			default:
				return "未知修饰键: " + m
			}
		}
	case "char":
		if p.Text == "" {
			return "char 事件的 text 不能为空"
		}
	default:
		return "未知事件类型: " + p.Type
	}
	return ""
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
// 跟不上：**丢最旧**（P1-4，对齐 screen_input.go 的队满丢最旧语义）而不是
// 丢新帧——新帧带着最新画面，丢它等于把延迟再往后推一帧且下一帧马上又满；
// 丢最旧把积压维持在一帧内。关键帧保护：优先丢最旧的非关键帧（丢一个 P 帧
// 只是轻微卡顿）；队列全是关键帧时才丢最旧的关键帧（代价从丢整个 GOP 的
// 2s 黑屏降为丢 1 帧——下一关键帧最多 2s 后到）。实现：非阻塞排干再回填
// （容量 8，单次开销可忽略）；排干期间 writeLoop 可能抢走个别帧——那帧
// 已开始写出，相当于正常消费，回填放不下的帧按「丢新」兜底（不阻塞）。
// 文本帧的 fast-fail 语义不变。
func (c *wsClient) enqueueBinary(frame []byte) bool {
	select {
	case c.bin <- frame:
		return true
	case <-c.done:
		return false
	default:
	}
	// 队满：排干 → 挑一个非关键帧丢掉 → 其余按原序回填 → 新帧入队。
	drained := make([][]byte, 0, cap(c.bin))
drain:
	for {
		select {
		case f := <-c.bin:
			drained = append(drained, f)
		default:
			break drain
		}
	}
	if len(drained) == 0 {
		select {
		case c.bin <- frame:
			return true
		case <-c.done:
			return false
		default:
			return false // writeLoop 抢空了又塞满（不可能走到）：保守丢新帧
		}
	}
	dropIdx := -1
	for i, f := range drained {
		if len(f) < protocol.FrameHeaderSize || f[1]&protocol.FrameFlagKeyframe == 0 {
			dropIdx = i // 最旧的非关键帧
			break
		}
	}
	if dropIdx < 0 {
		dropIdx = 0 // 全是关键帧：丢最旧（GOP 代价见上注）
	}
	drained = append(drained[:dropIdx], drained[dropIdx+1:]...)
	for _, f := range drained {
		select {
		case c.bin <- f:
		case <-c.done:
			return false
		default:
			// writeLoop 排干期间消费了帧、回填又塞满：剩余帧已有正常出路，
			// 丢弃失败的部分（罕见且无害——丢的是最旧的帧）。
		}
	}
	select {
	case c.bin <- frame:
		return true
	case <-c.done:
		return false
	default:
		return false
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
		s.shell.dropInputQueue()
		log.Printf("投屏接收者断开（%s），已停止投屏命令", clientTag(c))
		s.broadcastScreenChanged()
	}
}
