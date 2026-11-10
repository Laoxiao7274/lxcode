// 会话树面板的纯函数（components/panels/tree-model.ts）——user-only 纯导航 + 平级列表版。
//
// 为什么值得单独钉住：行形状的正确性全在几条容易写错的规则里——
//   ① 每条 user 一行、parentSeq 就是树上的父子（后端已按「最近前驱 user」给出），
//     但渲染是**平级列表**（2026-12 用户拍板）：按 seq 平铺，不做缩进层级；
//   ② 徽标 omitempty 语义：0 不显示（**不显示「0 文件」**）；
//   ③ off_path 行 = 「在分支上」（灰化但不隐藏；不标 leaf）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  branchBadge,
  filesBadge,
  flattenTree,
  latestSeq,
  onBranchBadge,
  truncationNote,
  userSeqUid,
} from '../src/components/panels/tree-model.ts';

/** 造节点的小工具（只填判定用得到的字段）。 */
const node = (seq, parentSeq, kind, over = {}) => ({
  seq, parentSeq, kind, children: 0, onPath: true, ...over,
});

/** 造一棵带分支的树（user-only：每行都是 user）：
 *
 *  1(user) ─ 3(user) ─ 5(user)   ← 当前路径（leaf=5）
 *              └─ 7(user)        ← 另一条分支（在 3 之后分叉，off_path）
 */
function branchyTree(leafSeq = 5) {
  return {
    leafSeq,
    total: 4,
    nodes: [
      node(1, 0, 'user', { turn: 1, summary: '第一条', children: 1 }),
      node(3, 1, 'user', { turn: 2, summary: '第二条', children: 2, files: 2 }),
      node(5, 3, 'user', { turn: 3, summary: '第三条' }),
      node(7, 3, 'user', { turn: 3, summary: '分支上的', onPath: false }),
    ],
  };
}

// ---------- 1. 成行粒度与平铺 ----------

test('每条 user 一行：树上只剩 user，parentSeq 就是树上的父子', () => {
  const rows = flattenTree(branchyTree());
  assert.deepEqual(rows.map((r) => r.seq), [1, 3, 5, 7], '四条 user 全部成行（off_path 不隐藏）');
  assert.deepEqual(rows.map((r) => r.kind), ['user', 'user', 'user', 'user'], 'kind 恒 user');
  assert.deepEqual(
    rows.map((r) => r.parentSeq),
    [0, 1, 3, 3],
    '5 与 7 都挂在 3 下（后端给的树上父子）',
  );
});

test('平级列表：按 seq 平铺，不携带 depth（缩进层级已删——2026-12 用户拍板）', () => {
  const rows = flattenTree(branchyTree());
  assert.deepEqual(rows.map((r) => r.seq), [1, 3, 5, 7], '按 seq 升序平铺，分叉的行照序排');
  for (const r of rows) {
    assert.equal('depth' in r, false, `行 ${r.seq} 不再有 depth 字段`);
    assert.equal('children' in r === false || typeof r.children === 'number', true);
  }
  assert.equal(
    rows.some((r) => 'depth' in r),
    false,
    '没有任何行带 depth——渲染层无从缩进',
  );
});

// ---------- 2. on_path / leaf / 分支标记 ----------

test('当前 leaf 只标一个；另一条分支灰化标记「在分支上」但不隐藏，不标 leaf', () => {
  const rows = flattenTree(branchyTree(5));
  assert.deepEqual(rows.filter((r) => r.isLeaf).map((r) => r.seq), [5], 'leaf 只有一个');
  const offPath = rows.filter((r) => !r.onPath).map((r) => r.seq);
  assert.deepEqual(offPath, [7], '另一条分支的行可见且标为 off_path（不隐藏）');
  assert.equal(rows.find((r) => r.seq === 7).onBranch, true, 'onBranch 显示语义位');
  assert.equal(onBranchBadge(rows.find((r) => r.seq === 7)), '在分支上');
  assert.equal(onBranchBadge(rows.find((r) => r.seq === 5)), null, '路径上的行不带徽标');
  assert.equal(rows.find((r) => r.seq === 7).isLeaf, false, 'off_path 行不标 leaf');
});

test('分支点标记：多个直接子行时 children>1', () => {
  const rows = flattenTree(branchyTree());
  const second = rows.find((r) => r.seq === 3);
  assert.equal(second.children, 2, '第 2 轮有两个后继分支（5 与 7）');
  assert.equal(branchBadge(second), '2 分支');
  assert.equal(branchBadge(rows.find((r) => r.seq === 1)), null, '单子节点不显示分支标记');
});

// ---------- 3. leaf 不在 user 行上（畸形/边界） ----------

test('leafSeq 不是 user 行（畸形/空树）时不标任何「你在这里」', () => {
  const rows = flattenTree({ leafSeq: 2, total: 1, nodes: [node(1, 0, 'user', { turn: 1 })] });
  assert.equal(rows.some((r) => r.isLeaf), false, 'leaf 指向不存在的行 → 不编一个');
  assert.deepEqual(flattenTree(null), [], 'null 树 → 空行');
  assert.deepEqual(flattenTree({ leafSeq: 0, total: 0, nodes: [] }), [], '空树 → 空行');
});

// ---------- 4. 徽标（omitempty 语义：0 不显示） ----------

test('文件徽标：0 不渲染，含子 Agent 时标注', () => {
  assert.equal(filesBadge({ files: 0, childFiles: 0 }), null, '0 文件不显示（零值 = 没有这个事实）');
  assert.equal(filesBadge({ files: 3, childFiles: 0 }), '3 文件');
  assert.equal(filesBadge({ files: 3, childFiles: 1 }), '3 文件 · 1 子 Agent');
});

test('分支徽标：0/1 不显示', () => {
  assert.equal(branchBadge({ children: 0 }), null);
  assert.equal(branchBadge({ children: 3 }), '3 分支');
});

// ---------- 5. 截断提示与「回到最新」 ----------

test('截断提示：total - nodes.length（不静默截断）', () => {
  const tree = { leafSeq: 5, total: 2000, truncated: true, nodes: [node(1, 0, 'user', { turn: 1 })] };
  const note = truncationNote(tree);
  assert.equal(note.count, 1999);
  assert.equal(note.text, '更早的 1999 条消息未加载');
  assert.equal(truncationNote({ ...tree, truncated: false }), null, '没截断就没有提示');
});

test('截断但节点数已齐：不编一个「0 条」', () => {
  const tree = { leafSeq: 1, total: 1, truncated: true, nodes: [node(1, 0, 'user', { turn: 1 })] };
  const note = truncationNote(tree);
  assert.equal(note.count, 0);
  assert.equal(note.text, '更早的消息未加载', '不显示「还有 0 条」这种假数字');
});

test('「回到最新」的落点 = 树里最大的 seq（不是 0——0 是空路径）', () => {
  assert.equal(latestSeq(branchyTree()), 7, '另一条分支的最大 seq 也是「最新」');
  assert.equal(latestSeq({ leafSeq: 3, total: 0, nodes: [] }), 0, '空树 = 0');
  assert.equal(latestSeq(null), 0, 'null 树 = 0（调用方据此禁用动作，不许传 0 去 setLeaf）');
});

// ---------- 6. 畸形数据不炸 ----------

test('父节点缺失（orphan）不丢节点：挂 root 并标 orphan', () => {
  const tree = {
    leafSeq: 3,
    total: 3,
    nodes: [
      node(1, 0, 'user', { turn: 1 }),
      node(2, 99, 'user', { turn: 2, orphan: true }), // 父 99 不存在
      node(3, 2, 'user', { turn: 3 }),
    ],
  };
  const rows = flattenTree(tree);
  assert.deepEqual(rows.map((r) => r.seq), [1, 2, 3], '畸形不丢节点');
  assert.equal(rows.find((r) => r.seq === 2).orphan, true, 'orphan 标出');
  assert.equal(rows.find((r) => r.seq === 2).parentSeq, 0, '缺父钳到 root');
});

// ---------- 7. seq → uid 映射 ----------

test('seq → uid 只收 user 块（dispatch/assistant 块没有 seq）', () => {
  const blocks = [
    { kind: 'user', uid: 11, text: 'a', seq: 1 },
    { kind: 'assistant', uid: 12, content: 'b', reasoning: '', streaming: false },
    { kind: 'user', uid: 13, text: 'c', seq: 2 },
    { kind: 'dispatch', uid: 14 },
  ];
  const map = userSeqUid(blocks);
  assert.deepEqual([...map.entries()], [[1, 11], [2, 13]]);
});
