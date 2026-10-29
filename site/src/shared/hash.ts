// 哈希路由：两个站点（官网 index.html / 管理站 admin.html）共用同一套实现。
//
// 为什么不引路由库：两个站点都是静态托管、各只有几个固定页面、没有嵌套布局与数据加载器，
// 而 hash 路由在静态目录下不需要服务端回退（`#/releases` 永远不会打到服务端）。
// 用 useSyncExternalStore 订阅 hashchange：React 官方认可的外部状态订阅方式，
// 比在 effect 里 setState 少一次多余渲染，也不会漏掉首次快照。

import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void): () => void {
  window.addEventListener("hashchange", onChange);
  return () => window.removeEventListener("hashchange", onChange);
}

/** 当前路径：空 hash 与 `#` 都算首页；其余补上前导斜杠。 */
export function readHashPath(): string {
  const raw = window.location.hash.replace(/^#/, "");
  if (raw === "" || raw === "/") return "/";
  return raw.startsWith("/") ? raw : `/${raw}`;
}

/** 当前 hash 路径（订阅 hashchange）。 */
export function useHashPath(): string {
  // 第三参 getServerSnapshot：两个站点都不做 SSR，给个确定值即可（回退到首页）。
  return useSyncExternalStore(subscribe, readHashPath, () => "/");
}
