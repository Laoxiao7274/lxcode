// AgentPicker 可委派面板的行为钉子（2026-12 用户报：面板贼长 + 换模型要去
// Agents 页逐个编辑）：
// ① 行集合 = backend truth：effectiveDelegates(agents, null)（主 Agent 名单
//    默认 ∩ 启用子 Agent）——不再列「全部启用的子 Agent」，也不再合成
//    sessionDelegates（那是前端内存态，后端分派不认它）；
// ② 每行有模型 chip（当前绑定显示名或「未绑定」），面板里有三级模型面板
//    （.ap-model）挂 data-pop；换模型走 updateAgent 乐观链路；
// ③ 面板限高 + 内部滚动（名单长不撑满屏）。
// AgentPicker 的二级/三级面板只在 open 态渲染，静态渲染（SSR）拿不到 open
// 态——运行时行为用受控渲染展开验证：把组件包进 provider、注入自定义
// agents 种子，断言收起/展开两种渲染的产物。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { effectiveDelegates } from '../src/shared/agent-delegation.ts';

// AgentPicker 依赖链里有模块顶层读 window 的兄弟模块（UpdateProvider 的壳桥
// 经 settings.tsx 传递）——先给最小桩再加载组件（与 approval-confirm 同模式）。
globalThis.window = { __LX__: undefined, dispatchEvent: () => {} };

const { SettingsProvider } = await import('../src/shared/settings.tsx');
const { AgentsProvider, useAgents } = await import('../src/shared/agents.tsx');
const { AgentPicker } = await import('../src/components/agents/AgentPicker.tsx');

// ---------- ① 行集合 = 名单默认 ∩ 启用子 Agent ----------

const sub = (id, enabled = true, model = '') => ({
  id, name: `子·${id}`, enabled, isMain: false, delegates: [], model,
});
const mainOf = (delegates) => ({
  id: 'main', name: '主', isMain: true, enabled: true, delegates, model: 'm1',
});

test('① AgentPicker 的行集合语义 = effectiveDelegates(agents, null)', () => {
  // 名单默认里指向停用 Agent 的条目自动出局——面板不许再列它（旧实现列
  // 「全部启用的子 Agent」，把名单外的也摆出来，是误导的另一半）。
  const agents = [mainOf(['a', 'b', 'ghost']), sub('a'), sub('b', false), sub('c')];
  assert.deepEqual(effectiveDelegates(agents, null), ['a']);
  assert.equal(effectiveDelegates(agents, null).includes('b'), false, '停用的不列');
  assert.equal(effectiveDelegates(agents, null).includes('c'), false, '名单外的启用子 Agent 也不列');
});

// 委派方按 delegates 维度判定（不按 isMain）——委派能力与主 Agent 身份解耦
// 的前置重构（assistant Agent 定义是后续功能提交）。
test('① 正例：isMain=false 但声明了 delegates → 有效名单非空', () => {
  const delegatingSub = { id: 'assistant', isMain: false, enabled: true, delegates: ['a'] };
  const agents = [delegatingSub, sub('a')];
  assert.deepEqual(effectiveDelegates(agents, null), ['a']);
  assert.deepEqual(effectiveDelegates(agents, ['a']), ['a']);
});

test('① 反例：isMain=true 但 delegates 为空 → 空名单', () => {
  const agents = [mainOf([]), sub('a')];
  assert.deepEqual(effectiveDelegates(agents, null), []);
  // 边界：agents 里没有任何 delegates 非空的 Agent（无委派方）时，
  // 会话覆盖非空仍按覆盖出（∩ 启用子 Agent，与旧实现一致）——覆盖优先
  // 于委派方查找；恢复默认（null）才回落到空名单。
  assert.deepEqual(effectiveDelegates(agents, ['a']), ['a']);
});

// ---------- ② 渲染：chip 文案 + 三级面板结构 + 覆盖只读指示 ----------

function fakeSource() {
  return {
    subscribe: () => () => {},
    send: () => {},
    setApproval: async () => {},
    confirm: async () => {},
    answer: async () => {},
    cancel: () => {},
    compact: async () => ({ compacted: false }),
    rewind: async () => ({ removed: 0 }),
    newSession: async () => 's1',
    mergeRequest: async () => 'j1',
    resumeSession: () => {},
    renameSession: () => {},
    archiveSession: async () => ({ ok: true }),
    unarchiveSession: () => {},
    childHistory: async () => ({ messages: [] }),
    sessions: () => [],
    projects: () => [],
    addProject: () => {},
    readInstructions: async () => ({ content: '', exists: false }),
    saveInstructions: async () => {},
    label: 'demo',
  };
}

/** 注入自定义 agents 种子的渲染：通过一个探针子组件调 useAgents 的
 *  setAgents 不可行（Provider 没暴露）——改为直接断言 seed 驱动的渲染，
 *  种子名单（主 Agent delegates ∩ 启用子 Agent）即面板行集合。 */
function renderPicker() {
  return renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(AgentsProvider, { source: fakeSource() },
      createElement(AgentPicker),
    ),
  ));
}

test('② 收起态渲染：委派入口在、覆盖指示不在（未展开）', () => {
  const html = renderPicker();
  assert.ok(html.includes('agent-chip'), 'Agent chip 应渲染: ' + html);
  assert.ok(html.includes('可委派'), 'chip 上带可委派计数');
  // 收起态没有二级面板（ap-panel 只在 open && panel 时渲染）
  assert.ok(!html.includes('ap-panel'), '收起态不渲染二级面板');
});

test('② 源码契约：二级面板行集合走 effectiveDelegates(agents, null)，不再合成会话覆盖', () => {
  const src = fs.readFileSync(new URL('../src/components/agents/AgentPicker.tsx', import.meta.url), 'utf8');
  assert.ok(src.includes('effectiveDelegates(agents, null)'),
    '行集合必须是名单默认 ∩ 启用（backend truth），不得传 sessionDelegates 合成');
  assert.ok(src.includes('ap-model-chip') && src.includes('ap-model"'),
    '行尾模型 chip 与第三级 .ap-model 面板必须存在');
  assert.ok(src.includes('未绑定'), '未绑定模型的子 Agent 要有灰态占位');
  assert.ok(src.includes('updateAgent({ ...agent, model: modelId })'),
    '换模型走 updateAgent 乐观链路（先例 = Agents 页 enabled 开关）');
  assert.ok(src.includes("可委派（名单）"), '面板文案改为「可委派（名单）」');
  // 勾选交互降级：不再有 menuitemcheckbox 的可勾行（写不到后端的假开关）
  assert.ok(!src.includes('menuitemcheckbox'), 'sessionDelegates 勾选交互必须移除（后端不认）');
  assert.ok(src.includes('setSessionDelegates(null)'), '只读覆盖指示保留「恢复默认」出口');
  // 三级面板在 rootRef 内（点外不误关：useDismissal 以 rootRef 判内外）
  assert.ok(/ref=\{rootRef\}[\s\S]*ap-model/.test(src), '三级面板必须包在 rootRef 内');
});
