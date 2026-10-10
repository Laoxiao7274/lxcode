// 会话运行态（busy）按项目聚合的 selector：侧栏项目行「运行中」角标的数据源。
// 纯函数（无 React）——tests/sidebar-busy.test.mjs 直接钉口径。
//
// 口径（体验修复批次 4）：**只算顶层会话**。子会话（dispatch / 合并子会话）的
// busy 事件被后端 childEmitter 拦下（internal/agent/dispatch.go：BusyEvent 不上抛
// ——子会话的忙闲不是主会话的），wire 上从来没有带子会话 id 的 chat.busy，
// 前端 sessionStates 里子会话的 busy 恒为 false——硬编「子会话也在跑」就是编数。
// 父会话整轮（含派发期间）都 busy，所以「项目下有没有会话在跑」由顶层口径
// 正确覆盖；只是「×N」的 N 不含子会话数。
//
// 输入的 sessions 应传**未归档**列表（侧栏展示口径）；workspace 为空串 = 未分组
// （Sidebar.LOOSE），未分组行的角标用同一份结果。

export function busyCountsByWorkspace(
  sessions: { id: string; workspace?: string }[],
  busyBySession: Record<string, boolean>,
): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const s of sessions) {
    if (!busyBySession[s.id]) continue;
    const ws = s.workspace ?? "";
    counts[ws] = (counts[ws] ?? 0) + 1;
  }
  return counts;
}
