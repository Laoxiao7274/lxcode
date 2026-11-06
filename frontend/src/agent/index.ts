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
 *  供给），所以壳内（Electron UA / app: 协议）直接回默认值。
 *
 *  dev 后端端口：scripts/dev.mjs 起 vite 时注入 LXCODE_DEV_BACKEND_PORT（=7790，
 *  与生产 7789 隔离——见 dev.mjs 头注），vite.config.ts 的 define 把它暴露成
 *  VITE_BACKEND_PORT。该注入只在 dev.mjs 传了环境变量的构建里存在——打包产物 /
 *  壳 / 生产构建读不到该键，恒回 7789。 */
function defaultLiveAddr(): string {
  // 注意写法：必须精确写 import.meta.env.VITE_BACKEND_PORT（无 ?.、无 as）——
  // vite.config.ts 的 define 只按这个字面表达式做文本替换；可选链或类型断言
  // 都会让替换失配，dev 下读不到端口回落 7789 连上生产（隔离失效）。
  const devPort = import.meta.env.VITE_BACKEND_PORT;
  const fallback = devPort ? `127.0.0.1:${devPort}` : "127.0.0.1:7789";
  if (isElectronRenderer()) return fallback;
  if (typeof location === "undefined" || location.protocol === "app:") return fallback;
  const h = location.hostname;
  if (!h || h === "localhost" || h === "127.0.0.1" || h === "::1" || h === "[::1]") return fallback;
  return `${h}:${devPort ?? "7789"}`;
}

/** 获取 AgentSource（单例）。 */
export function getAgentSource(): AgentSource {
  if (cached) return cached;
  // URL 显式选择优先于默认环境；不依赖 Electron API 即可使用真实后端。
  const mode = sourceMode(isElectronRenderer() ? "Electron" : "", typeof location === "undefined" ? "" : location.search);
  cached = mode === "live" ? new WSAgent(defaultLiveAddr(), () => backendToken()) : new DemoAgent();
  return cached;
}
