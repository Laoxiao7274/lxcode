// StatsPills 的组件级断言：**没有一步就整行不渲染**、时间胶囊的标题真的把口径渲染出来了。
//
// 纯函数（session-stats.ts）测的是口径，这里测的是**接线**——组件真的用了那份口径吗？
// 只在纯函数里做对而组件自己拼字符串，用户看到的仍然是另一个数（这正是本条要防的回归）。
//
// 2026-09-30 用户拍板：累计消耗那一半并进了上下文环的「会话用量」弹层，所以这一行
// 只剩时间胶囊（那一节的接线由 context-indicator.test.mjs 的 SessionUsage 断言钉住）。
//
// 用 react-dom/server 静态渲染（测试环境没有 DOM）：usePopover 里的 useEffect 在服务端
// 不执行，渲染路径只读 props（弹层要点击才开，静态渲染看不到它）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { StatsPills } from '../src/components/stats-pills/StatsPills.tsx';
import { timeDialogRows } from '../src/shared/session-stats.ts';
import { Composer } from '../src/components/composer/Composer.tsx';
import { AgentsProvider } from '../src/shared/agents.tsx';
import { SettingsProvider } from '../src/shared/settings.tsx';

const stats = (over = {}) => ({
  turns: 2, steps: 3, llm_ms: 6000, tool_ms: 2000,
  ttft_ms: 900, ttft_steps: 2, decode_ms: 4100, decode_tokens: 300,
  input_tokens: 1050, cache_read_tokens: 11000, cache_write_tokens: 100, output_tokens: 320,
  ...over,
});

const render = (value) => renderToStaticMarkup(createElement(StatsPills, { stats: value }));

test('StatsPills：null（还没有任何一步）整行不渲染', () => {
  assert.equal(render(null), '');
  assert.equal(render(undefined), '');
  assert.equal(render(stats({ steps: 0, turns: 0 })), '');
});

test('StatsPills：只有 token 没有步数 → 整行也不渲染（不留一个空胶囊）', () => {
  // 用量那一半已经并进「会话用量」弹层，这一行只剩时间胶囊——没有步数就没有可说的数字
  assert.equal(render(stats({ steps: 0, turns: 0 })), '');
});

test('StatsPills：时间胶囊渲染出轮/步/速度', () => {
  const html = render(stats());
  assert.ok(html.includes('2 轮'), html);
  assert.ok(html.includes('3 步'), html);
  assert.ok(html.includes('tok/s'), html);
  // 有按钮才有弹层入口（aria-haspopup 是"点开有东西"的语义）
  assert.ok(html.includes('aria-haspopup="dialog"'), html);
  // 累计消耗不再出现在输入条里（并进了上下文环的弹层）
  assert.ok(!html.includes('缓存命中'), '用量那一半不该再占输入条的位置: ' + html);
});

test('时间胶囊弹层：没有可测计时时不留空面板（如实说明为什么空）', () => {
  // 老会话：有步数、没有 duration_ms/首字/输出 → 明细一行都排不出来。
  // 空面板看起来像"加载失败"，所以纯函数给出空数组、组件给一句说明。
  const s = stats({
    steps: 1, turns: 1,
    llm_ms: 0, tool_ms: 0, ttft_ms: 0, ttft_steps: 0, decode_ms: 0, decode_tokens: 0,
    input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0,
  });
  assert.deepEqual(timeDialogRows(s), []);
});

test('Composer：统计胶囊挂在输入框那一行、**紧挨上下文环**（不是另起一行）', () => {
  // 位置是接线的一部分（2026-09-30 用户拍板）：胶囊在 .piBar 里、且在上下文环
  // （.ctx-wrap）之后——两者都是"这条会话花了多少"的读数，放一起看。
  // 渲染真的 Composer（不是文本比对源码）：Composer 需要 AgentsProvider 上下文，
  // 静态渲染下 provider 只读 source.subscribe/agentAdmin（useEffect 不执行），
  // 所以一个最小桩就够。
  const source = { subscribe: () => () => {} };
  const html = renderToStaticMarkup(
    createElement(AgentsProvider, { source },
      createElement(SettingsProvider, { source },
        createElement(Composer, { busy: false, stats: stats(), onSend: () => {}, onCancel: () => {} }))));
  const pillsAt = html.indexOf('class="stats-pills"');
  const ctxAt = html.indexOf('class="ctx-wrap"');
  const piBarAt = html.indexOf('class="piBar"');
  const piAt = html.indexOf('class="pi"');
  assert.ok(pillsAt >= 0, '应渲染统计胶囊: ' + html);
  assert.ok(piBarAt >= 0, '输入条应有 piBar');
  assert.ok(pillsAt > piBarAt, '胶囊应在 .piBar 里（不再另起一行挂输入框上方）');
  assert.ok(ctxAt >= 0 && pillsAt > ctxAt, '胶囊应紧挨上下文环（在 .ctx-wrap 之后）');
  assert.ok(piAt >= 0 && pillsAt > piAt, '胶囊在 .pi 之内');
});
