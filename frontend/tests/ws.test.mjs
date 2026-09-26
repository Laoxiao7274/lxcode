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
  agent.send('s1', 'hi');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1', text: 'hi' });
  agent.send('s1', 'hi', { effort: 'high', approval: 'strict' });
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1', text: 'hi', effort: 'high', approval: 'strict' });
  agent.send('s2', 'hi', { approval: 'auto' });
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's2', text: 'hi', approval: 'auto' });
});
test('broadcast user messages retain the payload session and nested content', async (t) => {
  const { agent, ws, events } = setup(t);
  ws.receive({ method: 'chat.userMessage', params: { session_id: 's2', message: { role: 'user', content: 'target session' } } });
  assert.deepEqual(events, [{ type: 'userMessage', sessionId: 's2', text: 'target session' }]);
  void agent;
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
test('releasing a session worktree calls the scoped protocol method', async (t) => {
  const { agent, ws } = setup(t);
  const release = agent.releaseWorktree('s-worktree');
  assert.equal(ws.sent.at(-1).method, 'session.worktree.release');
  assert.deepEqual(ws.sent.at(-1).params, { id: 's-worktree' });
  ws.reply({ released: true });
  await release;
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
  const first = agent.confirm('s1', 'confirm-id', true);
  assert.equal(ws.sent.at(-1).method, 'tool.confirm');
  ws.reply({ ok: true });
  await first;
  const second = agent.confirm('s1', 'next', false);
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
  assert.ok(events.some((e) => e.type === 'sessionsChanged'));
  assert.equal(events.some((e) => e.type === 'historyLoaded'), false);
});
test('created metadata event signals the session list after refreshing its cache', async (t) => {
  const { agent, ws, events } = setup(t);
  ws.receive({ method: 'session.changed', params: { id: 's2', reason: 'created' } });
  ws.reply([{ id: 's2', title: '新会话', updated_at: 'now', messages: 1, archived: false }]);
  await flush();
  assert.equal(agent.sessions()[0].id, 's2');
  assert.ok(events.some((e) => e.type === 'sessionsChanged'));
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
    if (last.method === 'connection.hello') socket.reply({ version: '2' });
    else if (last.method === 'session.new') socket.reply({ session_id: 'boot-session' });
    else if (last.method === 'session.resume') socket.reply({ session_id: 'boot-session' });
    else socket.reply({});
  }
  return methods;
}

// P1：上下文占用随 chat.done（主轮）与 chat.history 过 wire——映射后进 store。
test('context usage rides chat.done and chat.history (main turn only)', async (t) => {
  const { agent, ws, events } = setup(t);
  const ctx = { used: 777, window: 32768, system: 300, tool_results: 200, messages: 277 };
  ws.receive({ method: 'chat.done', params: { usage_tokens: 12, finish_reason: 'stop', context: ctx } });
  const done = events.find((e) => e.type === 'done');
  assert.deepEqual(done.context, ctx, '主轮 done 的 context 应原样映射');

  // 子轮的 done 不带 context（后端已按 dispatch 归属收口）
  ws.receive({ method: 'chat.done', params: { usage_tokens: 730, finish_reason: 'stop', dispatch_id: 'd1' } });
  const subs = events.filter((e) => e.type === 'done' && e.dispatchId === 'd1');
  assert.equal(subs.at(-1).context, undefined, '子轮 done 不应带 context');
});

test('historyLoaded carries the measured context (absent stays undefined)', async (t) => {
  const { agent, events } = setup(t);
  const p = agent['loadHistory']('s1');
  const last = FakeSocket.latest.sent.at(-1);
  assert.equal(last.method, 'chat.history');
  FakeSocket.latest.reply({ session_id: 's1', messages: [], busy: false, context: { used: 100, window: 8192 } });
  await p;
  assert.deepEqual(events.at(-1).history.context, { used: 100, window: 8192 });

  // 未知占用（后端刚重启）：整键缺席 → undefined，指示器据此显示中性态
  const q = agent['loadHistory']();
  FakeSocket.latest.reply({ session_id: 's1', messages: [], busy: false });
  await q;
  assert.equal(events.at(-1).history.context, undefined);
  assert.deepEqual(events.at(-1).history.checkpoints, [], '无检查点时为空列表（与 todos 同款）');
});

// P3：压缩事件过 wire + 历史检查点下标 + 手动压缩调用。
test('compacted event and history checkpoints ride the wire', async (t) => {
  const { agent, ws, events } = setup(t);
  ws.receive({ method: 'chat.compacted', params: { session_id: 's1', before: 8000, after: 2400, shadowed: 12, summary: '摘要', manual: true } });
  const ev = events.find((e) => e.type === 'compacted');
  assert.deepEqual(ev, { type: 'compacted', sessionId: 's1', before: 8000, after: 2400, shadowed: 12, summary: '摘要', manual: true, dispatchId: undefined });

  // 子会话自己的压缩带 dispatch_id（归属进卡内，不插主时间线）
  ws.receive({ method: 'chat.compacted', params: { session_id: 'parent-1', before: 100, after: 40, shadowed: 3, summary: '子摘要', dispatch_id: 'd1' } });
  const sub = events.filter((e) => e.type === 'compacted').at(-1);
  assert.equal(sub.dispatchId, 'd1');

  // 子会话 id 随 dispatchStart/End 过 wire（前端显示 + 续跑依据）
  ws.receive({ method: 'chat.dispatchStart', params: { owner_session_id: 'parent-1', dispatch_id: 'd1', session_id: 'child-1', agent_id: 'coder', agent_name: '代码 Agent', agent_color: '#000', task: 't' } });
  assert.equal(events.find((e) => e.type === 'dispatchStart').sessionId, 'parent-1');
  assert.equal(events.find((e) => e.type === 'dispatchStart').childSessionId, 'child-1');
  ws.receive({ method: 'chat.dispatchEnd', params: { owner_session_id: 'parent-1', dispatch_id: 'd1', session_id: 'child-1', result: 'r' } });
  assert.equal(events.filter((e) => e.type === 'dispatchEnd').at(-1).sessionId, 'parent-1');
  assert.equal(events.filter((e) => e.type === 'dispatchEnd').at(-1).childSessionId, 'child-1');

  // 历史里的检查点下标（渲染标记块的依据）
  const p = agent['loadHistory']('s1');
  FakeSocket.latest.reply({ session_id: 's1', messages: [{ role: 'user', content: 'x' }], busy: false, checkpoints: [0] });
  await p;
  assert.deepEqual(events.at(-1).history.checkpoints, [0]);

  // 手动压缩：无参数调用；应答原样回给调用方（compacted=false 不是错误）
  const compacting = agent.compact('s1');
  assert.equal(ws.sent.at(-1).method, 'chat.compact');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1' });
  ws.reply({ compacted: false });
  assert.deepEqual(await compacting, { compacted: false });
});

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

test('hello sends protocol v2 and stops initialization against an incompatible server', async (t) => {
  const { agent, events, ws } = setup(t);
  ws.onopen();
  assert.equal(ws.sent[0].method, 'connection.hello');
  assert.deepEqual(ws.sent[0].params, { client: 'lxcode-web', version: '2' });
  ws.reply({ version: '1' });
  await flush();
  assert.equal(ws.sent.length, 1, '协议不匹配时不能继续请求模型/会话列表');
  assert.ok(events.some((e) => e.type === 'operationError' && /协议版本不兼容/.test(e.message)));
  void agent;
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
  agent['connect']();
  const reconnected = FakeSocket.latest;
  reconnected.onopen();
  const again = await driveInit(reconnected);
  assert.equal(again.includes('session.new'), false, '重连不应再新建会话');
  assert.ok(again.includes('session.resume'), '重连恢复原焦点会话');
  assert.ok(again.includes('chat.history'), '重连仍应重放历史');
  void agent;
});
