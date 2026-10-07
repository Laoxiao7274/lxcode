// 浏览器式工作区导航历史（shared/view-history）——鼠标侧键后退/前进的数据面。
// 与 workspace-tabs 的「关闭回退」history 是两份语义：这份是时间线（去过哪、退回
// 去、还能前进回来）。纯函数测试，不必渲染 React。
import test from 'node:test';
import assert from 'node:assert/strict';
import { VIEW_HISTORY_LIMIT, initialViewHistory, pushView, stepView } from '../src/shared/view-history.ts';

test('pushView：连续同页去重；新页清空前进栈（浏览器语义）', () => {
  let h = initialViewHistory();
  assert.equal(pushView(h, 'chat'), h, '同页导航不是"去了一页"（原样返回）');
  h = pushView(h, 'agents');
  h = pushView(h, 'agents');
  assert.deepEqual(h.past, ['chat'], '重复聚焦同一个标签只记一次');
  h = pushView(h, 'catalog');
  assert.deepEqual(h.past, ['chat', 'agents']);
  assert.deepEqual(h.future, [], '从中间导航后前进栈作废');
});

test('后退/前进走完整旅程：chat → agents → git → 退回 → 前进', () => {
  let h = initialViewHistory();
  for (const v of ['agents', 'git']) h = pushView(h, v);
  const back = stepView(h, 'back', () => true);
  assert.equal(back.view, 'agents');
  assert.deepEqual(back.history.past, ['chat']);
  assert.deepEqual(back.history.future, ['git'], '退回的页压进前进栈');
  const fwd = stepView(back.history, 'forward', () => true);
  assert.equal(fwd.view, 'git');
  assert.deepEqual(fwd.history.past, ['chat', 'agents']);
  assert.deepEqual(fwd.history.future, []);
  // 退到第一页：past 还有 chat（你来时的页），退到它之后再退 = null（浏览器同款）
  const toChat = stepView(back.history, 'back', () => true);
  assert.equal(toChat.view, 'chat');
  assert.equal(stepView(toChat.history, 'back', () => true), null, '第一页再退没有更早的页');
  // 前进到底 = null
  assert.equal(stepView(fwd.history, 'forward', () => true), null);
});

test('后退时跳过已关闭的子会话标签（死条目不挡路也不复活）', () => {
  let h = initialViewHistory();
  for (const v of ['child:c1', 'agents', 'child:c2']) h = pushView(h, v);
  // c2 已被关掉：后退应跳过它落回 agents，且 c2 从历史里消失
  const step = stepView(h, 'back', (v) => v !== 'child:c2');
  assert.equal(step.view, 'agents');
  assert.deepEqual(step.history.past, ['chat', 'child:c1']);
  assert.deepEqual(step.history.future, ['child:c2'], 'present 压回前进栈（不变式：走过的页都在栈里）');
  // c1 也关了：从 agents 再退一步直接回 chat
  const step2 = stepView(step.history, 'back', (v) => v !== 'child:c1' && v !== 'child:c2');
  assert.equal(step2.view, 'chat');
  assert.deepEqual(step2.history.past, []);
});

test('前进时同样跳过死条目；全都死了 = null', () => {
  let h = initialViewHistory();
  h = pushView(h, 'child:c1');
  const back = stepView(h, 'back', () => true); // 退回 chat
  const dead = stepView(back.history, 'forward', (v) => v !== 'child:c1');
  assert.equal(dead, null, '前进方向只有死条目 = 无处可进');
});

test('历史上限：只留最近 50 页', () => {
  let h = initialViewHistory();
  for (let i = 0; i < VIEW_HISTORY_LIMIT + 10; i++) h = pushView(h, `child:s${i}`);
  assert.equal(h.past.length, VIEW_HISTORY_LIMIT);
  assert.equal(h.past[0], `child:s${9}`, '最老的条目被挤掉');
});
