import { useCallback, useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { gsap } from "gsap";
import type { AgentSource, SessionMeta } from "../../shared/types";
import { motionAllowed, staggerIn } from "../../shared/motion";
import { collapseAway, playEnter } from "../../shared/anim";
import { useDismissal } from "../../shared/popover";
import { IconPencil, IconArchive } from "../icons";
import { AddProjectDialog } from "./AddProjectDialog";

/** 「未分组」过滤目标（无归属会话的家——不依赖真实项目 id）。 */
const LOOSE = "";

/** 侧栏（Codex 2026-05 版形态）：
 *  导航项（新对话/搜索/插件/自动化）→「项目」分组（上）→「对话」分组（下）。
 *  项目区：+ 号添加；点项目行过滤对话（再点取消）；未绑定项目的会话
 *  归入「未分组」行。选中项目后「新对话」按钮带归属提示。 */
export function Sidebar({
  source,
  currentId,
  busy,
  onOpenSettings,
}: {
  source: AgentSource;
  currentId: string;
  busy: boolean;
  onOpenSettings: () => void;
}) {
  const [query, setQuery] = useState("");
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  /** 对话过滤目标：项目 id / LOOSE（未分组）/ null（全部）。
   *  「新对话」的归属上下文 = 选中项目（LOOSE/全部 = 无归属新会话）。 */
  const [filter, setFilter] = useState<string | null>(null);
  const [projectsTick, setProjectsTick] = useState(0); // projectsChanged 事件驱动重读
  const searchRef = useRef<HTMLInputElement>(null);
  const sideRef = useRef<HTMLElement>(null);

  // projectsChanged → 重读 projects()（新对象触发重渲染）
  useEffect(() => {
    return source.subscribe((ev) => {
      if (ev.type === "projectsChanged") setProjectsTick((n) => n + 1);
    });
  }, [source]);
  void projectsTick;
  const projects = source.projects();

  const all = source.sessions().filter((s) => !s.archived);
  const loose = all.filter((s) => !s.workspace);
  // 过滤语义：项目 id → 该项目会话；LOOSE → 未绑定；null → 全部
  const projectFiltered =
    filter === null ? all : filter === LOOSE ? loose : all.filter((s) => s.workspace === filter);
  const list = query.trim()
    ? projectFiltered.filter((s) => s.title.toLowerCase().includes(query.trim().toLowerCase()))
    : projectFiltered;
  // 项目 id → 元数据（会话 workspace 指向项目 id，显示时取名）
  const projectById = new Map(projects.map((p) => [p.id, p]));
  /** 当前过滤的显示名（新对话归属提示 + 过滤 chip）。 */
  const filterName = filter === null ? null : filter === LOOSE ? "未分组" : projectById.get(filter)?.name ?? "项目";

  // 后出现的会话行（首轮消息建会话、归档区恢复）单独入场；
  // 首屏整列由 staggerIn 接管，boot 窗口内跳过避免双份动画打架。
  const bootAt = useRef(performance.now());
  const enterRow = useCallback((el: HTMLDivElement | null) => {
    if (el && performance.now() - bootAt.current >= 1500) {
      playEnter(el, { opacity: 0, x: -6 }, { opacity: 1, x: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
    }
  }, []);

  // 菜单关闭走 gsap 退场再卸载（直接置 null 是瞬灭，开合不对称）
  const menuClosingRef = useRef(false);
  const closeMenu = useCallback(() => {
    const els = sideRef.current ? Array.from(sideRef.current.querySelectorAll<HTMLElement>(".session-menu")) : [];
    if (!els.length || !motionAllowed() || menuClosingRef.current) {
      menuClosingRef.current = false;
      setMenuFor(null);
      return;
    }
    menuClosingRef.current = true;
    // 入场 CSS fill:both 结束后仍占住样式，退场前先禁动画让 gsap 接管
    gsap.set(els, { animation: "none", pointerEvents: "none" });
    gsap.to(els, {
      opacity: 0, y: -4, scale: 0.96, transformOrigin: "right top", duration: 0.16, ease: "power2.in", overwrite: true,
      onComplete: () => { menuClosingRef.current = false; setMenuFor(null); },
    });
  }, []);

  // ⋯ 菜单点外/Esc 关闭
  useDismissal(sideRef, !!menuFor, closeMenu);

  const commitRename = (s: SessionMeta, value: string) => {
    const t = value.trim();
    if (t && t !== s.title) source.renameSession(s.id, t);
    setRenaming(null);
  };

  const doArchive = (e: ReactMouseEvent, id: string) => {
    collapseAway((e.currentTarget as HTMLElement).closest(".session-item"), () => source.archiveSession(id));
  };

  // 侧栏交错入场：导航项 → 搜索 → 分组标签 → 项目行 → 会话行
  useEffect(() => {
    const el = sideRef.current;
    if (!el) return;
    const items = el.querySelectorAll<HTMLElement>(".nav-item, .search-box, .sidebar-label, .proj-row, .session-item, .settings-row");
    staggerIn(items, { each: 0.035 });
  }, []);

  return (
    <aside className="sidebar" ref={sideRef}>
      {/* 导航项（图标 + 文字，Codex 同款四项）——新对话归属当前选中项目 */}
      <nav className="nav-list">
        <button type="button" className="nav-item" onClick={() => !busy && source.newSession(filter === null || filter === LOOSE ? undefined : filter)}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
          新对话
          {filterName && filter !== LOOSE && <span className="nav-ctx" title={`新会话归属 ${filterName}`}>{filterName}</span>}
        </button>
        <button type="button" className="nav-item" onClick={() => searchRef.current?.focus()}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          搜索
          <span className="nav-kbd">⌘K</span>
        </button>
        <button type="button" className="nav-item">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
          插件
        </button>
        <button type="button" className="nav-item">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <path d="M12 7v5l3 2" />
          </svg>
          自动化
        </button>
      </nav>

      {/* 搜索框（聚焦「搜索」时展开） */}
      <div className="sidebar-pad">
        <div className="search-box">
          <svg className="search-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input
            ref={searchRef}
            value={query}
            placeholder="搜索对话与项目"
            aria-label="搜索对话与项目"
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setQuery("");
                e.currentTarget.blur();
              }
            }}
          />
          {query ? (
            <button type="button" className="search-clear" aria-label="清除" onClick={() => setQuery("")}>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          ) : (
            <span className="search-kbd">Ctrl K</span>
          )}
        </div>
      </div>

      {/* 项目分组（上）——注册项目列表 + 添加按钮；点行过滤对话 */}
      <div className="sidebar-label project-head">
        项目
        <button type="button" className="proj-add-btn" aria-label="添加项目" title="添加项目" onClick={() => setAddOpen(true)}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </div>
      {projects.map((p) => {
        const count = all.filter((s) => s.workspace === p.id).length;
        const active = filter === p.id;
        return (
          <div
            key={p.id}
            className={"proj-row" + (active ? " active" : "")}
            title={p.path}
            role="button"
            tabIndex={0}
            aria-pressed={active}
            onClick={() => setFilter(active ? null : p.id)}
            onKeyDown={(e) => e.key === "Enter" && setFilter(active ? null : p.id)}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
            </svg>
            <span className="proj-name">{p.name}</span>
            <span className="proj-count">{count}</span>
          </div>
        );
      })}
      {/* 未分组：无归属会话的家（空则不占位；虚线文件夹 + 灰计数） */}
      {loose.length > 0 && (
        <div
          className={"proj-row loose" + (filter === LOOSE ? " active" : "")}
          title="未归属项目的对话"
          role="button"
          tabIndex={0}
          aria-pressed={filter === LOOSE}
          onClick={() => setFilter(filter === LOOSE ? null : LOOSE)}
          onKeyDown={(e) => e.key === "Enter" && setFilter(filter === LOOSE ? null : LOOSE)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
            <path d="M9 13.5h6" strokeDasharray="1.5 2.2" />
          </svg>
          <span className="proj-name">未分组</span>
          <span className="proj-count">{loose.length}</span>
        </div>
      )}
      {projects.length === 0 && loose.length === 0 && (
        <div className="proj-empty">还没有项目——点右上 + 添加</div>
      )}
      {addOpen && (
        <AddProjectDialog
          onAdd={(name, path) => source.addProject(name, path)}
          onClose={() => setAddOpen(false)}
        />
      )}

      {/* 对话分组（下）——选中项目/未分组时过滤；组头显示当前范围（可清除） */}
      <div className="sidebar-label group-head">
        对话
        {filterName && (
          <button type="button" className="group-filter" onClick={() => setFilter(null)} title="显示全部对话">
            {filterName}
            <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        )}
        <span className="group-actions" aria-hidden>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
            <path d="M3 6h18M6 12h12M10 18h4" />
          </svg>
        </span>
      </div>
      {list.slice(0, 8).map((s) => (
        <div
          key={s.id}
          ref={enterRow}
          className={"session-item" + (s.id === currentId ? " active" : "")}
          style={menuFor === s.id ? { zIndex: 30 } : undefined}
          onClick={() => !busy && renaming !== s.id && source.resumeSession(s.id)}
          title={s.title}
        >
          <div className="session-line">
            <span className={"s-dot" + (s.id === currentId && busy ? " live" : "")} aria-hidden />
            {renaming === s.id ? (
              <input
                className="session-rename"
                autoFocus
                defaultValue={s.title}
                aria-label="重命名会话"
                onClick={(e) => e.stopPropagation()}
                onPointerDown={(e) => e.stopPropagation()}
                onBlur={(e) => commitRename(s, e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    commitRename(s, e.currentTarget.value);
                  }
                  if (e.key === "Escape") {
                    e.preventDefault();
                    setRenaming(null);
                  }
                }}
              />
            ) : (
              <span className="title">{s.title}</span>
            )}
            <span className="time">{s.updatedAt}</span>
            {renaming !== s.id && (
              <button
                type="button"
                className="session-more"
                aria-label="会话操作"
                onPointerDown={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.stopPropagation();
                  if (menuFor === s.id) closeMenu();
                  else setMenuFor(s.id);
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
          {menuFor === s.id && (
            <div className="session-menu" role="menu" onPointerDown={(e) => e.stopPropagation()}>
              <button type="button" role="menuitem" onClick={() => { closeMenu(); setRenaming(s.id); }}>
                <IconPencil />
                重命名
              </button>
              <button type="button" role="menuitem" onClick={(e) => { closeMenu(); doArchive(e, s.id); }}>
                <IconArchive />
                归档
              </button>
            </div>
          )}
        </div>
      ))}
      {list.length > 8 && <div className="ws-more">Show more（{list.length - 8}）</div>}
      {list.length === 0 && query.trim() && (
        <div className="sidebar-empty">没有匹配「{query.trim()}」的对话</div>
      )}

      <div className="sidebar-footer">
        <div className="settings-row" role="button" tabIndex={0} onClick={onOpenSettings}>
          <span className="settings-label">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
            </svg>
            设置
          </span>
          <span className="ver">eabc22c</span>
        </div>
      </div>
    </aside>
  );
}
