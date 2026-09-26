# 后端化路线图（2026-09-18 定稿）

> 状态：M1–M4 已实现，M5（远程访问与客户端更新器）待做；已实现范围与剩余决策在本文同步维护。
> 依据：前端原型（`frontend/src/shared/agents.tsx` + `agent-types.ts`/`agent-seeds.ts`）定义 Agent 数据模型；当前后端按 session_id 持有独立运行时，存储使用 SQLite（sessions/messages/projects/worktrees）。

## 拍板记录

| 分叉 | 决策 | 时间 |
|---|---|---|
| 子 Agent 执行模型 | **独立持久子会话**——dispatch 为子 Agent 建独立 sessions/messages 与压缩检查点，任务消息作为子会话首条用户消息；可按 session id 续跑。子 Agent 看不到主会话历史，任务上下文明确注入 | 2026-09-22 |
| M1 范围 | **四张表一次成型**——agents/modules/tools/mcp_servers 一次迁移，agent.\* + catalog.\* 协议一起定 | 2026-09-18 |

## 数据模型（对齐前端 agent-types.ts——后端为事实源，前端同步消费）

```sql
-- agents：组装出的 Agent 名单（两类制：main 唯一调度者不可被委派）
CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  desc TEXT NOT NULL DEFAULT '',
  color TEXT NOT NULL DEFAULT '#3b82f6',
  model TEXT NOT NULL DEFAULT '',          -- 绑定模型（模型注册表 id；空 = 未绑定）
  tools TEXT NOT NULL DEFAULT '[]',        -- JSON: 工具白名单 id 数组（主 Agent 恒 ["agent_dispatch"]）
  workflow TEXT NOT NULL DEFAULT '',       -- 流程模块 id（单选；空 = 无）
  skills TEXT NOT NULL DEFAULT '[]',      -- JSON: 技能模块 id 数组（多选）
  delegates TEXT NOT NULL DEFAULT '[]',   -- JSON: 主 Agent 的默认委派名单（子 Agent 恒空）
  approval TEXT NOT NULL DEFAULT 'confirm', -- auto|confirm|strict（会话内可临时覆盖——协议层，不落库）
  enabled INTEGER NOT NULL DEFAULT 1,
  is_main INTEGER NOT NULL DEFAULT 0,      -- 两类制：主 Agent 唯一；不可被委派
  prompt TEXT NOT NULL DEFAULT '',         -- 自定义上下文段（四层组合的第三层）
  protocol TEXT NOT NULL DEFAULT '',       -- 定制协议（空 = 内置默认——第一层；保存时与默认相同则存空）
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- modules：上下文模块目录（模板/技能——纯 markdown 内容，单选注入/多选注入）
CREATE TABLE modules (
  id TEXT PRIMARY KEY,
  desc TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('process','skill')),
  body TEXT NOT NULL DEFAULT '',           -- markdown 正文（实际注入 Agent 上下文的内容）
  custom INTEGER NOT NULL DEFAULT 1,       -- 用户自建可删；内置种子由代码种入（custom=0）
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- tools：工具目录（自定义/binary——执行面在 M4；M1 只存元数据）
CREATE TABLE tools (
  id TEXT PRIMARY KEY,
  desc TEXT NOT NULL DEFAULT '',
  risk TEXT NOT NULL CHECK (risk IN ('low','high')),
  source TEXT NOT NULL CHECK (source IN ('builtin','binary','mcp')),
  params TEXT NOT NULL DEFAULT '[]',       -- JSON: [{name,type,required,desc}]
  doc TEXT NOT NULL DEFAULT '',
  server TEXT NOT NULL DEFAULT '',          -- source=mcp 时的来源服务器 id
  command TEXT NOT NULL DEFAULT '',         -- binary：{param} 占位模板
  example TEXT NOT NULL DEFAULT '',
  package_file TEXT NOT NULL DEFAULT '',   -- 声明的程序包文件名
  custom INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- mcp_servers：MCP 接入单元（stdio = command+args+env / sse = url）
CREATE TABLE mcp_servers (
  id TEXT PRIMARY KEY,                     -- 服务器名（唯一；tools.server 指向它）
  desc TEXT NOT NULL DEFAULT '',
  transport TEXT NOT NULL CHECK (transport IN ('stdio','sse')),
  command TEXT NOT NULL DEFAULT '',
  args TEXT NOT NULL DEFAULT '[]',         -- JSON: string[]
  env TEXT NOT NULL DEFAULT '{}',           -- JSON: Record<string,string>
  url TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  custom INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

- 所有 `updated_at` 用 RFC3339Nano（与 sessions 表同款）；排序 `ORDER BY rowid` 兜底（同秒确定性）。
- 内置种子（agent-seeds 的 Go 版）：首次迁移后 seed 入库，`custom=0` 只读。
- `agents.is_main` 唯一约束：主 Agent 恒一条（应用层维护，删除被拒）。

## M1 — 注册表与目录（地基，纯 CRUD）

**范围**：四张表迁移 + 种子 + `agent.*`/`catalog.*` 协议 + 前端接线。

**协议方法**（server 层转发，同 project.\* 模式——业务在 store，转发在 server）：

```
agent.list    → [{id,name,desc,color,model,tools,workflow,skills,delegates,approval,enabled,isMain,prompt,protocol}]
agent.add     {agent}            → 注册（id 冲突拒绝；isMain 由种子初始化后不可再建）
agent.update  {agent}            → 全量替换（isMain 的 delegates 可改；类型字段不可改）
agent.remove  {id}               → 删除（主 Agent/启用中/被引用不可删——详见校验）
agent.changed                   → 广播（多客户端同步重拉 agent.list）

catalog.modules.list / add / update / remove
catalog.tools.list / add / update / remove        → 自定义工具（builtin 只读）
catalog.mcp.list / add / update / remove
catalog.changed {kind}
```

**校验**（协议层拒绝，错误自解释——与前端表单校验对齐）：
- id 非空且目录内唯一；agents.name/modules.desc 非空
- agent.remove：is_main 拒绝（「主 Agent 不可删除」）；其他 agent 的 delegates 引用它时拒绝（或自动摘除——**拍板：拒绝并列出引用方**，与 MCP 导入的查重语义一致）
- module.remove：被 agent 的 workflow/skills 引用时拒绝并列出
- tool.remove：被 agent 的 tools 引用时拒绝并列出（M1 阶段目录条目少，列出引用是可承受的；引用图复杂后考虑软删除）

**前端接线**（AgentSource 扩展，复用 modelAdmin 能力模式）：
- `types.ts` 加 `AgentAdminSource` 接口：agents()/catalog()/addAgent/updateAgent/.../onChanged
- WSAgent 实现（对应 wsclient 方法）+ DemoAgent 内存实现（现状语义不变）
- AgentsProvider/搜索 Provider：真实模式从 WS 拉+写回，demo 保持内存种子
- 前端 agent.seeds 的 TS 种子只服务 demo 模式（与后端 Go 种子内容一致——单测双向钉住?：**拍板：前端种子的结构与后端 JSON 契约一致即可，不逐字同步**——前端演示数据允许更丰富）

**验收**：
1. 真实模式：建 Agent/改委派名单/建模板/建自定义工具 → 刷新 → 全部还原
2. 多客户端：A 改 B 收 agent.changed 重拉
3. 引用校验：删被引用的模板被拒且错误指明引用方
4. demo 模式行为与现状完全一致（不回归）
5. go test 全绿 + 协议帧契约测试（agent.* 载荷形状）+ 前端 npm test 全绿

**不做**（明确出界）：chat.send 带 agentId（M2）；dispatch（M3）；工具执行（M4）。

## M2 — 上下文组装 + Agent 直选

- `chat.send` 可选参数 `agent`（Agent id；空 = 主 Agent）——随消息携带，与 effort/approval 同模式
- **上下文组装器**（agent 包内纯函数，`ComposeSystemPrompt(def, catalog, delegates, workDir)`）：
  四层组合（协议[空=内置默认] + workflow 模块单选 + skills 多选 + 自定义段）+ 动态注入（主 Agent 的有效委派名单 = 前端 `effectiveDelegates` 的 Go 版——「覆盖 ?? 默认 ∩ 启用」）
- **模型绑定**：Session 的模型选择从「全局 default 角色」改为「Agent 绑定优先，回落 default」
- **工具白名单过滤**：runTools 前按 Agent.tools 过滤注册表（主 Agent 恒只含 dispatch；strict 只读等既有语义不动）
- **approval 语义**：请求级 > Agent 默认（现状请求级 > 全局默认——插入一层）
- 前端：chat.send 带 activeAgentId（SendOptions 扩展）；空态/AgentPicker 语义不变
- 验收：对话真的按选中 Agent 的人格/工具面/模型跑（真模型冒烟：选不同 Agent 发消息，行为可辨）

## M3 — agent_dispatch 运行时（子 Agent 独立持久会话）

- 新工具 `agent_dispatch`（主 Agent 唯一工具——`Def.Risk=low`、params: agent/task/context）：
  1. 校验目标在有效委派名单内（不在则错误回填模型自解释）
  2. **开独立持久子会话**：创建 `sessions` 行并按 `call.Session` 续跑；不混入主会话历史，保留自己的消息与压缩检查点（细节见 AGENTS.md §2.3）
  3. 子 Agent 的四层组合 + 模型绑定 + 工具白名单 + **权限取严**（子 Agent 默认 approval 与主 Agent 的会话级请求取更严者）
  4. 跑完整工具循环（复用 Session 的循环结构——抽出可复用的 runLoop）
  5. 最终 assistant 回复作为 dispatch 的工具结果回填主会话
- 事件流（前端 Thread 的 dispatch 块渲染依据）：
  - `chat.dispatchStart` / `chat.dispatchEnd` 携带 `owner_session_id`（归属父时间线）与 `session_id`（子会话）——前端据此显示并回放 dispatch 卡。
  - 子执行的 delta/toolCall/toolResult 等事件携带父 `session_id` 与 `dispatch_id`，前端按两者路由到正确会话及 dispatch 卡；不让焦点切换改变事件归属。
- 并发与预算：同一父会话内 dispatch 仍按工具循环顺序完成，子执行继承父取消 context；不同顶层会话可并发运行，各有自己的 busy 状态与取消。当前没有全局并发上限、供应商预算或公平调度器。
- 验收：真链路「让主 Agent 派代码 Agent 干活」——子 Agent 的工具调用在前端 dispatch 卡内可见；中途取消主会话，子执行同步停

## 多活跃会话与项目 worktree（已实现）

- server 按 session id 持有独立 `agent.Session`；发送、确认、取消、压缩、历史加载均显式带 `session_id`。每个会话一轮生成，顶层会话之间无进程级并发上限；服务端广播按所属会话隔离，CLI 与前端均可在生成时切换焦点。
- 项目会话首次发送时，在 `<sessions>/worktrees/<project-id>/<session-id>` 建立 `lxcode/session-<session-id>` 分支与 Git worktree，基线取此刻的项目 `HEAD`。未提交的主工作树改动不复制；没有可用 `HEAD` 时明确报错，不创建提交、不退化为共享路径。
- Worktree 元数据写入 SQLite；重启时复用同一工作树/分支，若目录被删则尝试从已有分支恢复。侧栏“释放工作区”只对有项目且已有消息的会话显示；仅允许释放空闲、干净的工作树，脏/未跟踪改动会拒绝，Git 忽略文件随目录删除。释放只移除检出目录，保留会话、元数据和分支；再次恢复时重挂原分支。归档不触发清理，会话互不自动合并；用户需自行 cherry-pick 或 merge。子 Agent 继承父会话的工作目录。
- 空白项目会话在首次发送前不创建 worktree；未分组会话没有 worktree，仍共享后端默认目录，因此不具文件系统隔离。无全局队列或供应商预算；提供方自身限流仍可能生效。

## M4 — 拓展执行面

- **自定义工具执行**（tools 包）：`command` 模板 + `{param}` 填充（参数值来自工具调用的 arguments）→ spawn（无 shell——模板按空格切分 + 参数值原样单参，引号无语义）；workDir 继承会话；超时/输出上限对齐 bash 工具；风险等级进注册表（高危走确认门——确认文本含完整命令）
  - **已落地（2026-09-21）**：`internal/tools/custom.go`（`CustomDef`：params→JSON Schema、模板渲染、spawn）+ `Registry.SetDynamic`（动态段整体替换，内置段不动，注册表加 RWMutex）+ `internal/server/tools_sync.go`（启动与 `catalog.tools.*` 变更后同步；未配 command 的条目跳过并记日志）+ 提示词层回落 `Def.Description`（原先只认 `systemPromptTools` 固定名 → 自定义工具会被静默漏掉）与「白名单里未注册的工具」点名。真链路冒烟 `temp/smoke-m4.mjs` 4/4（真模型调用自定义工具 → 真实 `go version` 输出回填）。
- **MCP 客户端**（新 internal/mcp）：stdio（spawn mcp 服务器进程，MCP 协议握手）→ tools/list 发现 → 以 `source=mcp, server=<id>` 注册进注册表（id 冲突加前缀）；服务器启停 = 注册/注销其工具；停用 = 工具挂起（保留目录条目）——**未做**
- **网页搜索工具**（`web_search`）：读搜索渠道配置（渠道表复用 tools 表? **拍板：渠道配置存 config 目录的 search.json**——它是环境配置不是拓展目录）+ 主渠道优先失败降级——**未做**（前端设置面板的渠道配置已就绪）
- 验收：拓展页建的自定义工具/MCP 工具真的能被 Agent 调用并出结果

## M5 — 远程访问与更新

- **远程访问**：`--remote` 或配置开关 → 监听 0.0.0.0 + hello 携带 token 校验（Authorization: Bearer）；token 生成/重置接口（重置 = 旧失效）；与前端连接管理的远程访问面板对接（地址上报经 hello）
- **更新器客户端**（壳侧）：manifest 检查（版本对比）→ 下载 update-\<version\>.zip → sha256 校验 → 两级替换（后端热替换：新 exe 就位+优雅重启；asar 冷替换：退出时换）→ 重启
- 验收：壳从公网地址连后端（token 对）；更新全流程（检查→下载→校验→替换→重启）真机通过

## 明确不做（本期范围外）

- 顶层多活跃会话与项目 worktree 已实现（见下节）；仍未做供应商预算/公平调度器
- 子 Agent 再委派（两类制结构性禁止——深度恒 1）
- 多层 Agent 会话树/子 Agent 再委派（两类制从结构上限制 dispatch 深度为 1）
- MCP 的 HTTP/SSE 传输（stdio 先行——桌面场景主流）
