// 设置分区通用行组件（标题块 / 开关行 / 值行 / 分段行 / 占位行 / 归档行）。
// 控件一律走表单套件（components/form——.fd- 命名空间）：Toggle /
// TextInput / Select / Button——本文件只做「行布局」（标签左、控件右）
// 与行内尺寸，不再裸写原生控件。
import type { ReactNode } from "react";
import { Button, Select, TextInput, Toggle, type SelectGroup } from "../form";

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
      <Toggle on={checked} onChange={onChange} ariaLabel={label} />
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

/** 分段选择行：标签在上、分段占满行宽（窄行内挤 3-4 个选项会把按钮
 *  压到文字宽度以下——溢出；占满行后每个按钮有充足空间）。 */
export function SegRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="set-row set-row-block">
      <div className="set-row-label">{label}</div>
      <div className="set-row-seg">{children}</div>
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
      <TextInput
        className={"set-row-input" + (mono ? " mono" : "")}
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        aria-label={label}
      />
    </div>
  );
}

/** 下拉选择行（可配置的枚举——如默认模型）。控件走套件自绘 Select，
 *  行内只负责宽度约束（.set-row-select 定宽 230px）。 */
export function SelectRow({ label, hint, value, onChange, options, ariaLabel }: {
  label: string; hint?: string; value: string; onChange: (v: string) => void;
  options: SelectGroup["options"]; ariaLabel?: string;
}) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <div className="set-row-select">
        <Select value={value} onChange={onChange} groups={[{ options }]} ariaLabel={ariaLabel ?? label} />
      </div>
    </div>
  );
}

export function ArchivedRow({ title, date, onRestore }: { title: string; date: string; onRestore: () => void }) {
  return (
    <div className="archived-row">
      <span className="archived-title">{title}</span>
      <span className="archived-date">{date}</span>
      <Button variant="ghost" className="archived-restore" onClick={onRestore}>恢复</Button>
    </div>
  );
}
