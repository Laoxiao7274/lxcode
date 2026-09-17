import test from 'node:test';
import assert from 'node:assert/strict';
import { WSAgent } from '../src/agent/ws/index.ts';
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
  return { agent, events, ws: FakeSocket.latest, off };
}
const flush = () => new Promise((resolve) => setImmediate(resolve));
test('JSON-RPC errors reject model changes and do not fake cache updates', async (t) => {
  const { agent, ws } = setup(t);
  const request = agent.modelAdmin.addModel({ id: 'a', base_url: 'http://test', model: 'a' });
  assert.equal(ws.sent.at(-1).method, 'model.add');
  const failed = assert.rejects(request, /denied/);
  ws.reply(undefined, { code: -1, message: 'denied' });
  await failed;
  assert.deepEqual(agent.models().models, []);
});
test('session failure emits operationError rather than terminating a chat', async (t) => {
  const { agent, ws, events } = setup(t);
  agent.newSession('project');
  assert.deepEqual(ws.sent.at(-1).params, { workspace: 'project' });
  ws.reply(undefined, { code: -1, message: 'busy' });
  await flush();
  assert.deepEqual(events, [{ type: 'operationError', message: '新建会话失败: busy' }]);
});
test('disconnect immediately rejects pending and unsubscribe stops reconnection', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { agent, ws, off } = setup(t);
  const request = agent.setRole('default', 'a');
  const failed = assert.rejects(request, /断开/);
  ws.close();
  await failed;
  off();
  t.mock.timers.tick(20_000);
  assert.equal(FakeSocket.latest, ws);
});
test('successful response clears deadline; late response after timeout is ignored', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { agent, ws } = setup(t);
  const first = agent.confirm('confirm-id', true);
  assert.equal(ws.sent.at(-1).method, 'tool.confirm');
  ws.reply({ ok: true });
  await first;
  const second = agent.confirm('next', false);
  const failed = assert.rejects(second, /超时/);
  t.mock.timers.tick(10_000);
  await failed;
  ws.reply({ ok: true });
});
test('renamed event refreshes list without reloading active generation history', async (t) => {
  const { agent, ws, events } = setup(t);
  ws.receive({ method: 'session.changed', params: { id: 's', reason: 'renamed' } });
  assert.equal(ws.sent.at(-1).method, 'session.list');
  ws.reply([{ id: 's', title: '中文', updated_at: 'today', messages: 2, archived: false, workspace: 'p' }]);
  await flush();
  assert.equal(agent.sessions()[0].updatedAt, 'today');
  assert.equal(agent.sessions()[0].workspace, 'p');
  assert.equal(events.some((e) => e.type === 'historyLoaded'), false);
});
