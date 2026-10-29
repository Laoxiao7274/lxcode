// 原型提示条的**页面级单例**状态。
//
// 为什么提到页面级：上轮提示条挂在每个链接上（各一份 useState），点一下就地撑开卡脚
//（实测 +110px），连点两个按钮还会出现两条。现在全站只有一条 fixed 提示条：
// 不占文档流 → 零跳动，连点只刷新同一条，4s 后自动消失。
//
// 形态与 hash 路由同一套：模块级单例 + useSyncExternalStore（纯 UI 状态，不碰数据层）。

import { useSyncExternalStore } from "react";

export interface ProtoNotice {
  /** 每次显示自增：连点同一处也能让订阅者重新渲染（内容相同也要有反馈）。 */
  id: number;
  text: string;
}

const AUTO_HIDE_MS = 4000;

let current: ProtoNotice | null = null;
let seq = 0;
let timer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

export function subscribeNotice(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getNotice(): ProtoNotice | null {
  return current;
}

/** 显示提示条（单例）：连点只刷新这一条，AUTO_HIDE_MS 后自动消失。 */
export function showNotice(text: string): void {
  seq += 1;
  current = { id: seq, text };
  if (timer) clearTimeout(timer);
  timer = setTimeout(hideNotice, AUTO_HIDE_MS);
  emit();
}

export function hideNotice(): void {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  if (current === null) return;
  current = null;
  emit();
}

export function useProtoNotice(): ProtoNotice | null {
  return useSyncExternalStore(subscribeNotice, getNotice, () => null);
}
