// 子会话临时视图的「正在处理/思考中」呈现钉子（2026-12 用户报「子会话视图缺少
// 生成中样式」的补齐批）：
//   ① 数据侧已有信号：子会话 state 的 busy 由 CHILD_BUSY_HINTS（delta/toolCall/
//      toolResult/userMessage/confirmRequest）兜底置真、轮末 busy=false 翻回——
//      这里钉住双投归约的忙闲翻转（busy=false 要能翻回，否则指示条永久挂着）；
//   ② 呈现侧：ChildSessionPage 时间线尾部复刻主时间线的形态——state.busy 且
//      尾块不是流式 assistant 时渲染「正在处理…」shimmer（Thread.tsx 同一判定、
//      同一文案、同一 ThinkingState 组件）；结构断言钉住挂载位置与判定来源；
//   ③ 页头忙点：.s-dot + busy 时加 .live（会话侧栏同一套件）；
//   ④ CSS 钉子：页头 .s-dot 的尺寸覆盖在 panels.css（页头行高更大）。
// 流式打字光标（AnswerBody stream-reveal）与思考块（ThinkingReasoning）由共用的
// Block 渲染路径自带（子会话 blocks 走同一个 Block 组件）——不在此重复钉。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { reduceSessionStates } from '../src/shared/reduce.ts';
import { mapEvent } from '../src/agent/ws/events.ts';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null };

// ---- ① 数据侧：CHILD_BUSY_HINTS 兜底置真 + busy=false 翻回 ----

test('子会话双投：toolCall 等活动事件把 busy 置真，busy=false 事件翻回（指示条要能消失）', () => {
  // dispatchStart 已记下 d1→child-1 的归属（真实接线在 dispatchStart，这里直接给映射）
  const dispatchChild = { d1: 'child-1' };
  let states = { s1: { ...base }, 'child-1': { ...base, currentId: 'child-1' } };

  // 子会话的工具调用（带归属）→ 子会话 state.busy 置真
  states = reduceSessionStates(states, {
    type: 'toolCall', sessionId: 's1', dispatchId: 'd1', id: 't1', name: 'read_file', arguments: '{}',
  }, dispatchChild);
  assert.equal(states['child-1'].busy, true, '活动事件应把子会话 busy 兜底置真');

  // 轮末 busy=false（无条件发）→ 翻回，指示条/忙点据此消失
  states = reduceSessionStates(states, {
    type: 'busy', sessionId: 's1', dispatchId: 'd1', busy: false,
  }, dispatchChild);
  assert.equal(states['child-1'].busy, false, '轮末 busy=false 必须翻回（否则指示条永久挂着）');

  // 带归属的 busy=true 也照常进子会话 state（视图打开前兜底路径之外的正路）
  states = reduceSessionStates(states, {
    type: 'busy', sessionId: 's1', dispatchId: 'd1', busy: true,
  }, dispatchChild);
  assert.equal(states['child-1'].busy, true);
});

test('wire 映射：chat.busy 带 dispatch_id（子会话忙闲的归属键要在映射层保留）', () => {
  const ev = mapEvent('chat.busy', { session_id: 's1', dispatch_id: 'd1', busy: true });
  assert.equal(ev.type, 'busy');
  assert.equal(ev.dispatchId, 'd1', 'dispatch_id 丢失 = 子会话忙闲落不到子会话 state 上');
  assert.equal(ev.busy, true);
});

// ---- ② 呈现侧：时间线尾部「正在处理…」指示（主时间线同款判定与文案） ----

test('ChildSessionPage：busy 且尾块非流式 assistant 时渲染「正在处理…」（主时间线同款形态）', async () => {
  const src = fs.readFileSync(new URL('../src/components/panels/ChildSessionPage.tsx', import.meta.url), 'utf8');
  // 判定与 Thread.tsx 同源：busy && !lastIsStreamingAssistant——不另立一套条件
  assert.ok(/busy && !lastIsStreamingAssistant\(blocks\)/.test(src),
    '子会话的指示判定必须与主时间线一致（busy && !lastIsStreamingAssistant）');
  assert.ok(src.includes('import { ThinkingState }'), '复用 ThinkingState 组件（不造第二套 shimmer）');
  assert.ok(src.includes('<ThinkingState text="正在处理…" />'),
    '文案与主时间线同一句（Thread.tsx：正在处理…）');
  // 指示条挂在 .thread 容器内、endRef（跟随锚点）之前——与 Thread.tsx 的挂载位置一致
  //（用 lastIndexOf：JSX 之外 lastIsStreamingAssistant 的注释里也提到这句文案）
  const indIdx = src.lastIndexOf('正在处理…');
  const endRef = src.indexOf('ref={endRef}');
  const threadOpen = src.indexOf('<div className="thread">');
  assert.ok(threadOpen > 0 && threadOpen < indIdx && indIdx < endRef,
    '「正在处理…」应挂在 .thread 容器内、跟随锚点之前');
});

test('lastIsStreamingAssistant：只认尾块是流式中的 assistant（与非流式/工具块区分）', async () => {
  const src = fs.readFileSync(new URL('../src/components/panels/ChildSessionPage.tsx', import.meta.url), 'utf8');
  assert.ok(/kind === "assistant" && last\.streaming/.test(src),
    '判定 = 尾块 kind=assistant 且 streaming（delta 流式期间不再叠「正在处理…」）');
});

// ---- ③ 页头忙点：.s-dot + busy 时 .live（侧栏同一套件） ----

test('ChildSessionPage 页头有忙闲点：busy 加 .live（会话侧栏同一套件）', async () => {
  const src = fs.readFileSync(new URL('../src/components/panels/ChildSessionPage.tsx', import.meta.url), 'utf8');
  assert.ok(/className=\{"s-dot" \+ \(busy \? " live" : ""\)\}/.test(src),
    '页头忙点 = .s-dot + busy ? .live（SessionRow 同款写法）');
  assert.ok(src.includes('aria-hidden'), '忙点是纯装饰（aria-hidden）');
});

// ---- ④ CSS 钉子：页头忙点的尺寸覆盖 ----

test('panels.css 有 .child-session-head .s-dot 的尺寸覆盖（页头行高更大）', () => {
  const css = fs.readFileSync(new URL('../src/styles/panels.css', import.meta.url), 'utf8');
  assert.ok(/\.child-session-head \.s-dot\s*\{[^}]*width:\s*6px/.test(css),
    '页头忙点尺寸覆盖必须存在（否则 5px 的侧栏尺寸在页头行里显小）');
});
