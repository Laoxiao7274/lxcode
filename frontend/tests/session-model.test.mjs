// 模型选择作用域（会话级模型）的前端契约：
// ① 切模型发给后端走 **chat.model**（会话级），不再是 role.set（全局 default 角色）
//    ——后者正是「一个会话切模型，其他所有会话跟着变」的成因；
// ② 收到 chat.modelChanged 按会话归约（每条会话显示自己的模型，不串台）；
// ③ 选中态来自会话 state.model（chat.history 的 model 键），不来自全局角色绑定。
//
// 三条都钉在「静默走偏」上：方法名/键名写错不会让 tsc 报错，只会变成后端一句
// 「未知方法」或者选择器静默停在旧模型上。
import test from 'node:test';
import assert from 'node:assert/strict';
import { mapEvent } from '../src/agent/ws/events.ts';
import { reduce, reduceSessionStates } from '../src/shared/reduce.ts';
import { initial } from '../src/shared/blocks.ts';
import { WSAgent } from '../src/agent/ws/index.ts';
import { DemoAgent } from '../src/agent/demo/index.ts';

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor(url) { this.url = url; FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result, error) { this.receive({ id: this.sent.at(-1).id, result, error }); }
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
const flush = () => new Promise((resolve) => setImmediate(resolve));

/** 应答初始化链（hello → 并行清单组 → session.new → chat.history），
 *  与 approval.test.mjs 同款：chat.model 要的是**当前会话 id**。 */
async function driveInit(socket) {
  const seen = new Set();
  for (let i = 0; i < 40; i++) {
    await flush();
    const fresh = socket.sent.filter((r) => !seen.has(r.id));
    if (fresh.length === 0) break;
    for (const req of fresh) {
      seen.add(req.id);
      if (req.method === 'connection.hello') socket.receive({ id: req.id, result: { version: '2' } });
      else if (req.method === 'session.new') socket.receive({ id: req.id, result: { session_id: 'boot-session' } });
      else if (req.method === 'session.resume') socket.receive({ id: req.id, result: { session_id: 'boot-session' } });
      else socket.receive({ id: req.id, result: {} });
    }
  }
}

// ---------- ① 切模型走会话级方法 ----------

test('切模型发给后端：chat.model 带当前会话与模型 id（不是 role.set）', async (t) => {
  const { agent, ws } = setup(t);
  ws.onopen();
  await driveInit(ws);
  ws.sent.length = 0;

  const done = agent.setSessionModel('deepseek-v3');
  const req = ws.sent.at(-1);
  assert.equal(req.method, 'chat.model', '切模型必须走会话级方法');
  assert.deepEqual(req.params, { session_id: 'boot-session', model_id: 'deepseek-v3' });
  ws.reply({ session_id: 'boot-session', model_id: 'deepseek-v3' });
  await done;

  // 关键回归：**不得**退化成 role.set（那是全局 default 角色绑定）
  assert.ok(!ws.sent.some((r) => r.method === 'role.set'), '切模型不该改全局 default 角色');
});

// ---------- ② 广播按会话归约 ----------

test('chat.modelChanged 的 wire 形状（snake_case）与事件映射', () => {
  assert.deepEqual(
    mapEvent('chat.modelChanged', { session_id: 's1', model_id: 'glm-4.6' }),
    { type: 'modelChanged', sessionId: 's1', model: 'glm-4.6' },
  );
});

test('模型变更按会话落状态：两条会话各显示自己的模型（不串台）', () => {
  let states = {};
  states = reduceSessionStates(states, { type: 'modelChanged', sessionId: 'a', model: 'm2' });
  states = reduceSessionStates(states, { type: 'modelChanged', sessionId: 'b', model: 'm1' });
  assert.equal(states.a.model, 'm2');
  assert.equal(states.b.model, 'm1');

  // 再改 a 不影响 b
  states = reduceSessionStates(states, { type: 'modelChanged', sessionId: 'a', model: 'm3' });
  assert.equal(states.a.model, 'm3');
  assert.equal(states.b.model, 'm1');
});

test('单会话归约：modelChanged 只改 model 字段，不动时间线/忙闲', () => {
  const before = { ...initial, model: 'm1', busy: true };
  const after = reduce(before, { type: 'modelChanged', sessionId: 's1', model: 'm2' });
  assert.equal(after.model, 'm2');
  assert.equal(after.busy, true);
  assert.equal(after.blocks, before.blocks);
});

// ---------- ③ 演示数据源同契约 ----------

test('演示数据源：切模型广播 modelChanged，且只改当前会话', async () => {
  const demo = new DemoAgent();
  const events = [];
  const off = demo.subscribe((e) => events.push(e));
  await demo.setSessionModel('glm-4.6');
  const ev = events.filter((e) => e.type === 'modelChanged').at(-1);
  assert.equal(ev.model, 'glm-4.6');
  // 真后端成功才广播；演示态同样广播——归约层那条路径在演示模式里也走得到
  assert.ok(ev.sessionId);
  off();
});
