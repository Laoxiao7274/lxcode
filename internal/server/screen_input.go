// screen_input.go —— 投屏控制（第二阶段：可控制）的**后端转发段**：手机端
// screen.input 事件的队列与壳侧长轮询端点（GET /screen/input）。
//
// 角色与通道选型（承接 screen_shell.go 的「为什么是本地回环 HTTP」）：输入是
// 高频低延迟事件，1s 命令轮询不够用；壳→后端推（POST）不符合「壳是客户端」
// 的方向也不省事。取长轮询：壳起一个常驻 goroutine 循环 GET /screen/input?wait=1，
// 后端 hold 最多 900ms（有事件立即回，无事件 900ms 超时空回），壳再发下一个
// 请求——事件从入队到被壳取走 ≤ 一次网络往返（回环上 <1ms），壳注入是本地
// SendInput（微秒级），全链路延迟由视频帧的 30fps 决定，输入通道不成为瓶颈。
//
// 安全模型：事件按**接收者**入队（receiverId 单接收者不变量的延伸——只有当前
// 在看的手机能注入）；壳侧取走全部队列事件（壳就是唯一的注入执行者，不存在
// 「壳 A 取走壳 B 的事件」——壳就一个）。token 门复用 screenGate（与
// /screen/ctl 同一份）。
package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// inputHoldMax 长轮询 hold 上限（900ms：小于壳侧任一超时预算，长轮询不断流）。
const inputHoldMax = 900 * time.Millisecond

// inputQueueCap 单接收者事件队列容量。move 类事件高频到达，容量取「一帧间隔
// （33ms）内可能积压的事件数 × 富余」；满了丢最旧——输入事件只有时序意义，
// 旧 move 被新 move 覆盖是正确语义（点按类事件极少积压）。
const inputQueueCap = 64

// inputWaitGroup 长轮询等待者的唤醒组（多壳并存时全体唤醒各取各的——实际
// 只有一个壳，结构上仍按广播写，不依赖单壳假设）。
type inputWaitGroup struct {
	mu sync.Mutex
	// ch 关闭即唤醒所有等待者；唤醒后换新 channel。nil = 从未有等待者
	//（wait 直接走超时分支）。
	ch chan struct{}
}

func (w *inputWaitGroup) wait(d time.Duration) {
	ch := func() chan struct{} {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.ch
	}()
	if ch == nil {
		<-time.After(d)
		return
	}
	select {
	case <-ch:
	case <-time.After(d):
	}
}

func (w *inputWaitGroup) broadcast() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch == nil {
		// 从未有等待者：换一个新 channel 即可（无可关——close(nil) panic）
		w.ch = make(chan struct{})
		return
	}
	close(w.ch)
	w.ch = make(chan struct{})
}

// ---- 队列（挂在 screenShell 上，与命令电平同一把锁） ----

// enqueueInput 一条输入事件入队（仅当前接收者的会话可入——安全门在 screen.go
// 的 screenInput 已校验 token/开关/壳在线，这里做最后的身份对齐：队列属于
// 接收者，不是属于「最近一个投屏者」）。返回 false = 调用者不是当前接收者。
func (sh *screenShell) enqueueInput(caller string, p protocol.ScreenInputParams) bool {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.receiverId == "" || sh.receiverId != caller {
		return false
	}
	if len(sh.inputQueue) >= inputQueueCap {
		sh.inputQueue = sh.inputQueue[1:] // 丢最旧（move 时序语义见 inputQueueCap 注释）
	}
	sh.inputQueue = append(sh.inputQueue, p)
	sh.inputWakers.broadcast()
	return true
}

// takeInputs 壳取走当前全部事件（队列传空即返回——长轮询请求把 hold 时间花在
// 等**新**事件上）。允许 forceEmpty：壳的轮询循环刚被空回唤醒，立刻再发请求，
// 此时队列本来就是空的。
func (sh *screenShell) takeInputs(hold time.Duration) []protocol.ScreenInputParams {
	sh.mu.Lock()
	if len(sh.inputQueue) > 0 {
		evts := sh.inputQueue
		sh.inputQueue = nil
		sh.mu.Unlock()
		return evts
	}
	sh.mu.Unlock()
	sh.inputWakers.wait(hold)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if len(sh.inputQueue) == 0 {
		return nil
	}
	evts := sh.inputQueue
	sh.inputQueue = nil
	return evts
}

// dropInputQueue 接收者变化/投屏停止时清队列（换人/没人看时，积压事件注入
// 出去是错投——事件是针对「看时的画面」发出的）。
func (sh *screenShell) dropInputQueue() {
	sh.mu.Lock()
	sh.inputQueue = nil
	sh.mu.Unlock()
}

// ---- 壳长轮询端点（GET /screen/input?wait=1） ----

// handleScreenInput 壳的输入事件长轮询端点。wait=1（或非 0）时 hold 最多
// inputHoldMax；立即返回当前全部事件（空 = []，壳据此立刻发下一轮）。响应体：
// {"events":[...]}。
func (s *Server) handleScreenInput(w http.ResponseWriter, r *http.Request) {
	if !s.screenGate(w, r) {
		return
	}
	if s.shell == nil {
		http.Error(w, "screen 未装配", http.StatusNotImplemented)
		return
	}
	hold := time.Duration(0)
	if r.URL.Query().Get("wait") != "" && r.URL.Query().Get("wait") != "0" {
		hold = inputHoldMax
	}
	evts := s.shell.takeInputs(hold)
	if evts == nil {
		evts = []protocol.ScreenInputParams{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": evts})
}
