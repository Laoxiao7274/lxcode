// 数据源工厂：根据环境选择真实后端（WS）或演示模式。
// - Electron 壳（壳主进程保证后端在线）→ WSAgent（连 :7789）
// - 浏览器（纯页面原型）→ DemoAgent（脚本编排演示数据）
import { DemoAgent } from "./demo";
import { WSAgent } from "./ws";
import type { AgentSource } from "../shared/types";

let cached: AgentSource | null = null;

/** 是否运行在 Electron 壳的渲染层里。 */
function isElectronRenderer(): boolean {
  return typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
}

/** 获取 AgentSource（单例）。 */
export function getAgentSource(): AgentSource {
  if (cached) return cached;
  // 壳模式连后端（壳主进程已拉起/直连 7789）；浏览器 = 纯页面原型，跑 DemoAgent 演示数据
  cached = isElectronRenderer() ? new WSAgent("127.0.0.1:7789") : new DemoAgent();
  return cached;
}
