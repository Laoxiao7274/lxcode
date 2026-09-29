package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

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
}

func (r *Registry) getJobs() *jobs.Manager {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	return r.jobsMgr
}

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
func jobOutputDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {"type": "string", "description": "任务 id（bash 的 run_in_background 返回，或 job_list 里的）"},
			"wait": {"type": "boolean", "description": "true = 阻塞等这个任务结束（默认 30s，上限 600s）；超时返回当前输出 + [status: running]"},
			"timeout_ms": {"type": "integer", "description": "wait 的等待上限毫秒数（默认 30000，上限 600000）"},
			"max_bytes": {"type": "integer", "description": "单次读取的字节上限（默认 32768，上限 262144）"}
		},
		"required": ["job_id"]
	}`)
	return &Def{
		Name: jobOutputToolName,
		Description: "读后台任务（bash 的 run_in_background）的输出。默认只返回**上次读过之后的新增部分**" +
			"（无新输出时回 (no new output)）；wait=true 阻塞等它结束（默认 30s、上限 600s），" +
			"适合「等构建/测试跑完再继续」。任务结束后仍可读（完整日志落盘在 OutputPath）。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				JobID     string `json:"job_id"`
				Wait      bool   `json:"wait"`
				TimeoutMS int    `json:"timeout_ms"`
				MaxBytes  int    `json:"max_bytes"`
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
			r.setJobCursor(sessionID, id, next)
			if a.Wait && !snap.Status.Terminal() {
				timeout := jobWaitDefault
				if a.TimeoutMS > 0 {
					timeout = time.Duration(min(a.TimeoutMS, int(jobWaitMax/time.Millisecond))) * time.Millisecond
				}
				snap = waitJob(ctx, mgr, id, timeout)
				// 等待期间可能又写了输出：把增量补上（游标接着走）
				if more, next2, snap2, rerr := mgr.Read(id, next, maxBytes); rerr == nil {
					data, next, snap = data+more, next2, snap2
					r.setJobCursor(sessionID, id, next)
				}
			}
			return formatJobOutput(snap, data), nil
		},
	}
}

// waitJob 等任务收尾（订阅 settled 事件；超时/取消则返回当前快照）。
//
// 先订阅再查快照：反过来的话，两者之间发生的 settle 会**丢掉**，
// 于是 wait=true 白等到超时（返回的 status 还停在 running）。
func waitJob(ctx context.Context, mgr *jobs.Manager, id string, timeout time.Duration) jobs.Snapshot {
	done := make(chan jobs.Snapshot, 1)
	unsub := mgr.Subscribe(func(ev jobs.Event) {
		if ev.Kind == jobs.EventSettled && ev.Snapshot.ID == id {
			select {
			case done <- ev.Snapshot:
			default:
			}
		}
	})
	defer unsub()
	if _, _, snap, err := mgr.Read(id, 0, 0); err == nil && snap.Status.Terminal() {
		return snap
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case snap := <-done:
		return snap
	case <-timer.C:
	case <-ctx.Done():
	}
	if _, _, snap, err := mgr.Read(id, 0, 0); err == nil {
		return snap
	}
	return jobs.Snapshot{ID: id}
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
				sessionID = SessionID(ctx)
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
func formatJobOutput(snap jobs.Snapshot, data string) string {
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
