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
import { useAgent } from "./shared/store";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
import { GitWorkbenchPage } from "./components/git/GitWorkbenchPage";
import { Topbar } from "./components/topbar";
import { Sidebar, LOOSE } from "./components/sidebar";
import { Thread } from "./components/thread";
import { Composer } from "./components/composer";
import type { SlashCommand } from "./components/composer/SlashPalette";
import { TabBar } from "./components/topbar/TabBar";
import { SettingsPanel } from "./components/settings";
import { SettingsProvider, useSettings } from "./shared/settings";
import { ConnectionsProvider } from "./shared/connections";
import { UpdateProvider } from "./shared/update";
import { SearchProvidersProvider } from "./shared/search-providers";
import { UpdateToast } from "./components/update/UpdateToast";
import type { AgentSource, SendOptions, SessionMeta } from "./shared/types";

export default function App() {
  const source = useMemo(() => getAgentSource(), []);
  return (
    <SettingsProvider source={source}>
      <AgentsProvider source={source}>
        <ConnectionsProvider>
          <UpdateProvider>
            <SearchProvidersProvider>
              <AppBody source={source} />
              <UpdateToast />
            </SearchProvidersProvider>
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

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: SessionMeta) => s.id === currentId)?.title ?? "任务";

  // 壳环境（Electron）= 真实窗口；浏览器 = 保留模拟壳（窗口模拟一层的差异，
  // 内部布局完全一致——同组件，不再两份 JSX）
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
  const errorNotice = state.operationError && (
    <div className="error-block" role="alert">{state.operationError}<button type="button" onClick={clearError}>关闭</button></div>
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
      return (
        <>
          {errorNotice}
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={(t) => sendWithOptions(t)} projectName={filterProjectName} />
          </div>
          <Composer busy={state.busy} todos={state.todos} context={state.context} onSend={sendWithOptions} onCancel={() => source.cancel(currentId)} onCompact={() => { void handleCompact(); }} commands={slashCommands} />
        </>
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
