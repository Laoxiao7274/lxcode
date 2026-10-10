// 后端事件 → AgentEvent 的**纯映射**（无 I/O、无 this、可单测）。
//
// 与 index.ts 的 reactTo 分工：这里只回答「这个事件在前端长什么样」，
// 「要不要重拉后端事实源」留在 reactTo。混在一起就没法单独测映射——
// 而映射恰恰是最容易写错、也最值得钉住的一层（字段名、默认值、归属）。
import type { AgentEvent, ConfirmRequest, ContextUsage, SessionStats, TodoItem } from "../../shared/types";
import { jobFromWire } from "../../shared/jobs";
import { normalizeApproval } from "../../shared/approval";

/** 返回 null = 这个事件不产出前端事件（由 reactTo 的副作用分支处理，或与前端无关）。 */
export function mapEvent(method: string, params: unknown): AgentEvent | null {
  const p = (params ?? {}) as Record<string, unknown>;
  const sessionId = String(p.session_id ?? "");
  // dispatch_id 归属（子 Agent 执行的事件——store 挂 dispatch 卡）
  const dispatchId = p.dispatch_id ? String(p.dispatch_id) : undefined;
  switch (method) {
    case "connection.ready":
      return { type: "ready", server: String(p.server ?? ""), version: String(p.version ?? ""), busy: Boolean(p.busy) };
    case "chat.userMessage": {
      // 协议载荷把消息包在 message 对象里；兼容早期扁平形状。
      const message = (p.message ?? {}) as Record<string, unknown>;
      const text = String(message.content ?? p.content ?? p.text ?? "");
      // seq = 撤回锚点（ChatMessage 上的字段，与历史回放是同一个类型——两条路径
      // 都读 message.seq，不另立字段名）。老后端没有它 → 键**缺席**（不是
      // undefined 值的键）：块不可撤回，但绝不炸（AGENTS.md §5 坑 11）。
      const seq = message.seq;
      return typeof seq === "number"
        ? { type: "userMessage", sessionId, text, seq }
        : { type: "userMessage", sessionId, text };
    }
    case "chat.delta":
      return { type: "delta", sessionId, kind: String(p.kind) as "text" | "reasoning", text: String(p.text ?? ""), dispatchId };
    case "chat.toolCall":
      return { type: "toolCall", sessionId, id: String(p.id), name: String(p.name), arguments: String(p.arguments ?? ""), dispatchId };
    case "chat.toolResult":
      return { type: "toolResult", sessionId, id: String(p.id), name: String(p.name), content: String(p.content ?? ""), isError: Boolean(p.is_error), dispatchId };
    case "chat.dispatchStart":
      return {
        type: "dispatchStart",
        sessionId: String(p.owner_session_id ?? ""),
        dispatchId: String(p.dispatch_id ?? ""),
        childSessionId: p.session_id ? String(p.session_id) : undefined,
        agentId: String(p.agent_id ?? ""),
        agentName: String(p.agent_name ?? ""),
        agentColor: String(p.agent_color ?? "#3b82f6"),
        task: String(p.task ?? ""),
      };
    case "chat.dispatchEnd":
      return {
        type: "dispatchEnd",
        sessionId: String(p.owner_session_id ?? ""),
        dispatchId: String(p.dispatch_id ?? ""),
        childSessionId: p.session_id ? String(p.session_id) : undefined,
        result: String(p.result ?? ""),
        isError: Boolean(p.is_error),
        usageTokens: Number(p.usage_tokens ?? 0),
      };
    case "chat.compacted":
      // 历史被压缩（可能是别的客户端触发的——广播给所有端）。
      // 子会话自己的压缩带 dispatch_id：归属进卡内，不插到主时间线。
      return {
        type: "compacted",
        sessionId,
        before: Number(p.before ?? 0),
        after: Number(p.after ?? 0),
        shadowed: Number(p.shadowed ?? 0),
        summary: String(p.summary ?? ""),
        manual: Boolean(p.manual),
        dispatchId: p.dispatch_id ? String(p.dispatch_id) : undefined,
      };
    // 后台任务：载荷**就是 JobInfo 本身**（不是包一层）。时间线归属取
    // owner_session_id（子 Agent 起的任务挂在**父会话**的时间线上——子会话不进
    // 侧栏，按执行会话上卡等于用户在主对话里什么都看不到），空才回落
    // session_id；两者都空 = 无归属（只进顶栏全局面板）。
    case "job.started":
    case "job.settled": {
      const job = jobFromWire(p);
      const owner = job.owner_session_id || job.session_id;
      if (method === "job.started") return { type: "jobStarted", sessionId: owner, job };
      return { type: "jobSettled", sessionId: owner, job };
    }
    case "chat.rewound":
      // 会话回退：seq 这条用户消息及其之后的全部历史被后端删掉了。这个事件既是
      // 「别的客户端撤回了」的同步，也是本地乐观截断的权威确认——归约器按同一份
      // 判定处理，重复到达是幂等 no-op（见 reduce.ts 的 reduceRewound）。
      // context：后端重算后的占用（撤回删掉一截历史，旧数字一定是错的）。未知时
      // 整键缺席——那时归约器回落中性态「—」，不编一个数。
      return { type: "rewound", sessionId, seq: Number(p.seq ?? 0), removed: Number(p.removed ?? 0), context: (p.context as ContextUsage | undefined) ?? undefined,
        // 重算后的整段统计：撤回删了行，统计会变小——UI 拿它直接刷新统计胶囊
        stats: (p.stats as SessionStats | undefined) ?? undefined };
    case "chat.confirmRequest":
      return { type: "confirmRequest", sessionId, request: p as unknown as ConfirmRequest };
    case "todo.updated":
      return { type: "todoUpdated", sessionId, items: (p.items as TodoItem[]) ?? [] };
    case "chat.done":
      return {
        type: "done",
        sessionId,
        usageTokens: Number(p.usage_tokens ?? 0),
        finishReason: String(p.finish_reason ?? "stop"),
        // 每轮计时/模型：缺席就是 undefined（**不填 0**——0 会被显示成「首字 0ms」的假数据）
        firstTokenMs: typeof p.first_token_ms === "number" ? p.first_token_ms : undefined,
        durationMs: typeof p.duration_ms === "number" ? p.duration_ms : undefined,
        model: typeof p.model === "string" && p.model !== "" ? p.model : undefined,
        dispatchId,
        // 上下文占用只随主轮来（子轮的 done 不带——后端已按 dispatch 归属收口）
        context: (p.context as ContextUsage | undefined) ?? undefined,
        // 整段会话统计同样只随主轮来（口径见 shared/types.ts 的 SessionStats）
        stats: (p.stats as SessionStats | undefined) ?? undefined,
      };
    case "chat.error":
      return { type: "error", sessionId, message: String(p.message ?? ""), aborted: Boolean(p.aborted) };
    case "chat.busy":
      // dispatchId 非空 = 子会话的忙闲（带归属）：归约侧与发送缓冲区边界都按它
      // 过滤（主会话的忙闲不被子会话翻动），双投路径把它送进子会话自己的 state。
      return { type: "busy", sessionId, busy: Boolean(p.busy), dispatchId };
    case "chat.approvalChanged":
      // 某会话的权限档被改了（多客户端同步）。落到设置的反向同步在设置层
      //（shared/approval.ts 的 subscribeApprovalSync）——这里只管形状。
      return { type: "approvalChanged", sessionId, approval: normalizeApproval(p.approval) };
    case "session.changed":
      return { type: "sessionChanged", id: String(p.id ?? ""), reason: String(p.reason ?? "") };
    case "files.changed": {
      // 一轮的产物汇总（验收视图——后端按 edit/write_file 收集）
      const f = p as { files?: Array<{ path: string; added: number; deleted: number; diff: string }> };
      if (!Array.isArray(f.files)) return null;
      return {
        type: "filesChanged",
        sessionId,
        files: f.files.map((x) => ({ path: x.path, added: x.added, deleted: x.deleted, diff: x.diff })),
      };
    }
    default:
      // model.changed / project.changed / agent.changed / catalog.changed /
      // search.changed 只重拉事实源，不产出前端事件（见 index.ts 的 reactTo）。
      return null;
  }
}
