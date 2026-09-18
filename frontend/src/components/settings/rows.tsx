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

/** 文本输入行（可配置的字符串——如 Git 默认分支）。 */
export function InputRow({ label, hint, value, onChange, placeholder, mono }: {
  label: string; hint?: string; value: string; onChange: (v: string) => void;
  placeholder?: string; mono?: boolean;
}) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <input
        className={"set-row-input" + (mono ? " mono" : "")}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        aria-label={label}
      />
    </div>
  );
}

/** 下拉选择行（可配置的枚举——如默认模型）。 */
export function SelectRow({ label, hint, value, onChange, options, ariaLabel }: {
  label: string; hint?: string; value: string; onChange: (v: string) => void;
  options: { value: string; label: string; desc?: string }[]; ariaLabel?: string;
}) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <select
        className="set-row-select"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={ariaLabel ?? label}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
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
