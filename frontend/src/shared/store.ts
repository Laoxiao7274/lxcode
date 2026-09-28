// useAgent：订阅 AgentSource 并归约成 UI 状态（会话 id 与操作错误也在这里——
// 单一事实源，消灭 App 的并行订阅）。归约逻辑已按职责拆到 blocks.ts /
// history.ts / reduce.ts；这里只留 React 绑定，并把公共名字重新导出，调用方
// （组件与测试）的 import 路径不变。
//
// resolve：确认裁决后把对应卡片定格（allow/deny 徽标）——裁决是本地 UI 状态
//（后端事件流没有「卡片已裁决」事件，toolResult 才是回执）。
// send/resolve 身份稳定：下游 React.memo(Block) 依赖 onConfirm 稳定。
import { useCallback, useEffect, useState } from "react";
import type { AgentSource, SendOptions } from "./types";
import { type UIState, initial } from "./blocks";
import { reduceSessionStates, resolveConfirm } from "./reduce";

export type { AssistantBlock, ThreadBlock, UIState } from "./blocks";
export { checkpointBody } from "./blocks";
export { reduce, reduceSessionStates, resolveConfirm } from "./reduce";
export function useAgent(source: AgentSource): {
  state: UIState;
  sessionStates: Record<string, UIState>;
  send: AgentSource["send"];
  resolve: (sessionId: string, id: string, outcome: "allow" | "deny") => void;
  clearError: () => void;
  reportError: (message: string) => void;
} {
  const [sessionStates, setSessionStates] = useState<Record<string, UIState>>({});
  const [currentId, setCurrentId] = useState("");
  const [operationError, setOperationError] = useState<string | null>(null);
  const [, setRevision] = useState(0);

  useEffect(() => {
    setSessionStates({});
    setCurrentId("");
    setOperationError(null);
    return source.subscribe((ev) => {
      if (ev.type === "sessionFocused") {
        setCurrentId(ev.id);
        setSessionStates((all) => all[ev.id] ? all : { ...all, [ev.id]: { ...initial, currentId: ev.id } });
        return;
      }
      if (ev.type === "operationError") {
        setOperationError(ev.message);
        return;
      }
      if (ev.type === "sessionsChanged" || ev.type === "projectsChanged" || ev.type === "sessionChanged" || ev.type === "ready") {
        setRevision((n) => n + 1);
        return;
      }
      if ("sessionId" in ev) setSessionStates((all) => reduceSessionStates(all, ev));
    });
  }, [source]);

  const state = {
    ...(sessionStates[currentId] ?? initial),
    currentId,
    operationError,
  };
  const send = useCallback((sessionId: string, text: string, opts?: SendOptions) => source.send(sessionId, text, opts), [source]);
  const resolve = useCallback((sessionId: string, id: string, outcome: "allow" | "deny") => {
    setSessionStates((all) => {
      const current = all[sessionId] ?? { ...initial, currentId: sessionId };
      return { ...all, [sessionId]: resolveConfirm(current, id, outcome) };
    });
  }, []);
  const clearError = useCallback(() => setOperationError(null), []);
  const reportError = useCallback((message: string) => setOperationError(message), []);
  return { state, sessionStates, send, resolve, clearError, reportError };
}
