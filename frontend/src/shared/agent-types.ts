// 可组装 Agent 域的类型定义（纯类型叶文件——零运行时依赖；种子数据在
// agent-seeds.ts，Provider 在 agents.tsx。消费方从 agents.tsx 的
// re-export 引用，或直接从此处引用）。
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

/** MCP 服务器（拓展第四版块的条目）：接入单元——服务器注册后暴露的
 *  能力（工具）进工具拓展（source=mcp + server 字段指回）。
 *  建模对齐 MCP 事实标准（Claude Desktop / Codex 的 mcpServers 形态）：
 *  stdio = command + args + env（进程直起，不经 shell）；sse = url。 */
export interface McServerSpec {
  /** 服务器名（唯一——也是工具 server 字段的指向）。 */
  id: string;
  desc: string;
  /** 传输：stdio（本地命令起进程）/ sse（远程事件流端点）。 */
  transport: "stdio" | "sse";
  /** stdio：可执行文件（npx / uvx / node …）。 */
  command: string;
  /** stdio：命令参数（逐个——不经 shell，无引号语义）。 */
  args: string[];
  /** stdio：环境变量（API key 等常见注入位）。 */
  env: Record<string, string>;
  /** sse：服务端点 URL。 */
  url: string;
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

/** 上下文模块拓展条目：可插拔的上下文块——流程（工作方式规范）与
 *  技能（领域知识方法）两类，与工具白名单同款交互（拓展 + 勾选注入）；
 *  不授予工具权限。body 是注入 Agent 上下文的完整 markdown 文档。 */
export interface ContextModuleSpec {
  id: string;
  desc: string;
  kind: "process" | "skill";
  /** 模块正文（markdown——实际注入 Agent 上下文的内容）。 */
  body: string;
  /** 用户自建条目（可编辑/删除）；内置条目只读。 */
  custom?: boolean;
}
