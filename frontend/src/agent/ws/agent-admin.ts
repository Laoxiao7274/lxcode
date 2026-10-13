// AgentAdminSource 域（Agent 名单与拓展目录——M1）：请求编排 + 缓存。
// apply* 双路：握手并行组的 catalog.*.list / agent.list 刷新（onInitList 注册）
// 与 agent.changed / catalog.changed 事件的副作用（onEventSideEffect 注册——
// 目录变更按 kind 只重拉对应列表）。
import type { AgentAdminEntry, AgentAdminMcServer, AgentAdminModule, AgentAdminSource, AgentAdminTool } from "../../shared/types";
import type { WsConnection } from "./connection";

export class AgentAdminDomain implements AgentAdminSource {
  /** Agent 名单与拓展目录快照（agent.changed/catalog.changed 驱动刷新）。 */
  private agentsCache: AgentAdminEntry[] = [];
  private modulesCache: AgentAdminModule[] = [];
  private toolsCache: AgentAdminTool[] = [];
  private mcpCache: AgentAdminMcServer[] = [];
  private listeners = new Set<() => void>();

  constructor(private readonly conn: WsConnection) {
    // 握手并行组：名单与三份目录各自落缓存
    conn.onInitList("agent.list", (r) => this.applyAgentList(r));
    conn.onInitList("catalog.modules.list", (r) => this.applyAgentModules(r));
    conn.onInitList("catalog.tools.list", (r) => this.applyAgentTools(r));
    conn.onInitList("catalog.mcp.list", (r) => this.applyAgentMcp(r));
    // 事件副作用：Agent 名单变更 → 重拉（后端事实源）；目录变更（kind 标明
    // 哪个目录）→ 只重拉对应列表
    conn.onEventSideEffect((method, params) => {
      if (method !== "agent.changed" && method !== "catalog.changed") return;
      if (method === "agent.changed") {
        this.conn.call("agent.list")
          .then((r) => this.applyAgentList(r))
          .catch((e) => this.conn.opError(`刷新 Agent 名单失败: ${e.message}`));
        return;
      }
      const p = (params ?? {}) as Record<string, unknown>;
      const kind = String(p.kind ?? "");
      const m = kind === "modules" ? "catalog.modules.list" : kind === "tools" ? "catalog.tools.list" : kind === "mcp" ? "catalog.mcp.list" : "";
      if (m) {
        this.conn.call(m)
          .then((r) => {
            if (kind === "modules") this.applyAgentModules(r);
            else if (kind === "tools") this.applyAgentTools(r);
            else this.applyAgentMcp(r);
          })
          .catch((e) => this.conn.opError(`刷新目录失败: ${e.message}`));
      }
    });
  }

  agents(): AgentAdminEntry[] {
    return this.agentsCache;
  }

  modules(): AgentAdminModule[] {
    return this.modulesCache;
  }

  tools(): AgentAdminTool[] {
    return this.toolsCache;
  }

  mcpServers(): AgentAdminMcServer[] {
    return this.mcpCache;
  }

  /** 订阅名单/目录变化（连接建立/agent.changed/catalog.changed）。 */
  onChanged(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  addAgent(agent: AgentAdminEntry): Promise<void> {
    return this.conn.call("agent.add", { agent }).then(() => undefined);
  }

  updateAgent(agent: AgentAdminEntry): Promise<void> {
    return this.conn.call("agent.update", { agent }).then(() => undefined);
  }

  removeAgent(id: string): Promise<void> {
    return this.conn.call("agent.remove", { id }).then(() => undefined);
  }

  addModule(module: AgentAdminModule): Promise<void> {
    return this.conn.call("catalog.modules.add", { module }).then(() => undefined);
  }

  updateModule(module: AgentAdminModule): Promise<void> {
    return this.conn.call("catalog.modules.update", { module }).then(() => undefined);
  }

  removeModule(id: string): Promise<void> {
    return this.conn.call("catalog.modules.remove", { id }).then(() => undefined);
  }

  addTool(tool: AgentAdminTool): Promise<void> {
    return this.conn.call("catalog.tools.add", { tool }).then(() => undefined);
  }

  updateTool(tool: AgentAdminTool): Promise<void> {
    return this.conn.call("catalog.tools.update", { tool }).then(() => undefined);
  }

  removeTool(id: string): Promise<void> {
    return this.conn.call("catalog.tools.remove", { id }).then(() => undefined);
  }

  addMcServer(server: AgentAdminMcServer): Promise<void> {
    return this.conn.call("catalog.mcp.add", { server }).then(() => undefined);
  }

  updateMcServer(server: AgentAdminMcServer): Promise<void> {
    return this.conn.call("catalog.mcp.update", { server }).then(() => undefined);
  }

  removeMcServer(id: string): Promise<void> {
    return this.conn.call("catalog.mcp.remove", { id }).then(() => undefined);
  }

  /** agent.list 结果 → 缓存 + 通知（agent.changed 事件也走这里）。 */
  private applyAgentList(result: unknown) {
    if (!Array.isArray(result)) return;
    this.agentsCache = result as AgentAdminEntry[];
    this.listeners.forEach((l) => l());
  }

  private applyAgentModules(result: unknown) {
    if (!Array.isArray(result)) return;
    this.modulesCache = result as AgentAdminModule[];
    this.listeners.forEach((l) => l());
  }

  private applyAgentTools(result: unknown) {
    if (!Array.isArray(result)) return;
    this.toolsCache = result as AgentAdminTool[];
    this.listeners.forEach((l) => l());
  }

  private applyAgentMcp(result: unknown) {
    if (!Array.isArray(result)) return;
    this.mcpCache = result as AgentAdminMcServer[];
    this.listeners.forEach((l) => l());
  }
}
