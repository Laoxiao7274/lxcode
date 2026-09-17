// 数据源工厂：根据环境选择真实后端（WS）或演示模式。
// - Electron 壳（壳主进程保证后端在线）→ WSAgent（连 :7789）
// - 浏览器默认 DemoAgent；?mode=live 显式独立连接真实后端
import { DemoAgent } from "./demo";
import { WSAgent } from "./ws";
import type { AgentSource } from "../shared/types";
import { sourceMode } from "./source-mode";

let cached: AgentSource | null = null;

/** 是否运行在 Electron 壳的渲染层里。 */
function isElectronRenderer(): boolean {
  return typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
}

/** 获取 AgentSource（单例）。 */
export function getAgentSource(): AgentSource {
  if (cached) return cached;
  // URL 显式选择优先于默认环境；不依赖 Electron API 即可使用真实后端。
  const mode = sourceMode(isElectronRenderer() ? "Electron" : "", typeof location === "undefined" ? "" : location.search);
  cached = mode === "live" ? new WSAgent("127.0.0.1:7789") : new DemoAgent();
  return cached;
}
