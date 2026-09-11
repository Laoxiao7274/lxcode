import type { AgentSource } from "../../shared/types";

/** 窗口标题栏：左 = 图标 + LxCode + 连接态；右 = 窗口控制（最小化/最大化/关闭）。
 *  data-tauri-drag-region = Tauri 原生拖拽区域；__LX__ 桥接窗口控制。 */
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

  return (
    <header className="topbar" data-tauri-drag-region>
      <div className="tb-left">
        <span className="brandMark">L</span>
        <span className="brandName">LxCode</span>
        <span className="tb-sep" />
        <span className="status">
          <span className="pulse-dot" data-off={!connected ? "true" : undefined} />
          <span>{connected ? "已连接" : source.label}</span>
        </span>
      </div>
      <span className="task-title">{taskTitle}</span>
      <div className="tb-right">
        <button type="button" className="win-btn" onClick={ctrl(win?.minimize)} aria-label="最小化" title="最小化" data-tauri-drag-region={false}>
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M5 12h14" />
          </svg>
        </button>
        <button type="button" className="win-btn" onClick={ctrl(win?.toggleMaximize)} aria-label="最大化" title="最大化" data-tauri-drag-region={false}>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" aria-hidden="true">
            <rect x="4" y="4" width="16" height="16" rx="1.5" />
          </svg>
        </button>
        <button type="button" className="win-btn win-close" onClick={ctrl(win?.close)} aria-label="关闭" title="关闭" data-tauri-drag-region={false}>
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </header>
  );
}
