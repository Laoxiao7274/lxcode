// preload.ts —— 渲染层壳桥（唯一 IPC 面）。
// Topbar 窗口控制按 window.__LX__.{minimize,toggleMaximize,close} 调用；
// 项目添加需要系统目录选择器（selectDirectory）；樱花frp 经 sakurafrp 命名
// 空间发意图/收状态（API 与 frpc 进程全在主进程，密钥不过渲染层）。
// 刻意保持最小面：不给渲染层任何 Node/文件/进程能力（AGENTS.md Electron 纪律）。
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("__LX__", {
  minimize: () => ipcRenderer.send("win:minimize"),
  toggleMaximize: () => ipcRenderer.send("win:toggleMaximize"),
  close: () => ipcRenderer.send("win:close"),
  // 打开系统目录选择器，返回选中路径（取消返回 null）。async 形式
  // 经 invoke（请求-应答语义，不是事件广播）。
  selectDirectory: (): Promise<string | null> => ipcRenderer.invoke("dialog:selectDirectory"),
  // 樱花frp：invoke 发意图（返回最新状态快照；失败 reject 人话错误消息），
  // onState 订阅主进程推送（登录恢复/轮询刷新/frpc 退出都走它）。
  sakurafrp: {
    getState: (): Promise<unknown> => ipcRenderer.invoke("sakura:getState"),
    login: (key: string): Promise<unknown> => ipcRenderer.invoke("sakura:login", key),
    logout: (): Promise<unknown> => ipcRenderer.invoke("sakura:logout"),
    refresh: (): Promise<unknown> => ipcRenderer.invoke("sakura:refresh"),
    createTunnel: (): Promise<unknown> => ipcRenderer.invoke("sakura:createTunnel"),
    startTunnel: (id: number): Promise<unknown> => ipcRenderer.invoke("sakura:startTunnel", id),
    stopTunnel: (): Promise<unknown> => ipcRenderer.invoke("sakura:stopTunnel"),
    removeTunnel: (id: number): Promise<unknown> => ipcRenderer.invoke("sakura:removeTunnel", id),
    onState: (cb: (s: unknown) => void): (() => void) => {
      const listener = (_e: unknown, s: unknown): void => cb(s);
      ipcRenderer.on("sakura:state", listener as never);
      return () => ipcRenderer.removeListener("sakura:state", listener as never);
    },
  },
  // Tailscale 模式：status 检测 + serve 发布开关（与樱花frp 同构）
  tailscale: {
    getState: (): Promise<unknown> => ipcRenderer.invoke("tailscale:getState"),
    serveOn: (): Promise<unknown> => ipcRenderer.invoke("tailscale:serveOn"),
    serveOff: (): Promise<unknown> => ipcRenderer.invoke("tailscale:serveOff"),
    refresh: (): Promise<unknown> => ipcRenderer.invoke("tailscale:refresh"),
    onState: (cb: (s: unknown) => void): (() => void) => {
      const listener = (_e: unknown, s: unknown): void => cb(s);
      ipcRenderer.on("tailscale:state", listener as never);
      return () => ipcRenderer.removeListener("tailscale:state", listener as never);
    },
  },
  // 后端远程访问：token 同步取（sendSync，主进程启动时已缓存）+ 开关/轮换
  backendRemote: {
    tokenSync: (): string | null => ipcRenderer.sendSync("backendRemote:tokenSync") as string | null,
    get: (): Promise<unknown> => ipcRenderer.invoke("backendRemote:get"),
    enable: (): Promise<unknown> => ipcRenderer.invoke("backendRemote:enable"),
    disable: (): Promise<unknown> => ipcRenderer.invoke("backendRemote:disable"),
    rotate: (): Promise<unknown> => ipcRenderer.invoke("backendRemote:rotate"),
    refresh: (): Promise<unknown> => ipcRenderer.invoke("backendRemote:refresh"),
  },
});
