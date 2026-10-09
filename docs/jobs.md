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
    SessionID string // 执行会话（谁起的——producer 视角的归属）
    // OwnerSessionID 是**时间线归属**（顶层会话）：子 Agent 是独立会话，它起的
    // 任务挂在子会话上；但用户在**父会话**里看着这条时间线，唤醒通告也只有投给
    // 父会话才有人能行动。空 = 与 SessionID 同（顶层会话起的任务）。
    OwnerSessionID string
    OutputLimit int  // 内存保留字节数（0 = 默认 64KB）
}

// OwnerOf 是时间线归属的**唯一判定点**（显式给了就用，否则回落 SessionID）——
// 事件路由、唤醒投递、job.list 过滤三处都走它，免得各写一遍回落逻辑而漂移。
func OwnerOf(spec Spec) string

// Snapshot 是一个任务的对外快照（工具与协议共用）。
type Snapshot struct {
    ID         string
    Kind       string
    Label      string
    Status     Status
    EndedBy    EndedBy
    Detail     string // 退出码 / 信号 / 超时 / 重启
    SessionID  string // 执行会话（谁起的）
    OwnerSessionID string // 时间线归属（见 Spec；Start 时定稿，恒非空）
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
    // OwnerSessionID 是**时间线归属**（顶层会话）：子 Agent 起的任务挂在父会话上，
    // 前端据此把它放进父会话的时间线、后端据此把唤醒通告投给父会话。空 = 回落 SessionID。
    OwnerSessionID string
    StartedAt, FinishedAt string  // RFC3339；FinishedAt 空 = 未结束
    OutputTail string             // 最近 64KB（面板直接显示）
    OutputPath string
}
```

**`job.kill` 就是用户点「结束」** → 后端 `Manager.Kill(id, EndedUser)`。
与 agent 的 `job_kill` 走**同一条路径**，只是 `by` 不同——两条路径的行为永远一致
（不会出现「工具杀不唤醒、按钮杀唤醒」这种分叉）。

## 5. 唤醒投递（本设计的关键）

settle 后投递给**时间线归属**（`Spec.OwnerSessionID`，见 §2 的 `OwnerOf`）——
**不是执行会话**。子 Agent 是独立会话、不进侧栏：投给执行会话等于投给一个没人看的
会话，用户在主对话里什么都看不到，主 Agent 也永远不知道用户已经把它停了，于是自己
脑补下一步（用户实测的原话：「它跑了个 dev，我关掉了，他又跑去接着思考新的」）。
唤醒预算也按时间线归属算（用户点 `job.kill` 重置的也是它）。

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

- **时间线卡片**：本次对话时间线上的任务（命令摘要 + 计时 + 输出 tail + 「结束」按钮）。
  **上卡位置按 `owner_session_id`**（空才回落 `session_id`）——子 Agent 起的任务要挂在
  **父会话**的时间线上，与后端的通告投递同一个归属（两处不一致就会出现「通告进了主会话、
  卡却在别处」的错位）。
- **全局入口**：顶栏角标 + 面板（跨会话的常驻任务——dev server 你在别的会话里也想看到、也想停）。
- 结束后：状态 + `EndedBy` 文案（「你停的」/「它挂了」/「超时」/「后端重启中断」）+ 「查看输出」（`job.log`）。

## 7. 边界

- **后端退出即全杀**（`Manager.Shutdown` → `EndedBy=backend`），不留孤儿进程。
- **会话归档不杀**（你可能还想看输出）；job 与日志留着。
- **不自动重启**：用户停掉的东西 agent 不许自己再起——通告里明说，且这是提示词层的硬约束。

## 8. 实测坑：取消必须杀**整棵进程树**（2026-09-29）

真链路验收时抓到的缺陷。**它让本功能对最典型的场景完全失效**，动进程启停代码前先读这节。

**现象**：用户点「结束」，任务状态变成 `stopping` 然后**永远停在那里**；结束通告
永远发不出去（agent 什么都不知道，用户以为停了其实没停）。实测：kill 一个 `sleep 600`
之后 30 秒仍未 settle，而 `sleep.exe` 还在跑。

**根因**（两条叠在一起，缺一条都还是坏的）：

1. **默认取消只杀直接子进程**。`exec.CommandContext` 的默认 `Cancel` 是
   `cmd.Process.Kill()`，在 Windows 上只 `TerminateProcess` **直接子进程**。而后台
   任务的形态恒为 `sh -c "<命令>"`——真正干活的是**孙进程**（`npm run dev` → node）。
   于是 sh 死了、node 活着：dev server 继续占着端口。
2. **`cmd.Wait` 要等 stdout 管道写端全部关闭**。`cmd.Stdout` 是任务句柄
   （`io.Writer`），`os/exec` 会建管道 + 拷贝 goroutine；只要还有一个进程攥着那个
   写端，`Wait` 就一直阻塞。孤儿正是那个攥着写端的进程 → `Wait` 不返回 →
   **任务永远不 settle → 结束通告永远发不出去**。对一个永不退出的 dev server，
   `stopping` 就是终态，用户看到的是「点了结束，什么都没发生」。

**修法**（`internal/tools/proctree*.go`；bash 前台、bash 后台、自定义工具三条路都接）：

- `cmd.Cancel = killProcessTree`：Windows 用 `taskkill /T /F /PID`（必须在父进程
  还活着时调用——父死了就找不到子进程），Unix 让命令自成进程组后 `kill(-pid, SIGKILL)`；
  失败退回 `Process.Kill`，至少把直接子进程杀掉。
- `cmd.WaitDelay = procKillGrace`（5s）兜底：即使真有进程逃逸出树，`Wait` 也一定在
  期限内返回，**结束通告一定送得出去**。它**不是任务寿命上限**——计时器只在「ctx 结束」
  或「进程已退出」时启动（`os/exec.Cmd.WaitDelay` 文档），健康的 dev server 不会被它杀掉。
- `normalizeWaitErr` 把 `exec.ErrWaitDelay` 还原成进程的真实退出状态：它说的是
  「I/O 管道没关掉」，不是「进程失败了」，不还原会把正常结束的任务记成 `StatusFailed`。

**判据**（`internal/tools/proctree_test.go`）：起 `(sleep 2; echo SURVIVED > marker) & wait`，
杀掉后断言 marker **写不出来**（孙进程真死了）+ 任务在期限内 settle 且归属仍是 `user`；
另有一条**正对照**（不杀则 marker 必须写得出来），否则「marker 不存在」可能只是因为命令
压根没跑起来，断言就变成了空断言。


## 9. merge producer（第二个 producer，Kind="merge"，2026-10）

合并进程是 jobs 的**第二个 producer**（`internal/server/mergejob.go`）：把本会话分支
的改动交给内置「合并 Agent」（merger，独立子会话）在集成分支的专用工作树里汇总。
任务体**不是**子进程，而是一个真 `agent.Session`——所以压缩/溢出兜底/确认门/工具循环
全部免费继承，job 日志只是它的事件出口之一。

**Kind 与启动路径（两条，同一后端函数 `startMergeJob`）**：

| 发起方 | 路径 |
|---|---|
| 模型 | `merge_request` 工具（低危，`internal/tools/merge.go`——经 `Registry.SetMergeStarter` 注入） |
| 用户 | 后台任务面板的「合并请求」入口 → `chat.mergeRequest{session_id?, target_branch?}` → 返回 `{job_id}`（`internal/server/dispatch_chat.go`） |

前置校验都在 `startMergeJob`（返回前做完，错误文案照搬给前端）：会话是项目会话
（有 workspace）、已有可合并的分支（worktree 元数据 Path/Branch 非空）、同一会话
同时只允许一个在跑的合并进程。`target_branch` 空 = 默认 `lxcode/integration`。

**任务体**：`EnsureIntegrationWorktree` 准备集成分支工作树（`<sessions>/worktrees/<project-id>/integration`）
→ `Session.RunAgentTask` 跑 merger 子会话（`Store.CreateChild` 建的**真子会话**，
parent_id 指向主会话——不进侧栏、父归档级联归档）。子会话在集成工作树里工作
（workDir 覆盖），任务说明书（`mergeTaskText`）自带合并纪律：先看两边改了什么、
冲突逐个解决并说明取舍、绝不用 `--force`/`-X theirs` 掩盖冲突、合并后跑构建测试、
失败恢复原状并如实报告。

**事件与标签页（2026-10 用户拍板「合并进程要像子 Agent 一样有标签页」）**：
`RunAgentTask` 把子会话事件走**双路**——一份进 job 日志（`job_output` 读），一份经
`childEmitter` 带 `dispatch_id` 上抛父会话：起手 `chat.dispatchStart`（owner=父会话、
session_id=子会话、**dispatch_id=子会话 id**——与确认/提问代理同一个归属键，前端
「按 dispatch_id 反查卡」的既有链路零改动）、结束 `chat.dispatchEnd`（结论/错误/取消
定格）。前端照子 Agent 的同一套归约：主时间线一张摘要卡、点开进子会话标签页（实时流
双投）、`ask_user` 的提问卡落进标签页内（用户在标签页里回答，答案经父通道转回）。

**结束与唤醒投递**：`EndedBy=EndedSelf`（与 bash 的 `settleBackground` 同口径）——
按 §5 的表投递（`agent`/`backend` 不投，其余投给父会话），合并结束的通告自动回到
父会话，这里不写任何通知逻辑。**取消语义**：用户/agent 停任务 → `CancelRegistrar`
取消 ctx → 子会话经 `SendWait` 的 ctx 传播中断并**等它真正收尾**（§8 的纪律对子会话
同样成立——不留半个合并）。

**失败判定**（绝不谎报成功）：merger 结论之后还查一遍 `project.MergeConflicts`——
冲突文件清单非空或仍有未收尾的合并（MERGE_HEAD 存在）→ `StatusFailed`，detail 写清
冲突文件清单（截到 600 字符）；其余按 `Settle(Failed, detail)` 如实报错。空仓库等
无改动场景合并成功 = `StatusCompleted`。

**验收用例**：`internal/server/mergejob_test.go`（起任务/未分组报错/同会话互斥/
通告投递/merger 定义/dispatch 事件广播）与 `internal/agent/ask_user_test.go`
（merger 子会话的提问经确认代理上抛父会话）。
