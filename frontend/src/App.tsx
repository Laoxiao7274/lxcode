import { useCallback, useEffect, useMemo, useState } from "react";
import { getAgentSource } from "./agent";
import { useAgent } from "./shared/store";
import { Topbar } from "./components/topbar";
import { Sidebar } from "./components/sidebar";
import { Thread } from "./components/thread";
import { Composer } from "./components/composer";
import { SettingsPanel } from "./components/settings";
import { SettingsProvider } from "./shared/settings";

export default function App() {
  return (
    <SettingsProvider>
      <AppBody />
    </SettingsProvider>
  );
}

function AppBody() {
  // 数据源：工厂（Electron 壳 = WSAgent 真实模式连 7789；浏览器 = DemoAgent 演示）
  const source = useMemo(() => getAgentSource(), []);
  const { state, send, resolve } = useAgent(source);
  // 初始无选中：空态起步（选中一个有历史的会话时 thread 才有内容——
  // 演示模式 resume 不重放历史，避免"高亮有历史、主区空白"的不一致）
  const [currentId, setCurrentId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);

  // 会话切换事件同步侧栏高亮（与 useAgent 的订阅并行，各管各的）
  useEffect(() => {
    return source.subscribe((ev: import("./shared/types").AgentEvent) => {
      if (ev.type === "sessionChanged") setCurrentId(ev.id);
    });
  }, [source]);

  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    source.confirm(id, allow);
    resolve(id, allow ? "allow" : "deny");
  }, [source, resolve]);

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: import("./shared/types").SessionMeta) => s.id === currentId)?.title ?? "任务";

  // 壳环境（Electron）= 真实窗口（不需要浏览器模拟壳）；浏览器 = 保留模拟壳
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");

  if (isShell) {
    // 壳模式：直接铺满窗口（无边框/暗底/投影——窗口本身就有）
    return (
      <div className="app">
        <Topbar taskTitle={currentTitle} source={source} connected={false} />
        <Sidebar source={source} currentId={currentId} busy={state.busy} onOpenSettings={() => setSettingsOpen(true)} />
        <main className="main">
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} />
          </div>
          <Composer busy={state.busy} onSend={send} onCancel={() => source.cancel()} />
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
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} />
          </div>
          <Composer busy={state.busy} onSend={send} onCancel={() => source.cancel()} />
        </main>
        <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} source={source} />
      </div>
    </div>
  );
}
