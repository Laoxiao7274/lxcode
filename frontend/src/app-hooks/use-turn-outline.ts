// 右侧「轮次」大纲面板域（审查 M5 前端重构：从 App.tsx 纯移动，语义零改动）。
//
// 分组来自纯函数：一条 user 块开一轮，其后的派发都归入该轮（判定与摘要都在
// panels/turns.ts——提示条 notice 在历史里也是 user 角色消息，但它不开新轮）。
import { useCallback, useMemo, useState } from "react";
import { turnGroups } from "../components/panels/turns";
import type { ThreadBlock } from "../shared/store";

export function useTurnOutline({ blocks }: { blocks: ThreadBlock[] }) {
  const groups = useMemo(() => turnGroups(blocks), [blocks]);
  /** 面板头的轮次数（如「共 7 轮 · 12 次派发」）。 */
  const dispatchCount = groups.reduce((n, group) => n + group.agents.length, 0);
  /** 最近跳转的那个块（高亮跟随点击）。它可能已经不在列表里（撤回/切会话），
   *  所以取用时再确认一次存在性。 */
  const [jumpedUid, setJumpedUid] = useState<number | null>(null);
  /** 默认高亮最后一轮的组头（最近发的那条用户消息）——沿用原大纲的算法。
   *
   *  存在性判据必须**同时**覆盖组头与组内的子 Agent：点组内某个子 Agent 时 jumpedUid
   *  是那条派发卡的 uid，只在组头里找会把它判成「不存在」而回落到最后一轮——点谁谁不亮。
   *  最后一条 user 之前的「会话开始」组没有锚点（uid: null），跳过它继续往前找。 */
  const lastTurnUid = groups.reduce<number | null>((acc, group) => (group.uid !== null ? group.uid : acc), null);
  const activeUid =
    jumpedUid !== null && groups.some((group) => group.uid === jumpedUid || group.agents.some((agent) => agent.uid === jumpedUid))
      ? jumpedUid
      : lastTurnUid;
  /** 跳到某条已发送消息：这里**只记目标**，滚动与高亮交给 Thread。
   *
   *  为什么不在 App 里滚：线程是**窗口化渲染**（只挂底部若干块），目标可能根本没
   *  挂上 DOM——在这里按 data-uid 找不到元素就静默变成 no-op，而长会话恰恰是最需要
   *  跳转的场景（用户点大纲第一条却毫无反应）。扩窗要改 Thread 自己的 windowSize，
   *  所以整跳归它：先撑窗到包含目标，等元素真的挂上再滚 + 高亮。
   *  寻址仍按 data-uid **精确匹配**——不许按文本找元素（同一句话发两次会命中错的那条）。 */
  const handleOutlineJump = useCallback((uid: number) => {
    setJumpedUid(uid);
  }, []);

  return { groups, dispatchCount, jumpedUid, activeUid, handleOutlineJump };
}
