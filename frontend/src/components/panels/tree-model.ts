// 会话树面板的纯函数层（**不 import React**——组件与 node:test 共用同一份判定，
// 测试不必拉起渲染层）。
//
// **只展示用户消息**（2026-12 用户拍板）：后端 chat.tree 只回 user 节点，这里
// 把骨架行直接铺成面板行——每条 user 一行、parent_seq 就是树上的父子（后端已算
// 成「树上最近前驱 user」），没有折叠、没有统计折叠、没有过滤档。
//
// 交互语义：
//   - on_path=false 的行是**在另一条分支上**——可见（灰化），单击先切过去再跳
//     （App 的 handleTreeJump 负责）；徽标「在分支上」提示它与当前对话无关；
//   - off_path 行**不标 leaf**（「你在这里」只属于当前路径）；
//   - 「回到最新」落点 = 树里最大的 seq（不变）。
import type { ThreadBlock } from "../../shared/blocks";
import type { ChatTreeResult, TreeNode } from "../../shared/types";
import { outlineLabel } from "./outline";

/** 面板要画的一行（= 一条 user 消息）。 */
export interface TreeRow {
  seq: number;
  /** 树上父行的 seq（0 = 树根；后端已按「最近前驱 user」给出）。
   *  **仅数据语义**：面板是平级列表（2026-12 用户拍板），不做缩进/层级渲染。 */
  parentSeq: number;
  kind: "user";
  /** 单行摘要（复用 outlineLabel，与右侧既有面板同一份实现）。 */
  label: string;
  /** 轮次序号（真实 user 消息从 1 起）。 */
  turn?: number;
  /** 跳转锚点 = 自己（user 行本身带 seq 的块）。 */
  userSeq: number;
  /** 树上的直接子行数（>1 = 分支点）。 */
  children: number;
  /** 是否在当前 leaf 路径上（false = 在另一条分支上）。 */
  onPath: boolean;
  /** === 当前 leaf 的 user（「你在这里」）。 */
  isLeaf: boolean;
  /** 该轮可还原的文件改动数（含子会话；0 = 没有，面板不渲染徽标）。 */
  files: number;
  /** 其中来自子会话的改动数（files 的子集）。 */
  childFiles: number;
  /** 该轮无法快照的写（user-only 树上无数据源，恒 0——字段保留兼容）。 */
  external: number;
  /** 畸形数据（父节点缺失/成环）——如实标出，不隐藏节点。 */
  orphan: boolean;
  /** 在另一条分支上（on_path=false 的显示语义，组件按它渲染「在分支上」徽标）。 */
  onBranch: boolean;
}

/** 树 → 面板行（按 seq 升序 = 时间顺序；后端已保证 parent 恒小于 seq）。
 *
 *  **平级列表（2026-12 用户拍板）**：忽略 parent 深度，按 seq 平铺——分支结构仍在
 *  数据里（parentSeq / onPath），视觉上不表达父子（off_path 行靠灰化 + title 标出）。
 *  畸形数据不炸（与后端同一条纪律）：断链/环/缺父的行仍渲染，orphan 标出、挂 root。 */
export function flattenTree(tree: ChatTreeResult | null | undefined): TreeRow[] {
  if (!tree) return [];
  const nodes = tree.nodes ?? [];

  const bySeq = new Map<number, TreeNode>();
  for (const n of nodes) bySeq.set(n.seq, n);

  /** 树上父行的 seq（0 = 根）：parent 在结果里就是它自己，不在（畸形/截断）钳到根。 */
  const parentOf = new Map<number, number>();
  for (const n of nodes) {
    const p = n.parentSeq;
    parentOf.set(n.seq, p > 0 && bySeq.has(p) ? p : 0);
  }

  const leafUser = tree.leafSeq > 0 && bySeq.get(tree.leafSeq)?.kind === "user" ? tree.leafSeq : 0;
  const rows: TreeRow[] = [];
  for (const n of nodes) {
    rows.push({
      seq: n.seq,
      parentSeq: parentOf.get(n.seq) ?? 0,
      kind: "user",
      label: outlineLabel(n.summary ?? "", 48) || "（空消息）",
      ...(n.turn && n.turn > 0 ? { turn: n.turn } : {}),
      userSeq: n.seq,
      children: n.children ?? 0,
      onPath: n.onPath,
      isLeaf: n.seq === leafUser,
      files: n.files ?? 0,
      childFiles: n.childFiles ?? 0,
      external: 0,
      orphan: n.orphan === true,
      onBranch: !n.onPath,
    });
  }
  return rows;
}

/** 树里的最大 seq（= 「回到最新」的落点；0 = 空树）。
 *
 *  为什么不用 leafSeq：leaf 可能是被回退过的位置（用户正停在中间），而「回到最新」
 *  要的是"这条会话长到哪儿了"——树里最大的 seq 才是它。**绝不传 0**：0 是「空路径」，
 *  会把用户送到第一条消息之前。 */
export function latestSeq(tree: ChatTreeResult | null | undefined): number {
  let max = 0;
  for (const n of tree?.nodes ?? []) if (n.seq > max) max = n.seq;
  return max;
}

/** 截断提示（truncated=true 显示「更早的 N 个节点未加载」）。 */
export function truncationNote(tree: ChatTreeResult | null | undefined): { count: number; text: string } | null {
  if (!tree?.truncated) return null;
  const count = Math.max(0, tree.total - tree.nodes.length);
  // 数字为 0（total 缺失/后端补齐导致节点数等于 total）时**不编一个 0**：
  // 说"还有更早的节点没加载"，不说"还有 0 个"。
  return count > 0
    ? { count, text: `更早的 ${count} 条消息未加载` }
    : { count: 0, text: "更早的消息未加载" };
}

/** 文件徽标：0 时返回 null（**不显示「0 文件」**——零值 = 没有这个事实）。
 *  含子会话时追加「· N 子 Agent」（用户要知道这一轮的文件不全是自己改的）。 */
export function filesBadge(row: Pick<TreeRow, "files" | "childFiles">): string | null {
  if (!row.files || row.files <= 0) return null;
  return row.childFiles && row.childFiles > 0
    ? `${row.files} 文件 · ${row.childFiles} 子 Agent`
    : `${row.files} 文件`;
}

/** 分支标记：直接子行 > 1 时显示「N 分支」。 */
export function branchBadge(row: Pick<TreeRow, "children">): string | null {
  return row.children > 1 ? `${row.children} 分支` : null;
}

/** 「在分支上」徽标：off_path 行常显（这条消息不在当前对话里——点了会切过去）。 */
export function onBranchBadge(row: Pick<TreeRow, "onPath">): string | null {
  return row.onPath ? null : "在分支上";
}

/** seq → 块 uid 的映射（跳转用）。
 *
 *  **只有 kind === "user" 的块带 seq**（dispatch/assistant/tool 块没有）——所以映射
 *  表只收 user 块；树行是 user 节点，它的跳转锚点就是自己。 */
export function userSeqUid(blocks: ThreadBlock[]): Map<number, number> {
  const map = new Map<number, number>();
  for (const b of blocks) {
    if (b.kind === "user" && typeof b.seq === "number") map.set(b.seq, b.uid);
  }
  return map;
}
