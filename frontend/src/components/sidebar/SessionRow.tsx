// 会话行：蓝点 + 标题 + 时间 + hover ⋯ 菜单（重命名/归档）。
// 从 Sidebar 抽出的行渲染器——晚于首屏的行单独入场动画、重命名就地
// 编辑、菜单的 gsap 退场都内聚在这里；Sidebar 只注入回调与状态。
import { memo, useCallback, useRef, type RefCallback } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { gsap } from "gsap";
import type { SessionMeta } from "../../shared/types";
import { motionAllowed } from "../../shared/motion";
import { collapseAway } from "../../shared/anim";
import { IconPencil, IconArchive } from "../icons";

export interface SessionRowProps {
  session: SessionMeta;
  current: boolean;
  busy: boolean;
  renaming: boolean;
  menuOpen: boolean;
  /** 归属项目名——仅「全部」视图下传（范围不唯一时才知道要声明归属）；
   *  已按项目/未分组过滤时不传，避免同一信息重复。 */
  projectName?: string;
  /** 打开菜单 / 关闭菜单（Sidebar 持有 menuFor 单值状态）。 */
  onOpenMenu: (id: string) => void;
  onCloseMenu: () => void;
  /** 进入重命名（空串 = 取消重命名）。 */
  onStartRename: (id: string) => void;
  /** 提交重命名。 */
  onRename: (s: SessionMeta, title: string) => void;
  onArchive: (id: string) => void;
  onResume: (id: string) => void;
  /** 行入场 ref 回调（Sidebar 的晚到行浮现动画）。 */
  enterRow: RefCallback<HTMLDivElement>;
}

export const SessionRow = memo(function SessionRow({
  session: s,
  current,
  busy,
  renaming,
  menuOpen,
  projectName,
  onOpenMenu,
  onCloseMenu,
  onStartRename,
  onRename,
  onArchive,
  onResume,
  enterRow,
}: SessionRowProps) {
  const menuClosingRef = useRef(false);

  // 菜单关闭走 gsap 退场再卸载（直接卸载是瞬灭，开合不对称）。
  // 先禁 CSS 入场动画（fill:both 占住样式，gsap 接管不了）。
  const closeMenu = useCallback(() => {
    const menu = document.querySelector<HTMLElement>(`.session-item[data-sid="${s.id}"] .session-menu`);
    if (!menu || !motionAllowed() || menuClosingRef.current) {
      menuClosingRef.current = false;
      onCloseMenu();
      return;
    }
    menuClosingRef.current = true;
    gsap.set(menu, { animation: "none", pointerEvents: "none" });
    gsap.to(menu, {
      opacity: 0, y: -4, scale: 0.96, transformOrigin: "right top", duration: 0.16, ease: "power2.in", overwrite: true,
      onComplete: () => { menuClosingRef.current = false; onCloseMenu(); },
    });
  }, [s.id, onCloseMenu]);

  const doArchive = (e: ReactMouseEvent) => {
    collapseAway((e.currentTarget as HTMLElement).closest(".session-item"), () => onArchive(s.id));
  };

  return (
    <div
      data-sid={s.id}
      ref={enterRow}
      className={"session-item" + (current ? " active" : "")}
      style={menuOpen ? { zIndex: 30 } : undefined}
      onClick={() => !busy && !renaming && onResume(s.id)}
      title={s.title}
    >
      <div className="session-line">
        <span className={"s-dot" + (current && busy ? " live" : "")} aria-hidden />
        {renaming ? (
          <input
            className="session-rename"
            autoFocus
            defaultValue={s.title}
            aria-label="重命名会话"
            onClick={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
            onBlur={(e) => onRename(s, e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                onRename(s, e.currentTarget.value);
              }
              if (e.key === "Escape") {
                e.preventDefault();
                onStartRename("");
              }
            }}
          />
        ) : (
          <span className="title">{s.title}</span>
        )}
        <span className="time">{s.updatedAt}</span>
        {!renaming && (
          <button
            type="button"
            className="session-more"
            aria-label="会话操作"
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation();
              menuOpen ? closeMenu() : onOpenMenu(s.id);
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <circle cx="5" cy="12" r="1.7" />
              <circle cx="12" cy="12" r="1.7" />
              <circle cx="19" cy="12" r="1.7" />
            </svg>
          </button>
        )}
      </div>
      {/* 归属行：仅「全部」视图下出现（范围唯一时这行是冗余信息）。
          放第二行而不是挤在标题行——侧栏窄，标签会把标题压成省略号。 */}
      {projectName && (
        <div className="session-sub" title={projectName}>
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
          <span className="session-sub-name">{projectName}</span>
        </div>
      )}
      {menuOpen && (
        <div className="session-menu" role="menu" onPointerDown={(e) => e.stopPropagation()}>
          <button type="button" role="menuitem" onClick={() => { closeMenu(); onStartRename(s.id); }}>
            <IconPencil />
            重命名
          </button>
          <button type="button" role="menuitem" onClick={(e) => { closeMenu(); doArchive(e); }}>
            <IconArchive />
            归档
          </button>
        </div>
      )}
    </div>
  );
});
