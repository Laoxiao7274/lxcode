// 模型选择器（Codex 式双菜单）：左 = 推理强度 + 底部模型跳转行，
// 右 = 模型列表。两菜单并排、互跳（左底部「模型名 >」、右底部强度行）。
// 证据：opencodex 项目对 Codex App picker 的截图复刻。
import { useEffect, useRef, useState } from "react";
import { useSettings, MODELS, EFFORTS, type Settings } from "../settings";

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
  const [panel, setPanel] = useState<"effort" | "model">("effort");
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

  const model = MODELS.find((m) => m.id === settings.model);
  const effortLabel = EFFORTS.find((e) => e.id === settings.effort)?.label;

  return (
    <div className="pop-wrap" ref={ref}>
      <span className="pop-trigger" onClick={() => setOpen((o) => !o)}>
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
          {/* 左面板：推理强度（独立浮层，锚定输入框上方） */}
          <div className="mp-panel mp-left" role="menu">
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
            <div className="mp-sep" />
            <button
              type="button"
              className={"mp-item mp-jump" + (panel === "model" ? " hl" : "")}
              onClick={() => setPanel("model")}
            >
              <span className="mp-label">{model?.id ?? settings.model}</span>
              <span className="mp-jump-arrow">{chevronRight}</span>
            </button>
          </div>
          {/* 右面板：模型列表（panel=model 时显示；独立浮层，向右错开） */}
          {panel === "model" && (
            <div className="mp-panel mp-right" role="menu">
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
              <button type="button" className="mp-item mp-jump" onClick={() => setPanel("effort")}>
                <span className="mp-label">
                  推理强度 · {effortLabel}
                </span>
                <span className="mp-jump-arrow">{chevronRight}</span>
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
