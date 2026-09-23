import { useCallback, useEffect, useMemo, useState } from "react";
import { getAgentSource } from "./agent";
import { useAgent } from "./shared/store";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
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
import type { AgentEvent, AgentSource, SendOptions, SessionMeta } from "./shared/types";

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
  const { state, send, resolve, clearError, reportError } = useAgent(source);
  const { settings, providers } = useSettings();
  const { resetSessionDelegates, activeAgentId } = useAgents();
  /** 主区视图：对话 / Agent 名单 / 拓展（工具·技能·模板·MCP）。 */
  const [view, setView] = useState<"chat" | "agents" | "catalog">("chat");
  /** 对话范围（项目 id / ""=未分组）——App 持有：侧栏过滤、「新对话」归属、
   *  空态项目标签三处共用。用户拍板：**恒有范围**（启动即「未分组」，点项目行
   *  切换且不可取消——没有「全部」视图）。 */
  const [filter, setFilter] = useState<string>(LOOSE);
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

  // live 写失败桥（AgentsProvider 的乐观更新 WS 调用失败 → 一次性提示）
  useEffect(() => {
    const on = (e: Event) => reportError(String((e as CustomEvent<string>).detail ?? ""));
    window.addEventListener("lx-operation-error", on);
    return () => window.removeEventListener("lx-operation-error", on);
  }, [reportError]);

  const currentId = state.currentId;

  // 当前范围的项目名（「未分组」→ 空态不标——无归属不需要声明）
  const filterProjectName =
    filter === LOOSE ? undefined : source.projects().find((p) => p.id === filter)?.name;

  // 发送时携带请求级参数：effort 只在当前模型声明推理能力时上帧（后端
  // 能力门控会丢弃不匹配档位，不带上帧更诚实）；approval 恒带当前设置；
  // agent = 当前选用的 Agent（主 Agent 也显式携带——后端按名单语境跑）。
  const sendWithOptions = useCallback((text: string) => {
    const current = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
    const opts: SendOptions = { approval: settings.approval, agent: activeAgentId };
    if (current && current.efforts.length > 0) opts.effort = settings.effort;
    send(text, opts);
  }, [providers, settings.model, settings.effort, settings.approval, send, activeAgentId]);

  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void source.confirm(id, allow)
      .then(() => resolve(id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError]);

  // 手动压缩：请求类失败进一次性提示（不动 blocks）；没有可压收益时给一句人话
  // 原因（不是错误——历史还太短是正常态）。压缩成功由 chat.compacted 事件渲染标记块。
  const handleCompact = useCallback(async () => {
    try {
      const res = await source.compact();
      if (!res.compacted) reportError(res.reason ?? "没有可压缩的历史");
    } catch (e) {
      reportError(`压缩失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [source, reportError]);

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
    { name: "new", desc: "开始新对话", run: () => { source.newSession(filter); backToChat(); } },
    { name: "compact", desc: "压缩早期历史（腾出上下文）", run: () => { void handleCompact(); } },
    { name: "agents", desc: "打开 Agent 名单与组装", run: openAgents },
    { name: "catalog", desc: "打开拓展（工具/技能/模板/MCP）", run: openCatalog },
    { name: "settings", desc: "打开设置", run: () => setSettingsOpen(true) },
  ], [source, filter, openAgents, openCatalog, handleCompact]);

  const mainView =
    view === "chat" ? (
      <>
        {errorNotice}
        <div className="thread-scroll">
          <Thread state={state} onConfirm={handleConfirm} onSuggestion={(t) => sendWithOptions(t)} projectName={filterProjectName} />
        </div>
        <Composer busy={state.busy} todos={state.todos} context={state.context} onSend={sendWithOptions} onCancel={() => source.cancel()} onCompact={() => { void handleCompact(); }} commands={slashCommands} />
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
      <TabBar
        source={source}
        currentId={currentId}
        busy={state.busy}
        onNewChat={() => { if (!state.busy) { source.newSession(filter); backToChat(); } }}
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
