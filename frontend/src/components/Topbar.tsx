import type { AgentSource } from "../agent/types";

/** 顶栏：品牌/模型/地址 + 连接状态。 */
export function Topbar({ source, connected }: { source: AgentSource; connected: boolean }) {
  return (
    <header className="topbar">
      <span className="logo">lxcode</span>
      <span className="sep">/</span>
      <span className="model">MYT</span>
      <span className="addr">127.0.0.1:7789</span>
      <span className="spacer" />
      <div className="status">
        <span className="pulse-dot" data-off={!connected ? "true" : undefined} />
        <span>{connected ? "已连接" : source.label}</span>
      </div>
    </header>
  );
}
