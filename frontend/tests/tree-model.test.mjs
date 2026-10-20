// 会话树面板的纯函数（components/panels/tree-model.ts）。
//
// 为什么值得单独钉住：树形状的正确性全在几条容易写错的规则里——
//   ① 折叠掉的中间节点（assistant/tool）**不能**计入缩进深度（否则缩进跳 3~5 级）；
//   ② 文件/外部操作计数必须沿 **parent 链**折到最近的成行祖先（按 seq 找会把 B 分支
//      的工具调用算到 A 分支那一轮的账上——分支之后两者会分叉）；
//   ③ 过滤后祖先必须保留成骨架（否则缩进失去意义、看不出节点在哪条分支上）；
//   ④ 徽标 omitempty 语义：0 不显示（**不显示「0 文件」**）。
// 这几条埋在 JSX 里只能靠肉眼验收，而肉眼看不出"这一轮的文件数被算到了另一条分支"。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  branchBadge,
  externalBadge,
  filesBadge,
  flattenTree,
  latestSeq,
  truncationNote,
  userSeqUid,
} from '../src/components/panels/tree-model.ts';

/** 造节点的小工具（只填判定用得到的字段）。 */
const node = (seq, parentSeq, kind, over = {}) => ({
  seq, parentSeq, kind, children: 0, onPath: true, ...over,
});

/** 造一棵带分支的树：
 *
 *  1(user) ─ 2(assistant) ─ 3(user) ─ 4(assistant) ─ 5(user)   ← 当前路径（leaf=5）
 *                                └─ 6(assistant) ─ 7(user)      ← 废弃分支（在 3 之后分叉）
 */
function branchyTree(leafSeq = 5) {
  return {
    leafSeq,
    total: 7,
    nodes: [
      node(1, 0, 'user', { turn: 1, summary: '第一条', children: 1 }),
      node(2, 1, 'assistant', { children: 1, files: 2 }),
      node(3, 2, 'user', { turn: 2, summary: '第二条', children: 2 }),
      node(4, 3, 'assistant', { children: 1 }),
      node(5, 4, 'user', { turn: 3, summary: '第三条' }),
      node(6, 3, 'assistant', { onPath: false, children: 1, external: 1 }),
      node(7, 6, 'user', { turn: 3, summary: '废弃分支', onPath: false }),
    ],
  };
}

// ---------- 1. 成行粒度与缩进 ----------

test('assistant/tool 折叠：不成行，缩进只数成行祖先', () => {
  const rows = flattenTree(branchyTree());
  assert.deepEqual(rows.map((r) => r.seq), [1, 3, 5, 7], '只剩 user 行（含废弃分支的首行；assistant 全折叠）');
  // depth：1 是根（0）；3 是 1 的子（1）；5 是 3 的子（2）；7 是 3 的孙子（2）
  // ——中间那两条 assistant（2、4、6）都不计。
  assert.equal(rows[0].depth, 0, '根行 depth=0');
  assert.equal(rows[1].depth, 1, '第二层 depth=1（中间的 assistant 不计）');
  assert.equal(rows[2].depth, 2, '5 挂在 3 下 → depth=2');
  assert.equal(rows[3].depth, 2, '7 的成行祖先是 3（6 是 assistant）→ depth=2');
});

test('成行节点的 parentSeq 是最近的成行祖先（不是原 parent）', () => {
  const rows = flattenTree(branchyTree());
  const five = rows.find((r) => r.seq === 5);
  assert.equal(five.parentSeq, 3, '5 的原 parent 是 4（assistant），成行后挂到 3');
  const seven = rows.find((r) => r.seq === 7);
  assert.equal(seven.parentSeq, 3, '7 的原 parent 是 6（assistant），成行后挂到 3');
});

// ---------- 2. 统计折叠（沿 parent 链） ----------

test('文件/外部操作计数沿 parent 链折到最近的成行祖先', () => {
  const rows = flattenTree(branchyTree());
  const first = rows.find((r) => r.seq === 1);
  assert.equal(first.files, 2, '节点 2（assistant，files=2）的改动算到第 1 轮');
  const second = rows.find((r) => r.seq === 3);
  assert.equal(second.files, 0, '第 2 轮自己没有文件改动');
  assert.equal(second.external, 1, '节点 6（废弃分支的 assistant，external=1）算到它的成行祖先 3');
});

test('分支之后按 parent 链归属，不按 seq 找（A 分支的账不会算到 B 分支）', () => {
  // 关键构造：B 分支那个带文件改动的 assistant（seq 7）**seq 更大**，但它的父是
  // 第 2 轮的 user（seq 3）——按「seq 更小的最近一条 user」会把它算到第 3 轮（seq 5）
  // 的账上，按 parent 链才对（它是第 2 轮那条 assistant）。
  const tree = {
    leafSeq: 6,
    total: 8,
    nodes: [
      node(1, 0, 'user', { turn: 1 }),
      node(2, 1, 'assistant', { files: 1 }),
      node(3, 2, 'user', { turn: 2, children: 2 }),
      node(4, 3, 'assistant', { children: 1, files: 5 }),
      node(5, 4, 'user', { turn: 3 }),
      node(6, 5, 'assistant', { files: 7 }),
      node(7, 3, 'assistant', { onPath: false, children: 1, files: 9 }),
      node(8, 7, 'user', { turn: 3, onPath: false, summary: 'B 分支' }),
    ],
  };
  const rows = flattenTree(tree);
  assert.equal(rows.find((r) => r.seq === 1).files, 1, '第 1 轮拿到自己的 assistant 的改动');
  assert.equal(rows.find((r) => r.seq === 3).files, 14, '第 2 轮 = 两条分支的 assistant 改动之和（5+9）');
  assert.equal(rows.find((r) => r.seq === 5).files, 7, '第 3 轮只有自己分支那一份（7），不含 B 分支的 9');
});

// ---------- 3. on_path / leaf / 分支标记 ----------

test('当前 leaf 只标一个；废弃分支灰化标记但不隐藏（首行常显）', () => {
  const rows = flattenTree(branchyTree(5));
  assert.deepEqual(rows.filter((r) => r.isLeaf).map((r) => r.seq), [5], 'leaf 只有一个');
  const offPath = rows.filter((r) => !r.onPath).map((r) => r.seq);
  assert.deepEqual(offPath, [7], '废弃分支的首行可见且标为 off_path（不隐藏）');
  assert.ok(!rows.some((r) => r.seq === 6), '废弃分支的中段（assistant）被默认折叠');
});

test('分支点标记：多个直接子行时 children>1', () => {
  const rows = flattenTree(branchyTree());
  const second = rows.find((r) => r.seq === 3);
  assert.equal(second.children, 2, '第 2 轮有两个后继分支（5 与 7）');
  assert.equal(branchBadge(second), '2 分支');
  assert.equal(branchBadge(rows.find((r) => r.seq === 1)), null, '单子节点不显示分支标记');
});

test('废弃分支展开后能看到中段（expanded 集合）', () => {
  const rows = flattenTree(branchyTree(), { expanded: new Set([3]) });
  assert.ok(rows.some((r) => r.seq === 7), '展开之后废弃分支的首行照旧可见');
  // 3 在 on_path 上，它的折叠由 collapsed 控制；off_path 的 3 不存在——这里验证
  // 「展开父行」不会误伤当前路径（5 仍在）
  assert.ok(rows.some((r) => r.seq === 5), '当前路径不受影响');
});

test('on_path 行默认展开、可折叠（collapsed 集合），折叠时报告隐藏行数', () => {
  const rows = flattenTree(branchyTree(), { collapsed: new Set([3]) });
  assert.deepEqual(rows.map((r) => r.seq), [1, 3], '折叠第 2 轮之后只剩它自己与其祖先');
  const second = rows.find((r) => r.seq === 3);
  assert.equal(second.collapsed, true);
  // 隐藏的是 5 与 7 两行（6 是 assistant，本来就不成行）
  assert.equal(second.hidden, 2, '隐藏了 5/7 两行');
});

// ---------- 4. 过滤三档 ----------

test('过滤「只看用户消息」：assistant 行消失，checkpoint 也消失', () => {
  const tree = {
    leafSeq: 4,
    total: 4,
    nodes: [
      node(1, 0, 'user', { turn: 1 }),
      node(2, 1, 'checkpoint', { shadowed: 3 }),
      node(3, 2, 'user', { turn: 2 }),
      node(4, 3, 'assistant'),
    ],
  };
  const rows = flattenTree(tree, { filter: 'user' });
  assert.deepEqual(rows.map((r) => r.kind), ['user', 'user'], '只剩两条用户消息');
  assert.deepEqual(rows.map((r) => r.seq), [1, 3], 'checkpoint 不做骨架（D.6：只留 user 行 + 分支骨架）');
});

test('过滤「只看有改动」：只留 files>0 或 external>0 的轮 + 祖先骨架', () => {
  const tree = {
    leafSeq: 7,
    total: 7,
    nodes: [
      node(1, 0, 'user', { turn: 1 }),
      node(2, 1, 'assistant'),
      node(3, 2, 'user', { turn: 2 }),
      node(4, 3, 'assistant'),
      node(5, 4, 'user', { turn: 3 }),
      // 第 3 轮自己的 assistant 带文件改动（按 parent 链折到 seq 5）
      node(6, 5, 'assistant', { files: 2, children: 1 }),
      node(7, 6, 'user', { turn: 4 }),
    ],
  };
  const rows = flattenTree(tree, { filter: 'changed' });
  // 只留命中项（第 3 轮，seq 5）+ 它的**祖先**（1、3）。后继（7）不保留——过滤是
  // 「找改动在哪一轮」，把命中项之后的行也拉进来会让结果退化成整条时间线。
  assert.deepEqual(rows.map((r) => r.seq), [1, 3, 5], '命中项 5 + 祖先 1/3');
  const skeleton = rows.filter((r) => r.skeleton).map((r) => r.seq);
  assert.deepEqual(skeleton, [1, 3], '祖先渲染成骨架（低对比度、不可点）');
  assert.equal(rows.find((r) => r.seq === 5).skeleton, false, '命中项不是骨架');
});

test('过滤不改变 leaf/on_path 判定（纯展示层筛选）', () => {
  const tree = branchyTree(5);
  const all = flattenTree(tree);
  const filtered = flattenTree(tree, { filter: 'user' });
  assert.equal(all.find((r) => r.seq === 5)?.isLeaf, true);
  assert.equal(filtered.find((r) => r.seq === 5)?.isLeaf, true, '过滤后 leaf 判定不变');
  assert.equal(filtered.find((r) => r.seq === 7)?.onPath, false, '过滤后 on_path 判定不变');
});

// ---------- 5. 徽标（omitempty 语义：0 不显示） ----------

test('文件徽标：0 不渲染，含子 Agent 时标注', () => {
  assert.equal(filesBadge({ files: 0, childFiles: 0 }), null, '0 文件不显示（零值 = 没有这个事实）');
  assert.equal(filesBadge({ files: 3, childFiles: 0 }), '3 文件');
  assert.equal(filesBadge({ files: 3, childFiles: 1 }), '3 文件 · 1 子 Agent');
  assert.equal(filesBadge({ files: 3, childFiles: 3 }), '3 文件 · 3 子 Agent');
});

test('外部操作徽标：0 不渲染，>0 带 ⚠️', () => {
  assert.equal(externalBadge({ external: 0 }), null);
  assert.equal(externalBadge({ external: 2 }), '⚠ 2');
});

// ---------- 6. 截断提示与「回到最新」 ----------

test('截断提示：total - nodes.length（不静默截断）', () => {
  const tree = { leafSeq: 5, total: 2000, truncated: true, nodes: [node(1, 0, 'user', { turn: 1 })] };
  const note = truncationNote(tree);
  assert.equal(note.count, 1999);
  assert.equal(note.text, '更早的 1999 个节点未加载');
  assert.equal(truncationNote({ ...tree, truncated: false }), null, '没截断就没有提示');
});

test('截断但节点数已齐（后端补齐祖先）：不编一个「0 个」', () => {
  const tree = { leafSeq: 1, total: 1, truncated: true, nodes: [node(1, 0, 'user', { turn: 1 })] };
  const note = truncationNote(tree);
  assert.equal(note.count, 0);
  assert.equal(note.text, '更早的节点未加载', '不显示「还有 0 个」这种假数字');
});

test('「回到最新」的落点 = 树里最大的 seq（不是 0——0 是空路径）', () => {
  assert.equal(latestSeq(branchyTree()), 7, '废弃分支的最大 seq 也是「最新」');
  assert.equal(latestSeq({ leafSeq: 3, total: 0, nodes: [] }), 0, '空树 = 0');
  assert.equal(latestSeq(null), 0, 'null 树 = 0（调用方据此禁用按钮，不许传 0 去 setLeaf）');
});

// ---------- 6b. 「切到此 + 还原文件」的数字口径 ----------

test('rollback* 是**这一行之后**的改动量（后端还原 owner_seq > seq）', () => {
  const rows = flattenTree({
    leafSeq: 7,
    total: 3,
    nodes: [
      node(3, 0, 'user', { turn: 1 }),
      node(5, 3, 'assistant', { children: 1, files: 2, childFiles: 1 }),
      node(6, 5, 'user', { turn: 2, children: 1 }),
      node(7, 6, 'assistant', { files: 3, external: 2 }),
    ],
  });
  const first = rows.find((r) => r.seq === 3);
  // 第 1 轮自己那行没有 files；它之后还有 2+3=5 个文件（含 1 个子 Agent）与 2 处外部操作
  assert.equal(first.rollbackFiles, 5, '不含本行（严格大于的切段口径）');
  assert.equal(first.rollbackChildFiles, 1);
  assert.equal(first.rollbackExternal, 2);
  const last = rows.find((r) => r.seq === 6);
  assert.equal(last.rollbackFiles, 3, '第 2 轮之后只剩下一轮那 3 个文件');
  // 最后一行是 user(6)；它之后的 assistant(7) 带 3 个文件——那些改动确实在它之后
  assert.equal(rows.at(-1).rollbackFiles, 3, '最后一行之后只剩本轮 assistant 的改动');
  assert.equal(rows.at(-1).rollbackExternal, 2, '外部操作同理');
});

test('rollback* 按 seq 算，不受折叠/过滤影响（折叠一个分支不该让数字变 0）', () => {
  const tree = {
    leafSeq: 3,
    total: 4,
    nodes: [
      node(1, 0, 'user', { turn: 1, children: 2 }),
      node(2, 1, 'assistant', { children: 1, files: 4 }),
      node(3, 2, 'user', { turn: 2 }),
      node(4, 1, 'assistant', { onPath: false, children: 1, files: 6 }),
      node(5, 4, 'user', { turn: 2, onPath: false, summary: '废弃分支' }),
    ],
  };
  const collapsed = flattenTree(tree, { collapsed: new Set([1]) });
  const first = collapsed.find((r) => r.seq === 1);
  assert.equal(first.rollbackFiles, 10, '折叠了后代，回滚量照旧是整段（4+6）');
  const filtered = flattenTree(tree, { filter: 'user' });
  assert.equal(filtered.find((r) => r.seq === 1).rollbackFiles, 10, '过滤不影响回滚量');
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

// ---------- 8. 畸形数据不炸 ----------

test('父节点缺失（orphan）不丢节点：仍成行并标 orphan', () => {
  const tree = {
    leafSeq: 2,
    total: 2,
    nodes: [
      node(1, 99, 'user', { turn: 1, orphan: true }), // 父 99 不存在
      node(2, 1, 'user', { turn: 2 }),
    ],
  };
  const rows = flattenTree(tree);
  assert.deepEqual(rows.map((r) => r.seq), [1, 2], '孤儿节点照旧显示（不隐藏）');
  assert.equal(rows[0].orphan, true);
  assert.equal(rows[0].depth, 0, '断链的行按根处理（不抛异常）');
});

test('成环不炸：环上的节点不重复、不死循环', () => {
  const tree = {
    leafSeq: 3,
    total: 3,
    nodes: [
      node(1, 3, 'user', { turn: 1 }), // 1 的父是 3（环）
      node(2, 1, 'user', { turn: 2 }),
      node(3, 2, 'user', { turn: 3 }),
    ],
  };
  const rows = flattenTree(tree);
  assert.ok(rows.length <= 3, '环上的节点不会被无限展开');
  assert.equal(new Set(rows.map((r) => r.seq)).size, rows.length, '每行只出现一次');
});

test('空树 / null 树 → 空行数组（面板走空态，不抛）', () => {
  assert.deepEqual(flattenTree(null), []);
  assert.deepEqual(flattenTree({ leafSeq: 0, total: 0, nodes: [] }), []);
});

// ---------- 9. 连接线（last 标记） ----------

test('last 标记：同一父行下最后一行（CSS 据此画 └）', () => {
  const rows = flattenTree(branchyTree());
  // 父 3 的子行是 5 与 7；7 是最后一个
  assert.equal(rows.find((r) => r.seq === 5).last, false);
  assert.equal(rows.find((r) => r.seq === 7).last, true);
  // 根层只有一个（1）
  assert.equal(rows.find((r) => r.seq === 1).last, true);
});
