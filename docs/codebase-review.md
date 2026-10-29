# 代码审查：超长文件与解耦（2026-09-28）

> **范围**：全仓——Go 168 文件 / 39.7k 行；前端 71 tsx + 44 ts / 14.5k 行 + 6 css / 7.0k 行；scripts 19 文件 / 2.7k 行。
> **方法**：机械统计（不是读感）——行数、函数长度、重复实现、死代码。脚本：`temp/code-size.cjs`、`temp/long-funcs.cjs`、`temp/dup-funcs.cjs`、`temp/dup-websearch.cjs`、`temp/dead-code.cjs`、`temp/outline.cjs`、`temp/css-sections.cjs`。
> **与上一份的关系**：`docs/frontend-review.md`（2026-09-18）只覆盖前端，其 8 项**已全部落地**（逐项核对：Thread 窗口化 ✅、`scripts/bench-render.cjs` ✅、Orb 已删 ✅、catalog 拆 9 文件 + `useConfirmClick` ✅、`agents.tsx` 三拆 ✅、App 双订阅已合并 ✅、connections 拆分 ✅、EmptyState ✅）。本文件是续篇，重点补它没覆盖的**后端与 CSS**。

## 一、超长文件（客观数据）

| 行数 | 文件 | 性质 |
|---|---|---|
| 6362 | `frontend/src/styles/global.css` | **一个文件 40 节**（最大问题） |
| 1389 | `scripts/check-preview-layout.cjs` | 开发期校验脚本（非产线代码） |
| 1329 | `internal/agent/session.go` | 7 种职责混在一个文件 |
| 1108 | `internal/server/server.go` | 含 **354 行的 `dispatch`** |
| 1071 | `internal/store/agents.go` | 4 套 CRUD + 种子同步 |
| 884 | `internal/store/store.go` | 会话存储（尚可） |
| 781 | `frontend/src/agent/ws/index.ts` | 一个类实现 5 个接口 + 事件映射 |
| 706 | `internal/protocol/protocol.go` | 协议帧定义（单处定义，**合理**） |
| 565 | `frontend/src/shared/store.ts` | reduce 217 行 + 历史归约 + hook |
| 517 | `internal/cli/repl.go` | CLI 渲染 + 命令分发 |
| 500 | `internal/websearch/exa.go` | 直连 API + 免配置 MCP 两条路 |

全仓 >500 行 21 个、>800 行 7 个、>1000 行 5 个。测试文件占大头是正常的（`session_test.go` 885、`protocol_test.go` 705 等），本审查只针对**非测试**文件。

## 二、解耦：按职责拆文件（Go 侧，纯搬移、零行为变更）

三个包的**拆法已经存在**（`internal/agent` 11 文件、`internal/server` 5 文件、`internal/store` 5 文件），所以下面的建议是**延续既有约定**，不是新造结构。同包内搬移不改任何 API、不动一行逻辑，`go build`/`go test` 就是验收。

**① `internal/agent/session.go`（1329）→ 7 个文件**

| 新文件 | 内容 | 约行数 |
|---|---|---|
| `session.go` | Session 类型 + New + 全部 setter/访问器（Send/Cancel/SendWait/Busy/History/Todos/ContextUsage/Close/WorkDir） | ~450 |
| `turn.go` | `runTurn`(124) + `streamRound`(89) + `streamWithLLM` + `recordContextUsage` | ~330 |
| `tools.go` | `runTools`(128) + `finishToolCall` + `pendingCalls` + `toolOutcome` + `sinkSkippedToolResults` + `sanitizeToolCallArgs` + `collectFileChange` | ~300 |
| `confirm.go` | `awaitConfirm` + `Confirm` + `SetConfirmProxy` | ~120 |
| `resolve.go` | `resolveAgent` + `modelFor` + `effectiveApproval` + `stricterApproval` + `approvalRank` + `agentDefaultOf` + `agentToolsOf` + `llmToolsFiltered` + `SendOpt`/`With*` | ~200 |
| `persist.go` | `append` + `persistLocked` + `ensureSessionLocked` + `resolveWorkspaceDir` + `EnablePersistence` + `AttachTo` + `checkpointIndexes` | ~230 |
| `switch.go` | `SwitchNew` + `SwitchTo` + `SessionList` + `AttachSessionSearch` + `switchGuardLocked` | ~120 |

**② `internal/server/server.go`（1108）→ 6 个文件**

`dispatch` 是**全仓最长函数（354 行）**：一个函数里按方法名前缀依次问各域处理器 + 内联了 chat/session/project/update 的核心方法。该包**已有域文件的先例**（`agent_catalog.go` 176 行的 `dispatchAgentCatalog`、`mcp.go`、`search.go`、`tools_sync.go`），所以拆法是照抄自己的约定。

| 新文件 | 内容 |
|---|---|
| `server.go` | Server 类型 + NewServer + Handler/serve + broadcast + Run + closeAllClients |
| `dispatch.go` | `dispatch` 拆成「前缀匹配 → 每域一个 `dispatchXxx` 小函数」（chat / session / project / model / update / 其余） |
| `session_ops.go` | `session` + `createSession` + `history` + `sendSession` |
| `worktree.go` | `prepareWorktree*` + `releaseSessionWorktree` + `worktreeReady*` + `sessionWorktreeLock` |
| `emit.go` | `emitEvent` + 全部 `toProtocol*` 转换器 |
| `agent_resolver.go` | `storeAgentResolver` + `containsStr` 的调用点 |

**③ `internal/store/agents.go`（1071）→ 5 个文件**

| 新文件 | 内容 |
|---|---|
| `agents.go` | Agent CRUD（List/Add/Update/Remove + `validateAgent` + `scanAgent`/`decodeStrList`） |
| `catalog_seeds.go` | `initAgents` + `syncCatalogSeeds` + `ensureSeedAgents` + `topUp*` + `upsertSeed*` + `insert*` + 基线表 |
| `modules.go` | 模块 CRUD |
| `tools.go` | 工具 CRUD + `validateTool` + `validToolID` |
| `mcpservers.go` | MCP 服务器 CRUD + `checkToolRefs` |

**④ `frontend/src/styles/global.css`（6362，40 节）→ 10 个文件**

按文件里**已有的 40 个分节注释**原样切开，`main.tsx` 按序 import（不引入 barrel、不用 `@import`——多文件 import 由 Vite 保序，且比 `@import` 少一层解析）。顺序敏感的只有两处：`:root` 的 token 必须最先、文件末尾的 `@media (prefers-reduced-motion)` 覆盖块必须最后。

`base.css`（:root + 布局骨架 + 响应式 + 动效减免）、`topbar.css`、`sidebar.css`、`thread.css`（消息块 + 工具行 + 空态 + 输入区）、`cards.css`（diff/产物/终端/代码围栏/dispatch 卡）、`composer.css`（斜杠面板 + 上下文指示器 + 模型选择器 + 浮层）、`settings.css`（设置弹窗 + 模型分区 + 网页搜索 + 更新 + 连接提供商）、`agents.css`（Agent 名单 904 行）、`catalog.css`（目录管理页 421 行）、`git.css`（Git 工作台 792 行）。

**⑤ 其余**：`agent/ws/index.ts`（781）把事件映射抽成 `ws/events.ts` 纯函数（`handleEvent` 单函数 162 行）；`shared/store.ts`（565）拆 `reduce.ts`(217) + `history.ts`(64)；`cli/repl.go`（517）拆 `commands.go`(123) + `render.go`(88)；`websearch/exa.go`（500）拆 `exa_mcp.go`（免配置通道 ~250 行）。

## 三、函数提取：可公用的重复

**① 网页搜索适配器样板——全仓最大的一处（约 360 行）**

26 个渠道适配器的 `Search` 开头是同一段样板，逐字重复：

| 重复片段 | 出现在 |
|---|---|
| `未配置 API key（获取地址: <docURL>）` 的 key 校验 | **22 个文件** |
| `base == "" → 默认 base` 兜底 | **24 个文件** |
| `NormalizeNumResults(opts.NumResults)` | **25 个文件** |
| `DomainFilterParts(opts.DomainFilter)` | **24 个文件** |
| `newJSONRequest(ctx, ...)` | **27 个文件** |
| `requestJSON(WithRequestTimeout(...))` | **16 个文件** |
| 内联 `Authorization: Bearer`（已有 `bearer()` 却没用） | 3 个文件 |

建议：给 `base` 加两个方法——`prepare(ch, opts) (searchPrep, error)`（key 校验 + base 兜底 + num + include/exclude）与 `postJSON(ctx, ch, path, body, &raw, timeout)`。各适配器只留**渠道特有的 body 组装与响应解析**。这是「新增一个渠道」成本的直接下降：从 ~15 行样板 + 解析，变成只有解析。**必须靠 `adapters_batch_*_test.go`（2167 行）兜底**——它们逐渠道钉住了行为，正是这种重构的安全网。

**② `containsStr` 逐字节重复**：`internal/server/server.go:485` 与 `internal/store/agents.go:333` 完全相同。抽到一个叶子包（与 `internal/jsonrepair` 同级的 `internal/strutil`，或就近各留一份但**必须**只留一处）。

**③ 两个 `clipStr` 语义不同，别合**：`cli/repl.go:484` 取首行、`store/store.go:862` 把换行换成空格。名字相同行为不同是隐患（读代码的人会以为一样）——建议**改名区分**（`clipFirstLine` / `clipOneLine`），而不是合并。

**④ `truncate` ×2**（`mcp/http.go:203` 与 `project/project.go:72`）：合并前先读后者，两者截断口径（是否 TrimSpace、后缀文案）大概率不同——同 ③，先改名再谈合并。

**⑤ `FormatSearchHits`**（`store/store.go:881`）是**有意转发**到 `sessiondata`，不是重复，别动。

## 四、超长函数（>100 行）

| 行数 | 位置 | 判断 |
|---|---|---|
| **354** | `server.go:594 dispatch` | **必拆**：按方法前缀分派给每域小函数 |
| 176 | `server/agent_catalog.go:92 dispatchAgentCatalog` | 可拆：按 catalog 子域 |
| 164 | `llm/anthropic_stream.go:50 parseAnthropicStream` | 可拆：SSE 事件分支各自成函数 |
| 138 | `llm/openai_stream.go:44 parseOpenAIStream` | 同上 |
| 128 | `agent/session.go:749 runTools` | 已按三阶段写清，可把「权限门」抽成 `gateTool` |
| 124 | `agent/session.go:515 runTurn` | 可把压缩触发与溢出重试抽成函数 |
| 123 | `cli/repl.go:326 runCommand` | 可拆成每个斜杠命令一个小函数 |
| 108 | `tools/bash.go:87 bashDef` | 主要是工具描述文本，**不用动** |
| 108 / 91 / 87 / 83 | `websearch/{querit,searchinfinity,serpbase,parallel}.go` | 见 §三-①（样板提取后自然变短） |
| 324 | `agents/AgentEditor.tsx` | 表单本体 + DetailPanel 可再拆 |
| 324 | `sidebar/Sidebar.tsx` | 五个视觉段可拆 |
| 298 | `git/GitWorkbenchPage.tsx` | 可拆面板 |
| 272 / 237 | `catalog/ToolEditor.tsx` / `CatalogPage.tsx` | 可拆表单/编排 |
| 217 | `shared/store.ts reduce` | 按事件族拆 |

## 五、目录结构

**已经很好的部分（别动）**：`internal/{agent,server,store,websearch}` 的分文件约定、`frontend/src/components/{catalog,connections,thread/blocks,form,settings}` 的域内聚、`shared/` 的纯函数 + 测试配对、`scripts/check-boundaries.mjs` 的静态边界守卫。

**要修的**：只有上面三个「一个文件装多个域」的点（session.go / server.go / agents.go）+ 一个 CSS 单体。

**边角**：`internal/websearch` 34 个文件按渠道一文件——布局是对的，问题是样板没提取（§三-①）；`temp/` 下 26 个一次性探针脚本（gitignored，不入仓）属于本地杂物，不算仓库代码。

## 六、过度设计：结论是**没有**

死代码扫描：全仓 **825 个导出符号，只有 9 个**在非测试代码里找不到引用，逐个核对后全是「包内自用的类型」（`ToolPairing`/`REPL`/`Registrar`/`Annotations`/`Instructions` 等），**没有一个是废弃代码**。也没有发现「只有一个实现的投机抽象」——`agent.Persistence`、`AgentResolver`、`AgentSource` 都是消费者定义的最小接口，是为测试与依赖倒置（AGENTS.md §4 分层规则明确要求）。

**所以这份代码库的问题不是抽象太多，而是该提取的地方没提取**：§三-① 的 360 行样板、§二 的三个 god file。这是好消息——做提取是纯收益，不需要先删抽象。

唯一可议的「大」是非产线件：`scripts/check-preview-layout.cjs` 1389 行（含逐场景断言 + dispatch 全链路冒烟）。它是开发期工具，拆它收益低，建议只把「场景清单」与「测量骨架」分开。

### 附：布局校验脚本已长期失效（本次审查实测）

跑 `node shell/node_modules/electron/cli.js scripts/check-preview-layout.cjs --no-sandbox --disable-gpu` 在 **HEAD（未含本次改动）** 上就是 **exit=1**。原因是它**只有一个顶层 try/catch**——第一个断言失败即 `finish(1)`，后面全部场景不再执行，于是失效可以长期积累而无人察觉。实测到的三处：

| # | 位置 | 现象 | 性质 |
|---|---|---|---|
| 1 | 工具目录卡片数（`afterImport.cards === 13` 等四处） | 断言写死基线 12/13，而演示目录的内置工具随种子演进（本轮加了 `web_search`/`read_skill`）→ 基线变 14 | **脆性断言**（已改为 `cat.cards + 1` 的相对断言） |
| 2 | `[data-sp="configure"]` 选择器 | 组件早已把按钮改名 `data-sp="edit"`，脚本没跟上 | **选择器过期**（已修） |
| 3 | 「配置后应显示遮罩 key 与设为主渠道入口」 | demo 模式下 `.sp-edit` 的保存按钮是 `disabled={!live}`，点不动 | **产品自相矛盾**（未改，见下） |

第 3 条值得单独说：`shared/search-admin.tsx` 的 demo 分支**完整实现了 `save`**（128-150 行，还带「与后端 channelReady/present 同语义」的注释，会重算 `configured`），而同文件的「清除」按钮在 demo 下**可用**——只有「保存」被 `disabled={!live}` 挡死。也就是说 demo 里这段实现是**不可达代码**，而校验脚本恰恰按「能保存」写的。两个修法各有取舍：

- **改脚本**（把断言改成 demo 诚实形态：保存按钮禁用、跳过后续保存链）：不动产品行为，但搜索分区的端到端覆盖只剩「表单能展开」。
- **改 UI**（去掉 `disabled={!live}`，让 demo 走它自己已实现的保存路径）：与 demo 的其余行为一致（清除可用、编辑可用），校验脚本的覆盖也回来了；代价是 demo 会「假装保存成功」（仅内存态）。

这条**需要你拍板**，本次只修了前两条（不涉及产品行为）。

## 七、建议实施序

| # | 事项 | 类型 | 收益 | 风险 | 工作量 |
|---|---|---|---|---|---|
| 1 | websearch 适配器样板提取（§三-①） | 提取 | 高（-360 行 + 新增渠道成本下降） | 低（2167 行适配器测试兜底） | 半天 |
| 2 | `agent/session.go` 拆 7 文件（§二-①） | 解耦 | 高 | 低（纯搬移，同包） | 1–2h |
| 3 | `store/agents.go` 拆 5 文件（§二-③） | 解耦 | 高 | 低 | 1–2h |
| 4 | `global.css` 拆 10 文件（§二-④） | 解耦 | 中（视觉回归面大，需截图对照） | 低（保序） | 1h + 目视 |
| 5 | `server/server.go` 拆 6 文件 + `dispatch` 拆域（§二-②/§四） | 解耦 | 高 | **中**（协议热路径，靠 server 测试兜底） | 半天 |
| 6 | `containsStr` 去重 + 3 处内联 Bearer 改用 `bearer()`（§三-②③） | 清理 | 低 | 极低 | 10min |
| 7 | `shared/store.ts` / `agent/ws/index.ts` / `cli/repl.go` 拆（§二-⑤） | 解耦 | 中 | 低 | 2–3h |
| 8 | `clipStr`/`truncate` 改名区分（§三-③④） | 可读性 | 低 | 极低 | 10min |
| 9 | `check-preview-layout.cjs` 场景与骨架分离 | 清理 | 低 | 低 | 1h |

## 八、落地进度（2026-09-28，§七 九项 + 追加三项全部收口）

| # | 事项 | 结果 | 状态 |
|---|---|---|---|
| 2 | `agent/session.go` 解耦 | 1329 → **7 文件**（session 342 / turn 225 / tools 242 / resolve 119 / persist 128 / switch 82 / confirm 57） | ✅ |
| 3 | `store/agents.go` 解耦 | 1071 → **5 文件**（agents 216 / catalog_seeds 262 / modules 96 / tools 148 / mcpservers 176） | ✅ |
| 4 | `styles/global.css` 解耦 | 6362 → **13 文件 + 14 行入口**（最大 composer 1048 / thread 968 / agents 906） | ✅ |
| 5 | `server/server.go` 解耦 | 1108 → **6 文件**；`dispatch` **354 行 → 36 行** + 5 个域文件（models/chat/sessions/projects/hello） | ✅ |
| 1 | websearch 适配器样板提取 | **20 个渠道** → `base.resolveBase`（`git diff --stat`：21 files, +78 −140） | ✅ |
| 7 | 前端与 CLI 解耦 | `cli/repl.go` 459 → repl 284 / render 119 / commands 138；`agent/ws/index.ts` 780 → index 692 + events 98；`shared/store.ts` 565 → store 70 / blocks 123 / history 71 / reduce 324 | ✅ |
| 8a | `clipStr` ×2 改名区分 | cli → `clipFirstLine`（取首行）、store → `clipOneLine`（换行转空格） | ✅ |
| 8b | `containsStr` 去重 | 两份都删，改用标准库 **`slices.Contains`**（Go 1.21+，仓库是 1.26）——连测试里的私有 helper 也一并去掉 | ✅ |
| 9 | 布局校验脚本场景与骨架分离 | 1370 → **1327 行场景** + `scripts/lib/preview-harness.cjs`（引导/窗口/量测/收尾/控制台错误兜底） | ✅ |
| 6 | 布局校验脚本过期断言（审查中实测发现） | 目录计数改相对基线、`data-sp="configure"`→`"edit"`、搜索分区改写为演示态诚实形态 + 新增分组收起断言 | ✅（**exit=0，85 场景**） |
| 追 1 | `store/store.go` 解耦（§一 剩下的最大 Go 文件） | 884 → **5 文件**（store 182 / sessions 301 / messages 304 / projects 50 / search 101） | ✅ |
| 追 2 | `shared/reduce.ts` 的 `reduce` 拆分 | 217 → **47 行** + 10 个分支函数（`reduceDelta`/`reduceToolCall`/…），小 case 留在 switch 里 | ✅ |
| 追 3 | `server/agent_catalog.go` 的 `dispatchAgentCatalog` 拆分 | 176 → **22 行** + 4 个域函数（agents/modules/tools/mcp），与 `dispatch_*.go` 同一套纪律 | ✅ |

### 8.1 websearch 提取：为什么前两次回退，第三次才成

前两次失败的根因是**把「看起来一样」当成「逐字一样」**：26 个适配器的前置段并不相同——`mistral_search` 中间夹了 tool 校验、`brightdata` 多一段 zone 校验、`jina` 多一段尾斜杠、`firecrawl`/`searxng` 只要地址不要 key、`duckduckgo` 没有前置、`exa` 分派 MCP/直连、`xcrawl` 多一个 `endpoint`。

| 尝试 | 手法 | 结果 |
|---|---|---|
| 第一版 | 正则整段替换 | 16 命中、6 变体不匹配、**2 个文件被改坏**（`brightdata.go`/`mistral_search.go` 语法错误）→ 回退 |
| 第二版 | 收窄到「key 校验 + base 兜底」 | 正则过严只命中 6 个；且上一轮 `git checkout` **没真正回退**，两次替换叠加把文件弄脏 → 回退 |
| 第三版 | 先**按内容分组**量出真实口径（20 个逐字相同），再精确字面量替换 + 命中数必须为 1 | 20 个全中，`+78 −140`，适配器测试全绿 |

**教训**：这种「大部分相同但不全同」的代码，正确顺序是**先量再改**——把每处前置段按内容分组，用测量结果决定提取边界，而不是拿一个理想化的样板去正则匹配。第三版只改了逐字相同的 20 个，7 个变体原样不动（它们各有各的理由）。

### 8.2 布局脚本骨架分离踩的坑

骨架参数最初叫 `ctx`，而场景里本来就有 `const ctx = await win.webContents.executeJavaScript(...)`（上下文指示器那段）→ 加载期 `SyntaxError: Identifier 'ctx' has already been declared`。**失败形态很坑**：错误发生在 `app.whenReady()` **之前**，所以 `finish()` 永远不会被调用 → **进程静默挂住、一行输出都没有**（Electron 主进程 stdout 在管道下本来就丢，看起来像卡在加载页面）。改名 `preview` 后通过。

### 8.3 拆分的验证手法（比 `go test` 更硬的一条）

- **Go**：`temp/goparse.cjs` + `temp/verify-units.cjs` —— 把 HEAD 的每个顶层声明逐个在当前文件集里找，要求**逐字节一致**。这轮靠它抓到两处静默损坏：
  1. 第一版解析器用「括号配平」定声明边界，而 `agentSchema` 是**跨行反引号原始字符串**里的 SQL —— 在 SQL 的括号处提前结束，正文被截断；
  2. 掩码版修好后，`agents.go` 的恢复脚本又把「掩码把 `//` 注释也变成空格」当成续行条件，多吃掉了注释与后随单元。
  修好解析器（掩码保留字符位置 + 每行结束状态）后，`verify-units.cjs` 报「HEAD 的全部单元逐字节一致」，唯一允许的差异是本轮故意改的 `syncCatalogSeeds`。
- **本次拆分的机械校验**（脚本自带断言，切错区间就报错退出，全部在写盘前拦下）：
  - Go `dispatch` 按行号切片后**拼回必须等于原 switch 主体**（去空行比对）；断言在这轮抓到 4 次我自己的行号误判（`hello` 块结尾是 `})` 不是 `}`、Go 的 `case` 子句**没有闭合花括号**、`ThreadBlock` 末尾是空行）；
  - TS/CSS 每个区间必须**起于列 0**（顶层声明或它的文档注释）、**末行不是空行也不是下一个 `case`**；
  - 新文件的 import 分组照仓库约定（标准库 / 第三方之间空行），`gofmt -l .` 必须为空。
- **CSS**：`temp/verify-css.cjs` 比对选择器序列（HEAD 1025 个 → 拆分后 1026 个，唯一差异是新增的 `.thread > .dispatch-card`），顺序与内容一致；渲染侧用仓库自带的 `check-preview-layout.cjs`（**85 个场景通过**）。

### 8.4 测试加载器的一个真缺陷（本次修）

`frontend/tests/ts-loader.mjs` 只对**带 `.ts` 后缀**的说明符短路，而 TS 源码里相对导入一律不带后缀（`./events`）。此前没暴露，是因为被测试的模块恰好都是叶子——type-only 导入会被转译擦除，压根不走解析。一旦出现「跨模块的运行时相对导入」（`ws/index.ts` → `./events`），`npm test` 就 `ERR_MODULE_NOT_FOUND`。已按 TS 规则补上 `.ts` → `.tsx` → `/index.ts` → `/index.tsx` 回退。

**顺带一个测试命令的坑**：`node --test tests/ws.test.mjs` **不会**激活加载器（`--import` 不传给子进程），只有 `npm test` 会。单跑某个测试文件时看到的失败是假象。

### 8.5 这一轮拆 `store.go` / `reduce` / `dispatchAgentCatalog` 踩的坑

全部是**脚本自带断言**在写盘前拦下的（没有一条是靠肉眼看 diff 发现的）：

| 坑 | 表现 | 断言 |
|---|---|---|
| PowerShell `>` 重定向写成 UTF-16 | `git show HEAD:... > temp/x.go` 出来是 UTF-16 + BOM，Go 报 `illegal character U+FFFD` | 改成脚本里 `execSync('git show ...')` 直接拿 UTF-8 字符串 |
| `temp/*.go` 被 `go build ./...` 捡走 | 临时源文件落在模块内 → 构建报错 | 不落 `.go` 文件；原始版本直接读 git |
| Go 的 `case` 子句**没有闭合花括号** | 按「找下一个 `}`」切域块，末行落到下一个 case 上 | 断言改成「末行必须是最后一条 `return` 语句」 |
| `switch method {` 属于 switch 而不是守卫段 | `get(93,99)` 把 `switch` 也搬进新函数 → `expected '}', found 'if'` | 断言「切出的行拼回必须等于原 switch 主体」 |
| 块体 case 的 `{` 在 **case 行末**（`case "delta": {`） | 断言「case 的下一行必须是 `{`」直接误报 | 断言改成「case 行以 `{` 结尾」 |
| 拆完的 case 顺序被打乱 | 提取的 case 排到了保留的 case 之后（switch 语义不变，但 diff 全是噪声） | 按**原 case 行号**交错重排——case 顺序不该因为提取而变 |
| import 推断两个假阳性 | ① `schema` 的 SQL 注释里写了 `llm.ToolCall` → 给 store.go 塞了用不到的 `llm`；② 别名 `crand` 与空白导入 `_` 被写成裸路径 | 扫描前剥掉注释与原始字符串；别名行照抄；排序按**导入路径**（gofmt 规则）而非整行文本 |

**验收纪律（每条都适用）**：拆文件是纯搬移 → `gofmt -l .` 空 + `go vet ./...` + `go build ./...` + `go test ./...` 全绿 + `npm test` **134** 绿 + `npx tsc --noEmit` 0 + `node scripts/check-boundaries.mjs` PASS + 布局校验 exit=0（85 场景）；**不改任何一行逻辑**，`git diff --stat` 应只体现文件增删（用 `git diff --color-moved` 核对）。

**不建议做的**：不要为拆分引入 barrel/re-export 层（`shared/store.ts` 的 3 行 re-export 是**保持调用方路径不变**的最小必要，不是层层转发）；不要把 Go 包再拆成子包（`internal/agent/turn` 这类子包会打断 `s.mu` 的封装与包内私有函数共享——同包多文件才是 Go 的正解）；不要为了减少行数去合并语义不同的函数（`clipStr` ×2 就是反例）；布局脚本只分**骨架 / 场景**这一层（引导、窗口、量测、收尾在 `lib/preview-harness.cjs`）——85 个场景本身是**一条线性断言流**，不要给它硬凑 85 个函数边界，那只是把 `try` 块换成函数，零行为收益。