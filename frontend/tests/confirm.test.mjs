// 确认门（confirmRequest → tool.confirm → toolResult）的归约契约。
// 覆盖 fab5c3a 引入的回归：确认卡与同 id 工具行并存 → 批准后出现两条
// 同 id 工具行，toolResult 只回填第一条，第二条永远停在"执行中…"。
// 以及 P4：挂起确认必须落进 blocks（只有 confirm 块会被渲染——pending
// 本身没有任何组件读取，丢了就死锁）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, resolveConfirm } from '../src/shared/store.ts';

const base = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };

/** 所有时间线（主 + 卡内）里 result 仍为 undefined 的工具行 = 僵尸行。 */
function zombies(state) {
  const out = [];
  const scan = (blocks, where) => {
    for (const b of blocks) {
      if (b.kind === 'tool' && b.result === undefined) out.push(`${where}:${b.name}:${b.id}`);
      if (b.kind === 'dispatch') scan(b.subBlocks, `dispatch(${b.id})`);
    }
  };
  scan(state.blocks, 'main');
  return out;
}

const REQ = { id: 'c-bash-1', name: 'bash', arguments: '{"command":"go test"}', prompt: '执行命令?' };

test('卡内确认：toolCall 先建行，confirmRequest 就地替换它（不追加第二行）', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码', agentColor: '#3b82f6', task: 't' });
  s = reduce(s, { type: 'toolCall', id: 'c-bash-1', name: 'bash', arguments: '{"command":"go test"}', dispatchId: 'd1' });
  const beforeUid = s.blocks[0].subBlocks[0].uid;
  s = reduce(s, { type: 'confirmRequest', request: { ...REQ, dispatch_id: 'd1' } });
  const sub = s.blocks.find((b) => b.kind === 'dispatch').subBlocks;
  assert.equal(sub.length, 1, '同 id 的工具行应被就地替换，不是追加');
  assert.equal(sub[0].kind, 'confirm');
  assert.equal(sub[0].uid, beforeUid, 'uid 不变（React key 稳定，卡片不重挂）');
  assert.deepEqual(s.pending, { ...REQ, dispatch_id: 'd1' });
});

test('卡内确认：批准 → toolResult 回填，无僵尸行（回归 fab5c3a）', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码', agentColor: '#3b82f6', task: 't' });
  s = reduce(s, { type: 'toolCall', id: 'c-bash-1', name: 'bash', arguments: '{"command":"go test"}', dispatchId: 'd1' });
  s = reduce(s, { type: 'confirmRequest', request: { ...REQ, dispatch_id: 'd1' } });
  s = resolveConfirm(s, 'c-bash-1', 'allow');
  assert.equal(s.pending, null, '裁决后不应再有挂起确认（两条分支一致）');
  s = reduce(s, { type: 'toolResult', id: 'c-bash-1', name: 'bash', content: 'PASS', isError: false, dispatchId: 'd1' });
  const sub = s.blocks.find((b) => b.kind === 'dispatch').subBlocks;
  assert.equal(sub.length, 1, '批准后仍只应有一行');
  assert.equal(sub[0].kind, 'tool');
  assert.equal(sub[0].result, 'PASS', '结果必须回填（否则界面永远"执行中…"）');
  assert.deepEqual(zombies(s), [], '不应有僵尸行');
});

test('主时间线确认：与工具行同 id 时同样就地替换（回落路径）', () => {
  let s = reduce(base, { type: 'toolCall', id: 'c-bash-2', name: 'bash', arguments: '{"command":"ls"}' });
  s = reduce(s, { type: 'confirmRequest', request: { id: 'c-bash-2', name: 'bash', arguments: '{"command":"ls"}', prompt: 'p' } });
  assert.equal(s.blocks.filter((b) => b.kind === 'confirm' || b.kind === 'tool').length, 1, '只应有一行');
  s = resolveConfirm(s, 'c-bash-2', 'allow');
  s = reduce(s, { type: 'toolResult', id: 'c-bash-2', name: 'bash', content: 'ok', isError: false });
  assert.deepEqual(zombies(s), []);
  assert.equal(s.blocks[0].result, 'ok');
});

test('确认先于 toolCall 到达时追加卡（无对应工具行）', () => {
  const s = reduce(base, { type: 'confirmRequest', request: { id: 'c-x', name: 'bash', arguments: '{}', prompt: 'p' } });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'confirm');
});

test('dispatch 卡缺失时确认回落主时间线（不静默丢弃——否则会话死锁）', () => {
  // 子事件带 dispatch_id 但卡不在（刷新丢卡、事件乱序）
  const s = reduce(base, { type: 'confirmRequest', request: { ...REQ, dispatch_id: 'ghost' } });
  assert.equal(s.blocks.length, 1, '必须落到主时间线，不能丢');
  assert.equal(s.blocks[0].kind, 'confirm');
  assert.ok(s.pending, 'pending 同步设置');
});

test('拒绝：卡片定格为已跳过，不转工具行', () => {
  let s = reduce(base, { type: 'toolCall', id: 'c-bash-3', name: 'bash', arguments: '{}' });
  s = reduce(s, { type: 'confirmRequest', request: { id: 'c-bash-3', name: 'bash', arguments: '{}', prompt: 'p' } });
  s = resolveConfirm(s, 'c-bash-3', 'deny');
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'confirm');
  assert.equal(s.blocks[0].resolved, 'deny');
});

test('历史回放重建挂起的确认卡（刷新后仍可批准）', () => {
  const s = reduce(base, {
    type: 'historyLoaded',
    history: {
      sessionId: 's1',
      messages: [{ role: 'user', content: '跑测试' }],
      busy: true,
      pending: { id: 'c-pend', name: 'bash', arguments: '{}', prompt: '执行?' },
      todos: [],
    },
  });
  assert.ok(s.pending, 'pending 保留');
  const card = s.blocks.find((b) => b.kind === 'confirm');
  assert.ok(card, '确认卡必须重建（pending 没有渲染点，只有块会画出来）');
  assert.equal(card.request.id, 'c-pend');
});

test('历史回放：挂起确认就地替换历史里的同 id 工具行（不并列两行）', () => {
  const s = reduce(base, {
    type: 'historyLoaded',
    history: {
      sessionId: 's1',
      messages: [
        { role: 'assistant', content: '', tool_calls: [{ id: 'c-dup', function: { name: 'bash', arguments: '{}' } }] },
      ],
      busy: true,
      pending: { id: 'c-dup', name: 'bash', arguments: '{}', prompt: 'p' },
      todos: [],
    },
  });
  // 后端在确认门挂起前已落库 tool_calls → 历史里有一行同 id 工具行；
  // 重建时它应被替换成确认卡（两行同 id 会导致批准后重复行 + 僵尸行）
  const withId = s.blocks.filter((b) => (b.kind === 'tool' || b.kind === 'confirm') && (b.id === 'c-dup' || b.request?.id === 'c-dup'));
  assert.equal(withId.length, 1, '同 id 只应有一行');
  assert.equal(withId[0].kind, 'confirm', '工具行被替换成确认卡');
  assert.ok(s.pending, 'pending 保留');
});

// ---- 顺序竞态：后端 ack 与工具执行并发，结果可能先于裁决到达 ----
// App.tsx 是 `source.confirm(id, allow).then(() => resolve(...))`——卡在 ack 之后
// 才定格成工具行；而工具在 ack 之后就开跑。结果先到时若只找工具行就会丢弃它，
// 卡随后定格成工具行却永远「执行中…」（用户报的僵尸行）。

test('结果先于裁决到达（卡内）：就地转工具行带结果，且 ack 到达不破坏它', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码', agentColor: '#3b82f6', task: 't' });
  s = reduce(s, { type: 'toolCall', id: 'c-race', name: 'bash', arguments: '{}', dispatchId: 'd1' });
  s = reduce(s, { type: 'confirmRequest', request: { ...REQ, id: 'c-race', dispatch_id: 'd1' } });
  // ack 还没回来（卡未定格），工具结果先到
  s = reduce(s, { type: 'toolResult', id: 'c-race', name: 'bash', content: 'PASS', isError: false, dispatchId: 'd1' });
  const sub = s.blocks.find((b) => b.kind === 'dispatch').subBlocks;
  assert.equal(sub.length, 1, '结果到达时确认卡就地转工具行，不新增行');
  assert.equal(sub[0].kind, 'tool');
  assert.equal(sub[0].result, 'PASS', '结果必须被消费（丢弃 = 永久「执行中…」）');
  assert.deepEqual(zombies(s), []);
  // 随后 ack 到达：找不到确认卡 → 空操作，已回填的结果不能被抹掉
  s = resolveConfirm(s, 'c-race', 'allow');
  const after = s.blocks.find((b) => b.kind === 'dispatch').subBlocks;
  assert.equal(after.length, 1, 'ack 到达不应再改行数');
  assert.equal(after[0].result, 'PASS', 'ack 到达不应抹掉已回填的结果');
  assert.deepEqual(zombies(s), []);
});

test('结果先于裁决到达（主时间线回落路径）：同样转工具行带结果', () => {
  let s = reduce(base, { type: 'toolCall', id: 'c-race2', name: 'bash', arguments: '{}' });
  s = reduce(s, { type: 'confirmRequest', request: { id: 'c-race2', name: 'bash', arguments: '{}', prompt: 'p' } });
  s = reduce(s, { type: 'toolResult', id: 'c-race2', name: 'bash', content: 'ok', isError: false });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'tool');
  assert.equal(s.blocks[0].result, 'ok');
  assert.deepEqual(zombies(s), []);
});

test('已裁决为拒绝的卡不被后到的结果覆盖（保持「已跳过」）', () => {
  let s = reduce(base, { type: 'toolCall', id: 'c-deny2', name: 'bash', arguments: '{}' });
  s = reduce(s, { type: 'confirmRequest', request: { id: 'c-deny2', name: 'bash', arguments: '{}', prompt: 'p' } });
  s = resolveConfirm(s, 'c-deny2', 'deny');
  // 后端在拒绝路径上也会回填一条 is_error 结果——不能把「已跳过」卡改成工具行
  s = reduce(s, { type: 'toolResult', id: 'c-deny2', name: 'bash', content: '用户拒绝执行', isError: true });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'confirm');
  assert.equal(s.blocks[0].resolved, 'deny');
});
