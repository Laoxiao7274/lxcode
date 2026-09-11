// 模型选择器（Codex 式双面板）：打开先显示模型列表（左），
// 点底部「推理强度 >」展开右面板调强度。两面板独立错位浮层。
import { useEffect, useRef, useState } from "react";
import { useSettings, MODELS, EFFORTS, type Settings } from "../../shared/settings";

const check = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M20 6 9 17l-5-5" />
  </svg>
);
const chevronRight = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="m9 6 6 6-6 6" />
  </svg>
);

export function ModelPicker() {
  const { settings, set } = useSettings();
  const [open, setOpen] = useState(false);
  // 打开先显示模型（先选模型，再调强度）
  const [panel, setPanel] = useState<"model" | "effort">("model");
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // 每次重新打开回到模型面板
  const toggle = () => {
    setOpen((o) => {
      if (!o) setPanel("model");
      return !o;
    });
  };

  const model = MODELS.find((m) => m.id === settings.model);
  const effortLabel = EFFORTS.find((e) => e.id === settings.effort)?.label;

  return (
    <div className="pop-wrap" ref={ref}>
      <span className="pop-trigger" onClick={toggle}>
        <button type="button" className="model-chip" title="模型与推理强度">
          {settings.model}
          <span className="chip-dim">· {effortLabel}</span>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
      </span>
      {open && (
        <>
          {/* 左面板：模型列表（打开即显示）+ 底部强度跳转行 */}
          <div className="mp-panel mp-left" role="menu">
            <div className="mp-title">模型</div>
            {MODELS.map((m) => (
              <button
                key={m.id}
                type="button"
                className={"mp-item" + (settings.model === m.id ? " on" : "")}
                role="menuitem"
                onClick={() => set({ model: m.id })}
              >
                <span className="mp-label">
                  <span className="mp-model-name">{m.id}</span>
                  <span className="mp-model-desc">{m.desc}</span>
                </span>
                {settings.model === m.id && <span className="mp-check">{check}</span>}
              </button>
            ))}
            <div className="mp-sep" />
            <button
              type="button"
              className={"mp-item mp-jump" + (panel === "effort" ? " hl" : "")}
              onClick={() => setPanel(panel === "effort" ? "model" : "effort")}
            >
              <span className="mp-label">推理强度 · {effortLabel}</span>
              <span className="mp-jump-arrow">{chevronRight}</span>
            </button>
          </div>
          {/* 右面板：推理强度（点左面板底部行展开） */}
          {panel === "effort" && (
            <div className="mp-panel mp-right" role="menu">
              <div className="mp-title">推理强度</div>
              {EFFORTS.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  className={"mp-item" + (settings.effort === e.id ? " on" : "")}
                  role="menuitem"
                  onClick={() => set({ effort: e.id as Settings["effort"] })}
                >
                  <span className="mp-label">{e.label}</span>
                  {settings.effort === e.id && <span className="mp-check">{check}</span>}
                </button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
