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
import { reduceSessionStates, allowAllPendingConfirms, resolveConfirm, resolveConfirmEverywhere } from "./reduce";

export type { AssistantBlock, ThreadBlock, UIState } from "./blocks";
export { checkpointBody } from "./blocks";
export { reduce, reduceSessionStates, pendingConfirmIds, allowAllPendingConfirms, resolveConfirm, resolveConfirmEverywhere } from "./reduce";
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
      if (ev.type === "approvalChanged" && ev.approval === "auto") {
        // 切「完全访问」：后端已沿确认通道放行本会话的挂起确认（含子会话的——
        // 确认门代理走父通道，父切档子的一起松）。UI 的待裁决卡片同步定格为
        // 「已允许」，不等 toolResult 回执——否则用户看着一张永远「待确认」的卡
        // 而工具其实已经在跑（显示与事实脱节比慢半拍更糟）。只动这个会话的
        // state：别的会话的挂起确认是别人的（主会话之间隔离）。
        setSessionStates((all) => allowAllPendingConfirms(all, ev.sessionId));
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
    // 裁决要**两处同时定格**：确认在父会话的卡里与子会话自己的时间线里各有一份
    //（子事件双投的必然结果）——只定格用户点的那一处，另一处会永远挂着「待确认」。
    // 先补上被裁决的那个会话（它可能还没有 state——确认卡在 blocks 里但 state 未建）。
    setSessionStates((all) => {
      const base = all[sessionId] ? all : { ...all, [sessionId]: { ...initial, currentId: sessionId } };
      return resolveConfirmEverywhere(base, id, outcome);
    });
  }, []);
  const clearError = useCallback(() => setOperationError(null), []);
  const reportError = useCallback((message: string) => setOperationError(message), []);
  return { state, sessionStates, send, resolve, clearError, reportError };
}
