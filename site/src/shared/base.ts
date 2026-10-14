// 站点部署根路径（vite 的 base）。
//
// 为什么要有这一个常量：官网可能被挂在**子路径**下（部署侧 `vite build --base=/site/`，
// 经 frp 统一入口 13000 的路径分流到站点后端），此时静态资源与接口都在 /site/ 之下；
// 而「以站点根为基准」的地址（/api、/releases、/manifest.json）写死前导斜杠就会打错地方。
// vite 把 base 注入 import.meta.env.BASE_URL（dev 与根路径部署都是 "/"），
// 所以这里统一去掉尾斜杠拼前缀，源码里继续只写相对站点根的路径。
export const SITE_BASE = import.meta.env.BASE_URL.replace(/\/$/, "");

/**
 * SITE_BASE 与相对站点根的路径段的安全拼接。
 *
 * 为什么不用裸 `SITE_BASE + path`：path 段**不保证**带前导斜杠
 * （android-release.json 的 url 是 `"downloads/…"`，没有斜杠；artifactUrl 返回
 * `"/releases/…"`，有斜杠）——SITE_BASE 又刻意去了尾斜杠，直接相加就会产生
 * `/sitedownloads/…` 这种缺斜杠形态（真机实测：落到 SPA 兜底页，
 * 用户下载到的是 200 + text/html 的 HTML 而不是 APK）。
 * 这里统一在交界处补足恰好一个斜杠，path 段带不带前导斜杠都能拼对。
 */
export function siteUrl(path: string): string {
  return `${SITE_BASE}/${path.replace(/^\/+/, "")}`;
}
