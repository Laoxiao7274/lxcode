import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "./shared/motion";
import {
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusWorkspacePage,
  type WorkspacePage,
  type WorkspaceView,
} from "./shared/workspace-tabs";
import { getAgentSource } from "./agent";
import { useAgent, type ThreadBlock } from "./shared/store";
import { beginEdit, canRewind, planRewind, type EditDraft } from "./shared/blocks";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
import { GitWorkbenchPage } from "./components/git/GitWorkbenchPage";
import { Topbar } from "./components/topbar";
import { Sidebar, LOOSE } from "./components/sidebar";
import { Thread } from "./components/thread";
import { Composer, type ComposerDraft } from "./components/composer";
import type { SlashCommand } from "./components/composer/SlashPalette";
import { TabBar } from "./components/topbar/TabBar";
import { SettingsPanel } from "./components/settings";
import { Button } from "./components/form";
import { OutlinePanel } from "./components/panels/OutlinePanel";
import { outlineItems } from "./components/panels/outline";
import { SubAgentPanel } from "./components/panels/SubAgentPanel";
import { dispatchItems } from "./components/panels/dispatch-list";
import { SettingsProvider, useSettings } from "./shared/settings";
import { ConnectionsProvider } from "./shared/connections";
import { UpdateProvider } from "./shared/update";
import { SearchAdminProvider } from "./shared/search-admin";
import { JobsProvider } from "./shared/jobs-admin";
import { UpdateToast } from "./components/update/UpdateToast";
import type { AgentSource, SendOptions, SessionMeta } from "./shared/types";

export default function App() {
  const source = useMemo(() => getAgentSource(), []);
  return (
    <SettingsProvider source={source}>
      <AgentsProvider source={source}>
        <ConnectionsProvider>
          <UpdateProvider>
            <SearchAdminProvider source={source}>
              {/* 后台任务域：时间线卡片与顶栏面板共用同一份任务清单 */}
              <JobsProvider source={source}>
                <AppBody source={source} />
                <UpdateToast />
              </JobsProvider>
            </SearchAdminProvider>
          </UpdateProvider>
        </ConnectionsProvider>
      </AgentsProvider>
    </SettingsProvider>
  );
}

function AppBody({ source }: { source: AgentSource }) {
  const { state, sessionStates, send, resolve, clearError, reportError } = useAgent(source);
  const { settings, providers } = useSettings();
  const { activeAgentId } = useAgents();
  /** 工作区页签与会话标签分开；未关闭的工作区页面保持挂载以保留表单/视图状态。 */
  const [workspaceState, setWorkspaceState] = useState(createWorkspaceTabs);
  const workspaceStateRef = useRef(workspaceState);
  workspaceStateRef.current = workspaceState;
  const view = workspaceState.active;
  /** 对话范围（项目 id / ""=未分组）——App 持有：侧栏过滤、「新对话」归属、
   *  空态项目标签三处共用。用户拍板：**恒有范围**（启动即「未分组」，点项目行
   *  切换且不可取消——没有「全部」视图）。 */
  const [filter, setFilter] = useState<string>(LOOSE);
  const [gitProjectId, setGitProjectId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  /** 右侧「已发送消息」大纲的开合：**纯 UI 状态**（不进会话历史、不落库、切会话不
   *  清——它只是当前窗口的查看方式）。 */
  const [outlineOpen, setOutlineOpen] = useState(true);

  const openWorkspace = useCallback((page: WorkspacePage) => {
    const next = focusWorkspacePage(workspaceStateRef.current, page);
    workspaceStateRef.current = next;
    setWorkspaceState(next);
  }, []);
  const closeWorkspace = useCallback((page: WorkspacePage): WorkspaceView => {
    const next = closeWorkspacePage(workspaceStateRef.current, page);
    workspaceStateRef.current = next;
    setWorkspaceState(next);
    return next.active;
  }, []);
  const openAgents = useCallback(() => openWorkspace("agents"), [openWorkspace]);
  const openCatalog = useCallback(() => openWorkspace("catalog"), [openWorkspace]);
  const openGit = useCallback(() => {
    const projects = source.projects();
    setGitProjectId((current) => current || (filter && projects.some((project) => project.id === filter) ? filter : projects[0]?.id ?? ""));
    openWorkspace("git");
  }, [filter, openWorkspace, source]);
  const backToChat = useCallback(() => {
    const next = focusChatTab(workspaceStateRef.current);
    workspaceStateRef.current = next;
    setWorkspaceState(next);
  }, []);
  const focusSession = (id: string) => {
    setFilter(source.sessions().find((session) => session.id === id)?.workspace ?? LOOSE);
    void source.resumeSession(id);
    backToChat();
  };

  // live 写失败桥（AgentsProvider 的乐观更新 WS 调用失败 → 一次性提示）
  useEffect(() => {
    const on = (e: Event) => reportError(String((e as CustomEvent<string>).detail ?? ""));
    window.addEventListener("lx-operation-error", on);
    return () => window.removeEventListener("lx-operation-error", on);
  }, [reportError]);

  const currentId = state.currentId;
  const busyBySession = useMemo(
    () => Object.fromEntries(Object.entries(sessionStates).map(([id, session]) => [id, session.busy])),
    [sessionStates],
  );
  const projects = source.projects();
  useEffect(() => {
    if (view === "git" && !gitProjectId && projects.length > 0) setGitProjectId(projects[0].id);
  }, [view, gitProjectId, projects]);

  // 当前范围的项目名（「未分组」→ 空态不标——无归属不需要声明）
  const filterProjectName =
    filter === LOOSE ? undefined : projects.find((p) => p.id === filter)?.name;

  // 发送时携带请求级参数：effort 只在当前模型声明推理能力时上帧（后端
  // 能力门控会丢弃不匹配档位，不带上帧更诚实）；approval 恒带当前设置；
  // agent = 当前选用的 Agent（主 Agent 也显式携带——后端按名单语境跑）。
  const sendWithOptions = useCallback((text: string) => {
    const current = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
    const opts: SendOptions = { approval: settings.approval, agent: activeAgentId };
    if (current && current.efforts.length > 0) opts.effort = settings.effort;
    if (currentId) send(currentId, text, opts);
  }, [providers, settings.model, settings.effort, settings.approval, send, activeAgentId, currentId]);

  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void source.confirm(currentId, id, allow)
      .then(() => resolve(currentId, id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, currentId]);

  // 手动压缩：请求类失败进一次性提示（不动 blocks）；没有可压收益时给一句人话
  // 原因（不是错误——历史还太短是正常态）。压缩成功由 chat.compacted 事件渲染标记块。
  const handleCompact = useCallback(async () => {
    try {
      const res = await source.compact(currentId);
      if (!res.compacted) reportError(res.reason ?? "没有可压缩的历史");
    } catch (e) {
      reportError(`压缩失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [source, reportError, currentId]);

  // ---- 用户气泡的三个动作（复制在 Block 内自足；这里管编辑与撤回）----

  /** 输入框草稿注入（撤回/编辑把原文放回输入框）。id 单调递增——同一条消息连续
   *  注入两次也要重新写入（按文本比较的话第二次是 no-op，用户看到"点了没反应"）。 */
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const draftIdRef = useRef(0);
  const injectDraft = useCallback((text: string) => {
    draftIdRef.current += 1;
    setDraft({ id: draftIdRef.current, text });
  }, []);
  /** 编辑态：只记锚点，**历史一个字都不动**——真正的撤回推迟到下次发送前。 */
  const [editDraft, setEditDraft] = useState<EditDraft | null>(null);
  /** 最新时间线的引用（撤回要按锚点取原文）。**刻意用 ref 而不是把 state.blocks
   *  写进 handleRewind 的依赖**：依赖它 = 每个流式 delta 都换一次回调身份，而
   *  Block 是 memo 的（onRewind 变了 → 整屏用户气泡每帧重渲染一遍）。 */
  const blocksRef = useRef(state.blocks);
  blocksRef.current = state.blocks;

  // 撤回：历史立刻清空（由 source 的乐观 rewound 事件驱动归约器截断——两个实现
  // 走同一条路径），原文回到输入框。**失败时 source 已经用后端真相对齐**（重放
  // 历史把被乐观删掉的块拿回来），这里只负责把失败说给用户听。
  const handleRewind = useCallback((block: ThreadBlock) => {
    if (!canRewind(block)) {
      reportError("这条消息来自旧版后端（没有 seq），无法撤回");
      return;
    }
    // 文本取自纯函数给出的撤回计划（与归约器同一份判定）——文本与截断不可能对不上
    const plan = planRewind(blocksRef.current, block.seq);
    if (!plan) {
      reportError("这条消息已经不在当前对话里了（可能已被撤回）");
      return;
    }
    injectDraft(plan.text);
    setEditDraft(null); // 撤回之后没有"编辑中"这回事：历史已经清了，没什么可取消
    void source.rewind(currentId, block.seq).catch((e) => {
      reportError(`撤回失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [source, currentId, injectDraft, reportError]);

  // 编辑：只把原文放回输入框并进入编辑态——**不动历史**。编辑是"我可能改主意"，
  // 一点就把后面的对话毁掉是不可接受的；真正的撤回推迟到下次发送前。
  const handleEdit = useCallback((block: ThreadBlock) => {
    const d = beginEdit(block);
    if (!d) {
      reportError("这条消息来自旧版后端（没有 seq），无法编辑");
      return;
    }
    setEditDraft(d);
    injectDraft(d.text);
  }, [injectDraft, reportError]);

  // 取消编辑：退出编辑态。输入框里的文本**留着**（用户可能还想发）——取消只是
  // 撤回"这条是编辑"的语义，下次发送就是一条普通的新消息。
  const handleCancelEdit = useCallback(() => setEditDraft(null), []);

  // 切会话必须退出编辑态：编辑锚点（seq）只在它所属的那个会话里有意义，带着它
  // 切走再发送会去撤回**另一个会话**的那条消息（静默删错历史——比不生效坏得多）。
  useEffect(() => {
    setEditDraft(null);
  }, [currentId]);

  // 发送：编辑态下**先撤回再发**（这是"编辑"真正生效的时刻）。撤回失败就不发——
  // 历史没清干净就发，新消息会接在被编辑那条的后面，等于改了个寂寞；文本还给
  // 用户重试（Composer 的 submit 已经先清了输入框）。
  const handleSend = useCallback((text: string) => {
    const editing = editDraft;
    setEditDraft(null);
    if (!editing) {
      sendWithOptions(text);
      return;
    }
    void source.rewind(currentId, editing.seq)
      .then(() => sendWithOptions(text))
      .catch((e) => {
        reportError(`撤回失败，未发送: ${e instanceof Error ? e.message : String(e)}`);
        injectDraft(text);
      });
  }, [editDraft, source, currentId, sendWithOptions, reportError, injectDraft]);

  // ---- 右侧「已发送消息」大纲 ----
  // 条目来自纯函数（只取 user 块——提示条 notice 在历史里也是 user 角色消息，但它
  // 不是用户说的话，列进「我发过的消息」是错的）。判定与截断都在 panels/outline.ts。
  const outline = useMemo(() => outlineItems(state.blocks), [state.blocks]);
  /** 最近跳转的那条（高亮跟随点击）。它可能已经不在列表里（撤回/切会话），
   *  所以取用时再确认一次存在性。 */
  const [jumpedUid, setJumpedUid] = useState<number | null>(null);
  const lastUserUid = outline.length > 0 ? outline[outline.length - 1].uid : null;
  const activeOutlineUid = jumpedUid !== null && outline.some((item) => item.uid === jumpedUid) ? jumpedUid : lastUserUid;

  // ---- 右侧「子 Agent 执行」面板 ----
  // 与大纲**共用同一个 jumpedUid**：两个面板都只是「跳到时间线某张卡」的入口，
  // 高亮跟随最近一次跳转（条目可能已被撤回/切会话清掉，所以取用时再确认一次存在性）。
  // 默认高亮最近一次派发——用户最关心的通常是刚发出去那一个跑到哪了。
  const dispatches = useMemo(() => dispatchItems(state.blocks), [state.blocks]);
  const lastDispatchUid = dispatches.length > 0 ? dispatches[dispatches.length - 1].uid : null;
  const activeDispatchUid = jumpedUid !== null && dispatches.some((item) => item.uid === jumpedUid) ? jumpedUid : lastDispatchUid;
  const targetTimerRef = useRef<number | null>(null);
  /** 跳到某条已发送消息：这里**只记目标**，滚动与高亮交给 Thread。
   *
   *  为什么不在 App 里滚：线程是**窗口化渲染**（只挂底部若干块），目标可能根本没
   *  挂上 DOM——在这里按 data-uid 找不到元素就静默变成 no-op，而长会话恰恰是最需要
   *  跳转的场景（用户点大纲第一条却毫无反应）。扩窗要改 Thread 自己的 windowSize，
   *  所以整跳归它：先撑窗到包含目标，等元素真的挂上再滚 + 高亮。
   *  寻址仍按 data-uid **精确匹配**——不许按文本找元素（同一句话发两次会命中错的那条）。 */
  const handleOutlineJump = useCallback((uid: number) => {
    setJumpedUid(uid);
  }, []);

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: SessionMeta) => s.id === currentId)?.title ?? "任务";

  // 壳环境（Electron）= 真实窗口；浏览器 = 保留模拟壳（窗口模拟一层的差异，
  // 内部布局完全一致——同组件，不再两份 JSX）
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
  // 操作提示条：请求级失败的一次性提示。**不复用 .error-block**——那条是
  // 时间线内的行内文本（Block/SettingsPanel 也在用），给它加底色会连带改掉
  // 那两处的排版。这里要的是一条独立的、可关闭的提示条（与 .job-notice 同款
  // 形态：左缘竖线 + 圆角 + 全宽），只是走危险色。
  const errorNotice = state.operationError && (
    <div className="op-notice" role="alert">
      <span className="op-notice-icon" aria-hidden>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          <path d="M12 9v4" />
          <path d="M12 17h.01" />
        </svg>
      </span>
      <span className="op-notice-text">{state.operationError}</span>
      {/* 关闭走表单套件（.fd-btn-g），不裸写原生 button——裸写就是 OS 默认皮肤 */}
      <Button className="op-notice-close" onClick={clearError}>关闭</Button>
    </div>
  );

  // 斜杠命令集（命令面板）：页面导航。后端化时同一面板接会话/工具域
  // 命令（/resume /compact …）与选择器聚焦。
  const slashCommands: SlashCommand[] = useMemo(() => [
    { name: "new", desc: "开始新对话", run: () => { void source.newSession(filter); backToChat(); } },
    { name: "compact", desc: "压缩早期历史（腾出上下文）", run: () => { void handleCompact(); } },
    { name: "agents", desc: "打开 Agent 名单与组装", run: openAgents },
    { name: "catalog", desc: "打开拓展（工具/技能/模板/MCP）", run: openCatalog },
    { name: "settings", desc: "打开设置", run: () => setSettingsOpen(true) },
  ], [source, filter, openAgents, openCatalog, handleCompact]);

  const renderView = (activeView: WorkspaceView): ReactNode => {
    if (activeView === "chat") {
      // 左聊天列 + 右大纲栏。分栏而不是把面板浮在时间线上：面板要独立滚动，
      // 且不许把聊天列挤成 0 宽（聊天列 flex:1，面板固定宽度——见 panels.css）。
      return (
        <div className="chat-split">
          <div className="chat-main">
            {errorNotice}
            <div className="thread-scroll">
              <Thread state={state} onConfirm={handleConfirm} onSuggestion={handleSend} projectName={filterProjectName} onEdit={handleEdit} onRewind={handleRewind} revealUid={jumpedUid} />
            </div>
            <Composer
              busy={state.busy}
              todos={state.todos}
              context={state.context}
              onSend={handleSend}
              onCancel={() => source.cancel(currentId)}
              onCompact={() => { void handleCompact(); }}
              commands={slashCommands}
              draft={draft}
              editing={editDraft !== null}
              onCancelEdit={handleCancelEdit}
            />
          </div>
          <aside className="outline-aside" data-open={outlineOpen ? "true" : "false"}>
            <div className="outline-head">
              {outlineOpen && <span className="outline-title">已发送消息</span>}
              {/* 开合按钮走表单套件（裸 button = OS 默认灰皮） */}
              <Button
                className="outline-toggle"
                aria-expanded={outlineOpen}
                aria-label={outlineOpen ? "收起大纲" : "展开大纲"}
                title={outlineOpen ? "收起大纲" : "展开大纲"}
                onClick={() => setOutlineOpen((open) => !open)}
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <path d="m15 18-6-6 6-6" />
                </svg>
              </Button>
            </div>
            {outlineOpen && <OutlinePanel items={outline} activeUid={activeOutlineUid} onJump={handleOutlineJump} />}
            {/* 第二个面板（子 Agent 执行）与大纲同栏、一条分隔线分节——不做页签系统：
             *  两个面板各自独立滚动，用户扫一眼就能同时看到「我说过什么」与「子 Agent 跑到哪」。
             *  跳转复用同一个 handleOutlineJump（扩窗 + 滚 + 高亮都在 Thread 里，面板不碰 DOM）。 */}
            {outlineOpen && (
              <>
                <div className="aside-sep" />
                <div className="outline-head aside-sub-head">
                  <span className="outline-title">子 Agent 执行</span>
                </div>
                <SubAgentPanel items={dispatches} activeUid={activeDispatchUid} onJump={handleOutlineJump} />
              </>
            )}
          </aside>
        </div>
      );
    }
    if (activeView === "agents") return <AgentsPage />;
    if (activeView === "catalog") return <CatalogPage />;
    return (
      <GitWorkbenchPage
        key={gitProjectId}
        projects={projects}
        projectId={gitProjectId}
        sessions={source.sessions()}
        onProjectChange={setGitProjectId}
        onOpenSession={focusSession}
      />
    );
  };

  const app = (
    <div className="app">
      <Topbar
        taskTitle={view === "agents" ? "Agent 名单" : view === "catalog" ? "拓展" : view === "git" ? "Git 管理" : currentTitle}
        source={source}
        connected={false}
      />
      <TabBar
        source={source}
        currentId={currentId}
        busyBySession={busyBySession}
        onNewChat={() => { void source.newSession(filter); backToChat(); }}
        onFocusSession={focusSession}
        workspaceTabs={workspaceState.tabs}
        activeWorkspaceView={view}
        onFocusChat={backToChat}
        onFocusWorkspacePage={openWorkspace}
        onCloseWorkspacePage={closeWorkspace}
      />
      <Sidebar
        source={source}
        currentId={currentId}
        busyBySession={busyBySession}
        filter={filter}
        setFilter={setFilter}
        onOpenSettings={() => setSettingsOpen(true)}
        agentsActive={view === "agents"}
        onOpenAgents={openAgents}
        onOpenChat={backToChat}
        catalogActive={view === "catalog"}
        onOpenCatalog={openCatalog}
        gitActive={view === "git"}
        onOpenGit={openGit}
      />
      <WorkspaceViewPanels
        activeView={view}
        openTabs={workspaceState.tabs}
        renderView={renderView}
      />
      <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} source={source} />
    </div>
  );

  if (isShell) return app;
  return (
    // 浏览器模式：应用窗口模拟（1440×900 自适应），外层暗底
    <div className="window-stage">
      <div className="app-window">{app}</div>
    </div>
  );
}

function WorkspaceViewPanels({
  activeView,
  openTabs,
  renderView,
}: {
  activeView: WorkspaceView;
  openTabs: WorkspacePage[];
  renderView: (view: WorkspaceView) => ReactNode;
}) {
  const [displayedView, setDisplayedView] = useState<WorkspaceView>(activeView);
  const panels = useRef(new Map<WorkspaceView, HTMLDivElement>());
  const firstDisplay = useRef(true);

  useLayoutEffect(() => {
    if (displayedView === activeView) return;
    const outgoing = panels.current.get(displayedView);
    if (!outgoing || !motionAllowed()) {
      setDisplayedView(activeView);
      return;
    }
    const context = gsap.context(() => {
      gsap.to(outgoing, {
        opacity: 0,
        y: -5,
        duration: 0.14,
        ease: "power1.in",
        onComplete: () => setDisplayedView(activeView),
      });
    }, outgoing);
    return () => { context.revert(); };
  }, [activeView, displayedView]);

  useLayoutEffect(() => {
    if (firstDisplay.current) {
      firstDisplay.current = false;
      return;
    }
    const incoming = panels.current.get(displayedView);
    if (!incoming || !motionAllowed()) return;
    const context = gsap.context(() => {
      gsap.fromTo(incoming, { opacity: 0, y: 5 }, {
        opacity: 1,
        y: 0,
        duration: 0.18,
        ease: "power2.out",
        clearProps: "transform,opacity",
      });
    }, incoming);
    return () => { context.revert(); };
  }, [displayedView]);

  const renderedPages: WorkspacePage[] = [...openTabs];
  if (displayedView !== "chat" && !renderedPages.includes(displayedView)) renderedPages.push(displayedView);
  const renderedViews: WorkspaceView[] = ["chat", ...renderedPages];

  return (
    <main className="main">
      {renderedViews.map((view) => (
        <div
          key={view}
          ref={(element) => {
            if (element) panels.current.set(view, element);
            else panels.current.delete(view);
          }}
          className={"workspace-view-panel" + (view === "chat" ? " workspace-chat-panel" : "")}
          data-workspace-view={view}
          hidden={displayedView !== view}
        >
          {renderView(view)}
        </div>
      ))}
    </main>
  );
}
