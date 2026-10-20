// project-live-remove.test.mjs —— 项目列表「新增实时显示 + 可移除」的钉子（2026-12）：
//
//  用户报两件事：① 新增项目后列表不出现，必须重启 lxcode 才看得到；
//  ② 没有删除项目的地方。
//
// ① 的根因在连接层：后端 `project.add` 成功后广播 `project.changed`，而
//   WSAgent 的事件副作用里**没有订阅它**——只有握手并行组拉过一次 project.list。
//   这里钉住「收到 project.changed → 重拉 project.list → 缓存落地 + 发
//   projectsChanged」这条链（少了任何一环，侧栏都停在旧列表上）。
// ② 后端 project.remove 的语义是**只解绑**（不删磁盘文件、不删会话）；前端入口
//   在侧栏项目行（hover 的「移除」），二次确认对话框写清两件不会发生的事。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

// ---- ① 连接层：project.changed → 重拉 project.list ----

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor(url) { this.url = url; FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result, error) { this.receive({ id: this.sent.at(-1).id, result, error }); }
}

const { WSAgent } = await import('../src/agent/ws/index.ts');

function setup(t) {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const events = [];
  const agent = new WSAgent('localhost:1234');
  const off = agent.subscribe((e) => events.push(e));
  t.after(off);
  return { agent, events, ws: FakeSocket.latest };
}
const flush = () => new Promise((resolve) => setImmediate(resolve));

test('① project.changed 广播 → 重拉 project.list → 新项目立刻进缓存并通知 UI', async (t) => {
  const { agent, events, ws } = setup(t);
  // 先走一遍握手（列表刷新链在 hello 之后才跑）
  ws.onopen();
  ws.reply({ version: '2' });
  await flush();
  for (const req of ws.sent) {
    if (req.method === 'connection.hello') continue;
    if (req.method === 'project.list') ws.receive({ id: req.id, result: [{ id: 'p1', name: '老项目', path: 'C:\\old' }] });
    else if (req.method === 'session.list') ws.receive({ id: req.id, result: [] });
    else ws.receive({ id: req.id, result: {} });
  }
  await flush();
  assert.deepEqual(agent.projects().map((p) => p.id), ['p1'], '握手后列表已落地');

  // 别的客户端（或本端）加了项目 → 后端广播 project.changed
  events.length = 0;
  const before = ws.sent.length;
  ws.receive({ method: 'project.changed', params: {} });
  await flush();

  const refresh = ws.sent.slice(before).find((r) => r.method === 'project.list');
  assert.ok(refresh, '收到 project.changed 必须重拉 project.list（否则列表停在旧值上，要重启才更新）');
  // 后端事实源回来：新项目出现
  ws.receive({ id: refresh.id, result: [
    { id: 'p2', name: '新项目', path: 'C:\\new' },
    { id: 'p1', name: '老项目', path: 'C:\\old' },
  ] });
  await flush();

  assert.deepEqual(agent.projects().map((p) => p.id), ['p2', 'p1'], '缓存立刻包含新项目');
  assert.ok(events.some((e) => e.type === 'projectsChanged'), '发 projectsChanged 让侧栏重读');
});

test('①c project.changed 也重拉 session.list（移除项目后会话落进未分组）', async (t) => {
  const { agent, ws } = setup(t);
  ws.onopen();
  ws.reply({ version: '2' });
  await flush();
  for (const req of ws.sent) {
    if (req.method === 'connection.hello') continue;
    if (req.method === 'session.list') ws.receive({ id: req.id, result: [{ id: 's1', title: 't', updated_at: 'now', messages: 1, workspace: 'p1' }] });
    else if (req.method === 'project.list') ws.receive({ id: req.id, result: [{ id: 'p1', name: 'P', path: 'C:\p' }] });
    else ws.receive({ id: req.id, result: {} });
  }
  await flush();
  assert.equal(agent.sessions()[0].workspace, 'p1');

  const before = ws.sent.length;
  ws.receive({ method: 'project.changed', params: {} });
  await flush();
  const sent = ws.sent.slice(before);
  const sess = sent.find((r) => r.method === 'session.list');
  assert.ok(sess, 'project.changed 也要重拉 session.list（会话归属可能被清空）');
  ws.receive({ id: sess.id, result: [{ id: 's1', title: 't', updated_at: 'now', messages: 1, workspace: '' }] });
  await flush();
  assert.equal(agent.sessions()[0].workspace, '', '会话立刻显示为未分组');
});

test('①b project.changed 的刷新失败报 operationError（不静默吞）', async (t) => {
  const { events, ws } = setup(t);
  ws.onopen();
  ws.reply({ version: '2' });
  await flush();
  for (const req of ws.sent) {
    if (req.method === 'connection.hello') continue;
    if (req.method === 'project.list') ws.receive({ id: req.id, result: [] });
    else if (req.method === 'session.list') ws.receive({ id: req.id, result: [] });
    else ws.receive({ id: req.id, result: {} });
  }
  await flush();
  events.length = 0;
  const before = ws.sent.length;
  ws.receive({ method: 'project.changed', params: {} });
  await flush();
  const refresh = ws.sent.slice(before).find((r) => r.method === 'project.list');
  assert.ok(refresh);
  ws.receive({ id: refresh.id, error: { code: -1, message: 'boom' } });
  await flush();
  assert.ok(
    events.some((e) => e.type === 'operationError' && /项目列表/.test(e.message)),
    '刷新失败要有可见提示',
  );
});

// ---- ② 前端 removeProject：走 project.remove 且只解绑 ----

test('② removeProject 发 project.remove（参数 project_id），列表更新靠广播', async (t) => {
  const { agent, events, ws } = setup(t);
  const p = agent.removeProject('p1');
  const req = ws.sent.at(-1);
  assert.equal(req.method, 'project.remove');
  assert.deepEqual(req.params, { project_id: 'p1' });
  // 后端广播回来后才更新缓存（事实源在后端）
  ws.receive({ id: req.id, result: { removed: true, sessions: 2 } });
  await p;
  ws.receive({ method: 'project.changed', params: {} });
  await flush();
  // 重拉顺序：先 project.list 再 session.list（两条都要，见 ①c）
  const refresh = ws.sent.slice(-2).find((r) => r.method === 'project.list');
  assert.ok(refresh, '移除后要重拉 project.list');
  ws.receive({ id: refresh.id, result: [] });
  await flush();
  assert.deepEqual(agent.projects(), [], '移除后列表不再有它');
  assert.ok(events.some((e) => e.type === 'projectsChanged'));
});

test('②b removeProject 失败向上抛（有会话在跑 → 后端拒绝，调用方显示原因）', async (t) => {
  const { agent, ws } = setup(t);
  const p = agent.removeProject('p1');
  const req = ws.sent.at(-1);
  ws.receive({ id: req.id, error: { code: -32602, message: '该项目还有 1 个会话正在运行——请先停止它们再移除项目' } });
  await assert.rejects(p, /正在运行/);
});

// ---- ③ 侧栏入口：项目行有「移除」按钮，二次确认对话框写清不删文件/不删会话 ----

test('③ 侧栏项目行渲染「移除」入口（hover 出现，点击不冒泡到项目行）', () => {
  const here = new URL('.', import.meta.url);
  const src = readFileSync(new URL('../src/components/sidebar/Sidebar.tsx', here), 'utf8');
  assert.match(src, /className="proj-remove-btn"/, '项目行要有移除按钮');
  assert.match(src, /data-remove=\{p\.id\}/, '移除按钮带项目 id（可测/可定位）');
  assert.match(src, /onClick=\{\(e\) => \{ e\.stopPropagation\(\); setRemoveFor\(p\); \}\}/, '点移除不触发项目行的过滤跳转');
  assert.match(src, /<RemoveProjectDialog/, '确认对话框挂载在侧栏');
  assert.match(src, /source\.removeProject\(removeFor\.id\)/, '确认后走 AgentSource.removeProject');
  // 移除的正是当前筛选范围 → 退回未分组（否则停在一个不存在的项目上）
  assert.match(src, /if \(filter === removeFor\.id\) setFilter\(LOOSE\)/, '移除当前项目后范围退回未分组');
});

test('③b 确认对话框文案：只说「移除」，明说不会删磁盘文件、会话保留', async () => {
  globalThis.window = { __LX__: undefined };
  const { RemoveProjectDialog } = await import('../src/components/sidebar/RemoveProjectDialog.tsx');
  const html = renderToStaticMarkup(createElement(RemoveProjectDialog, {
    project: { id: 'p1', name: '示例项目', path: 'C:\\work\\demo' },
    sessionCount: 3,
    onRemove: async () => {},
    onClose: () => {},
  }));
  assert.match(html, /移除项目「示例项目」/);
  assert.match(html, /C:\\work\\demo/);
  assert.match(html, /不会删除磁盘上的任何文件/, '必须明说磁盘文件不受影响');
  assert.match(html, /3 条会话保留在库里/, '会话数要说清（保留在库里）');
  assert.match(html, /归入「未分组」/, '说清会话去哪了（不会变成找不到的孤儿）');
  assert.match(html, /重新「添加项目」/, '给出可恢复路径');
  // 按钮文字用「移除」而不是「删除」（删除会让人以为连磁盘代码一起没了）
  assert.match(html, />移除项目</, '确认按钮是「移除项目」');
  assert.doesNotMatch(html, />删除项目</, '不用「删除项目」这类会误导的措辞');
});

test('③c 移除按钮与守则按钮同款出现时机（CSS 源码钉子）', () => {
  const css = readFileSync(new URL('../src/styles/composer.css', import.meta.url), 'utf8');
  assert.match(css, /\.proj-row:hover \.proj-remove-btn/, 'hover 才出现（不占视觉重量）');
  assert.match(css, /\.proj-remove-btn:focus-visible/, '键盘可达');
});

// ---- ④ DemoAgent 同语义（演示模式也能验删除） ----

test('④ DemoAgent.removeProject 只从列表拿掉并广播', async () => {
  const { DemoAgent } = await import('../src/agent/demo/index.ts');
  const demo = new DemoAgent();
  const events = [];
  demo.subscribe((e) => events.push(e));
  demo.addProject('甲', 'C:\\a');
  demo.addProject('乙', 'C:\\b');
  const before = demo.projects().length; // 演示库自带两个项目，这里只验增量
  const target = demo.projects().find((p) => p.name === '甲');
  assert.ok(target, '新加的项目在列表里');
  await demo.removeProject(target.id);
  assert.equal(demo.projects().length, before - 1);
  assert.ok(!demo.projects().some((p) => p.id === target.id));
  assert.ok(events.filter((e) => e.type === 'projectsChanged').length >= 3);
});

test('⑤ 当前范围的项目被移除后侧栏退回「未分组」（源码钉子 + 边界注释）', () => {
  const src = readFileSync(new URL('../src/components/sidebar/Sidebar.tsx', import.meta.url), 'utf8');
  assert.match(src, /if \(filter === LOOSE \|\| filter === ""\) return;/, '未分组不处理');
  assert.match(src, /if \(projects\.length === 0\) return;/, '列表还没回来时不误判成「项目被删」');
  assert.match(src, /if \(projects\.some\(\(p\) => p\.id === filter\)\) return;\s*\n\s*setFilter\(LOOSE\);/, '项目不在列表里 → 退回未分组');
});
