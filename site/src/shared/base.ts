// 站点部署根路径（vite 的 base）。
//
// 为什么要有这一个常量：官网可能被挂在**子路径**下（部署侧 `vite build --base=/site/`，
// 经 frp 统一入口 13000 的路径分流到站点后端），此时静态资源与接口都在 /site/ 之下；
// 而「以站点根为基准」的地址（/api、/releases、/manifest.json）写死前导斜杠就会打错地方。
// vite 把 base 注入 import.meta.env.BASE_URL（dev 与根路径部署都是 "/"），
// 所以这里统一去掉尾斜杠拼前缀，源码里继续只写相对站点根的路径。
export const SITE_BASE = import.meta.env.BASE_URL.replace(/\/$/, "");
