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
  // Tauri 模式始终尝试后端（壳会 spawn）；浏览器模式也试（可能手动起了后端）
  // 简单策略：先创建 WSAgent——连接失败时它会发出 error 事件，UI 自然降级。
  // 后续可以加 ping 探测自动 fallback 到 DemoAgent。
  if (isTauri) {
    cached = new WSAgent("127.0.0.1:7789");
  } else {
    // 浏览器模式也试连后端（开发时后端可能跑着）
    cached = new WSAgent("127.0.0.1:7789");
  }
  return cached;
}
