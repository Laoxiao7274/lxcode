// 助手域子会话的确认裁决桥（2026-12 方案 A：从 AssistantWorkspace 抽出共用）。
//
// 为什么是公共 hook：助手 run_task 子会话的临时视图现在有**两个**渲染点——
// 助手模式内的 AssistantWorkspace（childViewId）与项目模式的 astchild 视图
//（App 的 renderView astchild 分支）。裁决必须走**助手域**口（assistant.confirm
// + store 的 resolveAssistant 两处定格）：让项目侧的裁决走 use-confirm-flow 的
// 主域口（chat.confirm / resolve）是串域实害——assistant 域会话在主域注册表
// 里不存在，tool.confirm 找不到会话，用户点批准永远没反应。数据源恒是 store
// 双投的 assistantStates（App 持有并传给两个消费方）。
//
// 回调身份纪律（2026-12 闪烁修复）：子会话视图的 *partial* 包装必须 useCallback
//——内联箭头每帧换身份 → ChildSessionPage 的 handleConfirm/handleAnswer
//（useCallback 依赖 onConfirm/onAnswer）跟着换 → 时间线上的确认卡 Block 逐帧
// 失忆重挂（「点开子会话一直闪」的自激 bug）。
import { useCallback, useRef } from "react";
import type { UIState } from "../shared/blocks";
import type { AgentSource } from "../shared/types";

/** useAgent 返回的助手域状态表（Record<sessionId, UIState>——store.ts）。 */
type AssistantStates = Record<string, UIState>;
type ResolveAssistant = (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;

/** 子会话视图里的裁决：按确认的 dispatch_id 反查**助手域里**持有它的父会话
 *  （卡在哪条助手会话里，那张卡就是父）。查不到时退回子会话 id——发出去顶多
 *  报一句「没有挂起的确认」，store 侧的两处定格照旧生效。 */
export function resolveAssistantChildOwner(assistantStates: AssistantStates, childId: string): string {
  const did = assistantStates[childId]?.pending?.dispatch_id ?? "";
  if (!did) return childId;
  return Object.entries(assistantStates).find(([, st]) =>
    st.blocks.some((b) => b.kind === "dispatch" && b.id === did))?.[0]
    ?? childId;
}

export function useAssistantConfirm({ assistant, assistantStates, resolveAssistant, reportError }: {
  /** 助手域能力口（source.assistant）——缺席时裁决回调静默 no-op（与
   *  AssistantWorkspace 旧实现的可选链语义一致：演示源下子会话视图本就进不来）。 */
  assistant: AgentSource["assistant"];
  /** 助手域 states（store 双投——App/AssistantWorkspace 传入同一份）。
   *  进 ref：回调身份稳定（Block memo 纪律），读取走 ref.current 取最新值。 */
  assistantStates: AssistantStates;
  /** 助手域的两处定格（store.resolveAssistant）。 */
  resolveAssistant: ResolveAssistant;
  reportError: (message: string) => void;
}) {
  const assistantStatesRef = useRef(assistantStates);
  assistantStatesRef.current = assistantStates;

  const ownerOf = useCallback((childId: string): string => {
    return resolveAssistantChildOwner(assistantStatesRef.current, childId);
  }, []);

  const handleChildConfirm = useCallback((childId: string, confirmId: string, allow: boolean) => {
    void assistant?.confirm(ownerOf(childId), confirmId, allow)
      .then(() => resolveAssistant(childId, confirmId, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [assistant, ownerOf, resolveAssistant, reportError]);

  const handleChildAnswer = useCallback((childId: string, confirmId: string, text: string) => {
    void assistant?.confirm(ownerOf(childId), confirmId, true, text)
      .then(() => resolveAssistant(childId, confirmId, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [assistant, ownerOf, resolveAssistant, reportError]);

  // 子会话视图的 *partial* 包装（AssistantChildPage 要的是 (id, allow) 签名）。
  // childId 变化时重生成（依赖它是语义需要）。
  const childConfirmOf = useCallback(
    (childId: string) => (id: string, allow: boolean) => handleChildConfirm(childId, id, allow),
    [handleChildConfirm],
  );
  const childAnswerOf = useCallback(
    (childId: string) => (id: string, text: string) => handleChildAnswer(childId, id, text),
    [handleChildAnswer],
  );

  return { ownerOf, handleChildConfirm, handleChildAnswer, childConfirmOf, childAnswerOf };
}
