// 模型配置弹窗：单独编辑一个模型——ID / 显示名称 / 描述 / 上下文窗口 /
// 最大输出 / 能力标签，附删除与设为默认。弹窗语言复用连接提供商那套。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { useSettings, EFFORTS, type EffortId, type ModelMeta, type ProviderMeta } from "../../shared/settings";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { kfmtTokens } from "../../shared/format";

const TAG_CHOICES = ["推理", "工具", "视觉"];

/** 宽松解析 k 记法（"128k" / "128000" → tokens）。 */
const parseK = (s: string): number | null => {
  const t = s.trim().toLowerCase();
  const m = t.match(/^(\d+(?:\.\d+)?)\s*k?$/);
  if (!m) return null;
  const n = Math.round(parseFloat(m[1]) * (t.endsWith("k") ? 1_000 : 1));
  return n > 0 ? n : null;
};

export function ModelEditDialog({ provider, model, onClose }: { provider: ProviderMeta; model: ModelMeta; onClose: () => void }) {
  const { settings, set, updateModel, removeModel } = useSettings();
  const [draft, setDraft] = useState({
    id: model.id,
    name: model.name,
    desc: model.desc,
    contextWindow: kfmtTokens(model.contextWindow),
    maxOutput: kfmtTokens(model.maxOutput),
    tags: [...model.tags],
    efforts: [...model.efforts] as EffortId[],
  });
  const [err, setErr] = useState<string | null>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const isDefault = settings.model === model.id;

  useEscape(true, onClose);

  // 字段交错入场
  useEffect(() => {
    const el = formRef.current;
    if (!el || !motionAllowed()) return;
    gsap.fromTo(
      el.querySelectorAll(".mset-edit-field, .mset-edit-tags"),
      { opacity: 0, y: 8 },
      { opacity: 1, y: 0, duration: 0.26, stagger: 0.035, ease: "power2.out", clearProps: "transform,opacity" },
    );
  }, []);

  const save = () => {
    if (!draft.id.trim()) {
      setErr("模型 ID 不能为空");
      return;
    }
    if (!draft.name.trim()) {
      setErr("显示名称不能为空");
      return;
    }
    const ctx = parseK(draft.contextWindow);
    if (ctx === null) {
      setErr("上下文窗口格式不对（如 128k）");
      return;
    }
    const out = parseK(draft.maxOutput);
    if (out === null) {
      setErr("最大输出格式不对（如 8k）");
      return;
    }
    if (
      !updateModel(provider.id, model.id, {
        id: draft.id,
        name: draft.name,
        desc: draft.desc.trim(),
        tags: draft.tags,
        efforts: draft.efforts,
        contextWindow: ctx,
        maxOutput: out,
      })
    ) {
      setErr("模型 ID 已存在");
      return;
    }
    onClose();
  };

  const patch = (p: Partial<typeof draft>) => {
    setDraft((d) => ({ ...d, ...p }));
    setErr(null);
  };

  return (
    <div className="mset-connect-mask" role="dialog" aria-label="模型配置" aria-modal="true">
      <div className="mset-connect">
        <div className="mset-connect-head">
          <span className="mset-connect-back-spacer" />
          <span className="mset-connect-title">模型配置 · {provider.name}</span>
          {isDefault && <span className="mset-medit-badge">当前默认</span>}
          <button type="button" className="mset-connect-close" onClick={onClose} aria-label="关闭">×</button>
        </div>
        <form
          className="mset-connect-body mset-form"
          ref={formRef}
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <div className="mset-edit-fields">
            <label className="mset-edit-field">
              <span>模型 ID</span>
              <input className="mono" autoFocus value={draft.id} onChange={(e) => patch({ id: e.target.value })} spellCheck={false} />
            </label>
            <label className="mset-edit-field">
              <span>显示名称</span>
              <input value={draft.name} onChange={(e) => patch({ name: e.target.value })} spellCheck={false} />
            </label>
            <label className="mset-edit-field wide">
              <span>描述（选择器里显示的那行灰字）</span>
              <input value={draft.desc} onChange={(e) => patch({ desc: e.target.value })} placeholder="例如：通用对话" spellCheck={false} />
            </label>
            <label className="mset-edit-field">
              <span>上下文窗口</span>
              <input className="mono" value={draft.contextWindow} onChange={(e) => patch({ contextWindow: e.target.value })} placeholder="128k" spellCheck={false} />
            </label>
            <label className="mset-edit-field">
              <span>最大输出</span>
              <input className="mono" value={draft.maxOutput} onChange={(e) => patch({ maxOutput: e.target.value })} placeholder="8k" spellCheck={false} />
            </label>
          </div>
          <div className="mset-edit-tags">
            <span className="mset-chip-label">能力</span>
            {TAG_CHOICES.map((t) => (
              <button
                key={t}
                type="button"
                className={"mset-tag-toggle" + (draft.tags.includes(t) ? " on" : "")}
                aria-pressed={draft.tags.includes(t)}
                onClick={() => patch({ tags: draft.tags.includes(t) ? draft.tags.filter((x) => x !== t) : [...draft.tags, t] })}
              >
                {t}
              </button>
            ))}
          </div>
          <div className="mset-edit-tags">
            <span className="mset-chip-label">档位</span>
            {EFFORTS.map((e) => (
              <button
                key={e.id}
                type="button"
                className={"mset-tag-toggle" + (draft.efforts.includes(e.id) ? " on" : "")}
                aria-pressed={draft.efforts.includes(e.id)}
                title={e.hint}
                onClick={() => patch({ efforts: draft.efforts.includes(e.id) ? draft.efforts.filter((x) => x !== e.id) : [...draft.efforts, e.id] })}
              >
                {e.label}
              </button>
            ))}
          </div>
          {err && <div className="mset-edit-err" role="alert">{err}</div>}
          <div className="mset-medit-foot">
            <button
              type="button"
              className="mset-del-danger"
              onClick={() => {
                removeModel(provider.id, model.id);
                onClose();
              }}
            >
              删除模型
            </button>
            <span className="mset-medit-actions">
              {!isDefault && (
                <button type="button" className="mset-add-cancel" onClick={() => set({ model: model.id })}>
                  设为默认
                </button>
              )}
              <button type="button" className="mset-add-cancel" onClick={onClose}>取消</button>
              <button type="submit" className="mset-add-confirm">保存</button>
            </span>
          </div>
        </form>
      </div>
    </div>
  );
}
