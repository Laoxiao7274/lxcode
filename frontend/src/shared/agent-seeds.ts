// 可组装 Agent 域的种子数据（演示目录与名单——后端 Agent 注册表落地后
// 整体由后端数据替代；原型内存态）。类型见 agent-types.ts。
import type { AgentDef, ContextModuleSpec, McServerSpec, ToolSpec } from "./agent-types";

/** 主 Agent 的唯一工具：调用名单中的其他 Agent。 */
export const MAIN_TOOL: ToolSpec = {
  id: "agent_dispatch",
  desc: "调用名单中的其他 Agent 执行子任务（主 Agent 唯一的调度通道）",
  risk: "low",
  source: "builtin",
  params: [
    { name: "agent", type: "string", required: true, desc: "名单中的 Agent id" },
    { name: "task", type: "string", required: true, desc: "子任务描述与验收标准" },
    { name: "context", type: "string", desc: "给子 Agent 的背景信息" },
  ],
  doc: "主 Agent 唯一工具——调度通道。\n\n- task 描述必须自带**验收标准**：没有验收标准的任务不可验收\n- **并行**：一条消息里发多个 dispatch 调用，它们**真的并行跑**（子会话各自独立）；有前后依赖的才分多轮串行\n- 结果回来先验收再汇总，不合格的带着理由重派或自己说明",
};

/** 内置工具目录（与后端 tools 注册表对应）。 */
export const BUILTIN_TOOLS: ToolSpec[] = [
  {
    id: "read_file",
    desc: "按行读取文件（分页、256KB 上限）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "path", type: "string", required: true },
      { name: "offset", type: "int", desc: "起始行号" },
      { name: "limit", type: "int", desc: "行数（默认 2000）" },
      { name: "force", type: "bool", desc: "确实要重读原文时置 true（跳过同区间去重）" },
    ],
    doc: "按行输出，行号前缀（N→）。256KB 上限，二进制文件拒绝（魔数检测）。\n\nedit 前先 read 拿到精确的 old_string——这是精确替换工作流的第一步。\n\n**会话级去重**：同一会话里对同一文件用完全相同的 offset/limit 重读、且文件没被改过时只回一句摘要；确实要原文就加 force=true，或换区间读别的部分。",
  },
  {
    id: "search",
    desc: "纯 Go RE2 检索（files/content/count 三模式）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "pattern", type: "regex", required: true, desc: "RE2 正则" },
      { name: "mode", type: "enum", desc: "files / content / count" },
      { name: "path", type: "string", desc: "检索根目录" },
      { name: "glob", type: "string", desc: "文件名过滤" },
    ],
    doc: "纯 Go RE2，不经过 shell——无注入面。\n\n- files 模式：找文件名\n- content 模式：带上下文行\n- count 模式：只要计数",
  },
  {
    id: "web_search",
    desc: "联网搜索网页（多渠道，主渠道失败自动降级）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "query", type: "string", required: true, desc: "搜索查询词（自然语言问题即可）" },
      { name: "num_results", type: "int", desc: "结果条数（默认 5，上限 20）" },
      { name: "recency", type: "enum", desc: "时间范围：day / week / month / year" },
      { name: "domains", type: "array", desc: "限定域名；前缀 - 表示排除" },
    ],
    doc: "查最新信息、文档、报错、API 用法等可能过时的事实。\n\n- 不要用它搜本仓库代码（那用 search）\n- 多个渠道按主渠道优先自动降级；失败会说明是哪个渠道出的错\n- 「搜到 0 条」和「搜索失败」是两种结论——前者换关键词，后者如实报告\n\n渠道配置见「设置 → 网页搜索」。",
  },
  {
    id: "web_fetch",
    desc: "抓取网页正文（HTML 转文本，禁内网）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "url", type: "string", required: true, desc: "http/https 地址（先用 web_search 找到它）" },
      { name: "max_chars", type: "int", desc: "正文上限（默认 20000，上限 80000）" },
    ],
    doc: "web_search 只回标题与摘要，**要看全文用这个**。\n\n- 先用 web_search 找到地址，再抓正文\n- 只支持 http/https，**禁止访问本机与内网地址**（环回/私有网段/云元数据端点）\n- 正文超上限会截断（可调 max_chars）；纯 JS 渲染的页面可能抓不到正文\n- 二进制内容（图片/PDF）如实报类型，不灌乱码进上下文",
  },
  {
    id: "session_search",
    desc: "搜索历史会话内容",
    risk: "low",
    source: "builtin",
    params: [
      { name: "pattern", type: "string", required: true, desc: "搜索模式（正则）" },
      { name: "max", type: "int", desc: "最多返回条数（默认 30，上限 100）" },
      { name: "context", type: "int", desc: "每条命中前后各带几条相邻消息（默认 2，上限 5）" },
      { name: "role", type: "enum", desc: "只搜某个角色：user / assistant / tool" },
    ],
    doc: "搜历史会话内容，命中带**会话标题与时间**，并默认附前后各 2 条相邻消息。\n\n- 给「之前怎么处理过这类问题」提供依据——先查旧账再开新方\n- 带上下文是因为：命中行常常只是提问，「怎么修的」在它后面几条\n- role 过滤只作用于命中判定，上下文里仍能看到其它角色的行",
  },
  {
    id: "read_skill",
    desc: "读取技能模块的完整内容（提示词只列索引）",
    risk: "low",
    source: "builtin",
    params: [{ name: "id", type: "string", required: true, desc: "技能 id（提示词「可用技能」清单里的名字）" }],
    doc: "渐进披露：提示词只注入技能索引（id + 摘要），需要完整方法论时按 id 取全文。\n\n没在白名单里的技能读不到（提示词里看不到 = 不存在）。",
  },
  {
    id: "share_file",
    desc: "把工作目录里的文件共享到对话（图片缩略图/文件卡）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "path", type: "string", required: true, desc: "文件路径（相对会话工作目录；必须在工作目录之内）" },
      { name: "caption", type: "string", desc: "一句话说明（可选）：文件是什么、用户该看哪里" },
    ],
    doc: "只读语义：读源文件、复制进会话附件目录、渲染成附件卡——不修改任何文件。\n\n- 适合展示你刚生成的产物（图表、报告、导出数据、截图）\n- 图片（png/jpeg/webp/gif）渲染为可点击放大的缩略图，其余是可下载的文件卡\n- 单文件 ≤20MB；路径越出工作目录会被拒绝",
  },
  {
    id: "read_image",
    desc: "读取图片内容给视觉模型看（须声明「视觉」能力）",
    risk: "low",
    source: "builtin",
    params: [{ name: "path", type: "string", required: true, desc: "图片路径（相对会话工作目录；必须在工作目录之内）" }],
    doc: "只读语义：读工作目录里的图片（png/jpeg/webp/gif），作为视觉输入注入下一轮请求——模型直接描述与分析图里的内容。\n\n- 当前模型必须声明「视觉」能力，否则报人话错误\n- 给用户看图用 share_file（本工具只喂模型，不渲染给用户）\n- 单图 ≤5MB；路径越出工作目录会被拒绝",
  },
  {
    id: "merge_request",
    desc: "起一个合并进程（把本会话改动交给合并 Agent 汇总）",
    risk: "low",
    source: "builtin",
    params: [{ name: "target_branch", type: "string", desc: "目标分支（可选；默认 lxcode/integration）" }],
    doc: "起一个后台合并进程：任务体是内置的合并 Agent，在集成分支的专用工作树里把本会话分支的改动汇总进去。\n\n- 立刻返回任务 id；合并结束后经唤醒投递自动通告你\n- 未分组会话没有可合并的分支（先归入项目）\n- 同一会话同时只允许一个在跑的合并进程",
  },
  {
    id: "workspace_status",
    desc: "查询项目工作区状态（主检出改动、各会话分支、集成分支）",
    risk: "low",
    source: "builtin",
    params: [{ name: "project_id", type: "string", desc: "项目 id（可选；默认当前会话归属的项目）" }],
    doc: "只读汇总：主检出的未提交/未跟踪文件、各会话分支领先多少（有没有工作树、是否已并入主检出）、集成分支的领先/落后。\n\n- 用户问「本地改了哪些东西」用它\n- 全部只读，绝不动工作区\n- 未分组会话报「没有归属项目」",
  },
  {
    id: "workspace_sync",
    desc: "提交当前分支的改动并直接推送到 origin（可选）",
    risk: "high",
    source: "builtin",
    params: [
      { name: "message", type: "string", desc: "提交信息（可选；默认取最近一条用户消息首行）" },
      { name: "push", type: "bool", desc: "提交后是否把当前分支直接推送到远程 origin（默认 false）" },
    ],
    doc: "「帮我提交/推送」的执行链路：提交主检出当前分支 →（push=true 时）直接推送到 origin（不 force）。\n\n- 工作区干净则跳过提交（不报错）\n- 推送失败不影响已完成的提交（如实报错，提交不会回退）\n- **lxcode 本体仓库的推送被闸门拒绝**：本地 master 含历史逐轮检查点，发版走 `node scripts/push-release.mjs [version]`（提交照常）\n- 未分组会话报「没有归属项目」",
  },
  {
    id: "workspace_rollback",
    desc: "回滚主检出当前分支（上一个提交/会话起点/指定提交）",
    risk: "high",
    source: "builtin",
    params: [{ name: "target", type: "enum", desc: "last-turn（默认）/ session-start / 原始 commit hash" }],
    doc: "「回滚到上一次提交」的执行者：把主检出当前分支 reset --hard 到目标提交。\n\n- 轮末自动检查点已移除（2026-10 用户拍板）：只有经 workspace_sync 或手工提交过的位置可回滚，last-turn = 上一个提交（不是上一轮对话）\n- 工作区有未提交改动时拒绝（绝不静默丢弃手工改动）\n- 目标提交已合并进集成分支时警告：那部分要在集成分支上 revert 才能撤销\n- 绝不 push、绝不动集成分支与主检出\n- 执行前走确认门（文案列明丢弃哪些提交与文件）",
  },
  {
    id: "edit",
    desc: "精确替换（old_string 唯一匹配硬校验）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "path", type: "string", required: true },
      { name: "old_string", type: "string", required: true, desc: "必须唯一匹配" },
      { name: "new_string", type: "string", required: true },
    ],
    doc: "精确替换：old_string 在目标文件内必须**唯一**（0 或 >1 都报错并说明）。原子写——失败不落半截文件。",
  },
  {
    id: "write_file",
    desc: "全量覆盖写（覆盖确认 + 缩水守卫）",
    risk: "high",
    source: "builtin",
    params: [
      { name: "path", type: "string", required: true },
      { name: "content", type: "string", required: true, desc: "全量内容" },
    ],
    doc: "全量覆盖写。\n\n- 覆盖已有文件需确认 + 缩水守卫（新内容不足原文 50% 时警告）\n- 新建文件直接写",
  },
  {
    id: "bash",
    desc: "命令执行（超时 60s、输出 32KB 截断）",
    risk: "high",
    source: "builtin",
    params: [
      { name: "command", type: "string", required: true },
      { name: "timeout", type: "int", desc: "秒，上限 300" },
      { name: "stdin", type: "string", desc: "标准输入，≤64KB" },
    ],
    doc: "按 OS 选 shell（Windows 优先 Git Bash）。\n\n- 超时 60s（上限 300s）、输出 32KB 截断\n- 长命令建议先落盘成脚本再执行——可审查、可重放",
  },
  {
    id: "bash_readonly",
    desc: "执行只读 shell 命令（读数据：ls/grep/wc、git log、sqlite3 查询）",
    risk: "low",
    source: "builtin",
    params: [
      { name: "command", type: "string", required: true, desc: "只读命令（ls / cat / grep / find / wc / git log / sqlite3 的 SELECT 等）" },
      { name: "cwd", type: "string", desc: "工作目录（绝对或相对会话工作目录，可选）" },
      { name: "stdin", type: "string", desc: "标准输入，≤64KB" },
      { name: "timeout_sec", type: "int", desc: "秒，上限 300" },
    ],
    doc: "只读执行面：命令级白名单校验，写操作（rm/mv/重定向/git commit/git push/包管理/解释器脚本）一律拒绝并说明原因；命令替换、解释器等无法静态判定的结构同样拒绝（只读命令之间的管道可以）。\n\n- 适合读数据：查 SQLite、看 git 历史、统计文件规模\n- 改文件请交给代码 Agent（bash / edit / write_file）",
  },
  {
    id: "todo",
    desc: "任务清单全量写入（active 唯一性硬校验）",
    risk: "low",
    source: "builtin",
    params: [{ name: "items", type: "array", required: true, desc: "全量替换，active 唯一" }],
    doc: "任务清单全量写入；active 项唯一（硬校验）。\n\n多步任务的过程对齐——每完成一步更新状态，清单是唯一事实源。",
  },
  {
    id: "agent_read",
    desc: "查看 Agent 注册表（列表或单个完整定义，含提示词模板）",
    risk: "low",
    source: "builtin",
    params: [{ name: "id", type: "string", desc: "要读的 Agent id（可选；缺省 = 列出全部）" }],
    doc: "读 Agent 配置：不带 id 列出全部（id/名称/职责/白名单/技能），带 id 读单个完整定义（含模板全文）。\n\n- 修改 Agent 配置前先读现状——改之前知道它现在长什么样\n- 列表视图不含提示词全文（上下文考虑），要看全文带 id 单读",
  },
  {
    id: "agent_write",
    desc: "按 id 修改 Agent 配置（描述/模板/白名单/技能绑定，高危确认门）",
    risk: "high",
    source: "builtin",
    params: [
      { name: "id", type: "string", required: true, desc: "目标 Agent 的 id（不可改）" },
      { name: "desc", type: "string", desc: "新的职责描述（可选）" },
      { name: "prompt", type: "string", desc: "新的系统提示词模板全文（可选；不能为空）" },
      { name: "tools", type: "array", desc: "新的工具白名单（可选；全量替换，不能清空，名字必须真实存在）" },
      { name: "skills", type: "array", desc: "新的技能绑定（可选；全量替换，可为空）" },
    ],
    doc: "字段级更新：只写提供的字段，其余不动；id 不可改。\n\n- **高危**：改动落库即生效——该 Agent 下次被派发就按新配置运行\n- 工具白名单增删直接改变目标 Agent 能做什么（加写工具 = 它将能改动文件）\n- 硬校验：白名单不能清空、工具名必须真实存在、模板不能为空、主 Agent 不可改\n- 改之前先 agent_read 摸现状，向用户确认意图",
  },
];

/** 第三方工具目录——可插拔契约的演示：经进程边界接入，权限由 Harness 掌握。 */
export const THIRD_PARTY_TOOLS: ToolSpec[] = [
  {
    id: "ripgrep",
    desc: "Rust 检索二进制——大仓库全文搜索",
    risk: "low",
    source: "binary",
    // 与后端种子一致：command 是可运行的模板（{param} 占位，可选参数缺省就丢
    // 掉整个 token）——空 command 的工具不会进注册表，模型只会说「注册表没有」
    command: "rg -n --no-heading --color=never --glob={glob} {pattern} {path}",
    params: [
      { name: "pattern", type: "regex", required: true },
      { name: "path", type: "string", desc: "检索根目录（默认会话工作目录）" },
      { name: "glob", type: "string", desc: "文件名过滤" },
    ],
    doc: "Rust 检索二进制，经进程边界接入（Go 主刀、Rust 武器库）。\n\n大仓库全文搜索比内置 search 快一个量级；参数与 rg CLI 对齐。",
  },
  {
    id: "browser",
    desc: "Chromium 面板驱动（页面勘察与截图）",
    risk: "low",
    source: "binary",
    params: [
      { name: "url", type: "string", required: true },
      { name: "action", type: "enum", desc: "navigate / snapshot / click" },
    ],
    doc: "Chromium 面板驱动。\n\n三段式：navigate 导航 → snapshot 快照定位 → click 操作。\n\n**未配置**：还没有对应的驱动二进制——填上 command 才会进注册表。",
  },
  {
    id: "mcp:filesystem",
    desc: "MCP 文件系统服务（跨进程文件操作）",
    risk: "high",
    source: "mcp",
    server: "filesystem",
    params: [
      { name: "op", type: "enum", required: true, desc: "read / list / write" },
      { name: "path", type: "string", required: true },
    ],
    doc: "MCP 文件系统服务。\n\nop 枚举 read / list / write；写操作高危——走确认门。",
  },
  {
    id: "mcp:web-search",
    desc: "MCP 网页检索服务——公网搜索与摘要",
    risk: "low",
    source: "mcp",
    server: "web-search",
    params: [
      { name: "query", type: "string", required: true, desc: "检索词" },
      { name: "limit", type: "int", desc: "结果条数上限" },
    ],
    doc: "MCP 网页检索服务。\n\n公网搜索 + 结果摘要；只读无副作用——低危自动执行。",
  },
  {
    id: "mcp:sqlite",
    desc: "MCP SQLite 服务——会话库之外的独立数据查询",
    risk: "low",
    source: "mcp",
    server: "sqlite",
    params: [
      { name: "db", type: "string", required: true, desc: "数据库文件路径" },
      { name: "sql", type: "string", required: true, desc: "只读查询" },
    ],
    doc: "MCP SQLite 服务。\n\n只读查询通道（SELECT）；写操作走后端自己的存储——不共用。",
  },
];

/** MCP 服务器拓展（第四版块的种子——与 mcp: 工具的 server 字段对应；
 *  形态对齐 mcpServers 事实标准：command + args + env / url）。 */
export const MC_SERVERS: McServerSpec[] = [
  {
    id: "filesystem",
    desc: "官方文件系统服务——读写/list 跨进程文件操作",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/"],
    env: {},
    url: "",
    enabled: true,
  },
  {
    id: "web-search",
    desc: "网页检索服务——公网搜索与摘要",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@mcp/web-search-server"],
    env: {},
    url: "",
    enabled: true,
  },
  {
    id: "sqlite",
    desc: "独立数据只读查询（SELECT 通道）",
    transport: "stdio",
    command: "uvx",
    args: ["mcp-server-sqlite"],
    env: {},
    url: "",
    enabled: false,
  },
  {
    id: "remote-demo",
    desc: "远程 SSE 服务示例（演示 sse 传输形态）",
    transport: "sse",
    command: "",
    args: [],
    env: {},
    url: "https://example.com/mcp/sse",
    enabled: false,
  },
];

/** 演示态的 MCP 运行期状态（live 模式下后端是事实源）。
 *
 * 演示三种形态各一，让界面状态可被看见：连上（filesystem）、连不上
 * （web-search——命令不存在，这正是真实世界的常见形态）、已停止（停用的那些）。
 * 类型用宽松形状而不是 McpRuntime：本文件不 import agents.tsx（那会成环——
 * agents.tsx 从这里取种子）。 */
export const MC_RUNTIME: Record<string, { status: string; toolCount: number; lastError: string; stderr: string }> = {
  filesystem: { status: "connected", toolCount: 4, lastError: "", stderr: "" },
  "web-search": {
    status: "error",
    toolCount: 0,
    lastError: "MCP web-search initialize 失败: 启动 MCP 服务器进程失败: exec: \"npx\": executable file not found in %PATH%",
    stderr: "",
  },
  sqlite: { status: "stopped", toolCount: 0, lastError: "", stderr: "" },
  "remote-demo": { status: "stopped", toolCount: 0, lastError: "", stderr: "" },
};

/** 内置拓展（种子——运行时名单是 Provider 状态，用户可增删自定义条目）。 */
export const CONTEXT_MODULES: ContextModuleSpec[] = [
  {
    id: "plan-execute-verify",
    kind: "process",
    desc: "规划 → 执行 → 验证：先出方案再动手，完成后验证再交付",
    body: [
      "# 规划 → 执行 → 验证",
      "",
      "任何非平凡任务先出**方案**再动手，完成后**验证**再交付。",
      "",
      "## 规划",
      "",
      "- 把任务拆成可验证的步骤，每步写清验收标准",
      "- 方案有取舍时，先说明取舍再执行",
      "",
      "## 执行",
      "",
      "- 按步骤推进，偏移即时修正",
      "- 中途发现方案问题，回到规划重新拆解，不硬闯",
      "",
      "## 验证",
      "",
      "- 对照验收标准逐条核对",
      "- 构建测试必须实际运行，不凭推断说「应该没问题」",
      "- 验证不过不算完成——回到执行修复",
    ].join("\n"),
  },
  {
    id: "research-first",
    kind: "process",
    desc: "信息不足先检索，再进实现——不做没有依据的假设",
    body: [
      "# 检索先行",
      "",
      "信息不足先检索，再进实现——不做没有依据的假设。",
      "",
      "- 涉及现状的判断（代码行为 / 配置值 / 文档约定）先读再改",
      "- 结论必须可追溯到来源：文件与行号、命令输出",
      "- 检索不到就明说「未找到」，不编造",
    ].join("\n"),
  },
  {
    id: "minimal-change",
    kind: "process",
    desc: "变更最小化：只改达成目标必需的部分，不顺手重构",
    body: [
      "# 变更最小化",
      "",
      "只改达成目标必需的部分。",
      "",
      "- 动手前先确认目标边界，不顺手重构",
      "- 发现范围外的问题：记录并上报，不在本次改动里修",
      "- 每一步改动可独立解释、可回退",
    ].join("\n"),
  },
  {
    id: "frontend-design",
    kind: "skill",
    desc: "有辨识度的视觉设计方向",
    body: [
      "# Frontend Design",
      "",
      "有辨识度的视觉方向，拒绝模板脸。",
      "",
      "## 原则",
      "",
      "- 先定设计意图再选风格——反推而非套用",
      "- 排版层级清晰：字号 / 字重 / 间距只服务于信息优先级",
      "- 色彩克制：主色一到两个，语义色点到为止",
      "",
      "## 检查",
      "",
      "- 视觉密度与场景匹配（工具型紧凑 / 内容型舒展）",
      "- 深浅模式对比度过 WCAG AA",
    ].join("\n"),
  },
  {
    id: "gsap",
    kind: "skill",
    desc: "GSAP 动画编排与性能",
    body: [
      "# GSAP 动画编排",
      "",
      "时间轴思维，动效服务信息而非炫技。",
      "",
      "- 入场交错 stagger 0.03–0.06s，时长 0.2–0.5s",
      "- 退场比入场快两成：power2.out 进 / power2.in 出",
      "- 动画结束 clearProps——残留内联 transform 会把弹层 z-index 困住",
      "- 尊重 prefers-reduced-motion，门控统一走 motionAllowed",
    ].join("\n"),
  },
  {
    id: "ui-ux-pro-max",
    kind: "skill",
    desc: "UI/UX 规范与设计系统检索",
    body: [
      "# UI/UX Pro Max",
      "",
      "UI/UX 规范与设计系统检索。",
      "",
      "- 布局 / 对比 / 触达面积有硬指标：对比 4.5:1、触达 44px",
      "- 表单错误就地展示，不只在顶部",
      "- 弹层三件套：Esc 关闭、点外关闭、焦点回收",
    ].join("\n"),
  },
  {
    id: "windows-app-forensics",
    kind: "skill",
    desc: "Windows 桌面应用故障诊断",
    body: [
      "# Windows 应用取证",
      "",
      "桌面应用故障诊断。",
      "",
      "- 进程树取证：taskkill /T 的树杀语义、Job Object 连带终止",
      "- 安装器卡死先查进程检测：路径前缀匹配与名字匹配的差异",
      "- 证据三件套：文件头字节 / 哈希 / 在线 URL 探活",
    ].join("\n"),
  },
  {
    id: "shadcn",
    kind: "skill",
    desc: "组件库工程与注册表",
    body: [
      "# shadcn/ui",
      "",
      "组件库工程与注册表。",
      "",
      "- 组件按 registry 分发，不整包引入",
      "- 主题走 CSS 变量，不 fork 组件改样式",
      "- 升级以 diff 合并，不锁定版本",
    ].join("\n"),
  },
  {
    id: "merge-verify",
    kind: "process",
    desc: "合并 → 验证：先看两边改动再合并，冲突逐个解决，合并后跑构建测试",
    body: [
      "# 合并 → 验证",
      "",
      "在集成分支的工作树里把源分支的改动汇总进来。",
      "",
      "## 先看再合",
      "",
      "- 合并前先看两边改了什么（git log / git diff 源分支与目标分支）",
      "- 明白两边的意图再动手，不盲目 merge",
      "",
      "## 解冲突",
      "",
      "- 冲突逐个解决并说明取舍：为什么保留这一边，另一边的意图如何被满足",
      "- 绝不用 --force、-X theirs / -X ours 掩盖冲突——那是把别人的改动悄悄丢掉",
      "",
      "## 验证",
      "",
      "- 合并后跑构建与测试，按仓库的验收标准验证",
      "- 失败就把集成分支恢复原状并如实报告，绝不谎报成功",
      "- 不 push、不动主检出、不丢弃任何人的改动",
    ].join("\n"),
  },
];

/** 演示名单：主 Agent + 四个不同职责/权限面的组装示例。 */
export function seedAgents(): AgentDef[] {
  return [
    {
      id: "main",
      name: "主 Agent",
      isMain: true,
      color: "#0d0d0d",
      model: "MYT-Deep",
      desc: "决策与分派中枢：理解意图、拆解任务、调用名单中的 Agent 并验收汇总。不直接执行任务。",
      prompt: "",
      tools: [MAIN_TOOL.id, "merge_request", "workspace_status", "workspace_sync", "workspace_rollback"],
      workflow: "plan-execute-verify",
      skills: [],
      delegates: ["coder", "researcher", "tester", "reviewer", "ops", "lxagent"],
      approval: "confirm",
      enabled: true,
    },
    {
      id: "coder",
      name: "代码 Agent",
      color: "#3b82f6",
      model: "MYT-Deep",
      // 职责边界（2026-12 用户拍板，与后端种子 agents_seeds.go 同款）：**只写代码，
      // 不跑测试**——测试与验证归测试 Agent。desc 是主 Agent 的选人信号，写着
      // 「跑测试」会让它把验证任务也派给 coder；只保留「改完确认能编译」这条底线。
      desc: "写代码：新增功能、修 bug、重构与改配置（只改文件：测试交给测试 Agent、查资料交给调研 Agent），能把产物文件共享到对话、能读图片内容（视觉）。",
      prompt: "你是代码 Agent。改动前先读相关代码，遵守仓库规范；每步改动可解释、可回退。你只负责实现与重构，**不跑测试**——验证（跑构建与测试、补用例、验收）是测试 Agent 的职责，把改动与改动说明如实交回即可。唯一的自检底线：改完确认代码能编译、无明显语法错误（如 go build / tsc 级别的编译检查），不要让连编译都过不了的代码流到下游。绝不执行 git push（含 --force）：推送只由用户经 workspace_sync 发起；lxcode 本体仓库的发版只走 scripts/push-release.mjs。",
      tools: ["read_file", "search", "read_image", "edit", "write_file", "bash", "todo", "share_file"],
      workflow: "minimal-change",
      skills: ["frontend-design", "gsap"],
      delegates: [],
      approval: "confirm",
      enabled: true,
    },
    {
      id: "researcher",
      name: "调研 Agent",
      color: "#10a37f",
      model: "MYT",
      // desc 是**主 Agent 的选人信号**（可委派名单按它逐字生成）：漏写「联网搜索」，
      // 主 Agent 就不知道「查外部资料」该派给谁——工具与白名单都到位，能力照样等于不存在。
      // bash_readonly（2026-12 用户反馈）：调研要查本地数据（sqlite3 聚合、git log、
      // wc 统计），没有执行面就只能答「做不到」；完整 bash 又太危险，所以给只读执行面。
      desc: "只读调研：检索代码、读文件、查数据库（只读 bash_readonly：sqlite3 查询/git log/wc 等）、联网搜索与抓取网页正文、梳理跨文件脉络，只给结论与出处，不改任何文件。",
      prompt: "你是调研 Agent。只做检索与信息整理，**不修改任何文件、不执行构建测试、不提交推送**。\n\n职责边界：\n- 结论必须带依据：文件路径 + 行号、命令输出或文档链接；查不到就如实说「未找到」，不编造也不推测。\n- bash_readonly 是只读执行面：可以跑读取类命令（ls/cat/grep/find/wc、git log/diff/status、sqlite3 的 SELECT 查询等），写操作（rm/mv/重定向/git commit/git push/包管理/解释器脚本）会被工具直接拒绝。\n- 被拒绝时不要绕过：把「需要什么改动」写进结论，交回主 Agent 派给代码 Agent（coder）或测试 Agent（tester）。",
      tools: ["read_file", "search", "session_search", "web_search", "web_fetch", "ripgrep", "bash_readonly"],
      workflow: "research-first",
      skills: [],
      delegates: [],
      approval: "strict",
      enabled: true,
    },
    {
      id: "tester",
      name: "测试 Agent",
      color: "#0ea5e9",
      model: "MYT-Deep",
      // 与代码 Agent 的分工（2026-12 用户拍板）：测试与验证归这里——coder 只写
      // 代码不跑测试，所以描述里写明「代码 Agent 交回的改动由你验收」。
      desc: "验证与测试：跑构建与测试（代码 Agent 交回的改动由你验收）、按验收标准补用例，如实回传失败与复现命令（不改实现；查资料交给调研 Agent），能把产物文件共享到对话、能读图片内容（视觉）。",
      prompt: "你是测试 Agent，负责验证与测试（代码 Agent 只写代码、不跑测试，改动交回后由你跑构建与测试验收）。跑构建与测试，断言行为与契约而不是实现细节；失败如实回传（关键输出 + 复现命令）。绝不为了让测试变绿而删除、跳过或弱化断言——测试与实现冲突时先判断谁对：实现错了改实现，需求变了先改方案；改实现超出「补用例/修测试」的范围时，如实交回主 Agent 派给代码 Agent，不自己重写功能。只读调研（查资料、查历史、梳理跨文件脉络）也不是你的活，派给调研 Agent。绝不执行 git push（含 --force）：推送只由用户经 workspace_sync 发起。",
      tools: ["read_file", "search", "read_image", "edit", "write_file", "bash", "todo", "share_file"],
      workflow: "plan-execute-verify",
      skills: [],
      delegates: [],
      approval: "confirm",
      enabled: true,
    },
    {
      id: "reviewer",
      name: "审查 Agent",
      color: "#7c3aed",
      model: "MYT-Deep",
      desc: "只读审查：方案与 diff 复核，提出问题与风险，不改文件。",
      prompt: "你是审查 Agent。严格模式下工作：只读不写，逐条给出问题、依据与建议，不放过边界情况。",
      tools: ["read_file", "search", "session_search"],
      workflow: "",
      skills: [],
      delegates: [],
      approval: "strict",
      enabled: true,
    },
    {
      // lxagent：专门维护其他 Agent 配置的内置 Agent（与后端种子对齐）——
      // 改提示词模板/工具白名单/技能绑定，改动过确认门、落库即生效。
      id: "lxagent",
      name: "lxagent",
      color: "#7c3aed",
      model: "MYT-Deep",
      desc: "改 Agent 配置：查看与修改系统提示词模板、工具白名单与技能绑定（只改配置，不写业务代码、不跑测试），改动经确认门落库，对后续派发生效。",
      prompt: "你是 lxagent，专门维护其他 Agent 的配置：修改系统提示词模板、工具白名单与技能绑定。\n\n职责边界：\n- 只改 Agent 配置，不替用户做业务决策，也不亲自执行目标 Agent 的业务任务（不写业务代码、不跑测试、不做调研——那些派给 coder / tester / researcher）。\n- 动手前先 agent_read 摸清现状，向用户确认意图（改哪个 Agent、改成什么、为什么）再调用 agent_write。\n- agent_write 是高危操作（影响之后所有派发）：白名单加写工具会让目标 Agent 获得改动文件的能力，撤技能绑定会改变它的方法论——把后果说清让用户知情。\n- 每次改动后用 agent_read 复读确认落库结果，向用户回报改了什么。\n\n硬约束（工具层强制，勿试图绕过）：id 不可改、白名单不能清空、模板不能为空、工具名必须是工具目录里真实存在的、主 Agent 的配置不可经此修改。",
      tools: ["agent_read", "agent_write", "read_file", "search", "session_search", "ask_user", "todo"],
      workflow: "minimal-change",
      skills: [],
      delegates: [],
      approval: "confirm",
      enabled: true,
    },
    {
      // 合并进程拉起的**内置 Agent**（不是主 Agent 的委派对象——不进 delegates）。
      id: "merger",
      name: "合并 Agent",
      color: "#db2777",
      model: "MYT-Deep",
      desc: "把会话分支的改动汇总到集成分支：处理冲突、跑构建测试，如实报告结果。",
      prompt: "你是合并 Agent。在集成分支的专用工作树里工作，把源会话分支的改动合并到目标分支。合并纪律：先看两边改了什么（git log / git diff）再合并；冲突必须逐个解决并说明取舍；绝不用 --force、-X theirs / -X ours 掩盖冲突；合并后跑构建与测试；失败就把集成分支恢复原状并如实报告，绝不谎报成功；不 push、不动主检出、不丢弃任何人的改动。",
      tools: ["read_file", "search", "edit", "write_file", "bash"],
      workflow: "merge-verify",
      skills: [],
      delegates: [],
      approval: "auto",
      enabled: true,
    },
    {
      id: "ops",
      name: "运维 Agent",
      color: "#c2410c",
      model: "MYT",
      desc: "发布与运维操作（默认停用——命令执行面大，需要时再启用）。",
      prompt: "你是运维 Agent。操作前核对环境与影响面，高危命令必须给出理由与回退路径。",
      tools: ["bash", "todo"],
      workflow: "",
      skills: [],
      delegates: [],
      approval: "confirm",
      enabled: false,
    },
  ];
}

/** 名单可选标识色（主 Agent 的黑不在其中——身份固定）。 */
export const AGENT_COLORS = ["#3b82f6", "#10a37f", "#c2410c", "#7c3aed", "#db2777", "#0ea5e9", "#65a30d", "#64748b"];

/** 新建目录条目的空白起点：kind 取所在页签的默认（模板/技能）。 */
export function blankModule(kind: "process" | "skill"): ContextModuleSpec {
  return { id: "", desc: "", kind, body: "" };
}

/** 组装新 Agent 的空白起点：色板取未占用色，模型沿用主 Agent 的绑定。 */
export function blankAgent(mainModel: string, usedColors: string[]): AgentDef {
  const color = AGENT_COLORS.find((c) => !usedColors.includes(c)) ?? AGENT_COLORS[0];
  return {
    id: crypto.randomUUID(),
    name: "",
    desc: "",
    color,
    model: mainModel,
    prompt: "",
    tools: [],
    workflow: "",
    skills: [],
    delegates: [],
    approval: "confirm",
    enabled: true,
  };
}
