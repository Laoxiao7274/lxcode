// dispatch id 碰撞的隔离（2026-12 todo 跨派发残留批次③）：
//
//   dispatch id 是 LLM 生成的 tool_call id，**跨会话可能重复**。两个父会话先后
//   用了同一个 id 时：
//   ① recordChildLink 不静默覆盖——保留首次记录（后到者不抢路由，先建立的
//      子会话不失联；后到者的子事件降级走 owner blocks 反查）；
//   ② 兜底反查限定在**事件所属会话**的 blocks 内——另一个会话的同 id 卡不能
//      把归属抢走（子事件投进别人的子会话 = 进度串台）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { recordChildLink } from '../src/shared/workspace-tabs.ts';
import { reduceSessionStates } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';

const cardOf = (state, id) => state.blocks.find((b) => b.kind === 'dispatch' && b.id === id);
const st = (id, blocks = [], todos = []) => ({ ...initial, blocks, todos, currentId: id });

test('recordChildLink：同 dispatchId 不同 childId 碰撞 → 保留首次，不静默覆盖', () => {
  let links = recordChildLink({ childParents: {}, dispatchChild: {} }, { childId: 'c1', parentId: 'p1', dispatchId: 'dup' });
  assert.deepEqual(links.dispatchChild, { dup: 'c1' });
  // 另一个父会话用同一个 dispatch id 开了不同的子会话：不抢路由
  links = recordChildLink(links, { childId: 'c2', parentId: 'p2', dispatchId: 'dup' });
  assert.deepEqual(links.dispatchChild, { dup: 'c1' }, '碰撞时保留首次记录（后到者不覆盖）');
  assert.deepEqual(links.childParents, { c1: 'p1', c2: 'p2' }, '子→父映射不受影响（它按子会话 id 键入，天然无碰撞）');
  // 相同内容重复记录仍然幂等
  assert.equal(recordChildLink(links, { childId: 'c1', parentId: 'p1', dispatchId: 'dup' }), links);
});

test('兜底反查限定在事件所属会话的 blocks 内（同 id 卡在别的会话不抢归属）', () => {
  // 父 p2 的 blocks 里有一张同 id（dup）的卡指向 child-b；事件来自 p1，
  // p1 的 blocks 里没有这张卡 → 反查必须返回空串（不猜），而不是被 p2 的卡抢走
  const states = {
    p1: st('p1', []),
    p2: st('p2', [{ kind: 'dispatch', uid: 1, id: 'dup', sessionId: 'child-b', agentId: 'x', agentName: '', agentColor: '', task: 't', status: 'running', subBlocks: [] }]),
    'child-a': st('child-a'),
    'child-b': st('child-b'),
  };
  const delta = { type: 'delta', sessionId: 'p1', dispatchId: 'dup', kind: 'text', text: '子过程输出' };
  const next = reduceSessionStates(states, delta, {}); // 无映射表 → 走兜底反查
  assert.equal(
    next['child-a'].blocks.some((b) => b.kind === 'assistant' && b.content === '子过程输出'),
    false,
    'owner blocks 无此卡 → 反查不猜，不双投',
  );
  assert.equal(
    next['child-b'].blocks.some((b) => b.kind === 'assistant' && b.content === '子过程输出'),
    false,
    '别的会话的同 id 卡不能把归属抢走（子事件不投进别人的子会话）',
  );
});

test('有映射表时查表优先：双投进子会话自己的时间线（流式块进 streaming）', () => {
  const states = {
    p1: st('p1', [{ kind: 'dispatch', uid: 1, id: 'dup', sessionId: 'child-a', agentId: 'x', agentName: '', agentColor: '', task: 't', status: 'running', subBlocks: [] }]),
    'child-a': st('child-a'),
  };
  const delta = { type: 'delta', sessionId: 'p1', dispatchId: 'dup', kind: 'text', text: '输出' };
  const next = reduceSessionStates(states, delta, { dup: 'child-a' });
  assert.equal(
    next['child-a'].streaming?.content,
    '输出',
    '查表命中 → 双投进子会话自己的时间线（delta 写进流式块）',
  );
});
