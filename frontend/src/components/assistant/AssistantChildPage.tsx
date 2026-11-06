// 助手域子会话视图（助手模式 P2）：AssistantWorkspace 里派发卡点进的临时视图。
//
// **不复制 ChildSessionPage**——直接复用它（props 全透传，readOnly 语义同款），
// 只把域口钉死在助手域：历史装载 = assistant.childHistory、停止 = assistant.cancel
//（原 App.tsx 按 isAssistantChild 条件传的两处覆盖，在助手模式里恒真——这里
// 显式传，不靠调用方记）。返回按钮由 AssistantWorkspace 的子视图页头承担。
//
// **回调身份稳定（2026-12 闪烁修复）**：ChildSessionPage 的装载 effect 依赖
// historyLoader（loadHistory 的 useCallback 依赖里），onConfirm/onAnswer 进
// handleConfirm/handleAnswer 的 useCallback 依赖——给它们传内联箭头函数的话，
// 父级（AssistantWorkspace）每帧重渲染（流式 delta 每帧）都会换这三个 prop 的
// 身份 → 装载 effect 每帧重跑（assistant.childHistory 自激循环）+ Block 的
// key 全换 → 整条时间线反复卸载重挂（用户报的「点开子会话一直闪」）。所以：
// historyLoader/cancelSession 由本组件 useCallback 包住（依赖 [assistant]——
// assistant 是 source.assistant 的字段引用，AssistantWorkspace 侧 source 身份
// 稳定）；onConfirm/onAnswer 虽由调用方传入，但 AssistantWorkspace 侧已收敛为
// useCallback（handleChildConfirm/handleChildAnswer 的 *partial* 包装在
// AssistantWorkspace 里做成稳定引用——见该文件 childConfirmBridge），这里再包
// 一层 ref 兜底：调用方将来传不稳定回调也不回到自激循环。
import { useCallback, useRef } from "react";
import type { AgentSource } from "../../shared/types";
import type { UIState } from "../../shared/store";
import { ChildSessionPage } from "../panels/ChildSessionPage";

export function AssistantChildPage({
  source,
  sessionId,
  state,
  onConfirm,
  onAnswer,
}: {
  source: AgentSource;
  sessionId: string;
  state?: UIState;
  onConfirm: (id: string, allow: boolean) => void;
  onAnswer?: (id: string, text: string) => void;
}) {
  const assistant = source.assistant;
  // 调用方回调进 ref：读取口恒走 ref.current（最新身份、零依赖）——ref.current
  // 的赋值在渲染期同步完成，回调触发时读到的就是当帧的版本。
  const confirmRef = useRef(onConfirm);
  confirmRef.current = onConfirm;
  const answerRef = useRef(onAnswer);
  answerRef.current = onAnswer;

  // 稳定的装载/取消口（assistant 引用随 source 稳定）——ChildSessionPage 的
  // 装载 effect 只在 sessionId 变化时重跑。early return（assistant 缺席）在
  // 所有 hooks 之后：hooks 规则不允许条件早退在 hooks 之前。
  const historyLoader = useCallback(
    (id: string, beforeSeq?: number) => {
      if (!assistant) throw new Error("数据源不支持助手域（assistant.history）");
      return assistant.childHistory(id, beforeSeq);
    },
    [assistant],
  );
  const cancelSession = useCallback(
    (id: string) => assistant?.cancel(id),
    [assistant],
  );
  const handleConfirm = useCallback(
    (id: string, allow: boolean) => confirmRef.current(id, allow),
    [],
  );
  const handleAnswer = useCallback(
    (id: string, text: string) => answerRef.current?.(id, text),
    [],
  );

  if (!assistant) return null;
  return (
    <div className="assistant-child-page">
      <ChildSessionPage
        sessionId={sessionId}
        source={source}
        state={state}
        onConfirm={handleConfirm}
        onAnswer={handleAnswer}
        onBack={() => {}}
        historyLoader={historyLoader}
        cancelSession={cancelSession}
      />
    </div>
  );
}
