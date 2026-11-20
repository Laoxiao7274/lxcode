// Agent 名单与拓展目录（agent.* / catalog.* 的 wire 形态与管理接口，M1）。

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
  /** 执行会不会变更外部世界（strict 只读档的拒绝依据；缺省 = 未声明，按 true）。 */
  mutates?: boolean;
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
  /** 运行期连接状态：connected | error | stopped。
   *
   * 与 enabled 是**两件事**：enabled = 用户想开，status = 真的连上了
   * （连不上时 enabled 仍是 true——卡片必须显示真实状态，否则用户以为能用）。 */
  status?: McpRuntimeStatus;
  /** 已列举到的工具数（未连接时为 0）。 */
  tool_count?: number;
  /** 最近一次连接/列举失败的原因（成功时为空）。 */
  last_error?: string;
  /** stdio 子进程 stderr 的尾部（诊断用）。 */
  stderr?: string;
}

/** MCP 服务器的运行期连接状态。 */
export type McpRuntimeStatus = "connected" | "error" | "stopped";
