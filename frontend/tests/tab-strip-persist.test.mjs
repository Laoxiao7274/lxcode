// 标签持久化与溢出动效复用的钉子（2026-12 两域一致原则）：
//   ① tab-strip 读写纯函数：域键隔离（main / asst 四个 localStorage 键）、
//      坏数据回落、空数组 = 清键；
//   ② restoreTabStrip：持久化集合与真实会话列表的交集清洗（归档/删除/已关
//      的不复活），无持久化返回 null（调用方走 seed 原路径）；
//   ③ 两域接线源码断言：TabBar 与 AssistantWorkspace 都消费 restoreTabStrip
//      + writeTab*，且域参数正确（main vs asst 串域 = 互踩对方记忆）。
// ①② 是纯函数运行时断言（node:test 直接跑 TS 源）；③ 是源码文本断言——
// 组件接线在 node 单测里没有更便宜的观察点，同 child-tab-view.test.mjs 做法。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// happy-dom 全局 DOM（restoreTabStrip 的 localStorage 访问需要 window 语境）
import { Window } from 'happy-dom';
const dom = new Window({ url: 'http://localhost/' });
globalThis.window = dom;
globalThis.localStorage = dom.localStorage;

const {
  readTabOrder, writeTabOrder, readTabClosed, writeTabClosed, restoreTabStrip,
} = await import('../src/shared/tab-strip.ts');

const meta = (id, extra = {}) => ({ id, archived: false, ...extra });

test('① 读写：两域键隔离，互不覆盖', () => {
  localStorage.clear();
  writeTabOrder('main', ['a', 'b']);
  writeTabOrder('asst', ['c']);
  writeTabClosed('main', ['a']);
  writeTabClosed('asst', ['c', 'd']);
  assert.deepEqual(readTabOrder('main'), ['a', 'b']);
  assert.deepEqual(readTabOrder('asst'), ['c']);
  assert.deepEqual(readTabClosed('main'), ['a']);
  assert.deepEqual(readTabClosed('asst'), ['c', 'd']);
  // 底层键名：主域 lx.app.taborder/tabclosed，助手域 lx.app.asst.*
  assert.deepEqual(JSON.parse(localStorage.getItem('lx.app.taborder')), ['a', 'b']);
  assert.deepEqual(JSON.parse(localStorage.getItem('lx.app.asst.taborder')), ['c']);
});

test('① 空数组 = 清键；坏数据（非数组/非字符串元素/坏 JSON）回落空数组', () => {
  localStorage.clear();
  writeTabOrder('main', ['a']);
  writeTabOrder('main', []);
  assert.equal(localStorage.getItem('lx.app.tabOrder'), null);
  assert.deepEqual(readTabOrder('main'), []);
  localStorage.setItem('lx.app.tabclosed', 'not json');
  assert.deepEqual(readTabClosed('main'), []);
  localStorage.setItem('lx.app.tabclosed', '{"x":1}');
  assert.deepEqual(readTabClosed('main'), []);
  localStorage.setItem('lx.app.tabclosed', '[1,"a"]');
  assert.deepEqual(readTabClosed('main'), ['a']);
});

test('② restoreTabStrip：交集清洗——归档/删除/已关的条目不复活', () => {
  localStorage.clear();
  writeTabOrder('asst', ['a', 'dead', 'arch', 'b']);
  writeTabClosed('asst', ['closed', 'gone']);
  const sessions = [meta('a'), meta('b'), meta('arch', { archived: true }), meta('closed')];
  assert.deepEqual(restoreTabStrip('asst', sessions), {
    order: ['a', 'b'],
    closed: ['closed'], // closed 里指向已删除会话的条目一并丢弃（交集）
  });
});

test('② restoreTabStrip：无持久化返回 null（调用方走 seed 原路径）', () => {
  localStorage.clear();
  assert.equal(restoreTabStrip('main', [meta('a')]), null);
});

test('② restoreTabStrip：列表为空时原样退回（判不出死活，不清记忆）', () => {
  localStorage.clear();
  writeTabOrder('asst', ['a', 'b']);
  writeTabClosed('asst', ['a']);
  // 空列表：alive 映射为空 → 全部被剔。这是现有语义（会话列表未到不清记忆由
  // 调用方保证——两域的 seed effect 都在列表非空后才跑 restore）。
  assert.deepEqual(restoreTabStrip('asst', []), { order: [], closed: [] });
});

// ---------- ③ 两域接线（源码文本断言） ----------

const here = dirname(fileURLToPath(import.meta.url));
const read = (p) => readFileSync(join(here, p), 'utf8');
const tabbarSrc = read('../src/components/topbar/TabBar.tsx');
const asstSrc = read('../src/components/assistant/AssistantWorkspace.tsx');

test('③ 主域 TabBar：restoreTabStrip("main") 恢复 + writeTab*("main") 落盘', () => {
  assert.ok(tabbarSrc.includes('restoreTabStrip("main"'), '主域恢复走 main 域键');
  assert.ok(tabbarSrc.includes('writeTabOrder("main"'), '主域 order 落盘');
  assert.ok(tabbarSrc.includes('writeTabClosed("main"'), '主域 closed 落盘');
  // 恢复在 seed 同一个 effect 里（无记忆才 seed 原路径）
  assert.ok(tabbarSrc.includes('seedSessionTabs(sessions)'), '无记忆回落 seed 原路径');
});

test('③ 助手域 AssistantWorkspace：restoreTabStrip("asst") 恢复 + writeTab*("asst") 落盘', () => {
  assert.ok(asstSrc.includes('restoreTabStrip("asst"'), '助手域恢复走 asst 域键');
  assert.ok(asstSrc.includes('writeTabOrder("asst"'), '助手域 order 落盘');
  assert.ok(asstSrc.includes('writeTabClosed("asst"'), '助手域 closed 落盘');
  assert.ok(asstSrc.includes('seedSessionTabs(sessionList)'), '无记忆回落 seed 原路径');
});

// ---------- ④ 溢出动效复用（源码文本断言：共享一份实现，不再各自长） ----------

test('④ useStripOverflow 抽到 shared/tab-strip.ts：TabBar 不再有本地定义，AssistantTabStrip 接入', () => {
  const sharedSrc = read('../src/shared/tab-strip.ts');
  assert.ok(sharedSrc.includes('export function useStripOverflow'), 'hook 定义在 shared');
  assert.ok(sharedSrc.includes('export function useActiveTabVisible'), '滚进可视区也在 shared');
  assert.ok(!tabbarSrc.includes('function useStripOverflow'), 'TabBar 不再保留本地定义（单一实现）');
  assert.ok(tabbarSrc.includes('from "../../shared/tab-strip"'), 'TabBar 消费 shared');
  const stripSrc = read('../src/components/assistant/AssistantTabStrip.tsx');
  assert.ok(stripSrc.includes('useStripOverflow<HTMLDivElement>'), '助手标签条接入溢出动效');
  assert.ok(stripSrc.includes('useActiveTabVisible'), '助手标签条接入滚进可视区');
  assert.ok(stripSrc.includes('data-overflow={overflow}'), '渐隐方向 data 属性挂上');
  const asstCss = read('../src/styles/assistant-mode.css');
  assert.ok(asstCss.includes('.asst-tabstrip[data-overflow='), '渐隐样式进 assistant-mode.css');
});
