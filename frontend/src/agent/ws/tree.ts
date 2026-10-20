// chat.tree 的 wire → 前端形态映射（**纯函数**：无 I/O、无 this，可单测）。
//
// 为什么单独一层而不是在 WSAgent 里就地展开：字段名错一个不会编译报错（wire 是
// snake_case、前端是 camelCase），而 `parentSeq` 读错一位的后果是整棵树拍平成一
// 条直线——界面上看起来"有内容"，只是分支全没了。映射只有这一份实现，测试直测它。
//
// 容错纪律（与 snapshotFromWire / parseDispatchArgs 同一条）：畸形载荷不许抛。
// 树骨架来自后端库表，残缺记录是既成事实——抛异常的下场是右栏整块白屏。
import type { ChatTreeResult, TreeNode } from "../../shared/types";

/** 一个节点的合法 kind（协议值域，见 protocol.TreeNode.Kind 的注释）。
 *  未知 kind 不丢节点：归到 "assistant"（它本来也不成行，见 tree-model 的粒度规则）
 *  ——丢掉节点会让它的子节点变成孤儿，那才是真正的信息丢失。 */
const KINDS = new Set(["user", "assistant", "tool", "checkpoint", "notice"]);

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

/** wire → ChatTreeResult。nodes 保持后端给的顺序（按 seq 升序——前端的排序规则
 *  依赖它，不在这里重排：重排会让「谁先谁后」有两个事实源）。 */
export function treeFromWire(r: unknown): ChatTreeResult {
  const t = (r ?? {}) as { leaf_seq?: unknown; nodes?: unknown; total?: unknown; truncated?: unknown };
  const rawNodes = Array.isArray(t.nodes) ? t.nodes : [];
  const nodes: TreeNode[] = [];
  for (const raw of rawNodes) {
    if (typeof raw !== "object" || raw === null) continue;
    const n = raw as Record<string, unknown>;
    const seq = num(n.seq);
    // seq = 0 是「没有节点身份」（root 用 0 表示父指针，节点自己不该是 0）——
    // 跳过它而不是给个假 seq：假 seq 会被当成跳转锚点，点下去静默跳到别处。
    if (seq <= 0) continue;
    const kind = typeof n.kind === "string" && KINDS.has(n.kind) ? n.kind : "assistant";
    const node: TreeNode = {
      seq,
      parentSeq: num(n.parent_seq),
      kind,
      children: num(n.children),
      onPath: Boolean(n.on_path),
    };
    // 可选键：缺席就是"未知/零"——**不写这个键**（本仓库 wire 的统一约定：
    // 键缺席 = 未知，0 值不落成假事实）。
    if (typeof n.turn === "number" && n.turn > 0) node.turn = n.turn;
    if (typeof n.summary === "string" && n.summary !== "") node.summary = n.summary;
    if (n.orphan === true) node.orphan = true;
    if (typeof n.files === "number" && n.files > 0) node.files = n.files;
    if (typeof n.child_files === "number" && n.child_files > 0) node.childFiles = n.child_files;
    if (typeof n.external === "number" && n.external > 0) node.external = n.external;
    if (typeof n.shadowed === "number" && n.shadowed > 0) node.shadowed = n.shadowed;
    nodes.push(node);
  }
  return {
    leafSeq: num(t.leaf_seq),
    nodes,
    // total 缺席（老后端/畸形载荷）时回落节点数：它是「更早的 N 个未加载」的分母，
    // 缺了就只能说"节点不全"，不能说"还有 -N 个"。
    total: typeof t.total === "number" && t.total >= nodes.length ? t.total : nodes.length,
    ...(t.truncated === true ? { truncated: true } : {}),
  };
}
