// cert-allow.ts —— certificate-error 的自签放行判定（纯函数，main.ts 的 handler
// 调它；独立成模块是因为 main.ts 顶部 import electron，测试没法整体引入）。
//
// 背景：樱花frp 隧道节点对外 TLS 终结用 frpc **自签**证书，渲染层 WsConnection 的
// WebSocket 走 Chromium 网络栈，自签证书默认直接拒绝且无任何可跳过的 UI——
// 同一张配对二维码在桌面端扫（或手填 wss:// 地址）必然连接失败，故按事件定向
// 放行而不是全局 ignore-certificate-errors。
//
// 放行条件（三条取交集，尽量窄）：
//  * URL 是 wss:// 或 https://（隧道入口的 WS / 附件 origin 两种形态——
//    wsURL 会把 wss 地址用于 WS，httpOrigin 会把它换成 https 用于附件下载）；
//  * host 非回环（回环 = 本机后端，永远是 ws:// 明文，不该走到这里）；
//  * 错误属于自签特征（信任链无效 / 主机名不匹配——frpc 自签证书两者都会中）。
//    Electron 40 实测：事件给的 error 带 `net::` 前缀（wss 握手错误与导航同源
//    报 net::ERR_*），比对前必须剥掉——2026-11 用户「一直重连」的根因就是
//    白名单里存裸名、`Set.has()` 恒 false，放行条件从未命中。
// 其余证书错误（app:// 主帧、公网导航、过期证书等）照常拒绝，行为不变。

/** 自签特征错误名（裸名，不含 net:: 前缀——事件值剥前缀后比对）。 */
export const SELF_SIGNED_CERT_ERRORS = new Set([
  "ERR_CERT_AUTHORITY_INVALID",
  "ERR_CERT_COMMON_NAME_INVALID",
]);

/** certificate-error 事件里的 error 值是否落在自签白名单（剥 net:: 前缀后比对）。 */
export function isSelfSignedCertError(error: string): boolean {
  return SELF_SIGNED_CERT_ERRORS.has(error.replace(/^net::/, ""));
}

/** 完整放行判定：url / error / 回环判断都按事件原样传入（main.ts handler 薄包装）。 */
export function shouldAllowSelfSigned(url: string, error: string): boolean {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return false; // 非法 URL：不放行
  }
  // IPv6 字面量的 hostname 保留方括号（"[::1]"），剥掉再比——原实现只比
  // "::1"，IPv6 回环的 https/wss 会被漏放行（2026-11 补测试时暴露）。
  const host = u.hostname.replace(/^\[|\]$/g, "");
  const loopback = host === "localhost" || host === "127.0.0.1" || host === "::1";
  return (
    (u.protocol === "wss:" || u.protocol === "https:") &&
    !loopback &&
    isSelfSignedCertError(error)
  );
}
