// 事件流 → UI 状态的归约。Thread 的渲染单元是"块"（block）：
// 一条 user 消息、一条 assistant 回复（含正文/思考链/流式态）、一次
// 工具调用（含结果）、一张确认卡、一份任务清单、一条错误。
import { useCallback, useEffect, useState } from "react";
import type { AgentEvent, AgentSource, ConfirmRequest, FileChange, HistorySnapshot, SendOptions, TodoItem } from "./types";

export interface AssistantBlock {
  kind: "assistant";
  uid: number;
  content: string;
  reasoning: string;
  streaming: boolean;
  usageTokens?: number;
}

export type ThreadBlock =
  | { kind: "user"; uid: number; text: string }
  | AssistantBlock
  | { kind: "tool"; uid: number; id: string; name: string; arguments: string; result?: string; isError?: boolean }
  | { kind: "confirm"; uid: number; request: ConfirmRequest; resolved?: "allow" | "deny" }
  | { kind: "todo"; uid: number; items: TodoItem[] }
  | { kind: "files"; uid: number; files: FileChange[] }
  | { kind: "error"; uid: number; message: string; aborted: boolean };

export interface UIState {
  blocks: ThreadBlock[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
}

const initial: UIState = { blocks: [], busy: false, pending: null, todos: [] };

// 块的唯一序号——React 渲染的稳定 key（index 作 key 在插入新块时
// 会错位复用组件实例，是重复渲染类怪象的根因）。
let uidSeq = 0;
const nextUid = () => ++uidSeq;

export function reduce(state: UIState, ev: AgentEvent): UIState {
  switch (ev.type) {
    case "userMessage":
      return {
        ...state,
        blocks: [...state.blocks, { kind: "user", uid: nextUid(), text: ev.text }],
      };
    case "delta": {
      // reasoning/text 增量写进最近的 assistant 块（没有则开一块）。
      // 不可突变旧块对象——每条 delta 都以新对象替换，保证引用变化。
      // 开新块 = 前一块已定格：中间轮的 usage 一并清（tokens 只在整轮
      // 的最终 assistant 显示——工具行上方的「已完成 · N tokens」是
      // 错位的中间轮统计，DSH 的 stats 在轮末）。
      const blocks = [...state.blocks];
      const last = blocks[blocks.length - 1];
      let target: AssistantBlock;
      if (last && last.kind === "assistant" && last.streaming) {
        target = { ...last };
        blocks[blocks.length - 1] = target;
      } else {
        target = { kind: "assistant", uid: nextUid(), content: "", reasoning: "", streaming: true };
        for (let i = 0; i < blocks.length; i++) {
          const b = blocks[i];
          if (b.kind === "assistant" && b.usageTokens !== undefined) {
            blocks[i] = { ...b, usageTokens: undefined };
          }
        }
        blocks.push(target);
      }
      if (ev.kind === "text") target.content += ev.text;
      else target.reasoning += ev.text;
      return { ...state, blocks };
    }
    case "toolCall": {
      // 工具调用打断流式中的 assistant 块——立刻定格（否则它永远挂着
      // "思考中" shimmer，成为僵尸块；中间插工具/清单后再开新块续写）
      const blocks = state.blocks.map((b) =>
        b.kind === "assistant" && b.streaming ? { ...b, streaming: false } : b,
      );
      return {
        ...state,
        blocks: [...blocks, { kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments }],
      };
    }
    case "toolResult": {
      const blocks = state.blocks.map((b) =>
        b.kind === "tool" && b.id === ev.id
          ? { ...b, result: ev.content, isError: ev.isError }
          : b,
      );
      return { ...state, blocks };
    }
    case "confirmRequest":
      return {
        ...state,
        pending: ev.request,
        blocks: [...state.blocks, { kind: "confirm", uid: nextUid(), request: ev.request }],
      };
    case "todoUpdated":
      return {
        ...state,
        todos: ev.items,
        blocks: [...state.blocks, { kind: "todo", uid: nextUid(), items: ev.items }],
      };
    case "done": {
      // 定格最后一个 assistant 块（按 uid 定位，不按 index——
      // 工具/清单块可能插在 assistant 之后）
      const lastA = [...state.blocks].reverse().find((b) => b.kind === "assistant") as AssistantBlock | undefined;
      const blocks = lastA
        ? state.blocks.map((b) => (b.kind === "assistant" && b.uid === lastA.uid ? { ...b, streaming: false, usageTokens: ev.usageTokens } : b))
        : state.blocks;
      return { ...state, blocks };
    }
    case "error": {
      // 中断保留已生成部分：把进行中的 assistant 块定格
      const blocks = state.blocks.map((b) =>
        b.kind === "assistant" && b.streaming ? { ...b, streaming: false } : b,
      );
      return {
        ...state,
        blocks: [...blocks, { kind: "error", uid: nextUid(), message: ev.message, aborted: ev.aborted }],
      };
    }
    case "busy":
      return { ...state, busy: ev.busy, pending: ev.busy ? state.pending : null };
    case "sessionChanged":
      // reason 语义：new（用户点新对话——清屏）/ resumed（切会话——清屏后
      // 等 historyLoaded 重放）/ started（懒建行——只刷新列表，对话进行中不清屏）
      if (ev.reason !== "new" && ev.reason !== "resumed") return { ...state };
      return { ...initial };
    case "sessionsChanged":
      // 列表变化不改 UI 状态本身——新对象触发重渲染（侧栏重读 sessions()）
      return { ...state };
    case "projectsChanged":
      return { ...state };
    case "filesChanged":
      // 一轮任务的产物汇总（验收视图——Codex 的 diff 中心形态）
      return { ...state, blocks: [...state.blocks, { kind: "files", uid: nextUid(), files: ev.files }] };
    case "historyLoaded":
      // 全量重建（连接/切会话后）：messages → blocks（工具调用与结果配对）
      return { ...reduceHistory(ev.history), busy: ev.history.busy };
    default:
      return state;
  }
}

/** 历史快照 → UI 状态：消息序列重建 blocks。
 *  配对规则：assistant 的 tool_calls 先开 tool 块；后续 role=tool 的消息
 *  按 tool_call_id 回填对应块的 result（服务端的存储顺序保证可达）。 */
function reduceHistory(h: HistorySnapshot): UIState {
  const blocks: ThreadBlock[] = [];
  let lastAssistant: AssistantBlock | null = null;
  for (const m of h.messages) {
    if (m.role === "user") {
      blocks.push({ kind: "user", uid: nextUid(), text: m.content });
      lastAssistant = null;
    } else if (m.role === "assistant") {
      const a: AssistantBlock = {
        kind: "assistant", uid: nextUid(), content: m.content,
        reasoning: m.reasoning_content ?? "", streaming: false,
      };
      blocks.push(a);
      lastAssistant = a;
      // assistant 携带的工具调用：紧跟工具块（保持原顺序）
      for (const tc of m.tool_calls ?? []) {
        blocks.push({
          kind: "tool", uid: nextUid(), id: tc.id ?? "",
          name: tc.function?.name ?? "", arguments: tc.function?.arguments ?? "",
        });
      }
    } else if (m.role === "tool") {
      // 工具结果回填（按 tool_call_id 找块；找不到则丢弃——防御坏数据）
      const target = blocks.find((b) => b.kind === "tool" && b.id === m.tool_call_id) as
        | Extract<ThreadBlock, { kind: "tool" }>
        | undefined;
      if (target && target.result === undefined) {
        target.result = m.content;
        target.isError = false;
      }
      // 工具结果后正文续写：新开 assistant 块（下一条 assistant 自然处理）
      lastAssistant = null;
    }
  }
  // 没有任何输出的进行中轮次不重现（streaming 重建成本高，历史里也少见）
  return {
    blocks,
    busy: false,
    pending: h.pending ?? null,
    todos: h.todos ?? [],
  };
}

/** useAgent：订阅 AgentSource 并归约成 UI 状态。
 * resolve：确认裁决后把对应卡片定格（allow/deny 徽标）——裁决是本地
 * UI 状态（后端事件流没有"卡片已裁决"事件，toolResult 才是回执）。
 * send/resolve 身份稳定：下游 React.memo(Block) 依赖 onConfirm 稳定。 */
export function useAgent(source: AgentSource): {
  state: UIState;
  send: AgentSource["send"];
  resolve: (id: string, outcome: "allow" | "deny") => void;
} {
  const [state, setState] = useState<UIState>(initial);
  useEffect(() => {
    setState(initial);
    return source.subscribe((ev) => setState((s) => reduce(s, ev)));
  }, [source]);
  const send = useCallback((t: string, opts?: SendOptions) => source.send(t, opts), [source]);
  const resolve = useCallback((id: string, outcome: "allow" | "deny") => {
    setState((s) => resolveConfirm(s, id, outcome));
  }, []);
  return { state, send, resolve };
}

/** 确认裁决后把对应卡片定格（allow/deny 徽标）。 */
function resolveConfirm(state: UIState, id: string, outcome: "allow" | "deny"): UIState {
  return {
    ...state,
    pending: null,
    blocks: state.blocks.map((b) =>
      b.kind === "confirm" && b.request.id === id ? { ...b, resolved: outcome } : b,
    ),
  };
}
