// RemoveProviderDialog —— 移除提供商（ProviderBlock 卡片右上角「移除」入口）：
//
// 影响面大：删除的是该 base_url 分组下**全部**模型条目（含 API Key，models.json
// 不可恢复；磁盘上没有提供商实体，移除 = 逐条 model.remove）。所以用**弹窗**
// 二次确认（与「移除项目」同级别），不是浏览器 confirm()（原生弹窗在 Electron
// 壳里样式割裂且不可测）。
//
// 后端逐条 model.remove 不原子：被 default/vision 角色绑定的条目会被拒绝
// （ErrRoleInUse），其余照删——部分失败逐条点名原样回显（对齐 addBatch /
// updateProvider 的既有文案模式），弹窗不关闭、已删的留删。会话级/Agent 绑定
// 的模型后端不拦，删掉后那些会话/Agent 下轮报「模型不存在」，弹窗里如实列出。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";

export function RemoveProviderDialog({
  providerName,
  modelCount,
  onRemove,
  onClose,
}: {
  /** 提供商展示名（host）——标题里点名。 */
  providerName: string;
  /** 将删除的模型条目数（该提供商分组下的全部模型）。 */
  modelCount: number;
  /** 执行移除（失败时 reject——对话框显示原因、不关闭）。 */
  onRemove: () => Promise<void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);
  // 同一时刻只放行一次提交：连点确认钮 / Enter 重放都不重复发请求
  const firedRef = useRef(false);

  useEscape(!busy, onClose);

  useEffect(() => {
    if (!dialogRef.current || !motionAllowed()) return;
    gsap.fromTo(dialogRef.current, { opacity: 0, y: 8 }, { opacity: 1, y: 0, duration: 0.2, ease: "power2.out", clearProps: "transform,opacity" });
  }, []);

  const submit = async () => {
    if (busy || firedRef.current) return;
    firedRef.current = true;
    setBusy(true);
    setError("");
    try {
      await onRemove();
      onClose();
    } catch (e) {
      // 失败原样显示（部分失败点名回显）——不静默关闭；允许重试
      firedRef.current = false;
      setBusy(false);
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div
      className="proj-add-mask"
      role="alertdialog"
      aria-label={`移除提供商 ${providerName}`}
      aria-modal="true"
      onPointerDown={(e) => e.target === e.currentTarget && !busy && onClose()}
    >
      <div className="proj-add" ref={dialogRef} data-remove-provider={providerName}>
        <div className="proj-add-head">
          <span className="proj-add-title">移除提供商「{providerName}」？</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onClose} disabled={busy}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          {/* 文案纪律：说清「会发生什么」（删配置不可恢复）+「之后会发生什么」
              （绑定失效要重选）——两个层面都要在，用户才知道真实代价 */}
          <ul className="proj-remove-facts">
            <li>将删除该提供商下 <b>{modelCount} 个模型</b> 的配置（含 API Key，<b>不可恢复</b>）</li>
            <li>被「默认模型 / 视觉」角色绑定的会被后端拒绝并提示先解绑；其余照删</li>
            <li>正在使用这些模型的会话与绑定了它们的 Agent 之后会报「模型不存在」，需重新选择</li>
          </ul>
          {error && <div className="proj-add-error" role="alert">{error}</div>}
        </div>
        <div className="proj-add-foot">
          <button type="button" className="proj-add-cancel" autoFocus onClick={onClose} disabled={busy}>取消</button>
          <button
            type="button"
            className="proj-add-ok danger"
            aria-busy={busy}
            disabled={busy}
            onClick={() => void submit()}
          >
            {busy ? "移除中…" : "移除"}
          </button>
        </div>
      </div>
    </div>
  );
}
