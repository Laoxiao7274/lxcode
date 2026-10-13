# 对话附件（图片/文件）——第一版契约

2026-12 落地的第一版：**用户能发附件并看到它们，Agent 能把文件共享给用户**。
本文是存储布局、wire 形状与渲染形态的唯一契约。

## 范围边界（第一版明确不做）

- ~~多模态输入（vision）~~：用户图片喂给模型走既有 vision 通道（`llm.ImageRef`）。
  **2026-12 补全**：Agent 侧的视觉读图见下文「read_image 工具」——它把工作目录里的
  图片也接进了同一条通道。
- 附件的删除/过期清理：目录随会话归档，不单独清理。

## 存储布局

- 位置：`<sessions>/attachments/<session-id>/<随机id>.<ext>`（与 worktree 同层的
  `attachments/` 根；磁盘名恒为 16 字节随机 hex + 扩展名，**原名永不成为路径成分**）。
- 消息恒存**相对 attachments 根的引用**（`<session-id>/<文件名>`）；base64 只活在
  两个瞬间（chat.send 参数里 → 落盘即弃；LLM 请求构造瞬间），绝不进历史/事件/日志。
- 大小上限：图片 5MB（Anthropic base64 源图上限，vision 通道）、文件 20MB
  （`llm.MaxFileAttachmentBytes`，tools/store/server 三层共用一份常量）。

## wire 形状（全部向后兼容：可选字段 + 新工具声明，协议 Version 不变）

- `chat.send` 的 `images`/`files` 参数（base64）**就是上传通道**——第一版没有独立的
  `chat.attach` 方法：现状里协议已有这两个字段（2026-10 图片批次 B 铺好），上传与
  发送一步完成；`ChatSendImage` 新增可选 `name`（展示名，老客户端不带 → 回落「图片」）。
- **`llm.Message.Attachments []AttachmentRef`**（json `attachments,omitempty`）是展示
  元数据的唯一载体：`{path, name, size, mime?, kind: image|file, caption?}`。
  - user 消息：用户发的图/文件（与 `Images` 并存——Images 服务视觉请求，
    Attachments 服务前端渲染）；
  - tool 消息：`share_file` 执行的结果消息。
- 落库：`messages` 表新增 `attachments` 列（幂等 ALTER，老行默认 `''` = 无附件），
  JSON 数组随消息读写（与 `images` 列同一套模式）。
- 实时事件：`chat.userMessage` 载荷是整条 `llm.Message`（attachments 随行）；
  `chat.toolResult` 新增可选 `attachments`（share_file 的元数据，与结果消息同一份）。
  归属规则与既有事件同款：子会话的工具结果自带 `dispatch_id`。
- 渲染取件：`GET /attachments/<sessionID>/<file>`（只读；token 门与 /rpc 同一份）。
  图片扩展名回 `image/*` inline；其余回 `octet-stream` + `attachment` 处置（下载）。
  路径形态校验（sessionID 白名单 + 文件名 hex + 扩展名白名单 + 根内前缀双保险），
  穿越一律 404。

## share_file 工具（Agent → 用户）

- 参数：`path`（相对会话工作目录解析，**必须在工作目录之内**，`..` 与越出根一律拒绝；
  工作目录内的绝对路径合法）、`caption`（可选一句话说明）。
- 语义只读：读源文件 → 复制进会话附件目录 → 元数据随工具结果流动。不修改任何文件，
  `Mutates=false`（strict 只读档可用）。
- 接线（与 workdir/todo sink 同一套 ctx 注入）：会话在开轮时注入
  `tools.WithAttachmentSave`（落盘口，实现在 `store.SaveSharedAttachment`）；内核按
  **每次工具调用**注入收集器（`tools.AttachmentCollector`），执行产生的引用挂到那条
  结果消息与 `ToolResultEvent` 上。
- 归属：子会话共享的文件落进**子会话自己**的附件目录（save 口按本会话 id 解析），
  事件按 `dispatch_id` 归属进子会话时间线。
- **子会话附件回传主会话（2026-12 补全）**：派发收尾时把本次派发登记到的附件随
  `DispatchResult.Attachments` 回流主会话——内核把它们挂到**主会话的调度结果消息**
  上（结构化元数据，回放路径）并随 `agent_dispatch` 的 `ToolResultEvent` 发出（实时
  路径），同时结果文本带「[附件] 名字 → attachments/<子会话id>/<文件>」行（格式与
  用户文件附件的写入格式逐字一致）——主 Agent 下一轮拿得到路径，可转述或再次
  share_file。前端渲染见 `docs/child-session-ui.md` §2.6。

## read_image 工具（Agent → 模型，2026-12 补全）

- 参数：`path`（相对会话工作目录解析，穿越拒绝与 share_file 同一套判定）。
- 语义只读（`Mutates=false`）：读工作目录里的图片（png/jpg/jpeg/webp/gif，内容
  嗅探核对扩展名），复制进会话附件目录（复用 `AttachmentSave` 落盘口），引用挂到
  那条 tool 结果消息的 `llm.Message.Images` 上——**下一轮请求构造时 llm 层按引用
  编码成视觉输入**（base64 只活在请求构造瞬间，与用户图片同一条分层纪律）。两种
  wire 形态：OpenAI = tool 消息 content 分片数组（text + image_url data URI）；
  Anthropic = tool_result 的 content 块数组（text + image base64 源，官方支持的
  形态）。
- **能力门控（fail-closed）**：会话开轮时按本轮实际会用的模型（modelFor 同一条
  解析路径）把 `Capabilities.Vision` 经 `tools.WithVisionCapable` 注入；未声明视觉
  的模型调用 read_image 得人话错误（提示 share_file 给用户看 / 勾选视觉能力），
  不假装读图。未注入 = 不支持。
- 大小上限：单图 ≤5MB（`llm.MaxImageBytes`，Anthropic 对 base64 源图的上限）。
- 给模型的结果文本带图片基本信息（路径/格式/尺寸/大小）与「给用户看用 share_file」
  的提示；给用户的展示：普通工具调用卡（模型已看图，用户要看图走 share_file——
  不额外渲染）。
- 目录与种子：工具目录新增 `read_image` 条目（store/agents_seeds.go + 前端
  agent-seeds.ts，两份对齐）；coder/tester 种子白名单加 `read_image`，职责描述
  点明「能读图片内容（视觉）」，`seedAgentBaselines` 记录上一版白名单与描述
  （老库未动过的行自动补上，动过的不碰）。

## 渲染形态（前端）

- 图片 → 内联缩略图（`<img src="/attachments/...">`，点击全屏放大，Esc/点击关闭）；
  加载失败（源文件已不在）降级为不可点死卡 + 名字照显。
- 其他文件 → 文件卡（图标 + 名字 + 大小 + caption，点击下载）。
- **两条路径共用同一份归一化**（`shared/attachments.ts` 的 `attachmentsFromMessage`）：
  结构化 `attachments` 优先；老历史回落 `images` 引用（→ 图片卡）+ 消息文本的
  「[附件] 名字 → attachments/...」行（→ 文件卡，解析后从正文剥离——正文与卡区
  各渲染一份，正文里再留机器行是噪音）。
- 渲染位置：用户气泡（气泡上方卡区）与 `share_file` 工具行（卡为主体，不需展开）。
- 附件端点的 HTTP 源由 WSAgent 连接时注入（`setAttachmentOrigin(httpOrigin(addr))`），
  与后端同主机；URL 构造对带 `..`/反斜杠/非两段的引用返回 null → 死卡，不做穿越尝试。

## Agent 名单与老库升级

- 工具目录新增 `share_file` 条目（`store/agents_seeds.go` + 前端 `agent-seeds.ts`，
  两份对齐——编辑器渲染 chip 只读目录）。
- coder/tester 种子白名单加 `share_file` 并在职责描述里点明「能把产物文件共享到对话」
  （主 Agent 的选人信号按描述逐字生成）；`seedAgentBaselines` 记录上一版白名单与描述
  ——老库里没动过这两处的行升级时自动补上，动过的行不碰（用户在 Agent 编辑器自己勾）。

## 测试组织

- Go：`internal/tools/share_file_test.go`（参数/穿越/登记）、
  `internal/tools/read_image_test.go`（门控/穿越/格式/嗅探/成功路径）、
  `internal/store/attachments_persist_test.go`（列往返 + SaveSharedAttachment）、
  `internal/agent/share_file_test.go`（内核全链路：工具结果与事件带元数据、文件落盘）、
  `internal/agent/read_image_test.go`（视觉模型续轮请求带图引用 / 非视觉人话错误）、
  `internal/agent/dispatch_attachments_test.go`（子会话附件回传主会话三路径）、
  `internal/llm/tool_image_test.go`（tool 消息带图的双 wire 编码 + 无图逐字节不变）、
  `internal/server/attachments_test.go` + `files_test.go`（端点文件卡、发送元数据落库）。
- 前端：`tests/attachment-display.test.mjs`（URL 构造/行解析/归一化/实时与回放同形状/
  渲染），`tests/attachments.test.mjs` 是发送侧（composer/队列/回滚）的既有钉子。
