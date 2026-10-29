// 表单组件 · TextInput：文本输入（.fd-input——与 Select 触发器同规格）。
// 受控 API：value/onChange；其余 input 原生属性经 rest 透传（type/autoFocus/
// onKeyDown/spellCheck/title/disabled…），inputRef 供宿主做聚焦控制。
import type { InputHTMLAttributes, Ref } from "react";

export function TextInput({
  value,
  onChange,
  className,
  inputRef,
  ...rest
}: Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange" | "className"> & {
  value: string;
  onChange: (value: string) => void;
  className?: string;
  /** 宿主聚焦控制（自动聚焦/行内编辑定位）。 */
  inputRef?: Ref<HTMLInputElement>;
}) {
  return (
    <input
      type="text"
      className={"fd-input" + (className ? " " + className : "")}
      value={value}
      ref={inputRef}
      onChange={(e) => onChange(e.target.value)}
      {...rest}
    />
  );
}
