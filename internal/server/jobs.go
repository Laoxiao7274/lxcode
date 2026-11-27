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

	"context"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
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
		// 合并进程的入口工具（merge_request）：注册表只持一个回调，不 import server
		//（与 SetJobs 同款模式）。**遗留**：合并流程已退役（2026-10 架构修订），
		// 工具不再调用这个回调；合并实现（startMergeJob，mergejob.go）带 lxmerge
		// 构建标签隔离——默认构建没有这个符号，注入也一并退场。恢复隔离模式时：
		// `go build -tags lxmerge` 后把 `s.treg.SetMergeStarter(s.startMergeJob)`
		// 接回这里。
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
		// timer 到点：落库侧标记 done（重建只取未 done 的行——不标记的话
		// 重启后已触发的提醒会重复响）
		if ev.Snapshot.Kind == jobs.TimerKind && s.st != nil {
			if err := s.st.MarkTimerDone(ev.Snapshot.ID); err != nil {
				log.Printf("定时任务 %s 标记完成失败（重启后可能重复触发）: %v", ev.Snapshot.ID, err)
			}
		}
		s.deliverJobNotice(ev.Snapshot)
	}
}

// jobInfo 把内核快照映射成协议载荷（唯一映射点：事件与 job.list 共用一份形状）。
func (s *Server) jobInfo(snap jobs.Snapshot) protocol.JobInfo {
	info := protocol.JobInfo{
		ID: snap.ID, Kind: snap.Kind, Label: snap.Label,
		Status: string(snap.Status), EndedBy: string(snap.EndedBy), Detail: snap.Detail,
		SessionID: snap.SessionID, OwnerSessionID: snap.OwnerSessionID,
		Workspace: snap.Workspace, OutputPath: snap.OutputPath,
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

// deliverJobNotice 按契约 §5 的表决定投不投递、投什么措辞，并投给
// **执行会话 ∪ 时间线归属**（2026-12 修订）。
//
// 原「只投时间线归属」是「验收子会话瞎睡 53 分钟」事件的根因之一：子会话起的
// 后台任务结束事件永远回不到子会话（它不进侧栏、没人看），子 Agent 只能 sleep
// 盲等。双投语义：
//   - 执行会话（SessionID）：事件的**当事方**——通告唤醒/轮边界注入让它正在跑
//     或下一轮立刻看到结果，不用再睡。它不进侧栏没关系，投递走的是内核通道
//     （Notify → 唤醒开新轮 / 轮边界注入），不是给用户看的；
//   - 时间线归属（OwnerSessionID）：用户看着的主时间线——原语义原样保留
//     （用户实测「我关掉了，他又跑去接着思考新的」的那条防线不回退）；
//   - 两者同一个会话（顶层自己起的任务）只投一次，行为与以前逐字节一致。
//
// 唤醒预算只约束**时间线归属**那一次：它与 job.kill 重置的键（dispatch_jobs.go
// 的 resetWakes(snap.OwnerSessionID)）对得上；执行会话是兜底通道、不消耗预算
// ——子会话的盲等空转正是要消灭的，不该被预算挡住。
//
// 「开不开新一轮」的差别（对两个目标都一样）：
//   - 目标忙：注入下一步（轮边界），**不消耗预算**（会话已经在跑，注入不额外开轮）；
//   - 目标空闲且预算允许：开一轮（消耗一点预算，仅 owner）；
//   - 目标空闲但预算耗尽：只入队（QueueNotice），等下一次轮边界或用户下次
//     说话时投递——**绝不丢弃**。
func (s *Server) deliverJobNotice(snap jobs.Snapshot) {
	text, ok := jobNoticeText(snap)
	if !ok {
		return // agent 自己杀的 / 后端重启：不投递（契约 §5 的表）
	}
	executor, owner := snap.SessionID, snap.OwnerSessionID
	// OwnerSessionID 在 Start 时已定稿（为空才回落 SessionID）。
	if owner == "" {
		owner = executor
	}
	if executor != "" && executor != owner {
		if sess, err := s.session(executor); err != nil {
			log.Printf("后台任务 %s 的执行会话 %s 不可用（通告丢弃）: %v", snap.ID, executor, err)
		} else if err := sess.Notify(text); err != nil {
			log.Printf("后台任务 %s 对执行会话 %s 的通告投递失败: %v", snap.ID, executor, err)
		}
	}
	if owner == "" {
		log.Printf("后台任务 %s 结束通告未投递：任务没有归属会话", snap.ID)
		return
	}
	sess, err := s.session(owner)
	if err != nil {
		log.Printf("后台任务 %s 的归属会话 %s 不可用（通告丢弃）: %v", snap.ID, owner, err)
		return
	}
	if sess.Busy() {
		if err := sess.Notify(text); err != nil {
			log.Printf("后台任务 %s 的轮边界注入失败: %v", snap.ID, err)
		}
		return
	}
	if !s.consumeWake(owner) {
		// 预算耗尽：通告留在队列里（绝不丢弃）——等下一次轮边界或
		// 用户下次说话时投递
		if err := sess.QueueNotice(text); err != nil {
			log.Printf("后台任务 %s 的通告入队失败: %v", snap.ID, err)
		}
		log.Printf("后台任务 %s 结束：会话 %s 连续唤醒已达上限 %d，通告改为排队等待",
			snap.ID, owner, maxConsecutiveWakes)
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
	// timer（B2 定时/提醒）：正文就是提醒本身（Note 已在任务输出里，但措辞
	// 直接给正文——模型醒来读通告即行动，不必再 job_output 一趟）。label 是
	// 截断的正文预览，完整正文附后。
	if snap.Kind == jobs.TimerKind {
		return fmt.Sprintf("%s定时提醒到点：", prefix) + label, true
	}
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

// AttachSchedules 装配定时/提醒任务（B2）：schedule 工具的注入实现
//（落库 timers 表 + 起 timer 任务）与重启重建。
//
// 两条链：
//  - 落库+起任务（工具调用路径）：Start 成功才写行（起不来的任务不留库）；
//  - 重启重建（启动路径）：PendingTimers 逐行重新 Start（同 job id——事件、
//    job.list 的连续性）。重建的行不重写（SaveTimer 是 upsert，但重建就是
//    读取者，无需回写）。到点 settle 后标记 done（onJobEvent 的 timer 分支）。
//
// 未接存储（st == nil，纯内存模式）：工具报「会话存储未启用」——timer 的
// 约定本体在库里，没有库就没有跨重启语义，宁可不提供也不给半截功能。
func (s *Server) AttachSchedules() {
	if s.treg != nil {
		s.treg.SetSchedule(func(ctx context.Context, spec jobs.Spec) (string, error) {
			if err := jobs.ValidateTimer(spec); err != nil {
				return "", err
			}
			if s.st == nil {
				return "", fmt.Errorf("会话存储未启用，定时提醒不可用")
			}
			mgr := s.jobsManager()
			if mgr == nil {
				return "", fmt.Errorf("任务管理器未初始化")
			}
			j, err := mgr.Start(spec)
			if err != nil {
				return "", err
			}
			if err := s.st.SaveTimer(store.TimerRow{
				JobID: j.ID(), SessionID: spec.SessionID, OwnerSessionID: jobs.OwnerOf(spec),
				Workspace: spec.Workspace, Label: spec.Label, Note: spec.Note,
				DueAt: spec.DueAt,
			}); err != nil {
				// 落盘失败降级：任务照跑（本次进程内仍会触发），但重启后不重建——
				// 与 jobs 的「落盘失败不能让任务起不来」同一条纪律。
				log.Printf("定时任务 %s 落库失败（本次进程内有效，重启后不重建）: %v", j.ID(), err)
			}
			return j.ID(), nil
		})
	}
	s.rebuildTimers()
}

// rebuildTimers 重启重建：PendingTimers 逐行重新 Start（同 job id）。
// 已过点的行立即触发（startTimer 的 delay<0 分支——不静默丢弃存量提醒）。
func (s *Server) rebuildTimers() {
	if s.st == nil {
		return
	}
	rows, err := s.st.PendingTimers()
	if err != nil {
		log.Printf("定时任务重建失败（重启后已有的提醒不生效）: %v", err)
		return
	}
	mgr := s.jobsManager()
	if mgr == nil {
		return
	}
	for _, r := range rows {
		spec := jobs.Spec{
			Kind: jobs.TimerKind, Label: r.Label, Note: r.Note,
			SessionID: r.SessionID, OwnerSessionID: r.OwnerSessionID,
			Workspace: r.Workspace, DueAt: r.DueAt,
		}
		if _, err := mgr.Start(spec); err != nil {
			log.Printf("定时任务 %s 重建失败: %v", r.JobID, err)
		} else {
			log.Printf("定时任务 %s 已重建（到点 %s）", r.JobID, r.DueAt.Format("2006-01-02 15:04"))
		}
	}
	if len(rows) > 0 {
		log.Printf("定时任务重建完成：%d 条", len(rows))
	}
}
