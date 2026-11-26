// OOM 三件套 S2 的钉子：助手标签关掉后 mountedIds 真摘除（AssistantPage 实例
// 卸载、内存可回收），不是只加 tabClosed（旧行为：mountedIds 只增不删）。
//
// 方式：真挂载（react-dom/client）+ happy-dom 的全局 DOM（21.x 没有独立
// global-registrator 包——手工把 Window 实例的字段搬进 globalThis）。数据源用
// 内存 stub（assistant.* 方法，无网络无后端）。驱动路径：
//   ① 点会话行 → AssistantPage 实例挂载（DOM 节点 data-asst-session 为证）；
//   ② 关非当前标签的 × → 实例节点消失（挂载层摘除 + frozenRef 同步清空的联合证据）；
//   ③ 关当前标签的 × → 焦点转移到 fallback 后旧实例摘除；
//   ④ 新建会话（侧栏「新对话」）→ 旧实例摘除、新实例挂载（新建 ≠ 留两个）；
//   ⑤ 侧键 back（window.__lxNavBack）→ 离场实例摘除。
// state 更新走真实时钟等待（React 19 离散事件同步渲染 + 微任务调度的
// setState 各等一拍；act 的微任务 flush 覆盖不了 promise 链深处的 await）。
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
// React 19 的 act 需要 IS_REACT_ACT_ENVIRONMENT 标记
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement, act } from 'react';
import ReactDOMClient from 'react-dom/client';
const { createRoot } = ReactDOMClient;

const { AssistantWorkspace } = await import('../src/components/assistant/AssistantWorkspace.tsx');
const { SettingsProvider } = await import('../src/shared/settings.tsx');

const meta = (id, extra = {}) => ({
  id, title: `对话 ${id}`, updatedAt: '2026-12-08 12:00', messages: 1, archived: false, ...extra,
});

/** 内存 stub 数据源：list 可变、订阅可手动触发。 */
function stubSource(listSessions = []) {
  const listeners = new Set();
  let sessions = listSessions;
  let nextId = 1;
  return {
    subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
    __emit() { for (const fn of [...listeners]) fn({ type: 'ready' }); },
    assistant: {
      list: async () => sessions,
      newSession: async () => { const id = `new-${nextId++}`; sessions = [...sessions, meta(id)]; return id; },
      history: async () => {}, send: async () => {}, confirm: async () => {},
      compact: async () => {}, childHistory: async () => {},
      configGet: async () => ({}), configSet: async () => {},
    },
  };
}

const App = (source) => createElement(SettingsProvider, { source }, createElement(AssistantWorkspace, {
  source, assistantStates: {}, resolveAssistant: () => {}, childParents: {},
  reportError: () => {}, onOpenSettings: () => {}, mode: 'assistant',
}));

const settle = () => new Promise((r) => setTimeout(r, 30));
/** 轮询直到断言通过或超时（async 链的真实时钟落定时间随负载波动——固定 30ms
 *  的 settle 在满负荷机器上偶发不够，正确性断言改为条件等待，不是放宽容忍）。 */
const waitFor = async (fn, desc) => {
  for (let i = 0; i < 50; i++) {
    const v = fn();
    if (v) return v;
    await new Promise((r) => setTimeout(r, 10));
  }
  return null; // 500ms 仍未满足——交给断言如实红（不掩盖真实回归）
};
const mountedIdsIn = (container) =>
  [...container.querySelectorAll('[data-asst-session]')].map((el) => el.dataset.asstSession);
/** 点击 + 等一拍（微任务调度的 setState 与 promise 链都要真实时钟落定）。 */
const click = async (el) => {
  el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  await settle();
};
const rowOf = (container, id) =>
  [...container.querySelectorAll('.session-item')].find((el) => el.textContent.includes(id));
const closeBtnOf = (container, id) =>
  [...container.querySelectorAll('.asst-tab-close')]
    .find((el) => el.closest('.asst-tab')?.title.includes(id) || el.getAttribute('aria-label')?.includes(id));

/** 渲染一个工作区并等列表装载完成。 */
async function setup(sessions) {
  localStorage.clear(); // 用例间隔离（lastAssistantSession 记忆会串测试）
  const source = stubSource(sessions);
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => { root.render(App(source)); });
  await settle();
  return { source, container, root };
}

test('S2：关标签（非当前/当前）后 mountedIds 真摘除', async () => {
  const { container } = await setup([meta('a1'), meta('a2')]);
  assert.ok(rowOf(container, 'a1'), '列表装载（会话行渲染）');

  // ① 点开 a1 → 实例挂载
  await click(rowOf(container, 'a1'));
  assert.deepEqual(mountedIdsIn(container), ['a1'], '点开会话挂载实例');

  // ② 点开 a2 → 两实例并存（保活常驻）
  await click(rowOf(container, 'a2'));
  assert.deepEqual(mountedIdsIn(container).sort(), ['a1', 'a2'], '两会话实例并存');

  // ③ 关掉**非当前**标签 a1 → 实例摘除
  const closeBtn1 = closeBtnOf(container, 'a1');
  assert.ok(closeBtn1, '找到 a1 标签的关闭钮');
  await click(closeBtn1);
  assert.deepEqual(mountedIdsIn(container), ['a2'], '关非当前标签：实例摘除（冻结缓存随之失去渲染者）');

  // ④ 关掉**当前**标签 a2 → 无 fallback 可让（a1 已关），焦点原地不动，实例暂留
  const closeBtn2 = closeBtnOf(container, 'a2');
  await click(closeBtn2);
  assert.deepEqual(mountedIdsIn(container), ['a2'], '最后标签关不掉（session-tabs 规则）——焦点不动实例暂留');
});

test('S2：关当前标签且存在 fallback → 焦点转移后旧实例摘除', async () => {
  const { container } = await setup([meta('b1'), meta('b2')]);
  await click(rowOf(container, 'b1'));
  await click(rowOf(container, 'b2'));
  assert.deepEqual(mountedIdsIn(container).sort(), ['b1', 'b2']);

  // 关当前标签 b2 → 焦点让给 b1（最右优先）→ b2 实例摘除、b1 保留
  const closeBtn2 = closeBtnOf(container, 'b2');
  await click(closeBtn2);
  assert.deepEqual(mountedIdsIn(container), ['b1'], '关当前标签：焦点转移到 fallback，旧实例摘除');
});

test('S2：新建会话 = 关掉旧会话语义（旧实例摘除 + 新实例挂载）', async () => {
  const { container } = await setup([meta('a1')]);
  await click(rowOf(container, 'a1'));
  assert.deepEqual(mountedIdsIn(container), ['a1']);

  // 侧栏「新对话」nav → 新建后旧实例摘除（不是留着两个常驻页）。
  // newSession + refreshList 是两条 async 链，摘除断言用条件等待（见 waitFor）。
  const navNew = [...container.querySelectorAll('.nav-item')].find((el) => el.textContent.includes('新对话'));
  await click(navNew);
  let lastSeen = null;
  const ids = await waitFor(() => {
    const cur = mountedIdsIn(container);
    lastSeen = cur;
    return cur.length === 1 && cur[0] === 'new-1' ? cur : null;
  }, '新建后旧实例摘除');
  assert.deepEqual(ids, ['new-1'], `新建后旧实例摘除、新实例挂载（等待结束时 mountedIds=${JSON.stringify(lastSeen)}）`);
});

test('S2：鼠标侧键 back 离开当前会话 → 离场实例摘除（回列表中性态）', async () => {
  const { container } = await setup([meta('a1')]);
  await click(rowOf(container, 'a1'));
  assert.deepEqual(mountedIdsIn(container), ['a1']);

  assert.equal(typeof window.__lxNavBack, 'function', '助手模式可见时注册侧键探针');
  window.__lxNavBack();
  await settle();
  assert.deepEqual(mountedIdsIn(container), [], 'back 回中性态：离场实例摘除');
});

// ---- 标签持久化恢复（2026-12 两域一致）----
// seed effect 走 restoreTabStrip("asst")：有记忆恢复（交集清洗——归档/删除的
// 不复活、closed 指向已删会话的条目丢弃）、无记忆走 seed 原路径。活跃标签以
// lastAssistantSession 为准（恢复后 append effect 把它补进最右）。

test('持久化：有记忆时按持久化顺序恢复，归档/删除的会话不复活', async () => {
  const { container } = await setup([meta('a1'), meta('a2'), meta('a3')]);
  // 铺开：点开 a1、a2（顺序 a1, a2）
  await click(rowOf(container, 'a1'));
  await click(rowOf(container, 'a2'));
  await settle();
  const persisted = JSON.parse(localStorage.getItem('lx.app.asst.taborder'));
  assert.ok(persisted.includes('a1') && persisted.includes('a2'), `标签条已落盘（order=${JSON.stringify(persisted)}）`);

  // 模拟重启：清 DOM 重新挂载（新 root），localStorage 记忆保留；
  // a2 已归档、dead 不在列表——恢复时两者都不应出现在标签条。
  localStorage.setItem('lx.app.asst.taborder', JSON.stringify(['dead', 'a1', 'a2']));
  localStorage.setItem('lx.app.asst.tabclosed', JSON.stringify(['gone']));
  container.innerHTML = '';
  // a1 记为活跃标签（lastAssistantSession——重启后活跃会话恢复单标签，
  // append effect 会把它补进标签条；restoreTabStrip 本身不保证含活跃会话）。
  localStorage.setItem('lx.app.lastAssistantSession', 'a1');
  const source = stubSource([meta('a1'), meta('a2', { archived: true }), meta('a3')]);
  const root2 = createRoot(container);
  act(() => { root2.render(App(source)); });
  await settle();
  const tabs = [...container.querySelectorAll('.asst-tab')].map((el) => el.title);
  assert.ok(!tabs.some((t) => t.includes('a2')), '归档会话不复活');
  assert.ok(!tabs.some((t) => t.includes('dead')), '已删除会话不复活');
  assert.ok(tabs.some((t) => t.includes('a1')), `持久化的存活标签恢复（tabs=${JSON.stringify(tabs)}）`);
});

test('持久化：无记忆时走 seed 原路径（后端序反转）', async () => {
  localStorage.clear();
  const { container } = await setup([meta('a1'), meta('a2'), meta('a3')]);
  // 未点开任何会话（中性态不渲染标签条）→ order 数组已 seed（seed effect 无视
  // 活跃会话先铺一遍），落盘键 = seed 反转序；点开会话后标签条出现（seed 本就
  // 含全部会话，点击只是补进活跃）。断言落盘顺序 = 后端序反转（「新的在右」）。
  assert.deepEqual(JSON.parse(localStorage.getItem('lx.app.asst.taborder')), ['a3', 'a2', 'a1'], 'seed 落盘（后端序反转）');
  await click(rowOf(container, 'a1'));
  const tabs = [...container.querySelectorAll('.asst-tab')].map((el) => el.title);
  assert.ok(tabs.length === 3, `seed 铺开标签（tabs=${JSON.stringify(tabs)}）`);
  assert.ok(tabs[2].includes('a1'), '点击的会话高亮在最右（seed 序即最终序）');
});
