// 子 Agent 卡内的"子执行过程"：子 Agent 是**独立会话**（AGENTS.md §2.3），它自己的
// messages 与压缩检查点在库里另存一份——父会话历史里没有这些明细。所以卡展开时按
// sessionId 懒加载子会话历史，用 **historyBlocks** 映射成子时间线。
//
// 这个文件钉住五件事（外加数据源侧两条）：
//   ① historyBlocks（抽出来的纯函数）与 reduceHistory 对**同一份子会话快照**产出的块
//      **逐字段一致**——这是"共用一份映射"的钉子：谁再写第二份简化映射，这条就红；
//   ② 空快照 → 空数组，不崩；
//   ③ 子会话里的 agent_dispatch 调用也建 dispatch 卡（嵌套派发——深度恒 1 是后端
//      两类制的保证，映射逻辑不该因此分叉）；
//   ④ 子会话里的压缩检查点 → 渲染成 compacted 块（它也是会话，有自己的 compaction）；
//   ⑤ 坏 JSON 参数不抛（历史来自 SQLite，残缺是既成事实）；
//   ⑥/⑦ 数据源侧：WSAgent.childHistory 走 chat.history 按子会话 id 寻址；DemoAgent
//      返回**空快照**（演示态没有子会话，不编数据、也不报错）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { historyBlocks, reduceHistory } from '../src/shared/history.ts';
import { WSAgent } from '../src/agent/ws/index.ts';
import { DemoAgent } from '../src/agent/demo/index.ts';

/** 与 reduceHistory 入参同形状的初始 UIState（测试里手写，避免依赖 store 的初始值）。 */
const base = {
  blocks: [],
  busy: false,
  pending: null,
  todos: [],
  currentId: 's-child-1',
  operationError: null,
  context: null,
  historyReady: false,
};

/** 剥掉 uid 再比：uid 是全局自增的（nextUid），两次调用必然不同——
 *  它不表达内容，比较它只会让"逐字段一致"变成假红。 */
const strip = (b) => {
  const { uid, ...rest } = b;
  if (Array.isArray(rest.subBlocks)) rest.subBlocks = rest.subBlocks.map(strip);
  return rest;
};
const stripAll = (blocks) => blocks.map(strip);

const checkpoint = (summary) =>
  '这是自动生成的检查点，浓缩了之前的一段对话以腾出上下文空间。把其中记录的上下文当作既成背景直接使用。\n\n' +
  '<compacted-summary>\n' + summary + '\n</compacted-summary>';

/** 后端在 dispatch 结果末尾追加的续跑提示行（internal/agent/tools.go 逐字）。 */
const hint = (childId) =>
  `\n\n[子会话 id: ${childId} —— 只在这次**没做完**时填进 session 参数续跑；已经给出结论就别再派]`;

/** 一份**真实的子会话历史**形状：任务说明书（user）→ 思考 + 工具调用 → 结果 →
 *  嵌套派发 → 检查点（压缩）→ 正文。 */
const childSnapshot = (overrides = {}) => ({
  sessionId: 's-child-1',
  busy: false,
  pending: null,
  todos: [],
  checkpoints: [5],
  messages: [
    // history[0]：任务说明书（子会话的头部保护对象，user 角色，在历史里）
    { role: 'user', content: '给 internal/agent 的工具循环加 per-tool 120s 超时兜底。验收：回归用例 + 全量测试绿。' },
    // 思考 + 读文件（普通工具行）
    { role: 'assistant', content: '', reasoning_content: '先看 runTools 怎么写的', tool_calls: [
      { id: 'c1', function: { name: 'read_file', arguments: JSON.stringify({ path: 'internal/agent/session.go', offset: 296, limit: 40 }) } },
    ] },
    { role: 'tool', content: '296→func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {', tool_call_id: 'c1' },
    // 子会话里的**嵌套派发**（深度恒 1，正常不会出现；映射不该因此分叉）
    { role: 'assistant', content: '这个子任务我转派给 tester。', tool_calls: [
      { id: 'd-nested', function: { name: 'agent_dispatch', arguments: JSON.stringify({ agent: 'tester', task: '跑一遍回归' }) } },
    ] },
    { role: 'tool', content: '测试全绿。' + hint('s-grandchild'), tool_call_id: 'd-nested' },
    // 子会话自己的压缩检查点（user 角色 + checkpoints 下标）
    { role: 'user', content: checkpoint('## 主要请求与意图\n- 子会话自己压缩过') },
    { role: 'assistant', content: '改动已完成，附上结论。' },
  ],
  ...overrides,
});

// ---- ① 共用一份映射（这个文件的核心钉子） ----

test('① historyBlocks 与 reduceHistory 对同一份子会话快照逐字段一致', () => {
  const h = childSnapshot();
  const viaPure = historyBlocks(h);
  const viaState = reduceHistory(base, h).blocks;
  assert.deepEqual(stripAll(viaPure), stripAll(viaState),
    '两条路径必须共用 historyBlocks——产出不一致 = 有人写了第二份映射');
  // 不是"两边都空"的假绿：这份快照真的产出多种块
  assert.ok(viaPure.length >= 5, '样例快照应产出多种块（否则这条断言是空转）');
  assert.deepEqual([...new Set(viaPure.map((b) => b.kind))].sort(),
    ['assistant', 'compacted', 'dispatch', 'tool', 'user']);
});

test('① 钉子：纯函数不碰 state（只吃快照，回放与子会话渲染同源）', () => {
  const h = childSnapshot();
  // 同一份快照调两次：uid 不同（每次重建都是新块），其余逐字段一致
  assert.deepEqual(stripAll(historyBlocks(h)), stripAll(historyBlocks(h)));
  // reduceHistory 的会话级字段仍由它自己装（busy/pending/todos/context）
  const s = reduceHistory(base, h);
  assert.equal(s.busy, false);
  assert.equal(s.pending, null);
  assert.deepEqual(s.todos, []);
  assert.equal(s.historyReady, true);
  assert.deepEqual(s.context, null);
});

// ---- ② 空快照 ----

test('② 空快照 → 空数组，不崩（子会话刚建/没有历史）', () => {
  const empty = { sessionId: 's-empty', messages: [], busy: false, pending: null, todos: [] };
  assert.deepEqual(historyBlocks(empty), []);
  assert.deepEqual(reduceHistory(base, empty).blocks, []);
  // 缺 messages 键的坏快照也不崩（老后端/演示快照的形态）
  assert.deepEqual(historyBlocks({ sessionId: 's-x' }), []);
});

// ---- ③ 嵌套派发 ----

test('③ 子会话里的 agent_dispatch 调用也建 dispatch 卡（嵌套派发，映射不分叉）', () => {
  const blocks = historyBlocks(childSnapshot());
  const cards = blocks.filter((b) => b.kind === 'dispatch');
  assert.equal(cards.length, 1);
  assert.equal(cards[0].id, 'd-nested');
  assert.equal(cards[0].agentId, 'tester');
  assert.equal(cards[0].task, '跑一遍回归');
  assert.equal(cards[0].status, 'done');
  assert.equal(cards[0].result, '测试全绿。');
  // 子会话 id 取自结果的提示行（续跑依据），不是 arguments.session
  assert.equal(cards[0].sessionId, 's-grandchild');
  // 普通工具行照旧建，且结果已回填（嵌套派发不许把别的工具行带坏）
  const rows = blocks.filter((b) => b.kind === 'tool');
  assert.equal(rows.length, 1);
  assert.equal(rows[0].name, 'read_file');
  assert.equal(rows[0].isError, false);
});

// ---- ④ 子会话自己的压缩检查点 ----

test('④ 子会话的压缩检查点渲染成 compacted 块（不是用户气泡）', () => {
  const blocks = historyBlocks(childSnapshot());
  const compacted = blocks.filter((b) => b.kind === 'compacted');
  assert.equal(compacted.length, 1, 'checkpoints 下标命中的 user 消息必须渲染成标记块');
  // 摘要正文剥掉了定界标记（前言是给模型看的，不该出现在界面上）
  assert.match(compacted[0].summary, /子会话自己压缩过/);
  assert.ok(!compacted[0].summary.includes('<compacted-summary>'), '定界标记不该渲染出来');
  // 它**不是**用户气泡（否则会是一个巨大的假用户消息，把真正的任务说明书淹没）
  assert.equal(blocks.filter((b) => b.kind === 'user').length, 1);
});

// ---- ⑤ 坏 JSON 参数 ----

test('⑤ 坏 JSON 参数不抛（截断的 arguments 来自 SQLite 的历史既成事实）', () => {
  const broken = '{"path":"internal/agent/ses'; // max_tokens 截断的形态（AGENTS.md §5 坑 12）
  const h = childSnapshot({
    messages: [
      { role: 'assistant', content: '', tool_calls: [
        { id: 'b1', function: { name: 'edit', arguments: broken } },
        { id: 'b2', function: { name: 'agent_dispatch', arguments: broken } },
      ] },
    ],
    checkpoints: [],
  });
  let blocks;
  assert.doesNotThrow(() => { blocks = historyBlocks(h); },
    '畸形历史抛异常会让子 Agent 卡整个渲染不出来（父会话看起来"卡坏了"）');
  // 工具行照建（参数原样保留，用户看得到原文），调度卡建出来但没有 Agent 名与任务
  assert.equal(blocks.filter((b) => b.kind === 'tool').length, 1);
  const card = blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.agentId, '');
  assert.equal(card.task, '');
  // 同一份快照走 reduceHistory 也不抛（两条路径同源）
  assert.doesNotThrow(() => reduceHistory(base, h));
});

// ---- ⑥/⑦ 数据源 ----

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor(url) { this.url = url; FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result) { this.receive({ id: this.sent.at(-1).id, result }); }
}

test('⑥ WSAgent.childHistory：按 session_id 走 chat.history，返回快照且不发 historyLoaded', async (t) => {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const events = [];
  const agent = new WSAgent('localhost:1234');
  const off = agent.subscribe((e) => events.push(e));
  t.after(off);

  const p = agent.childHistory('s-child-1');
  assert.equal(FakeSocket.latest.sent.at(-1).method, 'chat.history');
  assert.deepEqual(FakeSocket.latest.sent.at(-1).params, { session_id: 's-child-1' },
    '子会话历史按子会话 id 寻址（父会话 id 会读到主时间线）');
  FakeSocket.latest.reply({
    session_id: 's-child-1',
    messages: [{ role: 'assistant', content: '子会话的正文' }],
    busy: false,
    checkpoints: [0],
    context: { used: 1234, window: 8192 },
  });
  const snap = await p;
  assert.equal(snap.messages.length, 1);
  assert.deepEqual(snap.checkpoints, [0]);
  assert.deepEqual(snap.context, { used: 1234, window: 8192 });
  // **不发 historyLoaded**：子会话历史是卡内的只读补充，发它会把卡内历史当成
  // 主时间线整块重建（整个对话被子 Agent 的过程顶掉）
  assert.equal(events.some((e) => e.type === 'historyLoaded'), false,
    'childHistory 不许触发主时间线的历史重建');

  // 读失败（子会话不存在/老后端）必须向上抛——卡里要显示原因，不许静默
  const failing = agent.childHistory('s-missing');
  const last = FakeSocket.latest;
  last.receive({ id: last.sent.at(-1).id, error: { code: -32602, message: '会话不存在' } });
  await assert.rejects(failing, /会话不存在/);
});

test('⑦ DemoAgent.childHistory 返回空快照（演示态没有子会话，不编数据也不报错）', async () => {
  const demo = new DemoAgent();
  const snap = await demo.childHistory('s-child-1');
  assert.deepEqual(snap.messages, [], '演示态没有子会话历史——空快照是实话，编一份过程才是骗人');
  assert.equal(snap.busy, false);
  assert.equal(snap.pending, null);
  assert.deepEqual(snap.todos, []);
  // 空快照经同一份映射后就是空数组（卡里显示"没有可显示的历史"）
  assert.deepEqual(historyBlocks(snap), []);
});
