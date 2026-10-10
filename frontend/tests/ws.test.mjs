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

// 图片批次 B：附件随 chat.send 上车（images 纯 base64 不带前缀；files name/data），
// 不带附件时 wire 上无这两个键；只附件没文本也放行。
test('send carries images/files attachments; omits keys when absent', async (t) => {
  const { agent, ws } = setup(t);
  agent.send('s1', '看图', {
    images: [{ mime: 'image/png', data: 'aGVsbG8=' }],
    files: [{ name: '报告.txt', data: 'aGVsbG8=' }],
  });
  assert.deepEqual(ws.sent.at(-1).params, {
    session_id: 's1', text: '看图',
    images: [{ mime: 'image/png', data: 'aGVsbG8=' }],
    files: [{ name: '报告.txt', data: 'aGVsbG8=' }],
  });
  agent.send('s2', '纯文本');
  const params = ws.sent.at(-1).params;
  assert.equal(params.images, undefined);
  assert.equal(params.files, undefined);
  // 只附件没文本：照发（后端以「[附件]」行充当正文）
  agent.send('s3', '', { files: [{ name: 'a.bin', data: 'eA==' }] });
  const attsOnly = ws.sent.at(-1).params;
  assert.equal(attsOnly.text, '');
  assert.deepEqual(attsOnly.files, [{ name: 'a.bin', data: 'eA==' }]);
});
test('broadcast user messages retain the payload session and nested content', async (t) => {
  const { agent, ws, events } = setup(t);
  ws.receive({ method: 'chat.userMessage', params: { session_id: 's2', message: { role: 'user', content: 'target session' } } });
  assert.deepEqual(events, [{ type: 'userMessage', sessionId: 's2', text: 'target session' }]);
  void agent;
});
// 渠道私有设置（options）必须原样上车，且未给时按现值回填——
// 后端是整体覆盖语义，不回填的话「只改 key」会把已存的 zone 抹掉。
test('saveChannel forwards options and back-fills them from the cached snapshot', async (t) => {
  const { agent, ws } = setup(t);
  ws.receive({
    method: 'search.changed',
    params: {
      channels: [
        { id: 'brightdata', label: 'BD', desc: 'd', needs_key: true, needs_url: false,
          category: 'serp', category_label: 'SERP 代理', enabled: true, primary: false, configured: true,
          option_specs: [{ key: 'zone', label: 'SERP zone', required: true }],
          options: { zone: 'cached-zone' } },
      ],
      primary: 'brightdata', ready: true,
    },
  });
  // 显式给 options：原样上车。
  const first = agent.searchAdmin.saveChannel('brightdata', { apiKey: 'k', options: { zone: 'new-zone' } });
  assert.deepEqual(ws.sent.at(-1).params, {
    id: 'brightdata', api_key: 'k', base_url: '', options: { zone: 'new-zone' }, enabled: true,
  });
  ws.reply({});
  await first;
  // 不给 options（只改 key）：按缓存回填，不能把 zone 抹成空。
  const second = agent.searchAdmin.saveChannel('brightdata', { apiKey: 'k2' });
  assert.deepEqual(ws.sent.at(-1).params.options, { zone: 'cached-zone' });
  ws.reply({});
  await second;
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
// 归档顺带释放工作区：release_worktree 只在请求时上车（omitempty 线形），
// 应答的释放结果映射成 ArchiveOutcome（归档成功但释放失败必须如实带回原因）。
test('archive carries release_worktree only when requested and maps the outcome', async (t) => {
  const { agent, ws } = setup(t);
  const plain = agent.archiveSession('s-archive');
  assert.equal(ws.sent.at(-1).method, 'session.archive');
  assert.deepEqual(ws.sent.at(-1).params, { id: 's-archive', archived: true });
  ws.reply({ archived: true, released_worktree: false });
  assert.deepEqual(await plain, { archived: true, releasedWorktree: false, releaseError: '' });

  const releasing = agent.archiveSession('s-archive-2', true);
  assert.deepEqual(ws.sent.at(-1).params, { id: 's-archive-2', archived: true, release_worktree: true });
  ws.reply({ archived: true, released_worktree: false, release_error: '工作区有未提交或未跟踪改动' });
  assert.deepEqual(await releasing, { archived: true, releasedWorktree: false, releaseError: '工作区有未提交或未跟踪改动' });
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

/** 应答初始化链（hello → 并行清单组 → 可选 session.new/session.resume → chat.history）。
 *  清单刷新已并行化（allSettled）：每轮把**所有未应答**的请求都应答掉，
 *  直到没有新请求为止（并行组一次性上车，逐条应答会漏答先发的）。 */
async function driveInit(socket) {
  const seen = new Set();
  const methods = [];
  for (let i = 0; i < 40; i++) {
    await flush();
    const fresh = socket.sent.filter((r) => !seen.has(r.id));
    if (fresh.length === 0) break; // 没有新请求 → 链已跑完或卡住
    for (const req of fresh) {
      seen.add(req.id);
      methods.push(req.method);
      if (req.method === 'connection.hello') socket.receive({ id: req.id, result: { version: '2' } });
      else if (req.method === 'session.new') socket.receive({ id: req.id, result: { session_id: 'boot-session' } });
      else if (req.method === 'session.resume') socket.receive({ id: req.id, result: { session_id: 'boot-session' } });
      else socket.receive({ id: req.id, result: {} });
    }
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

// 切会话与 boot 同一条纪律：**历史先到、焦点后切**。
// 焦点一换，UI 立刻按该会话的状态渲染，而它的历史还在路上——渲染出来的是
// 空会话（EmptyState「我们做点什么？」），一个 WS 往返后再被真历史顶掉。
// 帧级实测（点击会话后采样 DOM）：+20ms 整块对话消失、空态出现，+48ms 空态
// 消失、内容回来——用户报的「进入会话闪两下」。所以断言的是**事件顺序**，
// 不是"有没有发过 chat.history"。
test('resume reads history before switching focus (no empty-state flash)', async (t) => {
  const { agent, ws, events } = setup(t);
  const resuming = agent.resumeSession('s-target');
  assert.equal(ws.sent.at(-1).method, 'session.resume');
  ws.reply({ session_id: 's-target' });
  await flush();
  assert.equal(ws.sent.at(-1).method, 'chat.history', 'session.resume 之后应立刻读历史');

  ws.reply({ session_id: 's-target', messages: [{ role: 'user', content: 'hi' }], busy: false });
  await resuming;

  const order = events.map((e) => e.type);
  assert.ok(
    order.indexOf('historyLoaded') < order.indexOf('sessionFocused'),
    `historyLoaded 必须先于 sessionFocused（否则先渲染空会话再被顶掉）: ${order.join(',')}`,
  );
});

test('answer 发送 tool.confirm 且带 answer 键（ask_user 提问的回答通道）', async (t) => {
  const { agent, ws } = setup(t);
  const answering = agent.answer('s1', 'ask-1', '选 A');
  assert.equal(ws.sent.at(-1).method, 'tool.confirm');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1', id: 'ask-1', allow: true, answer: '选 A' });
  ws.reply({ ok: true });
  await answering;
});

// ---- 体验修复批次 3：心跳 + 窗口恢复钩子 + 初始化链并行化 ----

/** 换上假 document（node 无 DOM）：记录监听器，测试里手动 dispatch
 *  visibilitychange。必须在 setup（subscribe 注册监听）之前装。 */
function fakeDocument(t) {
  const original = globalThis.document;
  const listeners = {};
  const doc = {
    visibilityState: 'visible',
    addEventListener: (type, fn) => { (listeners[type] ??= []).push(fn); },
    removeEventListener: (type, fn) => {
      listeners[type] = (listeners[type] ?? []).filter((f) => f !== fn);
    },
  };
  globalThis.document = doc;
  t.after(() => { globalThis.document = original; });
  return {
    dispatch: (type) => { for (const fn of listeners[type] ?? []) fn(); },
    hide: () => { doc.visibilityState = 'hidden'; },
    show: () => { doc.visibilityState = 'visible'; },
  };
}

test('heartbeat closes the connection after two consecutive ping timeouts', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { agent, ws, events } = setup(t);
  ws.onopen();
  await flush();
  // 第一跳：15s 到点发 connection.ping，不应答 → 3s 超时
  t.mock.timers.tick(15_000);
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping', '存活期内应每 15s 发一次心跳');
  t.mock.timers.tick(3_000);
  await flush();
  assert.equal(ws.readyState, 1, '第一次超时不应关闭连接（阈值是连续 2 次）');
  // 第二跳再超时 → 主动 close 走既有 onclose → 重连
  t.mock.timers.tick(15_000);
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping');
  t.mock.timers.tick(3_000);
  await flush();
  assert.equal(ws.readyState, 3, '连续两次超时应主动关闭连接触发重连');
  assert.ok(
    events.some((e) => e.type === 'operationError' && /后端连接断开/.test(e.message)),
    'close 走既有 onclose 路径（断连提示 + 5s 重连兜底）',
  );
  void agent;
});

test('a successful pong resets the heartbeat failure count', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { ws } = setup(t);
  ws.onopen();
  await flush();
  t.mock.timers.tick(15_000);
  await flush();
  t.mock.timers.tick(3_000);
  await flush(); // 第一跳超时（失败计数 1）
  t.mock.timers.tick(15_000);
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping');
  ws.reply({ pong: true }); // 第二跳成功 → 计数清零
  await flush();
  t.mock.timers.tick(15_000);
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping', '成功后心跳照常继续');
  assert.equal(ws.readyState, 1, '一次成功 pong 清零计数，连接保持');
});

test('heartbeat pings are silent (no operationError from ping failures)', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { ws, events } = setup(t);
  ws.onopen();
  await flush();
  t.mock.timers.tick(15_000);
  await flush();
  t.mock.timers.tick(3_000);
  await flush(); // 第一跳超时
  assert.equal(
    events.filter((e) => e.type === 'operationError' && /connection\.ping|心跳/.test(e.message)).length,
    0,
    '心跳失败只计数，不进 operationError',
  );
});

test('visibilitychange restore reconnects immediately when the socket is not open', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const doc = fakeDocument(t);
  const { agent, ws } = setup(t);
  ws.onopen();
  await driveInit(ws);
  ws.close(); // 断线 → onclose 排了 5s 重连 timer
  await flush();
  assert.notEqual(agent['reconnectTimer'], null, '断线后应有 5s 重连 timer');
  doc.dispatch('visibilitychange'); // 窗口恢复：立即重连，不等 5s
  await flush();
  const second = FakeSocket.latest;
  assert.notEqual(second, ws, '恢复可见应立即重连');
  assert.equal(agent['reconnectTimer'], null, 'pending 的重连 timer 应被清除');
  t.mock.timers.tick(5_000);
  await flush();
  assert.equal(FakeSocket.latest, second, '重连 timer 已清除，不再重复连接');
});

test('visibilitychange restore health-checks an open socket and reconnects on failure', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const doc = fakeDocument(t);
  const { ws } = setup(t);
  ws.onopen();
  await driveInit(ws);
  doc.dispatch('visibilitychange'); // WS OPEN → 立即发一次健康检查
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping', '恢复时应立即发心跳健康检查');
  t.mock.timers.tick(3_000);
  await flush();
  assert.equal(ws.readyState, 3, '健康检查失败应主动 close 触发重连');
});

test('hidden page pauses the heartbeat; restore resets its rhythm', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const doc = fakeDocument(t);
  const { ws } = setup(t);
  ws.onopen();
  await flush();
  doc.hide(); // 页面隐藏：暂停心跳
  doc.dispatch('visibilitychange');
  t.mock.timers.tick(60_000);
  await flush();
  const pingsBefore = ws.sent.filter((r) => r.method === 'connection.ping').length;
  assert.equal(pingsBefore, 0, '隐藏期间不应发心跳（后台节流下定时器不可靠）');
  doc.show(); // 恢复可见：WS OPEN → 立即健康检查并重置心跳节奏
  doc.dispatch('visibilitychange');
  await flush();
  assert.equal(ws.sent.at(-1).method, 'connection.ping', '恢复可见应立即健康检查');
});

test('init refreshes run in parallel and one failure does not break the rest', async (t) => {
  const { agent, events, ws } = setup(t);
  ws.onopen();
  ws.reply({ version: '2' }); // 只应答 hello → 并行组应一次性全部发出
  await flush();
  await flush();
  const methods = ws.sent.map((r) => r.method);
  for (const m of ['model.list', 'project.list', 'session.list', 'agent.list', 'catalog.modules.list',
    'catalog.tools.list', 'catalog.mcp.list', 'search.channels.list', 'job.list']) {
    assert.ok(methods.includes(m), `并行组应发出 ${m}`);
  }
  // model.list 故意报错，其余正常应答
  for (const req of ws.sent) {
    if (req.method === 'connection.hello') continue;
    if (req.method === 'model.list') ws.receive({ id: req.id, error: { code: -1, message: 'boom' } });
    else if (req.method === 'session.list') {
      ws.receive({ id: req.id, result: [{ id: 's1', title: 'x', updated_at: 'now', messages: 1, archived: false }] });
    } else ws.receive({ id: req.id, result: {} });
  }
  await flush();
  await flush();
  assert.equal(ws.sent.at(-1).method, 'session.new', '并行组之后应照常走到 session.new');
  ws.receive({ id: ws.sent.at(-1).id, result: { session_id: 'boot-1' } });
  await flush();
  await flush();
  assert.equal(ws.sent.at(-1).method, 'chat.history', '焦点落定后应照常读历史');
  ws.receive({ id: ws.sent.at(-1).id, result: { session_id: 'boot-1', messages: [], busy: false } });
  await flush();
  assert.ok(
    events.some((e) => e.type === 'operationError' && /model\.list/.test(e.message)),
    '失败的那个报 operationError',
  );
  assert.deepEqual(
    agent.sessions().map((s) => s.id),
    ['s1'],
    '其余并行刷新不受单个失败影响，照常落地',
  );
});
