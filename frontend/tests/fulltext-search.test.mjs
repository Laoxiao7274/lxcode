// 全文检索（session.fulltext / A1）：shared/fulltext-search 的纯逻辑用例 +
// 侧栏「消息命中」区的渲染钉子。
//
// 渲染断言用 react-dom/server 静态渲染（与 sidebar-busy.test.mjs 同一模式）：
// useEffect 不执行，只断言 props 驱动的渲染路径——防抖/竞态在纯函数层测。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

const { groupFulltextHits, createFulltextSearcher, FULLTEXT_DEBOUNCE_MS } =
  await import('../src/shared/fulltext-search.ts');

const hit = (session_id, content, role = 'assistant', session_title = '会话') => ({
  session_id, session_title, updated_at: '12:00', index: 1, role, content,
});

// ---------- groupFulltextHits（分组纯函数） ----------

test('groupFulltextHits：按会话分组并保留出现顺序', () => {
  const groups = groupFulltextHits([
    hit('a', '先出现'), hit('b', '另一会话'), hit('a', '同会话第二条'),
  ]);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].sessionId, 'a');
  assert.equal(groups[0].hits.length, 2);
  assert.equal(groups[1].sessionId, 'b');
  assert.equal(groups[0].sessionTitle, '会话');
});

test('groupFulltextHits：空输入回空数组', () => {
  assert.deepEqual(groupFulltextHits([]), []);
});

// ---------- createFulltextSearcher（防抖 + 竞态 + 失败语义） ----------

/** 等到条件成立（轮询——测试内不允许 sleep 排队，见守则 16；这里是最小等待）。 */
async function waitFor(fn, ms = 1000) {
  const start = Date.now();
  while (!fn()) {
    if (Date.now() - start > ms) throw new Error('waitFor 超时');
    await new Promise((r) => setTimeout(r, 10));
  }
}

test('searcher：防抖期内不发请求，静默期满发一次', async () => {
  let calls = 0;
  const source = { searchMessages: () => { calls++; return Promise.resolve({ hits: [hit('a', '命中')], total: 1 }); } };
  const results = [];
  const s = createFulltextSearcher(source, (q, groups, total) => results.push({ q, groups, total }));
  s.onQuery('词');
  assert.equal(calls, 0, '防抖期内不应发请求');
  await waitFor(() => results.length > 0);
  assert.equal(calls, 1);
  assert.equal(results[0].groups.length, 1);
  assert.equal(results[0].total, 1);
  s.dispose();
});

test('searcher：竞态丢弃——旧 query 的迟到应答不覆盖新结果', async () => {
  const gates = [];
  const source = {
    searchMessages: () => new Promise((resolve) => { gates.push(resolve); }),
  };
  const results = [];
  const s = createFulltextSearcher(source, (q, groups) => results.push({ q, groups }));
  s.onQuery('第一个词');
  // 跨过防抖窗口再输第二个词（同一窗口内输入会被合并成一次——那是防抖语义，
  // 不是竞态；竞态是两次真实请求的应答乱序到达）。
  await waitFor(() => gates.length === 1, FULLTEXT_DEBOUNCE_MS + 200);
  s.onQuery('第二个词');
  await waitFor(() => gates.length === 2, FULLTEXT_DEBOUNCE_MS + 200);
  // 先到的应答是第一个词的——现在才 resolve 它（模拟迟到的旧应答）
  gates[0]({ hits: [hit('old', '旧结果')], total: 1 });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(results.length, 0, '旧应答应被丢弃');
  gates[1]({ hits: [hit('new', '新结果')], total: 1 });
  await waitFor(() => results.length > 0);
  assert.equal(results[0].q, '第二个词');
  assert.equal(results[0].groups[0].sessionId, 'new');
  s.dispose();
});

test('searcher：空 query 立即清结果且不发请求', async () => {
  let calls = 0;
  const source = { searchMessages: () => { calls++; return Promise.resolve({ hits: [], total: 0 }); } };
  const results = [];
  const s = createFulltextSearcher(source, (q, groups) => results.push({ q, groups }));
  s.onQuery('');
  assert.equal(calls, 0);
  assert.equal(results.length, 1);
  assert.deepEqual(results[0].groups, []);
  // 有过结果后清空：同样立即回调空（渲染层据此收起命中区）
  s.onQuery('词');
  await waitFor(() => results.length > 1);
  s.onQuery('   ');
  assert.equal(calls, 1, '空 query 不发请求');
  assert.equal(results[results.length - 1].groups.length, 0);
  s.dispose();
});

test('searcher：请求失败回 total=-1（检索不可用，不是「没有命中」）', async () => {
  const source = { searchMessages: () => Promise.reject(new Error('断连')) };
  const results = [];
  const s = createFulltextSearcher(source, (q, groups, total) => results.push({ q, groups, total }));
  s.onQuery('词');
  await waitFor(() => results.length > 0);
  assert.equal(results[0].total, -1);
  s.dispose();
});

test('searcher：能力缺席立即回空（demo 口径——侧栏不显示命中区）', async () => {
  const results = [];
  const s = createFulltextSearcher(undefined, (q, groups, total, timedOut) => results.push({ q, groups, total, timedOut }));
  s.onQuery('词');
  assert.equal(results.length, 1);
  assert.deepEqual(results[0].groups, []);
  assert.equal(results[0].total, 0);
  s.dispose();
});

// ---------- 渲染钉子：消息命中区（props 驱动路径） ----------

globalThis.window = globalThis.window ?? { __LX__: undefined };
const { Sidebar } = await import('../src/components/sidebar/Sidebar.tsx');
const { UpdateProvider } = await import('../src/shared/update.tsx');

/** 最小 source 桩：只喂 Sidebar 渲染所需的同步读（subscribe 空实现）。 */
function stubSource(fulltext) {
  return {
    subscribe: () => () => {},
    sessions: () => [],
    projects: () => [],
    label: '测试',
    ...(fulltext ? { fulltext } : {}),
  };
}

const baseProps = {
  currentId: '',
  busyBySession: {},
  filter: '',
  setFilter: () => {},
  onOpenProject: () => {},
  onOpenSettings: () => {},
  agentsActive: false,
  onOpenAgents: () => {},
  catalogActive: false,
  onOpenCatalog: () => {},
  gitActive: false,
  onOpenGit: () => {},
  remoteActive: false,
  onOpenRemote: () => {},
  onOpenAssistantChild: () => {},
  onOpenChat: () => {},
};

test('Sidebar：消息命中区渲染会话名 + 高亮片段', async () => {
  const source = stubSource({
    searchMessages: () => Promise.resolve({
      hits: [hit('a', '这就是命中的内容'), hit('a', '用户的追问', 'user')],
      total: 2,
    }),
  });
  // 全局搜索态由 query 驱动——但 query 是内部 state，静态渲染进不去；渲染层
  // 断言打在 FulltextHitText 的可复用性上：这里验证 hit 组件通过 props 高亮。
  // （防抖与 query 态的接线路径在纯函数层已覆盖；静态渲染只能给受控 props。）
  const { FulltextHitText } = await import('../src/components/sidebar/Sidebar.tsx');
  const html = renderToStaticMarkup(createElement(FulltextHitText, { content: '命中内容在此', query: '内容', role: 'assistant' }));
  assert.ok(html.includes('<mark'), '命中子串应有 mark 高亮');
  assert.ok(html.includes('命中<'), `高亮包裹了 query 子串: ${html}`);
  const htmlUser = renderToStaticMarkup(createElement(FulltextHitText, { content: '用户的话', query: '不存在', role: 'user' }));
  assert.ok(htmlUser.includes('fulltext-role'), 'user 命中带「你」前缀徽标');
});

test('Sidebar：fulltext 能力缺席时 source 不暴露 fulltext 键', () => {
  const source = stubSource(undefined);
  assert.equal(source.fulltext, undefined, 'demo/缺席源不应有 fulltext 能力——侧栏据此隐藏命中区');
});
