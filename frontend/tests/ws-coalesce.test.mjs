// WS 层 delta 合帧（2026-12 渲染性能 Top1）的钉子：
//
// 后端的 chat.delta 是 token 级（一轮几百条），每条都 emit = store 每条一次
// setState + 全量 blocks 归约——流式渲染风暴的主力。合帧后同一帧内到达的
// 同类 delta 合并成一次 emit（同键 = session|kind|dispatch 拼接文本）。
//
// 契约（本文件钉住）：
// 1. 合帧收益：一次同步突发 N 条 delta → emit 次数远小于 N，文本拼接无丢失；
// 2. 保序：非 delta 事件 emit 前先冲刷挂起的 delta（先到先发）——done/toolCall
//    不能插到已到达文本的前面；
// 3. 终态一致：合帧后的最终 blocks 与逐条归约完全一致（reduceDelta 对同键
//    delta 就是文本拼接，合并只是把 N 次拼接并成 1 次）；
// 4. 异键不合并：不同会话 / 不同 kind（text vs reasoning）/ 子会话 dispatch
//    各占一位，互不串文本。
import test from 'node:test';
import assert from 'node:assert/strict';
import { WSAgent } from '../src/agent/ws/index.ts';
import { reduce } from '../src/shared/reduce.ts';
import { initial } from '../src/shared/blocks.ts';

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor(url) { this.url = url; FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
}

function setup(t) {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const events = [], agent = new WSAgent('localhost:1234');
  const off = agent.subscribe((e) => events.push(e));
  t.after(off);
  return { agent, events, ws: FakeSocket.latest };
}

// 合帧冲刷走 setTimeout 兜底（node 无 rAF）——等一拍让它落地
const settle = () => new Promise((resolve) => setTimeout(resolve, 40));

test('delta 合帧：200 条同步突发合并成 1 次 emit，文本零丢失', async (t) => {
  const { events, ws } = setup(t);
  const chunks = Array.from({ length: 200 }, (_, i) => `第${i}段`);
  for (const c of chunks) {
    ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: c } });
  }
  assert.equal(events.filter((e) => e.type === 'delta').length, 0, '帧内不逐条 emit（这正是收益所在）');
  await settle();
  const deltas = events.filter((e) => e.type === 'delta');
  assert.equal(deltas.length, 1, '合并后 emit 次数（1）<< delta 次数（200）');
  assert.equal(deltas[0].text, chunks.join(''), '合并文本 = 全部 delta 顺序拼接');
  assert.equal(deltas[0].sessionId, 's1');
  assert.equal(deltas[0].kind, 'text');
});

test('保序：delta、delta、toolCall、delta → 合并 delta 先于 toolCall，随后第三条独立到达', async (t) => {
  const { events, ws } = setup(t);
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '前' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '文' } });
  ws.receive({ method: 'chat.toolCall', params: { session_id: 's1', id: 't1', name: 'search', arguments: '{}' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '后' } });
  await settle();
  const types = events.map((e) => `${e.type}:${e.text ?? e.name ?? ''}`);
  assert.deepEqual(types, ['delta:前文', 'toolCall:search', 'delta:后'],
    '非 delta 事件到达时先冲刷挂起 delta（先到先发），不插队到已到达文本之前');
});

test('终态一致：合帧后的 blocks 与逐条归约完全一致（含 toolCall 打断流式块）', async (t) => {
  const { events, ws } = setup(t);
  const seq = [
    { method: 'chat.delta', params: { session_id: 's1', kind: 'reasoning', text: '思考' } },
    { method: 'chat.delta', params: { session_id: 's1', kind: 'reasoning', text: 'A' } },
    { method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '回答' } },
    { method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '一二三' } },
    { method: 'chat.toolCall', params: { session_id: 's1', id: 't1', name: 'search', arguments: '{}' } },
    { method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '续' } },
  ];
  for (const m of seq) ws.receive(m);
  await settle();

  // 逐条归约的参照：同构的事件形状一条一 reduce（uid 计数器是全局的，比较时剥掉）
  let state = initial;
  for (const p of seq.map((m) => m.params)) {
    state = reduce(state, p.kind
      ? { type: 'delta', sessionId: p.session_id, kind: p.kind, text: p.text }
      : { type: 'toolCall', sessionId: p.session_id, id: p.id, name: p.name, arguments: p.arguments });
  }
  const strip = (blocks) => blocks.map((b) => b.kind === 'assistant'
    ? { kind: b.kind, content: b.content, reasoning: b.reasoning, streaming: b.streaming }
    : { kind: b.kind, id: b.id ?? null, name: b.name ?? null });
  const merged = strip(events.reduce(
    (st, ev) => (ev.type === 'delta' || ev.type === 'toolCall' ? reduce(st, ev) : st),
    initial,
  ).blocks);
  assert.deepEqual(merged, strip(state.blocks), '合帧只改变归约次数，不改变终态');
});

test('异键不合并：不同会话 / kind / 子会话 dispatch 各自独立到达', async (t) => {
  const { events, ws } = setup(t);
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '主' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's2', kind: 'text', text: '另' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'reasoning', text: '思' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '子', dispatch_id: 'd1' } });
  ws.receive({ method: 'chat.delta', params: { session_id: 's1', kind: 'text', text: '会话', dispatch_id: 'd1' } });
  await settle();
  const deltas = events.filter((e) => e.type === 'delta');
  assert.equal(deltas.length, 4, '四个键 = 四条合并事件（主会话 text / s2 text / reasoning / 子会话）');
  assert.deepEqual(deltas.map((d) => d.text), ['主', '另', '思', '子会话']);
  assert.equal(deltas[3].dispatchId, 'd1', 'dispatch 归属保留（子会话双投靠它路由）');
});
