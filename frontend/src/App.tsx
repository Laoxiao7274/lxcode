import { useEffect, useMemo, useState } from "react";
import { DemoAgent } from "./agent/demo";
import { useAgent } from "./agent/store";
import { Topbar } from "./components/Topbar";
import { Sidebar } from "./components/Sidebar";
import { Thread } from "./components/Thread";
import { Composer } from "./components/Composer";

export default function App() {
  // 数据源：演示模式（脚本编排一轮完整交互，覆盖全部 UI 状态）。
  // 真实模式（WSAgent 连 127.0.0.1:7789）接入时换这一行，UI 不动。
  const source = useMemo(() => new DemoAgent(), []);
  const { state, send, resolve } = useAgent(source);
  const [currentId, setCurrentId] = useState("20260911-103024-a1b2");

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

  const currentTitle = source.sessions().find((s) => s.id === currentId)?.title ?? "新任务";

  return (
    // 应用窗口：桌面应用尺寸（1440×900 自适应），外层暗底
    <div className="window-stage">
      <div className="app-window">
        <Topbar taskTitle={currentTitle} source={source} connected={false} />
        <Sidebar source={source} currentId={currentId} busy={state.busy} />
        <main className="main">
          <div className="thread-scroll">
            <Thread state={state} onConfirm={handleConfirm} onSuggestion={send} />
          </div>
          <Composer busy={state.busy} onSend={send} onCancel={() => source.cancel()} />
        </main>
      </div>
    </div>
  );
}
