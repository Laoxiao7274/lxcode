import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { staggerIn } from "../../shared/motion";
import { playEnter } from "../../shared/anim";
import { useDismissal } from "../../shared/popover";
import { useUpdate } from "../../shared/update";
import { busyCountsByWorkspace } from "../../shared/busy-summary";
import { LOOSE, openProject as openProjectEntry } from "../../shared/project-entry";
import { readSidebarSections, writeSidebarSections, type SidebarSectionsCollapsed } from "../../shared/sidebar-persist";
import type { AgentSource, ProjectMeta, SessionMeta } from "../../shared/types";
import { AddProjectDialog } from "./AddProjectDialog";
import { ProjectInstructionsDialog } from "./ProjectInstructionsDialog";
import { RemoveProjectDialog } from "./RemoveProjectDialog";
import { SessionRow } from "./SessionRow";

/** 「来自助手」小节标题行与行类（session-item 骨架复用——视觉与对话行同款）。
 *  行点击 = 跨模式跳转 onOpenAssistantChild(childId)：App 切助手模式 +
 *  AssistantWorkspace 直接落在该任务的子会话视图（域纪律：行是「看/跳」入口，
 *  不接 chat.* 历史装载；进的是 AssistantChildPage → assistant.childHistory）。 */
const ASSISTANT_SECTION_HEAD = "assistant-tasks-head";

/** 空列表常量：没有该分组的会话时返回同一个引用（不每次新建数组——
 *  否则「项目列表里的空分组」每次渲染都换引用，下游 memo 全部失效）。 */
const EMPTY_SESSIONS: SessionMeta[] = [];

/** 「未分组」过滤目标（无归属会话的家——不依赖真实项目 id）。
 *  导出给 App 用：启动时的默认范围可能落在「未分组」（App 的自动选中）。
 *  实体从 shared/project-entry.ts re-export（项目行交互语义的判定与常量同源）。 */
export { LOOSE } from "../../shared/project-entry";

/** 项目行的「运行中」角标：spinner +（多于一个时）×N。count<=0 不渲染。
 *  视觉沿用既有件（mset-spinner + 侧栏运行蓝 #3b82f6），不发明新视觉。 */
export function ProjectRunBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span className="proj-run" title={`${count} 个会话正在运行`} aria-label={`${count} 个会话正在运行`}>
      <span className="mset-spinner proj-run-spinner" aria-hidden />
      {count > 1 && <span className="proj-run-count">×{count}</span>}
    </span>
  );
}

/** 侧栏（Codex 2026-05 版形态）：
 *  导航项（新对话/搜索/插件/自动化）→「项目」分组（上）→「对话」分组（下）。
 *  项目区：+ 号添加；点项目行过滤对话（再点取消）；未绑定项目的会话
 *  归入「未分组」行。选中项目后「新对话」按钮带归属提示。
 *  memo（2026-12 渲染性能 Top1）：流式 delta 每帧重渲染 AppBody，侧栏的
 *  props（source/currentId/busyBySession/各回调）全部稳定身份（App 侧
 *  useCallback + busy 汇总签名 memo），没动的帧整棵子树跳过协调。 */
export const Sidebar = memo(function Sidebar({
  source,
  currentId,
  busyBySession,
  filter,
  setFilter,
  onOpenProject,
  onOpenSettings,
  agentsActive,
  onOpenAgents,
  onOpenChat,
  catalogActive,
  onOpenCatalog,
  gitActive,
  onOpenGit,
  remoteActive,
  onOpenRemote,
  onOpenAssistantChild,
}: {
  source: AgentSource;
  currentId: string;
  busyBySession: Record<string, boolean>;
  /** 对话范围（项目 id / ""=未分组）——App 持有：新对话归属提示与空态标签共用。
   *  用户拍板：**恒有范围**（启动即「未分组」；点项目行切换且不可再点取消——
   *  没有「全部」视图，列表永远只属于一个具体范围）。 */
  filter: string;
  setFilter: (f: string) => void;
  /** 新对话按钮用（归属当前范围）。 */
  onOpenChat: () => void;
  /** 点项目行/未分组行：过滤到该项目 + 切到该项目列表最上面的会话
   *  （App 侧 openProject——判定与边界在 shared/project-entry.ts）。
   *  list = 该项目（过滤后）的会话列表，顺序即侧栏展示顺序（顶=最新）。
   *  稳定身份（App 侧 useCallback）：项目行击穿 Sidebar memo 的同款纪律。 */
  onOpenProject: (workspace: string, list: readonly SessionMeta[]) => void;
  onOpenSettings: () => void;
  /** Agent 工作区标签当前激活（侧栏仅负责聚焦，不负责关闭标签）。 */
  agentsActive: boolean;
  onOpenAgents: () => void;
  /** 拓展工作区标签当前激活（工具/技能/模板/MCP 管理）。 */
  catalogActive: boolean;
  onOpenCatalog: () => void;
  /** Git 项目工作台当前激活。 */
  gitActive: boolean;
  onOpenGit: () => void;
  /** 远程访问页当前激活（局域网/樱花frp/Tailscale 模式配置）。 */
  remoteActive: boolean;
  onOpenRemote: () => void;
  /** 「来自助手」任务行点击（2026-12）：跨模式跳转——App 切助手模式 +
   *  AssistantWorkspace 直接落在该子会话视图。助手域能力缺席（演示源）时
   *  整个小节不渲染，回调不会被触发。 */
  onOpenAssistantChild: (childId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  /** 项目守则编辑器（项目行「守则」入口打开；null = 关闭）。 */
  const [insFor, setInsFor] = useState<ProjectMeta | null>(null);
  /** 移除项目确认（项目行 hover 的「移除」入口；null = 关闭）。
   *  语义 = 只解绑（不删磁盘文件、不删会话），文案见 RemoveProjectDialog。 */
  const [removeFor, setRemoveFor] = useState<ProjectMeta | null>(null);
  const [projectsTick, setProjectsTick] = useState(0); // projectsChanged 事件驱动重读
  /** 分节折叠态（2026-12 分层重构）：「项目」与「助手」两节的节头可点击收缩/
   *  展开，localStorage 记忆（sidebar-persist 的 sections 键）。单 state 一次
   *  读、落盘收口在同一个 toggle。 */
  const [collapsed, setCollapsed] = useState<SidebarSectionsCollapsed>(() => readSidebarSections());
  const toggleSection = useCallback((section: keyof SidebarSectionsCollapsed) => {
    setCollapsed((cur) => {
      const next = { ...cur, [section]: !cur[section] };
      writeSidebarSections(next);
      return next;
    });
  }, []);
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
  // 当前范围的项目被移除（本端或别的客户端）→ 退回「未分组」：否则会话列表
  // 停在一个已不存在的项目上，侧栏与空态都无从解释（多端同步的收口）。
  // projects 为空时不判（握手前/换后端清缓存那一刻列表还没回来——那不是
  // 「项目被删了」，此时重置会把用户的范围白白丢掉）。
  useEffect(() => {
    if (filter === LOOSE || filter === "") return;
    if (projects.length === 0) return;
    if (projects.some((p) => p.id === filter)) return;
    setFilter(LOOSE);
  }, [filter, projects, setFilter]);

  const allSessions = source.sessions();
  // **派生计算移出渲染体**（2026-12 渲染性能）：会话上千条时，原先渲染体里的
  // `all.filter(...)` 是「O(会话数) × 项目数」——每个项目行再扫一遍全部会话。
  // 现在按「会话列表引用」记忆化一次，按 workspace 分组预计算一次：
  //   - source.sessions() 返回**内部缓存的同一个数组**（会话列表变化才换引用，
  //     见 ws/index.ts 的 sessionsCache），所以列表变化时 useMemo 一定重算；
  //   - busy 位（busyBySession）与分组无关，只影响 busy 汇总（已单独记忆化）；
  //   - filter/query 变化只重算「当前范围」的那一小段，不再扫全部会话。
  // 陈旧数据的边界：分组结果与 `all` 同源同引用，`all` 随 allSessions 失效——
  // 新会话/归档/重命名都会换 sessionsCache 的引用（sessionsChanged 事件驱动
  // source 重读），所以不会出现「侧栏停在旧列表」。
  const all = useMemo(() => allSessions.filter((s) => !s.archived), [allSessions]);
  /** workspace → 会话（按列表原序；"" 键 = 未分组）。一次遍历。 */
  const byWorkspace = useMemo(() => {
    const map = new Map<string, SessionMeta[]>();
    for (const s of all) {
      const key = s.workspace ?? "";
      const bucket = map.get(key);
      if (bucket) bucket.push(s);
      else map.set(key, [s]);
    }
    return map;
  }, [all]);
  const loose = byWorkspace.get("") ?? EMPTY_SESSIONS;
  // 过滤语义：项目 id → 该项目会话；LOOSE（默认）→ 未绑定会话
  const projectFiltered = useMemo(
    () => (filter === LOOSE ? loose : (byWorkspace.get(filter) ?? EMPTY_SESSIONS)),
    [filter, loose, byWorkspace],
  );
  const trimmedQuery = query.trim().toLowerCase();
  const list = useMemo(
    () => (trimmedQuery ? projectFiltered.filter((s) => s.title.toLowerCase().includes(trimmedQuery)) : projectFiltered),
    [projectFiltered, trimmedQuery],
  );
  // ---- 全局搜索（跨项目）：查询命中项目名 → 直接切到该项目；命中会话标题 →
  // 「项目 · 会话」两级行，点击 = 切项目 + 开该会话（openProject 语义）。数据源
  // 就是上面的分组结果（byWorkspace + projectById），不另发请求。助手域不进
  // （搜索的是项目模式的双列表；assistant 任务子会话不进 byWorkspace 的顶层键）。
  // 结果渲染接管 sess-scroll 区（query 非空且全局命中时），空查询回原双列表。
  /** 全局命中的会话（跨所有项目，含未分组），按列表原序 = updated_at 倒序。 */
  const globalHits = useMemo(() => {
    if (!trimmedQuery) return EMPTY_SESSIONS;
    return all.filter((s) => s.title.toLowerCase().includes(trimmedQuery));
  }, [all, trimmedQuery]);
  /** 全局命中的项目（名称匹配；id 不匹配——id 不是用户口中的名字）。 */
  const projectHits = useMemo(
    () => (trimmedQuery ? projects.filter((p) => p.name.toLowerCase().includes(trimmedQuery)) : []),
    [projects, trimmedQuery],
  );
  /** 全局搜索态（结果列表替换会话区）：查询非空即接管——当前范围内没命中也
   *  显示「全局没有匹配」，不给用户「搜了个寂寞却以为只有本范围在搜」的歧义。 */
  const searchActive = trimmedQuery.length > 0;
  /** 全局搜索态的行点击（两级行同一条收口）：
   *  - 会话行：切到归属项目 + 打开**该会话**（targetId 已知——不走「第一条」
   *    判定；已是当前会话则零副作用，不重触发 resume）；
   *  - 项目行（targetId 缺省）：openProject 语义（切该项目 + 落到第一条会话；
   *    无会话只切范围空态如实；幂等判定在 project-entry）。
   *  助手域排除：搜索的是项目模式双列表，assistant 任务子会话不进。 */
  const openSearchHit = useCallback((workspace: string, targetId?: string) => {
    if (targetId) {
      setFilter(workspace);
      onOpenChat();
      if (targetId !== currentId) void source.resumeSession(targetId);
      return;
    }
    openProjectEntry({
      workspace,
      list: workspace === LOOSE ? loose : (byWorkspace.get(workspace) ?? EMPTY_SESSIONS),
      currentId,
      setFilter,
      resumeSession: (id) => { void source.resumeSession(id); },
      onOpenChat,
    });
  }, [byWorkspace, currentId, loose, onOpenChat, setFilter, source]);
  // 项目 id → 元数据（会话 workspace 指向项目 id，显示时取名）
  const projectById = useMemo(() => new Map(projects.map((p) => [p.id, p])), [projects]);
  /** 会话 id → 会话（归档区「恢复」要按 id 找它的归属范围——每次 O(n) 扫描
   *  在会话上千条时是白白的全表扫）。 */
  const sessionById = useMemo(() => new Map(all.map((s) => [s.id, s])), [all]);
  // busy 汇总（项目行「运行中」角标）：输入 =（未归档会话列表 + per-session busy，
  // 即 App 从 sessionStates 派生的同一份 busyBySession——与 TabBar 小点同源）。
  // 口径见 shared/busy-summary.ts：只算顶层会话（子会话 busy 不上 wire，无法可靠归属）。
  const busyCountByWorkspace = useMemo(
    () => busyCountsByWorkspace(all, busyBySession),
    [all, busyBySession],
  );
  /** 未分组行的运行计数（workspace 空串 = 未分组，同一份结果里的 "" 键）。 */
  const looseRun = busyCountByWorkspace[LOOSE] ?? 0;
  /** 当前范围的显示名（新对话归属提示用）。 */
  const filterName = filter === LOOSE ? "未分组" : projectById.get(filter)?.name ?? "项目";
  /** 会话行的 hover 提示：`<项目名> · <会话标题>`（未分组/项目缺席 → undefined，
   *  SessionRow 回落标题本身——不许把没有归属的会话伪装成有归属）。 */
  const sessionHint = useCallback((s: SessionMeta): string | undefined => {
    const name = s.workspace ? projectById.get(s.workspace)?.name : undefined;
    return name ? `${name} · ${s.title}` : undefined;
  }, [projectById]);

  // ---- 「来自助手」小节（2026-12）----
  // 当前范围是真实项目时拉 assistant.tasks(projectId)——run_task 派给该项目的
  // 任务子会话（updated_at 倒序）。空列表 = 该项目没有助手任务 → 小节不渲染。
  // 未分组（LOOSE）不拉：助手任务的 workspace 恒是具体项目 id。失败静默 =
  // 小节不出现（数据源缺席/后端老版本没有 assistant.tasks——不把侧栏变成
  // 错误横幅；列表是锦上添花的入口，子会话本体在助手模式里不丢）。
  const assistant = source.assistant;
  const [assistantTasks, setAssistantTasks] = useState<SessionMeta[]>(EMPTY_SESSIONS);
  useEffect(() => {
    if (!assistant || filter === LOOSE || filter === "") {
      setAssistantTasks(EMPTY_SESSIONS);
      return;
    }
    let alive = true;
    setAssistantTasks(EMPTY_SESSIONS); // 切范围先清旧项目的小节（迟到的应答不画到别的项目下）
    assistant.tasks(filter).then(
      (tasks) => { if (alive) setAssistantTasks(tasks.length ? tasks : EMPTY_SESSIONS); },
      () => { if (alive) setAssistantTasks(EMPTY_SESSIONS); },
    );
    return () => { alive = false; };
  }, [assistant, filter]);

  // Ctrl/Cmd+K → 聚焦搜索框并全选（search-kbd 角标做活）。挂在 document 的
  // keydown 上（全局快捷键——焦点在会话/时间线/设置里都要生效）；
  // input 本身聚焦时浏览器编辑键组合不受影响（Ctrl+A 全选等不拦）。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
        e.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

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
        <button type="button" className="nav-item" data-nav="remote" onClick={onOpenRemote} title="把后端暴露给其它设备（局域网/樱花frp/Tailscale）" aria-current={remoteActive ? "page" : undefined}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M12 3v6M8.5 5.5 12 3l3.5 2.5" />
            <rect x="4" y="9" width="16" height="11" rx="2" />
            <path d="M8 14h.01M12 14h.01M16 14h.01M7 17.5h10" />
          </svg>
          远程访问
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

      {/* 项目区（上分组）：标题行可点击收缩/展开（collapsed.projects，localStorage
          记忆）；行列表独立滚动（proj-scroll）——项目再多也不把下方的会话区/
          设置入口挤出视口。 */}
      <div className="sidebar-label project-head">
        <button
          type="button"
          className="section-toggle"
          aria-expanded={!collapsed.projects}
          title={collapsed.projects ? "展开项目列表" : "收起项目列表"}
          onClick={() => toggleSection("projects")}
        >
          <svg
            width="10"
            height="10"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.4"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            style={{ transform: collapsed.projects ? "rotate(-90deg)" : undefined, transition: "transform 140ms" }}
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
        项目
        <button type="button" className="proj-add-btn" aria-label="添加项目" title="添加项目" onClick={() => setAddOpen(true)}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </div>
      {collapsed.projects ? null : (
      <div className="proj-scroll">
      {projects.map((p) => {
        // 该项目的会话列表（分组结果直接取——不再每个项目扫一遍全部会话；
        // 顺序即列表原序 = 侧栏展示顺序 = 切换目标顺序）。
        const projList = byWorkspace.get(p.id) ?? EMPTY_SESSIONS;
        const count = projList.length;
        const active = filter === p.id;
        const runCount = busyCountByWorkspace[p.id] ?? 0;
        return (
          <div
            key={p.id}
            className={"proj-row" + (active ? " active" : "")}
            title={runCount > 0 ? `${p.path}（${runCount} 个会话正在运行）` : p.path}
            role="button"
            tabIndex={0}
            aria-pressed={active}
            aria-label={runCount > 0 ? `${p.name}，${runCount} 个会话正在运行` : undefined}
            onClick={() => { onOpenProject(p.id, projList); }}
            onKeyDown={(e) => { if (e.key === "Enter") onOpenProject(p.id, projList); }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
            </svg>
            <span className="proj-name">{p.name}</span>
            {/* 该项目下有会话在跑（顶层口径，见 busyCountsByWorkspace） */}
            <ProjectRunBadge count={runCount} />
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
            {/* 移除入口（hover 出现）：只从项目列表解绑，不删磁盘文件、不删会话 */}
            <button
              type="button"
              className="proj-remove-btn"
              data-remove={p.id}
              title="移除项目（不会删除磁盘文件与已有会话）"
              aria-label={`移除项目 ${p.name}`}
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => { e.stopPropagation(); setRemoveFor(p); }}
            >
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
            <span className="proj-count">{count}</span>
          </div>
        );
      })}
      {/* 未分组：无归属会话的家。**常驻显示**（它是启动时的默认范围——
          范围恒有选中，没有「全部」可退；会话为 0 时它仍代表当前范围）。 */}
      <div
        className={"proj-row loose" + (filter === LOOSE ? " active" : "")}
        title={looseRun > 0 ? `未归属项目的对话（${looseRun} 个会话正在运行）` : "未归属项目的对话"}
        role="button"
        tabIndex={0}
        aria-pressed={filter === LOOSE}
        aria-label={looseRun > 0 ? `未分组，${looseRun} 个会话正在运行` : undefined}
        onClick={() => { onOpenProject(LOOSE, loose); }}
        onKeyDown={(e) => { if (e.key === "Enter") onOpenProject(LOOSE, loose); }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          <path d="M9 13.5h6" strokeDasharray="1.5 2.2" />
        </svg>
        <span className="proj-name">未分组</span>
        <ProjectRunBadge count={looseRun} />
        <span className="proj-count">{loose.length}</span>
      </div>
      {projects.length === 0 && loose.length === 0 && (
        <div className="proj-empty">还没有项目——点右上 + 添加</div>
      )}
      </div>
      )}
      {addOpen && (
        <AddProjectDialog
          onAdd={async (name, path) => {
            // 创建成功 → 与「点项目行」等价的进入动作（openProject：setFilter +
            // 回聊天页；新项目无会话，project-entry 的判定不会触发 resume）+
            // 立即在该项目下开一条新对话。失败向上抛（对话框内显示原因，不关框）。
            const meta = await source.addProject(name, path);
            onOpenProject(meta.id, EMPTY_SESSIONS);
            void source.newSession(meta.id);
            return meta;
          }}
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
      {removeFor && (
        <RemoveProjectDialog
          project={removeFor}
          sessionCount={(byWorkspace.get(removeFor.id) ?? EMPTY_SESSIONS).length}
          onRemove={async () => {
            await source.removeProject(removeFor.id);
            // 移除的正是当前筛选范围 → 退回「未分组」（否则列表停在一个
            // 已不存在的项目上，侧栏与空态都无从解释）
            if (filter === removeFor.id) setFilter(LOOSE);
          }}
          onClose={() => setRemoveFor(null)}
        />
      )}

      {/* 对话分组（中）——列表恒属于当前范围（项目 / 未分组）；范围在项目区
          用选中态表达，这里不再放可清除的 chip（没有「全部」可退）。
          会话列表独立滚动（sess-scroll）：全部会话都可滚到，不再截断前 8 条。
          节名改「项目」（2026-12 用户拍板⑤：非助手节会话都归属项目/未分组，
          原名「对话」与节内容不符）。 */}
      <div className="sidebar-label group-head">
        项目
        <span className="group-actions" aria-hidden>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
            <path d="M3 6h18M6 12h12M10 18h4" />
          </svg>
        </span>
      </div>
      <div className="sess-scroll">
      {searchActive ? (
        // 全局搜索态：结果列表替换双列表（项目区也换命中项目行——见下方
        // proj-scroll 内的分支）。行 =「项目名 · 会话标题」两级，点击 = 切项目
        // + 打开该会话；当前范围内无命中也走这里（全局口径，不给「只有本范围
        // 在搜」的歧义）。
        <>
          {projectHits.map((p) => (
            <div
              key={p.id}
              className="proj-row search-hit"
              role="button"
              tabIndex={0}
              title={p.path}
              onClick={() => openSearchHit(p.id)}
              onKeyDown={(e) => { if (e.key === "Enter") openSearchHit(p.id); }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
              </svg>
              <span className="proj-name">{p.name}</span>
              <span className="proj-count">{(byWorkspace.get(p.id) ?? EMPTY_SESSIONS).length}</span>
            </div>
          ))}
          {globalHits.map((s) => {
            const ws = s.workspace ?? "";
            const wsName = ws ? projectById.get(ws)?.name : undefined;
            return (
              <div
                key={s.id}
                className={"session-item search-hit" + (s.id === currentId ? " active" : "")}
                role="button"
                tabIndex={0}
                title={wsName ? `${wsName} · ${s.title}` : s.title}
                aria-label={wsName ? `${wsName} · ${s.title}` : s.title}
                onClick={() => openSearchHit(ws, s.id)}
                onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openSearchHit(ws, s.id); } }}
              >
                <div className="session-line">
                  <span className={"s-dot" + (busyBySession[s.id] ? " live" : "")} aria-hidden />
                  {/* 两级行：`项目名 · 会话标题`（未分组显示标题本身——不伪装归属） */}
                  <span className="title">{wsName ? `${wsName} · ${s.title}` : s.title}</span>
                  {busyBySession[s.id] && <span className="mset-spinner session-spinner" aria-hidden />}
                  <span className="time">{s.updatedAt}</span>
                </div>
              </div>
            );
          })}
          {projectHits.length === 0 && globalHits.length === 0 && (
            <div className="sidebar-empty">没有匹配「{query.trim()}」的项目或对话</div>
          )}
        </>
      ) : (
        list.map((s) => (
        <SessionRow
          key={s.id}
          session={s}
          current={s.id === currentId}
          busy={Boolean(busyBySession[s.id])}
          renaming={renaming === s.id}
          menuOpen={menuFor === s.id}
          hint={sessionHint(s)}
          onOpenMenu={setMenuFor}
          onCloseMenu={() => setMenuFor(null)}
          onStartRename={setRenaming}
          onRename={commitRename}
          onArchive={(id) => source.archiveSession(id)}
          onResume={(id) => {
            // 归属范围从已建好的索引取（不再扫一遍会话列表）
            setFilter(sessionById.get(id)?.workspace ?? LOOSE);
            void source.resumeSession(id);
            onOpenChat();
          }}
          enterRow={enterRow}
        />
        ))
      )}
      </div>

      {/* 助手节（2026-12 分层重构：移到会话区**下方**——用户主诉求是位置感：
          项目/会话主域在上，助手任务在下；节头可点击收缩/展开（记忆态同项目节）。
          小节仍只在当前项目有助手派的任务子会话时出现（空分组不渲染）。
          行点击 = 跨模式跳转到助手模式的子会话视图（域纪律：这里不接 chat.*
          历史装载——assistant 任务的历史走 assistant.childHistory，在助手域内
          闭环）。 */}
      {assistantTasks.length > 0 && (
        <div className={ASSISTANT_SECTION_HEAD}>
          <div className="sidebar-label group-head assistant-tasks-label">
            <button
              type="button"
              className="section-toggle"
              aria-expanded={!collapsed.assistants}
              title={collapsed.assistants ? "展开助手任务" : "收起助手任务"}
              onClick={() => toggleSection("assistants")}
            >
              <svg
                width="10"
                height="10"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.4"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
                style={{ transform: collapsed.assistants ? "rotate(-90deg)" : undefined, transition: "transform 140ms" }}
              >
                <path d="m6 9 6 6 6-6" />
              </svg>
            </button>
            助手
          </div>
          {collapsed.assistants ? null : assistantTasks.map((t) => (
            <div
              key={t.id}
              className="session-item assistant-task-row"
              role="button"
              tabIndex={0}
              title={`助手派的任务：${t.title}`}
              aria-label={`来自助手的任务：${t.title}${t.running ? "（正在运行）" : ""}`}
              onClick={() => onOpenAssistantChild(t.id)}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpenAssistantChild(t.id); } }}
            >
              <div className="session-line">
                {/* 来源徽标（2026-12 ④整齐化）：统一放**行首图标位**（原来在标题
                    之后，点/时间都被它挤压错位）——与会话行的 s-dot 同位，三节
                    （项目会话/助手任务/全局搜索）行首对齐。 */}
                <span className="assistant-src-pill" aria-hidden>助手</span>
                <span className="title">{t.title}</span>
                {t.running && <span className="mset-spinner session-spinner" aria-hidden />}
                <span className="time">{t.updatedAt}</span>
              </div>
            </div>
          ))}
        </div>
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
});
