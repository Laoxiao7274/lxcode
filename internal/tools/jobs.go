package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// jobsManagerRef 是 jobs.Manager 的进程级引用（parseWaitCondition 与谓词判定的
// 辅助函数用——Exec 的闭包能拿到 r.jobsMgr，但 condHit 的输出比对在 Manager
// 的方法之外跑，得有一处能取到同一个单例。SetJobs 时更新，与注册表同生命周期）。
//
// 不引入新实例：它只是 r.jobsMgr 的别名视图（SetJobs 写、curOutputOf 读），
// 两份任务表的问题在这里不存在。
var jobsManagerRef atomic.Pointer[jobs.Manager]

// 后台任务的三个工具（契约 docs/jobs.md §3）。
//
// 三者皆**低危 + Mutates=false**：job_kill 杀的是**自己起的**进程，不是外部世界；
// strict 只读模式下也必须可用——否则只读模式连停掉自己起的任务都做不到。
//
// 实现经注入：注册表只持 jobs.Manager 的引用（server 装配时 SetJobs），
// 不自己 new 一个——任务注册表是进程级单例，多一个实例就会有两份互不可见
// 的任务表（与 SetWebSearch / SetSessionSearch 同款理由）。
const (
	jobOutputToolName = "job_output"
	jobListToolName   = "job_list"
	jobKillToolName   = "job_kill"

	// jobWaitDefault / jobWaitMax 是 job_output 的 wait 预算（契约 §3：默认 30s、上限 600s）。
	jobWaitDefault = 30 * time.Second
	jobWaitMax     = 600 * time.Second
	// jobOutputDefaultBytes / jobOutputMaxBytes 是单次读取的字节上限。
	jobOutputDefaultBytes = 32 * 1024
	jobOutputMaxBytes     = 256 * 1024
)

// SetJobs 注入后台任务管理器（server 装配时调用；与 SetWebSearch / SetSessionSearch 同款模式）。
func (r *Registry) SetJobs(mgr *jobs.Manager) {
	r.jobsMu.Lock()
	r.jobsMgr = mgr
	r.jobsMu.Unlock()
	jobsManagerRef.Store(mgr)
}

func (r *Registry) getJobs() *jobs.Manager {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	return r.jobsMgr
}

// currentJobsManager 是谓词辅助函数取管理器的入口（进程级单例的别名视图）。
func currentJobsManager() *jobs.Manager { return jobsManagerRef.Load() }

// jobCursorKey 是读游标的键：**会话 + 任务**。
// 只按任务 id 存会让两个会话互相吃掉对方的输出（一个读过，另一个就看不到了）。
func jobCursorKey(sessionID, jobID string) string { return sessionID + "\x00" + jobID }

func (r *Registry) jobCursor(sessionID, jobID string) int64 {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	return r.jobCursors[jobCursorKey(sessionID, jobID)]
}

func (r *Registry) setJobCursor(sessionID, jobID string, pos int64) {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	if r.jobCursors == nil {
		r.jobCursors = map[string]int64{}
	}
	r.jobCursors[jobCursorKey(sessionID, jobID)] = pos
}

// jobOutputDef：读后台任务的输出（增量 + 可选等待）。
//
// 游标记在注册表里（按会话 + 任务隔离）而不是让模型自己传：模型不该被要求
// 记住"上次读到第几字节"，它只要说"再给我点新输出"。
//
// wait_condition（2026-12 用户拍板：等待原语）是 wait=true 的谓词——不新增工具，
// 给现有 job_output 扩参数。五种条件 OR 组合、谁先到返回谁；条件命中时结果带
// hit 字段。等已有任务进度（构建出某行日志/端口起来了）用它，别再起轮询循环。
func jobOutputDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {"type": "string", "description": "任务 id（bash 的 run_in_background 返回，或 job_list 里的）"},
			"wait": {"type": "boolean", "description": "true = 阻塞等（默认等「任务结束」，默认 30s，上限 600s）；超时返回当前输出 + [status: running]"},
			"wait_condition": {"oneOf": [
				{"type": "string", "enum": ["exit", "exit_ok", "output", "idle"]},
				{"type": "object", "properties": {"pattern": {"type": "string"}}, "required": ["pattern"], "additionalProperties": false}
			], "description": "wait=true 时等什么（OR 语义——多个条件谁先到返回谁；缺省 = exit）。exit=任务结束（现行为）；exit_ok=任务结束且退出码 0（status=completed）；output=有新输出即返回（配 wait=true 就是「等下一段输出」，不要轮询）；idle=输出静默 idle_ms（默认 5000）——「构建安静下来了」的判据；{\"pattern\": \"…\"}=全量输出日志命中正则即返回（对落盘的完整日志精确匹配，不是仅增量；大小写敏感）"},
			"idle_ms": {"type": "integer", "description": "wait_condition=idle 的静默毫秒数（默认 5000）"},
			"timeout_ms": {"type": "integer", "description": "wait 的等待上限毫秒数（默认 30000，上限 600000）"},
			"max_bytes": {"type": "integer", "description": "单次读取的字节上限（默认 32768，上限 262144）"}
		},
		"required": ["job_id"]
	}`)
	return &Def{
		Name: jobOutputToolName,
		Description: "读后台任务（bash 的 run_in_background）的输出。默认只返回**上次读过之后的新增部分**" +
			"（无新输出时回 (no new output)）；wait=true 阻塞等待，wait_condition 选择等什么：" +
			"exit（默认，等任务结束）/ exit_ok（退出码 0）/ output（有新输出即回）/ idle（输出静默 N 秒）" +
			"/ {pattern:\"…\"}（全量输出日志命中正则即回，结果带 hit 命中行）——多个条件 OR 组合，" +
			"谁先到返回谁。「等任务跑到某行日志/端口就绪」这类等待用它，不要起轮询循环" +
			"（默认 30s、上限 600s）。任务结束后仍可读（完整日志落盘在 OutputPath）。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				JobID     string          `json:"job_id"`
				Wait      bool            `json:"wait"`
				Condition json.RawMessage `json:"wait_condition"`
				IdleMS    int             `json:"idle_ms"`
				TimeoutMS int             `json:"timeout_ms"`
				MaxBytes  int             `json:"max_bytes"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			id := strings.TrimSpace(a.JobID)
			if id == "" {
				return "", fmt.Errorf("job_id 不能为空（用 job_list 看有哪些任务）")
			}
			if a.TimeoutMS < 0 {
				return "", fmt.Errorf("timeout_ms 不能为负")
			}
			cond, err := parseWaitCondition(a.Condition, a.IdleMS)
			if err != nil {
				return "", err
			}
			mgr := r.getJobs()
			if mgr == nil {
				return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
			}
			maxBytes := a.MaxBytes
			if maxBytes <= 0 {
				maxBytes = jobOutputDefaultBytes
			}
			if maxBytes > jobOutputMaxBytes {
				maxBytes = jobOutputMaxBytes
			}
			sessionID := SessionID(ctx)
			data, next, snap, err := mgr.Read(id, r.jobCursor(sessionID, id), maxBytes)
			if err != nil {
				return "", err
			}
			// output 谓词的基线是**会话游标在本次读之前的值**——即上一轮读到的
			// 位置：本次 Read 已经把 `already here` 读走了，next=24 已包含它，
			// 拿它当基线会把「上一轮超时路径已补读的新输出」漏算成基线之外。
			// 会话游标（读之前）才是「上次等完后看到哪」的语义锚。
			baseline := r.jobCursor(sessionID, id)
			r.setJobCursor(sessionID, id, next)
			var hit *waitCondHit
			// 执行会话通告口：子会话 wait 等任务时，settle 即由 server 把通告
			// 投进子会话（不按时间线归属——那是 server 广播路径的事）。nil =
			// 未注入（无通道，行为与以前一致）。
			notify := JobNotify(ctx)
			if a.Wait && !snap.Status.Terminal() {
				timeout := jobWaitDefault
				if a.TimeoutMS > 0 {
					timeout = time.Duration(min(a.TimeoutMS, int(jobWaitMax/time.Millisecond))) * time.Millisecond
				}
				// hit：条件谓词给出的命中信息（exit/exit_ok/output/idle/pattern
				// 命中时带，措辞见 formatJobOutput）；超时或取消 → nil
				//（没等到就是没等到，不编 hit）。
				snap, hit = waitJob(ctx, mgr, id, timeout, cond, baseline, notify)
				// 等待期间可能又写了输出：把增量补上（游标接着走）
				if more, next2, snap2, rerr := mgr.Read(id, next, maxBytes); rerr == nil {
					data, next, snap = data+more, next2, snap2
					r.setJobCursor(sessionID, id, next)
				}
			}
			return formatJobOutput(snap, data, hit), nil
		},
	}
}

// waitCond 是一次等待的条件集合（OR 语义——任何一条满足即返回）。
type waitCond struct {
	exit    bool     // 任务结束（缺省谓词，wait=true 的现行为）
	exitOK  bool     // 结束且 Status=completed
	output  bool     // 相对基线游标有新输出即返回
	idle    bool     // 输出静默 idleDur 即返回
	idleDur time.Duration
	pattern *regexp.Regexp // 全量输出日志命中即返回
}

// jobIdleDefault 是 idle 谓词的默认静默窗（输出安静 5s 视为「阶段性完成」）。
const jobIdleDefault = 5 * time.Second

// parseWaitCondition 解析 wait_condition 参数（nil = 缺省 exit）。
//
// schema 用 oneOf 允许两种形态：枚举字符串（单条件）或 {"pattern": "…"} 对象。
// 多条件 OR 组合由**数组**承载——oneOf 的两种形态之外，数组里混排也认（宽容解析：
// 模型写 `["output", {"pattern": "build done"}]` 是合法的 OR 诉求，不是参数错）。
// 正则编译失败报参数错（模型当场重试，不是静默等到超时）。
func parseWaitCondition(raw json.RawMessage, idleMS int) (*waitCond, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return &waitCond{exit: true}, nil
	}
	cond := &waitCond{}
	items := []json.RawMessage{raw}
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("wait_condition 解析失败: %w", err)
		}
	}
	for _, item := range items {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			switch s {
			case "exit":
				cond.exit = true
			case "exit_ok":
				cond.exitOK = true
			case "output":
				cond.output = true
			case "idle":
				cond.idle = true
			default:
				return nil, fmt.Errorf("wait_condition 未知谓词 %q（可用: exit / exit_ok / output / idle / {\"pattern\": \"…\"}）", s)
			}
			continue
		}
		var obj struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(item, &obj); err != nil {
			return nil, fmt.Errorf("wait_condition 必须是枚举字符串或 {\"pattern\": \"…\"}: %w", err)
		}
		if obj.Pattern == "" {
			return nil, fmt.Errorf("wait_condition 对象形态必须有非空 pattern")
		}
		re, err := regexp.Compile(obj.Pattern)
		if err != nil {
			return nil, fmt.Errorf("wait_condition 的 pattern 不是合法正则: %v", err)
		}
		cond.pattern = re
	}
	if !cond.exit && !cond.exitOK && !cond.output && !cond.idle && cond.pattern == nil {
		return nil, fmt.Errorf("wait_condition 至少要有一个条件")
	}
	if cond.idle && idleMS < 0 {
		return nil, fmt.Errorf("idle_ms 不能为负")
	}
	if cond.idle {
		cond.idleDur = jobIdleDefault
		if idleMS > 0 {
			cond.idleDur = time.Duration(idleMS) * time.Millisecond
		}
	}
	return cond, nil
}

// waitCondHit 是条件命中时回带的说明（结果文本的 hit 行）。
type waitCondHit struct {
	kind    string // 命中的谓词名（output / idle / pattern）
	pattern string // pattern 谓词的正则原文
	line    string // pattern 命中的那一行原文
}

// waitJob 等任务满足 wait_condition（订阅 output + settled 事件；超时/取消则返回当前快照）。
//
// 先订阅再查快照：反过来的话，两者之间发生的 output/settle 会**丢掉**，
// 于是 wait 白等到超时（返回的 status 还停在 running）。订阅**必须同时含
// EventOutput**——output/idle/pattern 谓词的满足信号正是它（只订 settled 的
// 话「等下一段输出」会静默等到超时）。
//
// data0Total 是进入等待前已读到的绝对游标（Read 返回的 next；output 谓词的基线：
// 累计字节数超过它即「有新输出」——跨缓冲滚动单调，与 Read 语义严格一致）。
//
// 快照立即判定：进入等待前条件就已满足的（订阅前刚写完最后一行/正则早已命中）
// 也要命中——订阅-查询窗口的竞态不该让等待变成整段超时。
//
// onSettle 是等待方的**执行会话通告口**（ctx 注入，见 sessionstate.go 的
// WithJobNotify）：后台任务的结束通告按时间线归属投递，执行会话（尤其子会话）
// 自己收不到——子 Agent wait 等到的只是超时快照，之后它不知道任务其实已结束，
// 只能继续 sleep 盲等（真库实锤：验收子会话 sleep 累计 53.5 分钟、job_output
// 调用 0 次）。订阅到 settle 即把通告转给执行会话（server 侧 Notify：忙则轮边界
// 注入、空闲则唤醒——即便本轮 wait 超时返回，任务结束时子会话也会被点亮）。
func waitJob(ctx context.Context, mgr *jobs.Manager, id string, timeout time.Duration, cond *waitCond, data0Total int64, onSettle func(string)) (jobs.Snapshot, *waitCondHit) {
	done := make(chan struct {
		snap jobs.Snapshot
		hit  *waitCondHit
	}, 1)
	// idleWake 是「有新输出、静默被打断」的信号（idle 计时重设）。
	idleWake := make(chan struct{}, 1)
	notify := func(ev jobs.Event) {
		if ev.Snapshot.ID != id {
			return
		}
		if ev.Kind == jobs.EventSettled && onSettle != nil {
			onSettle(jobSettleNotice(ev.Snapshot))
		}
		if hit := condHit(ev.Snapshot, cond, data0Total); hit != nil {
			select {
			case done <- struct {
				snap jobs.Snapshot
				hit  *waitCondHit
			}{ev.Snapshot, hit}:
			default:
			}
		}
	}
	unsub := mgr.Subscribe(notify)
	defer unsub()
	// 快照立即判定（订阅之后做：真订阅丢事件与快照误判都消失——订阅在，
	// 两者之间发生的 output/settle 会走 notify；这里只是把「早就满足」的
	// 情形当场收割，不让它白等）。
	if snap, err := mgr.Snapshot(id); err == nil {
		if hit := condHit(snap, cond, data0Total); hit != nil {
			return snap, hit
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// idle 的计时起点 = 订阅时的静默时长（快照已判定过一轮：那时还不满足的
	// idle，从现在起还要再静默「剩余时长」）。idleC 是 nil-channel 惯用法：
	// cond.idle=false 时 select 永远不会选它（不空转）。
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if cond.idle {
		remaining := cond.idleDur
		if snap, err := mgr.Snapshot(id); err == nil {
			remaining = cond.idleDur - time.Since(lastOutputAtOf(snap))
			if remaining <= 0 {
				remaining = time.Millisecond // 已静默超窗：到点核快照收割
			}
		}
		idleTimer = time.NewTimer(remaining)
		defer idleTimer.Stop()
		idleC = idleTimer.C
	}
	// output 事件会打断静默：重设 idle 计时（订阅回调里只置标志，这里统一处理——
	// 回调跑在任意 goroutine，Timer.Reset 从回调调有竞态）。
	unsubIdleWake := mgr.Subscribe(func(ev jobs.Event) {
		if cond.idle && ev.Kind == jobs.EventOutput && ev.Snapshot.ID == id {
			idleWake <- struct{}{}
		}
	})
	defer unsubIdleWake()
	for {
		select {
		case res := <-done:
			return res.snap, res.hit
		case <-idleC:
			// idle 到点：核快照确认真的静默超窗（output 事件打断静默时会
			// 重设 idleTimer，这里核快照兜底竞态），到期即命中；未到期 Reset
			// 继续等——不能走超时出口，否则 idle 一次打断就变成超时返回。
			if snap, err := mgr.Snapshot(id); err == nil {
				if time.Since(lastOutputAtOf(snap)) >= cond.idleDur {
					return snap, &waitCondHit{kind: "idle"}
				}
			}
			idleTimer.Reset(cond.idleDur)
		case <-idleWake:
			// 静默被打断（有新输出）：从头再等一个静默窗口
			idleTimer.Reset(cond.idleDur)
		case <-timer.C:
			// 超时：按现行为返回当前快照（hit=nil——没等到就是没等到）
			if snap, err := mgr.Snapshot(id); err == nil {
				return snap, nil
			}
			return jobs.Snapshot{ID: id}, nil
		case <-ctx.Done():
			// 取消：同超时出口
			if snap, err := mgr.Snapshot(id); err == nil {
				return snap, nil
			}
			return jobs.Snapshot{ID: id}, nil
		}
	}
}

// lastOutputAtOf 是 idle 谓词的时间锚：最近一次写输出；从未写过则用任务起点
//（「起了就没吭声」的静默从起点算，不编一个零值时刻去糊弄 time.Since）。
func lastOutputAtOf(snap jobs.Snapshot) time.Time {
	if !snap.LastOutputAt.IsZero() {
		return snap.LastOutputAt
	}
	return snap.StartedAt
}

// condHit 对一份快照跑一遍谓词（快照立即判定与事件路径共用同一份判定——
// 两条路径漂移的代价是同一条件一处命中一处不中）。
//
// exit / exit_ok 谓词命中时返回带 kind 的 hit（formatJobOutput 据此多一行
// [hit: …]——模型要能区分「等到了 exit」和「等到了 pattern 命中」，下一步
// 决策不同）。exit_ok 在结束但非 completed 时不命中——等待继续走到超时，
// 如实报 [status: failed/killed]。
func condHit(snap jobs.Snapshot, cond *waitCond, data0Total int64) *waitCondHit {
	switch {
	case snap.Status.Terminal():
		if cond.exit {
			return &waitCondHit{kind: "exit"}
		}
		if cond.exitOK && snap.Status == jobs.StatusCompleted {
			return &waitCondHit{kind: "exit_ok"}
		}
	}
	if cond.output {
		if total, err := curOutputOf(snap.ID); err == nil && total > data0Total {
			return &waitCondHit{kind: "output"}
		}
	}
	if cond.pattern != nil {
		if line, ok := matchFullLog(snap.OutputPath, cond.pattern); ok {
			return &waitCondHit{kind: "pattern", pattern: cond.pattern.String(), line: line}
		}
	}
	return nil
}

// jobListDef：列后台任务（新的在前）。
func jobListDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"session_only": {"type": "boolean", "description": "true = 只列本会话起的任务（默认 false = 全部会话，含别的会话里常驻的 dev server）"}
		}
	}`)
	return &Def{
		Name: jobListToolName,
		Description: "列后台任务（新的在前）：id、状态、命令摘要、结束方。" +
			"session_only=true 只看本会话；默认列全部——跨会话的常驻任务（dev server）也该看得见。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				SessionOnly bool `json:"session_only"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			mgr := r.getJobs()
			if mgr == nil {
				return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
			}
			sessionID := ""
			if a.SessionOnly {
				// 按**时间线归属**过滤（不是执行会话）：主 Agent 要看得见它派出去
				// 的子 Agent 起的任务——那正是它自己时间线上的卡
				sessionID = OwnerSessionID(ctx)
				if sessionID == "" {
					sessionID = SessionID(ctx)
				}
				if sessionID == "" {
					return "", fmt.Errorf("当前会话没有 id，无法只列本会话的任务（改用 session_only=false）")
				}
			}
			return formatJobList(mgr.List(sessionID)), nil
		},
	}
}

// jobKillDef：停止一个后台任务（EndedBy=agent）。
//
// 非阻塞：只回「已请求取消」，真正的收尾由 producer 完成——工具立刻返回
// 模型才能继续做事，而结束结果随后由唤醒通告送达。
func jobKillDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {"type": "string", "description": "要停止的任务 id（job_list 里的）"}
		},
		"required": ["job_id"]
	}`)
	return &Def{
		Name: jobKillToolName,
		Description: "停止一个后台任务（你自己起的）。非阻塞——立刻回「已请求取消」，" +
			"真正的结束由进程收尾时定稿。注意：你停掉的任务**不要**自己重启，" +
			"要等用户指示（用户停掉的任务同理，通告里会说明）。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			id := strings.TrimSpace(a.JobID)
			if id == "" {
				return "", fmt.Errorf("job_id 不能为空（用 job_list 看有哪些任务）")
			}
			mgr := r.getJobs()
			if mgr == nil {
				return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
			}
			snap, err := mgr.Kill(id, jobs.EndedAgent)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("已请求取消任务 %s（%s），当前状态 %s。\n"+
				"它是被你自己停掉的——不要重启它；收尾后可用 job_output 读最终输出。",
				snap.ID, snap.Label, snap.Status), nil
		},
	}
}

// formatJobOutput 渲染一次读取结果：状态行 + 增量输出 + [status: ...]。
//
// 末尾的 [status: X] 是契约要求（超时等待时返回当前输出 + [status: running]），
// 模型据此判断「它还在跑」还是「已经结束、该看结论了」。
// hit != nil 时在状态行下带一行命中说明（等待原语的结果契约：模型要能区分
// 「等到了 exit」和「等到了 pattern 命中」，下一步决策不同）。
func formatJobOutput(snap jobs.Snapshot, data string, hit *waitCondHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务 %s（%s）状态: %s", snap.ID, snap.Label, snap.Status)
	if snap.Status.Terminal() {
		fmt.Fprintf(&b, "（结束方: %s", snap.EndedBy)
		if snap.Detail != "" {
			fmt.Fprintf(&b, "，%s", snap.Detail)
		}
		b.WriteString("）")
	}
	b.WriteString("\n")
	if hit != nil {
		switch hit.kind {
		case "output":
			b.WriteString("[hit: output — 出现了新输出]\n")
		case "idle":
			b.WriteString("[hit: idle — 输出已静默]\n")
		case "pattern":
			fmt.Fprintf(&b, "[hit: pattern %q 命中] %s\n", hit.pattern, hit.line)
		case "exit_ok":
			b.WriteString("[hit: exit_ok — 任务成功（退出码 0）]\n")
		case "exit":
			b.WriteString("[hit: exit — 任务已结束]\n")
		}
	}
	if data == "" {
		b.WriteString("(no new output)\n")
	} else {
		b.WriteString(data)
		if !strings.HasSuffix(data, "\n") {
			b.WriteString("\n")
		}
	}
	if snap.OutputPath != "" {
		fmt.Fprintf(&b, "完整日志: %s\n", snap.OutputPath)
	}
	fmt.Fprintf(&b, "[status: %s]", snap.Status)
	return b.String()
}

// curOutputOf 取任务**当前累计字节数**（output 谓词的基线比对源）。基线
// data0Total 是 Exec 开头那次 Read 返回的绝对游标，比对必须对**同一游标语义**
// 进行——按全量输出长度比对会在缓冲滚动后错判（基线只含新读段、全量含历史段）。
// 累计字节数跨缓冲滚动单调，与 Read 语义严格一致。
func curOutputOf(id string) (int64, error) {
	mgr := currentJobsManager()
	if mgr == nil {
		return 0, fmt.Errorf("后台任务未装配")
	}
	_, next, _, err := mgr.Read(id, 0, 0)
	return next, err
}

// matchFullLog 对任务的**全量落盘日志**做正则匹配，返回第一条命中行。
//
// 只在「等某行日志出现」这种场景用——它是全量匹配不是增量匹配：命中早已存在
// 的历史行也会立即返回（快照立即判定），这是语义的一部分（等待方要的往往是
// 「构建曾经报出过 X」，不是「等待期间新报的 X」）。
func matchFullLog(path string, re *regexp.Regexp) (string, bool) {
	if path == "" || re == nil {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if re.MatchString(line) {
			return strings.TrimRight(line, "\r"), true
		}
	}
	return "", false
}

// formatJobList 渲染任务列表（新的在前）。
func formatJobList(list []jobs.Snapshot) string {
	if len(list) == 0 {
		return "当前没有后台任务。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "后台任务（%d 个，新的在前）:\n", len(list))
	for _, s := range list {
		fmt.Fprintf(&b, "- %s [%s] %s", s.ID, s.Status, s.Label)
		if s.EndedBy != "" {
			fmt.Fprintf(&b, "（结束方 %s", s.EndedBy)
			if s.Detail != "" {
				fmt.Fprintf(&b, "，%s", s.Detail)
			}
			b.WriteString("）")
		} else {
			fmt.Fprintf(&b, "（已运行 %s）", time.Since(s.StartedAt).Round(time.Second))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// jobSettleNotice 把一次 settle 快照翻成**投给执行会话**的通告文本。
//
// 措辞对齐 server/jobs.go 的 jobNoticeText（契约 docs/jobs.md §5 的表），
// 但**独立一份**：分层守卫禁止 tools import server，而 server 的那份按
// EndedBy 过滤后还要带 protocol 前缀走广播路径；这里只服务 wait 订阅回调
// ——通告口（JobNotify）本来就是过滤后的（server 只注入给非 agent/非 backend
// 结束的情形），这里只按状态给一句「结束了 + 下一步」。
// agent/后端结束的快照不会走到这里（见 job_notify.go 的回调）。
func jobSettleNotice(snap jobs.Snapshot) string {
	label := snap.Label
	if label == "" {
		label = snap.ID
	}
	switch {
	case snap.Status == jobs.StatusCompleted:
		return fmt.Sprintf("后台任务 %s 已结束（%s）。用 job_output(job_id=%q) 读输出。",
			label, detailOrJob(snap.Detail, "退出码 0"), snap.ID)
	case snap.Detail == jobs.TimeoutDetail:
		return fmt.Sprintf("后台任务 %s 超时被杀。", label)
	default:
		return fmt.Sprintf("后台任务 %s 已结束（%s）。用 job_output(job_id=%q) 读输出。",
			label, detailOrJob(snap.Detail, string(snap.Status)), snap.ID)
	}
}

func detailOrJob(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
