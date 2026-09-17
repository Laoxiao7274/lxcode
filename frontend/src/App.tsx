import { useCallback, useEffect, useMemo, useState } from "react";
import { getAgentSource } from "./agent";
import { useAgent } from "./shared/store";
import { Topbar } from "./components/topbar";
import { Sidebar } from "./components/sidebar";
import { Thread } from "./components/thread";
import { PlanBar } from "./components/thread/PlanBar";
import { Composer } from "./components/composer";
import { SettingsPanel } from "./components/settings";
import { SettingsProvider, useSettings } from "./shared/settings";
import type { SendOptions } from "./shared/types";

export default function App() {
  const source = useMemo(() => getAgentSource(), []);
  return (
    <SettingsProvider source={source}>
      <AppBody source={source} />
    </SettingsProvider>
  );
}

function AppBody({ source }: { source: import("./shared/types").AgentSource }) {
  const [operationError, setOperationError] = useState<string | null>(null);
  const { state, send, resolve } = useAgent(source);
  const { settings, providers } = useSettings();
  // 初始无选中：空态起步（选中一个有历史的会话时 thread 才有内容——
  // 演示模式 resume 不重放历史，避免"高亮有历史、主区空白"的不一致）
  const [currentId, setCurrentId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);

  // 会话切换事件同步侧栏高亮（与 useAgent 的订阅并行，各管各的）
  useEffect(() => {
    return source.subscribe((ev: import("./shared/types").AgentEvent) => {
      if (ev.type === "sessionChanged" && ["new", "resumed", "started"].includes(ev.reason)) setCurrentId(ev.id);
      if (ev.type === "operationError") setOperationError(ev.message);
    });
  }, [source]);

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
      .catch((e) => setOperationError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve]);

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: import("./shared/types").SessionMeta) => s.id === currentId)?.title ?? "任务";

  // 壳环境（Electron）= 真实窗口（不需要浏览器模拟壳）；浏览器 = 保留模拟壳
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
  const errorNotice = operationError && <div className="error-block" role="alert">{operationError}<button type="button" onClick={() => setOperationError(null)}>关闭</button></div>;

  if (isShell) {
    // 壳模式：直接铺满窗口（无边框/暗底/投影——窗口本身就有）
    return (
      <div className="app">
        <Topbar taskTitle={currentTitle} source={source} connected={false} />
        <Sidebar source={source} currentId={currentId} busy={state.busy} onOpenSettings={() => setSettingsOpen(true)} />
        <main className="main">
          {errorNotice}
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} />
          </div>
          <PlanBar todos={state.todos} />
          <Composer busy={state.busy} onSend={sendWithOptions} onCancel={() => source.cancel()} />
        </main>
        <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} source={source} />
      </div>
    );
  }

  return (
    // 浏览器模式：应用窗口模拟（1440×900 自适应），外层暗底
    <div className="window-stage">
      <div className="app-window">
        <Topbar taskTitle={currentTitle} source={source} connected={false} />
        <Sidebar source={source} currentId={currentId} busy={state.busy} onOpenSettings={() => setSettingsOpen(true)} />
        <main className="main">
          {errorNotice}
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} />
          </div>
          <PlanBar todos={state.todos} />
          <Composer busy={state.busy} onSend={sendWithOptions} onCancel={() => source.cancel()} />
        </main>
        <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} source={source} />
      </div>
    </div>
  );
}
