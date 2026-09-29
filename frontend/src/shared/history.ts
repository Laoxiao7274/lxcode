// 历史快照 → UI 状态：消息序列重建 blocks（回放路径与实时路径必须一致）。

import type { HistorySnapshot } from "./types";
import { type AssistantBlock, type ThreadBlock, type UIState, nextUid, checkpointBody, placeConfirm } from "./blocks";
import { noticeBody, noticeLabel } from "./notices";

/** 历史快照 → UI 状态：消息序列重建 blocks。
 *  配对规则：assistant 的 tool_calls 先开 tool 块；后续 role=tool 的消息
 *  按 tool_call_id 回填对应块的 result（服务端的存储顺序保证可达）。 */
export function reduceHistory(state: UIState, h: HistorySnapshot): UIState {
  const blocks: ThreadBlock[] = [];
  const checkpoints = new Set(h.checkpoints ?? []);
  let lastAssistant: AssistantBlock | null = null;
  for (let i = 0; i < h.messages.length; i++) {
    const m = h.messages[i];
    if (m.role === "user") {
      // 压缩检查点不是用户说的话：渲染成标记块（否则会变成一个巨大的用户气泡，
      // 把真正的用户消息淹没——这是回放路径与实时路径必须一致的地方）
      if (checkpoints.has(i)) {
        blocks.push({ kind: "compacted", uid: nextUid(), before: 0, after: 0, shadowed: 0, summary: checkpointBody(m.content), manual: false });
      } else {
        // 提示条（后台任务通告 / 重复调用提醒）是 **user 角色**消息（模型要当作用户
        // 回合才能回应），但它不是用户说的话——按**前缀表**识别并渲染成提示条
        //（回放路径与实时路径必须一致，否则刷新之后同一句话换了张脸）
        const label = noticeLabel(m.content);
        if (label) {
          blocks.push({ kind: "notice", uid: nextUid(), label, text: noticeBody(m.content) });
        } else {
          blocks.push({ kind: "user", uid: nextUid(), text: m.content });
        }
      }
      lastAssistant = null;
    } else if (m.role === "assistant") {
      const a: AssistantBlock = {
        kind: "assistant", uid: nextUid(), content: m.content,
        reasoning: m.reasoning_content ?? "", streaming: false,
      };
      blocks.push(a);
      lastAssistant = a;
      // assistant 携带的工具调用：紧跟工具块（保持原顺序）
      for (const tc of m.tool_calls ?? []) {
        blocks.push({
          kind: "tool", uid: nextUid(), id: tc.id ?? "",
          name: tc.function?.name ?? "", arguments: tc.function?.arguments ?? "",
        });
      }
    } else if (m.role === "tool") {
      // 工具结果回填（按 tool_call_id 找块；找不到则丢弃——防御坏数据）
      const target = blocks.find((b) => b.kind === "tool" && b.id === m.tool_call_id) as
        | Extract<ThreadBlock, { kind: "tool" }>
        | undefined;
      if (target && target.result === undefined) {
        target.result = m.content;
        target.isError = false;
      }
      // 工具结果后正文续写：新开 assistant 块（下一条 assistant 自然处理）
      lastAssistant = null;
    }
  }
  // 没有任何输出的进行中轮次不重现（streaming 重建成本高，历史里也少见）。
  // 挂起的确认必须重建：刷新/切会话后待裁决的确认卡不能只留在 state.pending
  //（没有任何组件渲染 pending——只有 blocks 里的 confirm 块才会画出来），
  // 否则会话卡在 busy=true 且用户无从批准（死锁）。
  // 用 placeConfirm 就地替换：后端在确认门挂起前已把 assistant 的 tool_calls
  // 落库，所以历史里必然有一行同 id 的工具行——若直接追加会重现"两行同 id"。
  const pending = h.pending ?? null;
  const withPending = pending ? placeConfirm(blocks, pending) : blocks;
  return {
    ...state,
    blocks: withPending,
    busy: false,
    pending,
    todos: h.todos ?? [],
    // 未知占用（后端刚重启/刚切会话）置 null —— 指示器显示中性态，
    // 不沿用上一会话的数字（那是别人的窗口占用）
    context: h.context ?? null,
    historyReady: true,
  };
}
