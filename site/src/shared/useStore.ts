// 站点数据源的 React 绑定（把 store.ts 的模块级单例接进组件树）。
//
// 与 App.tsx 的 hash 路由同一套机制：useSyncExternalStore 订阅模块级单例。
// store.ts 现在从后端读、往后端写（见那里的注释），hook 本身不需要知道这件事。

import { useSyncExternalStore } from "react";

import { getStoreSnapshot, subscribeStore, type ApiState } from "./store";

/** 当前数据（任意页都用它读；写走 state.ts 的写函数，服务端是唯一事实源）。 */
export function useStore(): ApiState {
  return useSyncExternalStore(subscribeStore, getStoreSnapshot, getStoreSnapshot);
}
