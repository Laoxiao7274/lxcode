// 表单组件 · ColorPicker：标识色板（选中环 + 环境色点）。
export function ColorPicker({
  colors,
  value,
  onChange,
  ariaLabel = "选择颜色",
}: {
  colors: string[];
  value: string;
  onChange: (color: string) => void;
  ariaLabel?: string;
}) {
  return (
    <div className="fd-swatches" role="radiogroup" aria-label={ariaLabel}>
      {colors.map((c) => (
        <button
          key={c}
          type="button"
          className={"fd-swatch" + (value === c ? " on" : "")}
          style={{ background: c }}
          aria-label={`颜色 ${c}`}
          role="radio"
          aria-checked={value === c}
          onClick={() => onChange(c)}
        />
      ))}
    </div>
  );
}
