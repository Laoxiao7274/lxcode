// preload.ts —— 渲染层窗口控制桥（唯一 IPC 面）。
// Topbar 的窗口控制按钮按 window.__LX__.{minimize,toggleMaximize,close} 调用
// （Tauri 时代的调用约定平移到 Electron）。刻意保持最小面：只有这三个
// 窗口操作，不给渲染层任何 Node/文件/进程能力（AGENTS.md Electron 纪律）。
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("__LX__", {
  minimize: () => ipcRenderer.send("win:minimize"),
  toggleMaximize: () => ipcRenderer.send("win:toggleMaximize"),
  close: () => ipcRenderer.send("win:close"),
});
