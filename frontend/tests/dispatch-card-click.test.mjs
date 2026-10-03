// 卡头主区点击语义的钉子（用户实测报的「点击现在还是展开和收缩，并不是新标签页」，
// 以及 2026-09-30 拍板的「主会话不应该有展开收缩」）。
//
// 为什么单独一个文件：这是**交互语义**，不是渲染细节——判定抽成了纯函数
// （components/thread/blocks/dispatch-primary.ts，不 import React），所以这里不需要
// DOM、不需要测试渲染器就能逐条钉住它。语义一旦漂移，用户看到的正是原报的那个 bug：
// 点卡头还是只展开/收起，进不去那个独立会话（AGENTS.md §2.3：子 Agent = 独立会话）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  dispatchPrimaryAction,
  dispatchPrimaryTitle,
} from '../src/components/thread/blocks/dispatch-primary.ts';

/** 主时间线里那张 dispatch 块（只用到 sessionId 一个字段——纯函数刻意只依赖它）。 */
const block = (overrides = {}) => ({
  kind: 'dispatch', uid: 1, id: 'c1',
  sessionId: 'sess-1234567890', agentId: 'researcher', agentName: 'researcher',
  agentColor: '#888', task: '通读 internal/agent', status: 'done', result: '读完', subBlocks: [],
  ...overrides,
});

// ---------- ① 能打开就打开（唯一的操作 = 进独立会话） ----------

test('① 有 sessionId 且有 onOpenChild → open（点主区进独立会话）', () => {
  assert.equal(dispatchPrimaryAction(block(), true), 'open');
  // 运行中的卡（status=running）同样能进——子会话从派发那一刻就存在
  assert.equal(dispatchPrimaryAction(block({ status: 'running' }), true), 'open');
});

// ---------- ② 进不去 → none（**没有**展开/收起可回落了） ----------

test('② 没有 sessionId → none（卡里没有折叠区，主区渲染成静态行）', () => {
  // 字段整个缺席（回放路径的历史块：sessionId 来自 childSessionId，老数据没有）
  assert.equal(dispatchPrimaryAction({}, true), 'none');
  assert.equal(dispatchPrimaryAction({ sessionId: undefined }, true), 'none');
  assert.equal(dispatchPrimaryAction({ sessionId: null }, true), 'none');
});

// ---------- ③ 有 id 但没接 onOpenChild → 同样进不去 ----------

test('③ 有 sessionId 但没有 onOpenChild（演示态/未接线调用方）→ none', () => {
  // 关键反例：**有 id 不等于能打开**——没有 onOpenChild 就没有"打开"这个能力，
  // 此时返回 open 会让点击落进一个空实现里（用户点了没反应）。
  assert.equal(dispatchPrimaryAction(block(), false), 'none');
});

// ---------- ④ sessionId 是空串 → 与"没有 id"同一条路 ----------

test('④ sessionId 是空串 → none（空串不是有效的子会话 id）', () => {
  assert.equal(dispatchPrimaryAction({ sessionId: '' }, true), 'none');
  // 空串 + 没接线，两条判据都要求 none（不许两条判据互相顶）
  assert.equal(dispatchPrimaryAction({ sessionId: '' }, false), 'none');
});

// ---------- ⑤ title / aria-label 与判定同源（提示语不许骗人） ----------

test('⑤ 提示语与判定同源：可打开说"打开子会话 <id 前 8 位>"，否则如实说为什么进不去', () => {
  const openTitle = dispatchPrimaryTitle(block(), true);
  assert.match(openTitle, /^打开子会话 sess-123/);
  assert.match(openTitle, /独立会话/);
  assert.match(openTitle, /实时/, '提示里要说清过程是实时的（这正是它比卡内折叠区强的地方）');
  // 进不去时**不许**还挂着"打开子会话"——那是在骗用户（点下去只会没反应）
  for (const t of [
    dispatchPrimaryTitle({}, true),
    dispatchPrimaryTitle(block(), false),
    dispatchPrimaryTitle({ sessionId: '' }, true),
  ]) {
    assert.ok(!t.includes('打开子会话'), '不可打开时提示语不许说"打开子会话"：' + t);
    // 也不许再提"展开/收起"——卡里已经没有折叠区了（2026-09-30 拍板去掉）
    assert.ok(!t.includes('展开') && !t.includes('收起'),
      '不可打开时提示语不许提展开/收起（卡里没有折叠区了）：' + t);
  }
});
