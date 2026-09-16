// 数据源工厂：根据环境选择真实后端（WS）或演示模式。
// - Tauri 模式 / 后端可达 → WSAgent（连 :7789）
// - 浏览器无后端 → DemoAgent（脚本编排演示数据）
import { DemoAgent } from "./demo";
import { WSAgent } from "./ws";
import type { AgentSource } from "../shared/types";

let cached: AgentSource | null = null;

/** 获取 AgentSource（单例）。 */
export function getAgentSource(): AgentSource {
  if (cached) return cached;

  const isTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
  // Tauri 模式连后端（壳会 spawn）；浏览器 = 纯页面原型，跑 DemoAgent 演示数据
  cached = isTauri ? new WSAgent("127.0.0.1:7789") : new DemoAgent();
  return cached;
}
