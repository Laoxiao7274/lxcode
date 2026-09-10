# 01 · M1 内核设计

> 2026-09-10。M1 交付：纯 Go agent 内核 + CLI REPL（验收面）。
> 定位/架构基线见 AGENTS.md；本文记内核的分层与关键设计决策。

## 1. 分层

```
cmd/myt-harness      入口：装配（config → store → agent → cli）
internal/cli         REPL 宿主（零依赖、终端渲染、y/n 确认路由）
internal/agent       内核核心：会话运行时（工具循环/确认门/todo 状态）
  ├ events.go        typed Event（sealed interface）+ Snapshot
  ├ prompt.go        系统提示词动态生成（工具清单来自注册表）
  └ session.go       Send/Cancel/Confirm + runTurn/streamRound/runTools
internal/config      模型注册表（models.json 原子读写、角色绑定）
internal/store       JSONL 会话持久化（append-only、坏行容忍、恢复最近）
internal/tools       工具注册表 + 7 个内置工具（风险分级 + JSON 修复）
internal/llm         双格式 LLM 客户端（OpenAI/Anthropic、流式、ChatAuto）
```

依赖方向单向：cli → agent → {config, store, tools} → llm → stdlib。
agent 不 import cli（桌面壳后嵌入时零改动）；llm 不依赖任何本地包。

## 2. 关键设计决策

### 2.1 内核是包不是进程（vs local-myt-agent 的 WS 服务）

local-myt-agent 为远程设备 + TUI 客户端设计了 WS JSON-RPC 协议层。
myt-harness 的宿主是本机桌面壳（in-process），协议层整体取消：
事件改为 **typed Event（sealed interface）**，宿主 switch 编译期穷尽
（漏处理新事件 = 编译错误，不是运行时漏渲染）；确认门从 RPC 往返变为
`Confirm(id, allow)` 方法调用。将来若需要远程形态，加一个薄 server 包
把 Event 序列化下发即可——内核不用动。

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
- `go test ./...`：**120 PASS / 2 SKIP**（Windows 跳过的 bash 只读用例）；
- 真实端点冒烟（mytai.opencecs.com，anthropic 格式）：
  - 纯对话：流式回包 1.15s，自我介绍正确读取 identity；
  - 工具循环：`read_file config/local.json` → 低危自动执行 → 行号回填 →
    二轮正确回答"default 绑定 my"，1.93s 收尾（[stop · 2503 tokens]）。

## 4. M2 方向（未开始）

桌面壳选型（Wails v3 beta vs Electron+Go sidecar）→ 聊天主界面
（流式/思考折叠/工具卡片/审批卡/TodoList——设计语言见 local-myt-agent
的 temp/prototype/agent-console-v3.html + @aicss/react）→ 会话侧栏 →
设置页 → 托盘/热键。内核侧：上下文 compaction、多模态（vision 角色）、
语义记忆。
