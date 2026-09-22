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
test('send carries effort and approval only when provided (omitempty wire shape)', async (t) => {
  const { agent, ws } = setup(t);
  agent.send('hi');
  assert.deepEqual(ws.sent.at(-1).params, { text: 'hi' });
  agent.send('hi', { effort: 'high', approval: 'strict' });
  assert.deepEqual(ws.sent.at(-1).params, { text: 'hi', effort: 'high', approval: 'strict' });
  agent.send('hi', { approval: 'auto' });
  assert.deepEqual(ws.sent.at(-1).params, { text: 'hi', approval: 'auto' });
});
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

/** 逐条应答初始化链（hello → 各 list → 可选 session.new → chat.history）。
 *  初始化链是 await 串行的，所以每轮只应答"最后一条未应答"的请求即可。 */
async function driveInit(socket) {
  const seen = new Set();
  const methods = [];
  for (let i = 0; i < 40; i++) {
    await flush();
    const last = socket.sent.at(-1);
    if (!last || seen.has(last.id)) break; // 没有新请求 → 链已跑完或卡住
    seen.add(last.id);
    methods.push(last.method);
    socket.reply({});
  }
  return methods;
}

test('project instructions round-trip over the wire (project_id only, no path)', async (t) => {
  const { agent, ws } = setup(t);
  const reading = agent.readInstructions('proj-1');
  assert.equal(ws.sent.at(-1).method, 'project.instructions.get');
  assert.deepEqual(ws.sent.at(-1).params, { project_id: 'proj-1' });
  ws.reply({ path: 'C:\\proj\\AGENTS.md', content: '# 守则\n', exists: true });
  const got = await reading;
  assert.equal(got.path, 'C:\\proj\\AGENTS.md');
  assert.equal(got.content, '# 守则\n');
  assert.equal(got.exists, true);

  const saving = agent.saveInstructions('proj-1', '新守则\n');
  assert.equal(ws.sent.at(-1).method, 'project.instructions.save');
  // 客户端只传项目 id 与内容——路径由服务端解析（越权面为零）
  assert.deepEqual(ws.sent.at(-1).params, { project_id: 'proj-1', content: '新守则\n' });
  ws.reply({ path: 'C:\\proj\\AGENTS.md', content: '新守则\n', exists: true });
  await saving;
});

test('boot opens a fresh session once (session.new before chat.history), never on reconnect', async (t) => {
  const { agent, ws } = setup(t);
  ws.onopen(); // 首次连接
  const methods = await driveInit(ws);
  assert.ok(methods.includes('session.new'), '首次连接应先切到新会话（打开软件即空会话）');
  assert.ok(
    methods.indexOf('session.new') < methods.indexOf('chat.history'),
    `session.new 必须在 chat.history 之前（否则会先闪出上次的对话）: ${methods.join(',')}`,
  );
  assert.equal(methods.filter((m) => m === 'session.new').length, 1, '初始化链里只应切一次会话');

  // 重连（同实例再次 onopen）：不能再切一次会话，否则把用户正在聊的会话吃掉
  ws.close();
  const reconnected = FakeSocket.latest;
  reconnected.onopen();
  const again = await driveInit(reconnected);
  assert.equal(again.includes('session.new'), false, '重连不应再新建会话');
  assert.ok(again.includes('chat.history'), '重连仍应重放历史');
  void agent;
});
