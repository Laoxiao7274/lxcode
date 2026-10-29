// 已定稿前缀切分（2026-12 渲染性能 Top2）的钉子：
//
// AnswerBody 两段渲染的正确性完全压在 splitCommitted 上：切点必须在**块边界**
// 上——切在未闭合的代码围栏/列表/引用/表格中间，会把一个块渲染成两个（与
// 整段解析的 DOM 形态分叉）。本文件钉住：
// 1. 常规路径：committed = 倒数第 2 行之前，committed + tail === 原文；
// 2. 各未闭合形态逐行回退（围栏 / 列表 / 引用 / 表格）；
// 3. **前缀性质**：parseBlocks(committed) 与 parseBlocks(全文) 的前 k 块逐块
//    一致（gap 段距标记也一致）——这是"两段渲染 = 单段渲染"的硬证据；
// 4. hidden 冻结/恢复（stream-reveal 既有纪律）不受影响：committed 由 shown
//    派生，shown 冻结它就冻结、snap 它就一次落全量。
import test from 'node:test';
import assert from 'node:assert/strict';
import { splitCommitted, revealPlan, REVEAL_RESUME_JUMP } from '../src/shared/stream-reveal.ts';
import { parseBlocks } from '../src/shared/markdown.tsx';

const check = (text) => {
  const { committed, tail } = splitCommitted(text);
  assert.equal(committed + (committed === "" ? "" : "\n") + tail, text, 'committed + tail 必须还原原文');
  return { committed, tail };
};

/** 前缀性质：committed 的块序列是全文块序列的前缀（逐块 deepEqual）。 */
function assertPrefix(text) {
  const { committed } = check(text);
  if (committed === "") return;
  const full = parseBlocks(text);
  const pre = parseBlocks(committed);
  assert.ok(pre.length < full.length, '切点之后必须还有 tail 块（否则切了个寂寞）');
  assert.deepEqual(pre, full.slice(0, pre.length), 'committed 解析结果必须是全文解析的前缀');
}

test('常规路径：committed = 已完成行（增长中的末行恒在 tail）', () => {
  const text = ['# 标题', '', '第一段', '第二段', '第三段'].join('\n');
  const { committed, tail } = check(text);
  assert.equal(committed, ['# 标题', '', '第一段', '第二段'].join('\n'));
  assert.equal(tail, '第三段');
  assertPrefix(text);
});

test('两行以内的流：committed 不越过末行（末行恒在 tail）', () => {
  for (const text of ['', '单行']) {
    const { committed, tail } = splitCommitted(text);
    assert.equal(committed, '');
    assert.equal(tail, text);
  }
  // 两行：首行已定稿、末行（增长中）在 tail
  const two = splitCommitted('第一行\n第二行');
  assert.equal(two.committed, '第一行');
  assert.equal(two.tail, '第二行');
});

test('未闭合代码围栏：切点回退到围栏打开之前（切进去会把代码渲染成两段 pre）', () => {
  const text = ['# 标题', '', '```go', 'fmt.Println("还没闭合")'].join('\n');
  const { committed, tail } = check(text);
  assert.equal(committed, '# 标题', '回退到围栏打开行之前');
  assert.ok(tail.startsWith('\n```go'), '围栏整体留在 tail');
  // 切点安全 = committed 内 ``` 出现次数为偶数（与 Markdown 入口 split 同语义）
  assert.equal((committed.match(/```/g) ?? []).length % 2, 0);
});

test('已闭合代码围栏：不需要回退，围栏整体留在 committed', () => {
  const text = ['```go', 'fmt.Println("闭合了")', '```', '', '后面的段落', '另一段'].join('\n');
  const { committed, tail } = check(text);
  assert.equal(committed, ['```go', 'fmt.Println("闭合了")', '```', '', '后面的段落'].join('\n'));
  assert.equal(tail, '另一段');
});

test('未闭合列表：切点回退，列表整体留在 tail', () => {
  const text = ['前言一段', '', '- 第一条', '- 第二条', '- 第三条（还在长）'].join('\n');
  const { committed, tail } = check(text);
  assert.ok(!committed.includes('- 第一条'), '列表不能被拦腰切断（一个块渲染成两个）');
  assert.ok(tail.includes('- 第一条'));
  assertPrefix(text);
});

test('未闭合引用：切点回退，引用块整体留在 tail', () => {
  const text = ['前言', '', '> 引用行一', '> 引用行二', '> 引用行三'].join('\n');
  const { committed, tail } = check(text);
  assert.ok(!committed.includes('> 引用行一'));
  assertPrefix(text);
});

test('未闭合表格：切点回退，表格整体留在 tail', () => {
  const text = ['前言', '', '| 列A | 列B |', '| --- | --- |', '| 1 | 2 |', '| 3 | 4 |'].join('\n');
  const { committed, tail } = check(text);
  assert.ok(!committed.includes('| 列A |'), '表格行不能分属两段');
  assertPrefix(text);
});

test('committed 在整行完成之间保持稳定（两段渲染的收益前提）', () => {
  // 真实浇注的常态是「末行内部逐字增长」：只要没有新行开始，committed 恒为
  // 同一字符串 → TextBlocks 的 memo 按字符串比较恒命中（零解析零协调）；
  // 新行开始的瞬间才前进一格。
  const t1 = '第一段\n第二段';
  const t2 = '第一段\n第二段完成了';
  assert.equal(splitCommitted(t1).committed, '第一段', '首行已定稿');
  assert.equal(splitCommitted(t2).committed, splitCommitted(t1).committed, '末行内部增长不改变 committed');
  const t3 = '第一段\n第二段完成了\n第三';
  const t4 = '第一段\n第二段完成了\n第三段继续打字';
  assert.equal(splitCommitted(t3).committed, '第一段\n第二段完成了', '新行开始 → committed 前进一行');
  assert.equal(splitCommitted(t4).committed, splitCommitted(t3).committed, '新末行内部增长又不改变 committed');
});

test('hidden 冻结/恢复纪律不受两段渲染影响：committed 随 shown 冻结、snap 一次落全量', () => {
  // 复刻 stream-reveal.test 的端到端时序，只是每步再取 committed：
  // ① 可见阶段 shown 正常推进（这里用前缀对齐近似浇注的落账）；
  // ② hidden 期间 delta 继续到达但 shown 冻结 → committed 跟着冻结（不推进）；
  // ③ 恢复 snap 一次落全量 → committed 一步到位，且 committed+tail === 全文。
  let text = '', shown = '';
  const push = (d) => { text += d; };
  push('# 标题\n\n');
  shown = text; // 可见阶段浇注落账（前缀对齐）
  const beforeHold = splitCommitted(shown).committed;
  push('一段'.repeat(600) + '\n');
  push('第二段'.repeat(400));
  // hidden：revealPlan = hold，shown 冻结 → committed 保持不动
  assert.equal(revealPlan(shown, text, { visible: false, motion: true, lagLimit: 4000 }), 'hold');
  assert.equal(splitCommitted(shown).committed, beforeHold, '冻结期间 committed 不推进');
  // 恢复：积压超阈值 → snap 整块落下
  assert.equal(revealPlan(shown, text, { visible: true, motion: true, lagLimit: REVEAL_RESUME_JUMP }), 'snap');
  shown = text;
  const after = splitCommitted(shown);
  assert.notEqual(after.committed, beforeHold, 'snap 后 committed 一次到位');
  assert.equal(after.committed + '\n' + after.tail, text);
});
