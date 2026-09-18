// 页面级更新提示（右下角 Toast）：自动检查发现新版本后弹出，不打断——
// 「立即更新」直接走下载流（Toast 内联进度→就绪→重启），「稍后」收起
// （设置里仍可继续）。与设置的 UpdateBlock 共用 UpdateProvider 状态。
import { humanBytes } from "../../shared/connections";
import { useUpdate } from "../../shared/update";
import { useEscape } from "../../shared/popover";
import { useEnterRef } from "../../shared/anim";
import { Button } from "../form";

export function UpdateToast() {
  const { phase, manifest, progress, toastDismissed, dismissToast, download, restart } = useUpdate();
  const toastEnter = useEnterRef<HTMLDivElement>();
  // Escape 关提示（不打断——同「稍后」语义）
  useEscape(phase === "available" && !toastDismissed, dismissToast);

  const visible = !toastDismissed && (phase === "available" || phase === "downloading" || phase === "ready");
  if (!visible || !manifest) return null;

  return (
    <div className="upd-toast" ref={toastEnter} role="status" aria-label="更新提示">
      <button type="button" className="upd-toast-close" onClick={dismissToast} aria-label="稍后" title="稍后（设置里可继续）">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12" />
        </svg>
      </button>
      {phase === "available" && (
        <>
          <div className="upd-toast-title">
            <span className="upd-toast-dot" />
            新版本 <span className="mono">{manifest.version}</span>
          </div>
          <div className="upd-toast-notes">{manifest.notes.slice(0, 2).join(" · ")}…</div>
          <div className="upd-toast-actions">
            <Button variant="primary" data-ut="download" onClick={download}>
              立即更新（{humanBytes(manifest.size)}）
            </Button>
            <button type="button" className="upd-toast-later" onClick={dismissToast}>稍后</button>
          </div>
        </>
      )}
      {phase === "downloading" && (
        <>
          <div className="upd-toast-title">下载中 <span className="mono">{progress}%</span></div>
          <div className="set-update-progress">
            <div className="set-update-progress-bar" style={{ width: `${progress}%` }} />
          </div>
        </>
      )}
      {phase === "ready" && (
        <>
          <div className="upd-toast-title ok">✓ 已就绪——重启后生效</div>
          <div className="upd-toast-actions">
            <Button variant="primary" data-ut="restart" onClick={restart}>立即重启</Button>
            <button type="button" className="upd-toast-later" onClick={dismissToast}>稍后重启</button>
          </div>
        </>
      )}
    </div>
  );
}
