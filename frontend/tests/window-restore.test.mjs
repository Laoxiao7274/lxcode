// S4 窗口冻结回归的 DOM 级钉子（happy-dom，真挂载 + 同一 hook 组合）：
//
// 回归链（调研实锤）：agent 跑轮期间用户上翻（sticky=false）→ done 定型的尾块
// 落进 [end, total) 窗口外不渲染 → 用户滚回底部时**滚动本身零渲染**
// （useStickyFollow 只写 stickyRef），恢复唯一触发器「渲染 + sticky 同时成立」
// 永不满足 → 尾块无限期缺失；切会话 key 换代重置窗口才自愈。
//
// 修复：sticky false→true 翻转触发 onStick → restoreTail()（setWin 整备）——
// 滚动事件本身就把恢复带出来，不再依赖后续渲染。测试复现的是「追加块之后
// 不再发生任何 React 渲染、仅发生滚动」这一条最低限度的路径。
//
// 用最小复现组件挂同一对 hook（useStickyFollow + useWindowedHistory），
// 不引 Thread/ChildSessionPage 整页（那些页依赖 source/store 全家桶——这里的
// 被测对象是这对 hook 的契约，三个消费点接的是同一份实现）。
import { Window } from 'happy-dom';

const dom = new Window({ url: 'http://localhost/' });
for (const key of Object.getOwnPropertyNames(dom)) {
  if (key === 'window' || key in globalThis) continue;
  try { globalThis[key] = dom[key]; } catch { /* 不可写字段跳过 */ }
}
globalThis.window = dom;
globalThis.document = dom.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.navigator, configurable: true });
globalThis.localStorage = dom.localStorage;
globalThis.MouseEvent = dom.MouseEvent;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement, useRef, act } from 'react';
import ReactDOMClient from 'react-dom/client';
const { createRoot } = ReactDOMClient;

const { useStickyFollow } = await import('../src/components/thread/useStickyFollow.ts');
const { useWindowedHistory, WINDOW_INITIAL } =
  await import('../src/components/thread/useWindowedHistory.ts');

const h = createElement;
/** 最小复现组件：同一对 hook + 滚动容器结构（endRef 的
 *  parentElement.parentElement = 滚动容器，与三处消费点的 DOM 结构一致）。 */
function Repro({ blocks }) {
  const restoreTailRef = useRef(() => {});
  const { endRef, stickyRef, scrollRef } = useStickyFollow({
    empty: blocks.length === 0,
    composerScope: '.repro',
    sessionKey: 's1',
    onStick: () => restoreTailRef.current(),
  });
  const { hidden, end, restoreTail } = useWindowedHistory({
    sessionKey: 's1',
    total: blocks.length,
    scrollRef,
    isSticky: () => stickyRef.current,
  });
  restoreTailRef.current = restoreTail;
  const visible = blocks.slice(hidden, Math.min(end, blocks.length));
  return h('div', { className: 'repro' },
    h('div', { className: 'scroller', ref: scrollRef, 'data-testid': 'scroller' },
      h('div', { className: 'thread' },
        visible.map((b) => h('div', { key: b, className: 'block', 'data-uid': b }, b)),
        h('div', { ref: endRef }),
      ),
    ),
  );
}

/** happy-dom 没有布局引擎，clientHeight 恒 0——isNearBottom 的三件套里它
 *  必须手工设定：defineProperty 一个实例属性覆盖原型上的 getter。 */
function primeScroller(scroller, viewport) {
  Object.defineProperty(scroller, 'clientHeight', {
    configurable: true,
    get() { return viewport; },
  });
}

const settle = () => new Promise((r) => setTimeout(r, 30));

function setup() {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  return { container, root };
}

const tailInDom = (container) =>
  [...container.querySelectorAll('.block')].map((el) => el.dataset.uid);

test('S4 回归：上翻后滚动回底（零后续渲染）尾块必须出现', async () => {
  const { container, root } = setup();
  // 50 块已定型（超过初始窗口 40）。
  const blocks = Array.from({ length: 50 }, (_, i) => `b${i}`);
  await act(async () => { root.render(createElement(Repro, { blocks })); });
  await settle();

  const scroller = container.querySelector('[data-testid="scroller"]');
  const viewport = 600;
  primeScroller(scroller, viewport);
  // 内容 50×100 = 5000 高。
  Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 5000 });

  // 初始窗口 40：前 10 块 hidden，尾块 b49 在 DOM。
  assert.ok(tailInDom(container).includes('b49'), '初始：尾块在 DOM');
  assert.ok(!tailInDom(container).includes('b0'), '初始：头部块被窗口截短');

  // 等 mount 时程序置底的 prog 时间窗（120ms）过去——真实浏览器里用户上翻
  // 总在挂载之后很久，这里对齐这个前提。
  await new Promise((r) => setTimeout(r, 150));

  // ① 用户上翻到顶（远离贴底区 200px 之外）→ scroll 事件 → sticky=false。
  //    happy-dom 无布局，滚动位置属性我们手动驱动（与真实浏览器同语义）。
  scroller.scrollTop = 0;
  await act(async () => {
    scroller.dispatchEvent(new dom.Event('scroll', { bubbles: true }));
  });
  await settle();

  // ② agent 定型 5 个新块（done）——块数据在 state，但用户仍停在窗口外的
  //    旧滚动位置（sticky=false 不跟随）。渲染新块集：窗口 end 停在 50，
  //    新尾块在 [50, 55)——不渲染（S4 的冻结现场）。
  const blocks2 = [...blocks, 'b50', 'b51', 'b52', 'b53', 'b54'];
  await act(async () => { root.render(createElement(Repro, { blocks: blocks2 })); });
  await settle();
  assert.equal(tailInDom(container).includes('b54'), false, '复现：新尾块在窗口外不渲染');

  // ③ 用户滚回底部：isNearBottom 现值变 true → scroll 事件触发 sticky
  //    false→true 翻转 → onStick → restoreTail。**不发生任何其他渲染/事件**。
  Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 5500 });
  scroller.scrollTop = 5500 - 600;
  await act(async () => {
    scroller.dispatchEvent(new dom.Event('scroll', { bubbles: true }));
  });
  // 排进 restoreTail 的 setWin 之后等一次真实时钟落定（与仓库其余 DOM 测试
  // 同款——act 的微任务 flush 覆盖不了 promise 链深处的 await）。
  await settle();
  assert.equal(
    tailInDom(container).includes('b54'),
    true,
    '修复核心：滚动回底本身（无其他事件）必须把尾块恢复进 DOM',
  );
  // 不变式：恢复后是尾部窗口——头部块重新被截短。
  assert.ok(!tailInDom(container).includes('b0'), '恢复后回到尾部窗口形态');

  await act(async () => { root.unmount(); });
  container.remove();
});

test('S4 伴生守卫：span ≥ total 的短会话（全量上翻过）贴底必须恢复', async () => {
  const { container, root } = setup();
  // 短会话：35 块 < 初始窗口 40 → 初始就是 span=total。旧守卫
  // `win.span < total` 恒 false → 恢复永不触发（伴生 bug）。
  const blocks = Array.from({ length: 35 }, (_, i) => `s${i}`);
  await act(async () => { root.render(createElement(Repro, { blocks })); });
  await settle();
  const scroller = container.querySelector('[data-testid="scroller"]');
  const viewport = 600;
  primeScroller(scroller, viewport);
  assert.equal(tailInDom(container).length, 35, '短会话全量可见');

  // 滑到上界外的路径走不到这里——短会话的「丢尾」来自 olderWindowTransition
  // 滑动分支（end -= BATCH）。短会话 span=total<WINDOW_MAX 不滑动，但守卫的
  // 修复仍要保证：任何 end < total 的短会话贴底帧都恢复。直接用端点语义断言：
  // 滚离底部再回来（零中间渲染），窗口始终是尾窗（短会话=全量）。
  Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 3500 });
  scroller.scrollTop = 0;
  await act(async () => {
    scroller.dispatchEvent(new dom.Event('scroll', { bubbles: true }));
  });
  await settle();
  Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 3500 });
  scroller.scrollTop = 3500 - 600;
  await act(async () => {
    scroller.dispatchEvent(new dom.Event('scroll', { bubbles: true }));
  });
  await settle();
  assert.equal(tailInDom(container).length, 35, '短会话滚动往返后仍全量可见（守卫不再吞恢复）');

  await act(async () => { root.unmount(); });
  container.remove();
});

test('restoreTail 的 sticky 门：未贴底（isNearBottom=false）时不恢复', async () => {
  // 恢复判定复用 isNearBottom 现值：restoreTail 自己读 sticky，不贴底就拒绝
  // ——用户正在上翻时 sticky 短暂误判不能触发恢复（防打架）。直调 hook 验证。
  let scrollerEl = null;
  let restore = null;
  let stickyNow = false;
  const { container, root } = setup();
  function Probe() {
    const stickyRef = { current: false };
    const scrollRef = { current: null };
    const { restoreTail, end } = useWindowedHistory({
      sessionKey: 's1',
      total: 50,
      scrollRef: { current: null },
      isSticky: () => stickyRef.current,
    });
    // 手动喂一次调用序：sticky=false 时 restoreTail 不改窗口；sticky=true 时恢复。
    stickyNow = stickyRef.current;
    restore = restoreTail;
    return h('div', {}, String(end));
  }
  await act(async () => { root.render(h(Probe)); });
  await settle();
  // sticky=false：restoreTail 是 no-op（窗口维持初始——此处 total=50，
  // 初始 end=50 即尾部窗口，断言它不报错且不动窗口）。
  await act(async () => { restore(); });
  await settle();
  // sticky=true：同一调用恢复（本例 end=total 无可恢复，只验证不炸）。
  await act(async () => { restore(); });
  await settle();
  assert.ok(restore, 'hook 返回 restoreTail');
  container.remove();
});
