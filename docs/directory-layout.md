# 目录地图

本文只收拢 AGENTS.md、既有文档与当前仓库已经存在的约束，不另立架构规则。

## 顶层目录

| 路径 | 职责 |
| --- | --- |
| `AGENTS.md` | 全仓协作规范、架构决策与各领域工作守则的入口。 |
| `internal/` | Go 内核与服务实现的 21 个包；`architecture/boundaries_test.go` 用 AST 守卫依赖方向。 |
| `cmd/` | Go 后端与 CLI 的可执行入口；不承载客户端实现。 |
| `frontend/` | React 19 + TypeScript 桌面/浏览器客户端，`tests/` 放纯函数与协议护栏测试。 |
| `shell/` | Electron 薄壳、sidecar 生命周期、托盘与更新器；主进程不放业务。 |
| `android/` | 安卓远程客户端及 JVM 单元测试。 |
| `site/` | 官网前端；`dist/` 是部署通道，有意跟踪。 |
| `site-backend/` | 官网后端、发布清单与下载服务。 |
| `scripts/` | 构建、开发、发版、图标派生及架构守卫脚本。 |
| `docs/` | 各领域的完整契约、操作手册与决策记录。 |
| `config/` | 本地模型、搜索、远程访问等运行配置；含密钥的文件不入库。 |
| `temp/` | Git 忽略的本地构建输入、备份、探针与一次性产物，不是通用源码目录。 |

根目录的 `build.sh`、`dev.sh` 是构建与开发环境的合法入口，不为“根目录干净”而移动。

## 各端关键边界

### Go：依赖只能向内、向下

`internal/architecture/boundaries_test.go` 直接解析 Go import，现有五组测试分别钉住：

1. `TestBackendDependencyBoundaries`：`agent` 不依赖 `store/server/protocol`，`store` 不反向依赖运行时或传输层；`chatmsg` 保持叶包，`llm` 只走允许的消息模型与 JSON 修复叶包。
2. `TestLeafPackagesDoNotImportRuntime`：`tools/chatmsg/store` 不依赖 `server/agent`，避免底层反向拖入运行时。
3. `TestProtocolIsTransportOnly`：`protocol` 只做 wire，不依赖运行时和业务实现；共享数据形状走叶包。
4. `TestToolsDoNotImportStore`：工具执行层通过注入回调访问业务能力，不依赖存储实现。
5. `TestCmdIsBackendSide` 与 `TestWholeBackendNotClient`：入口及整个后端不 import `frontend/shell`。

### 前端：渲染层不越过适配层

`scripts/check-boundaries.mjs` 使用 TypeScript AST 检查 `frontend/src`：网络通信只能位于 `agent/ws`；普通消费者依赖能力接口，具体 `WSAgent` 只由 `agent/index.ts` 工厂引用；渲染层不得取得 Node/Electron 通用能力；`__LX__` 宿主桥只开放给白名单中的窗口、目录及宿主能力适配模块。它检查语法树而非扫注释，避免文档示例误报。

### `shared/` 归属规则

`frontend/src/shared/` 只放**无 JSX 的纯逻辑与设计 token**（状态归约、格式化、
协议镜像、纯函数）；JSX 组件不属于这里——按复用面分流：

- 多页面复用的组件住 `frontend/src/components/`（如灯箱 `lightbox.tsx`、
  markdown 渲染 `markdown.tsx`——两个页面群都在用）；
- 只服务单一页面的组件住该页面的目录（如 `components/thread/blocks/`、
  `components/settings/`）。

判断依据是引用面而不是文件大小：一个 `.tsx` 若只被一个页面 import，留在
shared/ 只会模糊「什么可以依赖什么」的边界。新增文件默认进对应页面目录，
进 shared/ 需要满足「纯逻辑、无 JSX、被多个不相邻的模块引用」。

### 协议：一个事实源，两份手写镜像

`internal/protocol` 是 WS JSON-RPC 2.0 的事实源；前端 `frontend/src/shared/types/`（桶入口 `frontend/src/shared/types.ts`，按协议域拆成 chat/events/rewind/jobs/git/source/search/settings/catalog/agent-admin 等文件）与安卓 `android/app/src/main/java/com/moyunteng/lxcode/remote/model/Wire.kt` 是各自维护的手写镜像，不从 Go 自动生成。

Go 与前端共用 `internal/protocol/testdata/golden.json`：Go 侧检查 wire 字段覆盖及样本序列化，前端 `frontend/tests/protocol-golden.test.mjs` 检查 TS 接口声明、往返稳定性与关键字段语义。安卓以 Wire 注释中的 Go 类型对齐关系和 JVM 测试约束参数/解析行为。协议改动必须同步三端并跑对应 golden/镜像护栏，不能只改“能编译”的一侧。

## AGENTS.md 覆盖补记

目前只有根目录 `AGENTS.md`，以下记录各目录已有但根守则未集中说明的边界。

### `android/`

- 代码按 `ui/`（Compose 页面与交互）、`net/`（WS 客户端、后端适配与事件处理）、`model/`（Wire 类型和状态归约）分层；`update/` 放更新流程。
- `app/src/test/` 是与上述分层对应的 JVM 单元测试，覆盖归约、分页、Markdown、投屏解码/手势与更新安全等纯逻辑。
- keystore、`keystore.properties` 等签名材料只在本地，根 `.gitignore` 已排除；丢失签名材料会导致无法同签名更新，须另行备份。
- 根目录验收截图、录像等本地产物不纳入版本控制；不要把验收媒体当成仓库资源提交。

### `site/`

- `dist/` 是官网部署通道的构建产物，**有意跟踪**，不能按一般前端 dist 惯例加入 ignore。
- `dist.bak-*` 与 `.design-preview/` 不入库；后者是大型浏览器审计缓存，曾触发 GitHub 单文件限制。

### `site-backend/`

- `dist/` 与 `data/`（SQLite 等运行时数据）不入库。
- `lxcode-site-backend-linux-arm64` 是官网部署通道产物，**有意跟踪**，不要按普通二进制清理或忽略。

### `temp/`

整个 `temp/` 已被 Git 忽略，但“忽略”不等于“都可删除”。合法长期居民包括：

- `ffmpeg/`：`scripts/build.mjs` 的 bundled ffmpeg 输入，缺失时安装包会回落到用户 PATH；
- `git-backup-*.bundle`：`scripts/push-release.mjs` 执行历史发布前写出的全量 Git 备份（它只写新 bundle，不读取旧 bundle）；
- 被 `docs/` 引用、用于代码盘点或真链路验证的 `*.cjs`：例如 `docs/codebase-review.md` 与 `docs/child-session-ui.md` 点名的脚本，清理时须先核对文档引用，不能按扩展名一锅端。

视频探针、旧安装包、日志及发版中间文件属于可再生成的一次性产物，不应与上述输入和备份混为一谈。

## 暂不改动的免战项

- **协议 codegen**：当前 Go 事实源 + 两份手写镜像 + golden/镜像测试已经构成约束，本次不引入生成链与第二套事实源。
- **`internal/agent/session.go`**：会话运行时集中承载多轮、取消、压缩和子会话语义，本次目录整理不做高风险拆分。
- **`internal/server/server.go`**：服务装配与协议路由牵涉面大，边界守卫已覆盖依赖方向，本次不为文件大小做无行为收益的拆分。
