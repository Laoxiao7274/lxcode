// RemoveProjectDialog —— 移除项目（侧栏项目行 hover 出现的「移除」入口）：
//
// 语义必须一眼看懂（用户拍板）：**只从 lxcode 的项目列表移除注册**——
// 不删磁盘目录、不删会话。项目下的会话保留在库里（历史/统计/子会话一条不丢），
// 只是侧栏不再有该项目分组。所以文案写的是「移除」而不是「删除」，并显式列出
// 两件不会发生的事。
//
// 二次确认照本仓库既有形态（AgentCard / 卡片删除）：点一次进确认态、再点才执行，
// 不是浏览器 confirm()（原生弹窗在 Electron 壳里样式割裂且不可测）。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import type { ProjectMeta } from "../../shared/types";

export function RemoveProjectDialog({
  project,
  sessionCount,
  onRemove,
  onClose,
}: {
  project: ProjectMeta;
  /** 该项目下的会话数（含已归档）——文案里说清「这些会话会保留」。 */
  sessionCount: number;
  /** 执行移除（失败时 reject——对话框显示原因、不关闭）。 */
  onRemove: () => Promise<void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);

  useEscape(!busy, onClose);

  useEffect(() => {
    if (!dialogRef.current || !motionAllowed()) return;
    gsap.fromTo(dialogRef.current, { opacity: 0, y: 8 }, { opacity: 1, y: 0, duration: 0.2, ease: "power2.out", clearProps: "transform,opacity" });
  }, []);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await onRemove();
      onClose();
    } catch (e) {
      // 失败原样显示（如「该项目还有 N 个会话正在运行」）——不静默关闭
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <div
      className="proj-add-mask"
      role="dialog"
      aria-label="移除项目"
      aria-modal="true"
      onPointerDown={(e) => e.target === e.currentTarget && !busy && onClose()}
    >
      <div className="proj-add" ref={dialogRef} data-remove-project={project.id}>
        <div className="proj-add-head">
          <span className="proj-add-title">移除项目「{project.name}」</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onClose} disabled={busy}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          <div className="proj-remove-path">{project.path}</div>
          {/* 文案纪律：说清「会发生什么」+「不会发生什么」——两段都要在，
              用户才不会以为磁盘上的代码被删了 */}
          <ul className="proj-remove-facts">
            <li>只从 lxcode 的项目列表移除，<b>不会删除磁盘上的任何文件</b></li>
            <li>
              {sessionCount > 0
                ? `该项目的 ${sessionCount} 条会话保留在库里（历史一条不丢），会归入「未分组」，侧栏不再显示该项目分组`
                : "该项目还没有会话"}
            </li>
            <li>需要时可以重新「添加项目」把这个目录接回来</li>
          </ul>
          {error && <div className="proj-add-error" role="alert">{error}</div>}
        </div>
        <div className="proj-add-foot">
          <button type="button" className="proj-add-cancel" onClick={onClose} disabled={busy}>取消</button>
          <button
            type="button"
            className="proj-add-ok danger"
            aria-busy={busy}
            disabled={busy}
            onClick={() => void submit()}
          >
            {busy ? "移除中…" : "移除项目"}
          </button>
        </div>
      </div>
    </div>
  );
}
