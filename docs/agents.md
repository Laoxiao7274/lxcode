# docs/agents.md — Agent 定义结构与配置维护（lxagent）

> Agent 注册表（可组装 Agent 域）的数据模型、读写链路与「用对话改配置」的通道。
> 2026-10 新增 `agent_read` / `agent_write` 两个工具与内置 Agent **lxagent** 时成文。

## 1. 数据模型（SQLite `agents` 表）

事实源在 `internal/store/agents.go`（建表）+ `catalog_seeds.go`（种子注入与增量同步）。一行一个 Agent：

| 字段 | 含义 |
|---|---|
| `id` | 名单 id（主键，**不可改**——它是委派、派发、子会话归属的寻址键） |
| `name` / `desc` / `color` | 展示名 / 职责描述 / 标识色 |
| `model` | 绑定模型 id（空 = 回落 default 角色） |
| `tools` | 工具白名单（JSON 数组，工具目录 id；白名单过滤在 `agent.llmToolsFiltered`） |
| `workflow` | 过程模块（modules 表 kind=process 的 id） |
| `skills` | 技能绑定（JSON 数组，modules 表 kind=skill 的 id） |
| `delegates` | 委派名单（仅主 Agent 消费——有效名单 = 默认 ∩ 启用，见 `server/agent_resolver.go`） |
| `approval` | 权限默认档（auto/confirm/strict） |
| `enabled` / `is_main` / `custom` | 启用 / 主 Agent 标记（结构唯一）/ 用户自建 |
| `seed_hash` / `user_modified` | 种子热更新的判定列：`user_modified=1` 的行永不被种子覆盖 |

`desc` 是**主 Agent 的选人信号**：可委派名单按它逐字生成进系统提示词，漏写能力主 Agent 就无从选人。

## 2. 读写链路

- **前端编辑器**（AgentEditor / 设置面板）：JSON-RPC `agent.list` / `agent.add` / `agent.update` / `agent.remove`（`internal/server/agent_catalog.go` → store 直通 + 协议映射）；`agent.update` 全量写并置 `user_modified=1`。变更后广播 `agent.changed`。
- **对话通道**（lxagent）：`agent_read` / `agent_write` 两个工具（`internal/tools/agent_catalog.go`），server 装配时经 `SetAgentCatalogOps` 注入 store 实现（tools 是叶子包，不 import store——与 workspace 三件套同款注入模式）。
- **委派校验**：`agent_dispatch` 派发时按主轮 Agent 载荷的 `Delegates`（默认 ∩ 启用）校验（`internal/agent/dispatch.go`）；新增可委派 Agent 的落地点见 §4。

## 3. agent_read / agent_write（对话改配置）

| 工具 | 风险 | 行为 |
|---|---|---|
| `agent_read` | 低危只读 | 不带 id 列出全部（id/名称/职责/白名单/技能/状态）；带 id 读单个完整定义（含提示词模板全文、委派名单）。列表视图刻意不带模板全文——上下文考虑。 |
| `agent_write` | 高危（确认门） | 按 id **字段级**更新：`desc` / `prompt` / `tools` / `skills`，未提供的字段不动；id 不可改（参数形状里根本没有新 id——形状保证而非校验）。落库走 `store.UpdateAgentFields`，成功后广播 `agent.changed`（编辑器界面同步）。**落库即生效**：该 Agent 下次被派发就用新配置。 |

**共享校验**（`sessiondata.ValidateAgentFieldUpdate`，两条写路径共用一份，防规则漂移）：

1. 主 Agent 不可改（工具白名单是结构性的、提示词每轮现组装）；
2. 提示词模板非空白；
3. 工具白名单不能清空（清空 = 失能；要停用请禁用该 Agent）；
4. 白名单里每个工具名必须在**工具目录**（`tools` 表）里真实存在；
5. 技能绑定每个 id 必须在模块目录（`modules` 表）里存在；清空绑定合法（不绑技能是合法配置）。

「存在」的判定基准是目录表而不是运行期注册表：目录是编辑器 chip 与白名单的事实源，注册表里没有的条目（未配置 command 的 binary 工具、未连接的 MCP 服务）在目录里合法存在——拿注册表当裁判会把现存合法白名单判死。

`agent_write` 落库同样置 `user_modified=1`：agent 代用户做的修改同样受「种子热更新不覆盖」保护。删除 Agent / 改名 / 建新 Agent / 调整委派名单**不在** agent_write 的语义内（走前端编辑器）——「不可删除最后一个 Agent」由「无删除能力 + enabled 不可写」结构性保证。

**确认门**：`agent_write` 的 `Def.Confirm` 产出确认文案（改哪个 Agent、动哪些字段、下次派发生效、白名单增删的后果）。运行期 gate 由 `agent.runTools` 的既有路径按 `Risk=high` 驱动——confirm 档弹卡、auto 档放行、strict 档按 Mutates 拒绝，**无新前端 UI**。

## 4. 新增一个内置 Agent 要动哪几处

1. `internal/store/agents_seeds.go` 的 `seedAgents`：加条目（id/name/desc/prompt/tools/workflow/approval/color）。desc 写清能力（选人信号）。
2. 可委派性：
   - 新库：主 Agent 种子的 `Delegates` 加上新 id；
   - 老库：`topUpMainDelegates` 自动补（前提：用户没删过名单里的种子子 Agent——判定基准 `seedDelegatesBaseline`）；
   - 不想被委派（如 merger）→ 加进 `seedNonDelegatable`。
3. 若该 Agent 的白名单/描述在本版**新增**了内容，把**上一版**的值留进 `seedAgentBaselines`（top-up 只在用户没动过该字段时补新工具/新描述）。
4. 工具白名单引用的每个工具必须在 `seedTools` 目录里（编辑器 chip 只读目录——目录里没有 = 界面上勾不到）。
5. 前端 `frontend/src/shared/agent-seeds.ts`：`BUILTIN_TOOLS`（新工具时）与 `seedAgents()` 同步对齐。
6. docs：AGENTS.md §3 工具表（新工具时）。

## 5. lxagent（内置 Agent 配置维护员）

- id `lxagent`，专司**改其他 Agent 的配置**：提示词模板、工具白名单、技能绑定。它的手脚就是 `agent_read` / `agent_write`。
- 职责边界（写在它的提示词里）：只改配置不替用户做业务决策；动手前先 `agent_read` 摸现状、向用户确认意图；`agent_write` 是高危（影响后续所有派发）——白名单加写工具 = 目标 Agent 获得改动文件的能力，把后果说清。
- 审批档 confirm：确认门在 confirm 档下生效，配置变更必须用户点头。
- 用户已有的自定义 Agent 不受影响：种子只补缺失行（`ensureSeedAgents`），已有行一律不碰。
