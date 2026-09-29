// 系统提示条的渲染形态（后台任务通告 / 重复调用提醒——种类表见 shared/notices.ts）。
//
// 为什么不能当用户气泡：通告在历史与实时流里都是**真实 user 角色消息**
// （模型必须把它当用户回合才能回应——这是后端的硬约束），但它不是用户说的话。
// 按角色渲染的后果是用户看到一句自己没说过的话挂在自己的气泡里（用户会以为
// 是自己发的，甚至以为会话被串了）。识别只认**文本前缀**，标签由 block.label 带来
//（判定与文案都在 notices.ts 的表里，这里不再硬编码），正文是 agent 该读的那句话。
import type { ThreadBlock } from "../../../shared/store";
import { useEnterRef } from "../../../shared/anim";

export function NoticeBar({ block }: { block: Extract<ThreadBlock, { kind: "notice" }> }) {
  const barRef = useEnterRef<HTMLDivElement>();
  return (
    <div className="job-notice" ref={barRef} role="note">
      <span className="job-notice-icon" aria-hidden>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 8v5" />
          <path d="M12 16.5h.01" />
          <circle cx="12" cy="12" r="9" />
        </svg>
      </span>
      <span className="job-notice-label">{block.label}</span>
      <span className="job-notice-text">{block.text}</span>
    </div>
  );
}
