// ContextIndicator 的组件级断言：**估算值必须在界面上看得出来**。
//
// 纯函数（context-usage.ts）测的是口径，这里测的是**接线**——组件真的把那个口径渲染
// 出来了吗？只在纯函数里标「估」而组件忽略它，用户看到的仍然是一个和真实用量一模一样
// 的百分比（这正是本条要防的回归）。
//
// 用 react-dom/server 静态渲染（测试环境没有 DOM）：usePopover 里的 useEffect 在服务端
// 不执行，渲染路径只读 props。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { ContextIndicator } from '../src/components/context-indicator/ContextIndicator.tsx';

const render = (usage) => renderToStaticMarkup(createElement(ContextIndicator, { usage }));

test('ContextIndicator：真实用量渲染成纯百分比', () => {
  const html = render({ used: 4096, window: 8192 });
  assert.ok(html.includes('50%'), html);
  assert.ok(!html.includes('50%估'), '真实用量不该带「估」标记');
});

test('ContextIndicator：估算用量渲染出「估」标记与说明（不许当真实用量展示）', () => {
  const html = render({ used: 4096, window: 8192, estimated: true });
  assert.ok(html.includes('50%估'), '估算值的百分比必须带「估」: ' + html);
  assert.ok(html.includes('估算值'), '必须有一句明说这是估算值的说明: ' + html);
});

test('ContextIndicator：未知占用渲染中性态「—」', () => {
  const html = render(null);
  assert.ok(html.includes('—'), html);
  assert.ok(!html.includes('%'), '未知时不该出现任何百分比: ' + html);
});
