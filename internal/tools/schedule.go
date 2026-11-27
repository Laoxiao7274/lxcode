// schedule 工具（B2 定时/提醒任务）：设一个到点提醒——到点后 jobs 的通告链
// 把提醒正文投给会话（唤醒 agent 执行「用户要提醒的事」）。
//
// 与 run_in_background bash 的分工：那是「跑一个长命令等结果」，这是「过一段
// 时间唤醒会话做一件事」——没有进程，只有一次到点投递。
//
// 参数：duration_minutes（相对延时）或 at（RFC3339 绝对时刻），二选一（都给
// 以 at 为准）；note 是要提醒的事（到点通告的正文——模型醒来看到的就是它）。
// 只允许未来时刻：过去的 at/duration ≤0 拒绝（立刻触发的提醒没有意义——
// 模型应直接说而不是绕道 timer）。
//
// 风险分级：低危（Mutates=false——不改工作区；到点通告走既有唤醒预算链，
// 不会造成无界自动开轮）。持久化在 store（server 接线侧 SaveTimer）——
// 后端重启后 timer 重建（timers 表的行是唯一事实源）。

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// ScheduleToolName 是 schedule 工具的 id（工具名字符集同束）。
const ScheduleToolName = "schedule"

// scheduleRefFn / SetSchedule 注入模式（与 SetJobs 同款——server 装配时接线）。
var scheduleRefFn func() *Registry

// ScheduleFn 是「落库 + 起任务」的实现约定：server 装配侧持有 store 与
// jobs.Manager，工具层不碰两者。返回 job id（结果里回给模型——到点后它会
// 在 job_list 里看到这次触发）。
type ScheduleFn func(ctx context.Context, spec jobs.Spec) (string, error)

// SetSchedule 注入定时任务实现（server 装配时调用）。
func (r *Registry) SetSchedule(fn ScheduleFn) {
	r.searchMu.Lock()
	r.schedule = fn
	r.searchMu.Unlock()
}

func (r *Registry) getSchedule() ScheduleFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.schedule
}

// scheduleParams 是 schedule 的参数形状。
type scheduleParams struct {
	// DurationMinutes 相对延时（>0）；at 给了时忽略。
	DurationMinutes float64 `json:"duration_minutes"`
	// At 绝对时刻（RFC3339，如 2026-12-31T15:04:00+08:00）。
	At   string `json:"at"`
	Note string `json:"note"`
}

// scheduleDef 返回 schedule 的注册声明。
func scheduleDef(r *Registry) *Def {
	scheduleRefFn = func() *Registry { return r }
	return &Def{
		Name: ScheduleToolName,
		Description: "设一个定时提醒：到点后把要提醒的事投递给当前会话（唤醒智能体处理）。" +
			"参数二选一：duration_minutes（多少分钟后）或 at（RFC3339 时刻，如 2026-12-31T15:04:00+08:00）。" +
			"note 是到点要提醒的事（务必写清用户要做什么——它就是到点时的唤醒通告正文）。" +
			"提醒跨后端重启持久化。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "duration_minutes": {"type": "number", "description": "多少分钟后提醒（>0；与 at 二选一，都给时以 at 为准）"},
    "at": {"type": "string", "description": "绝对时刻（RFC3339，含时区偏移）；到点触发"},
    "note": {"type": "string", "description": "到点要提醒的事（一句话写清：谁要做什么——它就是到点时的通告正文）"}
  },
  "required": ["note"]
}`),
		Risk:    RiskLow,
		Mutates: false,
		Exec:    execSchedule,
	}
}

// execSchedule 是 schedule 的执行：解析参数 → 组 Spec → 注入实现落库起任务。
func execSchedule(ctx context.Context, args json.RawMessage) (string, error) {
	var p scheduleParams
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	p.Note = strings.TrimSpace(p.Note)
	if p.Note == "" {
		return "", fmt.Errorf("缺少 note（到点要提醒的事——没有正文的提醒等于空唤醒）")
	}
	var due time.Time
	switch {
	case strings.TrimSpace(p.At) != "":
		t, err := time.Parse(time.RFC3339, p.At)
		if err != nil {
			return "", fmt.Errorf("at 不是合法的 RFC3339 时刻（示例 2026-12-31T15:04:00+08:00）: %v", err)
		}
		due = t
	case p.DurationMinutes > 0:
		due = time.Now().Add(time.Duration(p.DurationMinutes * float64(time.Minute)))
	default:
		return "", fmt.Errorf("缺少到点时间：给 duration_minutes（>0 的分钟数）或 at（RFC3339 时刻）")
	}
	if !due.After(time.Now()) {
		return "", fmt.Errorf("到点时间必须在未来（收到 %s；立刻要说的话直接说，不用定时）", due.Format(time.RFC3339))
	}
	// 上限护栏：timer 不是日历——超过 30 天的提醒多半是模型理解偏了
	if due.After(time.Now().Add(30 * 24 * time.Hour)) {
		return "", fmt.Errorf("定时最长 30 天（收到 %s；更远的安排请让用户用系统日历）", due.Format(time.RFC3339))
	}
	spec := jobs.Spec{
		Kind:           jobs.TimerKind,
		Label:          clipOneLineLocal(p.Note, 60),
		SessionID:      SessionID(ctx),
		OwnerSessionID: OwnerSessionID(ctx),
		Workspace:      Workspace(ctx),
		DueAt:          due,
		Note:           p.Note,
	}
	if err := jobs.ValidateTimer(spec); err != nil {
		return "", fmt.Errorf("定时参数不完整: %w", err)
	}
	fn := scheduleRefFn().getSchedule()
	if fn == nil {
		return "", fmt.Errorf("定时任务未装配（后端未初始化任务管理器）")
	}
	id, err := fn(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("设定时任务失败: %w", err)
	}
	return fmt.Sprintf("已设定提醒（任务 %s）：%s\n到点时间：%s。到点后提醒会自动投递到本会话，无需等待或轮询。",
		id, p.Note, due.Format("2006-01-02 15:04")), nil
}

// clipOneLineLocal 是摘要截断（jobs 的 commandLabel 同形态——包内不 import jobs
// 的私有实现，自己剪一份 60 字）。
func clipOneLineLocal(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
