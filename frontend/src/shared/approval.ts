// 权限档（会话级实时状态）的前端逻辑：协议值归一 + 后端广播 → 本端设置的反向同步。
//
// 为什么单独成模块而不是写进 React 组件：这两件事都是纯逻辑，而「事件到了、
// 设置没变」正是这条修复最怕的静默失败（用户看到的是「我说了别问，它还在问」）
// ——放进组件就只能靠人肉点一遍才验得到。
import type { AgentEvent, AgentSource, ApprovalMode } from "./types";

/** 协议值归一：空/未知 = confirm（协议约定：空 = 回落 confirm）。
 *
 *  后端 SetApproval 回的已经是规范化档位，这里再兜一层是因为收到没见过的值时
 *  写进设置会让选择器静默空掉——回落默认档至少是诚实的中性态。 */
export function normalizeApproval(value: unknown): ApprovalMode {
  return value === "auto" || value === "strict" ? value : "confirm";
}

/** 选「完全访问」是否需要弹窗确认：只有**升险**到 auto 拦（2026-10-09 用户拍板：
 *  二次点击改成弹窗确认）。降险方向（auto→confirm/strict、confirm↔strict）总是
 *  安全的，直接切——收权不需要向用户要许可。 */
export function needsApprovalConfirm(from: ApprovalMode, to: ApprovalMode): boolean {
  return to === "auto" && from !== "auto";
}

/** 选档入口的统一分流（PermPicker 与设置面板共用一份规则）：升险到 auto 先开
 *  确认弹窗（askConfirm），其余档位直接改（apply）。两个入口都走这里，规则才
 *  不会漂移成「一处弹窗、一处二次点击」。 */
export function approvalPick(
  from: ApprovalMode,
  to: ApprovalMode,
  apply: (mode: ApprovalMode) => void,
  askConfirm: () => void,
): void {
  if (needsApprovalConfirm(from, to)) askConfirm();
  else apply(to);
}

/** 后端广播的权限档变更 → 本端设置应同步到的档位；null = 与本端无关。
 *
 *  只认**当前会话**：多客户端同时开着时，别的会话的档位变更不能改写本端正在
 *  看的会话的设置（那是串台，不是同步）。载荷缺会话 id 时不猜——宁可不写。 */
export function approvalSyncTarget(ev: AgentEvent, currentSessionId: string): ApprovalMode | null {
  if (ev.type !== "approvalChanged") return null;
  if (!ev.sessionId || ev.sessionId !== currentSessionId) return null;
  return normalizeApproval(ev.approval);
}

/** 订阅后端广播的权限档变更并落到设置（返回退订——SettingsProvider 的 effect
 *  直接返回它）。当前会话从事件流里跟：sessionFocused 是「当前会话」的唯一事实
 *  源，另立一份状态迟早与它漂移。
 *
 *  **主会话之间隔离**（2026-10-07 用户要求）：档位是会话级状态，但本地设置只有
 *  一份——切到别的会话时显示端会沿用上一会话的本地值（串台）。chat.history 现在
 *  回带该会话此刻的档位，装载历史时按它对齐显示：每个主会话显示的都是它自己
 *  后端里的真实档位。快照没带 approval（老后端/演示源）时不写——不猜。 */
export function subscribeApprovalSync(source: AgentSource, apply: (mode: ApprovalMode) => void): () => void {
  let current = "";
  return source.subscribe((ev) => {
    if (ev.type === "sessionFocused") { current = ev.id; return; }
    if (ev.type === "historyLoaded") {
      if (ev.sessionId === current && ev.history.approval !== undefined) apply(ev.history.approval);
      return;
    }
    const mode = approvalSyncTarget(ev, current);
    if (mode) apply(mode);
  });
}
