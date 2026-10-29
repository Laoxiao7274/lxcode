// 表单组件 · Chips：多选胶囊。选中黑底打勾；焦点态（细描边环）与选中态正交
// ——供「点击即选中 + 查看详情」的多级联动（onFocus 回调带出被点项）；
// dot 是条目的身份色（如委派对象），highRisk 尾部红点（如高危工具）。
import { IconCheck } from "../icons";

export interface ChipOption {
  value: string;
  label: string;
  /** 悬停说明（tooltip）。 */
  desc?: string;
  /** 身份色点（如 Agent 的标识色）。 */
  dot?: string;
  /** 高危标记（尾部红点）。 */
  highRisk?: boolean;
}

export function Chips({
  options,
  value,
  onChange,
  focusedValue,
  onFocus,
  ariaLabel,
  exclusive,
}: {
  options: ChipOption[];
  value: string[];
  onChange: (values: string[]) => void;
  /** 焦点条目（详情面板跟随的对象）。 */
  focusedValue?: string | null;
  onFocus?: (value: string) => void;
  ariaLabel?: string;
  /** 单选模式：点击未选中项替换选择、点击选中项取消（值恒 ≤1）。
   *  用于流程这类原子语义的目录——缺合适条目就去补一个完整模块，
   *  不靠多个拼装（与技能类多选的区别）。 */
  exclusive?: boolean;
}) {
  return (
    <div className="fd-chips" role="group" aria-label={ariaLabel}>
      {options.map((o) => {
        const on = value.includes(o.value);
        return (
          <button
            key={o.value}
            type="button"
            className={"fd-chip" + (on ? " on" : "") + (focusedValue === o.value ? " focus" : "")}
            title={o.desc}
            aria-pressed={on}
            onClick={() => {
              onChange(exclusive ? (on ? [] : [o.value]) : on ? value.filter((v) => v !== o.value) : [...value, o.value]);
              onFocus?.(o.value);
            }}
          >
            {o.dot && <span className="fd-chip-dot" style={{ background: o.dot }} />}
            {on && <IconCheck size={9} strokeWidth={2.6} />}
            {o.label}
            {o.highRisk && <span className="fd-chip-risk" aria-label="高危" />}
          </button>
        );
      })}
    </div>
  );
}
