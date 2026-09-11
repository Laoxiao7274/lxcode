// 通用浮层菜单（Codex 式：白底圆角 + 阴影 + 选项行）。
// 点击外部/Escape 关闭；无依赖（原型层，不引 popover 库）。
import { useEffect, useRef, useState, type ReactNode } from "react";

export interface MenuOption {
  id: string;
  label: string;
  hint?: string;
  icon?: ReactNode;
  selected?: boolean;
  onSelect: () => void;
}

export function PopoverMenu({
  trigger,
  options,
  title,
  align = "left",
  width = 240,
}: {
  /** 触发元素（菜单锚定其下方）。 */
  trigger: (open: boolean) => ReactNode;
  options: MenuOption[];
  title?: string;
  align?: "left" | "right";
  width?: number;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div className="pop-wrap" ref={ref}>
      <span className="pop-trigger" onClick={() => setOpen((o) => !o)}>
        {trigger(open)}
      </span>
      {open && (
        <div className="pop-menu" style={{ width, [align]: 0 }} role="menu">
          {title && <div className="pop-title">{title}</div>}
          {options.map((o) => (
            <button
              key={o.id}
              type="button"
              className={"pop-item" + (o.selected ? " selected" : "")}
              role="menuitem"
              onClick={() => {
                o.onSelect();
                setOpen(false);
              }}
            >
              {o.icon && <span className="pop-icon">{o.icon}</span>}
              <span className="pop-label">{o.label}</span>
              {o.hint && <span className="pop-hint">{o.hint}</span>}
              {o.selected && (
                <svg className="pop-check" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M20 6 9 17l-5-5" />
                </svg>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
