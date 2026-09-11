// 设置弹窗（Codex 式）：居中模态 + 分区滚动列表。
// 分区结构对齐 Codex 官方设置文档（General/Appearance/Notifications/
// Agent configuration/Git/Integrations/Personalization/Archived threads），
// 控件映射 lxcode 的现有能力。
import { useEffect } from "react";
import { useSettings, MODELS, EFFORTS, APPROVALS, type Settings } from "../settings";

const toggleIcon = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M20 6 9 17l-5-5" />
  </svg>
);

export function SettingsPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { settings, set } = useSettings();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <>
      <div className="panel-mask" onClick={onClose} aria-hidden />
      <aside className="settings-panel" role="dialog" aria-label="设置" aria-modal="true">
        <header className="panel-head">
          <span className="panel-title">设置</span>
          <button type="button" className="panel-close" onClick={onClose} aria-label="关闭设置">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </header>

        <div className="panel-body">
          {/* General —— 通用行为 */}
          <section className="panel-group">
            <div className="panel-group-head">
              <div className="panel-group-title">通用</div>
              <div className="panel-group-desc">命令输出在对话里的展示量与生成时的电源行为。</div>
            </div>
            <SettingToggle
              label="命令输出完整展示"
              hint="关闭时工具结果默认折叠为摘要行"
              checked={settings.showThinking}
              onChange={(v) => set({ showThinking: v })}
            />
            <SettingToggle
              label="生成时阻止休眠"
              hint="有任务运行时保持屏幕常亮"
              checked={settings.keepAwake}
              onChange={(v) => set({ keepAwake: v })}
            />
          </section>

          {/* Appearance —— 外观 */}
          <section className="panel-group">
            <div className="panel-group-head">
              <div className="panel-group-title">外观</div>
              <div className="panel-group-desc">主题与界面字体。字体选择作用于全局，含终端块。</div>
            </div>
            <div className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">主题</span>
                <span className="panel-row-sub">跟随系统 / 浅色 / 深色</span>
              </span>
              <span className="panel-value">浅色</span>
            </div>
          </section>

          {/* Agent configuration */}
          <section className="panel-group">
            <div className="panel-group-head">
              <div className="panel-group-title">智能体配置</div>
              <div className="panel-group-desc">模型、推理强度与高危操作确认模式。</div>
            </div>
            <div className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">模型</span>
              </span>
              <span className="panel-value">{settings.model}</span>
            </div>
            <div className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">推理强度</span>
              </span>
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
            </div>
            <div className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">高危操作</span>
                <span className="panel-row-sub">{APPROVALS.find((a) => a.id === settings.approval)?.hint}</span>
              </span>
              <span className="panel-value">{APPROVALS.find((a) => a.id === settings.approval)?.label}</span>
            </div>
          </section>

          {/* Personalization */}
          <section className="panel-group">
            <div className="panel-group-head">
              <div className="panel-group-title">个性化</div>
              <div className="panel-group-desc">回答的默认语气；自定义指令写入 AGENTS.md。</div>
            </div>
            <div className="panel-row">
              <span className="panel-row-text">
                <span className="panel-row-title">语气</span>
              </span>
              <div className="panel-seg">
                {([
                  { id: "friendly", label: "友好" },
                  { id: "pragmatic", label: "务实" },
                  { id: "none", label: "无" },
                ] as const).map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className={"seg-btn" + (settings.personality === p.id ? " on" : "")}
                    onClick={() => set({ personality: p.id })}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>
          </section>

          {/* Archived threads */}
          <section className="panel-group">
            <div className="panel-group-head">
              <div className="panel-group-title">归档任务</div>
              <div className="panel-group-desc">归档的会话列表，可恢复继续。</div>
            </div>
            <div className="archived-row">
              <span className="archived-title">前后台分离的协议层评审</span>
              <span className="archived-date">昨天</span>
              <button type="button" className="archived-restore">恢复</button>
            </div>
            <div className="archived-row">
              <span className="archived-title">选型：Tauri 壳的边界</span>
              <span className="archived-date">上周</span>
              <button type="button" className="archived-restore">恢复</button>
            </div>
          </section>
        </div>

        <footer className="panel-foot">
          <span>lxcode 0.1 · eabc22c</span>
          <span className="panel-foot-hint">高级选项编辑 config.toml</span>
        </footer>
      </aside>
    </>
  );
}

function SettingToggle({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="panel-row">
      <span className="panel-row-text">
        <span className="panel-row-title">{label}</span>
        {hint && <span className="panel-row-sub">{hint}</span>}
      </span>
      <span
        className={"toggle" + (checked ? " on" : "")}
        role="switch"
        aria-checked={checked}
        tabIndex={0}
        onClick={() => onChange(!checked)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onChange(!checked);
          }
        }}
      >
        <span className="toggle-knob" />
        {checked && <span className="toggle-check">{toggleIcon}</span>}
      </span>
    </label>
  );
}
