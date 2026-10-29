// 子会话时间线的「任务」块：主 Agent 派发的任务说明书（user 角色消息，但它不是
// 用户说的话——子会话页只读，用户没法在子会话里发言，这条的唯一来源是
// agent_dispatch 的派发；续跑的第二轮会再进来一条，两条来源同一样式）。
//
// 为什么不能当用户气泡：用户气泡的语义是「我说的」——任务说明书画进气泡里，用户
// 分不清这是 AI 传进去的任务还是自己发的（用户原话「子Agent第一条消息是以用户的
// 形式发出去的」）。视觉语言对齐 dispatch 卡（同一类"系统注入"的呈现：surface 底 +
// 边框 + 标签行，左对齐铺满），与用户气泡（右对齐、inset 底、22px 圆角）明确区分。
// 判定来源两条路径各有一份标记：实时 = 双投打的 task 标记（reduce.ts），回放 =
// 快照的 child 键（history.ts）——两处必须落到同一个块种类。
import type { ThreadBlock } from "../../../shared/store";
import { useEnterRef } from "../../../shared/anim";
import { Markdown } from "../../../shared/markdown";

export function TaskBlock({ block, "data-uid": dataUid }: { block: Extract<ThreadBlock, { kind: "task" }>; "data-uid"?: number }) {
  const ref = useEnterRef<HTMLDivElement>();
  return (
    <div className="task-block" data-uid={dataUid} ref={ref}>
      <div className="task-head">
        {/* 收件托盘图标：任务被"派进来"——与用户气泡（无图标行）一眼区分 */}
        <span className="task-icon" aria-hidden>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 3v11" />
            <path d="m7 9 5 5 5-5" />
            <path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
          </svg>
        </span>
        <span className="task-tag">任务</span>
        <span className="task-note">主 Agent 派发 · 不是你发的消息</span>
      </div>
      <div className="task-body">
        <Markdown text={block.text} />
      </div>
    </div>
  );
}
