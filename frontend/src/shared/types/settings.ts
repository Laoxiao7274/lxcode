// 设置域：todo-first 档位、助手配置、项目守则（behavior.json / assistant.json / AGENTS.md 的 wire 形态）。

/** todo-first 提示词档位（后端 config/behavior.json 的 todoPrompt 字段——
 *  内置模板三选一，不靠模型自觉判断任务简不简单）。 */
export type TodoPromptMode = "strict" | "normal" | "off";

/** TodoPromptSource：todo-first 档位的查看与设置（后端 settings.todoPrompt.* 直通）。
 *
 * 与 SearchAdminSource 同模式：live 模式后端是唯一事实源（get/set + changed
 * 广播）；缺省（demo）时设置面板用本地状态自管。 */
export interface TodoPromptSource {
  /** 当前档位（缓存——连接建立与 changed 事件驱动刷新）。 */
  mode(): TodoPromptMode;
  /** 订阅档位变化（返回退订）。 */
  onChange(listener: () => void): () => void;
  /** 设置档位（后端原子写 + 广播；提示词下一轮生效）。 */
  set(mode: TodoPromptMode): Promise<void>;
}

/** 助手性格与拓展配置（后端 config/assistant.json 的 wire 形态）。
 *
 * 各字段空 = 未设置（用内置值），不是错误。与 AgentDef 的 skills/workflow
 * 同一目录 id 语义（拓展页的技能/模板）。 */
export interface AssistantConfig {
  name?: string;
  persona?: string;
  skills?: string[];
  workflow?: string;
  /** 绑定的 MCP 服务器 id（服务器粒度——该服务器的全部已物化工具对助手
   *  可用，暴露名 <server>_<tool>）。空 = 不绑定。 */
  mcpServers?: string[];
  /** 绑定的自定义命令工具 id（条目粒度——**仅在 toolWhitelist 为空时生效**
   *  （双字段正交过渡：白名单非空时旧通道停用，防双源叠加）。custom binary
   *  条目才可选；后端 set 校验 + resolver 过滤兜底。空 = 不绑定。 */
  tools?: string[];
  /** 工具白名单完整覆盖（2026-12 用户拍板：助手默认工具完全可配——内置
   *  bash/edit 也可勾入，用户担责）。非空 = 它就是助手的全部静态工具面；
   *  空 = 用内置默认清单（builtinDefaults——服务端是事实源，前端不写死）。
   *  MCP 工具仍由 mcpServers 独立合入（动态面与静态面正交）。 */
  toolWhitelist?: string[];
  /** 内置默认工具白名单（assistant.config.get 恒带回——UI 预填与「恢复默认」
   *  的事实源）。缺席 = 老服务端（未带回，前端回落既有展示）。 */
  builtinDefaults?: string[];
}

/** project.remove 的结果（**只解绑**：不删磁盘目录、不删会话）。 */
export interface ProjectRemoveResult {
  removed: boolean;
  /** 该项目下**保留在库里**的会话数（侧栏不再按项目分组，历史一条不丢）。 */
  sessions: number;
}

/** 项目守则（项目根 AGENTS.md）的读取结果——项目级「自定义指令」。 */
export interface ProjectInstructions {
  /** 守则文件绝对路径（服务端解析：项目根 + 固定文件名）。 */
  path: string;
  content: string;
  /** 文件是否存在（false = 该项目还没写守则，不是错误）。 */
  exists: boolean;
  /** 读取异常说明（如超大跳过）；空 = 正常。 */
  note?: string;
}
