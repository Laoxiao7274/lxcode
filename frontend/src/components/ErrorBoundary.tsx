// ErrorBoundary.tsx —— React 渲染期异常的最后防线（2026-12「全白屏」止血三件套②）。
//
// 白屏根因②：渲染期未捕获异常 = React 卸掉整树，壳 frame:false 连关窗按钮都
// 没有，用户只能杀进程。本边界只管这一层——渲染期异常接住后降级为可读的
// 错误页 + 重新加载按钮；业务层的可恢复错误提示（lx-operation-error）不经过
// 这里，两套机制不许混淆。
//
// 挂载纪律：main.tsx 里包在挂载根**最外层**（Topbar 也在边界内——顶栏是
// React 树的一部分，渲染期异常同样会卸掉它）。
import { Component, type ErrorInfo, type ReactNode } from "react";

/** 任意抛出物 → 单行人话摘要（ErrorBoundary 降级页与全局兜底共用一份口径）。 */
export function errorSummary(err: unknown): string {
  if (typeof err === "string") return err;
  if (err instanceof Error) return `${err.name}: ${err.message}`;
  return String(err);
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // console.error 经壳的 console-message 链路落盘 <userData>/renderer.log
    //（打包壳无 DevTools，这是渲染层异常的唯一观测通道）。
    console.error(`[ErrorBoundary] ${errorSummary(error)}${info.componentStack ?? ""}`);
  }

  render(): ReactNode {
    if (!this.state.error) return this.props.children;
    return (
      <div
        style={{
          minHeight: "100vh",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 12,
          padding: 24,
          background: "#0d1117",
          color: "#e6edf3",
          fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        }}
      >
        <div style={{ fontSize: 16, fontWeight: 600 }}>界面遇到错误</div>
        <div style={{ fontSize: 12, opacity: 0.75, maxWidth: 640, wordBreak: "break-all" }}>
          {errorSummary(this.state.error)}
        </div>
        <button
          type="button"
          onClick={() => location.reload()}
          style={{
            marginTop: 8,
            padding: "8px 20px",
            border: "1px solid #30363d",
            borderRadius: 6,
            background: "#21262d",
            color: "#e6edf3",
            cursor: "pointer",
            fontSize: 13,
          }}
        >
          重新加载
        </button>
      </div>
    );
  }
}
