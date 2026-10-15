// Git 管理页的数据层契约：wire（git.overview）→ UI 类型的映射纯函数、diff 统计、
// WSAgent 的协议方法（git.overview / git.diff）、演示模式的假数据与页面渲染冒烟。
//
// 旧的演示数据合成测试（createGitDemoState / simulateCommit / toggleStaged /
// selectBranch）已随演示原型一起删除——页面现在展示后端只读查询的真实结果。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { gitOverviewToState, countDiffLines, shortHash, relativeWhen, kindGlyph } from '../src/components/git/git-model.ts';
import { GitWorkbenchPage } from '../src/components/git/GitWorkbenchPage.tsx';
import { WSAgent } from '../src/agent/ws/index.ts';
import { DemoAgent } from '../src/agent/demo/index.ts';

// ---------- wire → UI 映射（纯函数） ----------

const OVERVIEW = {
  path: 'C:/work/demo',
  branch: 'main',
  dirty: [
    { path: 'a.go', kind: 'modified' },
    { path: 'b.md', kind: 'untracked' },
    { path: 'c.txt', kind: 'added' },
    { path: 'd.txt', kind: 'deleted' },
    { path: 'e.txt', kind: 'mystery' }, // 未知类别 → modified 兜底，不丢条目
  ],
  branches: [
    { name: 'main', current: true, ahead: 0, behind: 0 },
    { name: 'lxcode/session-s1', current: false, ahead: 2, behind: 0, session_id: 's1', session_title: '任务一', has_worktree: true, worktree_path: 'C:/wt/s1', dirty_count: 3 },
    { name: 'lxcode/session-s2', current: false, ahead: 0, behind: 0, session_id: 's2', session_title: '干净会话', has_worktree: true, worktree_path: 'C:/wt/s2', dirty_count: 0 },
    { name: 'lxcode/session-s3', current: false, ahead: 1, behind: 0, session_id: 's3', session_title: '已释放', archived: true, merged: true },
  ],
  commits: [{ hash: 'abcdef1234567890', message: '基线提交', author: 'tester', when: '2026-10-09T10:00:00Z' }],
};

test('gitOverviewToState：变更清单按类别映射，未知类别兜底为 modified', () => {
  const state = gitOverviewToState(OVERVIEW);
  assert.deepEqual(state.changes.map((c) => c.kind), ['modified', 'untracked', 'added', 'deleted', 'modified']);
  assert.deepEqual(state.changes.map((c) => c.path), ['a.go', 'b.md', 'c.txt', 'd.txt', 'e.txt']);
});

test('gitOverviewToState：工作树卡从会话分支派生（changes/clean/released 三态）', () => {
  const state = gitOverviewToState(OVERVIEW);
  assert.equal(state.worktrees.length, 3, '主检出分支不产生工作树卡');
  const bySession = Object.fromEntries(state.worktrees.map((wt) => [wt.sessionId, wt]));
  assert.equal(bySession.s1.state, 'changes', 'dirtyCount>0 → changes');
  assert.equal(bySession.s1.changedFiles, 3);
  assert.equal(bySession.s1.path, 'C:/wt/s1', '路径用 wire 里的真实路径');
  assert.equal(bySession.s2.state, 'clean', '有工作树且干净 → clean');
  assert.equal(bySession.s3.state, 'released', '无工作树 → released（分支保留）');
  assert.equal(bySession.s3.path, '');
  assert.equal(bySession.s3.archived, true, '归档标注透传');
});

test('gitOverviewToState：分支与提交字段透传', () => {
  const state = gitOverviewToState(OVERVIEW);
  assert.equal(state.branches.length, 4);
  assert.equal(state.branches[1].merged, undefined, '未并入的分支 merged 缺省');
  assert.equal(state.branches[3].merged, true, '已并入标记透传');
  assert.equal(state.branches[0].name, 'main');
  assert.equal(state.branches[0].current, true);
  assert.equal(state.branches[1].sessionId, 's1');
  assert.equal(state.commits[0].id, 'abcdef1234567890');
  assert.equal(state.commits[0].message, '基线提交');
});

test('countDiffLines：只数 +/- 行，文件头与 hunk 头不计', () => {
  const diff = [
    '+++ b/a.go',
    '--- a/a.go',
    '@@ -1,2 +1,3 @@',
    '+added line',
    '-removed line',
    ' context line',
    '+++（未跟踪文件，以下为全部内容）',
    '+untracked line',
  ].join('\n');
  assert.deepEqual(countDiffLines(diff), { additions: 2, deletions: 1 });
});

test('shortHash / relativeWhen / kindGlyph', () => {
  assert.equal(shortHash('abcdef1234567890'), 'abcdef1');
  assert.equal(shortHash('abc'), 'abc');
  assert.equal(kindGlyph('untracked'), 'U');
  assert.equal(kindGlyph('added'), 'A');
  assert.equal(kindGlyph('deleted'), 'D');
  assert.equal(kindGlyph('modified'), 'M');
  // 非法 ISO 原样返回，不抛
  assert.equal(relativeWhen('not-a-date'), 'not-a-date');
  assert.equal(relativeWhen(new Date().toISOString()), '刚刚');
});

// ---------- WSAgent：git.overview / git.diff 协议方法 ----------

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor() { FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result) { this.receive({ id: this.sent.at(-1).id, result }); }
}

function setup(t) {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const agent = new WSAgent('localhost:1234');
  const off = agent.subscribe(() => {});
  t.after(off);
  return { agent, ws: FakeSocket.latest };
}

test('gitOverview 走 git.overview：显式项目带 project_id，缺省不带键', async (t) => {
  const { agent, ws } = setup(t);
  const p = agent.gitOverview('proj-1');
  assert.equal(ws.sent.at(-1).method, 'git.overview');
  assert.deepEqual(ws.sent.at(-1).params, { project_id: 'proj-1' });
  ws.reply({ path: 'C:/work', branch: 'main', dirty: [], branches: [], commits: [] });
  const overview = await p;
  assert.equal(overview.branch, 'main');

  const p2 = agent.gitOverview();
  assert.equal(ws.sent.at(-1).params, undefined, '缺省 = 当前会话归属项目，params 整键缺席');
  ws.reply({ path: 'C:/work', branch: 'main', dirty: [], branches: [], commits: [] });
  await p2;
});

test('gitDiff 走 git.diff 并回读 diff 文本', async (t) => {
  const { agent, ws } = setup(t);
  const p = agent.gitDiff('proj-1', 'a.go');
  assert.equal(ws.sent.at(-1).method, 'git.diff');
  assert.deepEqual(ws.sent.at(-1).params, { project_id: 'proj-1', path: 'a.go' });
  ws.reply({ diff: '+hello\n' });
  assert.equal(await p, '+hello\n');
});

// ---------- DemoAgent：演示假数据（页面可演示，不假装真实） ----------

test('演示模式：gitOverview 返回简化假数据、gitDiff 返回演示 diff、releaseWorktree 不炸', async () => {
  const agent = new DemoAgent();
  const overview = await agent.gitOverview(agent.projects()[0].id);
  assert.equal(overview.branch, 'main');
  assert.ok(overview.dirty.length > 0);
  assert.ok(overview.commits.length > 0);
  assert.ok(overview.branches.some((b) => b.session_id), '演示里也要有会话分支（工作树卡可看）');
  const diff = await agent.gitOverview('nope').then(() => '', () => 'fallback'); // 未知项目回落第一个演示项目
  assert.equal(diff, '');
  assert.ok((await agent.gitDiff('proj-demo-lxcode', 'x.go')).includes('演示'));
  await agent.releaseWorktree('s1'); // 不抛即可
});

// ---------- 页面渲染冒烟（演示假数据源 + 真组件） ----------

function stubSource(overview) {
  return {
    subscribe: () => () => {},
    gitOverview: () => Promise.resolve(overview),
    gitDiff: () => Promise.resolve('+++ b/x\n+line\n'),
    releaseWorktree: () => Promise.resolve(),
    label: 'stub',
  };
}

test('页面渲染冒烟：加载态与数据态都不炸，只读说明出现', async () => {
  const props = {
    projects: [{ id: 'p1', name: 'Demo', path: 'C:/work/demo' }],
    projectId: 'p1',
    onProjectChange: () => {},
    onOpenSession: () => {},
  };
  // 数据未到（useEffect 在 SSR 不执行）→ 加载态
  const loading = renderToStaticMarkup(createElement(GitWorkbenchPage, { source: stubSource(OVERVIEW), ...props }));
  assert.ok(loading.includes('Git 管理'));
  assert.ok(loading.includes('只读视图'), '只读说明横幅');
});
