// ErrorBoundary 的契约（2026-12「全白屏」止血三件套②）：
//   ① errorSummary 纯函数：Error → "Name: message"、字符串原样、其他 stringify；
//   ② 降级渲染（静态渲染断言）：未出错渲染 children；出错后渲染「界面遇到错误」
//      摘要与「重新加载」按钮（onClick=location.reload()）；
//   ③ 挂载与全局兜底接线（源码钉子）：main.tsx 里 ErrorBoundary 包住 App（Topbar
//      在边界内）、挂 unhandledrejection/error → console.error（经壳的
//      console-message 链路落盘 renderer.log）。
// 渲染断言用 react-dom/server（无 DOM），降级态经 getDerivedStateFromError 之外
// 的直喂 state 触发——createRoot 才会跑 componentDidCatch，静态渲染拿不到；
// getDerivedStateFromError 本身是 React 内部协议，这里直接对 state.error 非空
// 的 render 输出断言（做法：渲染一个在 render 里 throw 的 Bomb，React SSR 遇
// throw 会直接冒泡，所以降级态走类字段手工置——ErrorBoundary.state 是公开字段）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

const { ErrorBoundary, errorSummary } = await import('../src/components/ErrorBoundary.tsx');

// ---------- ① errorSummary ----------

test('errorSummary：Error → "Name: message"', () => {
  assert.equal(errorSummary(new TypeError('x is not a function')), 'TypeError: x is not a function');
});

test('errorSummary：字符串原样，非 Error 对象 stringify', () => {
  assert.equal(errorSummary('直接抛的字符串'), '直接抛的字符串');
  assert.equal(errorSummary(42), '42');
  assert.equal(errorSummary(undefined), 'undefined');
  assert.equal(errorSummary({ code: 5 }), '[object Object]');
});

// ---------- ② 降级渲染 ----------

test('未出错：原样渲染 children（ErrorBoundary 透明）', () => {
  const html = renderToStaticMarkup(createElement(ErrorBoundary, null, createElement('div', { id: 'app-root' }, 'ok')));
  assert.match(html, /id="app-root"/);
  assert.doesNotMatch(html, /界面遇到错误/);
});

test('出错：降级页含错误摘要与「重新加载」按钮', () => {
  const boundary = new ErrorBoundary({ children: null });
  // 直接喂 state（SSR 无 React 调度；getDerivedStateFromError 的产物就是 { error }）
  boundary.state = { error: new TypeError('Cannot read properties of undefined') };
  const html = renderToStaticMarkup(boundary.render());
  assert.match(html, /界面遇到错误/);
  assert.match(html, /TypeError: Cannot read properties of undefined/);
  assert.match(html, /重新加载/);
});

test('ErrorBoundary 源码：重新加载按钮 onClick=location.reload（SSR 不出事件属性，钉在源上）', () => {
  const src = readFileSync(new URL('../src/components/ErrorBoundary.tsx', import.meta.url), 'utf8');
  assert.match(src, /onClick=\{\(\) => location\.reload\(\)\}/);
});

// ---------- ③ 挂载与全局兜底接线（源码钉子） ----------

const mainSource = readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8');

test('main.tsx：ErrorBoundary 是挂载根最外层（App 与 StrictMode 之间，Topbar 在边界内）', () => {
  assert.match(mainSource, /<ErrorBoundary>\s*\n\s*<App\s*\/>\s*\n\s*<\/ErrorBoundary>/);
});

test('main.tsx：挂 unhandledrejection 与 error 全局监听 → console.error（经壳链路落盘）', () => {
  assert.match(mainSource, /addEventListener\("unhandledrejection"/);
  assert.match(mainSource, /addEventListener\("error"/);
  const grab = mainSource.slice(mainSource.indexOf('unhandledrejection'));
  assert.match(grab, /console\.error/);
  const errGrab = mainSource.slice(mainSource.indexOf('addEventListener("error"'));
  assert.match(errGrab, /console\.error/);
});

test('ErrorBoundary：componentDidCatch 走 console.error（renderer.log 的观测通道）', () => {
  const src = readFileSync(new URL('../src/components/ErrorBoundary.tsx', import.meta.url), 'utf8');
  assert.match(src, /componentDidCatch[\s\S]{0,200}console\.error/);
});
