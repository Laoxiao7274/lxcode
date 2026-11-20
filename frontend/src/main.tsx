import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles/global.css";

// 字体自托管（Tauri 离线可用；fontsource 变量字体）
import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";

// 全局兜底（2026-12「全白屏」止血三件套②）：渲染期异常由 <ErrorBoundary> 接住
// 降级；这里再挂未处理的 Promise 拒绝与同步异常 → console.error——经壳的
// console-message 链路落盘 <userData>/renderer.log（打包壳无 DevTools，这是
// 唯一观测通道；业务层可恢复错误提示走 lx-operation-error，不经过这两条）。
window.addEventListener("unhandledrejection", (ev) => {
  console.error(`[unhandledrejection] ${ev.reason instanceof Error ? ev.reason.stack ?? String(ev.reason) : String(ev.reason)}`);
});
window.addEventListener("error", (ev) => {
  // 资源加载失败（img/script）也走 error 事件，无 error 对象时只记地址
  console.error(`[error] ${ev.error?.stack ?? `${ev.message} (${ev.filename}:${ev.lineno})`}`);
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
