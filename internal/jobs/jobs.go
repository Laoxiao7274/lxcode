// Package jobs 实现后台任务（jobs）的进程级注册表：任务句柄、输出保留
// （内存尾缓冲 + 全量落盘）、增量读取、取消归属与结束广播。
//
// 契约见 docs/jobs.md §2——本包是那份契约的实现，改行为先改契约。它只依赖
// 标准库：tools（bash 与三个 job_* 工具）与 server（唤醒投递）共同使用它，
// 它不能反向依赖它们（分层规则：叶子包）。
//
// 三条硬纪律：
//  1. 落盘失败不能让任务起不来——降级为纯内存并记日志；
//  2. EndedBy 是「谁结束的」的唯一入口：用户按钮与 agent 工具走同一条 Kill
//     路径，只有 by 不同（DSH 的教训：两者在数据里长得一样，agent 醒来只能
//     自己脑补下一步）；
//  3. 全程并发安全：多个任务并发写、订阅者在任意 goroutine 上被回调。
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status 是任务的运行状态。
type Status string

const (
	StatusRunning   Status = "running"
	StatusStopping  Status = "stopping" // 已请求取消，进程还没收尾
	StatusCompleted Status = "completed"
	StatusKilled    Status = "killed"
	StatusFailed    Status = "failed"
)

// Terminal 报告状态是否已结束（settle 之后不再变化）。
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusKilled || s == StatusFailed
}

// EndedBy 是「谁结束的」。空 = 还在跑。
type EndedBy string

const (
	EndedSelf    EndedBy = "self"    // 自己退出（含非零退出码 → StatusFailed）
	EndedUser    EndedBy = "user"    // 用户在应用里点了「结束」
	EndedAgent   EndedBy = "agent"   // agent 调 job_kill
	EndedBackend EndedBy = "backend" // 后端退出/重启（启动时清孤儿也用它）
)

// TimeoutDetail 是「超时被杀」的 Detail 取值。
//
// 唤醒投递要区分「超时」与「普通失败」（docs/jobs.md §5 的表），而 Snapshot
// 只有 Detail 一个自由文本字段。判据用这个**导出常量**而不是让投递方去猜
// 字符串：producer 与投递方共用同一个字面量，改措辞不会让判定静默失效。
const TimeoutDetail = "timeout"

// ShutdownDetail 是后端退出时统一写进 Detail 的说明。
const ShutdownDetail = "后端重启"

// jobsPidLineHeader 是任务日志首行的前缀（Start 时写占位、RecordPid 补数字）。
// 孤儿清扫按 `pid=<n>` 行定位残留进程；`@<unix-ns>` 后缀（RecordPid 补写时的
// 时刻）是 pid 复用校验的锚点——旧格式（无后缀）的存量日志回退为「存在就杀」。
const jobsPidLineHeader = "pid="

// pidTimeTolerance 是孤儿清扫 pid 复用校验的容差：真实残留进程的创建时刻与
// RecordPid 写 pid 行的时刻相差不过调度延迟（毫秒级），5s 容差已远超时钟
// 精度与文件系统时间戳抖动；被复用撞上的无关进程创建时刻必然晚于记录时刻
//（差可以任意大），不会落进这个窗口。
const pidTimeTolerance = 5 * time.Second

// RecordPid 把进程 pid 与**写入时刻**写进落盘日志首行（B3：孤儿清扫的定位
// 依据；@ 时刻是 pid 复用校验的锚点——我们的残留进程诞生于记录那一刻，
// 被复用的无关进程诞生于记录之后）。
//
// 谁起进程谁登记（Manager 不碰 os/exec）：bash 后台路径在 cmd.Start 成功后
// 调用。写失败只记日志——清扫退化为状态行判定，任务照常运行。
// 挂在 Job 接口上：tools 只持 Job 句柄（Start 的返回值），拿不到 *job 具体类。
func (j *job) RecordPid(pid int) {
	if pid <= 0 {
		return
	}
	j.mu.Lock()
	id, f := j.id, j.file
	j.mu.Unlock()
	if f == nil {
		return
	}
	line := fmt.Sprintf("%s%d @ %d\n", jobsPidLineHeader, pid, time.Now().UnixNano())
	if _, err := f.WriteAt([]byte(line), 0); err != nil {
		log.Printf("后台任务 %s 写 pid 行失败（孤儿清扫将按状态行判定）: %v", id, err)
	}
}

// OrphanReport 是一份孤儿日志的判定结果（ReapOrphans 返回给调用方记日志/汇报）。
type OrphanReport struct {
	ID     string // 任务 id（日志文件名去 .log）
	Pid    int    // 日志里记的 pid（0 = 没读到）
	Killed bool   // 是否实际执行了树杀（进程存活时为真）
}

// ReapOrphans 启动时的孤儿清扫（B3）：扫 <dir>/jobs/*.log，凡**没有终态
// 标记**（日志尾部没有 EndedBy 状态行——后端优雅退出会先 Shutdown 全部任务，
// 只剩硬杀/断电留下的）的任务视为孤儿：读 pid 行，进程存活则树杀 + 标记
// EndedBackend。
//
// 为什么不直接信 pid：pid 可能已被复用（上一个进程死了、别的进程占了这个
// 号）——杀错进程是真实风险。两道防线：① 平台探活（死了的不杀）；② **创建
// 时间落在 RecordPid 记录时刻 ± 容差窗口**（processStartedWithin）——我们的
// 残留进程诞生于记录那一刻，被复用撞上的无关进程诞生于记录之后。锚点不用日
// 志 mtime：任务日志持续有输出，mtime 是最后输出时刻，与进程创建时刻可以差
// 任意远（本机实证：跑 2s 的任务差 2s——跑 30 分钟的任务就差 30 分钟，按
// mtime 判会把这个真孤儿永远判成「复用」）。存量日志（无 @ 时刻）回退旧行为
//（存在就杀，与加固前一致）；校验不匹配一律不杀只标记（误杀一个无关的关键
// 进程比漏杀一个孤儿严重得多）。探活/校验失败（打不开进程、/proc 不可用）按
// 「不匹配」处理——与 processAlive 同一保守口径。无 dir（纯内存）时 no-op。
func (m *Manager) ReapOrphans() []OrphanReport {
	if m.dir == "" {
		return nil
	}
	dir := filepath.Join(m.dir, "jobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // 目录不存在 = 从没跑过任务，无事可扫
	}
	var out []OrphanReport
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".log")
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		if logHasTerminalMark(raw) {
			continue // 优雅退出的任务，不是孤儿
		}
		pid, recordedAt, hasTS := parsePidLine(raw)
		rep := OrphanReport{ID: id, Pid: pid}
		if pid > 0 && processAlive(pid) {
			// pid 复用校验：进程创建时间必须落在「RecordPid 记录时刻 ± 容差」
			// 窗口内——我们的残留进程诞生于记录那一刻；被复用撞上的无关进程
			// 诞生于记录之后（差可以任意大）。没有时刻锚点的存量日志回退旧行
			// 为（存在就杀，与加固前一致——不引入误杀增量，只是少了这道防线）；
			// 校验不匹配一律不杀只标记（误杀无关进程比漏杀一个孤儿严重得多）。
			if hasTS && !processStartedWithin(pid, recordedAt) {
				log.Printf("孤儿清扫：任务 %s 的 pid=%d 存活但创建时间偏离记录时刻（疑似 pid 复用），不杀只标记", id, pid)
			} else if err := killPidTree(pid); err != nil {
				log.Printf("孤儿清扫：任务 %s 的残留进程 %d 树杀失败: %v", id, pid, err)
			} else {
				rep.Killed = true
				log.Printf("孤儿清扫：杀掉上一轮残留的任务 %s（pid=%d）", id, pid)
			}
		}
		// 终态标记补进日志（下次启动不再扫它）+ EndedBackend 落在报告里
		//（没有内存任务对象，Settle 无处可挂——用日志尾行的约定承载）。
		_ = appendTerminalMark(path, id)
		out = append(out, rep)
	}
	return out
}

// logHasTerminalMark 判定日志是否带终态标记（Settle 的收尾行——见
// appendTerminalMark 与 jobs 侧 producer 的写入口）。判定看尾部 512 字节。
func logHasTerminalMark(raw []byte) bool {
	tail := raw
	if len(tail) > 512 {
		tail = tail[len(tail)-512:]
	}
	return strings.Contains(string(tail), terminalMarkPrefix)
}

// terminalMarkPrefix 是 Settle 收尾行前缀（Settle 时若日志在盘上则补写）。
const terminalMarkPrefix = "[job settled]"

// appendTerminalMark 把终态标记追加进日志文件（孤儿清扫后调用：标记补齐，
// 下次启动不再重复处理）。
func appendTerminalMark(path, id string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", terminalMarkPrefix, id)
	return err
}

// parsePidLine 从日志首行读 pid=<n>（B3 的定位依据），带 `@ <unix-ns>` 时刻
// 后缀时一并解析（pid 复用校验的锚点）。读不到 pid 回 0；有 pid 无时刻回
// (n, false)——清扫对它回退旧行为（存在就杀，与加固前一致）。
func parsePidLine(raw []byte) (int, time.Time, bool) {
	line := raw
	if i := strings.IndexByte(string(raw), '\n'); i >= 0 {
		line = raw[:i]
	}
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, jobsPidLineHeader) {
		return 0, time.Time{}, false
	}
	s = strings.TrimPrefix(s, jobsPidLineHeader)
	// @ 时刻后缀（pid=<n> @ <unix-ns>）
	var ts time.Time
	hasTS := false
	if i := strings.IndexByte(s, '@'); i >= 0 {
		if n, err := strconv.ParseInt(strings.TrimSpace(s[i+1:]), 10, 64); err == nil && n > 0 {
			ts = time.Unix(0, n)
			hasTS = true
		}
		s = strings.TrimSpace(s[:i])
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, time.Time{}, false
	}
	return pid, ts, hasTS
}

// OwnerOf 返回任务的时间线归属：Spec 显式给了就用它，否则回落执行会话本身
// （顶层会话起的任务，owner 就是它自己）。唯一判定点——事件路由、唤醒投递、
// job.list 的按会话过滤都走它，免得三处各写一遍回落逻辑而漂移。
func OwnerOf(spec Spec) string {
	if spec.OwnerSessionID != "" {
		return spec.OwnerSessionID
	}
	return spec.SessionID
}

// DefaultOutputLimit 是内存尾缓冲的默认上限（64KB）。
const DefaultOutputLimit = 64 * 1024

// DefaultIdleJobTTL 是后台任务寿命护栏（B4）的默认阈值：running 任务**无输出**
// 超过它 → 自动 Kill（EndedBy=backend）。防死循环轮询无限占资源——dev server
// 会持续写日志（不触发）；一个「每 30 分钟 sleep 轮询一次、无输出」的任务 24h
// 后被收走，资源不被无限占用。可经 config 覆盖（server 装配时 SetIdleTTL）。
const DefaultIdleJobTTL = 24 * time.Hour

// idleCheckInterval 是寿命护栏的检查周期（挂 Manager 内：Start 时起一个
// goroutine，Shutdown 时退出）。每小时一次足够——阈值是小时级。
const idleCheckInterval = time.Hour

// Spec 是一次后台任务的启动参数。
type Spec struct {
	Kind      string // "bash"（将来：其他 producer）
	Label     string // 一行摘要（命令截断，UI 与通告都用它）
	SessionID string // 执行会话（谁起的——producer 视角的归属）
	// OwnerSessionID 是**时间线归属**（顶层会话）。子 Agent 是独立会话，它起的
	// 任务挂在子会话上；而用户在**父会话**里看着这条时间线，唤醒通告也只有投给
	// 父会话才有人能行动（子会话不进侧栏，投给它等于投给一个没人看的
	// 会话）。空 = 与 SessionID 同（顶层会话起的任务，owner 就是它自己）。
	OwnerSessionID string
	// Workspace 是任务起时所属的**项目 id**（producer 从执行会话的 ctx 取
	// tools.Workspace，Start 时定稿、之后不变）。面板据此按项目分组；空 =
	// 未分组会话或未注入（两者都按「没有归属项目」处理）。
	Workspace   string
	OutputLimit int // 内存保留字节数（0 = 默认 64KB）
	// DueAt 是 timer 任务的到点时刻（Kind="timer" 专用；其余 kind 零值）。
	// 到点 producer settle，Note 作为提醒正文写进任务输出。
	DueAt time.Time
	// Note 是 timer 任务的提醒正文（到点写进输出、进通告措辞；其余 kind 空）。
	Note string
}

// Snapshot 是一个任务的对外快照（工具与协议共用）。
type Snapshot struct {
	ID        string
	Kind      string
	Label     string
	Status    Status
	EndedBy   EndedBy
	Detail    string // 退出码 / 超时 / 重启
	SessionID string // 执行会话（谁起的）
	// OwnerSessionID 是时间线归属（见 Spec.OwnerSessionID）：事件路由、唤醒
	// 投递、job.list 的按会话过滤都按它，不按 SessionID。
	OwnerSessionID string
	// Workspace 是任务起时所属的项目 id（见 Spec.Workspace；Start 时定稿）。
	Workspace  string
	StartedAt  time.Time
	FinishedAt time.Time // 零值 = 未结束
	// DueAt 是 timer 任务的到点时刻（Kind="timer"；其余 kind 零值——前端
	// 据此显示倒计时，非 timer 不显示）。快照携带 = job.spec 的只读投影。
	DueAt time.Time
	// LastOutputAt 是最近一次写输出的时刻（零值 = 从未写过）。job_output 的
	// idle 谓词（wait_condition）按它判静默——从任务起点开始静默的「从未写过」
	// 用 StartedAt 兜底，不在这里编一个假时间。
	LastOutputAt time.Time
	// OutputPath 是落盘日志路径；**落盘失败降级为纯内存时为空串**
	//（契约里写的是「恒有」，但那与「落盘失败不能让任务起不来」冲突——
	// 实现按后者：降级后这里为空，消费方据此判断有无全量日志）。
	OutputPath string
}

// Job 是一个后台任务的句柄（producer 用它写输出、报结束）。
type Job interface {
	ID() string
	// Write 追加输出（同时进内存尾缓冲与落盘文件）。
	Write(p []byte) (int, error)
	// Settle 结束任务并记录原因。重复调用是 no-op。
	Settle(status Status, by EndedBy, detail string)
	Snapshot() Snapshot
	// RecordPid 把进程 pid 写进落盘日志首行（B3 孤儿清扫的定位依据）。
	// 谁起进程谁登记——Manager 不碰 os/exec，producer 在 Start 成功后调用。
	RecordPid(pid int)
}

// PidRegistrar 已取消：RecordPid 的语义并入 Job 接口（B3 起所有 producer 都
// 登记 pid——孤儿清扫是任务系统的普遍承诺，不是 bash 的私有知识）。

// CancelRegistrar 是可选的扩展面：producer 用它登记取消函数（Kill 时调用）。
//
// 不放进 Job 接口是因为「怎么取消」是 producer 的私有知识（bash 杀进程、
// 将来的 producer 可能是断连接），注册表只需要一个回调。Start 返回的句柄
// 实现了它，producer 按需断言即可。
type CancelRegistrar interface {
	SetCancel(fn func())
}

// EventKind 是订阅事件的种类。
type EventKind string

const (
	EventStarted EventKind = "started"
	EventOutput  EventKind = "output"
	EventSettled EventKind = "settled"
)

// Event 是订阅者收到的事件：种类 + 事件发生时的快照。
type Event struct {
	Kind     EventKind
	Snapshot Snapshot
}

// job 是一个任务的具体实现（Manager 与 producer 共用的句柄）。
type job struct {
	mgr *Manager
	id  string
	// seq 是启动序号：StartedAt 相同时用它做稳定排序（时间戳精度不足时
	// 两条任务可能同刻启动，按 map 遍历顺序输出会让列表跳动）。
	seq     uint64
	spec    Spec
	limit   int
	logPath string

	mu         sync.Mutex
	buf        []byte // 尾缓冲：只保留最后 limit 字节（内存环形缓冲的语义）
	total      int64  // 累计写入字节数（游标是它上面的绝对偏移）
	status     Status
	endedBy    EndedBy
	detail     string
	startedAt  time.Time
	finishedAt time.Time
	// lastOutputAt 是最近一次写输出的时刻（Write 时更新；idle 谓词的数据源）。
	lastOutputAt time.Time
	file         *os.File
	diskErr      bool // 落盘出错：只记一次日志，之后纯内存
	cancel       func()
}

func (j *job) ID() string { return j.id }

// Write 追加输出：内存尾缓冲 + 落盘，然后广播 output 事件。
//
// 落盘失败不返回错误：返回错误会让 exec 的输出拷贝 goroutine 提前停止
// （任务"起不来"或输出静默丢失），而降级为纯内存仍然可用——这正是契约
// 要求的「落盘失败不能让任务起不来」。
func (j *job) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	j.mu.Lock()
	j.buf = append(j.buf, p...)
	if len(j.buf) > j.limit {
		// 只留尾部：与环形缓冲语义一致（Read 的游标按 total 做绝对偏移）
		j.buf = append(j.buf[:0], j.buf[len(j.buf)-j.limit:]...)
	}
	j.total += int64(len(p))
	j.lastOutputAt = time.Now()
	if f := j.file; f != nil {
		if _, err := f.Write(p); err != nil {
			if !j.diskErr {
				j.diskErr = true
				log.Printf("后台任务 %s 落盘失败（降级为纯内存，输出只保留最后 %dKB）: %v", j.id, j.limit/1024, err)
			}
			// 关掉坏句柄：后续写入不再重试同一条坏路径
			_ = f.Close()
			j.file = nil
		}
	}
	j.mu.Unlock()

	j.mgr.emit(Event{Kind: EventOutput, Snapshot: j.snapshot()})
	return len(p), nil
}

// Settle 结束任务并记录原因；重复调用是 no-op（幂等）。
func (j *job) Settle(status Status, by EndedBy, detail string) {
	if !status.Terminal() {
		// 非终态（空串/running/stopping）当作正常完成——Settle 的语义就是
		// 「结束」，传错状态不该让任务永远停在 running。
		status = StatusCompleted
	}
	j.mu.Lock()
	if j.status.Terminal() {
		j.mu.Unlock()
		return
	}
	j.status = status
	j.endedBy = by
	j.detail = detail
	j.finishedAt = time.Now()
	f := j.file
	j.file = nil
	j.mu.Unlock()
	if f != nil {
		// 终态标记（B3）：孤儿清扫按它区分「优雅退出已收尾」与「硬杀残留」。
		// 写失败不致命（清扫会重复处理该日志——树杀对已死进程是无害 no-op）。
		if _, err := fmt.Fprintf(f, "\n%s %s %s %s\n", terminalMarkPrefix, j.id, status, by); err != nil {
			log.Printf("后台任务 %s 写终态标记失败: %v", j.id, err)
		}
		_ = f.Sync()
		_ = f.Close()
	}
	j.mgr.emit(Event{Kind: EventSettled, Snapshot: j.snapshot()})
}

// SetCancel 登记取消函数（Kill 时调用）。
//
// 若 Kill 已经先到（任务已处于 stopping），立即调用——否则那一次 Kill 会
// 静默失效，进程永远收不了尾（Start 返回与 producer 登记之间有窗口）。
func (j *job) SetCancel(fn func()) {
	if fn == nil {
		return
	}
	j.mu.Lock()
	j.cancel = fn
	stopping := j.status == StatusStopping
	j.mu.Unlock()
	if stopping {
		fn()
	}
}

func (j *job) Snapshot() Snapshot { return j.snapshot() }

func (j *job) snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshotLocked()
}

func (j *job) snapshotLocked() Snapshot {
	return Snapshot{
		ID: j.id, Kind: j.spec.Kind, Label: j.spec.Label,
		Status: j.status, EndedBy: j.endedBy, Detail: j.detail,
		SessionID: j.spec.SessionID, OwnerSessionID: OwnerOf(j.spec),
		Workspace: j.spec.Workspace,
		StartedAt: j.startedAt, FinishedAt: j.finishedAt, LastOutputAt: j.lastOutputAt,
		DueAt:      j.spec.DueAt,
		OutputPath: j.logPath,
	}
}

// Manager 是任务注册表（进程级单例，挂 server）。
type Manager struct {
	dir string // 会话目录（日志落 <dir>/jobs/）；空 = 纯内存

	mu     sync.Mutex
	jobs   map[string]*job
	subs   map[uint64]func(Event)
	nextID uint64
	seq    uint64
	closed bool

	// idleTTL 是寿命护栏（B4）阈值：running 任务无输出超过它 → Kill
	//（EndedBy=backend）。0 = 用 DefaultIdleJobTTL。
	idleTTL time.Duration
	// stopIdleWatch 关闭寿命护栏的检查循环（Shutdown 时关）。
	stopIdleWatch chan struct{}
}

// NewManager 创建注册表。dir 是会话目录（日志落 <dir>/jobs/<job-id>.log）；
// 空串 = 不落盘（纯内存模式，单测与未接存储的调用方用）。
func NewManager(dir string) *Manager {
	m := &Manager{dir: dir, jobs: map[string]*job{}, subs: map[uint64]func(Event){},
		stopIdleWatch: make(chan struct{})}
	// 寿命护栏（B4）：挂 Manager 内的检查循环（Shutdown 时退出）。
	go m.watchIdleJobs()
	return m
}

// SetIdleTTL 覆盖寿命护栏阈值（server 装配时从 config 传入；<=0 用默认）。
func (m *Manager) SetIdleTTL(d time.Duration) {
	m.mu.Lock()
	m.idleTTL = d
	m.mu.Unlock()
}

// watchIdleJobs 是寿命护栏的检查循环（B4）：周期扫描 running 任务，无输出
// 超 idleTTL 的 Kill（EndedBy=backend）。LastOutputAt 是现成的时间锚（从未
// 写过输出用 StartedAt——「起了就没吭声」从起点算）。
func (m *Manager) watchIdleJobs() {
	t := time.NewTicker(idleCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stopIdleWatch:
			return
		case <-t.C:
			m.reapIdleJobs()
		}
	}
}

// reapIdleJobs 执行一次寿命护栏扫描（idleCheckInterval 周期与测试直呼共用）。
func (m *Manager) reapIdleJobs() {
	m.mu.Lock()
	ttl := m.idleTTL
	if ttl <= 0 {
		ttl = DefaultIdleJobTTL
	}
	var expired []*job
	for _, j := range m.jobs {
		j.mu.Lock()
		alive := j.status == StatusRunning
		j.mu.Unlock()
		if !alive {
			continue
		}
		snap := j.snapshot()
		anchor := lastOutputAnchor(snap)
		if time.Since(anchor) >= ttl {
			expired = append(expired, j)
		}
	}
	m.mu.Unlock()
	for _, j := range expired {
		log.Printf("后台任务 %s 无输出超过 %s，寿命护栏自动停止（EndedBy=backend）", j.id, ttl)
		m.Kill(j.id, EndedBackend)
	}
}

// lastOutputAnchor 是寿命护栏的时间锚：最近输出时刻；从未写过用任务起点。
// 与 tools 侧 lastOutputAtOf 同一份语义（jobs 层不能反向依赖 tools，这里
// 复制三行而不是引包）。
func lastOutputAnchor(snap Snapshot) time.Time {
	if !snap.LastOutputAt.IsZero() {
		return snap.LastOutputAt
	}
	return snap.StartedAt
}

// Dir 返回日志根目录（空 = 纯内存）。
func (m *Manager) Dir() string { return m.dir }

// Start 起一个任务：分配 id、开落盘文件、登记、广播 started。
func (m *Manager) Start(spec Spec) (Job, error) {
	if strings.TrimSpace(spec.Kind) == "" {
		return nil, errors.New("任务 Kind 不能为空")
	}
	limit := spec.OutputLimit
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	spec.OwnerSessionID = OwnerOf(spec) // 定稿：快照里 owner 恒非空（SessionID 非空时）
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("任务管理器已关闭")
	}
	m.seq++
	j := &job{
		mgr: m, id: m.newIDLocked(), seq: m.seq, spec: spec, limit: limit,
		status: StatusRunning, startedAt: time.Now(),
	}
	m.jobs[j.id] = j
	m.mu.Unlock()

	// 落盘失败降级为纯内存：任务照起（见包注释第 1 条）
	if path, f, err := m.openLog(j.id); err != nil {
		log.Printf("后台任务 %s 日志落盘失败（降级为纯内存）: %v", j.id, err)
	} else {
		j.mu.Lock()
		j.logPath, j.file = path, f
		j.mu.Unlock()
		// 进程残留治理 B3：pid 行由 producer 经 RecordPid 写（谁起进程谁登记
		//——Manager 不碰 os/exec；这里不写占位行，落盘内容保持纯净，
		// TestLogFileContent 等内容断言不背一条空 pid=）。
	}

	// timer producer（B2）：到点回调（写 Note + settle）。cancel 经句柄的
	// CancelRegistrar 登记——Kill 时 Stop 掉 AfterFunc。
	if spec.Kind == TimerKind {
		j.SetCancel(startTimer(j))
	}

	m.emit(Event{Kind: EventStarted, Snapshot: j.snapshot()})
	return j, nil
}

// Kill 请求结束：置 stopping、调 producer 登记的 cancel；真正的 settle 由
// producer 收尾时调 Settle（它才知道退出码）。重复 Kill 是 no-op。
//
// by 区分 agent 工具与用户按钮——这是归属的唯一入口。
func (m *Manager) Kill(id string, by EndedBy) (Snapshot, error) {
	j := m.get(id)
	if j == nil {
		return Snapshot{}, fmt.Errorf("任务 %s 不存在", id)
	}
	j.mu.Lock()
	if j.status != StatusRunning {
		snap := j.snapshotLocked()
		j.mu.Unlock()
		return snap, nil
	}
	j.status = StatusStopping
	j.endedBy = by
	cancel := j.cancel
	snap := j.snapshotLocked()
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return snap, nil
}

// Read 读输出：from 是字节游标，返回自那之后的新输出（增量）。
//
// 游标是**累计写入字节数**上的绝对偏移（不是缓冲内下标），所以缓冲滚动
// 丢掉旧数据后游标依然单调：from 早于缓冲起点时从起点开始（并如实返回
// 新游标），maxBytes <= 0 表示"取当前能取到的全部"。
func (m *Manager) Read(id string, from int64, maxBytes int) (string, int64, Snapshot, error) {
	j := m.get(id)
	if j == nil {
		return "", from, Snapshot{}, fmt.Errorf("任务 %s 不存在", id)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	start := j.total - int64(len(j.buf)) // 缓冲里最老一字节的绝对偏移
	if from < start {
		from = start
	}
	if from > j.total {
		from = j.total
	}
	n := j.total - from
	if maxBytes > 0 && int64(maxBytes) < n {
		n = int64(maxBytes)
	}
	data := string(j.buf[from-start : from-start+n])
	return data, from + n, j.snapshotLocked(), nil
}

// Snapshot 单取一份任务快照（不读输出——condHit 的快照判定只需要状态与时间戳；
// output 谓词的基线比对走 curOutputOf，不走这里）。
func (m *Manager) Snapshot(id string) (Snapshot, error) {
	j := m.get(id)
	if j == nil {
		return Snapshot{}, fmt.Errorf("任务 %s 不存在", id)
	}
	return j.snapshot(), nil
}

// List 列任务（sessionID 为空 = 全部，按开始时间倒序）。
//
// 过滤按**时间线归属**（OwnerSessionID）而不是执行会话：父会话要看得见子 Agent
// 起的任务（那正是它自己时间线上的卡），子会话看父会话的任务也无害（只读列表）。
func (m *Manager) List(sessionID string) []Snapshot {
	m.mu.Lock()
	out := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if sessionID != "" && OwnerOf(j.spec) != sessionID {
			continue
		}
		out = append(out, j)
	}
	m.mu.Unlock()
	// 倒序：后启动的在前；同刻启动按 seq 倒序（稳定，不随 map 遍历顺序跳动）
	sort.Slice(out, func(a, b int) bool {
		sa, sb := out[a].snapshot(), out[b].snapshot()
		if !sa.StartedAt.Equal(sb.StartedAt) {
			return sa.StartedAt.After(sb.StartedAt)
		}
		return out[a].seq > out[b].seq
	})
	snaps := make([]Snapshot, 0, len(out))
	for _, j := range out {
		snaps = append(snaps, j.snapshot())
	}
	return snaps
}

// Subscribe 订阅事件（started / output / settled）；返回取消函数（幂等）。
func (m *Manager) Subscribe(fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	m.subs[id] = fn
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs, id)
			m.mu.Unlock()
		})
	}
}

// Shutdown 结束所有任务（EndedBy=backend），供后端退出时调用。
//
// 先调 cancel（不留孤儿进程——docs/jobs.md §7），再统一 settle：producer
// 之后真正的 Settle 因为幂等而变成 no-op。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	jobs := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	// 寿命护栏的检查循环先停（closed 之后 Kill 是 no-op，但循环不该再跑）
	select {
	case <-m.stopIdleWatch:
	default:
		close(m.stopIdleWatch)
	}
	for _, j := range jobs {
		j.mu.Lock()
		alive := !j.status.Terminal()
		cancel := j.cancel
		j.mu.Unlock()
		if !alive {
			continue
		}
		if cancel != nil {
			cancel()
		}
		j.Settle(StatusKilled, EndedBackend, ShutdownDetail)
	}
}

// emit 把事件投给全部订阅者。**不持锁回调**：订阅者很可能回调 Read/List
// （持锁回调就是自锁死）；单个订阅者 panic 也不能拖垮写输出的 goroutine。
func (m *Manager) emit(ev Event) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	subs := make([]func(Event), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.mu.Unlock()
	for _, fn := range subs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("后台任务事件订阅者 panic（已隔离）: %v", r)
				}
			}()
			fn(ev)
		}()
	}
}

func (m *Manager) get(id string) *job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// newIDLocked 生成任务 id（文件名安全：只含字母数字与短横）。
// 毫秒时间戳 + 随机后缀：跨重启也不会撞（日志文件按 id 命名）。
func (m *Manager) newIDLocked() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("job-%d-%d", time.Now().UnixMilli(), m.seq)
	}
	return fmt.Sprintf("job-%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// openLog 打开任务的落盘日志 <dir>/jobs/<id>.log。
func (m *Manager) openLog(id string) (string, *os.File, error) {
	if m.dir == "" {
		return "", nil, errors.New("未配置会话目录")
	}
	dir := filepath.Join(m.dir, "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, id+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", nil, err
	}
	return path, f, nil
}
