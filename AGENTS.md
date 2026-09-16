# AGENTS.md — lxcode

> 本文件是本仓库的协作规范：任何 AI agent 或开发者在本仓库工作前，先读此文件。
> 2026-09-11 定名 **lxcode**（用户拍板，接续 LX 品牌线）；原名 myt-harness，全仓已重命名
> （module github.com/moyunteng/lxcode、目录、服务名、二进制、环境变量 LXCODE_*、协议身份）。

## 1. 项目定位

**lxcode 是用户自己的个人智能体：Go 内核 + 桌面壳（规划中）。**

- 双定位：**编程助手**（读写代码、改文件、跑构建测试）+ **通用个人助理**（日常事务、检索、自动化）；
- 跑在用户本机（Windows 开发机为主，兼容类 Unix），单用户；
- 是 local-myt-agent（`../local-myt-agent`，设备端 Go agent）的**精神续作而非代码分支**：架构模式与实证坑直接继承（LLM 双格式客户端、工具风险分级、JSONL 会话、动态提示词），但为桌面场景重新设计——内核是纯 Go 包而非独立服务进程，**不依赖任何 UI 框架与网络协议**，桌面壳以库形态嵌入。

## 2. 架构基线（已定事实）

| 项 | 决策 |
|---|---|
| 形态 | **前后台分离**（2026-09-10 用户拍板，对齐 local-myt-agent）：后端 `lxcode --serve` 独立进程（WS JSON-RPC `127.0.0.1:7789/rpc` + 注册表 + 会话运行时 + 工具循环，`/health` 健康检查）；CLI / 桌面壳都是客户端 |
| 协议 | `internal/protocol`：WS JSON-RPC 2.0（帧/方法/事件单处定义，客户端服务端共享）；扩展 `todo.updated` 事件与 `ChatHistoryResult.Todos`；端口 7789（与 local-myt-agent 的 7788 错开） |
| 内核 | `internal/agent`：纯 Go 包（typed Event + Emitter + Confirm），被 server 包装广播；桌面壳将来也可 in-process 嵌入（包级零 UI 依赖保持不变） |
| 客户端 | `internal/wsclient`：Backend 接口 + Dial（请求按 id 配对、事件 channel、断连 fast-fail、缓冲满丢最旧）；CLI 是第一个客户端，桌面壳复用同一协议 |
| 前端 | **React 19 + TypeScript + Vite + gsap**，零 UI 库（手写 CSS 设计 token，设计语言 agent-console-v3）；`AgentSource` 双实现：WSAgent（连 7789 真实后端）/ DemoAgent（纯前端演示，无后端也能全量跑 UI）；渲染纪律：打字机行级 memo + memo(Block) 稳定回调 + motionAllowed 动效门控（reduced-motion/测试开关） |
| 内核并发 | 单 Go 进程多会话（goroutine + context 贯穿全部等待点 + channel 传递状态）；多会话扩展见 §2.1（调速器 + 会话停车 + worktree），**不做每会话进程/微服务** |
| LLM | 双 wire 格式：OpenAI chat completions + Anthropic Messages（`internal/llm`，从 local-myt-agent 整包继承——含 ChatAuto 分流策略：anthropic 恒流式，openai 带工具走非流式回放，依据是真机端点实测 openai 流式丢 tool_calls） |
| 工具 | `internal/tools` 注册表 + 风险分级：低危自动执行，高危确认门 |
| 会话 | JSONL append-only（`internal/store`），重启恢复最近会话，`/new` `/resume` 切换 |
| 配置 | `internal/config` 模型注册表（models.json，原子写；default/vision 角色绑定；**30s 热加载** + model.changed 广播） |
| 服务化 | **Windows SCM 服务**（`scripts/service/{install,update,uninstall}.ps1`；开机自启 + 崩溃自动重启；`--probe` 验收；布局 `%ProgramData%\lxcode\{bin,config,sessions,logs}`）；服务形态日志落文件（16MB 轮转 ×3） |
| 桌面壳 | **Electron + Go sidecar（2026-09-16 用户拍板，推翻 09-10 的 Tauri 2 初选，决策记录见 §2.1）**；后端可先于壳长期独立运行，壳是薄客户端（窗口/托盘/渲染层直连 7789） |
| 依赖 | gorilla/websocket（协议层必需）；其余零第三方依赖 |

### 2.1 语言栈与桌面壳决策记录（2026-09-16 拍板）

- **桌面壳 = Electron + Go sidecar**（推翻 2026-09-10 的 Tauri 2 初选；`frontend/src-tauri` 骨架与 Tauri 构建脚本已于 2026-09-16 清理）。翻案理由：应用内嵌浏览器（人用面板）进入路线图，Electron 的 webContents（同窗口多视图 / session 隔离 / 请求拦截 / 内建 CDP）是唯一不将就的深度；Tauri/Wails 在 Windows 同用系统 WebView2（也是 Chromium），渲染无增益，省的只是占用（内存 ~100-200MB / 磁盘 ~100MB / 冷启动 +0.5s）——用占用换控制权与生态，且 LX-DSH 的 electron-builder/NSIS/自动更新管线原样平移、产品线栈统一。Electron 开销全在占用层，不在计算热路径（重活在 Go 内核；渲染器=你正在开发的同一个 Chromium 页面）。
- **Rust 不重写内核**：后端负载 90%+ 是等 LLM/等子进程，唯一 CPU 密集点（search）已由"exec 外部二进制"覆盖。**FFI/cgo 严禁入仓**（链接地狱 / panic 边界 / 跨语言调试成本远超收益）；真要第二语言模块，须同时满足三门槛才升 sidecar 服务：占热路径 >30% / 自包含无共享状态 / Go 生态无等效品。
- **Rust 的正确进入方式 = 进程边界**：ripgrep 这类外部 Rust 二进制直接接 tools 注册表（"Go 主刀，Rust 武器库"）；仓库零 Rust 工具链，"依赖只有两个"的基线不动。
- **多会话并发 = 单进程 + 调速器**：瓶颈链 = LLM 限流 << 机器 CPU/磁盘 << 单进程容量（100+ 会话不撞）。方案 = 三档 governor（llmBudget 按供应商 token-bucket / toolPool 分池并发上限 / maxActiveTurns 公平 FIFO）+ 空闲会话停车（状态落 JSONL，内存跟活跃集走）+ **Git worktree 每任务隔离**（设置页已预留分区）。协议先行：事件/方法显式 sessionId + `turnQueued`/`budgetWait` 类事件，契约测试钉住。
- **Electron 侧工程纪律**：单窗口 + WebContentsView 做浏览器面板（不开多 BrowserWindow）；contextIsolation 开、renderer 无 node 集成；主进程只做窗口/托盘/sidecar 生命周期，不放业务；后端仍是 SCM 服务优先（壳只是客户端，连不上给启动指引）。

## 3. 工具面（7 个）

| 工具 | 风险 | 说明 |
|---|---|---|
| `read_file` | 低危 | 按行输出（`行号→` 前缀），offset/limit 分页，256KB 上限，二进制拒绝 |
| `search` | 低危 | 纯 Go RE2 检索（files/content/count 三模式），不经过 shell |
| `session_search` | 低危 | 搜历史会话内容（注入接线：格式归 store 包） |
| `edit` | 低危 | **精确替换**：old_string 唯一匹配硬校验（0/>1 报错自解释），原子写 |
| `write_file` | 高危 | 全量覆盖：覆盖已有文件需确认 + 缩水守卫（<50% 告警） |
| `bash` | 高危 | 超时 60s/上限 300s、输出 32KB、stdin ≤64KB；**Windows shell 选择见 §5 坑** |
| `todo` | 低危 | 任务清单全量写入（active 唯一性硬校验）；会话持有状态 + TodoUpdated 事件 |

- **edit 为何低危**：编程 agent 的主编辑通道，确认门会让它不可用；破坏面受 old_string 唯一匹配约束 + 原子写 + 版本控制兜底（与 write_file 的全量覆盖破坏面不同类）。
- 参数坏 JSON 先走保守修复（裸换行/尾逗号/单引号/截断补括号），修复成功注明——弱模型坏参数是高频失败形态。

## 4. 开发约定

- Go，module `github.com/moyunteng/lxcode`；提交前 `go build ./...` + `go vet ./...` + `gofmt -l .`（输出为空）+ `go test ./...` 全绿；
- 注释解释"为什么"，用中文；提交信息 conventional commits（`feat(工具): …`）；
- 测试就近放包内（`_test.go` 与源码同目录——Go 项目按包放测试是正确布局）；
- 后端：`lxcode --serve`（配置默认 `./config/models.json`，`--config` / `LXCODE_CONFIG` 覆盖；会话目录默认 `<config 上级>/sessions`；监听 `--addr`，默认 `127.0.0.1:7789`）；
- CLI 客户端：`lxcode`（连 `--backend`，默认取 `--addr`；连接失败会给启动指引）；
- 本地冒烟：起后端 `lxcode --serve --config config/local.json --sessions temp/smoke-sessions`（config/local.json gitignored，含 key），然后 `node temp/smoke.mjs "消息"`（端到端）或 `node temp/smoke-confirm.mjs`（确认门）；
- 系统提示词：工具清单从注册表动态生成（`agent.BuildSystemPrompt`），`TestSystemPromptListsAllTools` 钉住不漂移——加新工具忘了更新 `systemPromptTools` 映射会直接红；
- 协议改动跑 `internal/protocol` 帧契约测试（字段改名不编译报错、只静默丢字段——测试钉住载荷形状）。

## 5. 已知坑（改代码前先看）

1. **Windows 的 bash.exe 是 WSL stub**：`LookPath("bash.exe")` 在装了 Git 的机器上也先命中 `C:\Windows\System32\bash.exe`——没装 WSL 发行版时它不执行命令、只打 UTF-16 安装提示。`selectShell` 的候选顺序：sh.exe → 不在系统目录的 bash.exe → 常见 Git Bash 路径 → cmd。
2. **Windows 的 `os.Rename` 不覆盖已存在文件**：原子写回退必须是"目标先改名备着"（`renameViaBackup`），绝不能"先删再改名"。
3. **同秒创建的会话文件名序不可靠**：恢复最近会话按 mtime，不按文件名（local-myt-agent 实测踩过）。
4. **race detector 在本机需要 CGO**（无 gcc）：`go test -race` 不可用，并发正确性靠代码评审 + 事件互斥纪律（emit 持 REPL.mu，Session 状态持 s.mu）。
5. **测试假流的中断契约**：error 事件须携带已生成部分（`Result`）——真实客户端（`llm.emitFinal`）如此，假流不带上会丢 partial（TestCancelPreservesPartial 踩过）。
6. **手写 models.json 测试必须带 `"version": 1`**：Load 校验磁盘格式版本，缺了整份拒绝且只记日志（表现为"热加载广播没来"）。
7. **PowerShell 脚本必须 UTF-8 带 BOM**：PS 5.1 对无 BOM 文件按 ANSI 读，中文注释乱码会**破坏语法**（ParseFile 实证）；而 Go/JSON 恰恰相反（BOM 会炸 json.Unmarshal）——两类文件的编码策略相反，别搞混。
8. **http.Shutdown 不打断 WS 长连接**：服务优雅停机必须先 `closeAllClients()` 再 Shutdown（local-myt-agent 依赖 docker kill 兜底，SCM 等不了）。

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
- 已继承：llm 双格式客户端（整包）、tools 注册表模式与六件工具、JSONL 会话存储、动态系统提示词、WS JSON-RPC 协议层与客户端库（protocol/wsclient 整体移植）、工程规范（AGENTS.md 奠基/中文注释/测试纪律）；
- 有意不同：端口 7789（错开 7788）；协议扩展 todo 事件/历史带 todos；去 chat.reset（session.new 覆盖）；agent 哨兵错误供服务端映射错误码（结构化判断不做字符串匹配）；maxToolRounds 8→16（编码任务链路更长）；edit 低危自动执行（编程 agent 语义）；bash 按 OS 选 shell（Windows 优先 Git Bash）；无热加载（后端重启即可，桌面壳接入后再评估）。

## 8. 待定决策

| 项 | 状态 |
|---|---|
| 桌面壳框架 | **已定 Electron + Go sidecar**（2026-09-16 用户拍板，决策记录 §2.1；推翻 09-10 的 Tauri 2 初选）——薄壳 + 前端直连 WS（React + aicss，设计语言 agent-console-v3）；动工时定细节；src-tauri 骨架已清理（2026-09-16） |
| 项目正式名 | 工作名 lxcode，用户保留命名权 |
| 上下文管理 | 工具结果截断（8KB/条）已兜底；compaction/历史摘要未做 |
| 语义记忆 | 未做（会话搜索先行；SQLite 嵌入式是倾向） |
| 自更新 | 参考项目同样未实现（update.sh --check 地基）；方向 = 定时检查 + 人工确认 |
