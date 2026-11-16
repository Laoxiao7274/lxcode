// CodeSeg 复制按钮的组件级断言：
// 1. 代码围栏渲染出右上角复制按钮（aria 可见，非 hover 显隐由 CSS 承担，HTML 里恒在）；
// 2. markdown.tsx 的复制写法与 TerminalCard 同款（clipboard 可选链 + 已复制反馈 +
//    stopPropagation），防止退化成同步赋值或丢反馈的实现。
//
// 测试环境没有 DOM 事件系统（react-dom/server 静态渲染），点击→写入→反馈的完整
// 交互链在浏览器里人工验收；这里钉结构 + 写法一致性。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Markdown } from '../src/shared/markdown.tsx';

test('Markdown 代码围栏渲染复制按钮（位置与 aria 可见）', () => {
  const html = renderToStaticMarkup(createElement(Markdown, { text: '```js\nconst a = 1;\nconst b = 2;\n```' }));
  assert.ok(html.includes('md-copy'), '必须有 .md-copy 按钮: ' + html);
  assert.ok(html.includes('复制代码'), '必须有 aria-label 复制代码: ' + html);
  assert.ok(html.includes('>复制<'), '按钮默认文案是「复制」: ' + html);
  assert.ok(html.includes('md-codeseg'), 'pre 带 md-codeseg 类（hover 显隐选择器）: ' + html);
  // 去语言行：首行 `js` 不应出现在代码文本里
  assert.ok(!html.includes('>&#x3C;span>js'), '语言行已被剥离（渲染为空）: ' + html);
});

test('纯文本不渲染复制按钮（TextBlocks 路径不受影响）', () => {
  const html = renderToStaticMarkup(createElement(Markdown, { text: '普通段落，无围栏' }));
  assert.ok(!html.includes('md-copy'), '非代码围栏不该出现复制按钮: ' + html);
});

test('markdown.tsx 的复制逻辑与 TerminalCard 同款（可选链 + 反馈 + stopPropagation）', () => {
  const src = readFileSync(new URL('../src/shared/markdown.tsx', import.meta.url), 'utf8');
  assert.ok(src.includes('navigator.clipboard?.writeText'), '必须走 clipboard 可选链');
  assert.ok(src.includes('setCopied(true)'), '必须有已复制反馈');
  assert.ok(src.includes('已复制'), '反馈文案必须是「已复制」');
  assert.ok(src.includes('e.stopPropagation()'), '按钮必须 stopPropagation（不破坏外层 memo）');
  assert.ok(src.includes('window.setTimeout'), '反馈必须带超时复位');
});
