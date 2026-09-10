# AGENTS.md — myt-harness

> 本文件是本仓库的协作规范：任何 AI agent 或开发者在本仓库工作前，先读此文件。
> 工作名 myt-harness（MYT + harness/智能体运行时）——改名只是 `go mod edit -module` + 目录重命名的事，用户拍板后执行。

## 1. 项目定位

**myt-harness 是用户自己的个人智能体：Go 内核 + 桌面壳（规划中）。**

- 双定位：**编程助手**（读写代码、改文件、跑构建测试）+ **通用个人助理**（日常事务、检索、自动化）；
- 跑在用户本机（Windows 开发机为主，兼容类 Unix），单用户；
- 是 local-myt-agent（`../local-myt-agent`，设备端 Go agent）的**精神续作而非代码分支**：架构模式与实证坑直接继承（LLM 双格式客户端、工具风险分级、JSONL 会话、动态提示词），但为桌面场景重新设计——内核是纯 Go 包而非独立服务进程，**不依赖任何 UI 框架与网络协议**，桌面壳以库形态嵌入。

## 2. 架构基线（已定事实）

| 项 | 决策 |
|---|---|
| 内核形态 | 纯 Go 包（`internal/agent`），无 UI/协议依赖；桌面壳 in-process 调用（typed Event + Emitter 回调 + Confirm 裁决） |
| CLI | `cmd/myt-harness`：零依赖 REPL（验收面/调试入口，非最终 UI） |
| LLM | 双 wire 格式：OpenAI chat completions + Anthropic Messages（`internal/llm`，从 local-myt-agent 整包继承——含 ChatAuto 分流策略：anthropic 恒流式，openai 带工具走非流式回放，依据是真机端点实测 openai 流式丢 tool_calls） |
| 工具 | `internal/tools` 注册表 + 风险分级：低危自动执行，高危确认门 |
| 会话 | JSONL append-only（`internal/store`），重启恢复最近会话，`/new` `/resume` 切换 |
| 配置 | `internal/config` 模型注册表（models.json，原子写；default/vision 角色绑定） |
| 桌面壳 | **规划中**（Wails v3 beta vs Electron+Go sidecar 待选型）；内核已为此保持包级干净（agent 不 import cli） |

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

- Go，module `github.com/moyunteng/myt-harness`；提交前 `go build ./...` + `go vet ./...` + `gofmt -l .`（输出为空）+ `go test ./...` 全绿；
- 注释解释"为什么"，用中文；提交信息 conventional commits（`feat(工具): …`）；
- 测试就近放包内（`_test.go` 与源码同目录——Go 项目按包放测试是正确布局）；
- 运行 CLI：`go run ./cmd/myt-harness`（配置默认 `./config/models.json`，`--config` / `MYT_HARNESS_CONFIG` 覆盖；会话目录默认 `<config 上级>/sessions`）；
- 本地冒烟：`config/local.json`（gitignored，含 key）+ `node temp/smoke.mjs "消息"`（真实端点端到端：流式 + 工具循环）；
- 系统提示词：工具清单从注册表动态生成（`agent.BuildSystemPrompt`），`TestSystemPromptListsAllTools` 钉住不漂移——加新工具忘了更新 `systemPromptTools` 映射会直接红。

## 5. 已知坑（改代码前先看）

1. **Windows 的 bash.exe 是 WSL stub**：`LookPath("bash.exe")` 在装了 Git 的机器上也先命中 `C:\Windows\System32\bash.exe`——没装 WSL 发行版时它不执行命令、只打 UTF-16 安装提示。`selectShell` 的候选顺序：sh.exe → 不在系统目录的 bash.exe → 常见 Git Bash 路径 → cmd。
2. **Windows 的 `os.Rename` 不覆盖已存在文件**：原子写回退必须是"目标先改名备着"（`renameViaBackup`），绝不能"先删再改名"。
3. **同秒创建的会话文件名序不可靠**：恢复最近会话按 mtime，不按文件名（local-myt-agent 实测踩过）。
4. **race detector 在本机需要 CGO**（无 gcc）：`go test -race` 不可用，并发正确性靠代码评审 + 事件互斥纪律（emit 持 REPL.mu，Session 状态持 s.mu）。
5. **测试假流的中断契约**：error 事件须携带已生成部分（`Result`）——真实客户端（`llm.emitFinal`）如此，假流不带上会丢 partial（TestCancelPreservesPartial 踩过）。

## 6. 与 local-myt-agent 的关系

- 参考实现：`C:\Users\xzy\Desktop\gs\local-myt-agent`（设备端 agent，Docker 全权容器部署）；
- 已继承：llm 双格式客户端（整包）、tools 注册表模式与六件工具、JSONL 会话存储、动态系统提示词、工程规范（AGENTS.md 奠基/中文注释/测试纪律）；
- 有意不同：无 WS JSON-RPC 层（桌面 in-process，typed Event 直调）；maxToolRounds 8→16（编码任务链路更长）；edit 低危自动执行（编程 agent 语义）；bash 按 OS 选 shell（Windows 优先 Git Bash）；无热加载（CLI 短生命周期，桌面壳后期再加）。

## 7. 待定决策

| 项 | 状态 |
|---|---|
| 桌面壳框架 | Wails v3 beta（Go 原生、单二进制）vs Electron + Go sidecar（复用 LX-DSH 经验与更新管线）——内核稳定后选 |
| 项目正式名 | 工作名 myt-harness，用户保留命名权 |
| 上下文管理 | 工具结果截断（8KB/条）已兜底；compaction/历史摘要未做 |
| 语义记忆 | 未做（会话搜索先行；SQLite 嵌入式是倾向） |
| 依赖策略 | 零第三方依赖（gorilla/websocket 都不需要了）——保持到桌面壳选型为止 |
