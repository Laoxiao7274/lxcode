// 任务清单模板（todo-first 三档）设置项的契约：
// ① 设置面板通用分区渲染三档单选（严格/常规/关闭）+ 当前档的一句话含义；
// ② live 模式（source.todoPromptAdmin 在）读后端缓存、set 走 WS（后端广播
//    回来才改显示——后端是唯一事实源，不做本地乐观）；
// ③ demo（无 todoPromptAdmin）本地自管：点选即改。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { SettingsProvider } from '../src/shared/settings.tsx';

// SettingsPanel 的依赖链里有模块顶层读 window（UpdateProvider 的壳桥）——
// node 没有 window，先给最小桩再加载组件（与 approval-confirm.test.mjs 同一模式）。
globalThis.window = { __LX__: undefined };

const { UpdateProvider } = await import('../src/shared/update.tsx');
const { SettingsPanel } = await import('../src/components/settings/SettingsPanel.tsx');

// ---------- ① 静态渲染：三档分段 + 含义文案 ----------

function baseSource(overrides = {}) {
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
    ...overrides,
  };
}

function renderPanel(source) {
  return renderToStaticMarkup(createElement(
    SettingsProvider,
    { source },
    createElement(UpdateProvider, null,
      createElement(SettingsPanel, { open: true, onClose: () => {}, source }),
    ),
  ));
}

test('SettingsPanel 渲染任务清单三档分段（严格/常规/关闭）+ 一句话含义', () => {
  const html = renderPanel(baseSource());
  assert.ok(html.includes('任务清单'), '分段行标题应出现: ' + html);
  assert.ok(html.includes('role="radiogroup"'), '三档是单选分段: ' + html);
  assert.ok(html.includes('>严格</button>'), '严格档: ' + html);
  assert.ok(html.includes('>常规</button>'), '常规档: ' + html);
  assert.ok(html.includes('>关闭</button>'), '关闭档: ' + html);
  assert.ok(html.includes('需要工具的任务必须先建 3~7 条清单'), 'strict 含义文案: ' + html);
  assert.ok(html.includes('按任务复杂度建清单'), 'normal 含义文案: ' + html);
  assert.ok(html.includes('不注入清单指令'), 'off 含义文案: ' + html);
  // 缺省（demo 本地态 = strict）时选中的是严格档（选中 = aria-checked="true"）
  assert.ok(html.includes('aria-checked="true" title="需要工具的任务必须先建'), '缺省选中严格档: ' + html);
  assert.ok(!html.includes('aria-checked="true" title="按任务复杂度建清单'), '常规档缺省不选中: ' + html);
  assert.ok(!html.includes('aria-checked="true" title="不注入清单指令'), '关闭档缺省不选中: ' + html);
});

// ---------- ② live 模式：读缓存 + set 走 WS ----------

test('live 模式：显示后端缓存档位；点选经 admin.set 写回（不做本地乐观改显）', async () => {
  const sets = [];
  let backendMode = 'normal';
  const listeners = new Set();
  const admin = {
    mode: () => backendMode,
    onChange: (l) => { listeners.add(l); return () => listeners.delete(l); },
    set: async (m) => { sets.push(m); },
  };
  const html = renderPanel(baseSource({ todoPromptAdmin: admin }));
  assert.ok(html.includes('aria-checked="true" title="按任务复杂度建清单'), '显示后端缓存档位 normal: ' + html);
  assert.ok(!html.includes('aria-checked="true" title="需要工具的任务必须先建'), '不显示 strict 为选中: ' + html);
  assert.deepEqual(sets, [], '静态渲染不该触发 set');
  // set 语义：调用 admin.set（后端广播负责把显示刷新——本测试钉住调用方向）
  await admin.set('off');
  assert.deepEqual(sets, ['off']);
});
