// 可组装 Agent 域的种子数据（演示目录与名单——后端 Agent 注册表落地后
// 整体由后端数据替代；原型内存态）。类型见 agent-types.ts。
import type { AgentDef, ContextModuleSpec, McServerSpec, ToolSpec } from "./agent-types";

/** 主 Agent 的唯一工具：调用名单中的其他 Agent。 */
export const MAIN_TOOL: ToolSpec = {
  id: "agent.dispatch",
  desc: "调用名单中的其他 Agent 执行子任务（主 Agent 唯一的调度通道）",
  risk: "low",
  source: "builtin",
  params: [
    { name: "agent", type: "string", required: true, desc: "名单中的 Agent id" },
    { name: "task", type: "string", required: true, desc: "子任务描述与验收标准" },
    { name: "context", type: "string", desc: "给子 Agent 的背景信息" },
  ],
  doc: "主 Agent 唯一工具——调度通道。\n\n- task 描述必须自带**验收标准**：没有验收标准的任务不可验收\n- 一次一个子任务；并行需求拆成多次 dispatch\n- 结果回来先验收再汇总，不合格的带着理由重派或自己说明",
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
    ],
    doc: "按行输出，行号前缀（N→）。256KB 上限，二进制文件拒绝（魔数检测）。\n\nedit 前先 read 拿到精确的 old_string——这是精确替换工作流的第一步。",
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
    id: "session_search",
    desc: "搜索历史会话内容",
    risk: "low",
    source: "builtin",
    params: [
      { name: "query", type: "string", required: true },
      { name: "limit", type: "int", desc: "结果条数上限" },
    ],
    doc: "搜历史会话内容，命中带会话标题与时间。\n\n给「之前怎么处理过这类问题」提供依据——先查旧账再开新方。",
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
    id: "todo",
    desc: "任务清单全量写入（active 唯一性硬校验）",
    risk: "low",
    source: "builtin",
    params: [{ name: "items", type: "array", required: true, desc: "全量替换，active 唯一" }],
    doc: "任务清单全量写入；active 项唯一（硬校验）。\n\n多步任务的过程对齐——每完成一步更新状态，清单是唯一事实源。",
  },
];

/** 第三方工具目录——可插拔契约的演示：经进程边界接入，权限由 Harness 掌握。 */
export const THIRD_PARTY_TOOLS: ToolSpec[] = [
  {
    id: "ripgrep",
    desc: "Rust 检索二进制——大仓库全文搜索",
    risk: "low",
    source: "binary",
    params: [
      { name: "pattern", type: "regex", required: true },
      { name: "path", type: "string", desc: "检索根目录" },
      { name: "glob", type: "string", desc: "文件名过滤" },
      { name: "max_results", type: "int", desc: "结果条数上限" },
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
    doc: "Chromium 面板驱动。\n\n三段式：navigate 导航 → snapshot 快照定位 → click 操作。",
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
      tools: [MAIN_TOOL.id],
      workflow: "plan-execute-verify",
      skills: [],
      delegates: ["coder", "researcher", "reviewer", "ops"],
      approval: "confirm",
      enabled: true,
    },
    {
      id: "coder",
      name: "代码 Agent",
      color: "#3b82f6",
      model: "MYT-Deep",
      desc: "编码实现与重构：读写代码、跑构建测试，产出可验证的改动。",
      prompt: "你是代码 Agent。改动前先读相关代码，遵守仓库规范；每步改动可解释、可回退，构建测试通过才算完成。",
      tools: ["read_file", "search", "edit", "write_file", "bash", "todo"],
      workflow: "minimal-change",
      skills: ["frontend-design", "gsap"],
      delegates: [],
      approval: "confirm",
      enabled: true,
    },
    {
      id: "researcher",
      name: "检索 Agent",
      color: "#10a37f",
      model: "MYT",
      desc: "资料与代码库勘察：全文检索、历史会话与网页信息收集。",
      prompt: "你是检索 Agent。只做检索与信息整理，给出来源与出处；不改任何文件，结论不确定就明说。",
      tools: ["search", "session_search", "ripgrep", "browser"],
      workflow: "research-first",
      skills: [],
      delegates: [],
      approval: "auto",
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
