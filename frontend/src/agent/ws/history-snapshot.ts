// wire → HistorySnapshot 的唯一一份映射（loadHistory 与 childHistory 共用）。
// 各写一份的代价是"子会话渲染的字段与主时间线悄悄不一致"——而两边渲染用的是
// 同一个 historyBlocks，形状一漂就是同一段历史两种画法。
import type { ContextUsage, HistorySnapshot, SessionStats, TodoItem } from "../../shared/types";
import { normalizeApproval } from "../../shared/approval";
import type { ConfirmRequest } from "../../shared/types";

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
  };
}
