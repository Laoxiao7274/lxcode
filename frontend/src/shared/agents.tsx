// 可组装 Agent 名单（原型）：Agent = 身份 + 模型 + 工具白名单 + 上下文
// （流程模块单选 / 技能模块多选 / 自定义段）+ 委派 + 权限默认的组合单元。
// 两类（2026-09-17 用户拍板）：主 Agent = 唯一调度者（不可被委派，默认
// 委派名单可配置 + 会话内可收窄）；子 Agent = 纯执行者（不可委派）——
// 委派深度恒为 1，环与借手提权从结构上不存在。内存态（刷新重置）——
// 后端 Agent 注册表落地前的 UI 原型。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useSettings } from "./settings";

/** 工具参数（详情层的展示数据——对齐后端工具的参数面）。 */
export interface ToolParam {
  name: string;
  type: string;
  required?: boolean;
  desc?: string;
}

/** 工具目录条目（内置与第三方统一形状——第三方经进程边界接入，标注来源）。 */
export interface ToolSpec {
  id: string;
  desc: string;
  /** 风险分级（与后端 tools 注册表对齐：低危自动执行 / 高危确认门）。 */
  risk: "low" | "high";
  /** 来源：内置注册表 / 外部二进制 / MCP。 */
  source: "builtin" | "binary" | "mcp";
  /** 参数面（详情层展示）。 */
  params?: ToolParam[];
  /** 扩展文档（markdown——工具的完整说明；详情层渲染）。 */
  doc?: string;
  /** 用户导入条目（可删除）；内置条目只读。导入格式见 shared/tool-import.ts。 */
  custom?: boolean;
  /** MCP 工具的来源服务器 id（source=mcp 时有——MCP 版块按服务器聚合）。 */
  server?: string;
  /** 运行命令（source=binary 的自定义工具——参数占位 {name}，
   *  如 `rg {pattern} {path}`；后端化时按模板填充后 spawn）。 */
  command?: string;
  /** 运行命令的固定示例参数（调用演示——后端化时给模型看的用法）。 */
  example?: string;
  /** 程序包文件名（zip/exe 上传——原型存声明；后端化时落盘
   *  plugins/ 目录并解压找入口）。 */
  packageFile?: string;
}

/** MCP 服务器（目录第四版块的条目）：接入单元——服务器注册后暴露的
 *  能力（工具）进工具目录（source=mcp + server 指回）。 */
export interface McServerSpec {
  id: string;
  name: string;
  desc: string;
  /** 启动命令或 URL（stdio / SSE——后端化时的真实接入面）。 */
  command: string;
  enabled: boolean;
  /** 用户自建（可编辑/删除）；演示种子只读。 */
  custom?: boolean;
}

/** 组装出的 Agent 定义（名单条目；运行实例是后续内核的事）。 */
export interface AgentDef {
  id: string;
  name: string;
  desc: string;
  /** 标识色（名单卡与输入区选择器的圆点）。 */
  color: string;
  /** 绑定模型（模型注册表条目 id；空 = 未绑定）。 */
  model: string;
  /** 工具白名单（目录 id）。主 Agent 固定只含 MAIN_TOOL。 */
  tools: string[];
  /** 选中的流程模块（目录 id，单选——工作方式是原子单元；空 = 无流程。
   *  缺合适流程就去补一个完整模块，不靠多个拼装。字段名用 workflow：
   *  与 Node 全局 process 撞形会让边界守卫误报（属性读取与 process.env
   *  结构上无法区分）。 */
  workflow: string;
  /** 选中的技能模块（目录 id，多选——可插拔注入）。 */
  skills: string[];
  /** 主 Agent 的默认委派名单（子 Agent id；子 Agent 恒空——不可委派）。
   *  停用的子 Agent 不参与分派；会话内可临时收窄（sessionDelegates 覆盖）。 */
  delegates: string[];
  /** 权限默认档（发送时仍可覆盖——与 chat.send 的 approval 同值域）。 */
  approval: "auto" | "confirm" | "strict";
  enabled: boolean;
  /** 主 Agent（调度中枢）：唯一可委派者，不可被委派。 */
  isMain?: boolean;
  /** 自定义上下文（私有自由段——拼在协议与模块之后；区别于
   *  Harness 固定的协议层与可插拔的模块层）。 */
  prompt: string;
  /** 定制协议（空 = 用内置默认——四层组合的第一层；主 Agent 的调度
   *  协议 / 子 Agent 的执行协议。用户可整段替换：改了就是 Agent 的
   *  「宪法修正案」，拼在模块与自定义段之前）。保存时与默认文本相同
   *  则不存（避免每个 Agent 带一份拷贝）。 */
  protocol?: string;
}

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

/** MCP 服务器目录（第四版块的种子——与 mcp: 工具的 server 字段对应）。 */
const MC_SERVERS: McServerSpec[] = [
  {
    id: "filesystem",
    name: "filesystem",
    desc: "官方文件系统服务——读写/list 跨进程文件操作",
    command: "npx -y @modelcontextprotocol/server-filesystem /",
    enabled: true,
  },
  {
    id: "web-search",
    name: "web-search",
    desc: "网页检索服务——公网搜索与摘要",
    command: "npx -y @mcp/web-search-server",
    enabled: true,
  },
  {
    id: "sqlite",
    name: "sqlite",
    desc: "独立数据只读查询（SELECT 通道）",
    command: "uvx mcp-server-sqlite",
    enabled: false,
  },
];

/** 上下文模块目录条目：可插拔的上下文块——流程（工作方式规范，如
 *  「规划→执行→验证」）与技能（领域知识方法）两类，与工具白名单同款
 *  交互（目录 + 勾选注入）；不授予工具权限。body 是注入 Agent 上下文的
 *  完整 markdown 文档（类 SKILL.md——详情层渲染，可能较长）。 */
export interface ContextModuleSpec {
  id: string;
  desc: string;
  kind: "process" | "skill";
  /** 模块正文（markdown——实际注入 Agent 上下文的内容）。 */
  body: string;
  /** 用户自建条目（可编辑/删除）；内置条目只读。 */
  custom?: boolean;
}

/** 内置目录（种子——运行时名单是 Provider 状态，用户可增删自定义条目）。 */
const CONTEXT_MODULES: ContextModuleSpec[] = [
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

/** 名单可选标识色（主 Agent 的黑不在其中——身份固定）。 */
export const AGENT_COLORS = ["#3b82f6", "#10a37f", "#c2410c", "#7c3aed", "#db2777", "#0ea5e9", "#65a30d", "#64748b"];

/** 演示名单：主 Agent + 四个不同职责/权限面的组装示例。 */
function seedAgents(): AgentDef[] {
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

interface AgentsValue {
  agents: AgentDef[];
  addAgent: (def: AgentDef) => void;
  updateAgent: (def: AgentDef) => void;
  removeAgent: (id: string) => void;
  /** 输入区当前选用的 Agent（原型只存 UI 态——协议接入是后续内核的事）。 */
  activeAgentId: string;
  setActiveAgentId: (id: string) => void;
  /** 本次会话的委派覆盖（null = 跟随主 Agent 的名单默认；
   *  新会话/切换会话时清掉——由 App 订阅 sessionChanged 调 reset）。 */
  sessionDelegates: string[] | null;
  setSessionDelegates: (list: string[] | null) => void;
  resetSessionDelegates: () => void;
  /** 上下文模块目录（运行时状态：内置种子 + 用户自建条目）。
   *  Agent 组装的 chips 与目录页都读这里——单一事实源。 */
  modules: ContextModuleSpec[];
  addModule: (mod: ContextModuleSpec) => void;
  updateModule: (mod: ContextModuleSpec) => void;
  removeModule: (id: string) => void;
  /** 工具目录（运行时状态：内置+第三方种子 + 导入/表单创建条目）。
   *  导入走固定格式 v1（shared/tool-import.ts 的 parseToolImport 校验）。 */
  tools: ToolSpec[];
  addTools: (tools: ToolSpec[]) => void;
  updateTool: (tool: ToolSpec) => void;
  removeTool: (id: string) => void;
  /** MCP 服务器目录（第四版块——接入单元；能力以 source=mcp 工具进工具目录）。 */
  mcpServers: McServerSpec[];
  addMcServer: (server: McServerSpec) => void;
  updateMcServer: (server: McServerSpec) => void;
  removeMcServer: (id: string) => void;
}

const Ctx = createContext<AgentsValue | null>(null);

export function AgentsProvider({ children }: { children: ReactNode }) {
  const [agents, setAgents] = useState<AgentDef[]>(seedAgents);
  const [activeAgentId, setActiveAgentId] = useState("main");
  const [sessionDelegates, setSessionDelegates] = useState<string[] | null>(null);
  const [modules, setModules] = useState<ContextModuleSpec[]>(CONTEXT_MODULES);
  const [tools, setTools] = useState<ToolSpec[]>(() => [...BUILTIN_TOOLS, ...THIRD_PARTY_TOOLS]);

  const addAgent = useCallback((def: AgentDef) => setAgents((list) => [...list, def]), []);
  const updateAgent = useCallback(
    (def: AgentDef) => setAgents((list) => list.map((a) => (a.id === def.id ? def : a))),
    [],
  );
  const removeAgent = useCallback((id: string) => {
    setAgents((list) => list.filter((a) => a.id !== id));
    // 删的是当前选用 → 回落主 Agent（入口永远存在）
    setActiveAgentId((cur) => (cur === id ? "main" : cur));
  }, []);
  const resetSessionDelegates = useCallback(() => setSessionDelegates(null), []);
  const addModule = useCallback((mod: ContextModuleSpec) => setModules((list) => [...list, mod]), []);
  const updateModule = useCallback(
    (mod: ContextModuleSpec) => setModules((list) => list.map((x) => (x.id === mod.id ? mod : x))),
    [],
  );
  const removeModule = useCallback(
    (id: string) => setModules((list) => list.filter((x) => x.id !== id)),
    [],
  );
  const addTools = useCallback((list: ToolSpec[]) => setTools((cur) => [...cur, ...list]), []);
  const updateTool = useCallback(
    (tool: ToolSpec) => setTools((cur) => cur.map((t) => (t.id === tool.id ? tool : t))),
    [],
  );
  const removeTool = useCallback(
    (id: string) => setTools((cur) => cur.filter((t) => t.id !== id)),
    [],
  );
  const [mcpServers, setMcServers] = useState<McServerSpec[]>(MC_SERVERS);
  const addMcServer = useCallback((s: McServerSpec) => setMcServers((list) => [...list, s]), []);
  const updateMcServer = useCallback(
    (s: McServerSpec) => setMcServers((list) => list.map((x) => (x.id === s.id ? s : x))),
    [],
  );
  const removeMcServer = useCallback(
    (id: string) => setMcServers((list) => list.filter((x) => x.id !== id)),
    [],
  );

  const value = useMemo(
    () => ({
      agents, addAgent, updateAgent, removeAgent,
      activeAgentId, setActiveAgentId,
      sessionDelegates, setSessionDelegates, resetSessionDelegates,
      modules, addModule, updateModule, removeModule,
      tools, addTools, updateTool, removeTool,
      mcpServers, addMcServer, updateMcServer, removeMcServer,
    }),
    [
      agents, addAgent, updateAgent, removeAgent,
      activeAgentId, sessionDelegates, resetSessionDelegates,
      modules, addModule, updateModule, removeModule,
      tools, addTools, updateTool, removeTool,
      mcpServers, addMcServer, updateMcServer, removeMcServer,
    ],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAgents(): AgentsValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAgents 必须在 AgentsProvider 内使用");
  return v;
}

/** 模型显示名（注册表条目 → 展示名；找不到回落原始 id）。 */
export function useModelLabel(modelId: string): string {
  const { providers } = useSettings();
  const m = providers.flatMap((p) => p.models).find((x) => x.id === modelId);
  return m ? m.name : modelId || "未绑定模型";
}

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
