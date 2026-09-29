// 权限模式实时化（契约 D 节）的前端契约：
// ① 改档必须**同时**发给后端（chat.approval）——只改本地设置的话，正在跑的那一轮
//    还在按开轮时的档位弹确认（用户实测的 bug）；
// ② 收到后端广播的 chat.approvalChanged 要反向同步设置（多客户端/壳+浏览器一致）。
//
// 两条都钉在「静默走偏」上：方法名/键名写错不会让 tsc 报错，只会变成后端一句
// 「未知方法」或者前端设置没变——用户看到的就是「我说了别问，它还在问」。
import test from 'node:test';
import assert from 'node:assert/strict';
import { mapEvent } from '../src/agent/ws/events.ts';
import { approvalSyncTarget, normalizeApproval, subscribeApprovalSync } from '../src/shared/approval.ts';
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

/** 逐条应答初始化链（hello → 各 list → session.new → chat.history）——
 *  与 ws.test.mjs 同款：setApproval 要的是**当前会话 id**，只有走完 boot 才有。 */
async function driveInit(socket) {
  const seen = new Set();
  const methods = [];
  for (let i = 0; i < 40; i++) {
    await flush();
    const last = socket.sent.at(-1);
    if (!last || seen.has(last.id)) break;
    seen.add(last.id);
    methods.push(last.method);
    if (last.method === 'connection.hello') socket.reply({ version: '2' });
    else if (last.method === 'session.new') socket.reply({ session_id: 'boot-session' });
    else if (last.method === 'session.resume') socket.reply({ session_id: 'boot-session' });
    else socket.reply({});
  }
  return methods;
}

// ---------- ① 改档发给后端 ----------

test('改档发给后端：chat.approval 带当前会话与档位（契约 B1 的键名逐字对齐）', async (t) => {
  const { agent, ws } = setup(t);
  ws.onopen();
  await driveInit(ws);
  const changing = agent.setApproval('auto');
  assert.equal(ws.sent.at(-1).method, 'chat.approval');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 'boot-session', approval: 'auto' });
  ws.reply({ approval: 'auto' });
  await changing;
  // 三档都照原样上车（后端负责校验；前端不自己改档位名）
  for (const mode of ['confirm', 'strict']) {
    const p = agent.setApproval(mode);
    assert.deepEqual(ws.sent.at(-1).params, { session_id: 'boot-session', approval: mode });
    ws.reply({ approval: mode });
    await p;
  }
});

test('改档不依赖下一次 chat.send：send 的请求级 approval 与它各自独立', async (t) => {
  const { agent, ws } = setup(t);
  ws.onopen();
  await driveInit(ws);
  const changing = agent.setApproval('auto');
  ws.reply({ approval: 'auto' });
  await changing;
  // chat.send 仍带请求级 approval（契约 E：chat.send 的参数语义不变）
  agent.send('boot-session', 'hi', { approval: 'strict' });
  const last = ws.sent.at(-1);
  assert.equal(last.method, 'chat.send');
  assert.equal(last.params.approval, 'strict');
});

// ---------- ② 收到广播反向同步设置 ----------

test('chat.approvalChanged 的 wire 形状（snake_case）与档位归一', () => {
  assert.deepEqual(
    mapEvent('chat.approvalChanged', { session_id: 's1', approval: 'strict' }),
    { type: 'approvalChanged', sessionId: 's1', approval: 'strict' },
  );
  // 空/未知 = confirm（协议：空 = 回落 confirm）——写进设置的是合法档位，不是 undefined
  assert.equal(mapEvent('chat.approvalChanged', { session_id: 's1' }).approval, 'confirm');
  assert.equal(normalizeApproval('nonsense'), 'confirm');
  assert.equal(normalizeApproval(undefined), 'confirm');
  assert.equal(normalizeApproval('auto'), 'auto');
});

test('反向同步只认当前会话（多客户端不串台）', () => {
  const ev = { type: 'approvalChanged', sessionId: 's1', approval: 'auto' };
  assert.equal(approvalSyncTarget(ev, 's1'), 'auto');
  assert.equal(approvalSyncTarget(ev, 's2'), null, '别的会话的档位不能改写本端设置');
  assert.equal(approvalSyncTarget({ ...ev, sessionId: '' }, ''), null, '载荷缺会话 id 时不猜');
  assert.equal(approvalSyncTarget({ type: 'busy', sessionId: 's1', busy: true }, 's1'), null);
});

test('收到 chat.approvalChanged 同步设置（订阅路径：事件 → 设置写入）', (t) => {
  const listeners = new Set();
  const fakeSource = { subscribe(l) { listeners.add(l); return () => listeners.delete(l); } };
  const applied = [];
  const off = subscribeApprovalSync(fakeSource, (mode) => applied.push(mode));
  t.after(off);
  const deliver = (ev) => listeners.forEach((l) => l(ev));

  deliver({ type: 'sessionFocused', id: 's1' });
  // 后端广播（先过 mapEvent 的纯映射——键名写错这里就会是 confirm/空）
  deliver(mapEvent('chat.approvalChanged', { session_id: 's1', approval: 'auto' }));
  assert.deepEqual(applied, ['auto'], '当前会话的广播必须落到设置');

  deliver(mapEvent('chat.approvalChanged', { session_id: 's2', approval: 'strict' }));
  assert.deepEqual(applied, ['auto'], '别的会话的广播不落本端设置');

  deliver(mapEvent('chat.approvalChanged', { session_id: 's1', approval: '' }));
  assert.deepEqual(applied, ['auto', 'confirm'], '空档位回落 confirm');

  deliver(mapEvent('chat.delta', { session_id: 's1', kind: 'text', text: 'x' }));
  assert.deepEqual(applied, ['auto', 'confirm'], '无关事件不触发设置写入');
});

// ---------- 演示实现（能力接口三个实现都要有） ----------

test('演示模式：改档 → 广播 → 设置同步（真实实现走完整条路径）', async (t) => {
  const agent = new DemoAgent();
  const events = [];
  agent.subscribe((e) => events.push(e));
  const applied = [];
  const off = subscribeApprovalSync(agent, (mode) => applied.push(mode));
  t.after(off);

  await agent.setApproval('strict');
  const ev = events.at(-1);
  assert.equal(ev.type, 'approvalChanged', '演示源也要广播（真后端改档会广播给所有端）');
  assert.equal(ev.approval, 'strict');
  assert.equal(ev.sessionId, agent.currentId);
  assert.deepEqual(applied, ['strict'], '演示模式也要走通「改档 → 广播 → 设置同步」');

  // 归一：未知档位不写进事件载荷（写进设置会让选择器静默空掉）
  await agent.setApproval('nonsense');
  assert.equal(events.at(-1).approval, 'confirm');
  assert.deepEqual(applied, ['strict', 'confirm']);
});
