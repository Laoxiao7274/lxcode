// 表单组件 · Textarea：多行文本（系统提示词这类长文；等宽字体）。
export function Textarea({
  value,
  onChange,
  placeholder,
  className,
  disabled,
  ariaLabel,
  spellCheck = false,
  rows,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  disabled?: boolean;
  ariaLabel?: string;
  spellCheck?: boolean;
  rows?: number;
}) {
  return (
    <textarea
      className={"fd-textarea" + (className ? " " + className : "")}
      value={value}
      placeholder={placeholder}
      aria-label={ariaLabel}
      disabled={disabled}
      spellCheck={spellCheck}
      rows={rows}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
