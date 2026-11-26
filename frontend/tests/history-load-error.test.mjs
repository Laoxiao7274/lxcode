// P0-1「历史装载失败永久卡死」的 store 用例：失败置位 / 重试清位 / 成功路径
// 不受影响。事件链：WSAgent.resumeSession 的 resume/history 两路失败发
// historyLoadError（ws.test.mjs 钉事件侧）→ reducer 置进会话 state 的
// historyLoadError → switching 层让位给错误态（App 层判定）→ 成功装载
//（historyLoaded）清位 → 骨架化也清位（重进失败会话是全新装载尝试）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates } from '../src/shared/store.ts';
import { skeletonizeState } from '../src/shared/state-retention.ts';
import { initial } from '../src/shared/blocks.ts';

test('装载失败置位（主域），错误文案随事件', () => {
  let all = {};
  all = reduceSessionStates(all, {
    type: 'historyLoadError', sessionId: 's1', message: '读取会话历史失败: boom',
  });
  assert.equal(all.s1.historyLoadError, '读取会话历史失败: boom');
  assert.equal(all.s1.historyReady, false, '失败不算装载成功');
});

test('装载失败置位（助手域共用同一份 reducer）', () => {
  let all = {};
  all = reduceSessionStates(all, {
    type: 'historyLoadError', sessionId: 'a1', message: '恢复会话失败: x',
  });
  assert.equal(all.a1.historyLoadError, '恢复会话失败: x');
});

test('重试成功（historyLoaded）清位，时间线照常重建', () => {
  let all = {};
  all = reduceSessionStates(all, {
    type: 'historyLoadError', sessionId: 's1', message: 'boom',
  });
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: 'hi', seq: 1 }], busy: false, pending: null },
  });
  assert.equal(all.s1.historyLoadError, '');
  assert.equal(all.s1.historyReady, true);
  assert.ok(all.s1.blocks.some((b) => b.kind === 'user' && b.text === 'hi'), '成功装载正常出块');
});

test('忙碌快照的保留分支同样清位', () => {
  let all = {};
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [], busy: false, pending: null },
  });
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: true });
  all = reduceSessionStates(all, { type: 'delta', sessionId: 's1', kind: 'text', text: '流式正文' });
  all = reduceSessionStates(all, {
    type: 'historyLoadError', sessionId: 's1', message: 'boom',
  });
  // 失败后又拉到历史（会话在生成）：忙碌保留分支也要清位，否则流式期间错误态不退
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [], busy: true, pending: null },
  });
  assert.equal(all.s1.historyLoadError, '');
  assert.ok(all.s1.streaming, '流式尾巴保留');
});

test('骨架化保留失败位：重进失败会话仍可见错误与重试（不回到挂起指示）', () => {
  const failed = { ...initial, historyLoadError: 'boom' };
  // 无块的失败态（正是装载失败的实际形态）：skeletonizeState 走「无块可丢」
  // 早退返回原引用——错误位保留，重进时 Thread 直接给错误态（给「正在载入」
  // 等于回到旧 bug 的挂起形态）。
  assert.strictEqual(skeletonizeState(failed), failed);
  assert.equal(skeletonizeState(failed).historyLoadError, 'boom');
});

test('初次装载失败时已有内容的会话不受影响（错误不抹时间线）', () => {
  const loaded = reduce({ ...initial, currentId: 's1' }, {
    type: 'historyLoaded',
    sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: '旧消息', seq: 1 }], busy: false, pending: null },
  });
  const failed = reduce(loaded, { type: 'historyLoadError', sessionId: 's1', message: 'boom' });
  // 错误置位但不碰 blocks——上翻分页失败等场景（如果有）不该把时间线擦掉；
  // 实际置位只发生在 blocks 为空的装载路径上，reducer 保持纯归约不分叉。
  assert.equal(failed.historyLoadError, 'boom');
  assert.equal(failed.blocks.length, loaded.blocks.length);
  assert.equal(failed.historyReady, true, '已装载的事实不被失败事件推翻');
});
