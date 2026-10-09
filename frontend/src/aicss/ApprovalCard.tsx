// @aicss/react 0.1.3 (MIT) vendor 改造：保留 command 变体（lxcode
// 的确认门形态）与 questions 变体（ask_user 的提问形态——上游遗留的
// 样式位在这里启用），lucide 图标换内联 SVG（v3 稿同款），中文标签，
// 新增 resolved 态（裁决后卡片定格显示结果，不再可点）。
// autoFocus 走 focus({preventScroll:true})——键盘可达但不抢滚动，
// 贴底跟随由 Thread 的滚动状态机负责（原生 focus 滚动会打断上翻）。
// 裁决徽标回弹入场（back.out），定格瞬间有确认感。
import styles from "./ApprovalCard.module.css";
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../shared/motion";
import type { ConfirmRequest } from "../shared/types";

export interface ApprovalCardProps {
  /** 命令文本（高危 bash 的 command）。ask 提问时是提问正文（prompt）。 */
  command: string;
  /** 工作目录（bash 的 cwd）。 */
  cwd?: string;
  /** 确认提示的补充说明（如缩水守卫告警）。 */
  note?: string;
  /** 完整的确认请求（ask 变体读 kind/options/prompt——二元确认只用上面的字段）。 */
  request?: ConfirmRequest;
  /** 裁决回调：true=运行，false=跳过。resolved 后不再触发。 */
  onDecide?: (allow: boolean) => void;
  /** 提问的回答回调（ask 变体专用）：选项点选与自由文本都走它。 */
  onAnswer?: (text: string) => void;
  /** 裁决结果（历史回放时定格显示）。 */
  resolved?: "allow" | "deny" | null;
  /** ask 回答后的答案摘要（已决卡显示「已回答：<text>」）。 */
  resolvedAnswer?: string;
  /** 卡片出现时自动聚焦运行按钮（当前挂起的确认）。 */
  autoFocus?: boolean;
  /** 块锚点（右侧大纲按 data-uid 精确寻址）。 */
  "data-uid"?: number;
}

export function ApprovalCard({ command, cwd, note, request, onDecide, onAnswer, resolved, resolvedAnswer, autoFocus, "data-uid": dataUid }: ApprovalCardProps) {
  const isAsk = request?.kind === "ask";
  const [local, setLocal] = useState<"allow" | "deny" | null>(resolved ?? null);
  const [draft, setDraft] = useState("");
  const outcome = resolved ?? local;
  const decided = outcome !== null;
  const runRef = useRef<HTMLButtonElement>(null);
  const badgeRef = useRef<HTMLSpanElement>(null);

  // 键盘可达但不抢滚动（原生 autoFocus 会把上翻阅读的用户拽到底部）
  useEffect(() => {
    if (autoFocus && !decided) runRef.current?.focus({ preventScroll: true });
  }, [autoFocus, decided]);

  // 裁决徽标回弹入场
  useEffect(() => {
    if (!decided || !badgeRef.current || !motionAllowed()) return;
    gsap.fromTo(badgeRef.current, { opacity: 0, scale: 0.7 }, { opacity: 1, scale: 1, duration: 0.3, ease: "back.out(2)", clearProps: "transform,opacity" });
  }, [decided]);

  const decide = (allow: boolean) => {
    if (decided) return;
    setLocal(allow ? "allow" : "deny");
    onDecide?.(allow);
  };

  // 提问的回答：文本框非空才可提交（空回答等于没回答——后端 Answer 会拒收空串）
  const answer = () => {
    if (decided) return;
    const text = draft.trim();
    if (!text) return;
    setLocal("allow");
    onAnswer?.(text);
  };
  const answerWith = (text: string) => {
    if (decided) return;
    setLocal("allow");
    onAnswer?.(text);
  };

  // 二元确认（command 变体）的 JSX 一行未改——ask 变体是独立分支。
  if (isAsk) {
    const options = request?.options ?? [];
    return (
      <div className={styles.card} data-uid={dataUid} data-variant="questions" data-resolved={outcome ?? undefined}>
        <div className={styles.head}>
          <span className={styles.icon} data-variant="questions">
            <svg className={styles.iconSvg} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="10" />
              <path d="M9.1 9a3 3 0 0 1 5.8 1c0 2-3 3-3 3" />
              <path d="M12 17h.01" />
            </svg>
          </span>
          <div className={styles.headText}>
            <div className={styles.title}>{decided ? (outcome === "allow" ? "已回答你的提问" : "已跳过（未回答）") : "有一个问题需要你决定"}</div>
            <div className={styles.askTool}>来自 {request?.name}</div>
          </div>
          {decided && (
            <span className={styles.resolvedBadge} data-outcome={outcome} ref={badgeRef}>
              {outcome === "allow" ? (resolvedAnswer ? `已回答：${resolvedAnswer}` : "已回答") : "已跳过"}
            </span>
          )}
        </div>
        <div className={styles.cmdBlock} data-dim={decided ? "true" : undefined}>
          <pre className={styles.askText}>{command}</pre>
        </div>
        {!decided && (
          <div className={styles.askBody}>
            {options.length > 0 && (
              <div className={styles.askOptions}>
                {options.map((opt) => (
                  <button key={opt} type="button" className={styles.btnGhost} onClick={() => answerWith(opt)}>
                    {opt}
                  </button>
                ))}
              </div>
            )}
            <div className={styles.askInputRow}>
              <input
                type="text"
                className={styles.askInput}
                placeholder="输入你的回答…"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") answer(); }}
                aria-label="回答"
              />
              <button type="button" className={styles.btnPrimary} ref={runRef} onClick={answer} disabled={!draft.trim()}>
                回答
                <svg className={styles.btnSubmitIcon} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M20 6 9 17l-5-5" />
                </svg>
              </button>
              <button type="button" className={styles.btnGhost} onClick={() => decide(false)}>
                跳过
              </button>
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className={styles.card} data-uid={dataUid} data-variant="command" data-resolved={outcome ?? undefined}>
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
          <span className={styles.resolvedBadge} data-outcome={outcome} ref={badgeRef}>
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
              ref={runRef}
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
