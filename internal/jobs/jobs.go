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

// DefaultOutputLimit 是内存尾缓冲的默认上限（64KB）。
const DefaultOutputLimit = 64 * 1024

// Spec 是一次后台任务的启动参数。
type Spec struct {
	Kind        string // "bash"（将来：其他 producer）
	Label       string // 一行摘要（命令截断，UI 与通告都用它）
	SessionID   string // 归属会话（事件路由 + 唤醒投递）
	OutputLimit int    // 内存保留字节数（0 = 默认 64KB）
}

// Snapshot 是一个任务的对外快照（工具与协议共用）。
type Snapshot struct {
	ID         string
	Kind       string
	Label      string
	Status     Status
	EndedBy    EndedBy
	Detail     string // 退出码 / 超时 / 重启
	SessionID  string
	StartedAt  time.Time
	FinishedAt time.Time // 零值 = 未结束
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
}

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
	file       *os.File
	diskErr    bool // 落盘出错：只记一次日志，之后纯内存
	cancel     func()
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
		SessionID: j.spec.SessionID, StartedAt: j.startedAt,
		FinishedAt: j.finishedAt, OutputPath: j.logPath,
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
}

// NewManager 创建注册表。dir 是会话目录（日志落 <dir>/jobs/<job-id>.log）；
// 空串 = 不落盘（纯内存模式，单测与未接存储的调用方用）。
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, jobs: map[string]*job{}, subs: map[uint64]func(Event){}}
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

// List 列任务（sessionID 为空 = 全部，按开始时间倒序）。
func (m *Manager) List(sessionID string) []Snapshot {
	m.mu.Lock()
	out := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if sessionID != "" && j.spec.SessionID != sessionID {
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
