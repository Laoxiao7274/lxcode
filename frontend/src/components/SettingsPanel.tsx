// 设置面板（Codex 式右侧滑出）：半透明遮罩 + 右侧圆角面板，
// 含外观/行为两组设置。原型数据落 Settings context。
import { useSettings, MODELS, EFFORTS, APPROVALS, type Settings } from "../settings";

export function SettingsPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { settings, set } = useSettings();
  if (!open) return null;
  return (
    <>
      <div className="panel-mask" onClick={onClose} aria-hidden />
      <aside className="settings-panel" role="dialog" aria-label="设置">
        <header className="panel-head">
          <span className="panel-title">设置</span>
          <button type="button" className="panel-close" onClick={onClose} aria-label="关闭设置">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </header>
        <div className="panel-body">
          <section className="panel-group">
            <div className="panel-label">模型</div>
            {MODELS.map((m) => (
              <label key={m.id} className="panel-row">
                <span className="panel-row-text">
                  <span className="panel-row-title">{m.id}</span>
                  <span className="panel-row-sub">{m.desc}</span>
                </span>
                <input
                  type="radio"
                  name="model"
                  checked={settings.model === m.id}
                  onChange={() => set({ model: m.id })}
                />
              </label>
            ))}
          </section>
          <section className="panel-group">
            <div className="panel-label">推理强度</div>
            <div className="panel-seg">
              {EFFORTS.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  className={"seg-btn" + (settings.effort === e.id ? " on" : "")}
                  onClick={() => set({ effort: e.id as Settings["effort"] })}
                >
                  {e.label}
                </button>
              ))}
            </div>
          </section>
          <section className="panel-group">
            <div className="panel-label">高危操作</div>
            {APPROVALS.map((a) => (
              <label key={a.id} className="panel-row">
                <span className="panel-row-text">
                  <span className="panel-row-title">{a.label}</span>
                  <span className="panel-row-sub">{a.hint}</span>
                </span>
                <input
                  type="radio"
                  name="approval"
                  checked={settings.approval === a.id}
                  onChange={() => set({ approval: a.id as Settings["approval"] })}
                />
              </label>
            ))}
          </section>
          <section className="panel-group">
            <div className="panel-label">显示</div>
            <label className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">思考链</span>
                <span className="panel-row-sub">思考中显示推理过程</span>
              </span>
              <input
                type="checkbox"
                checked={settings.showThinking}
                onChange={(e) => set({ showThinking: e.target.checked })}
              />
            </label>
          </section>
        </div>
        <footer className="panel-foot">lxcode 0.1 · eabc22c</footer>
      </aside>
    </>
  );
}
