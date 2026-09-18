import { useCallback, useEffect, useMemo, useState } from "react";
import { getAgentSource } from "./agent";
import { useAgent } from "./shared/store";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
import { Topbar } from "./components/topbar";
import { Sidebar } from "./components/sidebar";
import { Thread, PlanBar } from "./components/thread";
import { Composer } from "./components/composer";
import { SettingsPanel } from "./components/settings";
import { SettingsProvider, useSettings } from "./shared/settings";
import { ConnectionsProvider } from "./shared/connections";
import { UpdateProvider } from "./shared/update";
import { SearchProvidersProvider } from "./shared/search-providers";
import { UpdateToast } from "./components/update/UpdateToast";
import type { AgentEvent, AgentSource, SendOptions, SessionMeta } from "./shared/types";

export default function App() {
  const source = useMemo(() => getAgentSource(), []);
  return (
    <SettingsProvider source={source}>
      <AgentsProvider>
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
  const { state, send, resolve, clearError, reportError } = useAgent(source);
  const { settings, providers } = useSettings();
  const { resetSessionDelegates } = useAgents();
  /** 主区视图：对话 / Agent 名单 / 拓展（工具·技能·模板·MCP）。 */
  const [view, setView] = useState<"chat" | "agents" | "catalog">("chat");
  /** 对话过滤目标（项目 id / ""=未分组 / null=全部）——App 持有：
   *  侧栏过滤、「新对话」归属、空态项目标签三处共用。 */
  const [filter, setFilter] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const openAgents = () => setView((v) => (v === "agents" ? "chat" : "agents"));
  const openCatalog = () => setView((v) => (v === "catalog" ? "chat" : "catalog"));
  const backToChat = () => setView("chat");

  // 新会话/切换会话 → 本次会话的委派覆盖清掉，回到名单默认（会话 id 与
  // 操作错误已并入 useAgent 的单一归约——不再有第二份订阅）
  useEffect(() => {
    return source.subscribe((ev: AgentEvent) => {
      if (ev.type === "sessionChanged" && (ev.reason === "new" || ev.reason === "resumed")) resetSessionDelegates();
    });
  }, [source, resetSessionDelegates]);

  const currentId = state.currentId;

  // 当前过滤的项目名（""=未分组 → 空态不标——无归属不需要声明）
  const filterProjectName =
    filter === null || filter === ""
      ? undefined
      : source.projects().find((p) => p.id === filter)?.name;

  // 发送时携带请求级参数：effort 只在当前模型声明推理能力时上帧（后端
  // 能力门控会丢弃不匹配档位，不带上帧更诚实）；approval 恒带当前设置。
  const sendWithOptions = useCallback((text: string) => {
    const current = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
    const opts: SendOptions = { approval: settings.approval };
    if (current && current.efforts.length > 0) opts.effort = settings.effort;
    send(text, opts);
  }, [providers, settings.model, settings.effort, settings.approval, send]);

  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void source.confirm(id, allow)
      .then(() => resolve(id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError]);

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: SessionMeta) => s.id === currentId)?.title ?? "任务";

  // 壳环境（Electron）= 真实窗口；浏览器 = 保留模拟壳（窗口模拟一层的差异，
  // 内部布局完全一致——同组件，不再两份 JSX）
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
  const errorNotice = state.operationError && (
    <div className="error-block" role="alert">{state.operationError}<button type="button" onClick={clearError}>关闭</button></div>
  );

  const mainView =
    view === "chat" ? (
      <>
        {errorNotice}
        <div className="thread-scroll">
          <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} projectName={filterProjectName} />
        </div>
        <PlanBar todos={state.todos} />
        <Composer busy={state.busy} onSend={sendWithOptions} onCancel={() => source.cancel()} />
      </>
    ) : view === "agents" ? (
      <AgentsPage />
    ) : (
      <CatalogPage />
    );

  const app = (
    <div className="app">
      <Topbar
        taskTitle={view === "agents" ? "Agent 名单" : view === "catalog" ? "拓展" : currentTitle}
        source={source}
        connected={false}
      />
      <Sidebar
        source={source}
        currentId={currentId}
        busy={state.busy}
        filter={filter}
        setFilter={setFilter}
        onOpenSettings={() => setSettingsOpen(true)}
        agentsActive={view === "agents"}
        onOpenAgents={openAgents}
        onOpenChat={backToChat}
        catalogActive={view === "catalog"}
        onOpenCatalog={openCatalog}
      />
      <main className="main">{mainView}</main>
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
