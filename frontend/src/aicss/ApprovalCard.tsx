// @aicss/react 0.1.3 (MIT) vendor 改造：只保留 command 变体（myt-harness
// 的确认门形态），lucide 图标换内联 SVG（v3 稿同款），中文标签，
// 新增 resolved 态（裁决后卡片定格显示结果，不再可点）。
import styles from "./ApprovalCard.module.css";
import { useState } from "react";

export interface ApprovalCardProps {
  /** 命令文本（高危 bash 的 command）。 */
  command: string;
  /** 工作目录（bash 的 cwd）。 */
  cwd?: string;
  /** 确认提示的补充说明（如缩水守卫告警）。 */
  note?: string;
  /** 裁决回调：true=运行，false=跳过。resolved 后不再触发。 */
  onDecide?: (allow: boolean) => void;
  /** 裁决结果（历史回放时定格显示）。 */
  resolved?: "allow" | "deny" | null;
  /** 卡片出现时自动聚焦运行按钮（当前挂起的确认）。 */
  autoFocus?: boolean;
}

export function ApprovalCard({ command, cwd, note, onDecide, resolved, autoFocus }: ApprovalCardProps) {
  const [local, setLocal] = useState<"allow" | "deny" | null>(resolved ?? null);
  const outcome = resolved ?? local;
  const decided = outcome !== null;

  const decide = (allow: boolean) => {
    if (decided) return;
    setLocal(allow ? "allow" : "deny");
    onDecide?.(allow);
  };

  return (
    <div className={styles.card} data-variant="command" data-resolved={outcome ?? undefined}>
      <div className={styles.head}>
        <span className={styles.icon}>
          <svg className={styles.iconSvg} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="m4 17 6-6-6-6" />
            <path d="M12 19h8" />
          </svg>
        </span>
        <div className={styles.headText}>
          <div className={styles.title}>{decided ? (outcome === "allow" ? "已运行此命令" : "已跳过此命令") : "执行此命令？"}</div>
        </div>
        {decided && (
          <span className={styles.resolvedBadge} data-outcome={outcome}>
            {outcome === "allow" ? "已批准" : "已拒绝"}
          </span>
        )}
      </div>
      <div className={styles.cmdBlock} data-dim={decided ? "true" : undefined}>
        {cwd && <div className={styles.cwd}>{cwd}</div>}
        <pre className={styles.cmd}>{command}</pre>
        {note && <div className={styles.note}>{note}</div>}
      </div>
      {!decided && (
        <div className={styles.actions}>
          <div className={styles.actionsSpacer} aria-hidden />
          <div className={styles.actionBtns}>
            <button type="button" className={styles.btnGhost} onClick={() => decide(false)}>
              跳过
            </button>
            <button
              type="button"
              className={styles.btnPrimary}
              autoFocus={autoFocus}
              onClick={() => decide(true)}
            >
              运行
              <svg className={styles.btnSubmitIcon} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M20 6 9 17l-5-5" />
              </svg>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
