import { useState } from "react";
import type { AgentSource } from "../../shared/types";
import { useConnections } from "../../shared/connections";
import { ConnectionManager } from "../connections/ConnectionManager";

/** 窗口标题栏：左 = 图标 + LxCode + 连接指示器（点击管理连接）；
 *  右 = 窗口控制（最小化/最大化/关闭）。
 *  拖拽区 = CSS -webkit-app-region: drag（global.css 的 .topbar/.win-btn）；
 *  窗口控制经 Electron preload 桥 __LX__（浏览器模式无桥，按钮无害空操作）。 */
export function Topbar({
  taskTitle,
  source,
  connected,
}: {
  taskTitle: string;
  source: AgentSource;
  connected: boolean;
}) {
  const win = (window as unknown as { __LX__?: { minimize: () => void; toggleMaximize: () => void; close: () => void } }).__LX__;
  const ctrl = (fn?: () => void) => () => fn?.();
  const { active, remotes } = useConnections();
  const [mgrOpen, setMgrOpen] = useState(false);
  const activeName = active === "local" ? "本机" : remotes.find((c) => c.id === active)?.name ?? "远程";

  return (
    <header className="topbar">
      <div className="tb-left">
        <span className="brandMark">L</span>
        <span className="brandName">LxCode</span>
        <span className="tb-sep" />
        <button
          type="button"
          className="conn-pill"
          data-conn="pill"
          onClick={() => setMgrOpen(true)}
          title="连接管理（本机 / 远程后端）"
          aria-label={`当前连接：${activeName}，打开连接管理`}
        >
          <span className="pulse-dot" data-off={!connected ? "true" : undefined} />
          <span className="conn-pill-name">{activeName}</span>
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
        {!connected && <span className="conn-off-hint">{source.label}</span>}
      </div>
      <span className="task-title">{taskTitle}</span>
      <div className="tb-right">
        <button type="button" className="win-btn" onClick={ctrl(win?.minimize)} aria-label="最小化" title="最小化">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M5 12h14" />
          </svg>
        </button>
        <button type="button" className="win-btn" onClick={ctrl(win?.toggleMaximize)} aria-label="最大化" title="最大化">
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" aria-hidden="true">
            <rect x="4" y="4" width="16" height="16" rx="1.5" />
          </svg>
        </button>
        <button type="button" className="win-btn win-close" onClick={ctrl(win?.close)} aria-label="关闭" title="关闭">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
      {mgrOpen && <ConnectionManager onClose={() => setMgrOpen(false)} />}
    </header>
  );
}
