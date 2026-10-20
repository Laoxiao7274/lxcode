// 樱花frp 节点选择的展示口径（纯函数：组件只负责画，口径与测试共用一份）。
//
// 与壳侧 shell/src/sakura-nodes.ts 的分工：那边是**判定**（谁能建、建哪台），
// 这里是**展示与推荐**（列表怎么排、谁高亮、数字怎么显示）。两边都不重复实现
// 可用性判断——usable/reason 由壳算好随列表下发。
import type { SakuraNode } from "../agent/sakura";

/** 负载文案：null = 未知 → 「—」（不是 0%——0% 是"负载为零"）。 */
export function loadText(load: number | null | undefined): string {
  return typeof load === "number" ? `${load}%` : "—";
}

/** 延迟文案：null = 还没测出/测不出 → 「—」（不是 0 ms）。 */
export function latencyText(ms: number | null | undefined): string {
  return typeof ms === "number" ? `${Math.round(ms)} ms` : "—";
}

/** 延迟色调（快/中/慢），用于列表里的数字着色。 */
export function latencyTone(ms: number | null | undefined): "fast" | "mid" | "slow" | null {
  if (typeof ms !== "number") return null;
  if (ms < 80) return "fast";
  if (ms < 180) return "mid";
  return "slow";
}

/**
 * 推荐节点 = 可用节点里**实测延迟最低**者；延迟相同（或都还没测出）时
 * 负载低者优先（未知负载按最高处理），再按 id 升序保证结果稳定。
 *
 * 为什么延迟优先而不是负载优先：用户手动选节点时关心的是"连得顺不顺"，
 * 负载只说明节点忙不忙。自动创建仍走负载升序（壳侧行为不变）——两者口径
 * 不同是有意的：自动模式不探测（省连接），手动模式才测。
 */
export function recommendNode(nodes: SakuraNode[]): SakuraNode | null {
  const usable = nodes.filter((n) => n.usable);
  if (usable.length === 0) return null;
  const latencyOf = (n: SakuraNode): number =>
    typeof n.latency === "number" ? n.latency : Number.POSITIVE_INFINITY;
  const loadOf = (n: SakuraNode): number =>
    typeof n.load === "number" ? n.load : Number.POSITIVE_INFINITY;
  return [...usable].sort((a, b) => {
    const la = latencyOf(a);
    const lb = latencyOf(b);
    if (la !== lb) return la - lb;
    const oa = loadOf(a);
    const ob = loadOf(b);
    if (oa !== ob) return oa - ob;
    return a.id - b.id;
  })[0];
}

/** 面板初次打开时的选中项：有推荐就用推荐，否则回落到「自动选择」。 */
export function initialSelection(nodes: SakuraNode[]): number | "auto" {
  return recommendNode(nodes)?.id ?? "auto";
}

/** 还有可用节点没测出延迟（面板据此显示「测试中…」）。 */
export function pendingLatencyCount(nodes: SakuraNode[]): number {
  return nodes.filter((n) => n.usable && typeof n.latency !== "number").length;
}
