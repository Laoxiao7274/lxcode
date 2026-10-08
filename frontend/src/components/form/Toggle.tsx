// 表单组件 · Toggle：开关（视觉复用既有 .toggle 胶囊——设置面板同款）。
// 点击/回车都在组件内部 stopPropagation：开关常驻于可点击容器（卡片行）中，
// 翻转不应触发容器的动作。
export function Toggle({
  on,
  onChange,
  ariaLabel,
  disabled,
}: {
  on: boolean;
  onChange?: (on: boolean) => void;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  return (
    <span
      className={"toggle" + (on ? " on" : "") + (disabled ? " disabled" : "")}
      role="switch"
      tabIndex={disabled ? undefined : 0}
      aria-label={ariaLabel}
      aria-checked={on}
      aria-disabled={disabled || undefined}
      onClick={(e) => {
        e.stopPropagation();
        if (disabled) return;
        onChange?.(!on);
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.stopPropagation();
          if (disabled) return;
          onChange?.(!on);
        }
      }}
    >
      <span className="toggle-knob" />
    </span>
  );
}
