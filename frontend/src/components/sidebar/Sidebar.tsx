import { useCallback, useEffect, useRef, useState } from "react";
import { staggerIn } from "../../shared/motion";
import { playEnter } from "../../shared/anim";
import { useDismissal } from "../../shared/popover";
import { useUpdate } from "../../shared/update";
import type { AgentSource, ProjectMeta } from "../../shared/types";
import { AddProjectDialog } from "./AddProjectDialog";
import { ProjectInstructionsDialog } from "./ProjectInstructionsDialog";
import { SessionRow } from "./SessionRow";

/** 「未分组」过滤目标（无归属会话的家——不依赖真实项目 id）。
 *  导出给 App 用：启动时的默认范围可能落在「未分组」（App 的自动选中）。 */
export const LOOSE = "";

/** 侧栏（Codex 2026-05 版形态）：
 *  导航项（新对话/搜索/插件/自动化）→「项目」分组（上）→「对话」分组（下）。
 *  项目区：+ 号添加；点项目行过滤对话（再点取消）；未绑定项目的会话
 *  归入「未分组」行。选中项目后「新对话」按钮带归属提示。 */
export function Sidebar({
  source,
  currentId,
  busyBySession,
  filter,
  setFilter,
  onOpenSettings,
  agentsActive,
  onOpenAgents,
  onOpenChat,
  catalogActive,
  onOpenCatalog,
  gitActive,
  onOpenGit,
}: {
  source: AgentSource;
  currentId: string;
  busyBySession: Record<string, boolean>;
  /** 对话范围（项目 id / ""=未分组）——App 持有：新对话归属提示与空态标签共用。
   *  用户拍板：**恒有范围**（启动即「未分组」；点项目行切换且不可再点取消——
   *  没有「全部」视图，列表永远只属于一个具体范围）。 */
  filter: string;
  setFilter: (f: string) => void;
  onOpenSettings: () => void;
  /** Agent 工作区标签当前激活（侧栏仅负责聚焦，不负责关闭标签）。 */
  agentsActive: boolean;
  onOpenAgents: () => void;
  /** 新对话/恢复会话回到固定的「聊天」工作区标签。 */
  onOpenChat: () => void;
  /** 拓展工作区标签当前激活（工具/技能/模板/MCP 管理）。 */
  catalogActive: boolean;
  onOpenCatalog: () => void;
  /** Git 项目工作台当前激活。 */
  gitActive: boolean;
  onOpenGit: () => void;
}) {
  const [query, setQuery] = useState("");
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  /** 项目守则编辑器（项目行「守则」入口打开；null = 关闭）。 */
  const [insFor, setInsFor] = useState<ProjectMeta | null>(null);
  const [projectsTick, setProjectsTick] = useState(0); // projectsChanged 事件驱动重读
  const searchRef = useRef<HTMLInputElement>(null);
  const { phase: updPhase } = useUpdate();
  const hasUpdate = updPhase === "available" || updPhase === "downloading" || updPhase === "ready";
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
  // 过滤语义：项目 id → 该项目会话；LOOSE（默认）→ 未绑定会话
  const projectFiltered = filter === LOOSE ? loose : all.filter((s) => s.workspace === filter);
  const list = query.trim()
    ? projectFiltered.filter((s) => s.title.toLowerCase().includes(query.trim().toLowerCase()))
    : projectFiltered;
  // 项目 id → 元数据（会话 workspace 指向项目 id，显示时取名）
  const projectById = new Map(projects.map((p) => [p.id, p]));
  /** 当前范围的显示名（新对话归属提示用）。 */
  const filterName = filter === LOOSE ? "未分组" : projectById.get(filter)?.name ?? "项目";

  // 后出现的会话行（首轮消息建会话、归档区恢复）单独入场；
  // 首屏整列由 staggerIn 接管，boot 窗口内跳过避免双份动画打架。
  const bootAt = useRef(performance.now());
  const enterRow = useCallback((el: HTMLDivElement | null) => {
    if (el && performance.now() - bootAt.current >= 1500) {
      playEnter(el, { opacity: 0, x: -6 }, { opacity: 1, x: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
    }
  }, []);

  // ⋯ 菜单点外/Esc 关闭（退场动画在 SessionRow 内部）
  useDismissal(sideRef, !!menuFor, () => setMenuFor(null));

  const commitRename = (s: { id: string; title: string }, value: string) => {
    const t = value.trim();
    if (t && t !== s.title) source.renameSession(s.id, t);
    setRenaming(null);
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
      {/* 主导航（图标 + 文字）——新对话归属当前选中项目 */}
      <nav className="nav-list">
        <button type="button" className="nav-item" onClick={() => { void source.newSession(filter); onOpenChat(); }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
          新对话
          {filterName && filter !== LOOSE && <span className="nav-ctx" title={`新会话归属 ${filterName}`}>{filterName}</span>}
        </button>
        <button
          type="button"
          className={"nav-item" + (agentsActive ? " on" : "")}
          data-nav="agents"
          onClick={onOpenAgents}
          title="组装、注册与调度 Agent"
          aria-current={agentsActive ? "page" : undefined}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="7" y="7" width="10" height="10" rx="2" />
            <path d="M4 10v4M20 10v4M10 4h4M10 20h4" />
          </svg>
          Agents
        </button>
        <button
          type="button"
          className={"nav-item" + (catalogActive ? " on" : "")}
          data-nav="catalog"
          onClick={onOpenCatalog}
          title="工具、技能、模板与 MCP 的拓展"
          aria-current={catalogActive ? "page" : undefined}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
          拓展
        </button>
        <button
          type="button"
          className={"nav-item" + (gitActive ? " on" : "")}
          data-nav="git"
          onClick={onOpenGit}
          title="项目分支、变更与会话工作树"
          aria-current={gitActive ? "page" : undefined}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="6" cy="6" r="2" />
            <circle cx="18" cy="6" r="2" />
            <circle cx="6" cy="18" r="2" />
            <path d="M6 8v8M18 8a6 6 0 0 1-6 6H8" />
          </svg>
          Git 管理
        </button>
        <button type="button" className="nav-item">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <path d="M12 7v5l3 2" />
          </svg>
          自动化
        </button>
      </nav>

      {/* 搜索框（常驻——搜索会话与项目） */}
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
            onClick={() => { if (!active) setFilter(p.id); onOpenChat(); }}
            onKeyDown={(e) => { if (e.key === "Enter") { if (!active) setFilter(p.id); onOpenChat(); } }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
            </svg>
            <span className="proj-name">{p.name}</span>
            {/* 项目守则入口（hover 出现）：项目级「自定义指令」= 项目根 AGENTS.md */}
            <button
              type="button"
              className="proj-ins-btn"
              data-ins={p.id}
              title="项目守则（AGENTS.md）——该项目的每个会话每轮现读进提示词"
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => { e.stopPropagation(); setInsFor(p); }}
            >
              守则
            </button>
            <span className="proj-count">{count}</span>
          </div>
        );
      })}
      {/* 未分组：无归属会话的家。**常驻显示**（它是启动时的默认范围——
          范围恒有选中，没有「全部」可退；会话为 0 时它仍代表当前范围）。 */}
      <div
        className={"proj-row loose" + (filter === LOOSE ? " active" : "")}
        title="未归属项目的对话"
        role="button"
        tabIndex={0}
        aria-pressed={filter === LOOSE}
        onClick={() => { if (filter !== LOOSE) setFilter(LOOSE); onOpenChat(); }}
        onKeyDown={(e) => { if (e.key === "Enter") { if (filter !== LOOSE) setFilter(LOOSE); onOpenChat(); } }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          <path d="M9 13.5h6" strokeDasharray="1.5 2.2" />
        </svg>
        <span className="proj-name">未分组</span>
        <span className="proj-count">{loose.length}</span>
      </div>
      {projects.length === 0 && loose.length === 0 && (
        <div className="proj-empty">还没有项目——点右上 + 添加</div>
      )}
      {addOpen && (
        <AddProjectDialog
          onAdd={(name, path) => source.addProject(name, path)}
          onClose={() => setAddOpen(false)}
        />
      )}
      {insFor && (
        <ProjectInstructionsDialog
          source={source}
          project={insFor}
          onClose={() => setInsFor(null)}
        />
      )}

      {/* 对话分组（下）——列表恒属于当前范围（项目 / 未分组）；范围在项目区
          用选中态表达，这里不再放可清除的 chip（没有「全部」可退） */}
      <div className="sidebar-label group-head">
        对话
        <span className="group-actions" aria-hidden>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
            <path d="M3 6h18M6 12h12M10 18h4" />
          </svg>
        </span>
      </div>
      {list.slice(0, 8).map((s) => (
        <SessionRow
          key={s.id}
          session={s}
          current={s.id === currentId}
          busy={Boolean(busyBySession[s.id])}
          renaming={renaming === s.id}
          menuOpen={menuFor === s.id}
          onOpenMenu={setMenuFor}
          onCloseMenu={() => setMenuFor(null)}
          onStartRename={setRenaming}
          onRename={commitRename}
          onArchive={(id) => source.archiveSession(id)}
          onReleaseWorktree={(id) => source.releaseWorktree(id)}
          onResume={(id) => {
            setFilter(source.sessions().find((session) => session.id === id)?.workspace ?? LOOSE);
            void source.resumeSession(id);
            onOpenChat();
          }}
          enterRow={enterRow}
        />
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
            {hasUpdate && <span className="settings-upd-dot" title="有新版本可用" aria-label="有新版本可用" />}
          </span>
          <span className="ver">{hasUpdate ? "可更新" : "eabc22c"}</span>
        </div>
      </div>
    </aside>
  );
}
