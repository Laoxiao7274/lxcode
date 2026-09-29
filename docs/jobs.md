# 后台任务（jobs）设计契约

> 状态：**已拍板，实现中**（2026-09-29）。参考实现 = DSH 的 `dsh-tool-jobs` +
> `dsh-tool-bash/background` + `dsh-jobs`（打包形态，路径见 AGENTS.md §2.2）。
> 本文是实现的**唯一契约**：内核/工具/协议/前端按它对齐，改契约先改本文。

## 0. 为什么做（用户诉求）

现在只有同步 `bash`（默认 60s / 上限 300s），拿本仓库自己的命令量：

| 命令 | 实测 | 今天的后果 |
|---|---|---|
| `go test ./internal/server` | 19s | 能跑但白等 |
| `go test ./...` | ~25s | 同上 |
| `scripts/build.mjs` | 44~148s | 逼近/超默认 60s，超时即被杀 |
| 布局校验（真机 Electron，85 场景） | 分钟级 | **超 300s 上限，必然被杀** |
| `node scripts/dev.mjs --electron` | 常驻 | **根本起不来** |

即「跑一次完整验证」这条日常路径今天做不到。

## 1. 与 DSH 的关系（保留什么、补什么）

**保留** DSH 的骨架：`bash(run_in_background)` 起任务 → `job_output` / `job_list` /
`job_kill` 三个工具 → settle 后通告 → `wakeup` 唤醒（owner 忙则注入下一步，
空闲则开一轮，`maxConsecutiveWakes` 上限）。

**补上 DSH 缺的一块**：DSH 的 settle 只有 `status: killed`，**不记「谁结束的」**——
用户点结束与 agent 自己 kill 在数据里长得一样，于是 agent 醒来只看到「有个任务结束了」，
自己脑补下一步（用户实测：跑了个 dev，用户关掉，agent 又跑去接着思考新的）。

修法：**settle 事件带 `EndedBy`，通告措辞跟着变**。用户点结束 = 一条**归属于用户**
的消息，agent 收到的是「用户主动结束了它，不是失败，不要重启」而不是「它结束了」。

## 2. 内核：`internal/jobs`

```go
package jobs

// Status 是任务的运行状态。
type Status string

const (
    StatusRunning   Status = "running"
    StatusStopping  Status = "stopping"  // 已请求取消，进程还没收尾
    StatusCompleted Status = "completed"
    StatusKilled    Status = "killed"
    StatusFailed    Status = "failed"
)

// EndedBy 是「谁结束的」。空 = 还在跑。
type EndedBy string

const (
    EndedSelf    EndedBy = "self"    // 自己退出（含非零退出码 → StatusFailed）
    EndedUser    EndedBy = "user"    // **用户在应用里点了「结束」**
    EndedAgent   EndedBy = "agent"   // agent 调 job_kill
    EndedBackend EndedBy = "backend" // 后端退出/重启（启动时清孤儿也用它）
)

// Spec 是一次后台任务的启动参数。
type Spec struct {
    Kind      string // "bash"（将来：其他 producer）
    Label     string // 一行摘要（命令截断，UI 与通告都用它）
    SessionID string // 归属会话（事件路由 + 唤醒投递）
    OutputLimit int  // 内存保留字节数（0 = 默认 64KB）
}

// Snapshot 是一个任务的对外快照（工具与协议共用）。
type Snapshot struct {
    ID         string
    Kind       string
    Label      string
    Status     Status
    EndedBy    EndedBy
    Detail     string // 退出码 / 信号 / 超时 / 重启
    SessionID  string
    StartedAt  time.Time
    FinishedAt time.Time // 零值 = 未结束
    OutputPath string    // 落盘日志路径；**落盘失败降级为纯内存时为空串**
}

// Job 是一个后台任务的句柄（producer 用它写输出、报结束）。
type Job interface {
    ID() string
    // Write 追加输出（同时进内存环形缓冲与落盘文件）。
    Write(p []byte) (int, error)
    // Settle 结束任务并记录原因。重复调用是 no-op。
    Settle(status Status, by EndedBy, detail string)
    Snapshot() Snapshot
}

// Manager 是任务注册表（进程级单例，挂 server）。
type Manager struct{ ... }

// Start 起一个任务：分配 id、开落盘文件、登记、广播 job.started。
// 返回的句柄实现可选的 CancelRegistrar（producer 登记自己的 cancel 函数）——
// **必须处理「Kill 先到、cancel 后登记」的窗口**：登记时若已处于 stopping 就补调一次，
// 否则那次 Kill 会静默失效（用户点了结束却什么都没发生）。
func (m *Manager) Start(spec Spec) (Job, error)

// Kill 请求结束：先置 stopping，杀进程；真正的 settle 由 producer 收尾时调。
// by 区分 agent 工具与用户按钮——**这是归属的唯一入口**。
func (m *Manager) Kill(id string, by EndedBy) (Snapshot, error)

// Read 读输出：from 是字节游标，返回自那之后的新输出（增量）。
func (m *Manager) Read(id string, from int64, maxBytes int) (data string, next int64, snap Snapshot, err error)

// List 列任务（sessionID 为空 = 全部，按开始时间倒序）。
func (m *Manager) List(sessionID string) []Snapshot

// Subscribe 订阅事件（started / output / settled）。
func (m *Manager) Subscribe(fn func(Event)) func()

// Shutdown 结束所有任务（EndedBy=backend），供后端退出时调用。
func (m *Manager) Shutdown()
```

**输出保留**：内存环形缓冲 64KB（`Read` 的 tail 源）+ **全量落盘**
`<sessions>/jobs/<job-id>.log`（job 结束后仍可读——DSH 的纯内存缓冲做不到这点，
用户关掉 dev 之后想回头看它最后报了什么就没了）。

## 3. 工具面（3 个新工具 + bash 加参数）

```jsonc
// bash 新增参数
"run_in_background": {"type": "boolean", "description": "true 时立刻返回 job id（长命令：构建、测试、dev server）"}

// 新增 job_output：读输出（含增量与 wait）
{"job_id": "...", "wait": false, "timeout_ms": 30000, "max_bytes": 32768}
//   wait=true 阻塞等这个 job 结束（默认 30s、上限 600s）；超时返回当前输出 + [status: running]
//   无新输出回 "(no new output)"（DSH 同款）

// 新增 job_list
{"session_only": true}   // true = 只列本会话（默认 false = 全部）

// 新增 job_kill
{"job_id": "..."}        // EndedBy=agent；非阻塞，回「已请求取消」
```

三者皆**低危 + Mutates=false**（`job_kill` 杀的是自己起的进程，不是外部世界；
strict 只读模式下仍可用——否则只读模式连停掉自己起的任务都做不到）。

**确认门在启动那一刻同步走完**（与前台 bash 完全一致：批准了才返回 job id）。
跑起来之后不再弹确认——后台任务没有「当前轮」可挂，这是 `auto` 档的语义边界。

## 4. 协议（加法变更，Version 仍为 2）

```go
// 方法
job.list  JobListParams{SessionID string}      -> JobListResult{Jobs []JobInfo}
job.kill  JobKillParams{ID string}            -> JobKillResult{Job JobInfo}
job.log   JobLogParams{ID string}             -> JobLogResult{Data string, Truncated bool}

// 事件
job.started  JobInfo
job.settled  JobInfo   // 带 EndedBy —— 前端据此显示「你停的 / 它挂了 / 超时」

// JobInfo（对外快照）
type JobInfo struct {
    ID, Kind, Label, Status, EndedBy, Detail, SessionID string
    StartedAt, FinishedAt string  // RFC3339；FinishedAt 空 = 未结束
    OutputTail string             // 最近 64KB（面板直接显示）
    OutputPath string
}
```

**`job.kill` 就是用户点「结束」** → 后端 `Manager.Kill(id, EndedUser)`。
与 agent 的 `job_kill` 走**同一条路径**，只是 `by` 不同——两条路径的行为永远一致
（不会出现「工具杀不唤醒、按钮杀唤醒」这种分叉）。

## 5. 唤醒投递（本设计的关键）

settle 后投递给**归属会话**（`Spec.SessionID`）：

| EndedBy / Status | 投递？ | 通告措辞 |
|---|---|---|
| `self` + completed | 是 | `后台任务 <label> 结束（退出码 0）。用 job_output 读输出。` |
| `self` + failed | 是 | `后台任务 <label> 失败（退出码 N）。用 job_output 读输出。` |
| `self` + 超时 | 是 | `后台任务 <label> 超时被杀。`（判据是 `Detail == jobs.TimeoutDetail` 常量，producer 与投递方共用同一字面量，**不靠猜字符串**） |
| `agent` | **否** | 它自己杀的，同一轮内已知（避免噪声） |
| **`user`** | **是** | `用户主动结束了后台任务 <label>。这不是失败，**不要重启它**；等用户指示。` |
| `backend` | 否 | 后端重启时 agent 也不在了 |

**投递方式**（对齐 DSH）：owner 忙 → 注入下一步；owner 空闲 → 开一轮。
**预算**：`maxConsecutiveWakes = 3`；**用户消息或 `job.kill`（用户点结束）都重置预算**
（用户交互本身就是交互，不该被自己的操作耗掉预算）。

## 6. 前端

- **时间线卡片**：本次对话起的任务（命令摘要 + 计时 + 输出 tail + 「结束」按钮）。
- **全局入口**：顶栏角标 + 面板（跨会话的常驻任务——dev server 你在别的会话里也想看到、也想停）。
- 结束后：状态 + `EndedBy` 文案（「你停的」/「它挂了」/「超时」/「后端重启中断」）+ 「查看输出」（`job.log`）。

## 7. 边界

- **后端退出即全杀**（`Manager.Shutdown` → `EndedBy=backend`），不留孤儿进程。
- **会话归档不杀**（你可能还想看输出）；job 与日志留着。
- **不自动重启**：用户停掉的东西 agent 不许自己再起——通告里明说，且这是提示词层的硬约束。
