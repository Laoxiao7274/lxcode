// 「完全访问」开启确认弹窗（2026-10-09 用户拍板：二次点击 → 弹窗确认）的契约：
// ① 分流规则是**升险才拦**：confirm/strict → auto 先弹窗，其余档位（含 auto 降回
//    安全档）直接切——收权不需要向用户要许可。规则在 shared/approval.ts 纯函数里，
//    PermPicker 与设置面板两个入口共用一份，测它就是测两个入口的规则；
// ② 弹窗本体复用归档/释放弹层同款（proj-add-mask + alertdialog + aria-modal），
//    Esc/遮罩 = 取消——静态渲染钉结构，别让「复用现有弹层」悄悄变成另起炉灶；
// ③ 旧的「再次点击」内联确认（perm-item.confirming / settings-confirm-auto）必须
//    消失——留着的话同一件事就有两种确认形态。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { needsApprovalConfirm, approvalPick } from '../src/shared/approval.ts';
import { SettingsProvider } from '../src/shared/settings.tsx';

// SettingsPanel 的依赖链里有模块顶层读 window（UpdateProvider 的壳桥）——
// node 没有 window，先给最小桩再加载组件（与 sidebar-busy.test.mjs 同一模式）。
globalThis.window = { __LX__: undefined };

const { UpdateProvider } = await import('../src/shared/update.tsx');
const { PermPicker } = await import('../src/components/perm-picker/PermPicker.tsx');
const { ApprovalConfirmDialog } = await import('../src/components/perm-picker/ApprovalConfirmDialog.tsx');
const { SettingsPanel } = await import('../src/components/settings/SettingsPanel.tsx');

// ---------- ① 分流规则：升险弹窗、降险直接切 ----------

test('needsApprovalConfirm：只有升险到 auto 拦，降险/同档不拦', () => {
  assert.equal(needsApprovalConfirm('confirm', 'auto'), true, '默认 → 完全访问要弹窗');
  assert.equal(needsApprovalConfirm('strict', 'auto'), true, '只读 → 完全访问要弹窗');
  assert.equal(needsApprovalConfirm('auto', 'auto'), false, '已是完全访问不拦');
  assert.equal(needsApprovalConfirm('auto', 'confirm'), false, '完全访问 → 默认直接切（收权安全）');
  assert.equal(needsApprovalConfirm('auto', 'strict'), false, '完全访问 → 只读直接切（收权安全）');
  assert.equal(needsApprovalConfirm('confirm', 'strict'), false, '默认 → 只读直接切（收权安全）');
  assert.equal(needsApprovalConfirm('strict', 'confirm'), false, '只读 → 默认直接切（收权安全）');
});

test('approvalPick：升险到 auto 只开弹窗、绝不直接改档；其余档位直接改', () => {
  const applied = [], asked = [];
  const apply = (m) => applied.push(m);
  const askConfirm = () => asked.push(true);

  approvalPick('confirm', 'auto', apply, askConfirm);
  assert.deepEqual(asked, [true], '升险：走弹窗');
  assert.deepEqual(applied, [], '弹窗期间绝不能已经改档');

  approvalPick('auto', 'confirm', apply, askConfirm);
  approvalPick('confirm', 'strict', apply, askConfirm);
  approvalPick('auto', 'auto', apply, askConfirm);
  assert.deepEqual(asked, [true], '降险/同档不再开弹窗');
  assert.deepEqual(applied, ['confirm', 'strict', 'auto'], '降险直接改档');
});

// ---------- ② 弹窗结构：复用归档/释放弹层同款 ----------

test('ApprovalConfirmDialog：alertdialog 结构 + 后果说明 + 开启/取消按钮', () => {
  const html = renderToStaticMarkup(createElement(ApprovalConfirmDialog, { onConfirm: () => {}, onCancel: () => {} }));
  assert.ok(html.includes('proj-add-mask'), '复用现有弹层遮罩 class: ' + html);
  assert.ok(html.includes('role="alertdialog"'), html);
  assert.ok(html.includes('aria-modal="true"'), html);
  assert.ok(html.includes('开启完全访问？'), '标题要出现: ' + html);
  assert.ok(html.includes('不再逐条请求确认'), '后果说明（高危不再逐条确认）: ' + html);
  assert.ok(html.includes('子代理与合并进程'), '后果说明（子代理/合并进程同档）: ' + html);
  assert.ok(html.includes('ask'), '说明 ask 提问不被自动档替代: ' + html);
  assert.ok(html.includes('>开启完全访问</button>'), '确认按钮: ' + html);
  assert.ok(html.includes('>取消</button>'), '取消按钮: ' + html);
  assert.ok(html.includes('autofocus'), '焦点落取消按钮（Esc/回车默认取消，升险须显式点确认）: ' + html);
});

// ---------- ③ 旧「二次点击」形态必须消失 ----------

test('PermPicker 静态渲染（收起态）：弹窗不渲染、二次点击形态不残留', () => {
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(PermPicker),
  ));
  // 静态渲染拿不到内部 open 态（菜单只在展开时渲染），能钉住的是：
  // 收起态不弹窗、整条渲染链里没有「再次点击」两步确认的残留
  assert.ok(html.includes('perm-chip'), '权限 chip 应渲染: ' + html);
  assert.ok(!html.includes('alertdialog'), '收起态不该渲染确认弹窗: ' + html);
  assert.ok(!html.includes('再次点击确认'), '「再次点击」两步确认文案必须消失: ' + html);
  assert.ok(!html.includes('perm-item confirming'), '二次点击确认态样式必须消失: ' + html);
});

test('SettingsPanel 静态渲染：高危操作分段在、旧内联确认条消失', () => {
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(UpdateProvider, null,
      createElement(SettingsPanel, { open: true, onClose: () => {}, source: fakeSource() }),
    ),
  ));
  assert.ok(html.includes('高危操作'), html);
  assert.ok(!html.includes('settings-confirm-auto'), '旧内联确认条必须消失（换弹窗）: ' + html);
  assert.ok(!html.includes('再次点击'), '两步确认文案不得残留: ' + html);
});

// ---------- 测试脚手架（与 attachments.test.mjs 同款最小 source） ----------

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
    releaseWorktree: async () => {},
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
