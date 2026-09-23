# AGENTS.md — lxcode

> 本文件是本仓库的协作规范：任何 AI agent 或开发者在本仓库工作前，先读此文件。
> 2026-09-11 定名 **lxcode**（用户拍板，接续 LX 品牌线）；原名 myt-harness，全仓已重命名
> （module github.com/moyunteng/lxcode、目录、服务名、二进制、环境变量 LXCODE_*、协议身份）。

## 1. 项目定位

**lxcode 是用户自己的个人智能体：Go 后端 + React 客户端 + Electron 薄壳（已实现）。**

- 双定位：**编程助手**（读写代码、改文件、跑构建测试）+ **通用个人助理**（日常事务、检索、自动化）；
- 跑在用户本机（Windows 开发机为主，兼容类 Unix），单用户；
- 是 local-myt-agent（`../local-myt-agent`，设备端 Go agent）的**精神续作而非代码分支**：架构模式与实证坑直接继承（LLM 双格式客户端、工具风险分级、JSONL 会话、动态提示词），但为桌面场景重新设计——agent 内核是纯 Go 包，**不依赖 UI 框架与传输协议**；由 Go 服务装配，React 客户端通过 WS JSON-RPC 调用，Electron 不嵌入 Go 内核。

## 2. 架构基线（已定事实）

| 项 | 决策 |
|---|---|
| 形态 | **前后台分离**（2026-09-10 用户拍板，对齐 local-myt-agent）：后端 `lxcode --serve` 独立进程（WS JSON-RPC `127.0.0.1:7789/rpc` + 注册表 + 会话运行时 + 工具循环，`/health` 健康检查）；CLI / 桌面壳都是客户端 |
| 协议 | `internal/protocol`：WS JSON-RPC 2.0（帧/方法/事件单处定义，客户端服务端共享）；扩展 `todo.updated` 事件与 `ChatHistoryResult.Todos`；`chat.send` 可选参数 `effort`（minimal/low/medium/high，仅声明 reasoning 能力的模型生效）与 `approval`（auto/confirm/strict 工具执行三档，空 = confirm）；`chat.done` 与 `ChatHistoryResult` 携带 `context`（上下文占用测量，见 §2.2；未知时整键缺席）；端口 7789（与 local-myt-agent 的 7788 错开） |
| 内核 | `internal/agent`：纯 Go 包（typed Event + Emitter + Confirm），由 server 包装广播；客户端不得绕过 JSON-RPC 直接调用内核 |
| 客户端 | `internal/wsclient`：Backend 接口 + Dial（请求按 id 配对、事件 channel、断连 fast-fail、缓冲满丢最旧）；CLI 是第一个客户端，桌面壳复用同一协议 |
| 前端 | **React 19 + TypeScript + Vite + gsap**，零 UI 库（手写 CSS 设计 token，设计语言 agent-console-v3）；`AgentSource` 双实现：WSAgent（连 7789 真实后端）/ DemoAgent（纯前端演示，无后端也能全量跑 UI）；渲染纪律：打字机行级 memo + memo(Block) 稳定回调 + motionAllowed 动效门控（reduced-motion/测试开关） |
| 内核并发 | 当前一个活跃 Session，切换多份持久历史；多客户端共享当前会话，不支持多任务同时生成。多活跃会话、调速器与 worktree 属未来规划（§2.1），**不做每会话进程/微服务** |
| LLM | 双 wire 格式：OpenAI chat completions + Anthropic Messages（`internal/llm`，从 local-myt-agent 整包继承——含 ChatAuto 分流策略：anthropic 恒流式，openai 带工具走非流式回放，依据是真机端点实测 openai 流式丢 tool_calls） |
| 工具 | `internal/tools` 注册表 + 风险分级：低危自动执行，高危确认门 |
| 会话 | **SQLite**（modernc.org/sqlite 纯 Go，WAL；2026-12 用户拍板，替换初版 JSONL——为 compaction/语义记忆/多会话并发铺路），重启恢复最近会话，`/new` `/resume` 切换；**项目归属即工作目录**（sessions.workspace → 项目根：工具相对路径、bash 默认目录、系统提示词全对齐，经 ctx 注入 tools 层——`tools.WithWorkDir`） |
| 配置 | `internal/config` 模型注册表（models.json，原子写；default/vision 角色绑定；**30s 热加载** + model.changed 广播） |
| 服务化 | **Windows SCM 服务**（`scripts/service/{install,update,uninstall}.ps1`；开机自启 + 崩溃自动重启；`--probe` 验收；布局 `%ProgramData%\lxcode\{bin,config,sessions,logs}`）；服务形态日志落文件（16MB 轮转 ×3） |
| 桌面壳 | **Electron + Go sidecar（2026-09-16 用户拍板，推翻 09-10 的 Tauri 2 初选，决策记录见 §2.1；打包定案仅 Windows，2026-12）**；后端可先于壳长期独立运行，壳是薄客户端（窗口/托盘/渲染层直连 7789） |
| Go 直接依赖 | gorilla/websocket（WS）、golang.org/x/sys（平台接口）、modernc.org/sqlite（纯 Go 存储）；传递依赖以 go.mod 为准 |

### 2.1 语言栈与桌面壳决策记录（2026-09-16 拍板）

- **桌面壳 = Electron + Go sidecar**（推翻 2026-09-10 的 Tauri 2 初选；`frontend/src-tauri` 骨架与 Tauri 构建脚本已于 2026-09-16 清理）。翻案理由：应用内嵌浏览器（人用面板）进入路线图，Electron 的 webContents（同窗口多视图 / session 隔离 / 请求拦截 / 内建 CDP）是唯一不将就的深度；Tauri/Wails 在 Windows 同用系统 WebView2（也是 Chromium），渲染无增益，省的只是占用（内存 ~100-200MB / 磁盘 ~100MB / 冷启动 +0.5s）——用占用换控制权与生态。Electron 开销全在占用层，不在计算热路径（重活在 Go 内核；渲染器=你正在开发的同一个 Chromium 页面）。
- **Rust 不重写内核**：后端负载 90%+ 是等 LLM/等子进程，唯一 CPU 密集点（search）已由"exec 外部二进制"覆盖。**FFI/cgo 严禁入仓**（链接地狱 / panic 边界 / 跨语言调试成本远超收益）；真要第二语言模块，须同时满足三门槛才升 sidecar 服务：占热路径 >30% / 自包含无共享状态 / Go 生态无等效品。
- **Rust 的正确进入方式 = 进程边界**：ripgrep 这类外部 Rust 二进制直接接 tools 注册表（"Go 主刀，Rust 武器库"）；仓库零 Rust 工具链。依赖基线：原「只有两个」（gorilla/websocket + x/sys）——**2026-12 用户拍板存储切 SQLite 追加第三个 `modernc.org/sqlite`（纯 Go 无 CGO，符合 FFI 禁令），此后新增依赖仍须从严评估**。
- **未来规划：多活跃会话 = 单进程 + 调速器**（尚未实现，容量未基准测试）：拟采用供应商预算、工具并发池、公平队列 + 空闲状态落 SQLite + Git worktree 每任务隔离。需先定义 sessionId 与排队事件的协议契约；不得把当前侧栏多历史会话当成并行执行能力。
- **Electron 侧工程纪律**：单窗口 + WebContentsView 做浏览器面板（不开多 BrowserWindow）；contextIsolation 开、renderer 无 node 集成；主进程只做窗口/托盘/sidecar 生命周期，不放业务；后端仍是 SCM 服务优先（壳只是客户端，连不上给启动指引）。窗口 `frame:false`（无系统标题栏，Topbar 自绘拖拽区+窗口控制，经 preload 的 `window.__LX__` IPC 桥）；应用图标 = `shell/build/icon.png`（electron-builder 自动转 ico）。
- **壳打包定案（2026-12 调研+实测，仅 Windows）**：electron-builder + NSIS（one-click、per-user、免管理员）+ **自建 zip 更新机制**（2026-12 用户拍板，替代原定的 electron-updater——重跑完整安装器的更新路径被否；服务端 = 纯静态目录 `manifest.json + update-<version>.zip`，zip 内路径=安装目录相对路径，只含 `resources/app.asar` 与 `resources/bin/lxcode.exe`；两级更新：后端热替换（壳不退）、asar 冷替换（退出时换）；Electron/Chromium 升级走全量安装包不进 zip；客户端更新器未实现，产物契约已定）+ 壳主进程 **esbuild 单入口直构**（不用 vite-plugin-electron：薄壳无主进程 HMR 价值，且保持 frontend vite 配置与 Electron 零耦合、浏览器模式零条件分支）；Go 后端二进制走 extraResources（asar 归档内不能 spawn 二进制）。本机实测：打包 44~148s（受后台负载影响），安装包 ~87-95MB、更新包 ~12MB。sidecar 生命周期纪律：单实例锁 → 先探测 7789（SCM 服务或旧实例在跑则直连，不 spawn）→ 离线才拉起 bundled exe，且**必须显式传 `--config`/`--sessions` 指向 userData**（否则后端配置解析顺序会落到 `%ProgramData%` 安装形态配置，两形态数据串台）→ 优雅退出 before-quit kill；崩溃兜底 = Electron 主进程 Windows Job Object（`KILL_ON_JOB_CLOSE`，壳被强杀也不留孤儿后端）。版本兼容用协议 hello 的 Version 握手。范围：壳仅 Windows（2026-12 用户拍板；Linux/macOS 不做壳，Linux 上后端二进制独立跑 + CLI/浏览器即可）。
- **版本号机制（2026-12）**：唯一版本源 = `shell/package.json` 的 `version`（electron-builder 原生读它出安装包名）；`scripts/build.mjs` 构建时经 `-ldflags "-X main.version=<版本>"` 烙进 Go 二进制（`lxcode --version` 可查；dev.mjs 无烙印显示 `dev`）；manifest.json 用同一版本——安装包/二进制/更新清单三方同源。发版流程 = 改 shell/package.json version → build → 产物目录全量上传。

### 2.2 上下文计账与压缩（compaction）（2026-09-22：P1~P5 + 子会话头部保护已落地）

**计账（P1，`internal/agent/context_usage.go`）**：`agent.ContextUsage` 是上下文占用的唯一事实源（压缩触发与 UI 指示器共用它，不各算一遍）：

- **used 优先取 provider 真实用量**（`llm.ChatResult.PromptTokens`——用户真实端点实测会回报），拿不到才回落固定密度估算（`字节数/4 + 每块 4 + 每消息 4`，DSH token-meter 同款）。**按字节算对中文是低估的**（一个汉字 3 字节 ≈ 0.75 token），所以估算值只服务「端点不回报 usage」的回落与分类拆分，不要拿它做精确预算；
- **分类拆分按 used 归一**（`anchoredTo`）——分类之和恒等于 used。不归一的后果是同一屏两个数字互相矛盾（环形来自真实用量、占比条来自估算）；
- **只有主轮写这个值**（`streamRound` 的 `dispatchID == ""`）——子上下文有自己的窗口，写进主指示器就是错的；
- **新会话/切会话清零**——沿用上一会话的占用会误导压力判定；`Snapshot.Context` 零值 = 未知，wire 上整键缺席，前端显示中性态「—」而不是编一个数（`ContextIndicator` 的冷态断言钉住这点）；
- wire：`chat.done`（仅主轮）与 `ChatHistoryResult` 的 `context` 键。

**工具配对不变量（P2，`internal/agent/toolpair.go`）**：`AnalyzeToolPairing` 扫一遍历史得出每个切点的平衡性——`assistant` 带 N 个 tool_call 则游标 +N，`tool` 结果则 −1，**游标为 0 的切点才是安全切割点**。压缩选区间（P3）与「取消时补配对」（P5）共用这一份判定——判定漂移的代价是静默产生畸形历史（严格端点会 400：assistant 的 tool_calls 必须有配对 tool 消息）。三条纪律：

- **畸形数据不抛异常**（我们的历史来自 SQLite，残缺记录是既成事实，加载路径抛异常会让老会话直接打不开）：游标钳到 0，异常另记诊断（`Unpaired` 未等到结果的调用 id / `Orphans` 无前置调用的结果下标）；
- **两条判定刻意分开**：`Cuts` 用计数（对端点改写 id 鲁棒——它只回答「能不能在这里切」），`Unpaired` 用 id（精确回答「哪一个调用没等到结果」）。二者在 id 错配时会分叉，这不是 bug；
- `NearestBalancedAtOrBefore` 是选区间用的回退（保留预算边界往前退到最近的平衡切点）；
- **取消与失败路径必须补齐配对**（P5，`Session.sinkSkippedToolResults`）：`runTools` 的两个取消出口（循环中发现 ctx 已断 / 确认门等待期间取消）与 `runTurn` 的流式失败分支，都要给**不会执行**的调用补一条合成 tool 结果（「已取消，未执行」/「本轮生成失败」）——与白名单拒绝、strict 拒绝、用户拒绝三处是同款写法、同一个理由。不补的代价不是"不好看"：严格端点 400 拒收整轮，而且游标在缺配对处之后再不平衡 → **压缩切点永久卡在它之前，那个会话从此再也压不动**（`TestPairingRepairUnfreezesCompaction` 钉住这条；子会话走同一条路径，见 §2.3）。

**压缩（P3/P4，`internal/agent/compaction.go` + `compaction_prompt.go`）**：把历史的一段替换成一份摘要检查点。对齐 DSH compaction-basic：

- **触发三条路**：① 轮与轮之间（`runTurn` 的 `round > 0`）按上一次请求的**真实 prompt_tokens** 判压力（阈值 0.8×窗口、保留尾部 0.16×窗口——那时它就是精确值，不用估算）；② 端点报超长（`llm.IsContextOverflow`：4xx + 特征串，字符串匹配关在 llm 层）时强制压一次（保留预算归零）再重试**同一轮**，只重试一次；③ 手动 `chat.compact`（`Session.Compact`）不受阈值约束，空闲才允许（busy → `ErrBusy`）；
- **窗口未知（模型没配 `context_window`）时不压**——算不出阈值就不猜（单测钉住）；
- **选区间**：从尾部往前累加到保留预算，再回退到最近的**配对平衡切点**（P2 的不变量）；区间起点通常为 0（主会话压缩前缀），**子会话保护头部任务说明书时从 1 开始**（`Session.protectHead`，见 §2.3）；
- **摘要调用**：指令作为**最后一条 user 消息**追加在重放历史之后（复用 KV 缓存），八段结构 + 工具声明照发；摘要是**一段文本**，落回历史时包成一条 **user 消息**（`<compacted-summary>` + 前言）——不用 system：anthropic 适配器会把所有 system 消息提到顶层，中途插会语义错位；
- **缩水检查**：比的是**被压段**与摘要（保留尾部两边都在，抵消）——不是整段历史。摘要不小于被压段就是「没有收益」：回 `compacted=false + reason`（`ErrNothingToCompact` 包装人话原因），**不是错误码**；
- **fail-closed**：摘要失败、缩水不通过、落库失败，都不改历史（落库失败还把内存回滚）；
- **落库（影子区间，用户拍板对齐 DSH）**：检查点行记 `shadowed_seqs`（影子掉的 seq 集合，**权威**）+ `shadow_start_seq/shadow_end_seq`（历史位置边界，只写不读——回放只认 `shadowed_seqs`）；被影子的原文**不删**（`Search` 照旧搜全量日志），只是回放（`Load`/`Latest`）跳过它们；
- **`AppendCheckpoint(skip, count)` 说的是「当前历史里从第 skip 条起的 count 条」**（不是 seq 区间）：`skip/count` 就是 agent 选出的可压区间 `[skip, skip+count)`——主会话 skip=0（前缀），子会话 skip=1（跳过头部任务说明书）。区间越界时报错（那说明落盘落后于内存，写错的影子区间会让回放丢掉不该丢的历史）；
- **历史顺序按「检查点落在它影子段原本占据的位置上、其余按 seq 升序」重建**（检查点行是追加在末尾的，但它顶替被影子段在历史里的位置 = DSH 的 surface position）：插入锚点 = 影子集合里的最小 seq，无影子集合时回落自身 seq（落尾）；多个检查点按锚点升序（**不是自身 seq**——两次压缩可以「后写的替换更靠前的一段」，按 seq 排会把两份摘要的先后搞反）。skip=0 时锚点就是库内最小 seq → 检查点仍落第一位，**与旧的「一律排最前」逐字节一致**（老库里的检查点行影子集合都是前缀，`TestCheckpointShadowsPrefix` 是这条兼容性的钉子）；
- **落库口径**：`Load`/`Latest`/`List` 的消息数都按当前历史算（`List` 在 Go 侧复用同一套存活判定——SQL 表达不了 seq 集合）；
- **压缩后必须更新占用测量**（锚定算术：新占用 = 旧真实占用 − 被压段估算 + 检查点估算）——不更新的话指示器停在压缩前的数字（真链路实测抓到过）；
- wire：`chat.compact`（方法，参数 `agent` 可选）+ `chat.compacted` 事件（before/after/shadowed/summary/manual）+ `ChatHistoryResult.Checkpoints`（检查点下标，前端渲染「已压缩历史」块而不是用户气泡）；前端入口 = 输入区指示器里的「立即压缩」+ `/compact` 斜杠命令。

**剩余（未做）**：① P3 的确定性裁剪（工具结果写入时已截到 8KB，将来加裁剪接在选区间之前，顺序对齐 DSH）。参考实现 = DSH 的 `dsh-compaction`（引擎接缝 + 配对不变量 + 检查点溯源）/`dsh-compaction-basic`（阈值与选区间策略）/`dsh-compaction-tool-result-pruner`（无模型裁剪）/`dsh-token-meter`（计账），路径 `C:\Users\xzy\AppData\Local\Programs\lx-dsh\resources\dsh\node_modules\@deepseek-ai\`（打包形态；`dsh-compaction` 带 TS 源码 `src/`）。

### 2.3 子会话：子 Agent = 独立会话（2026-09-22 用户拍板）

**一句话**：派发给子 Agent 的任务**不是**内存里的一段临时上下文，而是**一个独立会话**——自己的 `sessions` 行 + 自己的 `messages` 历史 + 自己的压缩检查点。于是进度天然可续（历史在库里，续跑附着同一个 id 接着跑）、压缩同款（子会话就是会话）、子过程可回放/可对账。

**存储**（`sessions` 表加三列，幂等 ALTER）：`parent_id`（派发方；空 = 顶层会话）、`agent_id`（它是哪个 Agent——续跑时按同一套四层组合组装）、`dispatch_id`（哪次调度开的）。`Store.CreateChild(parentID, agentID, dispatchID)` 用 `INSERT ... SELECT ... FROM sessions WHERE id = 父` **在 SQL 里继承父的 workspace**（子会话的工具相对路径与 bash 默认目录必须与父一致，agent 层不需要记住 workspace id）；父不存在时插入 0 行 → 报错（不开孤儿）。

**三条硬纪律**：
- **`List()`/`Latest()` 只认 `parent_id = ''`**——子会话不进侧栏、不参与「重启恢复最近会话」；漏了这条，重启会把用户恢复到某个子 Agent 的会话上，侧栏也会被子会话淹掉；
- **归档级联**：子会话不进侧栏、用户没法单独操作，父归档时一并归档（否则库里长期堆积孤儿）；恢复父时子会话一并恢复；
- **`ChildrenOf(parent)`** 按父查子（对账/将来的子会话视图）。

**运行时**（`internal/agent/dispatch.go`）：
- `runDispatch` = 解析目标 → 校验有效委派名单 → `openChildSession`（新建或按 `call.Session` 续跑）→ 包一层 emitter → `child.SendWait(ctx, task, WithAgent, WithApproval(取严后的审批))` → 取子会话最后一条有正文的 assistant 消息作为结论回填；
- **子会话是一个真 `*agent.Session`**（自己的 history/id/st/context），所以**压缩、溢出兜底、确认门、工具循环全部免费继承**（不再有子循环特例：`dispatchMaxRounds` 与手写子循环已删除）；
- **`Session.AttachTo(st, id)`**：按 id 精确附着（子会话新建后历史为空；续跑时历史在库里）——与 `EnablePersistence`（走 Latest 恢复）分工明确；
- **`Session.SendWait(ctx, ...)`**：跑一轮并等它结束（派发要等子会话给出结论）；ctx 取消 → `child.Cancel()` 并等它真正收尾；
- **事件归属**（`childEmitter`）：Delta/ToolCall/ToolResult/TurnDone/Compacted 打上 `dispatch_id` 归属进卡；**BusyEvent 不上抛**（子会话的忙闲不是主会话的）、**TodoUpdatedEvent 不上抛**（清单归子会话自己）、**SessionStartedEvent 不上抛**（子会话不进侧栏）、**TurnErrorEvent 转成 DispatchEndEvent**（卡内呈现，不跑到主时间线当独立错误）；
- **确认门代理**（`SetConfirmProxy`）：子会话的确认请求交给父会话裁决并打上 dispatch_id——全应用只有"同时一个挂起确认"这条不变式，子会话自己持 pending 的话服务端的 `tool.confirm` 找不到它（会话会卡在 busy）；
- **无存储时退化成内存子会话**（`st == nil`）：仍是一个独立会话（自己的历史与压缩），只是不落库、不能续跑——纯内存模式/未挂 store 的调用方照旧能派发。

**每会话注入态（`internal/tools/sessionstate.go`）**：`todo` 的清单写回口与 `read_skill` 的技能目录**经 ctx 注入**（`tools.WithTodoSink` / `WithSkillSource`），不再挂注册表全局——注册表是进程级单例，而这两者都是**会话级**状态；子 Agent 变独立会话后，注册表级全局态会让父子互相踩（子会话一建就把父的 sink 顶掉、子会话的技能目录污染父会话——原来的 save/restore hack 就是被这件事逼出来的补丁）。与 `tools.WithWorkDir` 同一套机制与理由。

**续跑（S4）**：`agent.dispatch` 加可选 `session` 参数（续跑既有子会话）；工具结果里回带 `[子会话 id: …]`，主 Agent 下一轮就能显式接着它跑（真链路实测：主 Agent 自发这么做了——它读到工具结果里的子会话 id 后，在下一轮把该 id 填进 `session` 续跑）。

**协议**：`chat.dispatchStart/End` 带 `session_id`（子会话 id 上卡，前端显示 + 续跑依据）；`chat.compacted` 带 `dispatch_id`（子会话自己的压缩归属进卡内，不插主时间线）；前端 `DispatchCard` 显示子会话 id 徽标，`reduceSub` 处理 `compacted`。

**为什么这个设计省事**：子会话是会话 → 压缩/检查点/影子区间**零特例**（`runCompaction` 只要一个有 st/id/history 的 Session）；子会话的 system 提示词同样每轮现组装（不在历史里），所以**不需要给子上下文加 system 头部保护特例**。注意区分：**任务消息**（`history[0]`，**user** 角色）是另一回事——它确实在历史里，所以需要显式的头部保护：`openChildSession` 给子会话置 `protectHead`（压缩区间起点从 1 开始），任务说明书永远留在 `history[0]`，摘要落在它之后；store 侧配合支持中间段影子（见 §2.2）。

## 3. 工具面（内置 9 个 + 目录动态注入）

| 工具 | 风险 | 说明 |
|---|---|---|
| `read_file` | 低危 | 按行输出（`行号→` 前缀），offset/limit 分页，256KB 上限，二进制拒绝 |
| `search` | 低危 | 纯 Go RE2 检索（files/content/count 三模式），不经过 shell |
| `session_search` | 低危 | 搜历史会话内容（注入接线：格式归 store 包） |
| `read_skill` | 低危 | 读技能模块全文（提示词只注入技能索引——渐进披露） |
| `edit` | 低危 | **精确替换**：old_string 唯一匹配硬校验（0/>1 报错自解释），原子写 |
| `write_file` | 高危 | 全量覆盖：覆盖已有文件需确认 + 缩水守卫（<50% 告警） |
| `bash` | 高危 | 超时 60s/上限 300s、输出 32KB、stdin ≤64KB；**Windows shell 选择见 §5 坑** |
| `todo` | 低危 | 任务清单全量写入（active 唯一性硬校验）；会话持有状态 + TodoUpdated 事件 |
| `agent.dispatch` | 低危 | 主 Agent 唯一工具：把任务派给名单里的子 Agent（**子 Agent = 独立会话**，见 §2.3；深度恒 1） |

**自定义工具（M4-1 执行面，2026-09-21）**：目录里 `source=binary` 的条目由 server 在启动与每次 `catalog.tools.*` 变更后经 `syncDynamicTools()` 注册进注册表（`tools.Registry.SetDynamic` 整体替换动态段；内置段不动，同名跳过并记日志）。执行语义：

- **不经过 shell**：`command` 模板按空白切分成 argv，`{param}` 占位替换为参数值——值原样作为单个 argv（含空格不切分），引号/分号/`$` 全是普通字符，注入面在结构上消失；
- 参数只支持标量（string/number/bool）；缺参/未知参/非标量/null 都自解释报错回填模型，不 spawn；
- cwd = 会话工作目录（`tools.WorkDir`）；超时 60s、输出 32KB 截断（对齐 bash）；退出码/超时/启动失败以文本回填；
- `Mutates=true`（strict 只读模式拒绝）；`risk=high` 走确认门，确认文本含**渲染后的完整命令**；
- 未配置 `command` 的 binary 条目（种子里 `browser` 是「声明了没实现」的形态）跳过并记日志，不影响其余目录；目录页对它标**「未配置」**（amber pill），外部二进制条目**可编辑**（内置只读）——用户勾进白名单前就能看见、也能自己补 command；
- 白名单勾了但注册表里没有的工具，会在该 Agent 的系统提示词里被点名「当前不可用」——不让模型去调一个不存在的工具。
- **种子同步（`initAgents` 每次 Open 都跑）**：种子只在库空时整套注入会有一个致命后果——**代码修好了种子，老库永远吃不到**（用户报告过「给了 ripgrep，模型答注册表没有」）。所以库非空时跑 `syncCatalogSeeds`：按 id 同步种子行（缺失插入、`custom=0` 按代码更新全字段），**`custom=1`（用户自建或改过的行）一律不碰**，**不删除**（避免动到白名单可能引用的行）。Agent 名单走**更严**的同步（只插缺失、绝不改已有行），见 §8「种子同步」。

- **edit 为何低危**：编程 agent 的主编辑通道，确认门会让它不可用；破坏面受 old_string 唯一匹配约束 + 原子写 + 版本控制兜底（与 write_file 的全量覆盖破坏面不同类）。
- **权限模式三档**（chat.send 的 approval 参数，随消息携带）：`confirm`（默认）= 低危自动 + 高危确认；`auto` = 高危也自动执行（仅隔离环境）；`strict` = 只读——变更类工具（`Def.Mutates`：edit/write_file/bash，与风险等级正交）直接拒绝、错误回填模型。风险等级管"要不要确认"，Mutates 管"只读模式禁不禁"——edit 低危但变更文件，strict 下必须拒。
- 参数坏 JSON 先走保守修复（`internal/jsonrepair`：裸换行/尾逗号/单引号/截断补括号），修复成功注明——弱模型坏参数是高频失败形态。
- **工具调用参数必须在写边界就合法**（2026-09-23 线上事故，见 §5 坑 12）：三道防线——① `agent.sanitizeToolCallArgs` 在 `s.append` 前清洗（正常轮次与流失败保留 partial 两条路径都走）；② `llm.repairToolArgsForWire` 组装请求时兜底（保守修复，修不动发 `{}`，**绝不报错**）；③ `tools.Execute` 执行前再试一次。三者共用 `jsonrepair` 一份实现。
- **工具 id 必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**（OpenAI 与 Anthropic 的同一条约束）：内置的 `agent.dispatch` **带点号，违反这条**，宽松网关过去放过、严格的会 400 拒收整轮（见 §5 坑 13）。新增内置工具或导入目录条目时不要用点号。

## 4. 开发约定

- Go，module `github.com/moyunteng/lxcode`；提交前 `go build ./...` + `go vet ./...` + `gofmt -l .`（输出为空）+ `go test ./...` 全绿；
- 注释解释"为什么"，用中文；提交信息 conventional commits（`feat(工具): …`）；
- 测试就近放包内（`_test.go` 与源码同目录——Go 项目按包放测试是正确布局）；
- 后端：`lxcode --serve`（配置默认 `./config/models.json`，`--config` / `LXCODE_CONFIG` 覆盖；会话目录默认 `<config 上级>/sessions`；监听 `--addr`，默认 `127.0.0.1:7789`）；
- CLI 客户端：`lxcode`（连 `--backend`，默认取 `--addr`；连接失败会给启动指引）；
- 本地冒烟：起后端 `lxcode --serve --config config/local.json --sessions temp/smoke-sessions`（config/local.json gitignored，含 key），然后 `node temp/smoke.mjs "消息"`（端到端）或 `node temp/smoke-confirm.mjs`（确认门）；
- 壳开发：`node scripts/dev.mjs --electron`（或 frontend 下 `npm run dev:electron`，或仓库根 `./dev.sh`——无参默认壳模式，bash 薄包装）——Go + 壳 TS（esbuild，秒级）→ 后端 → vite → 自动拉起 Electron 连 dev URL；纯浏览器模式 `node scripts/dev.mjs` 不变（首跑需 shell/ 与 frontend/ 各 `npm install` 一次）；
- 壳打包：`node scripts/build.mjs`（或 `./build.sh`）——Go → 渲染层 → stage 进 shell/ → electron-builder 出 NSIS（shell/release/，one-click per-user，Go 后端在 extraResources；无原生模块故 npmRebuild:false 省 rebuild 开销）；
- 系统提示词：工具清单从注册表动态生成（`agent.BuildSystemPrompt` / `ComposeSystemPrompt`）——内置工具用 `systemPromptTools` 的手写摘要，目录里的动态工具回落 `Def.Description` 首行 + 风险说明（**没有回落 = 自定义工具被静默漏掉**，模型不知道它存在）。两条测试钉住：`TestSystemPromptListsAllTools`（清单与注册表不漂移）+ `TestBuiltinToolsHaveCuratedLine`（内置工具不许落到回落上——那等于丢了风险等级表述）；
- **项目守则注入（2026-09-21）**：会话所属项目的根 `AGENTS.md` 每轮组装提示词时现读并注入（`project.LoadInstructions` → `Session.SetProjectDocs` → `projectDocsSection`）。规则：**只做项目级**（不向上找父目录、不读全局；未分组会话 `workDir` 为空 → 不注入，避免把后端进程目录的守则串进无关对话）；**优先级低于 Agent 自身**（段落在四层组合之后、环境与工具清单之前，并显式声明「与用户当前指令冲突时以用户为准」）；读不到/超大只记 `Note` 不打断生成；源上限 1MB、注入上限 32KB（超限截断并注明）。子 Agent（dispatch）与主会话同一 `workDir`，自动拿到同一份守则；
- **项目守则的 UI 入口（同上一并落地）**：`project.instructions.get/save` 两个协议方法——**只按项目 id 寻址**（客户端不传路径，服务端从项目根解析 + 固定文件名 `AGENTS.md`，越权面为零）；写入走 `internal/atomicfile`（同目录临时文件 + fsync + rename，Windows 的 rename 不覆盖语义走备份回退——`tools` 的 write_file/edit 与这里共用同一份实现，**不许复制第二份**）；前端入口在**侧栏项目行的「守则」按钮**（hover 出现）→ 弹窗编辑器（显示项目根 AGENTS.md 路径 + 已存在/新建状态 + 内容编辑），设置面板不再放「自定义指令」那张全局空头支票；
- 协议改动跑 `internal/protocol` 帧契约测试（字段改名不编译报错、只静默丢字段——测试钉住载荷形状）；
- **前后端分离边界（静态守卫，CI 同款）**：`node scripts/check-boundaries.mjs`（TS AST 检查 frontend/src：无 Node/Electron API、网络通信只在 `agent/ws`、`WSAgent` 只许 `agent/index.ts` 工厂引用、`__LX__` 宿主桥只在 Topbar/AddProjectDialog）+ `go test ./internal/architecture`（go/ast 检查 Go 侧：后端不 import frontend/shell；agent 不 import store/server/protocol；store 不 import agent/server/protocol）。改完跑 `node --test scripts/check-boundaries.test.mjs` 验证守卫自身。前端纯函数测试 `cd frontend && npm test`（node:test + 就地 TS 转译，无构建产物）；
- **栈版本自检（排查协议类诡异 bug 的第一步）**：`node scripts/check-stack.mjs`——比对 `bin/lxcode.exe` 内嵌的 buildvcs 提交与当前 HEAD，**且只把 Go 侧改动（`*.go` / `go.mod` / `go.sum`）算作落后**（纯前端提交改了 commit 但不改后端行为，不算落后——否则会误导人白重启栈）；落后则退出码 1 并列出改过的 Go 文件（改完跑 `node --test scripts/check-stack.test.mjs` 验证它自身）。判定用 Go 构建元数据而非字节搜索：链接器会去重字符串，字节搜索连正对照都能搜不到（2026-09-21 实测）。`dev.mjs` 启动时也会打印后端编译提交；
- **分层规则**：`sessiondata`（中立业务类型）← `store`/`agent`；`agent.Persistence` 是消费者定义的最小接口（agent 不见 SQL/连接/事务）；`project` 包持目录校验与 git init 业务（server 只是协议转发）；设置面板依赖 `ModelAdminSource` 能力接口而非具体 WSAgent——UI 永远不知道数据来自 WS 还是 Demo；`jsonrepair`（保守修复模型给的坏 JSON 参数）是只依赖标准库的叶子包，`tools`/`agent`/`llm` 三方共用同一份实现（tools 依赖 llm，所以实现不能放 tools 里被 llm 反向引用）。

## 5. 已知坑（改代码前先看）

1. **Windows 的 bash.exe 是 WSL stub**：`LookPath("bash.exe")` 在装了 Git 的机器上也先命中 `C:\Windows\System32\bash.exe`——没装 WSL 发行版时它不执行命令、只打 UTF-16 安装提示。`selectShell` 的候选顺序：sh.exe → 不在系统目录的 bash.exe → 常见 Git Bash 路径 → cmd。
2. **Windows 的 `os.Rename` 不覆盖已存在文件**：原子写回退必须是"目标先改名备着"（`renameViaBackup`），绝不能"先删再改名"。
3. **会话存储已由 JSONL 切为 SQLite**：不再按文件名/mtime 恢复；顺序依据数据库 updated_at 与稳定排序。WAL + synchronous=NORMAL 保持一致性，但不保证断电后最近提交不丢。
4. **race detector 在本机需要 CGO**（无 gcc）：`go test -race` 不可用，并发正确性靠代码评审 + 事件互斥纪律（emit 持 REPL.mu，Session 状态持 s.mu）。
5. **测试假流的中断契约**：error 事件须携带已生成部分（`Result`）——真实客户端（`llm.emitFinal`）如此，假流不带上会丢 partial（TestCancelPreservesPartial 踩过）。
6. **手写 models.json 测试必须带 `"version": 1`**：Load 校验磁盘格式版本，缺了整份拒绝且只记日志（表现为"热加载广播没来"）。
7. **PowerShell 脚本必须 UTF-8 带 BOM**：PS 5.1 对无 BOM 文件按 ANSI 读，中文注释乱码会**破坏语法**（ParseFile 实证）；而 Go/JSON 恰恰相反（BOM 会炸 json.Unmarshal）——两类文件的编码策略相反，别搞混。
8. **http.Shutdown 不打断 WS 长连接**：服务优雅停机必须先 `closeAllClients()` 再 Shutdown（local-myt-agent 依赖 docker kill 兜底，SCM 等不了）。
9. **vite 产物不能直接 file:// 加载**：`<script type="module" crossorigin>` 在不透明源（file:// 的 origin 是 null）下被 CORS 拒绝——React 不挂载、页面空白且**无任何报错**（did-fail-load 只管主帧导航，资源级失败静默）。壳产线用 `app://` 特权协议从 asar 提供渲染层（`shell/src/main.ts` 的 protocol.handle）。
10. **从被 Job Object 包住的宿主拉起 Electron 时必须 `--no-sandbox`**：Chromium 子进程沙箱与外层 Job Object 冲突，GPU 子进程 STATUS_BREAKPOINT（0x80000003）崩溃循环直至整个应用 FATAL（实测：DSH 后台 job 里拉起必崩，交互式启动正常）。本应用渲染层零远程内容，安全面可接受；引入远程内容渲染前必须重新评估。
11. **后端二进制不会热重载——前端热的、后端可能是几天前的**：dev 栈只在 `dev.mjs` 启动那一刻编译一次 Go 后端，之后 vite 热重载前端、后端进程纹丝不动。**症状是「前端诡异 bug」**：协议新增字段（如 `ConfirmRequest.dispatch_id`）在旧后端里不存在，于是新前端收到的事件缺字段，表现为子 Agent 的确认卡跑到外层时间线、卡片永远「执行中…」，而四层映射代码全都是对的（2026-09-21 实测事故，排查代价极大）。**先跑 `node scripts/check-stack.mjs`**（比较二进制内嵌 buildvcs 提交与 HEAD）再动前端代码；处置 = 重启 dev 栈（Windows 下运行中的 exe 被锁，必须先停栈才能重新 `go build`）。
12. **历史里一条参数非法的 tool call 会让会话永久发不出请求**（2026-09-23 线上事故，排查代价极大）：模型输出被 max_tokens 截断时 `arguments` 是半截 JSON，旧实现把它原样写进历史；此后**每一次**请求都在 anthropic 适配器组装阶段硬失败（`工具 X 的 arguments 不是合法 JSON: …`），用户连发三条消息全部无响应，只能新开会话。**症状**：那条报错的 100 字节前缀与历史里某条 tool call 的参数逐字节相同（用只读探针把 `messages.tool_calls` 抠出来比对即可定性）。**处置**：写边界清洗 + 读侧兜底（见 §3）。**教训**：畸形历史条目要么在写边界拦住、要么在读侧兜底，"硬校验 + 无修复路径"会把单个坏数据放大成会话级故障（与坑 5 的配对不变量同类）。
13. **工具名里的点号会被严格网关 400 拒收**（2026-09-23 实测）：OpenAI 与 Anthropic 都把工具名约束为 `^[a-zA-Z0-9_-]{1,64}$`，内置的 `agent.dispatch` 带点号**违反这条**；宽松网关（旧的 LiteLLM 配置等）过去放过，严格化之后即 `Invalid 'tools[0].name': string does not match pattern` + `No fallback model group found`，**每一个带工具的主 Agent 轮次全部失败**（子 Agent 白名单无点号，仍可用——这也是一条应急旁路）。判定方法：拿同一端点直发两次最小请求（`read_file` 与 `agent.dispatch`）对比状态码。新增工具/目录条目一律避开点号。

## 6. Windows 服务运维（对齐参考项目的部署形态）

- **布局**（安装形态固定根，= 参考项目的 `/mmc/myt-agent/`）：
  `%ProgramData%\lxcode\`：`bin\lxcode.exe`（二进制）、`config\models.json`（配置）、`sessions\`（会话）、`logs\lxcode.log`（日志，16MB 轮转 ×3）。
- **配置解析顺序**（内置了参考项目包装器的语义，免包装器）：`--config` > `LXCODE_CONFIG` > `.\config\models.json`（存在时，开发形态）> `%ProgramData%\lxcode\config\models.json`（安装形态）。
- **安装**（管理员 PowerShell，在仓库根）：`scripts\service\install.ps1 [-ExePath .\lxcode.exe] [-ConfigPath .\config\local.json]`——端口预检 → 布置文件 → `sc create`（start=auto + 崩溃自动重启 5s/5s/60s）→ `--probe` 验收 → 失败自动回退（停服务 + 删除）。
- **升级**：`scripts\service\update.ps1 -File <新二进制> [-Sha256 <hex>]`——停 → 校验 → 备份 → 替换 → 起 → 验收 → 失败自动回滚到 `.bak`。
- **卸载**：`scripts\service\uninstall.ps1`（保留 config/sessions/logs）。
- **验收探针**：`lxcode --probe [addr]`——连接/ready/hello/model.list/session.list/history；退出码 0=全好、1=协议失败（服务死）、2=活着但没配模型（等热加载）。
- **服务形态与控制台形态同一条装配路径**（`runServe`）：差异只在 ctx 取消信号来源（SCM Stop vs Ctrl+C）；服务里 `svc.Execute` 薄壳，`serviceName` 常量与 install.ps1 的 `sc create` 名字绑定（两处同步改）。
- 详见 docs/02-windows-service.md。

## 7. 与 local-myt-agent 的关系

- 参考实现：`C:\Users\xzy\Desktop\gs\local-myt-agent`（设备端 agent，Docker 全权容器部署）；
- 已继承：llm 双格式客户端（整包）、tools 注册表模式与六件工具、JSONL 会话存储（lxcode 后于 2026-12 切 SQLite）、动态系统提示词、WS JSON-RPC 协议层与客户端库（protocol/wsclient 整体移植）、工程规范（AGENTS.md 奠基/中文注释/测试纪律）；
- 有意不同：端口 7789（错开 7788）；协议扩展 todo 事件/历史带 todos；去 chat.reset（session.new 覆盖）；agent 哨兵错误供服务端映射错误码（结构化判断不做字符串匹配）；maxToolRounds 8→16（编码任务链路更长）；edit 低危自动执行（编程 agent 语义）；bash 按 OS 选 shell（Windows 优先 Git Bash）；模型注册表已有 30s 热加载（代码变更仍需重启后端）。

## 8. 可组装 Agent（2026-09-17 设计定案，前端原型已落地）

Harness 的目标形态：**主 Agent 只做决策与分派，子 Agent 是用户组装的执行单元**。前端原型已完成（内存态，`frontend/src/shared/agents.tsx` + `components/agents/`），后端化路线见 §9 待定。设计决策：

- **两类制**：主 Agent = 唯一调度者（不可被委派）；子 Agent = 纯执行者（不可委派）。委派深度恒为 1——环、成本爆炸、借手提权从结构上消失（未来要局部协作需显式引入新类，不静默改回白名单）。
- **Agent 上下文四层组合**：① 协议层（**可定制**——预填内置默认，用户可整段替换「宪法修正案」；主=调度协议/子=执行协议）② 模块层（**上下文模块目录**，可插拔：流程模块**单选**——工作方式是完整单元，缺流程就补一个，不拼装；技能模块多选）③ 自定义段（每个 Agent 私有的自由文本）④ 动态注入（主 Agent 的可委派名单 + 会话上下文）。主 Agent 的提示词不是用户写的——用户通过数据控制调度（子 Agent 的职责描述 = 主 Agent 的选人信号）。
- **委派两层配置**：名单默认（主 Agent 的 `delegates`，编辑器勾选）+ 会话覆盖（输入区 Agent 菜单二级面板，新会话/切换会话自动回落默认）。有效名单 = 覆盖 ?? 默认 ∩ 启用的子 Agent（`shared/agent-delegation.ts` 纯函数，有测试钉住）。
- **目录条目 = 短摘要 + 完整 markdown 文档**（类 SKILL.md）：工具带参数表与扩展文档，模块带正文；编辑器两级详情（右侧紧凑面板跟随 chip 焦点，点面板开 640px 文档弹窗）。
- **目录可自建**：技能/模板是纯内容（markdown），用户可创建/编辑/删除（目录管理页 + 模块编写器带实时预览，Provider 状态单源——Agent 组装 chips 即时可选）；模板/技能创建分开口（类型由入口页签定死，不表内切换）。工具走**导入**：固定格式 v1 的 JSON 契约（`shared/tool-import.ts` 的 `parseToolImport` 校验，有测试钉住）——`{"version":1,"tools":[{"id","desc","risk":"low|high","source":"builtin|binary","params"?:[{"name","type","required"?,"desc"?}],"doc"?}]}`，id 目录内唯一，source 只 builtin/binary——**MCP 工具不手动创建/导入**（由 MCP 服务器注册后自动暴露）；后端化后同一格式做插件的分发载荷。**MCP 是目录第四版块**：服务器（`McServerSpec`——id/名称/命令/启停）是接入单元，能力以工具形式进工具目录（`source=mcp` + `server` 字段指回来源），停用服务器 = 能力挂起。
- **表单组件套件**（`components/form/`，`.fd-` 命名空间）：Button/TextInput/Textarea/Select/Segmented/ColorPicker/Chips/Toggle——项目内表单一律用套件，不再裸写原生控件（chips 有 `exclusive` 单选模式承载流程原子语义）。
- 字段命名注意：AgentDef 的流程模块字段叫 `workflow` 不叫 `process`——与 Node 全局 `process` 撞形会让边界守卫（scripts/check-boundaries.mjs）误报，属性读取与 `process.env` 结构上无法区分。
- **种子名单（2026-09-23）**：后端种子 = 主 Agent + 三个执行面 Agent——`coder`（代码，实现）、`researcher`（调研，只读勘察）、`tester`（测试，验证与跑命令）；主 Agent 的 `delegates` = 这三个 id。前端 `agent-seeds.ts` 的演示名单（main/coder/researcher/reviewer/ops）是**演示专属**，live 模式下后端是事实源（`shared/agents.tsx`）。三条种子纪律：
  - **执行面即白名单**：调研 Agent 的 `tools` 不含 edit/write_file/bash（"不改文件"是结构保证），`approval=strict` 是第二道保险；测试 Agent 必须含 `bash`（不跑就无从验证）；
  - **子 Agent 种子不含 `agent.dispatch`**（两类制，深度恒 1）；白名单也不含"声明了没实现"的工具（如 `browser`——注册表里没有它，提示词会点名「当前不可用」）；
  - **种子必须自洽**：`workflow`/`skills`/`tools` 引用的 id 必须真实存在——server 按 id 解析时**缺失静默跳过**，打错就是一份空提示词且不报任何错（`TestSeedAgentsSelfConsistent` 钉住这条）。
- **种子同步（Agent 侧，2026-09-23）**：工具/模块按 `custom` 全字段同步（见 §3），Agent 名单**更严**——`ensureSeedAgents` 只插入缺失的 id（已有行一律不 UPDATE、绝不 DELETE：子 Agent 种子插入即 `custom=1`＝用户所有，主 Agent 的 `delegates` 更是用户配置），`topUpMainDelegates` 只在主 Agent 的委派名单仍含**上一版**种子子 Agent（`seedDelegatesBaseline`）时才补新增的。代价：用户删过的种子 Agent 下次 Open 会回来（工具/模块的种子同步本来同性质——不想要应停用而不是删除）。
- **审批取严（2026-09-23）**：`effectiveApproval` 是"请求级 > Agent 默认 > confirm"（请求级是用户的显式选择，**不在这里取严**）；取严发生在**派发**这一层——`runDispatch` 用 `stricterApproval(父轮审批, 子 Agent 默认)`（auto < confirm < strict；子 Agent 未声明默认 = 继承请求方），子执行面因此不大于请求方。

## 9. 待定决策

| 项 | 状态 |
|---|---|
| 桌面壳框架 | **已定 Electron + Go sidecar**（2026-09-16 用户拍板，决策记录 §2.1；推翻 09-10 的 Tauri 2 初选）——薄壳 + 前端直连 WS（React + aicss，设计语言 agent-console-v3）；**打包与更新机制已定（2026-12，electron-builder + NSIS + 自建 zip 更新，仅 Windows，见 §2.1）**；src-tauri 骨架已清理（2026-09-16） |
| 项目正式名 | 工作名 lxcode，用户保留命名权 |
| 上下文管理 | **已落地（2026-09-22，P1~P5 + 子会话头部保护，见 §2.2/§2.3）**：计账（真实 prompt_tokens 锚定 + 分类归一）、工具配对不变量、摘要压缩（自动三条触发路径 + 手动 `/compact` + 影子区间落库，区间可为前缀也可为中间段）、取消/失败路径的配对补齐（P5——不挂 LLM 的确定性补齐）、子会话任务说明书的头部保护（`protectHead`）、前端「已压缩历史」块与指示器入口。**未做**：P3 确定性裁剪；工具结果截断（8KB/条）继续兜底 |
| 语义记忆 | 未做（会话搜索先行）；**存储底座已定（2026-12）：会话已切 SQLite（modernc 纯 Go）——语义记忆/向量检索（FTS5/sqlite-vec）将在同库扩展，不再单独立项选型** |
| 自更新 | 方向 = 定时检查 + 人工确认；机制已定（2026-12）：**自建 zip 更新**（manifest.json + update-\<version\>.zip，两级：后端热替换 / asar 冷替换，Electron 升级走全量安装包；electron-updater 方案作废）——**产物侧已实现**（build.mjs 产出 zip+manifest），**客户端更新器未实现**（待做：检查/下载/校验/替换编排，路线图 M5）；服务形态走 scripts\service\update.ps1 |
| **后端化路线** | **已定**（2026-09-18，docs/backend-roadmap.md）：M1 注册表与目录（四张表+agent.\*/catalog.\* 协议+前端接线）→ M2 上下文组装+Agent 直选（chat.send 带 agentId）→ M3 agent.dispatch（**2026-09-22 升级：子 Agent = 独立会话**——见 §2.3；原「子上下文隔离」的取舍是"子上下文随主会话轮次结束丢弃"，现已改为独立持久会话，可续跑、压缩同款）→ M4 拓展执行面（自定义工具 spawn/MCP stdio/网页搜索）→ M5 远程访问+更新器。子 Agent 再委派/多活跃会话/worktree 明确出界 |
