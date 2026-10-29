// 「回到这里 + 还原文件」的二次确认弹窗 + 冲突清单弹窗。
//
// 结构复用 ApprovalConfirmDialog / SessionRow 的归档弹层（proj-add-mask +
// role=alertdialog + aria-modal）：Esc 关闭、点遮罩关闭、点内容不冒泡。文案来自
// ./rewind-confirm 的纯函数（三段骨架固定，只填数字）——**组件不许自己拼文案**，
// 否则两处口径迟早分叉（这个仓库为同一类漂移吃过多次亏）。
import { useRef } from "react";
import type { FileConflict } from "../../shared/types";
import { conflictHeadline, conflictLines, rewindConfirmLines, type RewindPlanFacts } from "./rewind-confirm";

/** 二次确认：把对话回到某处并还原文件。 */
export function RewindConfirmDialog({ facts, onConfirm, onCancel }: {
  facts: RewindPlanFacts;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  // 防重复提交：onConfirm 触发即定格（弹窗卸载前点再多下也只发一次）。
  const firedRef = useRef(false);
  const confirm = () => {
    if (firedRef.current) return;
    firedRef.current = true;
    onConfirm();
  };
  const lines = rewindConfirmLines(facts);
  return (
    <div
      className="proj-add-mask"
      role="alertdialog"
      aria-modal="true"
      aria-label="回到这里并还原文件"
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape") onCancel();
      }}
      onPointerDown={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onCancel();
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="proj-add session-release">
        <div className="proj-add-head">
          <span className="proj-add-title">回到这里，并还原文件？</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onCancel}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="m18 6-12 12M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          {lines.map((line, i) => (
            // 第 2 段是「无法还原」的警告——用危险色标出来（三段里只有它会让人后悔）
            <p key={i} className={i === 1 && facts.external > 0 ? "proj-field-hint stree-warn-line" : "proj-field-hint"}>
              {line}
            </p>
          ))}
        </div>
        <div className="proj-add-foot">
          <button type="button" className="proj-add-cancel" autoFocus onClick={onCancel}>取消</button>
          <button type="button" className="proj-add-ok" onClick={confirm}>继续</button>
        </div>
      </div>
    </div>
  );
}

/** 冲突清单（fail-closed：整批未执行，对话与文件都没动）。
 *
 *  为什么单独一个弹窗而不是一行 toast：冲突要**逐文件**给出路径与原因（用户得自己
 *  判断哪个文件被谁改过），一行提示装不下；而且用户需要停在这里读完再决定
 *  「只切历史（不带文件）」还是先手动处理文件。 */
export function RewindConflictDialog({ conflicts, onClose, onHistoryOnly }: {
  conflicts: FileConflict[];
  onClose: () => void;
  /** 「只切历史，不动文件」（files:false 重试）——失败时的退路，不是默认动作。 */
  onHistoryOnly?: () => void;
}) {
  const lines = conflictLines(conflicts);
  return (
    <div
      className="proj-add-mask"
      role="alertdialog"
      aria-modal="true"
      aria-label="文件冲突"
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape") onClose();
      }}
      onPointerDown={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onClose();
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="proj-add session-release stree-conflict-box">
        <div className="proj-add-head">
          <span className="proj-add-title">{conflictHeadline(conflicts)}</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onClose}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="m18 6-12 12M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          <p className="proj-field-hint">
            这些文件在快照之后被改过（编辑器 / bash / 构建）。**不会覆盖你的改动**——整批还原已取消。
          </p>
          <ul className="stree-conflict-list">
            {lines.map((c) => (
              <li key={c.path} className="stree-conflict-item">
                <code className="stree-conflict-path">{c.path}</code>
                <span className="stree-conflict-reason">
                  {c.reason}
                  {c.session ? ` · 由 ${c.session} 改动` : ""}
                </span>
              </li>
            ))}
          </ul>
        </div>
        <div className="proj-add-foot">
          {onHistoryOnly && (
            <button type="button" className="proj-add-cancel" onClick={onHistoryOnly}>
              只切历史，不动文件
            </button>
          )}
          <button type="button" className="proj-add-ok" autoFocus onClick={onClose}>知道了</button>
        </div>
      </div>
    </div>
  );
}
