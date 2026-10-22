// 会话树面板的纯函数层（**不 import React**——组件与 node:test 共用同一份判定，
// 测试不必拉起渲染层）。
//
// 输入是后端 `chat.tree` 的骨架（按 seq 升序的节点 + parent_seq），输出是面板要画的
// 行数组：缩进深度、连接线（由 depth + last 决定，见 CSS）、分支标记、折叠/过滤结果。
//
// 为什么要有这一层：树形状的正确性全在几条容易写错的规则里（折叠掉的中间节点不能
// 计入缩进、分支统计必须沿 **parent 链**而不是"seq 更小的最近一条"、过滤后祖先要留成
// 骨架）——埋在 JSX 里就只能靠肉眼验收，而肉眼看不出"这条工具调用被算到了另一条
// 分支的轮上"。
//
// 粒度（docs/session-tree.md §D.3 规则 1）：`user` / `checkpoint` / `notice` 各自成行；
// `assistant` / `tool` **折叠进最近的成行祖先**（沿 parent 链，不是按 seq 找），它们的
// 文件/外部操作计数累加到那一行上——后端把 `files` 挂在**携带 tool_call 的 assistant
// 消息**的 seq 上（owner_seq），所以这一步折叠是徽标能对上的前提。
import type { ChatTreeResult, TreeNode } from "../../shared/types";
import type { ThreadBlock } from "../../shared/blocks";
import { outlineLabel } from "./outline";

/** 过滤三档（用户要求）：全部 / 只看用户消息 / 只看有改动。 */
export type TreeFilter = "all" | "user" | "changed";

/** 视觉缩进最多钳到这一级（**只影响渲染，不改 `TreeRow.depth` 的语义**）。
 *
 *  为什么需要钳制：单链会话每轮再深一层（后端每条消息的 parent_seq = 当时的 leaf），
 *  而缩进按 depth 线性乘算——一条 20 轮的会话就把标签挤到面板外（288px 面板、每级
 *  13px，depth≈13 起内容实质消失，且 `.stree-list` 没有横向滚动可退）。
 *
 *  取值依据（288px 面板，逐层扣减）：
 *    288 − 面板左右 padding 12 − 缩进基准 6 − 13·min(depth, 8) − 行体 padding 10
 *        − caret 11 − 轮次徽标 16 − 2 个 gap 10
 *    → depth 0 标签 ≈ 223px；钳制后最深层（任何 depth ≥ 8）标签 ≈ **119px**
 *      （再加「2 分支」≈ 33 + 间距 5 → 81px；再加「你在这里」≈ 46 + 间距 5 → 30px，
 *      极端徽标堆满时由 `.stree-list` 的横向滚动兜底）。
 *  再抬 MAX 会把最坏情况压到 1~2 个字（MAX=10 时标签 ≈ 93px、全徽标为负），
 *  再降则层级感不够，故取 8。 */
export const STREE_MAX_VISUAL_DEPTH = 8;

/** 视觉深度 = min(depth, STREE_MAX_VISUAL_DEPTH)。
 *
 *  **纯视觉量**：CSS 的 padding-left 与连接线（::before/::after）都只认它，于是所有
 *  depth ≥ MAX 的行共用同一列缩进、连接线在钳制边界处仍然连贯（不会错位/断线）。
 *  `depth` 本身照旧承载「成行祖先数」（分支/路径信息），不改。 */
export function visualDepth(depth: number): number {
  if (!Number.isFinite(depth) || depth <= 0) return 0;
  return Math.min(Math.floor(depth), STREE_MAX_VISUAL_DEPTH);
}

/** 是否被钳制（depth > MAX → 渲染一个「还有更深层级」的轻量标记，不静默压平）。 */
export function depthClamped(depth: number): boolean {
  return Number.isFinite(depth) && depth > STREE_MAX_VISUAL_DEPTH;
}

/** 面板要画的一行（= 一个成行节点 + 折叠进来的统计）。 */
export interface TreeRow {
  seq: number;
  /** 成行祖先的 seq（0 = 树根）。 */
  parentSeq: number;
  /** 从根到该行的**成行**祖先数（折叠掉的中间节点不计——否则缩进会跳 3~5 级）。 */
  depth: number;
  kind: "user" | "checkpoint" | "notice";
  /** 单行摘要（复用 outlineLabel，与右侧既有面板同一份实现）。 */
  label: string;
  /** 轮次序号（user/notice 有；checkpoint 没有——它不是一轮）。 */
  turn?: number;
  /** 最近的 user 行（含自己）：跳转锚点只能落在 user 块上（只有它带 seq 的块）。
   *  0/缺省 = 这条链上没有用户消息（会话开始前的孤儿），该行不可点。 */
  userSeq: number;
  /** 成行的直接子行数（>1 = 分支点）。 */
  children: number;
  /** 是否在当前 leaf 路径上（false = 废弃分支 → 灰化但不隐藏）。 */
  onPath: boolean;
  /** === 当前 leaf（「你在这里」）。 */
  isLeaf: boolean;
  /** 该轮可还原的文件改动数（含子会话；0 = 没有，面板不渲染徽标）。 */
  files: number;
  /** 其中来自子会话的改动数（files 的子集）。 */
  childFiles: number;
  /** 该轮无法快照的写（bash/MCP/自定义工具，含子会话里的 bash；0 = 没有）。 */
  external: number;
  /** checkpoint 的影子条数（0 = 不是检查点）。 */
  shadowed: number;
  /** 从这一行**之后**（seq 更大）还能还原的文件改动数（含子会话）。
   *  这是「切到此 + 还原文件」真正会动到的量——后端还原的是 `owner_seq > seq`
   *  （严格大于：这一行自己的改动属于「目标状态」，不该被还原），所以**不含本行**
   *  （docs §B.2 的切段口径）。0 = 没有可还原的，面板不显示文件按钮。 */
  rollbackFiles: number;
  /** 上面那批里来自子会话的改动数。 */
  rollbackChildFiles: number;
  /** 从这一行之后无法快照的写（bash / MCP / 自定义工具）——确认框要列它。 */
  rollbackExternal: number;
  /** 有子行（可以折叠/展开）。 */
  collapsible: boolean;
  /** 当前是折叠态（其下后代被隐藏）。 */
  collapsed: boolean;
  /** 被这一行折叠掉的后代行数（渲染「▸ 还有 N 个」）。 */
  hidden: number;
  /** 过滤时仅作为**路径骨架**保留（不命中过滤条件）：低对比度显示、不可点。 */
  skeleton: boolean;
  /** 是否是最后一个子行（CSS 画 └ 而不是 ├）。 */
  last: boolean;
  /** 畸形数据（父节点缺失/成环）——如实标出，不隐藏节点。 */
  orphan: boolean;
}

/** 折叠/展开状态（两个集合而不是一个"翻转"集合）：两者的**默认值相反**——
 *  当前路径上的行默认展开、废弃分支默认只露首行（docs §D.3 规则 4）。用一个集合
 *  表达会让「切分支之后同一个 seq 的默认态反转」变成静默的行为改变。 */
export interface TreeViewState {
  filter?: TreeFilter;
  /** 显式折叠的行（只对 on_path 行有意义）。 */
  collapsed?: ReadonlySet<number>;
  /** 显式展开的行（只对 off_path 行有意义：废弃分支默认只显示首行）。 */
  expanded?: ReadonlySet<number>;
}

const EMPTY_SET: ReadonlySet<number> = new Set<number>();

/** 成行判定：assistant / tool 折叠（见文件头）。 */
function isRowKind(kind: string): kind is TreeRow["kind"] {
  return kind === "user" || kind === "checkpoint" || kind === "notice";
}

/** 行的显示文案：检查点与提示条有自己的语义名，用户消息用后端给的摘要
 *  （后端已截到 ≤120 字符，这里再按侧栏宽度折一次——复用 outlineLabel）。 */
function rowLabel(n: TreeNode): string {
  if (n.kind === "checkpoint") {
    const shadowed = n.shadowed ?? 0;
    return shadowed > 0 ? `已压缩历史 · 影子 ${shadowed} 条` : "已压缩历史";
  }
  if (n.kind === "notice") return outlineLabel(n.summary ?? "", 48) || "系统提示";
  return outlineLabel(n.summary ?? "", 48);
}

/** 树 → 面板行（按 seq 升序 = 时间顺序，与现有面板一致：用户的心智模型是时间线）。 */
export function flattenTree(tree: ChatTreeResult | null | undefined, view: TreeViewState = {}): TreeRow[] {
  if (!tree) return [];
  const nodes = tree.nodes ?? [];
  const filter = view.filter ?? "all";
  const collapsed = view.collapsed ?? EMPTY_SET;
  const expanded = view.expanded ?? EMPTY_SET;

  const bySeq = new Map<number, TreeNode>();
  for (const n of nodes) bySeq.set(n.seq, n);

  /** 最近成行祖先（含自己）：0 = 这条链上没有成行节点。
   *  环/断链都钳住（畸形数据不炸——与后端 pathRows 的同一条纪律）。 */
  const nearestRowCache = new Map<number, number>();
  const nearestRow = (seq: number): number => {
    if (seq <= 0) return 0;
    const cached = nearestRowCache.get(seq);
    if (cached !== undefined) return cached;
    let cur = seq;
    let result = 0;
    const seen = new Set<number>();
    for (let guard = 0; cur > 0 && guard <= nodes.length + 1; guard++) {
      if (seen.has(cur)) break; // 环
      seen.add(cur);
      const n = bySeq.get(cur);
      if (!n) break; // 断链（父节点缺失）
      if (isRowKind(n.kind)) {
        result = cur;
        break;
      }
      cur = n.parentSeq;
    }
    nearestRowCache.set(seq, result);
    return result;
  };

  // ① 统计折叠：非成行节点的 files/childFiles/external 累加到它的最近成行祖先上。
  const stats = new Map<number, { files: number; childFiles: number; external: number }>();
  for (const n of nodes) {
    if (isRowKind(n.kind)) {
      stats.set(n.seq, { files: n.files ?? 0, childFiles: n.childFiles ?? 0, external: n.external ?? 0 });
    }
  }
  for (const n of nodes) {
    if (isRowKind(n.kind)) continue;
    if (!n.files && !n.childFiles && !n.external) continue;
    const owner = nearestRow(n.seq);
    if (owner === 0) continue; // 没有成行祖先：无处归属（不编一个孤儿行）
    const s = stats.get(owner);
    if (!s) continue;
    s.files += n.files ?? 0;
    s.childFiles += n.childFiles ?? 0;
    s.external += n.external ?? 0;
  }

  // ② 行父子关系（成行之后：中间折叠节点被跳过）。
  const rowParent = new Map<number, number>();
  const childCount = new Map<number, number>();
  const rowOrder: number[] = [];
  for (const n of nodes) {
    if (!isRowKind(n.kind)) continue;
    rowOrder.push(n.seq);
    const parent = nearestRow(n.parentSeq);
    rowParent.set(n.seq, parent);
    if (parent > 0) childCount.set(parent, (childCount.get(parent) ?? 0) + 1);
  }

  // ③ 「最近的 user 行」：父行 seq 恒小于子行（seq 是插入序），一遍扫完。
  //    跳转锚点只能落在 user 块上（只有它带 seq 的块有 data-uid），非 user 行靠它
  //    回落到自己的 user 祖先。（depth 在 ⑦ 里按**实际渲染的父链**算，不在这里算
  //    ——过滤会改变父链，先算一遍会得到跳级的缩进。）
  const userSeqOf = new Map<number, number>();
  for (const seq of rowOrder) {
    const n = bySeq.get(seq)!;
    const parent = rowParent.get(seq) ?? 0;
    const parentUser = parent > 0 ? userSeqOf.get(parent) ?? 0 : 0;
    userSeqOf.set(seq, n.kind === "user" ? seq : parentUser);
  }

  // ④ 过滤：命中集合（hits）+ 命中项的祖先（骨架）。**过滤时忽略折叠**——过滤是
  //    "找"，把命中项藏进折叠分支里等于让过滤失效（D.6 只要求保留分支骨架）。
  //
  //    两条纪律：
  //    ① 命中项与骨架分开存：骨架低对比度、不可点，而一个节点可能既是命中项又是
  //       别的命中项的祖先——命中优先；
  //    ② **checkpoint / notice 不做骨架**（docs §D.6：「只看用户消息」= 只留 user 行
  //       + 分支骨架，checkpoint/notice 隐藏）。它们不是分支结构的一部分，而是轮内
  //       的标记——把它们当骨架留下会让「只看用户消息」里混进压缩标记。
  const filtering = filter !== "all";
  const hits = new Set<number>();
  const included = new Set<number>();
  if (filtering) {
    const matches = (kind: string, s: { files: number; external: number }) =>
      filter === "user" ? kind === "user" : s.files > 0 || s.external > 0;
    for (const seq of rowOrder) {
      const n = bySeq.get(seq)!;
      const s = stats.get(seq)!;
      if (matches(n.kind, s)) {
        hits.add(seq);
        included.add(seq);
      }
    }
    // 祖先一起保留（否则缩进失去意义、看不出这个节点在哪条分支上）；checkpoint /
    // notice 跳过但继续往上找——它们的祖先仍要保留。
    for (const seq of [...hits]) {
      for (let p = rowParent.get(seq) ?? 0, guard = 0; p > 0 && guard <= rowOrder.length; guard++) {
        if (included.has(p)) break;
        const pn = bySeq.get(p)!;
        if (pn.kind === "user") included.add(p);
        p = rowParent.get(p) ?? 0;
      }
    }
  }

  /** 行是否参与本次渲染（过滤时只留命中项与骨架）。 */
  const includedRow = (seq: number) => (filtering ? included.has(seq) : true);

  /** 渲染时的父行：最近的**参与渲染**的祖先（过滤掉的 checkpoint/notice 被跳过，
   *  否则它们的子行会挂在一个不存在的父上——缩进与连接线都会画错）。 */
  const shownParent = new Map<number, number>();
  for (const seq of rowOrder) {
    if (!includedRow(seq)) continue;
    let p = rowParent.get(seq) ?? 0;
    for (let guard = 0; p > 0 && !includedRow(p) && guard <= rowOrder.length; guard++) {
      p = rowParent.get(p) ?? 0;
    }
    shownParent.set(seq, includedRow(p) ? p : 0);
  }

  // ⑤ 可见性：on_path 行默认展开；off_path 分支默认只露**首行**（灰化但不隐藏）。
  const expandedRow = (seq: number): boolean => {
    const n = bySeq.get(seq);
    if (!n) return true;
    // 废弃分支默认折叠（中段藏起来），当前路径默认展开——两者的默认值相反。
    return n.onPath ? !collapsed.has(seq) : expanded.has(seq);
  };
  const visible = new Map<number, boolean>();
  for (const seq of rowOrder) {
    if (!includedRow(seq)) continue;
    const parent = shownParent.get(seq) ?? 0;
    // 过滤时忽略折叠（过滤是"找"，把命中项藏进折叠分支里等于让过滤失效）
    const parentOpen = filtering || parent <= 0 ? true : expandedRow(parent);
    const parentVisible = parent > 0 ? visible.get(parent) === true : true;
    visible.set(seq, parentVisible && parentOpen);
  }

  // ⑥ 折叠计数：被隐藏的行归到它最近的**可见**祖先头上（「▸ 还有 N 个」）。
  const hiddenCount = new Map<number, number>();
  for (const seq of rowOrder) {
    if (visible.get(seq) === true || !includedRow(seq)) continue;
    for (let p = shownParent.get(seq) ?? 0, guard = 0; p > 0 && guard <= rowOrder.length; guard++) {
      if (visible.get(p) === true) {
        hiddenCount.set(p, (hiddenCount.get(p) ?? 0) + 1);
        break;
      }
      p = shownParent.get(p) ?? 0;
    }
  }

  // ⑦ 缩进深度：**按实际渲染的父链**数（过滤掉的行不计——否则缩进会跳级）。
  const shownDepth = new Map<number, number>();
  for (const seq of rowOrder) {
    if (!includedRow(seq)) continue;
    const parent = shownParent.get(seq) ?? 0;
    shownDepth.set(seq, parent > 0 ? (shownDepth.get(parent) ?? 0) + 1 : 0);
  }

  // ⑧ 回滚量（后缀和）：倒序累加「这一行之后」的改动。
  //
  //    两条口径必须与后端一致（docs §B.2 / §C.2.1）：
  //    ① 按 **seq** 而不是按可见性——回滚作用域是整条会话的 owner_seq 区间，与面板
  //       当前折叠/过滤了什么无关（折叠一个分支不该让"将还原 N 个文件"变成 0）；
  //    ② 按**原始节点的 seq** 而不是成行后的统计——快照的 owner_seq 是「携带 tool_call
  //       的 assistant 消息」的 seq，它常常比它的 user 行大（那一轮的改动挂在 assistant
  //       上）。用成行统计（已折到 user 行）会让「这一行之后的改动」漏掉本轮自己的
  //       assistant——后端却会还原它（切段是 `owner_seq > seq`）。
  const suffix = new Map<number, { files: number; childFiles: number; external: number }>();
  const bySeqAsc = [...nodes].sort((a, b) => a.seq - b.seq);
  let accFiles = 0;
  let accChildFiles = 0;
  let accExternal = 0;
  for (let i = bySeqAsc.length - 1; i >= 0; i--) {
    const n = bySeqAsc[i];
    suffix.set(n.seq, { files: accFiles, childFiles: accChildFiles, external: accExternal });
    accFiles += n.files ?? 0;
    accChildFiles += n.childFiles ?? 0;
    accExternal += n.external ?? 0;
  }

  // ⑨ 组装（seq 升序；last = 同一父行下的最后一个**可见**行，CSS 据此画 └）。
  const lastOfParent = new Map<number, number>();
  for (const seq of rowOrder) {
    if (visible.get(seq) !== true) continue;
    lastOfParent.set(shownParent.get(seq) ?? 0, seq);
  }
  const rows: TreeRow[] = [];
  for (const seq of rowOrder) {
    if (visible.get(seq) !== true) continue;
    const n = bySeq.get(seq)!;
    const s = stats.get(seq)!;
    const parent = shownParent.get(seq) ?? 0;
    const kids = childCount.get(seq) ?? 0;
    const isCollapsed = kids > 0 && !filtering && !expandedRow(seq);
    rows.push({
      seq,
      parentSeq: parent,
      depth: shownDepth.get(seq) ?? 0,
      kind: n.kind as TreeRow["kind"],
      label: rowLabel(n),
      ...(n.turn && n.turn > 0 ? { turn: n.turn } : {}),
      userSeq: userSeqOf.get(seq) ?? 0,
      children: kids,
      onPath: n.onPath,
      isLeaf: seq === tree.leafSeq,
      files: s.files,
      childFiles: s.childFiles,
      external: s.external,
      shadowed: n.shadowed ?? 0,
      rollbackFiles: suffix.get(seq)?.files ?? 0,
      rollbackChildFiles: suffix.get(seq)?.childFiles ?? 0,
      rollbackExternal: suffix.get(seq)?.external ?? 0,
      collapsible: kids > 0,
      collapsed: isCollapsed,
      hidden: isCollapsed ? hiddenCount.get(seq) ?? 0 : 0,
      // 骨架 = 只因后代命中被保留（低对比度、不可点）；命中项自己不是骨架。
      skeleton: filtering && !hits.has(seq),
      last: (lastOfParent.get(parent) ?? 0) === seq,
      orphan: n.orphan === true,
    });
  }
  return rows;
}

/** 树里的最大 seq（= 「回到最新」的落点；0 = 空树）。
 *
 *  为什么不用 leafSeq：leaf 可能是被回退过的位置（用户正停在中间），而「回到最新」
 *  要的是"这条会话长到哪儿了"——树里最大的 seq 才是它（docs §0 的「回到最新不是
 *  特殊值，而是传树里最大的 seq」）。**绝不传 0**：0 是「空路径」，会把用户送到
 *  第一条消息之前。 */
export function latestSeq(tree: ChatTreeResult | null | undefined): number {
  let max = 0;
  for (const n of tree?.nodes ?? []) if (n.seq > max) max = n.seq;
  return max;
}

/** 截断提示（docs §D.3：`truncated=true` 显示「更早的 N 个节点未加载」）。 */
export function truncationNote(tree: ChatTreeResult | null | undefined): { count: number; text: string } | null {
  if (!tree?.truncated) return null;
  const count = Math.max(0, tree.total - tree.nodes.length);
  // 数字为 0（total 缺失/后端补齐祖先导致节点数等于 total）时**不编一个 0**：
  // 说"还有更早的节点没加载"，不说"还有 0 个"。
  return count > 0
    ? { count, text: `更早的 ${count} 个节点未加载` }
    : { count: 0, text: "更早的节点未加载" };
}

/** 文件徽标：0 时返回 null（**不显示「0 文件」**——零值 = 没有这个事实）。
 *  含子会话时追加「· N 子 Agent」（用户要知道这一轮的文件不全是自己改的）。 */
export function filesBadge(row: Pick<TreeRow, "files" | "childFiles">): string | null {
  if (!row.files || row.files <= 0) return null;
  return row.childFiles && row.childFiles > 0
    ? `${row.files} 文件 · ${row.childFiles} 子 Agent`
    : `${row.files} 文件`;
}

/** 外部操作徽标（bash/MCP/自定义工具改的东西**回滚不了**——这是用户最容易被骗的
 *  一处，所以用 ⚠️ 且常显）。0 时返回 null。 */
export function externalBadge(row: Pick<TreeRow, "external">): string | null {
  return row.external && row.external > 0 ? `⚠ ${row.external}` : null;
}

/** 分支标记：直接子行 > 1 时显示「N 分支」。 */
export function branchBadge(row: Pick<TreeRow, "children">): string | null {
  return row.children > 1 ? `${row.children} 分支` : null;
}

/** seq → 块 uid 的映射（跳转用）。
 *
 *  **只有 kind === "user" 的块带 seq**（dispatch/assistant/tool 块没有）——所以映射
 *  表只收 user 块；非 user 节点单击时用 TreeRow.userSeq 回落到它的 user 祖先。 */
export function userSeqUid(blocks: ThreadBlock[]): Map<number, number> {
  const map = new Map<number, number>();
  for (const b of blocks) {
    if (b.kind === "user" && typeof b.seq === "number") map.set(b.seq, b.uid);
  }
  return map;
}
