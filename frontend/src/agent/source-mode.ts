// 纯环境选择：浏览器默认演示，显式 ?mode=live 可独立连接 Go 服务。
export function sourceMode(userAgent: string, search: string): "live" | "demo" {
  const mode = new URLSearchParams(search).get("mode");
  if (mode === "live" || mode === "demo") return mode;
  return userAgent.includes("Electron") ? "live" : "demo";
}
