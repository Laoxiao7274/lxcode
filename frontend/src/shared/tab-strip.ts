// 标签条的两条溢出行为（抽自 TabBar.tsx，2026-12 助手标签条对齐主域——同一份
// 实现，两处不再各自长一份：AssistantTabStrip 接入后行为与 TabBar 完全一致）。
//
// ① useStripOverflow：横向溢出的两个附属行为——
//    · 渐隐提示：方向由运行时 scrollLeft 决定，CSS 读不到（纯 CSS 只能"永远
//      两侧都渐隐"，那在没溢出时是假提示）——算成 data-overflow，样式只负责
//      按方向画（base.css 的 [data-overflow] 规则）。
//    · 滚轮横向滚：overflow-x: auto 在多数浏览器里不吃纵向滚轮，把 deltaY
//      转到 scrollLeft。只在真的溢出、且该方向还有余量时拦截——否则会吞掉
//      页面自身的滚动。
// ② useActiveTabVisible：活动标签滚进可视区——用户报的「标签页超出窗口被
//    遮住」的主因修复：新开的标签（追加在最右）落在可视区外，而滚动条是隐藏的。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { overflowAttr, overflowEdges, shouldInterceptWheel } from "./scroll-metrics";
import type { OverflowAttr } from "./scroll-metrics";

/** 活动标签滚进可视区。 */
export function useActiveTabVisible(ref: RefObject<HTMLElement | null>, activeKey: string, tabCount: number) {
  useLayoutEffect(() => {
    // 活动标签就是带 .on 的那个（两条 strip 的渲染件都用这个类，不再另起一套标记）
    ref.current?.querySelector<HTMLElement>(".tab.on")?.scrollIntoView({ block: "nearest", inline: "nearest" });
    // tabCount 也要进依赖：标签是**渲染后**才挂上 DOM 的（首屏铺开标签时 key 并没有变），
    // 只盯 key 的话，首屏那一次滚进可视区会被整个跳过。
  }, [ref, activeKey, tabCount]);
}

/** 标签条横向溢出的两个附属行为，返回 [strip 引用, data-overflow 值]。 */
export function useStripOverflow<T extends HTMLElement>() {
  const ref = useRef<T | null>(null);
  const [attr, setAttr] = useState<OverflowAttr>("none");
  const syncRef = useRef<() => void>(() => {});
  // 每次渲染后重算：标签增删改的是 scrollWidth 而不是容器的盒子宽，
  // ResizeObserver 只盯容器本身，看不见"标签多了"。
  useLayoutEffect(() => { syncRef.current(); });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const sync = () => {
      const next = overflowAttr(overflowEdges(el));
      setAttr((prev) => (prev === next ? prev : next)); // 同值不 setState：否则每次渲染都多跑一轮
    };
    syncRef.current = sync;
    sync();
    const onScroll = () => sync();
    const onWheel = (event: WheelEvent) => {
      if (!shouldInterceptWheel(el, event.deltaY)) return; // 不溢出/该方向到头 → 让事件冒泡
      event.preventDefault();
      el.scrollLeft += event.deltaY;
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    // 要 preventDefault，监听器就不能是 passive
    el.addEventListener("wheel", onWheel, { passive: false });
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    return () => {
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", onWheel);
      ro.disconnect();
      syncRef.current = () => {};
    };
  }, []);
  return [ref, attr] as const;
}

// ---- 两域标签持久化（2026-12 对齐主域体验）----
// tabOrder/tabClosed 落 localStorage，重启恢复标签条。持久化集合**只是记忆**，
// 恢复时必须与真实会话列表取交集（会话可能已归档/删除）——见 restoreTabStrip。
// 键按域分开：主域 lx.app.taborder / lx.app.tabclosed；助手域
// lx.app.asst.taborder / lx.app.asst.tabclosed（app-mode.ts 是两域共享的
// localStorage 记忆层，键固定、读写失败静默——同 lastAssistantSession 纪律）。
// 键全小写：happy-dom（测试环境）的 localStorage 把键名小写化，混大小写的键
// 在测试里读写对不上。

export interface TabStripPersist {
  order: string[];
  closed: string[];
}

function lsKey(domain: "main" | "asst", kind: "order" | "closed"): string {
  return domain === "main" ? `lx.app.tab${kind}` : `lx.app.asst.tab${kind}`;
}
// 全小写键（上方注释：happy-dom 小写化键名）
export const TAB_ORDER_KEY = { main: "lx.app.taborder", asst: "lx.app.asst.taborder" } as const;
export const TAB_CLOSED_KEY = { main: "lx.app.tabclosed", asst: "lx.app.asst.tabclosed" } as const;

/** 读持久化的标签顺序（坏数据 = 空数组，调用方按未持久化处理）。 */
export function readTabOrder(domain: "main" | "asst"): string[] {
  try {
    const raw = localStorage.getItem(lsKey(domain, "order"));
    if (!raw) return [];
    const v: unknown = JSON.parse(raw);
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** 写标签顺序（空数组 = 清除——语义与"标签条为空"一致）。 */
export function writeTabOrder(domain: "main" | "asst", order: string[]): void {
  try {
    if (order.length) localStorage.setItem(lsKey(domain, "order"), JSON.stringify(order));
    else localStorage.removeItem(lsKey(domain, "order"));
  } catch {
    /* 隐私模式等——不持久化而已 */
  }
}

/** 读持久化的关闭集合（坏数据 = 空）。 */
export function readTabClosed(domain: "main" | "asst"): string[] {
  try {
    const raw = localStorage.getItem(lsKey(domain, "closed"));
    if (!raw) return [];
    const v: unknown = JSON.parse(raw);
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** 写关闭集合（空 = 清除）。 */
export function writeTabClosed(domain: "main" | "asst", closed: string[]): void {
  try {
    if (closed.length) localStorage.setItem(lsKey(domain, "closed"), JSON.stringify(closed));
    else localStorage.removeItem(lsKey(domain, "closed"));
  } catch {
    /* 隐私模式等——不持久化而已 */
  }
}

/** 启动恢复：持久化 order/closed 与真实会话列表取交集（会话可能已归档/删除
 *  ——恢复的是"记忆"，不是"现实"）。closed 里不在会话列表里的条目也丢弃：
 *  会话已不存在，关闭态无意义。order 里 dead id 剔掉；归档的剔除（visible
 *  本来也不显示，留着只会占容量）；有当前会话且未关掉时它保证可见（调用方
 *  的 appendSessionTab 会补——本函数不做）。列表为空时原样退回（判不出死活）。
 *  无持久化（空 order）返回 null——调用方走 seedSessionTabs 原路径。
 *  archived 缺省（旧数据源）视为未归档——与 seedSessionTabs 的 `!s.archived`
 *  同一宽容度。 */
export function restoreTabStrip(
  domain: "main" | "asst",
  sessions: { id: string; archived?: boolean }[],
): { order: string[]; closed: string[] } | null {
  const savedOrder = readTabOrder(domain);
  if (savedOrder.length === 0) return null;
  const alive = new Map(sessions.map((s) => [s.id, s.archived === true]));
  const savedClosed = readTabClosed(domain);
  const closed = savedClosed.filter((id) => alive.has(id));
  const closedSet = new Set(closed);
  const order = savedOrder.filter((id) => {
    const archived = alive.get(id);
    return archived !== undefined && !archived && !closedSet.has(id);
  });
  return { order, closed };
}
