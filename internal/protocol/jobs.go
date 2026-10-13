package protocol

// ---- 后台任务（jobs——docs/jobs.md §4）----
//
// JobNoticePrefix 标注「这条消息是后台任务通告，不是用户说的」。
//
// 唤醒投递往归属会话发一条消息（契约 §5），前端据此把气泡渲染成通告而不是
// 用户气泡——没有这个标记，用户会以为那句话是自己说的。常量放协议包而不是
// agent 包：agent 不 import protocol（分层守卫），服务端组装文本时拼上它。
const JobNoticePrefix = "[后台任务通告] "

// JobInfo 是后台任务的对外快照（job.list 的条目与 job.started/job.settled
// 事件的载荷同款——一份形状，前端只需一套解析）。
//
// 时间字段是 RFC3339 字符串；FinishedAt 为空 = 未结束。
type JobInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	EndedBy   string `json:"ended_by,omitempty"`
	Detail    string `json:"detail,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// OwnerSessionID 是**时间线归属**（顶层会话）：子 Agent 起的任务挂在父会话上，
	// 前端据此把它放进父会话的时间线、后端据此把唤醒通告投给父会话。
	OwnerSessionID string `json:"owner_session_id,omitempty"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at,omitempty"`
	// OutputTail 是最近 64KB 输出（面板直接显示，不必再拉一次 job.log）。
	OutputTail string `json:"output_tail,omitempty"`
	// OutputPath 是落盘日志路径（「查看输出」用它；降级为纯内存时为空）。
	OutputPath string `json:"output_path,omitempty"`
}

// JobListParams 是 job.list 的参数：SessionID 为空 = 全部会话。
//
// 与 job_list 工具的 session_only 同语义：默认列全部——跨会话常驻的
// dev server 在别的会话里也该看得见。
type JobListParams struct {
	SessionID string `json:"session_id,omitempty"`
}

// JobListResult 是任务列表（新的在前）。
type JobListResult struct {
	Jobs []JobInfo `json:"jobs"`
}

// JobKillParams 是 job.kill 的参数。
type JobKillParams struct {
	ID string `json:"id"`
}

// JobKillResult 回取消请求后的快照（状态通常是 stopping——真正的 settle
// 由 producer 收尾时定稿，随后的 job.settled 事件才是终态）。
type JobKillResult struct {
	Job JobInfo `json:"job"`
}

// JobLogParams 是 job.log 的参数（读全量落盘日志）。
type JobLogParams struct {
	ID string `json:"id"`
}

// JobLogResult 是全量日志：超上限时回**尾部**并置 Truncated
// （日志的尾部信息量最大——用户想知道的通常是"它最后报了什么"）。
type JobLogResult struct {
	Data      string `json:"data"`
	Truncated bool   `json:"truncated"`
}
