// 会话行：蓝点 + 标题 + 时间 + hover ⋯ 菜单（重命名/归档）。
// 从 Sidebar 抽出的行渲染器——晚于首屏的行单独入场动画、重命名就地
// 编辑、菜单的 gsap 退场都内聚在这里；Sidebar 只注入回调与状态。
import { memo, useCallback, useRef, useState, type RefCallback } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { gsap } from "gsap";
import type { SessionMeta } from "../../shared/types";
import { motionAllowed } from "../../shared/motion";
import { collapseAway } from "../../shared/anim";
import { IconPencil, IconArchive, IconDownload } from "../icons";

export interface SessionRowProps {
  session: SessionMeta;
  current: boolean;
  busy: boolean;
  renaming: boolean;
  menuOpen: boolean;
  /** 打开菜单 / 关闭菜单（Sidebar 持有 menuFor 单值状态）。 */
  onOpenMenu: (id: string) => void;
  onCloseMenu: () => void;
  /** 进入重命名（空串 = 取消重命名）。 */
  onStartRename: (id: string) => void;
  /** 提交重命名。 */
  onRename: (s: SessionMeta, title: string) => void;
  onArchive: (id: string) => void;
  onReleaseWorktree: (id: string) => Promise<void>;
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
  onOpenMenu,
  onCloseMenu,
  onStartRename,
  onRename,
  onArchive,
  onReleaseWorktree,
  onResume,
  enterRow,
}: SessionRowProps) {
  const menuClosingRef = useRef(false);
  const [releaseOpen, setReleaseOpen] = useState(false);
  const [releaseBusy, setReleaseBusy] = useState(false);
  const [releaseDone, setReleaseDone] = useState(false);
  const [releaseError, setReleaseError] = useState("");

  const closeRelease = () => {
    if (releaseBusy) return;
    setReleaseOpen(false);
    setReleaseDone(false);
    setReleaseError("");
  };

  const confirmRelease = async () => {
    setReleaseBusy(true);
    setReleaseError("");
    try {
      await onReleaseWorktree(s.id);
      setReleaseDone(true);
    } catch (error) {
      setReleaseError(error instanceof Error ? error.message : String(error));
    } finally {
      setReleaseBusy(false);
    }
  };

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
      role={releaseOpen ? undefined : "button"}
      tabIndex={renaming || releaseOpen ? -1 : 0}
      aria-current={current ? "page" : undefined}
      aria-label={releaseOpen ? undefined : `${s.title}${busy ? "（生成中）" : ""}`}
      style={menuOpen ? { zIndex: 30 } : undefined}
      onClick={() => !renaming && onResume(s.id)}
      onKeyDown={(e) => {
        if ((e.key === "Enter" || e.key === " ") && !renaming) {
          e.preventDefault();
          onResume(s.id);
        }
      }}
      title={s.title}
    >
      <div className="session-line">
        <span className={"s-dot" + (busy ? " live" : "")} aria-hidden />
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
      {menuOpen && (
        <div className="session-menu" role="menu" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
          <button type="button" role="menuitem" onClick={() => { closeMenu(); onStartRename(s.id); }}>
            <IconPencil />
            重命名
          </button>
          {s.workspace && s.messages > 0 && (
            <button type="button" role="menuitem" onClick={() => {
              closeMenu();
              setReleaseDone(false);
              setReleaseError("");
              setReleaseOpen(true);
            }}>
              <IconDownload />
              释放工作区
            </button>
          )}
          <button type="button" role="menuitem" onClick={(e) => { e.stopPropagation(); closeMenu(); doArchive(e); }}>
            <IconArchive />
            归档
          </button>
        </div>
      )}
      {releaseOpen && (
        <div
          className="proj-add-mask"
          role="alertdialog"
          aria-modal="true"
          aria-label="释放工作区"
          onKeyDown={(e) => {
            e.stopPropagation();
            if (e.key === "Escape" && !releaseBusy) closeRelease();
          }}
          onPointerDown={(e) => {
            e.stopPropagation();
            if (e.target === e.currentTarget) closeRelease();
          }}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="proj-add session-release">
            <div className="proj-add-head">
              <span className="proj-add-title">{releaseDone ? "工作区已释放" : "释放工作区？"}</span>
              {!releaseBusy && (
                <button type="button" className="proj-add-close" aria-label="关闭" onClick={closeRelease}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                    <path d="m18 6-12 12M6 6l12 12" />
                  </svg>
                </button>
              )}
            </div>
            <div className="proj-add-body">
              {releaseDone ? (
                <p className="proj-field-hint">会话记录和分支已保留。下次恢复该会话时，会从原分支重建工作区。</p>
              ) : (
                <>
                  <p className="proj-field-hint">只移除这份项目目录；会话记录和 Git 分支会保留，不会自动合并到主项目。</p>
                  <p className="proj-field-hint">未提交或未跟踪的文件会阻止释放。忽略文件（例如依赖和构建缓存）会随工作区目录一起删除。</p>
                  {releaseError && <div className="proj-add-error" role="alert">{releaseError}</div>}
                </>
              )}
            </div>
            <div className="proj-add-foot">
              {releaseDone ? (
                <button type="button" className="proj-add-ok" autoFocus onClick={closeRelease}>完成</button>
              ) : (
                <>
                  <button type="button" className="proj-add-cancel" disabled={releaseBusy} autoFocus onClick={closeRelease}>取消</button>
                  <button type="button" className="proj-add-ok" disabled={releaseBusy} aria-busy={releaseBusy} onClick={() => void confirmRelease()}>
                    {releaseBusy ? "正在释放…" : "释放工作区"}
                  </button>
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
});
