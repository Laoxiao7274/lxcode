import type { AgentSource } from "../agent/types";

/** 窗口标题栏：中置任务标题 + 右侧连接状态（Codex 线程视图顶栏）。 */
export function Topbar({
  taskTitle,
  source,
  connected,
}: {
  taskTitle: string;
  source: AgentSource;
  connected: boolean;
}) {
  return (
    <header className="topbar">
      <span className="logo">lxcode</span>
      <span className="task-title">{taskTitle}</span>
      <div className="status">
        <span className="pulse-dot" data-off={!connected ? "true" : undefined} />
        <span>{connected ? "已连接" : source.label}</span>
      </div>
    </header>
  );
}
