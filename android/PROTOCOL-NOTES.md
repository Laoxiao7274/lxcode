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
| `session.archive` | `{"id":"…","archived":true}` | `{}` |

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

参数：`{"session_id":"…"}`。
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
