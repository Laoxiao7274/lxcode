// 用户气泡的三个动作（复制 / 编辑 / 撤回）——判定全部抽成纯函数之后在这里钉住。
//
// 为什么值得单独一个文件：撤回的锚点语义是「这条及其之后的全部历史一起消失」，
// 写错一个下标只会**静默丢历史**（编译器和类型系统都拦不住），而这正是用户最不能
// 接受的一类 bug（"我撤回了上一条，下面三条也没了 / 没删干净"）。协议字段名同理：
// `seq` 写成 `seq_no` 不会让任何东西编译失败，后端只会当作 seq 缺失——撤回
// "成功"返回 removed=0 而历史一条没删。两条都要有钉子。
import test from 'node:test';
import assert from 'node:assert/strict';
import { rewindParams, planRewind, canRewind, beginEdit, cancelEdit } from '../src/shared/blocks.ts';
import { reduce } from '../src/shared/store.ts';
import { WSAgent } from '../src/agent/ws/index.ts';
import { mapEvent } from '../src/agent/ws/events.ts';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null, historyReady: true };

/** 一串带 seq 的块：user/assistant/tool 混排——撤回必须把锚点之后的一切都带走，
 *  不只是后面的用户气泡（工具行、正文、确认卡都在历史里）。 */
function timeline() {
  return [
    { kind: 'user', uid: 1, text: '第一条', seq: 10 },
    { kind: 'assistant', uid: 2, content: 'A1', reasoning: '', streaming: false },
    { kind: 'tool', uid: 3, id: 't1', name: 'read_file', arguments: '{}', result: 'ok' },
    { kind: 'user', uid: 4, text: '第二条', seq: 20 },
    { kind: 'assistant', uid: 5, content: 'A2', reasoning: '', streaming: false },
    { kind: 'user', uid: 6, text: '第三条', seq: 30 },
  ];
}
const uids = (blocks) => blocks.map((b) => b.uid);

// ---------- 1. 与后端逐字一致的协议形状 ----------

test('chat.rewind 的参数与 Go 侧逐字一致（session_id / seq）', async () => {
  // 字面量断言（不是拿 rewindParams 的返回值自比）：字段名漂移必须变红
  assert.deepEqual(rewindParams('s1', 7), { session_id: 's1', seq: 7 });
  assert.deepEqual(Object.keys(rewindParams('s1', 7)).sort(), ['seq', 'session_id']);
  // 再走一遍**真实现**：生产路径上发出去的就是这两个键（WSAgent.rewind）
  const agent = new WSAgent('localhost:1');
  const sent = [];
  agent.call = (method, params) => { sent.push({ method, params }); return Promise.resolve({ removed: 2 }); };
  const out = await agent.rewind('s1', 7);
  assert.deepEqual(sent, [{ method: 'chat.rewind', params: { session_id: 's1', seq: 7 } }]);
  assert.deepEqual(out, { removed: 2 });
});

test('chat.rewound 的载荷映射成 rewound 事件（session_id / seq / removed / context）', () => {
  // context：后端重算后的占用。未知时整键缺席——那时必须是 undefined（归约器回落中性态「—」），
  // 不是 0 也不是 {}（编一个数会让指示器显示假的 0%）。
  assert.deepEqual(mapEvent('chat.rewound', { session_id: 's1', seq: 20, removed: 3 }), {
    type: 'rewound', sessionId: 's1', seq: 20, removed: 3, context: undefined,
  });
  const usage = { used: 1200, window: 32768 };
  assert.deepEqual(mapEvent('chat.rewound', { session_id: 's1', seq: 20, removed: 3, context: usage }), {
    type: 'rewound', sessionId: 's1', seq: 20, removed: 3, context: usage,
  });
});

// ---------- 2. 撤回锚点语义：这条及其之后全没，之前的原样 ----------

test('撤回第 N 条：这条及其之后全没了（含锚点自己），之前的原样保留', () => {
  const blocks = timeline();
  const plan = planRewind(blocks, 20);
  assert.ok(plan, 'seq 在时间线里就必须给出计划');
  assert.deepEqual(uids(plan.blocks), [1, 2, 3], '锚点(uid 4)及其之后的 uid 5/6 都要消失');
  // 保留的块必须是**原对象**（引用相等）：重建会丢掉展开态这类本地状态
  for (let i = 0; i < plan.blocks.length; i++) assert.strictEqual(plan.blocks[i], blocks[i]);
});

test('撤回第一条 → 时间线全空', () => {
  const plan = planRewind(timeline(), 10);
  assert.deepEqual(plan.blocks, []);
  assert.equal(plan.text, '第一条');
});

test('撤回最后一条 → 只留它之前的', () => {
  const plan = planRewind(timeline(), 30);
  assert.deepEqual(uids(plan.blocks), [1, 2, 3, 4, 5]);
});

test('撤回计划里的文本就是锚点那条的原文（要回到输入框的那句）', () => {
  assert.equal(planRewind(timeline(), 20).text, '第二条');
});

// ---------- 3. 归约：chat.rewound 截断 + 幂等 ----------

test('chat.rewound 归约：截断到锚点之前，且重复到达是幂等的', () => {
  let s = { ...base, blocks: timeline(), context: { used: 999 } };
  s = reduce(s, { type: 'rewound', sessionId: 's1', seq: 20, removed: 3 });
  assert.deepEqual(uids(s.blocks), [1, 2, 3], '锚点(uid 4, seq 20)及其之后的 uid 5/6 都消失');
  // 删掉历史后旧占用一定是错的（协议不带重算后的占用）——中性态而不是编一个数
  assert.equal(s.context, null);
  // 后端广播重复到达（本地乐观截断已经把锚点删了）：不能再往下切一刀
  const again = reduce(s, { type: 'rewound', sessionId: 's1', seq: 20, removed: 3 });
  assert.strictEqual(again, s, '找不到锚点必须原样返回（不换对象、不改内容）');
});

// ---------- 4. 两条路径都带 seq（回放 + 实时） ----------

test('实时 chat.userMessage 的 message.seq 进块（撤回锚点）', () => {
  const ev = mapEvent('chat.userMessage', { session_id: 's1', message: { role: 'user', content: 'hi', seq: 42 } });
  assert.deepEqual(ev, { type: 'userMessage', sessionId: 's1', text: 'hi', seq: 42 });
  const s = reduce({ ...base }, ev);
  assert.equal(s.blocks[0].kind, 'user');
  assert.equal(s.blocks[0].seq, 42);
});

test('历史回放的每条消息同样带 seq（两条路径必须一致）', () => {
  const s = reduce({ ...base }, {
    type: 'historyLoaded', sessionId: 's1',
    history: {
      sessionId: 's1', busy: false, pending: null, todos: [],
      messages: [{ role: 'user', content: 'hi', seq: 5 }, { role: 'assistant', content: 'ok' }],
    },
  });
  assert.equal(s.blocks[0].kind, 'user');
  assert.equal(s.blocks[0].seq, 5, '刷新之后同一条消息必须还能撤回');
});

// ---------- 5. 老后端 / 演示历史：没有 seq 不许炸 ----------

test('没有 seq 的块（老后端 / 演示历史）不许炸：动作禁用，判定不猜位置', () => {
  const legacy = [
    { kind: 'user', uid: 1, text: '老后端的一条' },
    { kind: 'assistant', uid: 2, content: 'A', reasoning: '', streaming: false },
  ];
  assert.equal(canRewind(legacy[0]), false, '没有 seq 就不能挂撤回/编辑');
  assert.equal(beginEdit(legacy[0]), null, '编辑也要给明确反馈（null），不能静默');
  assert.equal(planRewind(legacy, 10), null, '没有锚点就不能猜一个位置截断');
  // 不能被 0 / NaN 这类强制转换出来的数字误命中（Number(undefined) 的经典坑）
  assert.equal(planRewind(legacy, 0), null);
  assert.equal(planRewind(legacy, NaN), null);
  // 其它块型也不能当锚点
  assert.equal(canRewind(legacy[1]), false);
  // 老后端的事件里 seq 整键缺席（不是 undefined 值的键）——归约与渲染都不受影响
  assert.deepEqual(
    mapEvent('chat.userMessage', { session_id: 's1', message: { role: 'user', content: 'hi' } }),
    { type: 'userMessage', sessionId: 's1', text: 'hi' },
  );
  const s = reduce({ ...base }, { type: 'historyLoaded', sessionId: 's1', history: { sessionId: 's1', busy: false, pending: null, todos: [], messages: [{ role: 'user', content: 'hi' }] } });
  assert.equal(s.blocks[0].kind, 'user');
  assert.equal(s.blocks[0].seq, undefined);
});

// ---------- 4. 编辑态：进入 / 取消都不动历史 ----------

test('编辑态：进入后能取消，取消一个字的历史都不动', () => {
  const blocks = timeline();
  const before = JSON.parse(JSON.stringify(blocks));
  const draft = beginEdit(blocks[3]); // 第二条（seq 20）
  assert.deepEqual(draft, { seq: 20, text: '第二条' });
  // 进入编辑态**不动历史**：这是"编辑"与"撤回"的分界（编辑是"我可能改主意"）
  assert.deepEqual(blocks, before, '进入编辑态不能动任何块');
  assert.equal(cancelEdit(), null, '取消 = 回到无编辑态');
  assert.deepEqual(blocks, before, '取消更不能动历史');
});

// ---------- 撤回的时序：先本地截断，失败再用后端真相对齐 ----------

test('撤回先本地乐观截断（在等后端应答之前就发出 rewound）', async () => {
  const agent = new WSAgent('localhost:1');
  const events = [];
  agent.emit = (e) => events.push(e);
  let release;
  agent.call = () => new Promise((res) => { release = res; });
  const pending = agent.rewind('s1', 7);
  assert.deepEqual(events, [{ type: 'rewound', sessionId: 's1', seq: 7, removed: 0 }], '点了就得清，不能等往返');
  release({ removed: 3 });
  assert.deepEqual(await pending, { removed: 3 });
});

test('撤回失败：重放历史用后端真相对齐（不让本地截断骗过刷新）', async () => {
  const agent = new WSAgent('localhost:1');
  const events = [];
  agent.emit = (e) => events.push(e);
  const calls = [];
  agent.call = (method) => {
    calls.push(method);
    if (method === 'chat.rewind') return Promise.reject(new Error('未知方法'));
    return Promise.resolve({ session_id: 's1', messages: [{ role: 'user', content: '还在后端', seq: 7 }], busy: false, pending: null, todos: [] });
  };
  await assert.rejects(() => agent.rewind('s1', 7), /未知方法/);
  assert.deepEqual(calls, ['chat.rewind', 'chat.history'], '失败后必须重放历史对齐');
  assert.equal(events.some((e) => e.type === 'historyLoaded'), true);
});
