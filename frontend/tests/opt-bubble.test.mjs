// 乐观用户气泡 + 发送中态（体验修复批次 2）：
// send 发起后立即插 pending 用户块（本地事件 optimisticUser，不经后端），
// 后端 userMessage 回执到达按 FIFO 去重，发送失败移除 pending，busy 交接发送中态。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';

// 空白会话状态（与 store 的 initial 同形状——测试里显式写，避免依赖导出形态）。
const blank = () => ({ ...initial, currentId: 's1' });

test('send 后立即出现 pending 用户块并进入发送中态', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '帮我看看这个报错' });
  const pending = s.blocks.filter((b) => b.kind === 'user' && b.pending);
  assert.equal(pending.length, 1, '乐观气泡应该立即出现');
  assert.equal(pending[0].text, '帮我看看这个报错');
  assert.equal(s.sending, true, '发送中态应该立即打开');
  assert.equal(s.busy, false, 'busy 还没到——仍是发送中而不是生成中');
});

test('userMessage 回执到达后 pending 块去重（不出现两条相同消息）', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '第一条' });
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '第一条', seq: 1 });
  const users = s.blocks.filter((b) => b.kind === 'user');
  assert.equal(users.length, 1, '回执后只剩真块——pending 已被 FIFO 移除');
  assert.equal(users[0].seq, 1, '真块带撤回锚点');
  assert.equal(users[0].pending, undefined, '真块不是 pending');
});

test('无 pending 块时 userMessage 照常渲染（老路径不受影响）', () => {
  let s = blank();
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '普通消息', seq: 7 });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'user');
  assert.equal(s.blocks[0].seq, 7);
});

test('连续发两条：回执按 FIFO 与 pending 一一对应', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '消息A' });
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '消息B' });
  let pendings = s.blocks.filter((b) => b.kind === 'user' && b.pending);
  assert.equal(pendings.length, 2);
  assert.equal(pendings[0].text, '消息A', 'FIFO：最旧的 pending 在前');
  // 回执 A 到达 → 移除最旧的 pending（A），B 留着
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '消息A', seq: 1 });
  pendings = s.blocks.filter((b) => b.kind === 'user' && b.pending);
  assert.equal(pendings.length, 1);
  assert.equal(pendings[0].text, '消息B', '回执 A 移除的是 pending A，不是 B');
  // 回执 B 到达 → 移除最后一个 pending
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '消息B', seq: 2 });
  pendings = s.blocks.filter((b) => b.kind === 'user' && b.pending);
  assert.equal(pendings.length, 0);
  const users = s.blocks.filter((b) => b.kind === 'user');
  assert.deepEqual(users.map((b) => b.text), ['消息A', '消息B'], '顺序与发送顺序一致');
});

test('send 失败：移除 pending 块并结束发送中态（错误走 operationError 通道）', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '会失败的' });
  assert.equal(s.sending, true);
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '会失败的', failed: true });
  assert.equal(s.blocks.filter((b) => b.kind === 'user').length, 0, 'pending 块已移除');
  assert.equal(s.sending, false, '发送中态已结束');
});

test('busy 到达：发送中态无缝交接给生成中', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '问题' });
  s = reduce(s, { type: 'busy', sessionId: 's1', busy: true });
  assert.equal(s.sending, false, 'busy 到达后发送中态结束');
  assert.equal(s.busy, true, '交接给生成中');
  // done 不清 busy（轮收尾由 busy=false 事件负责）——sending 必须已经被交接掉
  s = reduce(s, { type: 'done', sessionId: 's1', usageTokens: 10, finishReason: 'stop' });
  assert.equal(s.sending, false);
  s = reduce(s, { type: 'busy', sessionId: 's1', busy: false });
  assert.equal(s.busy, false);
  assert.equal(s.sending, false);
});

test('pending 块挂在会话自己的 state 上：切会话不串线、回来还能看到', () => {
  let all = {};
  all = reduceSessionStates(all, { type: 'sessionFocused', id: 's1' });
  all = reduceSessionStates(all, { type: 'optimisticUser', sessionId: 's1', text: 's1 的消息' });
  all = reduceSessionStates(all, { type: 'sessionFocused', id: 's2' });
  all = reduceSessionStates(all, { type: 'optimisticUser', sessionId: 's2', text: 's2 的消息' });
  assert.equal(all.s2.blocks.filter((b) => b.kind === 'user' && b.pending).length, 1);
  assert.equal(all.s2.blocks.some((b) => b.kind === 'user' && b.text === 's1 的消息'), false, 's1 的 pending 不串到 s2');
  // s1 的回执没到时切回 s1：pending 块还在 s1 里
  const s1Pending = all.s1.blocks.filter((b) => b.kind === 'user' && b.pending);
  assert.equal(s1Pending.length, 1);
  assert.equal(s1Pending[0].text, 's1 的消息');
});

test('提示条（notice）回执不动 pending 块：它不是本次发送的确认', () => {
  let s = blank();
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '问题' });
  // 后台任务通告在历史里也是 user 角色消息（notices.ts 前缀表识别）
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '[后台任务通告] job-1 已结束' });
  assert.equal(s.blocks.filter((b) => b.kind === 'user' && b.pending).length, 1, 'pending 不被通告吃掉');
  assert.equal(s.blocks.some((b) => b.kind === 'notice'), true, '通告照常渲染成提示条');
});
