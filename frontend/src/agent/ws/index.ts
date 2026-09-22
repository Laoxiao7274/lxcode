// WSAgent：真实后端对接（WS JSON-RPC，默认 127.0.0.1:7789——协议与 Go
// internal/protocol 一致）。AgentSource + ModelAdminSource + AgentAdminSource
// 三能力实现。事件 → AgentEvent 映射与 DemoAgent 可互换（工厂一行切换）。
// 错误纪律：生成类失败（chat.error）走 error 事件（会话状态由 reducer
// 收敛）；请求类失败（拒绝/断连/超时）走 operationError 事件——UI 只提示，
// 不动 blocks/pending。
import type {
  AgentAdminEntry, AgentAdminMcServer, AgentAdminModule, AgentAdminSource, AgentAdminTool,
  AgentEvent, AgentSource, ConfirmRequest, ModelAdminSource, ModelEntry,
  ProjectMeta, SendOptions, SessionMeta, TodoItem,
} from "../../shared/types";

/** WS JSON-RPC 帧结构（与 Go internal/protocol 对齐）。 */
interface WsRequest {
  jsonrpc: string;
  id?: number;
  method: string;
  params?: unknown;
}

interface WsResponse {
  jsonrpc: string;
  id?: number;
  result?: unknown;
  error?: { code: number; message: string };
  method?: string; // 事件帧（无 id）
  params?: unknown;
}

type Listener = (ev: AgentEvent) => void;

interface PendingCall {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
}

const WS_READY_STATE_OPEN = 1;

const RECONNECT_MS = 5000;
const REQUEST_TIMEOUT_MS = 10_000;

export class WSAgent implements AgentSource, ModelAdminSource, AgentAdminSource {
  label = "真实后端";
  readonly modelAdmin: ModelAdminSource = this;
  readonly agentAdmin: AgentAdminSource = this;
  private readonly addr: string;
  private ws: WebSocket | null = null;
  private listeners = new Set<Listener>();
  private nextId = 1;
  private pending = new Map<number, PendingCall>();
  private sessionsCache: SessionMeta[] = [];
  private projectsCache: ProjectMeta[] = [];
  /** 模型注册表快照（model.changed 驱动刷新；settings 面板的数据源）。 */
  private modelsCache: ModelEntry[] = [];
  private rolesCache: Record<string, string> = {};
  private modelListeners = new Set<() => void>();
  /** Agent 名单与拓展目录快照（agent.changed/catalog.changed 驱动刷新）。 */
  private agentsCache: AgentAdminEntry[] = [];
  private agentModulesCache: AgentAdminModule[] = [];
  private agentToolsCache: AgentAdminTool[] = [];
  private agentMcpCache: AgentAdminMcServer[] = [];
  private agentListeners = new Set<() => void>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  /** 订阅时惰性建连（构造不再触网——测试可先插桩再连接）。 */
  private started = false;
  /** 首次连接是否已完成「开机即空会话」（用户拍板：打开软件就是空会话，
   *  不恢复上次对话）。**只做首次**：重连再清一次会把用户正在聊的会话吃掉。 */
  private booted = false;

  constructor(addr = "127.0.0.1:7789") {
    this.addr = addr;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    if (!this.started) {
      this.started = true;
      this.connect();
    }
    return () => {
      this.listeners.delete(listener);
      // 最后一个订阅者退订：停重连并断开（演示/测试挂载卸载不留后台连接）
      if (this.listeners.size === 0) this.disposeSocket();
    };
  }

  /** 连接 WS（自动重连）。 */
  private connect() {
    if (this.ws && (this.ws.readyState === WS_READY_STATE_OPEN || this.ws.readyState === 0)) return;
    const url = `ws://${this.addr}/rpc`;
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      // 握手失败则不继续调用；旧连接的初始化链不得串入重连后的连接。
      this.call("connection.hello", { client: "lxcode-web", version: "1" })
        .then(async () => {
          const refresh = async (method: string, apply: (r: unknown) => void) => {
            if (this.ws !== ws) return;
            try { const r = await this.call(method); if (this.ws === ws) apply(r); }
            catch (e) { if (this.ws === ws) this.opError(`${method}: ${String(e)}`); }
          };
          await refresh("model.list", (r) => this.applyModelList(r));
          await refresh("project.list", (r) => this.applyProjectList(r));
          await refresh("session.list", (r) => this.applySessionList(r));
          await refresh("agent.list", (r) => this.applyAgentList(r));
          await refresh("catalog.modules.list", (r) => this.applyAgentModules(r));
          await refresh("catalog.tools.list", (r) => this.applyAgentTools(r));
          await refresh("catalog.mcp.list", (r) => this.applyAgentMcp(r));
          // 开机即空会话（用户拍板）：首次连接先切到新会话，再拉历史——顺序
          // 决定不会先闪出上次的对话。后端启动时恢复了最近会话，这里显式开新
          // 会话把它换掉（空会话不落库：发第一条消息才建行）。
          if (!this.booted && this.ws === ws) {
            this.booted = true;
            await refresh("session.new", () => {});
          }
          if (this.ws === ws) await this.loadHistory();
        })
        .catch((e) => { if (this.ws === ws) this.opError(`初始化失败: ${e.message}`); });
    };

    ws.onmessage = (e) => {
      let msg: WsResponse;
      try {
        msg = JSON.parse(String(e.data));
      } catch {
        return; // 非 JSON 帧直接忽略（防坏数据炸监听器）
      }
      // 应答帧（有 id）
      if (msg.id !== undefined && msg.id !== null) {
        this.settlePending(Number(msg.id), msg);
        return;
      }
      // 事件帧（无 id）
      if (msg.method) {
        this.handleEvent(msg.method, msg.params);
      }
    };

    ws.onclose = () => {
      this.ws = null;
      // 断连：所有挂起请求立刻失败（不等 10s 超时——后端已死，等是骗人）
      this.rejectAllPending("后端连接已断开");
      this.emit({ type: "error", message: "后端连接断开", aborted: true });
      // 5s 重连
      this.reconnectTimer = setTimeout(() => this.connect(), RECONNECT_MS);
    };

    ws.onerror = () => {
      // onclose 会跟着触发
    };
  }

  /** 断开并停止重连（测试/卸载用）。 */
  private disposeSocket() {
    this.started = false;
    if (this.ws) this.ws.onclose = null;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.rejectAllPending("后端连接已断开");
    this.ws?.close();
    this.ws = null;
  }

  /** 应答帧落地：resolve/reject 并清掉超时计时器。 */
  private settlePending(id: number, msg: WsResponse) {
    const p = this.pending.get(id);
    if (!p) return;
    this.pending.delete(id);
    clearTimeout(p.timeout);
    if (msg.error) p.reject(new Error(msg.error.message));
    else p.resolve(msg.result);
  }

  /** 断连时统一拒绝全部挂起请求。 */
  private rejectAllPending(reason: string) {
    for (const [, p] of this.pending) {
      clearTimeout(p.timeout);
      p.reject(new Error(reason));
    }
    this.pending.clear();
  }

  /** 后端事件 → AgentEvent 映射。 */
  private handleEvent(method: string, params: unknown) {
    const p = (params ?? {}) as Record<string, unknown>;
    // dispatch_id 归属（子 Agent 执行的事件——store 挂 dispatch 卡）
    const dispatchId = p.dispatch_id ? String(p.dispatch_id) : undefined;
    switch (method) {
      case "connection.ready":
        this.emit({ type: "ready", server: String(p.server ?? ""), version: String(p.version ?? ""), busy: Boolean(p.busy) });
        break;
      case "chat.userMessage":
        // 载荷是 llm.Message 形态（content 键——历史坑：按 text 读永远空）
        this.emit({ type: "userMessage", text: String(p.content ?? p.text ?? "") });
        break;
      case "chat.delta":
        this.emit({ type: "delta", kind: String(p.kind) as "text" | "reasoning", text: String(p.text ?? ""), dispatchId });
        break;
      case "chat.toolCall":
        this.emit({ type: "toolCall", id: String(p.id), name: String(p.name), arguments: String(p.arguments ?? ""), dispatchId });
        break;
      case "chat.toolResult":
        this.emit({ type: "toolResult", id: String(p.id), name: String(p.name), content: String(p.content ?? ""), isError: Boolean(p.is_error), dispatchId });
        break;
      case "chat.dispatchStart":
        this.emit({
          type: "dispatchStart",
          dispatchId: String(p.dispatch_id ?? ""),
          agentId: String(p.agent_id ?? ""),
          agentName: String(p.agent_name ?? ""),
          agentColor: String(p.agent_color ?? "#3b82f6"),
          task: String(p.task ?? ""),
        });
        break;
      case "chat.dispatchEnd":
        this.emit({
          type: "dispatchEnd",
          dispatchId: String(p.dispatch_id ?? ""),
          result: String(p.result ?? ""),
          isError: Boolean(p.is_error),
          usageTokens: Number(p.usage_tokens ?? 0),
        });
        break;
      case "chat.confirmRequest":
        this.emit({ type: "confirmRequest", request: p as unknown as ConfirmRequest });
        break;
      case "todo.updated":
        this.emit({ type: "todoUpdated", items: (p.items as TodoItem[]) ?? [] });
        break;
      case "chat.done":
        this.emit({ type: "done", usageTokens: Number(p.usage_tokens ?? 0), finishReason: String(p.finish_reason ?? "stop"), dispatchId });
        // 主轮结束——会话列表元数据（标题/时间/消息数）可能变了：重拉
        //（子轮的 done 不触发——dispatchId 归属时不刷列表）
        if (!dispatchId) {
          this.call("session.list")
            .then((r) => {
              this.applySessionList(r);
              this.emit({ type: "sessionsChanged" });
            })
            .catch((e) => this.opError(`刷新会话列表失败: ${e.message}`));
        }
        break;
      case "chat.error":
        this.emit({ type: "error", message: String(p.message ?? ""), aborted: Boolean(p.aborted) });
        break;
      case "chat.busy":
        this.emit({ type: "busy", busy: Boolean(p.busy) });
        break;
      case "session.changed":
        this.emit({ type: "sessionChanged", id: String(p.id ?? ""), reason: String(p.reason ?? "") });
        // started（懒建行）只刷列表——对话进行中，视图不动；new/resumed
        // 已由 reducer 清屏，resumed 后重拉 chat.history 重放历史
        if (String(p.reason ?? "") === "started") {
          this.call("session.list")
            .then((r) => {
              this.applySessionList(r);
              this.emit({ type: "sessionsChanged" });
            })
            .catch((e) => this.opError(`刷新会话列表失败: ${e.message}`));
        } else if (String(p.reason ?? "") === "resumed") {
          void this.loadHistory().catch((e) => this.opError(`加载历史失败: ${e.message}`));
        } else if (String(p.reason ?? "") !== "new") {
          this.call("session.list").then((r) => this.applySessionList(r))
            .catch((e) => this.opError(`刷新会话列表失败: ${e.message}`));
        }
        break;
      case "model.changed":
        // 模型注册表变更——重拉列表（settings 的 providers 数据源）
        this.call("model.list")
          .then((r) => this.applyModelList(r))
          .catch((e) => this.opError(`刷新模型列表失败: ${e.message}`));
        break;
      case "files.changed":
        // 一轮的产物汇总（验收视图——后端按 edit/write_file 收集）
        {
          const f = params as { files?: Array<{ path: string; added: number; deleted: number; diff: string }> };
          if (Array.isArray(f.files)) {
            this.emit({
              type: "filesChanged",
              files: f.files.map((x) => ({ path: x.path, added: x.added, deleted: x.deleted, diff: x.diff })),
            });
          }
        }
        break;
      case "project.changed":
        // 项目增删 → 重拉项目列表（后端事实源）
        this.call("project.list")
          .then((r) => {
            this.applyProjectList(r);
            this.emit({ type: "projectsChanged" });
          })
          .catch((e) => this.opError(`刷新项目列表失败: ${e.message}`));
        break;
      case "agent.changed":
        // Agent 名单变更 → 重拉（后端事实源）
        this.call("agent.list")
          .then((r) => this.applyAgentList(r))
          .catch((e) => this.opError(`刷新 Agent 名单失败: ${e.message}`));
        break;
      case "catalog.changed": {
        // 目录变更（kind 标明哪个目录）→ 只重拉对应列表
        const kind = String(p.kind ?? "");
        const method = kind === "modules" ? "catalog.modules.list" : kind === "tools" ? "catalog.tools.list" : kind === "mcp" ? "catalog.mcp.list" : "";
        if (method) {
          this.call(method)
            .then((r) => {
              if (kind === "modules") this.applyAgentModules(r);
              else if (kind === "tools") this.applyAgentTools(r);
              else this.applyAgentMcp(r);
            })
            .catch((e) => this.opError(`刷新目录失败: ${e.message}`));
        }
        break;
      }
    }
  }

  /** 发 JSON-RPC 请求并等应答（10s 超时；超时/断连都会清理挂起表）。 */
  private call(method: string, params?: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WS_READY_STATE_OPEN) {
        reject(new Error("后端未连接"));
        return;
      }
      const id = this.nextId++;
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error("请求超时"));
      }, REQUEST_TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timeout });
      const req: WsRequest = { jsonrpc: "2.0", id, method };
      if (params !== undefined) req.params = params;
      try {
        this.ws.send(JSON.stringify(req));
      } catch (e) {
        clearTimeout(timeout);
        this.pending.delete(id);
        reject(e instanceof Error ? e : new Error(String(e)));
      }
    });
  }

  private emit(ev: AgentEvent) {
    this.listeners.forEach((l) => l(ev));
  }

  /** 请求级失败（不影响生成状态）→ operationError 事件。 */
  private opError(message: string) {
    this.emit({ type: "operationError", message });
  }

  // ---- AgentSource 接口 ----

  send(text: string, opts?: SendOptions): void {
    if (!text.trim()) return;
    // effort/approval/agent 只在显式携带时进帧（omitempty 语义——旧请求形状不变）
    const params: Record<string, unknown> = { text };
    if (opts?.effort) params.effort = opts.effort;
    if (opts?.approval) params.approval = opts.approval;
    if (opts?.agent) params.agent = opts.agent;
    this.call("chat.send", params).catch((e) => {
      this.opError(`发送失败: ${e.message}`);
    });
  }

  /** 裁决确认门。resolve = 后端确认成功（结果随后以 toolResult 到达）；
   *  reject（确认丢失/已终结/断连）向上抛——调用方决定卡片回退与否。 */
  confirm(id: string, allow: boolean): Promise<void> {
    return this.call("tool.confirm", { id, allow }).then(() => undefined);
  }

  cancel(): void {
    this.call("chat.cancel").catch((e) => {
      this.opError(`取消失败: ${e.message}`);
    });
  }

  newSession(workspace?: string): void {
    this.call("session.new", workspace ? { workspace } : undefined)
      .then(() => undefined)
      .catch((e) => this.opError(`新建会话失败: ${e.message}`));
  }

  resumeSession(id: string): void {
    this.call("session.resume", { id })
      .then(() => undefined)
      .catch((e) => this.opError(`恢复会话失败: ${e.message}`));
  }

  renameSession(id: string, title: string): void {
    const t = title.trim();
    if (!t) return;
    // 列表更新走 session.changed 广播（后端事实源），这里只发请求
    this.call("session.rename", { id, title: t })
      .then(() => undefined)
      .catch((e) => this.opError(`重命名失败: ${e.message}`));
  }

  archiveSession(id: string): void {
    this.call("session.archive", { id, archived: true })
      .then(() => undefined)
      .catch((e) => this.opError(`归档失败: ${e.message}`));
  }

  unarchiveSession(id: string): void {
    this.call("session.archive", { id, archived: false })
      .then(() => undefined)
      .catch((e) => this.opError(`恢复归档失败: ${e.message}`));
  }

  sessions(): SessionMeta[] {
    return this.sessionsCache;
  }

  projects(): ProjectMeta[] {
    return this.projectsCache;
  }

  addProject(name: string, path: string): void {
    // project.changed 广播回来时刷新列表（后端事实源）
    this.call("project.add", { name, path })
      .then(() => undefined)
      .catch((e) => this.opError(`添加项目失败: ${e.message}`));
  }

  /** 后端 session.list 结果 → 缓存（协议 snake_case → 前端 camelCase 映射）。 */
  private applySessionList(result: unknown) {
    const list = result as Array<{ id: string; title: string; updated_at: string; messages: number; archived?: boolean; workspace?: string }>;
    if (!Array.isArray(list)) return;
    this.sessionsCache = list.map((s) => ({
      id: s.id,
      title: s.title,
      updatedAt: s.updated_at,
      messages: s.messages,
      archived: Boolean(s.archived),
      workspace: s.workspace ?? "",
    }));
    this.emit({ type: "sessionsChanged" });
  }

  /** 后端 project.list 结果 → 缓存。 */
  private applyProjectList(result: unknown) {
    const list = result as Array<{ id: string; name: string; path: string }>;
    if (!Array.isArray(list)) return;
    this.projectsCache = list.map((p) => ({ id: p.id, name: p.name, path: p.path }));
    this.emit({ type: "projectsChanged" });
  }

  // ---- ModelAdminSource（模型注册表直通；settings 面板数据源） ----

  /** 拉当前会话的历史并重放视图（连接建立/切换会话后调）。 */
  private loadHistory(): Promise<void> {
    return this.call("chat.history")
      .then((r) => {
        const h = r as {
          session_id?: string;
          messages?: Array<{
            role: string;
            content: string;
            reasoning_content?: string;
            tool_calls?: Array<{ id?: string; function?: { name: string; arguments?: string } }>;
            tool_call_id?: string;
          }>;
          busy?: boolean;
          pending?: ConfirmRequest | null;
          todos?: TodoItem[];
        };
        this.emit({
          type: "historyLoaded",
          history: {
            sessionId: h.session_id ?? "",
            messages: h.messages ?? [],
            busy: Boolean(h.busy),
            pending: h.pending ?? null,
            todos: h.todos ?? [],
          },
        });
      }) as Promise<void>;
  }

  models(): { models: ModelEntry[]; roles: Record<string, string> } {
    return { models: this.modelsCache, roles: this.rolesCache };
  }

  /** 订阅模型列表变化（返回退订）。 */
  onModelsChanged(listener: () => void): () => void {
    this.modelListeners.add(listener);
    return () => this.modelListeners.delete(listener);
  }

  addModel(entry: Partial<Omit<ModelEntry, "id">> & { id: string; base_url?: string }): Promise<void> {
    return this.call("model.add", { ...entry, id: entry.id }).then(() => undefined);
  }

  updateModel(entry: ModelEntry): Promise<void> {
    return this.call("model.update", entry).then(() => undefined);
  }

  removeModel(id: string): Promise<void> {
    return this.call("model.remove", { id }).then(() => undefined);
  }

  setModelEnabled(id: string, enabled: boolean): Promise<void> {
    return this.call("model.enable", { id, enabled }).then(() => undefined);
  }

  setRole(role: string, modelId: string): Promise<void> {
    return this.call("role.set", { role, model_id: modelId }).then(() => undefined);
  }

  /** model.list 结果 → 缓存 + 通知（model.changed 事件也走这里）。 */
  private applyModelList(result: unknown) {
    const r = result as { models?: ModelEntry[]; roles?: Record<string, string> };
    if (!r || !Array.isArray(r.models)) return;
    this.modelsCache = r.models;
    this.rolesCache = r.roles ?? {};
    this.modelListeners.forEach((l) => l());
  }

  // ---- AgentAdminSource（Agent 名单与拓展目录——M1） ----

  agents(): AgentAdminEntry[] {
    return this.agentsCache;
  }

  modules(): AgentAdminModule[] {
    return this.agentModulesCache;
  }

  tools(): AgentAdminTool[] {
    return this.agentToolsCache;
  }

  mcpServers(): AgentAdminMcServer[] {
    return this.agentMcpCache;
  }

  /** 订阅名单/目录变化（连接建立/agent.changed/catalog.changed）。 */
  onChanged(listener: () => void): () => void {
    this.agentListeners.add(listener);
    return () => this.agentListeners.delete(listener);
  }

  addAgent(agent: AgentAdminEntry): Promise<void> {
    return this.call("agent.add", { agent }).then(() => undefined);
  }

  updateAgent(agent: AgentAdminEntry): Promise<void> {
    return this.call("agent.update", { agent }).then(() => undefined);
  }

  removeAgent(id: string): Promise<void> {
    return this.call("agent.remove", { id }).then(() => undefined);
  }

  addModule(module: AgentAdminModule): Promise<void> {
    return this.call("catalog.modules.add", { module }).then(() => undefined);
  }

  updateModule(module: AgentAdminModule): Promise<void> {
    return this.call("catalog.modules.update", { module }).then(() => undefined);
  }

  removeModule(id: string): Promise<void> {
    return this.call("catalog.modules.remove", { id }).then(() => undefined);
  }

  addTool(tool: AgentAdminTool): Promise<void> {
    return this.call("catalog.tools.add", { tool }).then(() => undefined);
  }

  updateTool(tool: AgentAdminTool): Promise<void> {
    return this.call("catalog.tools.update", { tool }).then(() => undefined);
  }

  removeTool(id: string): Promise<void> {
    return this.call("catalog.tools.remove", { id }).then(() => undefined);
  }

  addMcServer(server: AgentAdminMcServer): Promise<void> {
    return this.call("catalog.mcp.add", { server }).then(() => undefined);
  }

  updateMcServer(server: AgentAdminMcServer): Promise<void> {
    return this.call("catalog.mcp.update", { server }).then(() => undefined);
  }

  removeMcServer(id: string): Promise<void> {
    return this.call("catalog.mcp.remove", { id }).then(() => undefined);
  }

  /** agent.list 结果 → 缓存 + 通知（agent.changed 事件也走这里）。 */
  private applyAgentList(result: unknown) {
    if (!Array.isArray(result)) return;
    this.agentsCache = result as AgentAdminEntry[];
    this.agentListeners.forEach((l) => l());
  }

  private applyAgentModules(result: unknown) {
    if (!Array.isArray(result)) return;
    this.agentModulesCache = result as AgentAdminModule[];
    this.agentListeners.forEach((l) => l());
  }

  private applyAgentTools(result: unknown) {
    if (!Array.isArray(result)) return;
    this.agentToolsCache = result as AgentAdminTool[];
    this.agentListeners.forEach((l) => l());
  }

  private applyAgentMcp(result: unknown) {
    if (!Array.isArray(result)) return;
    this.agentMcpCache = result as AgentAdminMcServer[];
    this.agentListeners.forEach((l) => l());
  }
}
