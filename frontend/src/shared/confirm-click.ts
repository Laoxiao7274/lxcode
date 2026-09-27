// 两步删除确认的统一行为（useConfirmClick）：首次点击进入确认态
// （按钮变「确认」），再点执行 onConfirm，失焦/离开重置。
// 之前这段逻辑在 EntryCard / McServerCard / conn-item / tunnel 卡 /
// SearchSection 自定义渠道卡五处复制粘贴——全仓最大重复，抽此收敛。
import { useState } from "react";

/** 确认态的状态机事件：点击按钮、按钮失焦。 */
export type ConfirmEvent = "click" | "blur";

/** 一次状态转移的结果。 */
export interface ConfirmTransition {
  /** 转移后的确认态 */
  confirming: boolean;
  /** 是否应当执行 onConfirm */
  fire: boolean;
}

/**
 * 两步确认的状态机（纯函数——便于就地测试，不必渲染 React）。
 *
 * **确认态下点击必须同时复位**：不复位的话，条目没被删掉时（SearchSection
 * 的渠道卡「清除」后只是变成未配置态，组件并不卸载）按钮会一直停在
 * 「确认清除」，用户下一次点击就直接执行——二次确认形同虚设。
 * 卸载路径下复位是无害的（状态随组件一起消亡）。
 */
export function nextConfirmState(confirming: boolean, ev: ConfirmEvent): ConfirmTransition {
  if (ev === "blur") return { confirming: false, fire: false };
  if (confirming) return { confirming: false, fire: true };
  return { confirming: true, fire: false };
}

/** 返回 { confirming, onClick, onBlur }——喂给删除按钮。
 *  onReset 场景：条目卸载等外部重置（调用方持 key 换组件即可，无需
 *  额外动作——confirming 随组件卸载消亡）。 */
export function useConfirmClick(onConfirm: () => void) {
  const [confirming, setConfirming] = useState(false);
  return {
    confirming,
    onClick: () => {
      const next = nextConfirmState(confirming, "click");
      // 先复位再执行：onConfirm 之后组件可能仍活着（见 nextConfirmState 注释）
      setConfirming(next.confirming);
      if (next.fire) onConfirm();
    },
    onBlur: () => setConfirming(false),
  };
}
