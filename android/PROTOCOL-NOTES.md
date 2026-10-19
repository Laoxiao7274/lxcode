# lxcode 安卓端协议笔记（PROTOCOL-NOTES）

> 权威定义：`internal/protocol/protocol.go`（帧/方法/事件单处定义）；客户端参考实现
> `internal/wsclient/wsclient.go`（Go）与 `frontend/src/agent/ws/index.ts`（TS）。
> 本文是安卓端 `net/` 包的实现依据，2026-10-09 按仓库当前代码逐字段核对。
> 协议版本：**Version = "2"**；WS 端点：**`ws://<addr>/rpc`**（`protocol.Path`）。

## 1. 传输与帧

- WebSocket JSON-RPC 2.0，单连接单帧（文本消息，一帧一个 JSON 对象）。
- **鉴权门**（`internal/server/remote.go`）：仅当后端配置开启 remote access
  （`remote.json` `enabled`）时要求凭证——升级请求须带 `?token=` 查询参数或
  `Authorization: Bearer <token>` 头；未开启（本地默认）时两者都不需要。
  安卓侧：地址可附带 token，连接 URL 拼 `?token=`（空则不拼）。
- **请求帧**（客户端 → 服务端，带自增整数 id）：

```json
{"jsonrpc":"2.0","id":1,"method":"connection.hello","params":{"client":"android","version":"2"}}
```

- **应答帧**（同 id 配对）：

```json
{"jsonrpc":"2.0","id":1,"result":{"server":"lxcode","version":"2","busy":false}}
```

- **事件帧**（服务端 → 客户端广播，**无 id** = 通知）：

```json
{"jsonrpc":"2.0","method":"chat.delta","params":{"session_id":"…","kind":"text","text":"…"}}
```

- **错误应答**：`{"jsonrpc":"2.0","id":1,"error":{"code":1003,"message":"…"}}`。
- 应用错误码：1001 未绑 default 模型 / 1002 模型停用 / 1003 会话忙（ErrBusy）/
  1004 无待确认调用 / 1005 Agent 不在名单 / 1006 Agent 停用 / 1007 协议版本不匹配。

## 2. hello 握手（`connection.hello`）

参数（`protocol.HelloParams`）：

```json
{"client":"android","version":"2"}
```

- `client`：客户端标识（服务端只记录日志，取值自由：cli/desktop/web/probe，安卓用 `android`）。
- `version`：必须等于 `"2"`，**不等返回 code 1007 错误**（不是应答里带版本号）。

结果（`protocol.HelloResult`）：

```json
{"server":"lxcode","version":"2","busy":false}
```

注意与前端 WSAgent 的差别：服务端对版本不匹配**直接回错误帧**（`dispatch_hello.go`），
客户端无须再比对 result.version（比对是防御性的，兼容未来服务端宽松化）。

## 3. 会话管理

| 方法 | 参数 | 结果 |
|---|---|---|
| `session.list` | 无 | `SessionMeta[]`（见下） |
| `session.new` | `{"workspace":"<项目id>"}`（可选，空 = 未分组） | `{"session_id":"…"}` |
| `session.resume` | `{"id":"…"}` | `{}` |
| `session.rename` | `{"id":"…","title":"…"}` | `{}` |
| `session.archive` | `{"id":"…","archived":true}` | `{}`（`SessionArchiveResult`：`archived` / `released_worktree` / `release_error?`） |

**会话操作缺口（2026-10-09 第二批核对，安卓端不造）**：
- **删除会话**：协议**没有** `session.delete`（只有 archive；归档即从侧栏消失）——安卓「删除」入口缺位；
- **置顶会话**：协议**没有**置顶/pin 方法（`SessionMeta` 也无 pinned 字段）——缺位；
- ~~`session.worktree.release` 存在（`{"id"}` → `{released}`），属项目工作区管理，本批不接。~~（该方法已随直连模式移除——2026-12 用户拍板，调用会得到标准的未知方法错误。）

`SessionMeta`（`session.list` 条目）：

```json
{"id":"a1b2…","title":"第一条用户消息截断","updated_at":"2026-10-09T10:00:00Z",
 "messages":6,"archived":false,"workspace":""}
```

- `updated_at` 是 ISO 时间戳——安卓侧展示「N 分钟前」需自行换算。
- 侧栏只该显示 `archived=false` 的条目（子会话 `parent_id` 不会出现在 list 里，
  服务端已过滤——`store.List()` 只认 `parent_id = ''`）。
- 协议**有**列表方法（`session.list`），安卓端直接用它，无需「只显示当前会话」的回退。

## 4. 历史（`chat.history`）

参数：`{"session_id":"…","tail":50}`。`tail` 可选（2026-10 慢链路优化）：只拉
当前历史尾部 N 条，0/缺省 = 全量（老客户端不受影响）。尾部起点强制回退到
**工具配对平衡切点**（不会把 assistant 的 tool_calls 切在它的结果之前），
检查点下标随窗口平移；busy/pending/todos/context/stats 与全量一致（context
的投影基线按全量历史算——窗口只是传输裁剪，不改占用计账）。
安卓端固定带 `tail:50`（TAIL_WINDOW，RealBackend）进会话先见最新。

**分页（2026-12）**：`before_seq` 可选（>0 生效）= 返回该消息**之前**（不含它）
最多 `tail` 条——向上翻页的 anchor 语义。`tail` 在此语境下是窗口大小；配平与
检查点平移在内核 `Session.HistoryBefore`（与 TailHistory 同一套纪律）。请求：

```json
{"session_id":"…","tail":50,"before_seq":42}
```

- `before_seq` 不在历史里 / ≤0 / 缺 `tail` → `CodeInvalidParams`（错误的 anchor
  会切出重复或空洞的窗口，不静默回落）；
- 应答多两个**分页专属键**（非分页应答整键缺席）：`has_more`（anchor 之前还有
  没有更早的）与 `first_seq`（本窗口首条消息的 seq——下一次请求的 anchor，
  配平回退后它可能比 anchor−N 更旧，客户端以它为准推进薄记）。

结果（`protocol.ChatHistoryResult`）：

```json
{
  "messages": [ /* llm.Message[]，见 §4.1 */ ],
  "busy": false,
  "pending": { /* ConfirmRequest，见 §6；无挂起确认时整键缺席 */ },
  "session_id": "…",
  "todos": [ {"content":"…","status":"done"} ],
  "context": { "used":12345,"window":131072,"system":1000,"tools":2000,
               "tool_results":3000,"messages":5000,"reasoning":0,"estimated":false },
  "stats": { "turns":2,"steps":3,"llm_ms":4200,"tool_ms":120,"ttft_ms":812,
             "ttft_steps":2,"decode_ms":2100,"decode_tokens":180,
             "input_tokens":900,"cache_read_tokens":0,"cache_write_tokens":0,
             "output_tokens":180 },
  "checkpoints": [2],
  "model": "deepseek-v3.2",
  "approval": "confirm"
}
```

- `context` / `stats` / `pending` / `todos` / `checkpoints` / `model` / `approval`
  都是**缺省键**（未知/无数据时整键缺席）——安卓侧解析一律用可空类型，**不编数字**：
  `context` 缺席 → 上下文环显示「—」；`stats` 缺席 → 统计胶囊整个不渲染。
- `checkpoints` 是 `messages` 的下标：这些消息渲染成「已压缩历史」块而不是助手气泡。
- **重连/切会话顺序纪律**（前端实测结论）：`session.new` / `session.resume` 必须先于
  `chat.history`；恢复会话「历史先到、焦点后切」避免闪空。

### 4.1 `llm.Message` 的 wire 形态（历史与事件共用）

```json
{"role":"assistant","content":"正文","reasoning_content":"思考链",
 "tool_calls":[{"id":"call_1","type":"function",
                "function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}],
 "tool_call_id":"call_1","seq":5,
 "first_token_ms":812,"duration_ms":2100,"model":"deepseek-v3.2","usage_tokens":180}
```

- `role`：`system | user | assistant | tool`；历史回放里 system 不出现（每轮现组装）。
- `seq`：会话内序号（撤回锚点；0 = 未落库）。
- `tool` 消息的 `content` 是结果文本，`tool_call_id` 配对回它响应的调用；
  `duration_ms` 在 tool 消息上是**工具执行耗时**。
- 用量分桶（input/cache_read/cache_write）**不在消息 wire 上**（`json:"-"`），
  整段会话统计由后端折叠好放 `stats`。
- `notice` 同为 `json:"-"`（统计簿记，不上 wire）。

## 5. 对话（`chat.send`）

参数（`protocol.ChatSendParams`）：

```json
{"session_id":"a1b2…","text":"用一句话介绍你自己",
 "effort":"medium","approval":"confirm","agent":"main"}
```

- `effort`：可选（minimal/low/medium/high），仅声明 reasoning 能力的模型生效。
- `approval`：可选（auto/confirm/strict，空 = confirm）。会话级实时档另有
  `chat.approval` 方法（`{"session_id":"…","approval":"strict"}` →
  `{"approval":"confirm"}` 规范化回填）。
- `agent`：可选（执行 Agent 的名单 id，空 = 主 Agent）。
- 应答：`{"session_id":"…"}`（回声）。**内容全部经事件到达**（见 §5.1）；
  请求失败（忙/没模型）走应答错误帧，生成中的失败走 `chat.error` 事件。
- 取消：`chat.cancel`，参数 `{"session_id":"…"}`；中断的内容经 `chat.error`
  （`aborted=true` + `partial`）回报。

### 5.1 一轮的事件序列（主会话）

```
chat.userMessage → (chat.delta{kind:"reasoning"})* → (chat.delta{kind:"text"})*
→ (chat.toolCall → chat.confirmRequest? → chat.toolResult)*
→ chat.done（或 chat.error）
```

- 每**个中间步**（每次工具轮后继续生成）都会再来一遍 delta 序列并各自发一条
  `chat.done`（`message` 不同）——客户端每条 done 都把 `message.content` 与
  当前流式块对齐，`context`/`stats` 取**最后收到**的那份。

## 6. 确认门（`chat.confirmRequest` 事件 → `tool.confirm` 方法）

事件载荷（`protocol.ConfirmRequest`）：

```json
{"session_id":"a1b2…","id":"call_1","name":"bash",
 "arguments":"{\"command\":\"ls -la\",\"timeout_sec\":60}",
 "prompt":"执行此命令？bash 工具申请执行以下命令（高危：会改动工作区）。"}
```

- `prompt` 是后端组装好的人话确认文案（标题）；`arguments` 是 **JSON 字符串**
  （需二次解析展示命令块）；`id` 是要回传的调用 id。
- 裁决：`tool.confirm`，参数 `{"session_id":"…","id":"call_1","allow":true|false}`，
  应答 `{}`。裁决成功后结果以正常 `chat.toolResult` 事件到达；
  拒绝时后端合成「已取消，未执行」的 tool 结果回填模型，会话继续不崩。
- 会话历史里若挂着未决确认，`chat.history` 的 `pending` 键会带回同一结构。

## 7. 事件类型清单（安卓本批消费的）

| 事件 | 载荷字段（protocol.*Params） |
|---|---|
| `connection.ready` | 无（连接建立时单发） |
| `chat.userMessage` | `session_id`、`message`(llm.Message) |
| `chat.delta` | `session_id`、`kind`(text\|reasoning)、`text`、`dispatch_id?` |
| `chat.toolCall` | `session_id`、`id`、`name`、`arguments`(JSON 串)、`dispatch_id?` |
| `chat.toolResult` | `session_id`、`id`、`name`、`content`、`is_error`、`dispatch_id?` |
| `chat.confirmRequest` | `session_id`、`id`、`name`、`arguments`、`prompt`、`dispatch_id?` |
| `chat.done` | `session_id`、`message`、`usage_tokens`、`finish_reason`、`dispatch_id?`、`context?`、`stats?`、`first_token_ms?`、`duration_ms?`、`model?` |
| `chat.error` | `session_id`、`message`、`aborted`、`partial?`(llm.Message) |
| `chat.busy` | `session_id`、`busy` |
| `chat.approvalChanged` | `session_id`、`approval` |
| `todo.updated` | `session_id`、`items`(tools.TodoItem[]：`content`/`status`) |
| `session.changed` | `id`、`reason`(created\|started\|renamed\|archived\|compacted\|rewound) |
| `chat.dispatchStart` | `owner_session_id`、`dispatch_id`、`session_id?`、`agent_id`、`agent_name`、`agent_color`、`task` |
| `chat.dispatchEnd` | `owner_session_id`、`dispatch_id`、`session_id?`、`result`、`is_error`、`usage_tokens?` |
| `chat.compacted` | `session_id`、`before`、`after`、`shadowed`、`summary`、`manual?`、`dispatch_id?` |
| `chat.rewound` | `session_id`、`seq`、`removed`、`context?`、`stats?` |
| `files.changed` | `session_id`、`files[]`(`path`/`added`/`deleted`/`diff`) |
| `model.changed` | ModelListResult（本批不消费） |

本批安卓端**不消费**（记录备查）：`model.changed`、`files.changed`（本批未渲染产物卡）、
`job.*`、`catalog.*`、`search.changed`、`project.changed`、`agent.changed`。
`dispatch_id` 非空的事件属于**子会话**，本批一律忽略（主时间线只收空 dispatch_id 的事件）。

## 8. 安卓端实现决策

- **WS 客户端 = OkHttp 4.12.0**（`com.squareup.okhttp3:okhttp`）：业界标准、WS 支持成熟、
  传递面小（仅 okio + kotlin-stdlib，两者本机缓存已有，离线可构建）；
  Android 自带 `java.net` 无同步 WS API，`java.net.http.HttpClient` 要 API 34（minSdk 26）。
  **JSON = org.json**（Android 平台自带，零依赖）：帧结构浅（两层），手写解析足够；
  kotlinx-serialization 要引编译插件 + 运行时，对原型过重。
- **请求 id 配对 / 断连 fast-fail** 对齐 Go wsclient：pending 表按 id 配对；连接关闭时
  所有 pending 立刻失败（「后端连接已断开」），事件流收尾。
- **重连**：本批手动（断线 UI + 重试按钮），指数退避自动重连后置（与任务要求一致）。
- **协议缺口记录**（只记录不改服务端）：
  1. `chat.delta` 无轮次/块序号字段——多 delta 流交错时客户端只能按「最后一条 assistant
     块」归并，纯启发式（桌面端同款做法，非阻塞）。
  2. `session.list` 条目没有 `running` 字段（busy 是事件态 `chat.busy` 广播，不进列表）——
     安卓侧运行中状态点只能在连接期间用事件维护，重启后未知。
  3. hello 成功应答的 `busy` 恒为 false（服务端未填真实值），客户端不应依赖它。

### 8.1 第二批（斜杠面板 / 会话操作 / 返回栈 / 派发卡实时流）补记（2026-10-09）

- **斜杠命令面板**：命令集照抄桌面端 `App.tsx` slashCommands（new/compact/agents/catalog/
  settings，名称与描述逐字一致）。`/new` → `session.new`、`/compact` → `chat.compact`
  （参数 `{"session_id"}` → `CompactResult{compacted,reason?}`，都是协议内方法）；
  **agents/catalog/settings 是桌面端页面导航命令**——不是协议缺口，是安卓原型没有
  对应页面，选中时如实提示「安卓原型未提供该页」，不假装执行。
- **派发卡实时流**：子会话 `chat.toolCall/toolResult`（带 dispatch_id）归并进派发卡的
  `childRuns` 实时行（工具名/状态/耗时）。**耗时是本地计时**：协议 `toolResult` 事件
  不带 `duration_ms`（只在历史 tool 消息上有），实时流在调用到达时记
  `SystemClock.elapsedRealtime()`、结果到达时作差——与历史回放的口径（消息里的
  duration_ms）来源不同，如实标注。
- **会话操作**：`session.rename` / `session.archive` 已接（见 §3 的缺口清单——删除/
  置顶协议没有，记缺口跳过）。mock 模式下这些操作如实提示「需连接真实后端」，
  不对静态演示数据假装生效。

### 8.2 第三批（mock 退役 / 滚到底 / 历史整块呈现 / 多端共享进度）补记（2026-10-14）

- **mock 退役**：`mock/MockData.kt` 删除（假数据本体），协议对齐的类型定义迁到
  `model/Wire.kt`（BackendConn/SessionMeta/ThreadBlock/ContextUsage/SessionStats/
  ModelMeta…）。未连接任何后端时会话页显示**连接引导态**（logo + 扫码/手动添加入口），
  不再显示编造的会话列表。composer 的模型菜单改读 `model.list`（注册表投影：
  按 base_url 分组、efforts 派生自 capabilities.reasoning），模型选择走 `role.set`
  （default 角色绑定，多端同步）。

  > **2026-12 修订（模型选择作用域）**：模型选择改走 **`chat.model`**（会话级：
  > 后端落库 `sessions.model` + 广播 `chat.modelChanged`，只改这一条会话）。原先
  > 走 `role.set` 改全局 default 角色——一个会话切模型所有会话跟着变。`role.set`
  > 仍保留全局语义（= 新会话的默认模型，设置页用）。菜单选中态 = `chat.history`
  > 的 `model` 键 / `chat.modelChanged`（`RealBackend.activeModelId`：会话选择 →
  > 回落 default 角色）。
- **ThreadScreen 滚到底**：三个触发时机——①历史装载完成（`RealBackend.historyVersion`
  在 applyHistory 末尾递增，ThreadScreen 据此 scrollToItem(last)）；②消息流增长
  （blocks.size 变化即滚）；③键盘弹出（WindowInsets.ime 高度变化再滚一次）。
- **打字机只用于实时流**：`ThreadBlock.Assistant.streaming` 位——delta 建的流式块
  为 true（rememberStreamReveal enabled=true），历史回放/`chat.done` 定格/`chat.error`
  partial 均为 false（直接整块呈现全文）。对齐桌面端 AnswerBody 的 committed/tail
  语义：历史 = snap 全量落下。
- **多端共享进度**：后端 WS 是全量广播（多客户端各收一份），安卓侧此前只消费
  `session_id == currentSessionId` 的事件。本批起：
  - `chat.busy` 先记进**会话级忙闲投影** `sessionBusy`（所有会话都记），会话列表的
    「运行中」状态点读它——桌面端在跑的会话，手机列表上同样亮着；重连时投影清账
    （hello 不带逐会话 busy，未知显示空闲等广播修正，不编状态）。
  - 当前打开的会话消费全部事件（delta/toolCall/toolResult/confirm/done/error/
    todo/compacted），与桌面端 reduceSessionStates 的实时追加语义一致；其他会话
    的这些事件仍丢弃（没有历史基线，拼出来的是半截时间线——对齐「子会话 state
    不存在时不建」的桌面纪律）。
  - `todo.updated` 带 `dispatch_id`（子 Agent 清单）归并进归属派发卡
    （`childTodosDone/childTodosTotal`，卡上「清单 N/M」一行）——手机上看派发
    进度的轻量形态，不必做完整子会话视图。`chat.dispatchStart` 的 `todos`
    初稿（主 Agent 预拆）同样进卡。
  - `model.changed` 广播 → 重拉 model.list（多端模型/绑定同步）。
- **心跳与自动重连（LxWsClient）**：每 15s 发一次 `connection.ping`（3s 超时、
  连续 2 次失败主动断开），断线后指数退避自动重连（1s→2s→4s…封顶 30s；用户显式
  disconnect 不重连）。多端常驻的前提：手机息屏/切后台导致连接死掉时能自己恢复，
  事件流随之续上。后端每请求一个 goroutine（TestSlowRequestDoesNotHeadOfLineBlockPing
  钉住），心跳不会被慢请求队头阻塞。
