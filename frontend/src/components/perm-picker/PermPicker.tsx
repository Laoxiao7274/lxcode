// 权限选择器（Codex 式菜单）：左主菜单 + hover 侧边描述面板。
// 菜单项带图标和标签（Codex 形态），hover 时右侧显示当前选项的完整描述。
import { useEffect, useRef, useState } from "react";
import { useSettings, type Settings } from "../../shared/settings";

const PRESETS: {
  id: Settings["approval"];
  label: string;
  desc: string;
  icon: string; // svg path
}[] = [
  {
    id: "confirm",
    label: "默认",
    desc: "Codex 在沙箱内自动运行命令。工作区内可读写，超出边界的操作（联网、写工作区外）需要你批准。",
    icon: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z",
  },
  {
    id: "auto",
    label: "完全访问",
    desc: "Codex 拥有对你电脑的完全访问权限，几乎不会打断你。仅在可随时销毁的隔离环境中使用。",
    icon: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
  },
  {
    id: "strict",
    label: "只读",
    desc: "Codex 只能读取文件和搜索，不会修改任何内容，也不会联网。适合规划、审查和问答。",
    icon: "M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z",
  },
];

export function PermPicker() {
  const { settings, set } = useSettings();
  const [open, setOpen] = useState(false);
  const [hovered, setHovered] = useState<number | null>(null);
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

  const current = PRESETS.find((p) => p.id === settings.approval) ?? PRESETS[0];
  const activeDesc = hovered !== null ? PRESETS[hovered].desc : current.desc;

  return (
    <div className="pop-wrap" ref={ref}>
      <span className="pop-trigger" onClick={() => setOpen((o) => !o)}>
        <button type="button" className="perm-chip" title="权限模式">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
          {current.label}
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
      </span>
      {open && (
        <div className="perm-menu" role="menu">
          <div className="perm-main">
            {PRESETS.map((p, i) => (
              <button
                key={p.id}
                type="button"
                className={"perm-item" + (settings.approval === p.id ? " on" : "")}
                role="menuitem"
                onMouseEnter={() => setHovered(i)}
                onMouseLeave={() => setHovered(null)}
                onClick={() => {
                  set({ approval: p.id });
                  setOpen(false);
                }}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d={p.icon} />
                </svg>
                <span className="perm-label">{p.label}</span>
                {settings.approval === p.id && (
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M20 6 9 17l-5-5" />
                  </svg>
                )}
              </button>
            ))}
          </div>
          {/* 侧边描述面板（hover 当前选项的说明） */}
          <div className="perm-desc-panel">
            <p>{activeDesc}</p>
          </div>
        </div>
      )}
    </div>
  );
}
