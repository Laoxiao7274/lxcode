// 「完全访问」开启确认弹窗：升险到 auto 档前的最后一道门（2026-10-09 用户
// 拍板，取代原来的「再次点击」两步确认）。
//
// 结构复用 SessionRow 归档/释放弹层同款（proj-add-mask + role=alertdialog +
// aria-modal）：Esc 关闭、点遮罩关闭、点内容不冒泡。输入区 PermPicker 与设置
// 面板「高危操作」两个入口共用这一个组件——文案与交互不许漂移。
//
// 防重复提交：onConfirm 触发即定格（firedRef），弹窗卸载前点再多下也只发一次。
import { useRef } from "react";

export function ApprovalConfirmDialog({ onConfirm, onCancel }: { onConfirm: () => void; onCancel: () => void }) {
  const firedRef = useRef(false);
  const confirm = () => {
    if (firedRef.current) return;
    firedRef.current = true;
    onConfirm();
  };
  return (
    <div
      className="proj-add-mask"
      role="alertdialog"
      aria-modal="true"
      aria-label="开启完全访问"
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
          <span className="proj-add-title">开启完全访问？</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onCancel}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="m18 6-12 12M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          <p className="proj-field-hint">低危与高危工具（含 bash、写文件）都会自动执行，不再逐条请求确认。</p>
          <p className="proj-field-hint">子代理与合并进程也会按自动档运行；向你的提问（ask）仍会等待回答。</p>
        </div>
        <div className="proj-add-foot">
          <button type="button" className="proj-add-cancel" autoFocus onClick={onCancel}>取消</button>
          <button type="button" className="proj-add-ok" onClick={confirm}>开启完全访问</button>
        </div>
      </div>
    </div>
  );
}
