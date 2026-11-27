// timer producer（B2 定时/提醒任务）：到点把 Note 写进任务输出并 settle——
// 通告链（server/jobs.go 的 deliverJobNotice）接既有唤醒投递把提醒送到会话。
//
// 与 bash producer 的分工：timer 没有 OS 进程，生命周期就是一个 time.AfterFunc；
// 「取消」= AfterFunc.Stop。持久化在 store 层（server 装配侧负责落库与重启
// 重建——Manager 保持无存储依赖，与日志落盘的接缝纪律一致：Manager 只管内存
// 注册表与事件，磁盘归调用方）。
//
// 到点语义：Note 先 Write（进输出尾缓冲 + job_output 可读），再 Settle
//（StatusCompleted + EndedSelf + Detail="timer"）——通告措辞由 server 侧按
// Kind="timer" 分流（jobNoticeText 的 timer 行），不改 bash 的表。

package jobs

import (
	"fmt"
	"time"
)

// TimerDetail 是 timer 任务 settle 的 Detail 取值（通告措辞的分流依据）。
const TimerDetail = "timer"

// TimerKind 是 timer 任务的 Spec.Kind。
const TimerKind = "timer"

// startTimer 给 timer 任务挂到点回调（Start 的 timer 分支）。
// 返回取消函数（Kill 经 CancelRegistrar 调用——AfterFunc.Stop）。
func startTimer(j *job) func() {
	due := j.spec.DueAt
	note := j.spec.Note
	delay := time.Until(due)
	if delay < 0 {
		delay = 0 // 已过点（重启重建的存量）：立即触发，不静默丢弃
	}
	t := time.AfterFunc(delay, func() {
		if note != "" {
			_, _ = j.Write([]byte(note))
		}
		j.Settle(StatusCompleted, EndedSelf, TimerDetail)
	})
	return func() {
		// Stop 返回 true = 到点回调还没跑——bash producer 的收尾是等进程退出
		// 后 Settle，timer 没有进程，取消回调就是唯一收尾点：不补的话任务
		// 永远停在 stopping（Kill 只置状态、不 settle）。归属沿用 Kill 已记
		// 的 endedBy（user/agent）；Settle 幂等，与到点回调竞争时先到者赢。
		// Stop 返回 false = 回调已在跑或已跑完——它的 Settle 负责收尾，不动。
		if !t.Stop() {
			return
		}
		j.mu.Lock()
		by := j.endedBy
		j.mu.Unlock()
		if by == "" {
			by = EndedUser
		}
		j.Settle(StatusKilled, by, TimerDetail)
	}
}

// ValidateTimer 校验 timer Spec（schedule 工具与重启重建共用一份判定）：
// Kind 必须是 timer、DueAt 不能为零、Note 不能为空（没有正文的提醒等于
// 空唤醒——agent 醒来无事可做）。
func ValidateTimer(spec Spec) error {
	if spec.Kind != TimerKind {
		return fmt.Errorf("Kind 必须是 %s", TimerKind)
	}
	if spec.DueAt.IsZero() {
		return fmt.Errorf("缺少到点时间（duration_minutes 或 at）")
	}
	if spec.SessionID == "" {
		return fmt.Errorf("缺少执行会话（timer 的提醒要投递给会话）")
	}
	if spec.Note == "" {
		return fmt.Errorf("缺少提醒内容（note）")
	}
	return nil
}
