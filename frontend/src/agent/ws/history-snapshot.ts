// wire → HistorySnapshot 的唯一一份映射（loadHistory 与 childHistory 共用）。
// 各写一份的代价是"子会话渲染的字段与主时间线悄悄不一致"——而两边渲染用的是
// 同一个 historyBlocks，形状一漂就是同一段历史两种画法。
import type { ContextUsage, HistorySnapshot, SessionStats, TodoItem } from "../../shared/types";
import { normalizeApproval } from "../../shared/approval";
import type { ConfirmRequest } from "../../shared/types";

/** 首屏装载的**尾部窗口**条数（慢链路分页：先拉最新 N 条，上滑再补更早的）。
 *  0/缺省 = 全量（老语义）。240 的取法：工具行 + 确认卡 + 派发卡让「一条消息」
 *  平均折成 1.5~2 个块，240 条 ≈ 首屏 400 块，覆盖几次上翻才需要分页。 */
export const HISTORY_TAIL = 240;

/** 每次上翻补多少条（before_seq 请求的 tail）。 */
export const HISTORY_PAGE = 240;

/** 前端 → chat.history 的**唯一**参数构造器（首屏 tail 与上翻 before_seq 共用）。
 *
 *  - 首屏：`{session_id, tail: HISTORY_TAIL}`——只拉尾部窗口；
 *  - 上翻：`{session_id, tail: HISTORY_PAGE, before_seq: firstSeq}`——anchor 之前
 *    再拉一页（协议要求 before_seq 必须配 tail，否则后端判 CodeInvalidParams）。
 *  字段名与 Go protocol.ChatHistoryParams 逐字一致（错一个不会编译报错，只会
 *  静默丢掉分页语义——后端按全量返回）。 */
export function historyParams(sessionId: string, beforeSeq?: number): Record<string, unknown> {
  const params: Record<string, unknown> = { session_id: sessionId, tail: HISTORY_TAIL };
  if (beforeSeq && beforeSeq > 0) {
    params.before_seq = beforeSeq;
    params.tail = HISTORY_PAGE;
  }
  return params;
}

/** 首屏 tail 应答的「还有更早」推导（**不是编的后端能力**）。
 *
 *  协议里 has_more/first_seq **只在 before_seq 应答**上出现（Tail 应答不带，
 *  见 protocol.ChatHistoryResult.HasMore 的注释）。首屏用 tail 拉窗口，想继续
 *  上翻就必须自己判断「窗口是不是被截过」：
 *   - 后端 TailHistory 的口径是「n ≥ 历史长度 → 等价全量」，所以**返回条数少于
 *     请求的 tail** 只可能意味着已经取到历史开头 → 没有更早的；
 *   - 返回条数 ≥ tail ⇒ 窗口被截过（或者恰好相等）→ 认为可能还有更早的。
 *  这是从**真实返回条数**推出来的，不是猜一个值；配对回退（TailHistory 把起点
 *  往前挪到平衡切点）只会让条数变多，不影响这条判定的方向。
 *  anchor（firstSeq）直接取窗口首条消息自带的 seq——那是 wire 上的真实字段。 */
export function deriveTailPaging(snapshot: HistorySnapshot, requestedTail: number): HistorySnapshot {
  if (snapshot.hasMore !== undefined) return snapshot; // 分页应答：后端权威值，不覆盖
  const messages = snapshot.messages ?? [];
  if (messages.length === 0) return snapshot;
  const first = messages[0];
  const firstSeq = typeof first.seq === "number" ? first.seq : 0;
  return {
    ...snapshot,
    // 条数达到请求窗口 ⇒ 可能还有更早的；没达到 ⇒ 后端已经把整条历史都给了
    hasMore: requestedTail > 0 && messages.length >= requestedTail,
    ...(firstSeq > 0 ? { firstSeq } : {}),
  };
}

export function snapshotFromWire(r: unknown): HistorySnapshot {
  const h = (r ?? {}) as {
    session_id?: string;
    messages?: Array<{
      role: string;
      content: string;
      /** 撤回锚点（ChatMessage 上的字段）——老后端没有，整键缺席。 */
      seq?: number;
      /** 每轮计时/用量/模型（与实时 chat.done 同名字段）——回放路径直接读它。 */
      first_token_ms?: number;
      duration_ms?: number;
      model?: string;
      usage_tokens?: number;
      reasoning_content?: string;
      tool_calls?: Array<{ id?: string; function?: { name: string; arguments?: string } }>;
      tool_call_id?: string;
      attachments?: import("../../shared/types").AttachmentRef[];
      images?: Array<{ path: string; mime: string }>;
    }>;
    busy?: boolean;
    pending?: ConfirmRequest | null;
    todos?: TodoItem[];
    context?: ContextUsage;
    /** 整段会话统计（缺省 = 还没有任何一步——不渲染统计胶囊）。 */
    stats?: SessionStats;
    checkpoints?: number[];
    /** 该会话实际用的模型 id（子会话就是它自己 Agent 的模型）。 */
    model?: string;
    /** 该会话此刻的权限档（老后端没有 → 缺席，显示端不猜）。 */
    approval?: string;
    /** 这次回放对应的叶（0 = 空路径；老后端缺席）。 */
    leaf_seq?: number;
    /** 分页窗口元数据（只在 before_seq 请求的应答里出现，见 HistorySnapshot.hasMore）。 */
    has_more?: boolean;
    first_seq?: number;
  };
  return {
    sessionId: h.session_id ?? "",
    messages: h.messages ?? [],
    busy: Boolean(h.busy),
    pending: h.pending ?? null,
    todos: h.todos ?? [],
    context: h.context,
    stats: h.stats,
    checkpoints: h.checkpoints ?? [],
    model: h.model,
    approval: h.approval === undefined ? undefined : normalizeApproval(h.approval),
    // 叶指针（0 = 空路径，是**合法状态**不是"未知"）：树面板拿它高亮「你在这里」，
    // 也拿它决定「回到最新」的落点。老后端缺席时**不写这个键**——显示端据此
    // 退回"不知道叶在哪"（树面板用 chat.tree 自己的 leaf_seq 兜底）。
    ...(typeof h.leaf_seq === "number" ? { leafSeq: h.leaf_seq } : {}),
    // 分页窗口元数据：只在分页应答（before_seq 请求）里出现。缺省**不写键**
    // （= 不是分页应答 → 不再上翻），与 has_more 的 omitempty 语义一致。
    ...(typeof h.has_more === "boolean" ? { hasMore: h.has_more } : {}),
    ...(typeof h.first_seq === "number" ? { firstSeq: h.first_seq } : {}),
  };
}
