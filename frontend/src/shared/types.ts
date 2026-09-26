// UI 事件模型——字段语义与后端 internal/protocol 一一对应（demo 与 live
// 两个 AgentSource 实现都发这套事件，UI 层不感知数据来源）。

/** 任务清单项（对齐 tools.TodoItem）。 */
export interface TodoItem {
  content: string;
  status: "pending" | "active" | "done";
}

/** 上下文占用（对齐 protocol.ContextUsage）：used/window 是压力与环形依据
 *  （used 优先真实 prompt_tokens），四个分类是估算拆分（已归一：分类之和 == used）。 */
export interface ContextUsage {
  used: number;
  /** 模型窗口上限（0/缺省 = 未知——不画环形百分比）。 */
  window?: number;
  system?: number;
  tool_results?: number;
  messages?: number;
  reasoning?: number;
}

/** 手动压缩的结果（chat.compact 的应答）。 */
export interface CompactOutcome {
  /** false = 没有可压的收益（历史太短 / 摘要不缩水）——不是错误。 */
  compacted: boolean;
  /** 为什么没压（人话，直接显示）。 */
  reason?: string;
  before?: number;
  after?: number;
  shadowed?: number;
}

/** 确认请求（对齐 protocol.ConfirmRequest）。 */
export interface ConfirmRequest {
  id: string;
  name: string;
  arguments: string;
  prompt: string;
  /** 非空 = 子 Agent 的确认（归属 dispatch 卡内）。 */
  dispatch_id?: string;
}

/** AgentSource 推给 UI 的事件流（对齐服务端广播事件）。 */
export type AgentEvent =
  | { type: "ready"; server: string; version: string; busy: boolean }
  | { type: "sessionFocused"; id: string }
  | { type: "userMessage"; sessionId: string; text: string }
  | { type: "delta"; sessionId: string; kind: "text" | "reasoning"; text: string; dispatchId?: string }
  | { type: "toolCall"; sessionId: string; id: string; name: string; arguments: string; dispatchId?: string }
  | { type: "toolResult"; sessionId: string; id: string; name: string; content: string; isError: boolean; dispatchId?: string }
  | { type: "confirmRequest"; sessionId: string; request: ConfirmRequest }
  | { type: "todoUpdated"; sessionId: string; items: TodoItem[] }
  | { type: "done"; sessionId: string; usageTokens: number; finishReason: string; dispatchId?: string; context?: ContextUsage }
  | { type: "error"; sessionId: string; message: string; aborted: boolean }
  | { type: "dispatchStart"; sessionId: string; dispatchId: string; childSessionId?: string; agentId: string; agentName: string; agentColor: string; task: string }
  | { type: "dispatchEnd"; sessionId: string; dispatchId: string; childSessionId?: string; result: string; isError: boolean; usageTokens?: number }
  /** 请求失败不代表生成失败：不得清空会话、定格正文或解除确认卡。 */
  | { type: "operationError"; message: string }
  | { type: "busy"; sessionId: string; busy: boolean }
  | { type: "sessionChanged"; id: string; reason: string }
  /** 会话列表本身变了（重命名/归档/恢复）——UI 重读 sessions()。 */
  | { type: "sessionsChanged" }
  /** 项目列表变了（添加）——UI 重读 projects()。 */
  | { type: "projectsChanged" }
  /** 一轮任务的产物汇总（改动文件 + diff 统计——验收视图）。 */
  | { type: "filesChanged"; sessionId: string; files: FileChange[] }
  /** 历史被压缩（前缀替换成摘要检查点）——UI 插一条「已压缩历史」标记块。
   *  dispatchId 非空 = 子会话自己的压缩（归属进 dispatch 卡内，不进主时间线）。 */
  | { type: "compacted"; sessionId: string; before: number; after: number; shadowed: number; summary: string; manual?: boolean; dispatchId?: string }
  /** 历史载入（连接/切会话后）——全量重建对话视图。 */
  | { type: "historyLoaded"; sessionId: string; history: HistorySnapshot };

/** 会话列表条目（对齐 protocol.SessionMeta；workspace 用于侧栏按工作区分组）。 */
export interface SessionMeta {
  id: string;
  title: string;
  updatedAt: string;
  messages: number;
  /** 所属工作区（项目路径的末段；空 = 未分组）。 */
  workspace?: string;
  /** 归档态——侧栏不显示，设置「归档任务」里可恢复。 */
  archived?: boolean;
}

/** 历史快照（chat.history 的载荷——重建视图用）。 */
export interface HistorySnapshot {
  sessionId: string;
  messages: HistoryMessage[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
  /** 上下文占用（缺省 = 未知——刚切会话/后端刚重启，指示器显示中性态）。 */
  context?: ContextUsage;
  /** 压缩检查点在 messages 里的下标（这些消息渲染成「已压缩历史」块，不是用户气泡）。 */
  checkpoints?: number[];
}

/** 历史消息（llm.Message 的 wire 形态）。 */
export interface HistoryMessage {
  role: string;
  content: string;
  reasoning_content?: string;
  tool_calls?: Array<{
    id?: string;
    function?: { name: string; arguments?: string };
  }>;
  tool_call_id?: string;
}

/** 改动文件条目（一轮任务结束时的产物汇总——Codex 的 diff 中心形态）。 */
export interface FileChange {
  path: string;
  /** 增加行数 / 删除行数（diff 统计）。 */
  added: number;
  deleted: number;
  /** 精简 diff 文本（Codex 风格渲染：@ 文件头、- 红行、+ 绿行）。 */
  diff: string;
}

/** 项目（侧栏「项目」分组的数据源；对应后端 projects 表）。 */
export interface ProjectMeta {
  id: string;
  name: string;
  path: string;
}

/** 发送选项：随消息携带的请求级参数（不传 = 后端默认）。 */
export interface SendOptions {
  /** 推理强度（仅对声明 reasoning 能力的模型生效）。 */
  effort?: string;
  /** 权限模式：auto 高危自动 / confirm 高危确认（默认）/ strict 只读。 */
  approval?: "auto" | "confirm" | "strict";
  /** 执行 Agent 的名单 id（空 = 主 Agent——后端按 Agent 四层组合提示词、
   *  模型绑定与工具白名单跑这一轮）。 */
  agent?: string;
}

/**
 * AgentSource 是数据源抽象：demo（脚本编排）与 live（WS 连后端）实现
 * 同一接口。浏览器与 Electron 渲染层复用相同 JSON-RPC 适配器。
 */
export interface AgentSource {
  /** 订阅事件流（返回退订函数）。 */
  subscribe(listener: (ev: AgentEvent) => void): () => void;
  /** 发送消息（一轮开始；opts 携带 effort/approval，缺省 = 后端默认）。 */
  send(sessionId: string, text: string, opts?: SendOptions): void;
  /** 裁决确认门（目标会话显式传入，避免切换焦点后误投）。 */
  confirm(sessionId: string, id: string, allow: boolean): Promise<void>;
  /** 取消指定会话的生成。 */
  cancel(sessionId: string): void;
  /** 手动压缩指定会话的历史。 */
  compact(sessionId: string): Promise<CompactOutcome>;
  /** 新会话（可选归属项目 id——会话挂在项目分组下）。 */
  newSession(workspace?: string): Promise<string>;
  /** 释放干净项目会话的 worktree 目录，保留分支与会话数据。 */
  releaseWorktree(id: string): Promise<void>;
  /** 恢复会话。 */
  resumeSession(id: string): Promise<void>;
  /** 重命名会话。 */
  renameSession(id: string, title: string): void;
  /** 归档会话（当前会话被归档时自动切到新会话）。 */
  archiveSession(id: string): void;
  /** 从归档恢复。 */
  unarchiveSession(id: string): void;
  /** 会话列表。 */
  sessions(): SessionMeta[];
  /** 项目列表。 */
  projects(): ProjectMeta[];
  /** 添加项目（注册目录为 git 仓库——已有仓库不动，没有则 init）。 */
  addProject(name: string, path: string): void;
  /** 读项目守则（项目根 AGENTS.md——项目级「自定义指令」，每轮现读进提示词）。 */
  readInstructions(projectId: string): Promise<ProjectInstructions>;
  /** 写项目守则（项目根 AGENTS.md，原子写）。 */
  saveInstructions(projectId: string, content: string): Promise<void>;
  /** 显示名（顶栏徽标）。 */
  label: string;
  /** 模型注册表管理；缺省时设置面板使用独立的本地演示目录。 */
  modelAdmin?: ModelAdminSource;
  /** Agent 名单与拓展目录管理（M1）；缺省时前端用内存种子自管（demo）。 */
  agentAdmin?: AgentAdminSource;
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

/** 后端模型注册表（config.ModelConfig 的 wire 形态，snake_case）。 */
export interface ModelEntry {
  id: string;
  display_name?: string;
  base_url: string;
  api_key?: string;
  format?: string;
  model: string;
  context_window?: number;
  max_output_tokens?: number;
  capabilities?: { tools?: boolean; vision?: boolean; json_output?: boolean; reasoning?: boolean };
  enabled: boolean;
}

/** ModelAdminSource：模型注册表的查看与管理（后端 model.* 直通）。
 * 独立能力接口——UI 面板依赖它而非具体 WSAgent；Demo 不实现。 */
export interface ModelAdminSource {
  /** 当前快照（model.list 结果缓存）。 */
  models(): { models: ModelEntry[]; roles: Record<string, string> };
  /** 订阅注册表变化（连接建立/model.changed；返回退订）。 */
  onModelsChanged(listener: () => void): () => void;
  /** 新增注册表条目；校验失败以 rejected Promise 返回。 */
  addModel(entry: Partial<Omit<ModelEntry, "id">> & { id: string; base_url?: string }): Promise<void>;
  /** 更新（以现有条目为底套 patch）。 */
  updateModel(entry: ModelEntry): Promise<void>;
  /** 删除。 */
  removeModel(id: string): Promise<void>;
  /** 可见性开关。 */
  setModelEnabled(id: string, enabled: boolean): Promise<void>;
  /** 角色绑定（default/vision）。 */
  setRole(role: string, modelId: string): Promise<void>;
}

/** AgentAdminSource：Agent 名单与拓展目录的查看与管理（后端 agent.与
 * catalog.两组方法直通——M1 注册表与目录）。与 ModelAdminSource 同模式：
 * UI 依赖能力接口而非具体 WSAgent；Demo 不实现（前端内存种子自管）。 */
export interface AgentAdminSource {
  /** 当前 Agent 名单（agent.list 结果缓存——主 Agent 首位）。 */
  agents(): AgentAdminEntry[];
  /** 拓展目录（catalog.*.list 结果缓存）。 */
  modules(): AgentAdminModule[];
  tools(): AgentAdminTool[];
  mcpServers(): AgentAdminMcServer[];
  /** 订阅名单/目录变化（连接建立/agent.changed/catalog.changed；返回退订）。 */
  onChanged(listener: () => void): () => void;
  addAgent(agent: AgentAdminEntry): Promise<void>;
  updateAgent(agent: AgentAdminEntry): Promise<void>;
  removeAgent(id: string): Promise<void>;
  addModule(module: AgentAdminModule): Promise<void>;
  updateModule(module: AgentAdminModule): Promise<void>;
  removeModule(id: string): Promise<void>;
  addTool(tool: AgentAdminTool): Promise<void>;
  updateTool(tool: AgentAdminTool): Promise<void>;
  removeTool(id: string): Promise<void>;
  addMcServer(server: AgentAdminMcServer): Promise<void>;
  updateMcServer(server: AgentAdminMcServer): Promise<void>;
  removeMcServer(id: string): Promise<void>;
}

/** 后端 Agent 名单条目（protocol.AgentEntry 的 wire 形态，snake_case——
 * is_main 与前端的 isMain 映射在适配层做）。 */
export interface AgentAdminEntry {
  id: string;
  name: string;
  desc: string;
  color: string;
  model: string;
  tools: string[];
  workflow: string;
  skills: string[];
  delegates: string[];
  approval: string;
  enabled: boolean;
  is_main?: boolean;
  prompt: string;
  protocol: string;
  custom?: boolean;
}

/** 后端模块目录条目（protocol.ModuleEntry）。 */
export interface AgentAdminModule {
  id: string;
  desc: string;
  kind: "process" | "skill";
  body: string;
  custom: boolean;
}

/** 后端工具目录条目（protocol.ToolEntry）。 */
export interface AgentAdminTool {
  id: string;
  desc: string;
  risk: "low" | "high";
  source: "builtin" | "binary" | "mcp";
  params?: Array<{ name: string; type: string; required?: boolean; desc?: string }>;
  doc?: string;
  server?: string;
  command?: string;
  example?: string;
  package_file?: string;
  custom: boolean;
}

/** 后端 MCP 服务器条目（protocol.McServerEntry）。 */
export interface AgentAdminMcServer {
  id: string;
  desc: string;
  transport: "stdio" | "sse";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  enabled: boolean;
  custom: boolean;
}
