// 后台任务的装配、协议映射与**唤醒投递**（契约 docs/jobs.md §4/§5）。
//
// 三件事都在这里（缺一件就是半截功能）：
//  1. 工具面：注册表挂上 Manager（SetJobs）——否则模型调 job_* 会拿到「未装配」；
//  2. 协议面：job.started / job.settled 事件广播 + job.list/kill/log 三个方法
//     （dispatch_jobs.go）；
//  3. 唤醒投递：settle 事件 → 归属会话 → 按契约 §5 的表决定投不投递与措辞。
package server

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// maxConsecutiveWakes 是连续唤醒预算（契约 §5）：只约束「空闲 → 开一轮」，
// 每投递一次这样的唤醒消耗一点；用户消息或用户点 job.kill 都清零
// （用户交互本身就是交互，不该被自己的操作耗掉预算）。
//
// 轮边界注入**不**受它约束：会话已经在跑，注入一条通告不额外开轮——
// 压着不给反而让模型基于过期信息继续做（比如它还在等一个已经被杀掉的 dev）。
const maxConsecutiveWakes = 3

// maxJobLogBytes 是 job.log 单次返回的上限；超上限回**尾部**并置 Truncated
// （日志的尾部信息量最大——用户想知道的通常是"它最后报了什么"）。
const maxJobLogBytes = 256 * 1024

// AttachJobs 装配后台任务管理器：工具面 + 事件广播 + 唤醒投递。
func (s *Server) AttachJobs(mgr *jobs.Manager) {
	s.jobsMu.Lock()
	s.jobs = mgr
	s.jobsMu.Unlock()
	if s.treg != nil {
		s.treg.SetJobs(mgr)
	}
	// 订阅在装配期挂上、进程生命周期内不摘：任务结束事件必须有人接，
	// 不接 = 唤醒投递静默失效（用户看到任务结束了，agent 却永远不知道）
	mgr.Subscribe(s.onJobEvent)
}

func (s *Server) jobsManager() *jobs.Manager {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	return s.jobs
}

// onJobEvent 把任务事件转成协议广播，并在 settle 时投递唤醒通告。
//
// output 事件不广播：协议里没有增量输出事件（面板按需拉 job.log /
// 快照里的 OutputTail），每写一块就广播一次只是噪声。
func (s *Server) onJobEvent(ev jobs.Event) {
	switch ev.Kind {
	case jobs.EventStarted:
		s.broadcast(protocol.EventJobStarted, s.jobInfo(ev.Snapshot))
	case jobs.EventSettled:
		s.broadcast(protocol.EventJobSettled, s.jobInfo(ev.Snapshot))
		s.deliverJobNotice(ev.Snapshot)
	}
}

// jobInfo 把内核快照映射成协议载荷（唯一映射点：事件与 job.list 共用一份形状）。
func (s *Server) jobInfo(snap jobs.Snapshot) protocol.JobInfo {
	info := protocol.JobInfo{
		ID: snap.ID, Kind: snap.Kind, Label: snap.Label,
		Status: string(snap.Status), EndedBy: string(snap.EndedBy), Detail: snap.Detail,
		SessionID: snap.SessionID, OutputPath: snap.OutputPath,
		StartedAt: snap.StartedAt.UTC().Format(time.RFC3339),
	}
	if !snap.FinishedAt.IsZero() {
		info.FinishedAt = snap.FinishedAt.UTC().Format(time.RFC3339)
	}
	if mgr := s.jobsManager(); mgr != nil {
		// 内存尾缓冲就是最近 64KB（Read 从 0 读即全部可读部分）——
		// 面板直接显示，不必再拉一次 job.log
		if data, _, _, err := mgr.Read(snap.ID, 0, 0); err == nil {
			info.OutputTail = data
		}
	}
	return info
}

// deliverJobNotice 按契约 §5 的表决定投不投递、投什么措辞，并投给归属会话。
//
// 两条路径的差别只有「开不开新一轮」：
//   - owner 忙：注入下一步（轮边界），**不消耗预算**（会话已经在跑，注入不额外开轮）；
//   - owner 空闲且预算允许：开一轮（消耗一点预算）；
//   - owner 空闲但预算耗尽：只入队（QueueNotice），等下一次轮边界或用户下次
//     说话时投递——**绝不丢弃**。
func (s *Server) deliverJobNotice(snap jobs.Snapshot) {
	text, ok := jobNoticeText(snap)
	if !ok {
		return // agent 自己杀的 / 后端重启：不投递（契约 §5 的表）
	}
	if snap.SessionID == "" {
		log.Printf("后台任务 %s 结束通告未投递：任务没有归属会话", snap.ID)
		return
	}
	sess, err := s.session(snap.SessionID)
	if err != nil {
		log.Printf("后台任务 %s 的归属会话 %s 不可用（通告丢弃）: %v", snap.ID, snap.SessionID, err)
		return
	}
	if sess.Busy() {
		if err := sess.Notify(text); err != nil {
			log.Printf("后台任务 %s 的轮边界注入失败: %v", snap.ID, err)
		}
		return
	}
	if !s.consumeWake(snap.SessionID) {
		// 预算耗尽：通告留在队列里（绝不丢弃）——等下一次轮边界或
		// 用户下次说话时投递
		if err := sess.QueueNotice(text); err != nil {
			log.Printf("后台任务 %s 的通告入队失败: %v", snap.ID, err)
		}
		log.Printf("后台任务 %s 结束：会话 %s 连续唤醒已达上限 %d，通告改为排队等待",
			snap.ID, snap.SessionID, maxConsecutiveWakes)
		return
	}
	if err := sess.Notify(text); err != nil {
		log.Printf("后台任务 %s 的结束通告投递失败: %v", snap.ID, err)
	}
}

// jobNoticeText 按契约 §5 的表决定投不投递与措辞。
//
// 这张表是本设计的关键：DSH 的 settle 只有 status=killed、不记「谁结束的」，
// 于是用户点结束与 agent 自己 kill 在数据里长得一样，agent 醒来只看到
// 「有个任务结束了」，自己脑补下一步（用户实测：跑了个 dev、用户关掉、
// agent 又跑去接着思考新的）。所以归属（EndedBy）决定投不投递与措辞。
func jobNoticeText(snap jobs.Snapshot) (string, bool) {
	prefix := protocol.JobNoticePrefix
	label := snap.Label
	switch {
	case snap.EndedBy == jobs.EndedAgent:
		// 它自己杀的，同一轮内已知——投递只是噪声
		return "", false
	case snap.EndedBy == jobs.EndedBackend:
		// 后端重启时 agent 也不在了（唤醒一个不存在的会话毫无意义）
		return "", false
	case snap.EndedBy == jobs.EndedUser:
		// 用户主动结束：**这不是失败**，措辞必须明说「不要重启它」——
		// 否则 agent 醒来看到「结束了」会自己脑补接着把它拉起来
		return fmt.Sprintf("%s用户主动结束了后台任务 %s。这不是失败，不要重启它；等用户指示。", prefix, label), true
	case snap.Status == jobs.StatusCompleted:
		return fmt.Sprintf("%s后台任务 %s 结束（%s）。用 job_output 读输出。", prefix, label, detailOr(snap.Detail, "退出码 0")), true
	case snap.Detail == jobs.TimeoutDetail:
		return fmt.Sprintf("%s后台任务 %s 超时被杀。", prefix, label), true
	case snap.Status == jobs.StatusFailed:
		return fmt.Sprintf("%s后台任务 %s 失败（%s）。用 job_output 读输出。", prefix, label, detailOr(snap.Detail, string(snap.Status))), true
	}
	// 表里没有这一行（如 self+killed 但不是超时）：不投递
	return "", false
}

func detailOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// consumeWake 消耗一次连续唤醒预算；false = 预算已耗尽（该走 QueueNotice）。
func (s *Server) consumeWake(sessionID string) bool {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wakes == nil {
		s.wakes = map[string]int{}
	}
	if s.wakes[sessionID] >= maxConsecutiveWakes {
		return false
	}
	s.wakes[sessionID]++
	return true
}

// resetWakes 重置某个会话的唤醒预算：用户消息与用户点 job.kill 都调它
// （用户交互本身就是交互，不该被自己的操作耗掉预算——契约 §5）。
func (s *Server) resetWakes(sessionID string) {
	if sessionID == "" {
		return
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	delete(s.wakes, sessionID)
}

// jobLog 读任务的**全量落盘日志**（job.log）。
//
// 降级为纯内存时没有落盘文件，回内存尾缓冲（如实说明，不假装有全量）。
func jobLog(mgr *jobs.Manager, id string) (protocol.JobLogResult, error) {
	_, _, snap, err := mgr.Read(id, 0, 0)
	if err != nil {
		return protocol.JobLogResult{}, err
	}
	if snap.OutputPath == "" {
		data, _, _, err := mgr.Read(id, 0, 0)
		if err != nil {
			return protocol.JobLogResult{}, err
		}
		return protocol.JobLogResult{Data: data, Truncated: true}, nil
	}
	f, err := os.Open(snap.OutputPath)
	if err != nil {
		return protocol.JobLogResult{}, fmt.Errorf("打开任务日志失败: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return protocol.JobLogResult{}, err
	}
	if st.Size() > maxJobLogBytes {
		if _, err := f.Seek(st.Size()-maxJobLogBytes, io.SeekStart); err != nil {
			return protocol.JobLogResult{}, err
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return protocol.JobLogResult{}, err
		}
		return protocol.JobLogResult{Data: string(data), Truncated: true}, nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return protocol.JobLogResult{}, err
	}
	return protocol.JobLogResult{Data: string(data)}, nil
}
