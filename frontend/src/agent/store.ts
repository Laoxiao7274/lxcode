// 事件流 → UI 状态的归约。Thread 的渲染单元是"块"（block）：
// 一条 user 消息、一条 assistant 回复（含正文/思考链/流式态）、一次
// 工具调用（含结果）、一张确认卡、一份任务清单、一条错误。
import { useEffect, useState } from "react";
import type { AgentEvent, AgentSource, ConfirmRequest, SessionMeta, TodoItem } from "./types";

export interface AssistantBlock {
  kind: "assistant";
  content: string;
  reasoning: string;
  streaming: boolean;
  usageTokens?: number;
}

export type ThreadBlock =
  | { kind: "user"; text: string }
  | AssistantBlock
  | { kind: "tool"; id: string; name: string; arguments: string; result?: string; isError?: boolean }
  | { kind: "confirm"; request: ConfirmRequest; resolved?: "allow" | "deny" }
  | { kind: "todo"; items: TodoItem[] }
  | { kind: "error"; message: string; aborted: boolean };

export interface UIState {
  blocks: ThreadBlock[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
  sessionIds: string[];
}

const initial: UIState = { blocks: [], busy: false, pending: null, todos: [], sessionIds: [] };

function reduce(state: UIState, ev: AgentEvent): UIState {
  switch (ev.type) {
    case "userMessage":
      return {
        ...state,
        blocks: [...state.blocks, { kind: "user", text: ev.text }],
      };
    case "delta": {
      // reasoning/text 增量写进最近的 assistant 块（没有则开一块）
      const blocks = [...state.blocks];
      let last = blocks[blocks.length - 1];
      if (!last || last.kind !== "assistant" || !last.streaming) {
        last = { kind: "assistant", content: "", reasoning: "", streaming: true };
        blocks.push(last);
      }
      if (ev.kind === "text") last.content += ev.text;
      else last.reasoning += ev.text;
      return { ...state, blocks };
    }
    case "toolCall":
      return {
        ...state,
        blocks: [...state.blocks, { kind: "tool", id: ev.id, name: ev.name, arguments: ev.arguments }],
      };
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
        blocks: [...state.blocks, { kind: "confirm", request: ev.request }],
      };
    case "todoUpdated":
      return {
        ...state,
        todos: ev.items,
        blocks: [...state.blocks, { kind: "todo", items: ev.items }],
      };
    case "done": {
      const blocks = state.blocks.map((b, i) =>
        b.kind === "assistant" && i === state.blocks.length - 1
          ? { ...b, streaming: false, usageTokens: ev.usageTokens }
          : b,
      );
      return { ...state, blocks };
    }
    case "error": {
      // 中断保留已生成部分：把进行中的 assistant 块定格
      const blocks = state.blocks.map((b) =>
        b.kind === "assistant" && b.streaming ? { ...b, streaming: false } : b,
      );
      return {
        ...state,
        blocks: [...blocks, { kind: "error", message: ev.message, aborted: ev.aborted }],
      };
    }
    case "busy":
      return { ...state, busy: ev.busy, pending: ev.busy ? state.pending : null };
    case "sessionChanged":
      // 演示模式切会话：清空重排（真实模式由 chat.history 重放）
      return { ...initial, sessionIds: [...state.sessionIds, ev.id] };
    default:
      return state;
  }
}

/** useAgent：订阅 AgentSource 并归约成 UI 状态。
 * resolve：确认裁决后把对应卡片定格（allow/deny 徽标）——裁决是本地
 * UI 状态（后端事件流没有"卡片已裁决"事件，toolResult 才是回执）。 */
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
  const resolve = (id: string, outcome: "allow" | "deny") => {
    setState((s) => resolveConfirm(s, id, outcome));
  };
  return { state, send: (t: string) => source.send(t), resolve };
}

/** 确认裁决后把对应卡片定格（allow/deny 徽标）。 */
export function resolveConfirm(state: UIState, id: string, outcome: "allow" | "deny"): UIState {
  return {
    ...state,
    pending: null,
    blocks: state.blocks.map((b) =>
      b.kind === "confirm" && b.request.id === id ? { ...b, resolved: outcome } : b,
    ),
  };
}

export type { AgentSource, SessionMeta };
