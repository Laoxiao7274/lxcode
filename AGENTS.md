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
| 协议 | `internal/protocol`：WS JSON-RPC 2.0（帧/方法/事件单处定义，客户端服务端共享）；扩展 `todo.updated` 事件与 `ChatHistoryResult.Todos`；`chat.send` 可选参数 `effort`（minimal/low/medium/high，仅声明 reasoning 能力的模型生效）与 `approval`（auto/confirm/strict 工具执行三档，空 = confirm）；端口 7789（与 local-myt-agent 的 7788 错开） |
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
| `agent.dispatch` | 低危 | 主 Agent 唯一工具：把任务派给名单里的子 Agent（子上下文隔离，深度恒 1） |

**自定义工具（M4-1 执行面，2026-09-21）**：目录里 `source=binary` 的条目由 server 在启动与每次 `catalog.tools.*` 变更后经 `syncDynamicTools()` 注册进注册表（`tools.Registry.SetDynamic` 整体替换动态段；内置段不动，同名跳过并记日志）。执行语义：

- **不经过 shell**：`command` 模板按空白切分成 argv，`{param}` 占位替换为参数值——值原样作为单个 argv（含空格不切分），引号/分号/`$` 全是普通字符，注入面在结构上消失；
- 参数只支持标量（string/number/bool）；缺参/未知参/非标量/null 都自解释报错回填模型，不 spawn；
- cwd = 会话工作目录（`tools.WorkDir`）；超时 60s、输出 32KB 截断（对齐 bash）；退出码/超时/启动失败以文本回填；
- `Mutates=true`（strict 只读模式拒绝）；`risk=high` 走确认门，确认文本含**渲染后的完整命令**；
- 未配置 `command` 的 binary 条目（种子里 `browser` 是「声明了没实现」的形态）跳过并记日志，不影响其余目录；目录页对它标**「未配置」**（amber pill），外部二进制条目**可编辑**（内置只读）——用户勾进白名单前就能看见、也能自己补 command；
- 白名单勾了但注册表里没有的工具，会在该 Agent 的系统提示词里被点名「当前不可用」——不让模型去调一个不存在的工具。
- **种子同步（`initAgents` 每次 Open 都跑）**：种子只在库空时整套注入会有一个致命后果——**代码修好了种子，老库永远吃不到**（用户报告过「给了 ripgrep，模型答注册表没有」）。所以库非空时跑 `syncCatalogSeeds`：按 id 同步种子行（缺失插入、`custom=0` 按代码更新全字段），**`custom=1`（用户自建或改过的行）一律不碰**，**不删除**（避免动到白名单可能引用的行）。Agent 名单**不同步**——主 Agent 的 `delegates` 等字段用户可改，覆盖就是吃掉用户的配置。

- **edit 为何低危**：编程 agent 的主编辑通道，确认门会让它不可用；破坏面受 old_string 唯一匹配约束 + 原子写 + 版本控制兜底（与 write_file 的全量覆盖破坏面不同类）。
- **权限模式三档**（chat.send 的 approval 参数，随消息携带）：`confirm`（默认）= 低危自动 + 高危确认；`auto` = 高危也自动执行（仅隔离环境）；`strict` = 只读——变更类工具（`Def.Mutates`：edit/write_file/bash，与风险等级正交）直接拒绝、错误回填模型。风险等级管"要不要确认"，Mutates 管"只读模式禁不禁"——edit 低危但变更文件，strict 下必须拒。
- 参数坏 JSON 先走保守修复（裸换行/尾逗号/单引号/截断补括号），修复成功注明——弱模型坏参数是高频失败形态。

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
- **分层规则**：`sessiondata`（中立业务类型）← `store`/`agent`；`agent.Persistence` 是消费者定义的最小接口（agent 不见 SQL/连接/事务）；`project` 包持目录校验与 git init 业务（server 只是协议转发）；设置面板依赖 `ModelAdminSource` 能力接口而非具体 WSAgent——UI 永远不知道数据来自 WS 还是 Demo。

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

## 9. 待定决策

| 项 | 状态 |
|---|---|
| 桌面壳框架 | **已定 Electron + Go sidecar**（2026-09-16 用户拍板，决策记录 §2.1；推翻 09-10 的 Tauri 2 初选）——薄壳 + 前端直连 WS（React + aicss，设计语言 agent-console-v3）；**打包与更新机制已定（2026-12，electron-builder + NSIS + 自建 zip 更新，仅 Windows，见 §2.1）**；src-tauri 骨架已清理（2026-09-16） |
| 项目正式名 | 工作名 lxcode，用户保留命名权 |
| 上下文管理 | 工具结果截断（8KB/条）已兜底；compaction/历史摘要未做 |
| 语义记忆 | 未做（会话搜索先行）；**存储底座已定（2026-12）：会话已切 SQLite（modernc 纯 Go）——语义记忆/向量检索（FTS5/sqlite-vec）将在同库扩展，不再单独立项选型** |
| 自更新 | 方向 = 定时检查 + 人工确认；机制已定（2026-12）：**自建 zip 更新**（manifest.json + update-\<version\>.zip，两级：后端热替换 / asar 冷替换，Electron 升级走全量安装包；electron-updater 方案作废）——**产物侧已实现**（build.mjs 产出 zip+manifest），**客户端更新器未实现**（待做：检查/下载/校验/替换编排，路线图 M5）；服务形态走 scripts\service\update.ps1 |
| **后端化路线** | **已定**（2026-09-18，docs/backend-roadmap.md）：M1 注册表与目录（四张表+agent.\*/catalog.\* 协议+前端接线）→ M2 上下文组装+Agent 直选（chat.send 带 agentId）→ M3 agent.dispatch（**子上下文隔离已拍板**——dispatch 开独立执行上下文，结果回填主会话；复用现有事件流+dispatchId 归属标记）→ M4 拓展执行面（自定义工具 spawn/MCP stdio/网页搜索）→ M5 远程访问+更新器。子 Agent 再委派/多活跃会话/worktree 明确出界 |
