// 确认门/提问裁决流（审查 M5 前端重构：从 App.tsx 纯移动，语义零改动）。
//
// 四个回调共用一条不变式：裁决必须发给**持有挂起确认的那个会话**（主会话 =
// currentId；子会话视图里的裁决按确认的 dispatch_id 反查父会话——确认门由父
// 会话代理，AGENTS.md §2.3）。resolve 两处定格（request.id 主会话 + dispatch
// 归属）的机制原样保留。
import { useCallback } from "react";
import type { UIState } from "../shared/blocks";
import type { AgentSource } from "../shared/types";

/** useAgent 返回的会话状态表（Record<sessionId, UIState>——store.ts）。 */
type SessionStates = Record<string, UIState>;
type Resolve = (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;

/** 子会话视图里的裁决：按确认的 dispatch_id 反查持有它的父会话（卡在哪条
 *  会话里，那张卡就是父）。查不到时退回子会话 id——发出去顶多报一句
 *  「没有挂起的确认」，store 侧的两处定格照旧生效。 */
export function resolveConfirmOwner(
  sessionStates: SessionStates,
  sessionId: string,
): string {
  const did = sessionStates[sessionId]?.pending?.dispatch_id ?? "";
  if (!did) return sessionId;
  return Object.entries(sessionStates).find(([, st]) =>
    st.blocks.some((b) => b.kind === "dispatch" && b.id === did))?.[0]
    ?? sessionId;
}

/** 主会话时间线上的 confirm / answer + 子会话视图里的 childConfirm / childAnswer。 */
export function useConfirmFlow({ source, resolve, reportError, currentId, sessionStatesRef }: {
  source: AgentSource;
  resolve: Resolve;
  reportError: (message: string) => void;
  currentId: string;
  sessionStatesRef: { current: SessionStates };
}) {
  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void source.confirm(currentId, id, allow)
      .then(() => resolve(currentId, id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, currentId]);

  /** 主会话时间线上 ask_user 提问的回答：文本答案发给当前会话（与 confirm 同一条
   *  tool.confirm 通道——answer 非空时后端走 Answer 路径）。 */
  const handleAnswer = useCallback((id: string, text: string) => {
    void source.answer(currentId, id, text)
      .then(() => resolve(currentId, id, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, currentId]);

  /** 子会话视图里的裁决。**必须发到持有挂起确认的那个会话**：确认门由**父会话
   *  代理**（AGENTS.md §2.3——子会话自己持 pending 的话服务端的 tool.confirm
   *  找不到它，会话会卡在 busy），而 `tool.confirm` 是按 session_id 找会话再找
   *  它那条挂起确认的（见 server 的 MethodToolConfirm）。所以这里按确认的
   *  `dispatch_id` 反查**卡在哪条会话里**——那张卡就是父会话，挂起确认就在它手上。
   *
   *  查不到（老数据没记下 dispatch_id）时退回子会话 id：发出去顶多报一句
   *  "没有挂起的确认"，而 store 侧的两处定格照旧生效——不会出现"点了没反应"。 */
  const handleChildConfirm = useCallback((sessionId: string, id: string, allow: boolean) => {
    void source.confirm(resolveConfirmOwner(sessionStatesRef.current, sessionId), id, allow)
      .then(() => resolve(sessionId, id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, sessionStatesRef]);

  /** 子会话视图里 ask_user 提问的回答：与 handleChildConfirm 同一条 owner 反查
   *  规则（提问由父会话代理挂起，答案必须发给持有它的会话）——只换成 answer 方法
   *  与「已回答」定格。 */
  const handleChildAnswer = useCallback((sessionId: string, id: string, text: string) => {
    void source.answer(resolveConfirmOwner(sessionStatesRef.current, sessionId), id, text)
      .then(() => resolve(sessionId, id, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, sessionStatesRef]);

  return { handleConfirm, handleAnswer, handleChildConfirm, handleChildAnswer };
}
