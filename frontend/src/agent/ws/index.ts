// WSAgent：真实后端对接（WS JSON-RPC，默认 127.0.0.1:7789——协议与 Go
// internal/protocol 一致）。AgentSource + ModelAdminSource + AgentAdminSource +
// SearchAdminSource 四能力实现。事件 → AgentEvent 映射与 DemoAgent 可互换
// （工厂一行切换）。
// 错误纪律：生成类失败（chat.error）走 error 事件（会话状态由 reducer
// 收敛）；请求类失败（拒绝/断连/超时）走 operationError 事件——UI 只提示，
// 不动 blocks/pending。
import type {
  AgentAdminEntry, AgentAdminMcServer, AgentAdminModule, AgentAdminSource, AgentAdminTool,
  AgentEvent, AgentSource, ApprovalMode, CompactOutcome, ConfirmRequest, ContextUsage, HistorySnapshot, JobAdminSource, JobInfo,
  JobLogResult, ModelAdminSource, ModelEntry,
  ProjectInstructions, ProjectMeta, RewindOutcome, SearchAdminSource, SearchChannel, SearchChannelsSnapshot,
  SearchTestResult, SendOptions, SessionMeta, TodoItem,
} from "../../shared/types";
import { rewindParams } from "../../shared/blocks";
import { jobFromWire, sortJobs, upsertJob } from "../../shared/jobs";
import { mapEvent } from "./events";

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
const PROTOCOL_VERSION = "2";

const RECONNECT_MS = 5000;
const REQUEST_TIMEOUT_MS = 10_000;

export class WSAgent implements AgentSource, ModelAdminSource, AgentAdminSource, SearchAdminSource, JobAdminSource {
  label = "真实后端";
  readonly modelAdmin: ModelAdminSource = this;
  readonly agentAdmin: AgentAdminSource = this;
  readonly searchAdmin: SearchAdminSource = this;
  readonly jobAdmin: JobAdminSource = this;
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
  /** 搜索渠道快照（search.changed 驱动刷新；设置面板网页搜索区的数据源）。 */
  private searchCache: SearchChannelsSnapshot = { channels: [], ready: false };
  private searchListeners = new Set<() => void>();
  /** 后台任务快照（job.list 结果 + job.started/settled 增量；顶栏面板的数据源）。 */
  private jobsCache: JobInfo[] = [];
  private jobListeners = new Set<() => void>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  /** 订阅时惰性建连（构造不再触网——测试可先插桩再连接）。 */
  private started = false;
  /** 首次连接是否已为本连接建立焦点 Session；重连恢复原焦点，不清空运行态。 */
  private booted = false;
  private currentSessionId = "";

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
      this.call("connection.hello", { client: "lxcode-web", version: PROTOCOL_VERSION })
        .then(async (result) => {
          const serverVersion = (result as { version?: string } | null)?.version;
          if (serverVersion !== PROTOCOL_VERSION) {
            throw new Error(`协议版本不兼容（客户端 ${PROTOCOL_VERSION}，服务端 ${serverVersion ?? "未知"}）`);
          }
          const refresh = async (method: string, apply: (r: unknown) => void, params?: unknown) => {
            if (this.ws !== ws) return;
            try { const r = await this.call(method, params); if (this.ws === ws) apply(r); }
            catch (e) { if (this.ws === ws) this.opError(`${method}: ${String(e)}`); }
          };
          await refresh("model.list", (r) => this.applyModelList(r));
          await refresh("project.list", (r) => this.applyProjectList(r));
          await refresh("session.list", (r) => this.applySessionList(r));
          await refresh("agent.list", (r) => this.applyAgentList(r));
          await refresh("catalog.modules.list", (r) => this.applyAgentModules(r));
          await refresh("catalog.tools.list", (r) => this.applyAgentTools(r));
          await refresh("catalog.mcp.list", (r) => this.applyAgentMcp(r));
          await refresh("search.channels.list", (r) => this.applySearchChannels(r));
          // 后台任务：重连/刷新后事件不会重放，必须主动拉一次清单
          // （在跑的任务在面板里不能因为重连而消失）
          await refresh("job.list", (r) => this.applyJobList(r));
          // 首次连接开一个空会话；重连则恢复该连接原焦点。所有后续操作都显式带
          // session_id，因此连接焦点只为兼容旧客户端与首屏 UI 服务。
          if (!this.booted && this.ws === ws) {
            this.booted = true;
            try {
              const result = await this.call("session.new") as { session_id?: string };
              if (this.ws === ws && result?.session_id) this.focusSession(result.session_id);
            } catch (e) {
              if (this.ws === ws) {
                this.booted = false;
                this.opError(`新建会话失败: ${String(e)}`);
              }
            }
          } else if (this.ws === ws && this.currentSessionId) {
            await refresh("session.resume", () => {}, { id: this.currentSessionId });
          }
          if (this.ws === ws && this.currentSessionId) await this.loadHistory(this.currentSessionId);
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
      this.opError("后端连接断开，正在重连");
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

  /** 后端事件 → 前端事件（纯映射在 events.ts）+ 副作用（重拉事实源）。 */
  private handleEvent(method: string, params: unknown) {
    // ① 纯映射：绝大多数事件就是「载荷 → AgentEvent」
    const ev = mapEvent(method, params);
    if (ev) this.emit(ev);
    // ② 副作用：后端是事实源，元数据类事件要重拉列表 / 写缓存
    this.reactTo(method, params, ev);
  }

  /** 事件带来的副作用（重拉事实源 / 写缓存）。
   *  与映射分开的理由见 events.ts 头注——映射是纯函数，这里全是 I/O。 */
  private reactTo(method: string, params: unknown, ev: AgentEvent | null) {
    const p = (params ?? {}) as Record<string, unknown>;
    switch (method) {
      case "chat.done":
        // 主轮结束——会话列表元数据（标题/时间/消息数）可能变了：重拉
        //（子轮的 done 不触发——dispatchId 归属时不刷列表）
        if (ev?.type === "done" && !ev.dispatchId) this.refreshSessionList();
        break;
      case "session.changed":
        // 元数据变化不切焦点或重载历史；列表缓存更新后通知 UI 重读
        this.refreshSessionList();
        break;
      case "model.changed":
        // 模型注册表变更——重拉列表（settings 的 providers 数据源）
        this.call("model.list")
          .then((r) => this.applyModelList(r))
          .catch((e) => this.opError(`刷新模型列表失败: ${e.message}`));
        break;
      case "project.changed":
        // 项目增删 → 重拉项目列表（后端事实源）
        this.call("project.list")
          .then((r) => this.applyProjectList(r))
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
        const m = kind === "modules" ? "catalog.modules.list" : kind === "tools" ? "catalog.tools.list" : kind === "mcp" ? "catalog.mcp.list" : "";
        if (m) {
          this.call(m)
            .then((r) => {
              if (kind === "modules") this.applyAgentModules(r);
              else if (kind === "tools") this.applyAgentTools(r);
              else this.applyAgentMcp(r);
            })
            .catch((e) => this.opError(`刷新目录失败: ${e.message}`));
        }
        break;
      }
      case "job.started":
      case "job.settled":
        // 任务事件同时是面板缓存的增量：事件载荷即最新快照，直接 upsert
        //（不再往返 job.list——后端广播时已经把它算好了，与 search.changed 同款）
        if (ev?.type === "jobStarted" || ev?.type === "jobSettled") this.applyJob(ev.job);
        break;
      case "chat.approvalChanged":
        // 权限档变更（多客户端同步）：纯映射已产出 approvalChanged 事件，
        // 落到本地设置由设置层订阅完成（shared/approval.ts）——本层不做 I/O。
        break;
      case "search.changed":
        // 搜索渠道配置变更——载荷就是快照，直接采用（不必再往返一次
        // search.channels.list：后端广播时已经把它算好了）。
        this.applySearchChannels(p);
        break;
    }
  }

  /** 重拉会话列表（chat.done / session.changed 共用）。
   *  applySessionList 自己会广播 sessionsChanged——这里不重复发（原先发两遍，
   *  同一 tick 里让 store 的 revision 跳两次，是拆分时顺手去掉的冗余）。 */
  private refreshSessionList() {
    this.call("session.list")
      .then((r) => this.applySessionList(r))
      .catch((e) => this.opError(`刷新会话列表失败: ${e.message}`));
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

  send(sessionId: string, text: string, opts?: SendOptions): void {
    if (!sessionId || !text.trim()) return;
    // 每个请求都标明目标 Session；连接焦点只是旧客户端兼容回退。
    const params: Record<string, unknown> = { session_id: sessionId, text };
    if (opts?.effort) params.effort = opts.effort;
    if (opts?.approval) params.approval = opts.approval;
    if (opts?.agent) params.agent = opts.agent;
    this.call("chat.send", params).catch((e) => {
      this.opError(`发送失败: ${e.message}`);
    });
  }

  /** 中途改权限档（chat.approval）：**立刻生效于运行中的那一轮**。
   *
   *  与 chat.send 的 approval 参数分工明确：后者是请求级、要等下一轮才到后端，
   *  用户实测的 bug 正是「改完档还在弹确认」——那一轮早就开跑了，读不到新档。
   *  会话 id 取连接焦点（UI 的操作永远发生在某个已聚焦的会话上）。 */
  setApproval(mode: ApprovalMode): Promise<void> {
    return this.call("chat.approval", { session_id: this.currentSessionId, approval: mode }).then(() => undefined);
  }

  /** 裁决确认门。resolve = 后端确认成功（结果随后以 toolResult 到达）；
   *  reject（确认丢失/已终结/断连）向上抛——调用方决定卡片回退与否。 */
  confirm(sessionId: string, id: string, allow: boolean): Promise<void> {
    return this.call("tool.confirm", { session_id: sessionId, id, allow }).then(() => undefined);
  }

  cancel(sessionId: string): void {
    this.call("chat.cancel", { session_id: sessionId }).catch((e) => {
      this.opError(`取消失败: ${e.message}`);
    });
  }

  compact(sessionId: string): Promise<CompactOutcome> {
    return this.call("chat.compact", { session_id: sessionId }).then((r) => (r ?? { compacted: false }) as CompactOutcome);
  }

  /** 会话回退（chat.rewind）：删掉 seq 这条用户消息及其之后的全部历史。
   *
   *  **先本地乐观截断**：等 WS 往返再清，用户会看到一段"点了没反应"。本地发一条
   *  rewound 事件 → 归约器 planRewind 立刻截断；后端广播 chat.rewound 到达时锚点
   *  已不在，是幂等重放（no-op）。乐观回声不知道条数（removed=0）——归约器不拿
   *  它渲染，真值随后端广播到达。
   *
   *  **失败必须用后端真相对齐**：本地已截断而后端没删 = 用户以为撤回了、下次刷新
   *  又全回来（比"点了没反应"更坏，是"骗过一次刷新"）。重放历史是唯一的真相来源。
   *  老后端没有 chat.rewind 方法时走的正是这条路径——报错 + 时间线复原。 */
  async rewind(sessionId: string, seq: number): Promise<RewindOutcome> {
    this.emit({ type: "rewound", sessionId, seq, removed: 0 });
    try {
      const r = (await this.call("chat.rewind", rewindParams(sessionId, seq))) as { removed?: number } | null;
      return { removed: Number(r?.removed ?? 0) };
    } catch (e) {
      await this.loadHistory(sessionId).catch(() => undefined);
      throw e;
    }
  }

  async newSession(workspace?: string): Promise<string> {
    try {
      const result = await this.call("session.new", workspace ? { workspace } : undefined) as { session_id?: string };
      const id = String(result?.session_id ?? "");
      if (!id) throw new Error("服务端未返回 session_id");
      this.focusSession(id);
      return id;
    } catch (e) {
      this.opError(`新建会话失败: ${e instanceof Error ? e.message : String(e)}`);
      return "";
    }
  }

  async releaseWorktree(id: string): Promise<void> {
    await this.call("session.worktree.release", { id });
  }

  async resumeSession(id: string): Promise<void> {
    try {
      await this.call("session.resume", { id });
    } catch (e) {
      this.opError(`恢复会话失败: ${e instanceof Error ? e.message : String(e)}`);
      return;
    }
    // **历史先到、焦点后切**。焦点一换，UI 渲染的就是这个会话的状态，而它的
    // 历史还在路上——先渲染出的是"空会话"（EmptyState「我们做点什么？」），
    // 一个 WS 往返后再被真历史顶掉。帧级实测（点击会话后采样）：+20ms 整块
    // 对话消失、空态出现，+48ms 空态消失、内容回来——用户看到的就是
    // 「进入会话闪两下」。焦点推迟到历史之后，这个中间态根本不会出现。
    // 与 boot 链同一条纪律（session.new 必须先于 chat.history，见 ws.test.mjs）。
    // 历史读失败也照切焦点：点了会话没反应，比闪一下更糟。
    try {
      await this.loadHistory(id);
    } catch (e) {
      this.opError(`读取会话历史失败: ${e instanceof Error ? e.message : String(e)}`);
    }
    this.focusSession(id);
  }

  private focusSession(id: string) {
    this.currentSessionId = id;
    this.emit({ type: "sessionFocused", id });
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

  /** 读项目守则（项目根 AGENTS.md——项目级「自定义指令」）。 */
  async readInstructions(projectId: string): Promise<ProjectInstructions> {
    const r = (await this.call("project.instructions.get", { project_id: projectId })) as {
      path?: string; content?: string; exists?: boolean; note?: string;
    };
    return {
      path: r.path ?? "",
      content: r.content ?? "",
      exists: Boolean(r.exists),
      note: r.note ?? "",
    };
  }

  /** 写项目守则（后端原子写；下一轮提示词就会读到新内容）。 */
  async saveInstructions(projectId: string, content: string): Promise<void> {
    await this.call("project.instructions.save", { project_id: projectId, content });
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

  /** wire → HistorySnapshot：**唯一一份映射**（loadHistory 与 childHistory 共用）。
   *  各写一份的代价是"子会话渲染的字段与主时间线悄悄不一致"——而两边渲染用的是
   *  同一个 historyBlocks，形状一漂就是同一段历史两种画法。 */
  private snapshotFromWire(r: unknown): HistorySnapshot {
    const h = (r ?? {}) as {
      session_id?: string;
      messages?: Array<{
        role: string;
        content: string;
        /** 撤回锚点（ChatMessage 上的字段）——老后端没有，整键缺席。 */
        seq?: number;
        reasoning_content?: string;
        tool_calls?: Array<{ id?: string; function?: { name: string; arguments?: string } }>;
        tool_call_id?: string;
      }>;
      busy?: boolean;
      pending?: ConfirmRequest | null;
      todos?: TodoItem[];
      context?: ContextUsage;
      checkpoints?: number[];
    };
    return {
      sessionId: h.session_id ?? "",
      messages: h.messages ?? [],
      busy: Boolean(h.busy),
      pending: h.pending ?? null,
      todos: h.todos ?? [],
      context: h.context,
      checkpoints: h.checkpoints ?? [],
    };
  }

  /** 拉当前会话的历史并重放视图（连接建立/切换会话后调）。 */
  private loadHistory(sessionId: string): Promise<void> {
    return this.call("chat.history", { session_id: sessionId })
      .then((r) => {
        this.emit({ type: "historyLoaded", sessionId, history: this.snapshotFromWire(r) });
      }) as Promise<void>;
  }

  /** 读子会话的历史（AGENTS.md §2.3：子 Agent = 独立会话，它自己的 messages 与
   *  压缩检查点在库里另存一份，父会话历史里没有这些明细）。
   *
   *  与 loadHistory 走同一条协议方法（chat.history 按 session_id 寻址），但
   *  **返回快照而不是发事件**：子会话历史是某张 dispatch 卡的只读补充，不进
   *  store、不参与主时间线归约——发 historyLoaded 会把卡内的子历史当成主时间线
   *  整块重建（"刷新之后整个对话被一段子 Agent 的过程顶掉"）。
   *
   *  失败原样抛（断连/超时/子会话不存在）：调用方（DispatchCard）必须把原因显示
   *  出来——静默吞掉会让用户以为"子会话本来就是空的"。 */
  async childHistory(sessionId: string): Promise<HistorySnapshot> {
    const r = await this.call("chat.history", { session_id: sessionId });
    return this.snapshotFromWire(r);
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

  // ---- SearchAdminSource（网页搜索渠道——M4） ----

  channels(): SearchChannelsSnapshot {
    return this.searchCache;
  }

  onChannelsChanged(listener: () => void): () => void {
    this.searchListeners.add(listener);
    return () => this.searchListeners.delete(listener);
  }

  saveChannel(
    id: string,
    patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean },
  ): Promise<void> {
    // 未给的字段按现值回填：后端是 upsert 语义（缺省即清空），
    // 不回填的话「只改 base_url」会把已存的 key 抹掉。
    const cur = this.searchCache.channels.find((c) => c.id === id);
    return this.call("search.channel.save", {
      id,
      api_key: patch.apiKey ?? cur?.api_key ?? "",
      base_url: patch.baseUrl ?? cur?.base_url ?? "",
      // options 同样按现值回填：它是整体覆盖而非增量合并，
      // 不回填的话「只改 key」会把已存的 zone/档位抹掉。
      options: patch.options ?? cur?.options ?? {},
      enabled: patch.enabled ?? cur?.enabled ?? true,
    }).then(() => undefined);
  }

  removeChannel(id: string): Promise<void> {
    return this.call("search.channel.remove", { id }).then(() => undefined);
  }

  setPrimary(id: string): Promise<void> {
    return this.call("search.primary.set", { id }).then(() => undefined);
  }

  testChannel(id: string, query?: string): Promise<SearchTestResult> {
    return this.call("search.test", { id, query: query ?? "" }) as Promise<SearchTestResult>;
  }

  /** search.channels.list 结果 / search.changed 载荷 → 缓存 + 通知。 */
  private applySearchChannels(result: unknown) {
    const r = result as Partial<SearchChannelsSnapshot> | null;
    if (!r || !Array.isArray(r.channels)) return;
    this.searchCache = {
      channels: r.channels as SearchChannel[],
      primary: r.primary,
      ready: r.ready === true,
    };
    this.searchListeners.forEach((l) => l());
  }

  // ---- JobAdminSource（后台任务——job.*） ----

  jobs(): JobInfo[] {
    return this.jobsCache;
  }

  onJobsChanged(listener: () => void): () => void {
    this.jobListeners.add(listener);
    return () => this.jobListeners.delete(listener);
  }

  async listJobs(sessionId?: string): Promise<JobInfo[]> {
    const r = await this.call("job.list", sessionId ? { session_id: sessionId } : undefined);
    const list = (Array.isArray(r) ? r : (r as { jobs?: unknown[] } | null)?.jobs ?? []).map(jobFromWire);
    // 按会话过滤的请求不覆盖全局缓存（顶栏面板看的是跨会话全量）。
    if (!sessionId) this.applyJobList({ jobs: list });
    return list;
  }

  async killJob(id: string): Promise<JobInfo> {
    const r = (await this.call("job.kill", { id })) as { job?: unknown } | null;
    // 应答里的 JobInfo 是请求后的即时快照（stopping）；真正的 settle 随后由
    // job.settled 事件补上——两条路都 upsert，重复更新是幂等的。
    const job = jobFromWire(r?.job ?? r);
    this.applyJob(job);
    return job;
  }

  async readJobLog(id: string): Promise<JobLogResult> {
    const r = (await this.call("job.log", { id })) as { data?: string; truncated?: boolean } | null;
    return { data: r?.data ?? "", truncated: Boolean(r?.truncated) };
  }

  /** job.list 结果 → 缓存 + 通知（重连后的全量对齐）。 */
  private applyJobList(result: unknown) {
    const list = Array.isArray(result) ? result : (result as { jobs?: unknown[] } | null)?.jobs;
    if (!Array.isArray(list)) return;
    this.jobsCache = sortJobs(list.map(jobFromWire));
    this.jobListeners.forEach((l) => l());
  }

  /** 单个任务快照 → 缓存 + 通知（job.started/settled 与 kill 的应答共用）。 */
  private applyJob(job: JobInfo) {
    if (!job.id) return;
    this.jobsCache = sortJobs(upsertJob(this.jobsCache, job));
    this.jobListeners.forEach((l) => l());
  }
}
