// 两步删除确认的统一行为（useConfirmClick）：首次点击进入确认态
// （按钮变「确认」），再点执行 onConfirm，失焦/离开重置。
// 之前这段逻辑在 EntryCard / McServerCard / conn-item / tunnel 卡 /
// SearchSection 自定义渠道卡五处复制粘贴——全仓最大重复，抽此收敛。
import { useState } from "react";

/** 返回 { confirming, onClick, onBlur }——喂给删除按钮。
 *  onReset 场景：条目卸载等外部重置（调用方持 key 换组件即可，无需
 *  额外动作——confirming 随组件卸载消亡）。 */
export function useConfirmClick(onConfirm: () => void) {
  const [confirming, setConfirming] = useState(false);
  return {
    confirming,
    onClick: () => {
      if (confirming) onConfirm();
      else setConfirming(true);
    },
    onBlur: () => setConfirming(false),
  };
}
