// 表单组件 · Select：自绘下拉（原生 select 的 OS 皮肤与应用设计语言不搭）。
// 触发器与文本输入同规格（同边框/圆角/内距），菜单复用 usePopover 的
// 开合/点外/Esc/gsap 退场动画约定（面板挂 data-pop），向下弹出。
// 分组选项 + 选中打勾 + 次行说明；列表为空时显示提示并禁用触发器。
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown } from "../icons";

export interface SelectOption {
  value: string;
  label: string;
  desc?: string;
}

export interface SelectGroup {
  /** 分组标题（缺省 = 无标题的顶层组）。 */
  group?: string;
  options: SelectOption[];
}

export function Select({
  value,
  onChange,
  groups,
  placeholder,
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  groups: SelectGroup[];
  placeholder?: string;
  ariaLabel?: string;
}) {
  const { open, toggle, requestClose, rootRef } = usePopover();
  const all = groups.flatMap((g) => g.options);
  const current = all.find((o) => o.value === value);
  const empty = all.length === 0;

  return (
    <div className={"fd-select" + (open ? " open" : "")} ref={rootRef}>
      <button
        type="button"
        className={"fd-trigger" + (open ? " open" : "")}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={toggle}
        disabled={empty}
      >
        <span className={"fd-trigger-value" + (current ? "" : " placeholder")}>
          {current ? current.label : empty ? "没有可用选项" : (placeholder ?? "请选择")}
        </span>
        <span className="fd-chevron">
          <IconChevronDown size={10} />
        </span>
      </button>
      {open && (
        <div className="fd-menu" role="listbox" data-pop aria-label={ariaLabel}>
          {empty && <div className="fd-empty">没有可用选项</div>}
          {groups.map((g, gi) => (
            <div key={g.group ?? gi} className="fd-group">
              {g.group && <div className="fd-group-title">{g.group}</div>}
              {g.options.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  className={"fd-item" + (o.value === value ? " on" : "")}
                  role="option"
                  aria-selected={o.value === value}
                  onClick={() => {
                    onChange(o.value);
                    requestClose();
                  }}
                >
                  <span className="fd-item-label">
                    <span className="fd-item-name">{o.label}</span>
                    {o.desc && <span className="fd-item-desc">{o.desc}</span>}
                  </span>
                  {o.value === value && <IconCheck />}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
