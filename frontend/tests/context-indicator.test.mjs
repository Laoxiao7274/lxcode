// ContextIndicator 的组件级断言：**估算值必须在界面上看得出来**。
//
// 纯函数（context-usage.ts）测的是口径，这里测的是**接线**——组件真的把那个口径渲染
// 出来了吗？只在纯函数里标了估算、组件却当成真实用量展示，用户就再也看不出区别了
//（这正是本条要防的回归）。
//
// 2026-09-30 用户拍板后的记号约定：**可见记号只有 `~`**（弹层读数与分类行，对齐 DSH 的
// ContextMeter），「这份数字是估的」走 title/aria-label（悬停与读屏能看到，静态渲染也在 HTML 里）。
//
// 用 react-dom/server 静态渲染（测试环境没有 DOM）：usePopover 里的 useEffect 在服务端
// 不执行，渲染路径只读 props。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { ContextIndicator, SessionUsage } from '../src/components/context-indicator/ContextIndicator.tsx';

const render = (usage) => renderToStaticMarkup(createElement(ContextIndicator, { usage }));

test('ContextIndicator：真实用量渲染成纯百分比，说明里不出现"估算"', () => {
  const html = render({ used: 4096, window: 8192 });
  assert.ok(html.includes('50%'), html);
  assert.ok(!html.includes('估算'), '真实用量不该出现"估算"字样: ' + html);
});

test('ContextIndicator：估算用量把区别放进 title（可见的百分比仍是纯数字）', () => {
  const html = render({ used: 4096, window: 8192, estimated: true });
  assert.ok(html.includes('50%'), html);
  // 「估」字已按用户决定去掉（DSH 没有这个记号）——但区别不能就此消失：
  // 它必须出现在 title/aria-label 里，否则用户看到的就是一个和真实用量一模一样的 50%
  assert.ok(!html.includes('50%估'), '可见百分比不该再挂「估」字: ' + html);
  assert.ok(html.includes('估算值'), 'title 里必须明说这是估算值: ' + html);
  assert.ok(html.includes('不是真实用量'), 'title 里必须明说不是真实用量: ' + html);
});

test('ContextIndicator：未知占用渲染中性态「—」', () => {
  const html = render(null);
  assert.ok(html.includes('—'), html);
  assert.ok(!html.includes('%'), '未知时不该出现任何百分比: ' + html);
});

test('ContextIndicator：弹层读数只在点击后渲染（静态渲染不该崩）', () => {
  // 弹层里的分类行与「~used / window」都要点击才开——静态渲染看不到它们，
  // 那两条口径由 shared/context-usage.ts 的纯函数钉住（context-usage.test.mjs）。
  // 这里只确认组件在带完整分类（含 tools）时渲染路径不炸。
  const html = render({
    used: 4096, window: 8192, system: 100, tools: 200, tool_results: 1000, messages: 2696, reasoning: 100,
  });
  assert.ok(html.includes('50%'), html);
});

test('SessionUsage：并进「会话用量」弹层的累计消耗一节（标题 + 总量 + 明细行）', () => {
  // 这一节单独渲染（弹层在静态渲染下是关着的，抽出来才测得到接线）：
  // 2026-09-30 用户拍板把「会话消耗」胶囊并进这里——标题「会话消耗」+ 总量
  // 「12.5k tok」+ 明细行（缓存命中/未缓存输入/缓存读取/缓存写入/输出）。
  const html = renderToStaticMarkup(createElement(SessionUsage, { stats: {
    turns: 2, steps: 3, llm_ms: 6000, tool_ms: 2000,
    ttft_ms: 900, ttft_steps: 2, decode_ms: 4100, decode_tokens: 300,
    input_tokens: 1050, cache_read_tokens: 11000, cache_write_tokens: 100, output_tokens: 320,
  } }));
  assert.ok(html.includes('会话消耗'), html);
  assert.ok(html.includes('12.5k tok'), html);
  assert.ok(html.includes('缓存命中'), html);
  assert.ok(html.includes('91%'), html);
});

test('SessionUsage：没有 token（老会话）整节不渲染（不显示一排 0）', () => {
  const empty = {
    turns: 1, steps: 1, llm_ms: 0, tool_ms: 0,
    ttft_ms: 0, ttft_steps: 0, decode_ms: 0, decode_tokens: 0,
    input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0,
  };
  assert.equal(renderToStaticMarkup(createElement(SessionUsage, { stats: empty })), '');
  assert.equal(renderToStaticMarkup(createElement(SessionUsage, { stats: null })), '');
});
