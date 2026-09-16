# 01 · M1 内核设计

> 2026-09-10。M1 交付：纯 Go agent 内核 + CLI REPL（验收面）。
> 定位/架构基线见 AGENTS.md；本文记内核的分层与关键设计决策。
> **2026-09-10 晚间架构调整**：前后台分离（用户拍板，对齐 local-myt-agent
> 参考形态）——后端独立进程 + WS JSON-RPC，见 §1.1。

## 1. 分层

```
cmd/lxcode
  ├─ --serve     后端进程：config → store → server（WS JSON-RPC :7789/rpc）
  └─ (默认)      CLI 客户端：wsclient.Dial → cli REPL
internal/cli         CLI 客户端 REPL（零依赖终端渲染、y/n 确认路由）
internal/wsclient    WS JSON-RPC 客户端库（id 配对/事件流/断连 fast-fail）
internal/server      WS 服务端：方法分发 + 事件广播（包裹 agent.Session）
internal/protocol   协议契约（帧/方法/事件/载荷，两端共享 + 契约测试）
internal/agent       内核核心：会话运行时（工具循环/确认门/todo 状态）
  ├ events.go        typed Event（sealed interface）+ Snapshot
  ├ prompt.go        系统提示词动态生成（工具清单来自注册表）
  └ session.go       Send/Cancel/Confirm + runTurn/streamRound/runTools
internal/config      模型注册表（models.json 原子读写、角色绑定）
internal/store       SQLite 会话持久化（modernc.org/sqlite 纯 Go、WAL；2026-12 由 JSONL 切换，为 compaction/语义记忆铺路）
internal/tools       工具注册表 + 7 个内置工具（风险分级 + JSON 修复）
internal/llm         双格式 LLM 客户端（OpenAI/Anthropic、流式、ChatAuto）
```

依赖方向单向：cli → wsclient → protocol；server → agent → {config, store,
tools} → llm → stdlib。agent 不 import protocol/cli（保持可嵌入性——桌面壳
若选 in-process 形态也可直接用）；protocol 引 llm/tools 的类型做载荷（无环）。

### 1.1 前后台分离（2026-09-10 定案）

最初 M1 按"内核是包不是进程"交付（CLI 进程内直调）。用户明确定为前后台
分离、后端独立运行（对齐参考项目 local-myt-agent 的双进程架构）、主做
Windows。调整内容：

- 协议层与客户端库从 local-myt-agent 移植（帧契约测试一并移植）；
- server 包装 agent：typed 事件 → 协议事件广播（唯一映射点 emitEvent）；
- agent 哨兵错误（ErrBusy/ErrNoDefaultModel/ErrModelDisabled/ErrPendingConfirm）
  供服务端 errors.Is 映射协议错误码——不做字符串匹配；
- CLI 从进程内直调改为 wsclient 客户端；busy/pending 状态由事件流同步
  （后端是唯一事实来源，多客户端一致）。

协议相对参考项目的差异：+todo.updated 事件、ChatHistoryResult 带 todos、
去 chat.reset（session.new 语义覆盖）、端口 7789。

## 2. 关键设计决策

### 2.1 内核是包，宿主是进程（2026-09-10 晚调整为前后台分离）

local-myt-agent 为远程设备 + TUI 客户端设计了 WS JSON-RPC 协议层。
lxcode 最初按"桌面壳 in-process 直调"交付（typed Event，无协议层），
当晚按用户决策调整为前后台分离——协议层从参考项目移植，server 包装
agent.Session 做方法分发与事件广播。内核形态不变：agent 仍是纯 Go 包
（typed Event + Confirm 方法），server 是它的第一个宿主；桌面壳将来既可
走 WS（与后端同机或远程），也可 in-process 嵌入 agent 包——两条路都通。

事件模型不变（typed Event → 协议事件的映射是 server.emitEvent 单点），
确认门从进程内方法调用变为 RPC 往返（chat.confirmRequest 事件 +
tool.confirm 方法）。

### 2.2 事件模型

| 事件 | 时机 |
|---|---|
| UserMsg | 用户消息入会话 |
| Delta | 流式增量（text/reasoning） |
| ToolCall | 模型发起调用（执行前） |
| ToolResult | 工具完成（含拒绝） |
| ConfirmRequest | 高危操作等待裁决 |
| Busy | 忙闲翻转 |
| TurnDone / TurnError | 轮次收尾（错误带 Aborted + Partial） |
| TodoUpdated | 任务清单变更 |

中断语义（从 local-myt-agent/pi-ai 继承）：取消不丢已生成内容——
error 事件携带 Partial，入历史并随事件带给宿主。

### 2.3 工具循环

- 上限 16 轮（编码任务"读→搜→改→验证"常态十几个来回；local-myt-agent 的 8 是设备运维场景）；
- 工具结果入历史上限 8KB/条（执行层 32KB 全文，历史层再收口）；
- 低危自动执行；高危先 `tools.Confirm` 生成提示 → `awaitConfirm` 挂起 →
  宿主 `Confirm(id, allow)` 裁决；拒绝回填自解释文本（模型换路）。

### 2.4 todo 状态归属

工具层只做校验与格式化（`tools.SetTodoSink` 注入），会话持有状态并广播
`TodoUpdated`——桌面壳的 TodoList 卡片直接从 Session.Todos() 渲染。
清单不持久化（规划辅助，非数据）——M2 视体验再定。

### 2.5 系统提示词

动态生成（注册表驱动）+ 手写守则（反偷懒三条、并行调用、edit 纪律、
todo 纪律、session_search 纪律）。`TestSystemPromptListsAllTools` 钉住
清单与注册表不漂移。

## 3. 验收记录（2026-09-10）

- `go build ./...` / `go vet ./...` / `gofmt -l .`（空）全过；
- `go test ./...`：**146 PASS / 2 SKIP**（Windows 跳过的 bash 只读用例；
  新增 protocol 帧契约 12、server 端到端 8、wsclient 10）；
- 真实端点冒烟（mytai.opencecs.com，anthropic 格式，**经前后台分离链路**：
  CLI 客户端 → WS → 后端进程 → LLM）：
  - 纯对话：流式回包 1.08s，自我介绍正确读取 identity；
  - 工具循环：`read_file config/local.json` → 低危自动执行 → 二轮正确
    回答"default 绑定 my"，1.72s 收尾；客户端重连恢复会话（6 条消息）；
  - 确认门：bash 高危 → 确认请求事件 → 客户端 y → tool.confirm RPC →
    执行 → 结果回填 → 二轮作答，1.86s 端到端。

## 4. M2 方向（未开始）

桌面壳选型（Wails v3 beta vs Electron+Go sidecar）→ 聊天主界面
（流式/思考折叠/工具卡片/审批卡/TodoList——设计语言见 local-myt-agent
的 temp/prototype/agent-console-v3.html + @aicss/react）→ 会话侧栏 →
设置页 → 托盘/热键。内核侧：上下文 compaction、多模态（vision 角色）、
语义记忆。
