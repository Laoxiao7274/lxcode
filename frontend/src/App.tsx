import { useEffect, useMemo, useState } from "react";
import { DemoAgent } from "./agent/demo";
import { useAgent } from "./agent/store";
import { Topbar } from "./components/Topbar";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";
import { SettingsPanel } from "./components/SettingsPanel";
import { SettingsProvider } from "./settings";

export default function App() {
  return (
    <SettingsProvider>
      <AppBody />
    </SettingsProvider>
  );
}

function AppBody() {
  // 数据源：演示模式（脚本编排一轮完整交互，覆盖全部 UI 状态）。
  // 真实模式（WSAgent 连 127.0.0.1:7789）接入时换这一行，UI 不动。
  const source = useMemo(() => new DemoAgent(), []);
  const { state, send, resolve } = useAgent(source);
  // 初始无选中：空态起步（选中一个有历史的会话时 thread 才有内容——
  // 演示模式 resume 不重放历史，避免"高亮有历史、主区空白"的不一致）
  const [currentId, setCurrentId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);

  // 会话切换事件同步侧栏高亮（与 useAgent 的订阅并行，各管各的）
  useEffect(() => {
    return source.subscribe((ev) => {
      if (ev.type === "sessionChanged") setCurrentId(ev.id);
    });
  }, [source]);

  const handleConfirm = (id: string, allow: boolean) => {
    source.confirm(id, allow);
    resolve(id, allow ? "allow" : "deny");
  };

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s) => s.id === currentId)?.title ?? "任务";

  // Tauri 环境 = 真实窗口（不需要浏览器模拟壳）；浏览器 = 保留模拟壳
  const isTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

  if (isTauri) {
    // Tauri 模式：直接铺满窗口（无边框/暗底/投影——窗口本身就有）
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
        <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} />
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
        <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} />
      </div>
    </div>
  );
}
