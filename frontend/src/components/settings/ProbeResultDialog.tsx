// 端点探测结果弹窗：「获取模型列表」拿到的清单独立成弹窗，不再内嵌在提供商卡里。
//
// 契约沿用原内嵌面板的纪律，并按用户拍板补一条：
//  1. 候选**绝不自动添加**——一律用户勾选（端点可能报 200 个模型，自动写入会淹掉选择器）；
//  2. 默认全不选（可预期胜过聪明——预勾选在 3 个模型时方便，在 200 个时是灾难）；
//  3. **已添加过的模型仍然显示**——整行置灰 + 开关禁用 + 「已添加」标签，可见、可核对、
//     不可再勾（藏掉会让用户以为端点没报它）；全选/计数只作用于未添加的。
//
// 弹窗骨架与 ProviderEditDialog / ConnectProviderDialog 同款（mset-connect-mask），
// 候选行复用 ConnectProviderDialog 的 CandidateRows——两处勾选行逐字同款。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { useSettings, type ProviderMeta } from "../../shared/settings";
import { catalogTags } from "../../shared/settings-models";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { kfmtLimit } from "../../shared/format";
import type { DiscoveredModel } from "../../shared/types";
import { CandidateRows } from "./ConnectProviderDialog";

/** 探测结果条目：added = 该 id 已在本提供商的注册表里（ProviderBlock 打标）。 */
export type ProbeModel = DiscoveredModel & { added?: boolean };

export function ProbeResultDialog({
  provider,
  endpoint,
  models,
  onClose,
}: {
  provider: ProviderMeta;
  endpoint: string;
  models: ProbeModel[];
  onClose: () => void;
}) {
  const { addDiscovered } = useSettings();
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

  const selectable = models.filter((m) => !m.added);
  const addedCount = models.length - selectable.length;

  useEscape(true, onClose);

  // 候选行交错浮现（与连接对话框的清单同款入场）
  useEffect(() => {
    const el = listRef.current;
    if (!el || !motionAllowed()) return;
    const rows = el.querySelectorAll(".mset-model-row");
    if (rows.length) {
      gsap.fromTo(rows, { opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.22, stagger: 0.03, delay: 0.06, ease: "power2.out", clearProps: "transform,opacity" });
    }
  }, []);

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const toggleAll = () =>
    setPicked((prev) => (prev.size === selectable.length ? new Set() : new Set(selectable.map((c) => c.id))));

  const commit = async () => {
    const chosen = selectable.filter((c) => picked.has(c.id));
    if (chosen.length === 0) return;
    setBusy(true);
    const ok = await addDiscovered(provider.id, chosen);
    setBusy(false);
    // 失败留在弹窗里：错误由设置面板顶部 error 块呈现，关掉弹窗用户就看不到上下文了
    if (ok) onClose();
  };

  return (
    <div className="mset-connect-mask" role="dialog" aria-label="获取到的模型" aria-modal="true">
      <div className="mset-connect wide">
        <div className="mset-connect-head">
          <span className="mset-connect-back-spacer" />
          <span className="mset-connect-title">获取结果 · {provider.name}</span>
          <button type="button" className="mset-connect-close" onClick={onClose} aria-label="关闭">×</button>
        </div>
        <div className="mset-connect-body mset-form">
          <p className="mset-form-desc">
            端点报告 <b>{models.length}</b> 个模型
            {addedCount === 0
              ? `，其中 ${selectable.length} 个未添加`
              : `，其中 ${selectable.length} 个未添加、${addedCount} 个已添加`}
          </p>
          <div className="mset-probe-endpoint" title="实际请求的地址">{endpoint}</div>
          <div className="mset-connect-scroll mset-candidate-scroll" ref={listRef}>
            <CandidateRows
              items={models.map((c) => ({
                id: c.id,
                name: c.name,
                note: (c.context_window ?? 0) > 0 || (c.max_output_tokens ?? 0) > 0
                  ? `上下文 ${kfmtLimit(c.context_window ?? 0)} · 输出 ${kfmtLimit(c.max_output_tokens ?? 0)}`
                  : undefined,
                tags: c.added ? ["已添加"] : [...catalogTags(c), "新发现"],
                disabled: c.added,
              }))}
              picked={picked}
              onToggle={toggle}
            />
          </div>
          {/* 底栏在 body 内侧（与内容同边距）：左侧已选计数 + 全选，右侧取消/添加。
              计数与全选只针对未添加的；全部已添加时只留「关闭」。 */}
          <div className="mset-medit-foot mset-probe-foot">
            {selectable.length > 0 ? (
              <>
                <span className="mset-probe-text">已选 {picked.size} / {selectable.length}</span>
                <span className="mset-medit-actions">
                  <button type="button" className="mset-probe-all" onClick={toggleAll}>
                    {picked.size === selectable.length ? "全不选" : "全选"}
                  </button>
                  <button type="button" className="mset-add-cancel" onClick={onClose}>取消</button>
                  <button type="button" className="mset-add-confirm" disabled={busy || picked.size === 0} onClick={commit}>
                    {busy ? "添加中…" : `添加选中（${picked.size}）`}
                  </button>
                </span>
              </>
            ) : (
              <>
                <span className="mset-probe-text">全部已添加</span>
                <span className="mset-medit-actions">
                  <button type="button" className="mset-add-cancel" onClick={onClose}>关闭</button>
                </span>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
