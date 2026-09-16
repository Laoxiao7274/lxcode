// preload.ts —— 渲染层壳桥（唯一 IPC 面）。
// Topbar 窗口控制按 window.__LX__.{minimize,toggleMaximize,close} 调用；
// 项目添加需要系统目录选择器（selectDirectory）。刻意保持最小面：
// 不给渲染层任何 Node/文件/进程能力（AGENTS.md Electron 纪律）。
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("__LX__", {
  minimize: () => ipcRenderer.send("win:minimize"),
  toggleMaximize: () => ipcRenderer.send("win:toggleMaximize"),
  close: () => ipcRenderer.send("win:close"),
  // 打开系统目录选择器，返回选中路径（取消返回 null）。async 形式
  // 经 invoke（请求-应答语义，不是事件广播）。
  selectDirectory: (): Promise<string | null> => ipcRenderer.invoke("dialog:selectDirectory"),
});
