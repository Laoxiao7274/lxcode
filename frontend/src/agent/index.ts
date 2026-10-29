// 数据源工厂：根据环境选择真实后端（WS）或演示模式。
// - Electron 壳（壳主进程保证后端在线）→ WSAgent（连 :7789，带远程访问凭证）
// - 浏览器默认 DemoAgent；?mode=live 显式独立连接真实后端
import { DemoAgent } from "./demo";
import { WSAgent } from "./ws";
import type { AgentSource } from "../shared/types";
import { backendToken } from "./host";
import { sourceMode } from "./source-mode";

let cached: AgentSource | null = null;

/** 是否运行在 Electron 壳的渲染层里。 */
function isElectronRenderer(): boolean {
  return typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
}

/** 浏览器 live 模式的默认后端地址：本机（localhost/回环）访问保持 127.0.0.1；
 *  从局域网/远程浏览器打开页面时默认连**宿主机**（页面的 hostname + 后端端口），
 *  免去每次手填地址。Electron 壳不走这里——壳产线加载 app://lxcode/index.html，
 *  hostname === "lxcode" 会误算成 "lxcode:7789"（壳内一律本机地址，token 由壳桥
 *  供给），所以壳内（Electron UA / app: 协议）直接回默认值。 */
function defaultLiveAddr(): string {
  const fallback = "127.0.0.1:7789";
  if (isElectronRenderer()) return fallback;
  if (typeof location === "undefined" || location.protocol === "app:") return fallback;
  const h = location.hostname;
  if (!h || h === "localhost" || h === "127.0.0.1" || h === "::1" || h === "[::1]") return fallback;
  return `${h}:7789`;
}

/** 获取 AgentSource（单例）。 */
export function getAgentSource(): AgentSource {
  if (cached) return cached;
  // URL 显式选择优先于默认环境；不依赖 Electron API 即可使用真实后端。
  const mode = sourceMode(isElectronRenderer() ? "Electron" : "", typeof location === "undefined" ? "" : location.search);
  cached = mode === "live" ? new WSAgent(defaultLiveAddr(), () => backendToken()) : new DemoAgent();
  return cached;
}
