// WSAgent：真实后端对接的门面（WS JSON-RPC，默认 127.0.0.1:7789——协议与 Go
// internal/protocol 一致）。AgentSource + ModelAdminSource + AgentAdminSource +
// SearchAdminSource + JobAdminSource + TodoPromptSource 六能力实现。
//
// 分层（2026-12 前端重构 M4）：连接层（重连/心跳/请求配对/超时/事件分发）在
// connection.ts；五个管理域的请求编排各自成文件（model-admin / agent-admin /
// search-admin / job-admin / todo-prompt）；delta 合帧在 coalesce.ts。本类只做
// 装配与转发——对外接口（AgentSource 等六个）签名与 DemoAgent 完全一致，App 与
// 其他调用方零改动。
//
// 错误纪律：生成类失败（chat.error）走 error 事件（会话状态由 reducer
// 收敛）；请求类失败（拒绝/断连/超时）走 operationError 事件——UI 只提示，
// 不动 blocks/pending。
import type {
  AgentAdminEntry, AgentAdminMcServer, AgentAdminModule, AgentAdminSource, AgentAdminTool, AgentEvent, AgentSource,
  ApprovalMode, ArchiveOutcome, CatalogModelList, CatalogProviderList, CompactOutcome, DiscoverResult, GitOverview,
  HistorySnapshot, JobAdminSource, JobInfo, JobLogResult, ModelAdminSource, ModelEntry, ProjectInstructions,
  ProjectMeta, RewindOutcome, SearchAdminSource, SearchChannel, SearchChannelsSnapshot, SearchTestResult,
  SendAttachments, SendOptions, SessionMeta, TodoItem, TodoPromptMode, TodoPromptSource,
} from "../../shared/types";
import { rewindParams } from "../../shared/blocks";
import { normalizeApproval } from "../../shared/approval";
import { hasPathAtts, isPathReadError, readAttsAsBase64, wireFile, wireImage } from "../../shared/attachment-send";
import { WsConnection, type Listener } from "./connection";
import { snapshotFromWire } from "./history-snapshot";
import { ModelAdmin } from "./model-admin";
import { AgentAdminDomain } from "./agent-admin";
import { SearchAdmin } from "./search-admin";
import { JobAdminDomain } from "./job-admin";
import { TodoPrompt } from "./todo-prompt";

export { wsURL } from "./connection";

export class WSAgent implements AgentSource, ModelAdminSource, AgentAdminSource, SearchAdminSource, JobAdminSource, TodoPromptSource {
  label = "真实后端";
  readonly modelAdmin: ModelAdminSource;
  readonly agentAdmin: AgentAdminSource;
  readonly searchAdmin: SearchAdminSource;
  readonly jobAdmin: JobAdminSource;
  readonly todoPromptAdmin: TodoPromptSource;
  // 管理域转发（implements 六接口——成员在域对象上，这里逐个转发以满足接口
  // 与既有调用方 `agent.modelAdmin.*` / `agent.jobAdmin.*` 两种访问形态）。
  models(): ReturnType<ModelAdminSource["models"]> { return this.modelAdmin.models(); }
  onModelsChanged(l: Parameters<ModelAdminSource["onModelsChanged"]>[0]): () => void { return this.modelAdmin.onModelsChanged(l); }
  addModel(...a: Parameters<ModelAdminSource["addModel"]>): Promise<void> { return this.modelAdmin.addModel(...a); }
  updateModel(...a: Parameters<ModelAdminSource["updateModel"]>): Promise<void> { return this.modelAdmin.updateModel(...a); }
  removeModel(...a: Parameters<ModelAdminSource["removeModel"]>): Promise<void> { return this.modelAdmin.removeModel(...a); }
  setModelEnabled(...a: Parameters<ModelAdminSource["setModelEnabled"]>): Promise<void> { return this.modelAdmin.setModelEnabled(...a); }
  setRole(...a: Parameters<ModelAdminSource["setRole"]>): Promise<void> { return this.modelAdmin.setRole(...a); }
  catalogProviders(...a: Parameters<ModelAdminSource["catalogProviders"]>): Promise<CatalogProviderList> { return this.modelAdmin.catalogProviders(...a); }
  catalogModels(...a: Parameters<ModelAdminSource["catalogModels"]>): Promise<CatalogModelList> { return this.modelAdmin.catalogModels(...a); }
  discoverModels(...a: Parameters<ModelAdminSource["discoverModels"]>): Promise<DiscoverResult> { return this.modelAdmin.discoverModels(...a); }
  agents(): ReturnType<AgentAdminSource["agents"]> { return this.agentAdmin.agents(); }
  modules(): ReturnType<AgentAdminSource["modules"]> { return this.agentAdmin.modules(); }
  tools(): ReturnType<AgentAdminSource["tools"]> { return this.agentAdmin.tools(); }
  mcpServers(): ReturnType<AgentAdminSource["mcpServers"]> { return this.agentAdmin.mcpServers(); }
  onChanged(l: Parameters<AgentAdminSource["onChanged"]>[0]): () => void { return this.agentAdmin.onChanged(l); }
  addAgent(...a: Parameters<AgentAdminSource["addAgent"]>): Promise<void> { return this.agentAdmin.addAgent(...a); }
  updateAgent(...a: Parameters<AgentAdminSource["updateAgent"]>): Promise<void> { return this.agentAdmin.updateAgent(...a); }
  removeAgent(...a: Parameters<AgentAdminSource["removeAgent"]>): Promise<void> { return this.agentAdmin.removeAgent(...a); }
  addModule(...a: Parameters<AgentAdminSource["addModule"]>): Promise<void> { return this.agentAdmin.addModule(...a); }
  updateModule(...a: Parameters<AgentAdminSource["updateModule"]>): Promise<void> { return this.agentAdmin.updateModule(...a); }
  removeModule(...a: Parameters<AgentAdminSource["removeModule"]>): Promise<void> { return this.agentAdmin.removeModule(...a); }
  addTool(...a: Parameters<AgentAdminSource["addTool"]>): Promise<void> { return this.agentAdmin.addTool(...a); }
  updateTool(...a: Parameters<AgentAdminSource["updateTool"]>): Promise<void> { return this.agentAdmin.updateTool(...a); }
  removeTool(...a: Parameters<AgentAdminSource["removeTool"]>): Promise<void> { return this.agentAdmin.removeTool(...a); }
  addMcServer(...a: Parameters<AgentAdminSource["addMcServer"]>): Promise<void> { return this.agentAdmin.addMcServer(...a); }
  updateMcServer(...a: Parameters<AgentAdminSource["updateMcServer"]>): Promise<void> { return this.agentAdmin.updateMcServer(...a); }
  removeMcServer(...a: Parameters<AgentAdminSource["removeMcServer"]>): Promise<void> { return this.agentAdmin.removeMcServer(...a); }
  channels(): ReturnType<SearchAdminSource["channels"]> { return this.searchAdmin.channels(); }
  onChannelsChanged(l: Parameters<SearchAdminSource["onChannelsChanged"]>[0]): () => void { return this.searchAdmin.onChannelsChanged(l); }
  saveChannel(...a: Parameters<SearchAdminSource["saveChannel"]>): Promise<void> { return this.searchAdmin.saveChannel(...a); }
  removeChannel(...a: Parameters<SearchAdminSource["removeChannel"]>): Promise<void> { return this.searchAdmin.removeChannel(...a); }
  setPrimary(...a: Parameters<SearchAdminSource["setPrimary"]>): Promise<void> { return this.searchAdmin.setPrimary(...a); }
  testChannel(...a: Parameters<SearchAdminSource["testChannel"]>): Promise<SearchTestResult> { return this.searchAdmin.testChannel(...a); }
  jobs(): ReturnType<JobAdminSource["jobs"]> { return this.jobAdmin.jobs(); }
  onJobsChanged(l: Parameters<JobAdminSource["onJobsChanged"]>[0]): () => void { return this.jobAdmin.onJobsChanged(l); }
  listJobs(...a: Parameters<JobAdminSource["listJobs"]>): Promise<JobInfo[]> { return this.jobAdmin.listJobs(...a); }
  killJob(...a: Parameters<JobAdminSource["killJob"]>): Promise<JobInfo> { return this.jobAdmin.killJob(...a); }
  readJobLog(...a: Parameters<JobAdminSource["readJobLog"]>): Promise<JobLogResult> { return this.jobAdmin.readJobLog(...a); }
  mode(): ReturnType<TodoPromptSource["mode"]> { return this.todoPromptAdmin.mode(); }
  onChange(l: Parameters<TodoPromptSource["onChange"]>[0]): () => void { return this.todoPromptAdmin.onChange(l); }
  set(...a: Parameters<TodoPromptSource["set"]>): Promise<void> { return this.todoPromptAdmin.set(...a); }
  /** 连接层：重连/心跳/请求配对/超时/事件分发（见 connection.ts）。 */
  private readonly conn: WsConnection;
  /** 会话域缓存（sessions/projects——AgentSource 自身的数据源）。 */
  private sessionsCache: SessionMeta[] = [];
  private projectsCache: ProjectMeta[] = [];
  /** 本连接内已成功装载过历史的会话 id（resumeSession 的「秒回」依据，见
   *  resumeSession 注释）。握手成功清账（换后端 = 旧会话 id 全部作废）。 */
  private loadedSessions = new Set<string>();

  constructor(addr = "127.0.0.1:7789", token?: string | (() => string | null)) {
    this.conn = new WsConnection(addr, token);
    this.modelAdmin = new ModelAdmin(this.conn);
    this.agentAdmin = new AgentAdminDomain(this.conn);
    this.searchAdmin = new SearchAdmin(this.conn);
    this.jobAdmin = new JobAdminDomain(this.conn);
    this.todoPromptAdmin = new TodoPrompt(this.conn);
    this.wireAgentSourceSideEffects();
  }

  /** AgentSource 域自己的事件副作用与握手刷新注册（会话/项目列表缓存）。 */
  private wireAgentSourceSideEffects() {
    this.conn.onInitList("project.list", (r) => this.applyProjectList(r));
    // session.list 的刷新同时服务握手并行组与两条事件副作用（chat.done 主轮 /
    // session.changed——applySessionList 自己会广播 sessionsChanged，这里不重复发）。
    this.conn.onInitList("session.list", (r) => this.applySessionList(r));
    // 后端切换清账：握手成功那一刻清旧后端的会话缓存（连接层清焦点，缓存在这里）
    this.conn.onHelloReset(() => {
      this.sessionsCache = [];
      this.projectsCache = [];
      this.loadedSessions.clear();
    });
    this.conn.onEventSideEffect((method, _params, ev) => {
      if (method === "chat.done") {
        // 主轮结束——会话列表元数据（标题/时间/消息数）可能变了：重拉
        //（子轮的 done 不触发——dispatchId 归属时不刷列表）
        if (ev?.type === "done" && !ev.dispatchId) this.refreshSessionList();
        return;
      }
      if (method === "session.changed") {
        // 元数据变化不切焦点或重载历史；列表缓存更新后通知 UI 重读
        this.refreshSessionList();
      }
    });
  }

  // ---- 连接层转发（连接管理 / 事件订阅） ----

  setBackend(addr: string, token?: string | null): void {
    this.conn.setBackend(addr, token);
  }

  subscribe(listener: Listener): () => void {
    return this.conn.subscribe(listener);
  }

  /** 测试插桩口：直接驱动连接流程（ws.test.mjs 用 agent['connect']()）。 */
  private connect(): void {
    this.conn.connect();
  }

  /** 测试插桩口：断线后 pending 的重连 timer（ws.test.mjs 断言它被清/在）。 */
  private get reconnectTimer(): ReturnType<typeof setTimeout> | null {
    return this.conn.pendingReconnect;
  }

  /** 测试插桩口：按会话拉历史并重放（ws.test.mjs 直接调用私有形态）。
   *  无参调用保持原 wire 形态（params 带 session_id: undefined → JSON 丢键）。
   *  装载成功记入 loadedSessions（resumeSession 的秒回依据——失败不记，
   *  空历史也是一次成功装载）。 */
  private loadHistory(sessionId?: string): Promise<void> {
    return this.call("chat.history", { session_id: sessionId })
      .then((r) => {
        if (sessionId) this.loadedSessions.add(sessionId);
        this.emit({ type: "historyLoaded", sessionId: String(sessionId ?? ""), history: snapshotFromWire(r) });
      }) as Promise<void>;
  }

  /** 发 JSON-RPC 请求（转发连接层；测试可插桩——message-actions.test.mjs）。 */
  private call(method: string, params?: unknown): Promise<unknown> {
    return this.conn.call(method, params);
  }

  /** 事件出闸（经合帧器；测试可插桩——message-actions.test.mjs）。 */
  private emit(ev: AgentEvent): void {
    this.conn.emit(ev);
  }

  /** 请求级失败 → operationError 事件。 */
  private opError(message: string): void {
    this.conn.opError(message);
  }

  /** 连接层的历史重放（boot/resume 链内部用——不经测试插桩口）。
   *  装载成功同样记入 loadedSessions（与插桩口同一份记忆——resume 链的秒回）。 */
  private loadHistoryConn(sessionId: string): Promise<void> {
    return this.conn.loadHistory(sessionId).then(() => { this.loadedSessions.add(sessionId); });
  }

  /** 切换连接焦点会话（发 sessionFocused）。 */
  private focusSession(id: string): void {
    this.conn.focus(id);
  }

  // ---- AgentSource 接口 ----

  send(sessionId: string, text: string, opts?: SendOptions): void {
    // 空文本但有附件也放行（图片批次 B）：后端把附件落盘并以「[附件]」行
    // 充当消息正文——只发一个文件不打字是正常用法。
    const hasAtts = Boolean(opts?.images?.length || opts?.files?.length);
    if (!sessionId || (!text.trim() && !hasAtts)) return;
    // 每个请求都标明目标 Session；连接焦点只是旧客户端兼容回退。
    const params: Record<string, unknown> = { session_id: sessionId, text };
    if (opts?.effort) params.effort = opts.effort;
    // 审批档不再随 send 上车：它是后端持久化的会话级状态（chat.approval 改、
    // 新会话按 behavior 默认档初始化）——恒带请求级 approval 会静默覆盖会话档
    // 且不广播（持久化/隔离问题的根因之一）。协议侧参数仍保留（老客户端兼容）。
    if (opts?.agent) params.agent = opts.agent;
    // 附件（图片批次 B + 2026-12 路径优先）：双形态——path 非空 = 本地路径形态
    //（wire 上只有路径，后端按路径读盘；file 对象在 wireImage/wireFile 里剥掉，
    // 绝不上 wire）；path 为空 = base64 形态（粘贴截图/虚拟文件/浏览器模式）。
    if (opts?.images?.length) params.images = opts.images.map(wireImage);
    if (opts?.files?.length) params.files = opts.files.map(wireFile);
    const atts: SendAttachments | undefined = hasAtts
      ? { images: opts?.images ?? [], files: opts?.files ?? [] }
      : undefined;
    // 乐观用户气泡（体验修复批次 2）：请求已发起就立刻给视觉反馈——真正的用户
    // 气泡要等后端 chat.userMessage 回执，而回执排在 worktree 准备等工作之后
    //（新会话首条消息空白等待数百毫秒）。收到回执后由归约器按 FIFO 去重；
    // 请求失败（超时/断连/后端拒绝）在这里发 failed 变体移除 pending 块，
    // 错误文案走既有 operationError 通道。failed 变体带回原附件——store 的
    // onSendFailed 钩子把它装回输入框附件区（文本丢了附件不能丢）。
    this.emit({ type: "optimisticUser", sessionId, text, ...(atts ? { atts } : {}) });
    const degrade = hasPathAtts(opts?.images, opts?.files);
    this.call("chat.send", params).catch((e) => {
      // 降级链路（path 失败 → base64 重发一次）：后端读不到 path（文件在暂存
      // 之后被移动/删除）报「本地文件读取失败」类错误——用暂存时留在手上的
      // File 对象把同一批文件读成 base64 重发一次。降级成功 = 乐观气泡照常
      // 等回执；降级也失败（File 快照也没了/重发仍被拒）才走失败路径。
      if (degrade && isPathReadError(e?.message)) {
        void this.degradeResend(sessionId, text, opts, atts);
        return;
      }
      this.emit({ type: "optimisticUser", sessionId, text, failed: true, ...(atts ? { atts } : {}) });
      this.opError(`发送失败: ${e.message}`);
    });
  }

  /** path 形态发送失败的降级重发：全部 path 条目按 File 读成 base64，重发一次。
   *  成功 → 什么都不做（乐观气泡继续等后端回执）；失败 → 原样走失败路径
   * （failed 乐观气泡装回附件区 + operationError 人话文案）。 */
  private async degradeResend(
    sessionId: string,
    text: string,
    opts: SendOptions | undefined,
    atts: SendAttachments | undefined,
  ): Promise<void> {
    try {
      const b64 = await readAttsAsBase64(opts?.images, opts?.files);
      const params: Record<string, unknown> = { session_id: sessionId, text };
      if (opts?.effort) params.effort = opts.effort;
      if (opts?.agent) params.agent = opts.agent;
      if (b64.images.length) params.images = b64.images.map(wireImage);
      if (b64.files.length) params.files = b64.files.map(wireFile);
      await this.call("chat.send", params);
    } catch (e) {
      this.emit({ type: "optimisticUser", sessionId, text, failed: true, ...(atts ? { atts } : {}) });
      this.opError(`发送失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /** 中途改权限档（chat.approval）：**立刻生效于运行中的那一轮**。
   *
   *  与 chat.send 的 approval 参数分工明确：后者是请求级、要等下一轮才到后端，
   *  用户实测的 bug 正是「改完档还在弹确认」——那一轮早就开跑了，读不到新档。
   *  会话 id 取连接焦点（UI 的操作永远发生在某个已聚焦的会话上）。 */
  setApproval(mode: ApprovalMode): Promise<void> {
    return this.call("chat.approval", { session_id: this.conn.sessionFocus, approval: mode }).then(() => undefined);
  }

  /** 改**当前会话**选中的模型（chat.model）：后端落库 + 广播 chat.modelChanged，
   *  只改这一条会话（全局 default 角色不受影响——那是「新会话的默认模型」）。
   *
   *  会话 id 取连接焦点（与 setApproval 同一条约定）。选中态不在这里乐观改：
   *  归约层按 modelChanged 广播/chat.history 的 model 对齐——后端没改成功就不会
   *  广播，显示自然停在旧模型上（不假装成功）。 */
  setSessionModel(modelId: string): Promise<void> {
    return this.call("chat.model", { session_id: this.conn.sessionFocus, model_id: modelId }).then(() => undefined);
  }

  /** 读「新会话默认审批档」（settings.defaultApproval.get——后端 behavior.json）。 */
  defaultApproval(): Promise<ApprovalMode> {
    return this.call("settings.defaultApproval.get", {}).then((r) =>
      normalizeApproval((r as { approval?: string } | null)?.approval));
  }

  /** 写「新会话默认审批档」（settings.defaultApproval.set：落盘 + 广播）。
   *  只影响之后新建的会话；既有会话的档位是它们自己的落库值。 */
  setDefaultApproval(mode: ApprovalMode): Promise<void> {
    return this.call("settings.defaultApproval.set", { approval: mode }).then(() => undefined);
  }

  /** 裁决确认门。resolve = 后端确认成功（结果随后以 toolResult 到达）；
   *  reject（确认丢失/已终结/断连）向上抛——调用方决定卡片回退与否。 */
  confirm(sessionId: string, id: string, allow: boolean): Promise<void> {
    return this.call("tool.confirm", { session_id: sessionId, id, allow }).then(() => undefined);
  }

  /** 回答 ask_user 的提问（tool.confirm 带 answer）：与 confirm 同一个方法、
   *  同一条路由规则——answer 非空时后端走 Answer 路径（文本答案投进与二元
   *  确认同一个等待通道）。 */
  answer(sessionId: string, id: string, text: string): Promise<void> {
    return this.call("tool.confirm", { session_id: sessionId, id, allow: true, answer: text }).then(() => undefined);
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
      // 空会话也拉一次历史（**历史先到、焦点后切**，与 resumeSession 同一条帧序
      // 纪律）：chat.history 回带该会话的初始审批档（behavior 默认档）——不拉的
      // 话显示端沿用上一会话的档位镜像（串台），用户在新会话里看到的权限档是假的。
      try {
        await this.loadHistoryConn(id);
      } catch {
        // 空会话读历史失败不挡建会话：显示层回落缺省档，下次装载历史会对齐
      }
      this.focusSession(id);
      return id;
    } catch (e) {
      this.opError(`新建会话失败: ${e instanceof Error ? e.message : String(e)}`);
      return "";
    }
  }

  /** git.overview：项目主检出的只读快照（Git 管理页数据源）。 */
  async gitOverview(projectId?: string): Promise<GitOverview> {
    const result = await this.call("git.overview", projectId ? { project_id: projectId } : undefined);
    return result as GitOverview;
  }

  /** git.diff：主检出工作区里单个文件的未提交差异（懒加载）。 */
  async gitDiff(projectId: string, path: string): Promise<string> {
    const result = await this.call("git.diff", { project_id: projectId, path }) as { diff?: string } | null;
    return String(result?.diff ?? "");
  }

  /** 起一个后台合并进程（chat.mergeRequest）：**遗留**——合并流程已退役，后端
   *  如实回说明并以错误码拒绝。保留方法签名便于将来恢复隔离模式。 */
  async mergeRequest(sessionId: string, targetBranch?: string): Promise<string> {
    const params: Record<string, string> = { session_id: sessionId };
    if (targetBranch && targetBranch.trim()) params.target_branch = targetBranch.trim();
    const r = (await this.call("chat.mergeRequest", params)) as { job_id?: string } | null;
    const id = String(r?.job_id ?? "");
    if (!id) throw new Error("服务端未返回任务 id");
    return id;
  }

  async resumeSession(id: string): Promise<void> {
    // 切回已装载过的会话（本连接内 history 装载成功的记忆）：**本地秒回**——
    // 发 sessionFocused，不发 session.resume / chat.history。所有后续操作都显式
    // 带 session_id（见 send 注释），连接焦点只为兼容旧客户端与首屏 UI 服务，
    // 所以省掉这两个往返是安全的。收益：
    //  ① 大会话 chat.history 全量重载（磁盘 IO + JSON 反序列化 + 块重建）整段
    //     跳过——往返本身可能上百毫秒（HEAVY_METHODS 为它放宽到 30s 就是佐证）；
    //  ② 时间线整列表全量重建（reduceHistory 逐块新对象）连带 React 协调整屏，
    //     「切回来滚一下」的常见操作直接归零。
    // 快照照旧由后端/浏览器历史装载路径兜底：切走后回放出的增量（别的会话跑完
    // 回写、重连重放）不追——后续轮次的 chat.done/事件本来就会实时到达本连接，
    // 重连/换后端清缓存（onHelloReset）后一切照旧全量装载。演示源（DemoAgent）
    // 不走这里（它没有 loadHistory 纪律），行为不变。
    if (this.loadedSessions.has(id) && this.conn.sessionFocus !== id) {
      this.focusSession(id);
      return;
    }
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
      await this.loadHistoryConn(id);
    } catch (e) {
      this.opError(`读取会话历史失败: ${e instanceof Error ? e.message : String(e)}`);
    }
    this.focusSession(id);
  }

  renameSession(id: string, title: string): void {
    const t = title.trim();
    if (!t) return;
    // 列表更新走 session.changed 广播（后端事实源），这里只发请求
    this.call("session.rename", { id, title: t })
      .then(() => undefined)
      .catch((e) => this.opError(`重命名失败: ${e.message}`));
  }

  async archiveSession(id: string): Promise<ArchiveOutcome> {
    const result = await this.call("session.archive", { id, archived: true }) as {
      archived?: boolean;
    } | null;
    return { archived: Boolean(result?.archived ?? true) };
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

  /** 重拉会话列表（chat.done 主轮 / session.changed 共用）。 */
  private refreshSessionList() {
    this.call("session.list")
      .then((r) => this.applySessionList(r))
      .catch((e) => this.opError(`刷新会话列表失败: ${e.message}`));
  }

  /** 后端 session.list 结果 → 缓存（协议 snake_case → 前端 camelCase 映射）。 */
  private applySessionList(result: unknown) {
    const list = result as Array<{ id: string; title: string; updated_at: string; messages: number; archived?: boolean; workspace?: string; running?: boolean }>;
    if (!Array.isArray(list)) return;
    // 此刻在跑的会话 id（事件载荷——App 据此初始化 busyBySession 投影，冷启动
    // /重连后项目行运行指示立即亮，不等点进会话的 chat.history）。
    const running = list.filter((s) => s.running).map((s) => s.id);
    this.sessionsCache = list.map((s) => ({
      id: s.id,
      title: s.title,
      updatedAt: s.updated_at,
      messages: s.messages,
      archived: Boolean(s.archived),
      workspace: s.workspace ?? "",
      // 此刻是否在生成（老后端缺席 = false/undefined，不编状态）：
      // 新连上的客户端错过 busy=true 广播时，列表据此直接亮运行指示。
      running: Boolean(s.running),
    }));
    this.emit({ type: "sessionsChanged", running });
  }

  /** 后端 project.list 结果 → 缓存。 */
  private applyProjectList(result: unknown) {
    const list = result as Array<{ id: string; name: string; path: string }>;
    if (!Array.isArray(list)) return;
    this.projectsCache = list.map((p) => ({ id: p.id, name: p.name, path: p.path }));
    this.emit({ type: "projectsChanged" });
  }

  // ---- 子会话历史（childHistory 在 AgentSource 上，历史映射在连接层） ----

  /** 读子会话的历史（AGENTS.md §2.3：子 Agent = 独立会话，它自己的 messages 与
   *  压缩检查点在库里另存一份，父会话历史里没有这些明细）。
   *
   *  与 loadHistory 走同一条协议方法（chat.history 按 session_id 寻址），也**发
   *  historyLoaded**（2026-09-30 用户拍板：子会话视图要看到实时流，所以它得先有
   *  自己的 state —— 历史是基线、随后的事件是增量）。
   *
   *  与 loadHistory 的**关键差别是事件里的 sessionId 是子会话的那个**（快照的
   *  session_id 就是按它查的）：store 按 sessionId 路由，重建的是**子会话自己**
   *  的 state，主时间线一个块都不动。早先不发这个事件是因为那时子会话没有自己的
   *  state（子历史只是卡内的只读补充），发它只会白白重建一份没人看的状态。
   *
   *  失败原样抛（断连/超时/子会话不存在）：调用方（App 打开标签、卡片的懒加载）
   *  必须把原因显示出来——静默吞掉会让用户以为"子会话本来就是空的"。 */
  async childHistory(sessionId: string): Promise<HistorySnapshot> {
    return this.conn.childHistory(sessionId);
  }
}
