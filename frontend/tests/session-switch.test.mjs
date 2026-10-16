// 会话切换流畅度的结构性钉子（渲染无法在 node 单测里跑，接线用源码文本钉住——
// 同 child-tab-view / theme 的做法）：
//
// ① 防闪烁：焦点已切、历史未装载（historyReady=false 且 blocks 空）的那一帧，
//    聊天面板必须带 data-switching 标记——外层据此渲染装载指示，而不是让 Thread
//    画空态（EmptyState「我们做点什么？」）。空态闪现 = 用户报的「切换瞬间闪两下」。
// ② 切回已装载会话秒回：WSAgent.loadedSessions 记忆 + resumeSession 短路（行为
//    断言在 ws.test.mjs，这里钉接线：装载成功要记、清账要清、命中要短路）。
// ③ 切换帧 sticky 置真：换会话那帧 Thread 显式把跟随置真（新会话从底部看起），
//    上翻过再切换不会停在顶部。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const appSrc = readFileSync(join(here, '../src/App.tsx'), 'utf8');
const wsSrc = readFileSync(join(here, '../src/agent/ws/index.ts'), 'utf8');
const threadSrc = readFileSync(join(here, '../src/components/thread/Thread.tsx'), 'utf8');
const threadCss = readFileSync(join(here, '../src/styles/thread.css'), 'utf8');
const composerCss = readFileSync(join(here, '../src/styles/composer.css'), 'utf8');

test('① 切换装载期不渲染空时间线：App 有 switching 判定与 data-switching 接线', () => {
  // 判定存在：焦点切换时按 historyReady + 空块判定装载中
  assert.match(appSrc, /setSwitching\(!state\.historyReady && state\.blocks\.length === 0\)/,
    'switching 判定必须同时要求「历史未装载」与「块为空」');
  // 装载完成要收尾（不因竞态把指示条留挂在已就绪的会话上）
  assert.match(appSrc, /if \(switching && state\.historyReady\) setSwitching\(false\)/,
    'historyReady 到达必须清掉 switching');
  // 接线存在：thread-scroll 上真的落了 data-switching
  assert.match(appSrc, /data-switching=\{switching/,
    'switching 必须接到 .thread-scroll 的 data 属性上');
  // CSS 有对应的装载指示（否则 data 属性是死接线）
  assert.match(threadCss, /thread-scroll\[data-switching="true"\]/,
    'thread.css 必须有 data-switching 的装载指示样式');
});

test('② 切回已装载会话秒回：loadedSessions 记忆/清账/短路三处接线齐全', () => {
  assert.match(wsSrc, /loadedSessions\.add\(sessionId\)/, '装载成功要记入缓存');
  assert.match(wsSrc, /loadedSessions\.clear\(\)/, '换后端（onHelloReset）要清缓存');
  assert.match(wsSrc, /this\.loadedSessions\.has\(id\) && this\.conn\.sessionFocus !== id/,
    'resumeSession 命中缓存且焦点未落时才短路（避免重复发 sessionFocused）');
  assert.match(wsSrc, /if \(this\.loadedSessions\.has\(id\)[\s\S]{0,80}this\.focusSession\(id\);\s*return;/,
    '命中必须直接 focusSession 返回，不发 resume/history');
});

test('③ 换会话帧 sticky 置真：Thread 显式重置跟随（新会话从底部看起）', () => {
  assert.match(threadSrc, /prevIdRef\.current !== state\.currentId[\s\S]{0,200}stickyRef\.current = true/,
    '换会话帧必须把 sticky 置真——否则上翻后切换会停在新会话顶部');
});

test('④ 切换瞬间读数跳动缓解：ctx-pct 钉 tabular-nums（数字位数变化不抖宽度）', () => {
  const pctBlock = composerCss.match(/\.ctx-pct \{[^}]*\}/);
  assert.ok(pctBlock, 'ctx-pct 样式块存在');
  assert.match(pctBlock[0], /font-variant-numeric:\s*tabular-nums/);
});
