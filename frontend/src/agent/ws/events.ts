// 后端事件 → AgentEvent 的**纯映射**（无 I/O、无 this、可单测）。
//
// 与 index.ts 的 reactTo 分工：这里只回答「这个事件在前端长什么样」，
// 「要不要重拉后端事实源」留在 reactTo。混在一起就没法单独测映射——
// 而映射恰恰是最容易写错、也最值得钉住的一层（字段名、默认值、归属）。
import type { AgentEvent, ConfirmRequest, ContextUsage, TodoItem } from "../../shared/types";
import { jobFromWire } from "../../shared/jobs";

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
      return { type: "userMessage", sessionId, text: String(message.content ?? p.content ?? p.text ?? "") };
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
        dispatchId,
        // 上下文占用只随主轮来（子轮的 done 不带——后端已按 dispatch 归属收口）
        context: (p.context as ContextUsage | undefined) ?? undefined,
      };
    case "chat.error":
      return { type: "error", sessionId, message: String(p.message ?? ""), aborted: Boolean(p.aborted) };
    case "chat.busy":
      return { type: "busy", sessionId, busy: Boolean(p.busy) };
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
