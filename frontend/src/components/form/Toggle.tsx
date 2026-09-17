// 表单组件 · Toggle：开关（视觉复用既有 .toggle 胶囊——设置面板同款）。
// 点击/回车都在组件内部 stopPropagation：开关常驻于可点击容器（卡片行）中，
// 翻转不应触发容器的动作。
export function Toggle({
  on,
  onChange,
  ariaLabel,
}: {
  on: boolean;
  onChange?: (on: boolean) => void;
  ariaLabel?: string;
}) {
  return (
    <span
      className={"toggle" + (on ? " on" : "")}
      role="switch"
      tabIndex={0}
      aria-label={ariaLabel}
      aria-checked={on}
      onClick={(e) => {
        e.stopPropagation();
        onChange?.(!on);
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.stopPropagation();
          onChange?.(!on);
        }
      }}
    >
      <span className="toggle-knob" />
    </span>
  );
}
