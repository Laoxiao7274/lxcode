// 助手域（assistant.* 方法域，助手模式 P1）的请求编排——与 model-admin 等管理域
// 同模式：经连接层 call 发请求、经 emit 出事件，UI 依赖能力接口（AssistantSource）
// 而非本类。
//
// 域隔离（internal/server/dispatch_assistant.go 的硬约束）：assistant.* 只寻址
// space='assistant' 的会话；主域 chat.* 传助手会话 id 报错，反之亦然——本域的
// 每个请求都显式带 session_id，绝不混用主域的连接焦点。
//
// 事件回放：assistant.history 的应答经 snapshotFromWire（与主域 loadHistory /
// childHistory 共用同一份 wire → HistorySnapshot 映射）后发 historyLoaded，
// 打上 space 标记——store 据此把回放归约进助手域 assistantStates。
import type { AgentEvent, AssistantConfig, AssistantSource, ChatSendImage, CompactOutcome, HistorySnapshot, SendOptions, SessionMeta } from "../../shared/types";
import { wireImage } from "../../shared/attachment-send";
import { deriveTailPaging, historyParams, HISTORY_TAIL, snapshotFromWire } from "./history-snapshot";
import type { WsConnection } from "./connection";

export class AssistantDomain implements AssistantSource {
  constructor(
    private readonly conn: WsConnection,
    /** 事件出闸（合帧器之后的 emit——与主域同一条出口）。 */
    private readonly emit: (ev: AgentEvent) => void,
    /** 请求级失败 → operationError 事件（与主域 send 同一条通道）。 */
    private readonly opError: (message: string) => void,
  ) {}

  /** 助手域会话列表（assistant.list）：wire 形状与 session.list 同源
   *  （snake_case SessionMeta 数组 + running 位）——映射与主域 applySessionList
   *  同款，只是就地返回不进主域缓存（助手域列表由 AssistantWorkspace 持有）。 */
  async list(): Promise<SessionMeta[]> {
    const r = await this.conn.call("assistant.list");
    return this.mapAssistantMetas(r);
  }

  /** 某项目的助手任务子会话（assistant.tasks）：应答与 assistant.list 同构
   *  （AssistantListResult + running 位）——同一份映射。 */
  async tasks(projectId: string): Promise<SessionMeta[]> {
    const r = await this.conn.call("assistant.tasks", { project_id: projectId });
    return this.mapAssistantMetas(r);
  }

  /** assistant.list / assistant.tasks 共用的 wire → SessionMeta 映射
   *  （snake_case + running 位；与主域 applySessionList 同款但就地返回）。 */
  private mapAssistantMetas(r: unknown): SessionMeta[] {
    const raw = (r as { sessions?: unknown } | null)?.sessions;
    const arr = Array.isArray(raw) ? raw : [];
    return arr.map((s) => {
      const m = s as { id: string; title: string; updated_at: string; messages: number; archived?: boolean; running?: boolean };
      return {
        id: m.id,
        title: m.title,
        updatedAt: m.updated_at,
        messages: m.messages,
        archived: Boolean(m.archived),
        running: Boolean(m.running),
      };
    });
  }

  /** 助手域的 historyLoaded（historyLoaded 变体没有逐键声明 space——与 store.ts
   *  的运行时判定同一条注释：标记只在 assistant 路径写入）。 */
  private emitHistoryLoaded(sessionId: string, snapshot: HistorySnapshot, beforeSeq?: number): void {
    this.emit({
      type: "historyLoaded", sessionId, history: snapshot,
      ...(beforeSeq ? { paging: true } : {}),
      space: "assistant",
    } as AgentEvent);
  }

  async newSession(): Promise<string> {
    const r = (await this.conn.call("assistant.new")) as { session_id?: string } | null;
    const id = String(r?.session_id ?? "");
    if (!id) throw new Error("服务端未返回 session_id");
    return id;
  }

  send(sessionId: string, text: string, opts?: { effort?: string; approval?: string; images?: ChatSendImage[] }): void {
    if (!sessionId || (!text.trim() && !(opts?.images?.length))) return;
    // 参数 = ChatSendParams 的子集：session_id/text 必带，effort/approval/images
    // 可选（2026-12 助手附件：images 走与 chat.send 同款 wireImage——path 形态只带
    // 路径，file 对象在 wireImage 里剥掉，绝不上 wire）。
    const params: Record<string, unknown> = { session_id: sessionId, text };
    if (opts?.effort) params.effort = opts.effort;
    if (opts?.approval) params.approval = opts.approval;
    if (opts?.images?.length) params.images = opts.images.map(wireImage);
    // 无乐观气泡：助手域的 userMessage 回执也排在准备工作之后，但助手页签是
    // 独立时间线、无发送缓冲区，首条消息的空窗用 busy 事件的「正在处理」行呈现
    //（与 ChildSessionPage 同款）——不引入主域的 pending 机制（第二份 FIFO 去重
    // 的维护成本超过收益，风险明确：首字之前时间线只有 busy 行）。
    this.conn.call("assistant.send", params).catch((e) => {
      this.opError(`发送失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }

  async history(sessionId: string, beforeSeq?: number): Promise<HistorySnapshot> {
    const r = await this.conn.call("assistant.history", historyParams(sessionId, beforeSeq));
    const snapshot = deriveTailPaging(snapshotFromWire(r), HISTORY_TAIL);
    // 用快照自己的 session_id（后端回带；缺席回落请求参数）
    const id = snapshot.sessionId || sessionId;
    this.emitHistoryLoaded(id, snapshot, beforeSeq);
    return snapshot;
  }

  async childHistory(sessionId: string, beforeSeq?: number): Promise<HistorySnapshot> {
    // 助手子会话的域内寻址：assistant.history（子会话库行继承父 space='assistant'）。
    // child 标记同主域 childHistory：子时间线里非通告的 user 消息 = 任务说明书。
    const r = await this.conn.call("assistant.history", historyParams(sessionId, beforeSeq));
    const snapshot = deriveTailPaging(snapshotFromWire(r), HISTORY_TAIL);
    snapshot.child = true;
    const id = snapshot.sessionId || sessionId;
    this.emitHistoryLoaded(id, snapshot, beforeSeq);
    return snapshot;
  }

  cancel(sessionId: string): void {
    this.conn.call("assistant.cancel", { session_id: sessionId }).catch((e) => {
      this.opError(`取消失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }

  confirm(sessionId: string, id: string, allow: boolean, answer?: string): Promise<void> {
    return this.conn
      .call("assistant.confirm", { session_id: sessionId, id, allow, ...(answer ? { answer } : {}) })
      .then(() => undefined);
  }

  compact(sessionId: string): Promise<CompactOutcome> {
    return this.conn
      .call("assistant.compact", { session_id: sessionId })
      .then((r) => (r ?? { compacted: false }) as CompactOutcome);
  }

  setApproval(sessionId: string, mode: string): void {
    this.conn
      .call("assistant.approval", { session_id: sessionId, approval: mode })
      .catch((e) => {
        this.opError(`切换权限档失败: ${e instanceof Error ? e.message : String(e)}`);
      });
  }

  setModel(sessionId: string, modelId: string): void {
    this.conn
      .call("assistant.model", { session_id: sessionId, model_id: modelId })
      .catch((e) => {
        this.opError(`切换模型失败: ${e instanceof Error ? e.message : String(e)}`);
      });
  }

  /** 读助手配置（assistant.config.get）。各字段空 = 未设置（用内置值）。 */
  async getConfig(): Promise<AssistantConfig> {
    const r = await this.conn.call("assistant.config.get");
    const cfg = (r ?? {}) as Record<string, unknown>;
    return {
      ...(typeof cfg.name === "string" && cfg.name ? { name: cfg.name } : {}),
      ...(typeof cfg.persona === "string" && cfg.persona ? { persona: cfg.persona } : {}),
      ...(Array.isArray(cfg.skills) && cfg.skills.length ? { skills: cfg.skills.map(String) } : {}),
      ...(typeof cfg.workflow === "string" && cfg.workflow ? { workflow: cfg.workflow } : {}),
      ...(Array.isArray(cfg.mcpServers) && cfg.mcpServers.length
        ? { mcpServers: cfg.mcpServers.map(String) }
        : {}),
      ...(Array.isArray(cfg.tools) && cfg.tools.length ? { tools: cfg.tools.map(String) } : {}),
      ...(Array.isArray(cfg.toolWhitelist) && cfg.toolWhitelist.length
        ? { toolWhitelist: cfg.toolWhitelist.map(String) }
        : {}),
      ...(Array.isArray(cfg.builtinDefaults) && cfg.builtinDefaults.length
        ? { builtinDefaults: cfg.builtinDefaults.map(String) }
        : {}),
    };
  }

  /** 写助手配置（assistant.config.set，全量替换）。失败上抛——调用方
   *  （拓展页的助手页签）把后端的人话校验错误（无效模块 id / MCP 服务器 id
   *  等）显示给用户。 */
  async setConfig(cfg: AssistantConfig): Promise<void> {
    await this.conn.call("assistant.config.set", {
      ...(cfg.name ? { name: cfg.name } : {}),
      ...(cfg.persona ? { persona: cfg.persona } : {}),
      ...(cfg.skills?.length ? { skills: cfg.skills } : {}),
      ...(cfg.workflow ? { workflow: cfg.workflow } : {}),
      ...(cfg.mcpServers?.length ? { mcpServers: cfg.mcpServers } : {}),
      ...(cfg.tools?.length ? { tools: cfg.tools } : {}),
      // 白名单是全量替换语义的一个字段：非空 = 覆盖、空 = 用内置默认——
      // **不空发**：setConfig 传空数组与不带键在后端是同一语义（省略 = 清空），
      // 前端用「白名单非空才带键」表达（恢复默认 = 不带键）。
      ...(cfg.toolWhitelist?.length ? { toolWhitelist: cfg.toolWhitelist } : {}),
    });
  }
}

/** SendOptions 到 assistant.send 参数的收敛（AssistantPage 用——SendOptions 的
 *  files 在助手域不接（输入区是图片精简版），显式只透传 effort 与 images）。 */
export function assistantSendOpts(opts?: SendOptions): { effort?: string; images?: SendOptions["images"] } | undefined {
  const out: { effort?: string; images?: SendOptions["images"] } = {};
  if (opts?.effort) out.effort = opts.effort;
  if (opts?.images?.length) out.images = opts.images;
  return Object.keys(out).length ? out : undefined;
}
