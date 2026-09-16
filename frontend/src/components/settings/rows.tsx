// 设置分区通用行组件（标题块 / 开关行 / 值行 / 分段行 / 占位行 / 归档行）。
import type { ReactNode } from "react";

export function Section({ title, desc, children }: { title: string; desc: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="set-sec-title">{title}</h3>
      <p className="set-sec-desc">{desc}</p>
      <div className="set-sec-body">{children}</div>
    </section>
  );
}

export function ToggleRow({ label, hint, checked, onChange }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <span
        className={"toggle" + (checked ? " on" : "")}
        role="switch"
        aria-checked={checked}
        tabIndex={0}
        onClick={() => onChange(!checked)}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onChange(!checked); } }}
      >
        <span className="toggle-knob" />
      </span>
    </div>
  );
}

export function ValueRow({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <span className="set-row-value">{value}</span>
    </div>
  );
}

export function SegRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
      </div>
      {children}
    </div>
  );
}

export function PlaceholderRow({ label, hint }: { label: string; hint?: string }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
    </div>
  );
}

export function ArchivedRow({ title, date, onRestore }: { title: string; date: string; onRestore: () => void }) {
  return (
    <div className="archived-row">
      <span className="archived-title">{title}</span>
      <span className="archived-date">{date}</span>
      <button type="button" className="archived-restore" onClick={onRestore}>恢复</button>
    </div>
  );
}
