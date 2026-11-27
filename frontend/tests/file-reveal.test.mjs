// 文件卡跳编辑器（A4，file.reveal）与侧栏版本号接桥（A5）。
//
// 路径白名单的判定在后端（resolveWorkspacePath，Go 侧用例钉住）；前端层测：
//  ① revealFile 的缓存与错误路径（demo 源 / 壳桥缺席时动作不可见）；
//  ② 渲染钉子：FilesCard 的动作钮按「能力在场」显隐；
//  ③ A5：侧栏版本号不再硬编码（源码钉子——ver 显示 useUpdate().current）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

const here = new URL('.', import.meta.url);
const read = (p) => readFileSync(new URL(p, here), 'utf8');

const filesCardSrc = read('../src/components/thread/blocks/FilesCard.tsx');
const sidebarSrc = read('../src/components/sidebar/Sidebar.tsx');

// ---------- ① 渲染期显隐判定 ----------

test('canRevealFiles：源有 revealFile 且壳桥在场才 true（node 无壳桥 = false）', async () => {
  const { canRevealFiles } = await import('../src/components/thread/blocks/FilesCard.tsx');
  // node 测试环境没有 window.__LX__.files 桥 → 一律 false（动作不可见）
  assert.equal(canRevealFiles(undefined), false);
  assert.equal(canRevealFiles({ revealFile: async () => '/abs' }), false);
});

// ---------- ② revealFile 流程（缓存 + 打开失败回报） ----------

test('revealFile：先 file.reveal 拿绝对路径（缓存命中不再请求），壳失败上抛', async () => {
  // 壳桥桩（getFilesBridge 读 window.__LX__.files）
  const revealCalls = [];
  const openCalls = [];
  globalThis.window = {
    __LX__: {
      files: {
        reveal: async (p) => { revealCalls.push(p); return { ok: true }; },
        open: async (p) => { openCalls.push(p); return { ok: true }; },
      },
    },
  };
  const { revealFile } = await import('../src/components/thread/blocks/FilesCard.tsx');
  const source = {
    revealFile: async (sid, path) => {
      revealCalls.push(`rpc:${sid}:${path}`);
      return '/abs/path/a.go';
    },
  };
  await revealFile(source, 's1', 'a.go', 'open');
  await revealFile(source, 's1', 'a.go', 'reveal');
  // 缓存生效：RPC 只一次，壳调用两次（open + reveal）
  assert.equal(revealCalls.filter((c) => String(c).startsWith('rpc:')).length, 1);
  assert.equal(openCalls.length, 1);
  assert.equal(openCalls[0], '/abs/path/a.go');
  assert.equal(revealCalls.filter((c) => c === '/abs/path/a.go').length, 1);
  // 打开失败：错误上抛（调用方给一次性提示）
  globalThis.window.__LX__.files.open = async () => ({ ok: false, error: '文件不存在' });
  await assert.rejects(revealFile(source, 's1', 'a.go', 'open'), /文件不存在/);
  delete globalThis.window;
});

test('revealFile：无壳桥回 null（调用方静默跳过）', async () => {
  globalThis.window = { __LX__: undefined };
  const { revealFile } = await import('../src/components/thread/blocks/FilesCard.tsx');
  const r = await revealFile({ revealFile: async () => '/abs' }, 's1', 'a.go', 'open');
  assert.equal(r, null);
  delete globalThis.window;
});

// ---------- ③ 源码钉子：接线正确性 ----------

test('FilesCard：动作钮显隐按 revealable，WSAgent 实现 revealFile', () => {
  assert.match(filesCardSrc, /revealable \? handleReveal : undefined/);
  assert.match(filesCardSrc, /getFilesBridge\(\) !== null/);
  const wsSrc = read('../src/agent/ws/index.ts');
  assert.match(wsSrc, /"file\.reveal"/, 'WSAgent 必须实现 file.reveal 转发');
});

test('A5：侧栏版本号不再硬编码——ver 显示 useUpdate 的 current', () => {
  assert.doesNotMatch(sidebarSrc, /eabc22c/, '硬编码版本必须移除');
  assert.match(sidebarSrc, /current: updVersion/);
  assert.match(sidebarSrc, /hasUpdate \? "可更新" : updVersion/);
});
