// 助手输入附件补齐（2026-12 批次 1：文件入口 + paste/drop + files 透传）的钉子：
// ① AssistantInput 源码断言：文件菜单项 + 通用文件 input + textarea onPaste +
//    容器 onDragOver/onDrop + FolderFileCards 预览（照 assistant-config-tools
//    的源码断言模式——组件交互层 node 测不了，钉结构）；
// ② wire 层：AssistantDomain.send 的 images/files 都上 assistant.send
//    （files 与 chat.send 同款 wireFile——path 形态只带路径、file 不上 wire）；
// ③ assistantSendOpts：files 透传（不再丢弃）；
// ④ demo 源：assistantSend 只附件（files）也放行、正文标记如实计数。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

import { AssistantDomain } from '../src/agent/ws/assistant.ts';
import { assistantSendOpts } from '../src/agent/ws/assistant.ts';

const inputSrc = fs.readFileSync(
  new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8');

// ---------- ① AssistantInput：文件入口 / paste / drop / 预览 ----------

test('① 「+」菜单有「文件」项 + 通用文件 input（无 accept 白名单）', () => {
  assert.ok(/title="从本地选择文件（最多 4 个，单个 20MB 内；交给智能体读取）"/.test(inputSrc),
    '文件菜单项 title 与主域 Composer 同文案');
  assert.ok(inputSrc.includes('文件\n                  </button>'), '菜单项文案「文件」');
  // 两个隐藏 input：图片带 accept 白名单、文件无 accept（通用文件）
  assert.ok(/ref=\{imgInputRef\}[\s\S]*?accept="image\/png,image\/jpeg,image\/webp,image\/gif"/.test(inputSrc),
    '图片 input 带 mime 白名单');
  assert.ok(/ref=\{fileInputRef\}[\s\S]*?multiple/.test(inputSrc), '文件 input 存在且 multiple');
  // vision 门只禁图片项：文件项 disabled 不挂 visionBlocked，图片项挂
  assert.ok(/disabled=\{Boolean\(visionBlocked\)\}[\s\S]{0,400}从本地选择图片/.test(inputSrc),
    'vision 门挂在图片项');
  assert.ok(!/disabled=\{Boolean\(visionBlocked\)\}[\s\S]{0,400}从本地选择文件/.test(inputSrc),
    '文件项不受 vision 门限');
});

test('① textarea onPaste + 容器 onDragOver/onDragLeave/onDrop + 文件夹拖入', () => {
  assert.ok(inputSrc.includes('onPaste={onPaste}'), 'textarea 挂 onPaste（剪贴板图片进暂存）');
  assert.ok(inputSrc.includes('onDragOver={onDragOver}'), '容器挂 onDragOver');
  assert.ok(inputSrc.includes('onDrop={onDrop}'), '容器挂 onDrop');
  assert.ok(inputSrc.includes('ingestFolders'), '文件夹拖入走 folder-drop 收集（与主域同款）');
  assert.ok(inputSrc.includes('collectFolderEntries, defaultFolderDeps'), '复用 shared/folder-drop 纯函数');
});

test('① 暂存区渲染 FolderFileCards + attsFromPending(images, files) 带真文件', () => {
  assert.ok(inputSrc.includes('<FolderFileCards'), '文件暂存渲染主域同款 FolderFileCards');
  assert.ok(inputSrc.includes('attsFromPending(images, files)'), '发送组装带文件');
  assert.ok(inputSrc.includes('hasPendingAtts(images, files)'), 'canSend 门计文件');
});

// ---------- ② AssistantDomain.send：images/files 都上 wire ----------

test('② assistant.send：images 走 wireImage、files 走 wireFile（path 形态只带路径）', () => {
  const calls = [];
  const conn = { call: (method, params) => { calls.push({ method, params }); return Promise.resolve({}); } };
  const domain = new AssistantDomain(conn, () => {}, () => {});
  domain.send('a1', '看一下', {
    images: [{ mime: 'image/png', data: 'AAAA' }],
    files: [{ name: 'a.txt', data: 'aGVsbG8=' }],
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, 'assistant.send');
  assert.deepEqual(calls[0].params.files, [{ name: 'a.txt', data: 'aGVsbG8=' }],
    'files 与 chat.send 同款 wireFile 形状');
  assert.deepEqual(calls[0].params.images, [{ mime: 'image/png', data: 'AAAA' }]);
  // path 形态：只带路径，file 对象不上 wire
  calls.length = 0;
  domain.send('a1', '看路径文件', {
    files: [{ name: 'b.txt', data: '', path: 'C:\\tmp\\b.txt', file: new File(['x'], 'b.txt') }],
  });
  assert.deepEqual(calls[0].params.files, [{ name: 'b.txt', path: 'C:\\tmp\\b.txt' }],
    'path 形态 file 剥掉、data 不上 wire');
});

test('② 只附件（files）不打字也放行；空文本无附件不发', () => {
  const calls = [];
  const conn = { call: (method, params) => { calls.push({ method, params }); return Promise.resolve({}); } };
  const domain = new AssistantDomain(conn, () => {}, () => {});
  domain.send('a1', '', { files: [{ name: 'a.txt', data: 'aGk=' }] });
  assert.equal(calls.length, 1, '只附件没文本仍发送');
  calls.length = 0;
  domain.send('a1', '', undefined);
  assert.equal(calls.length, 0, '空文本无附件不发');
  calls.length = 0;
  domain.send('a1', '', { files: [] });
  assert.equal(calls.length, 0, '空数组附件同不发');
});

// ---------- ③ assistantSendOpts：files 透传 ----------

test('③ assistantSendOpts 透传 files（不再丢弃）', () => {
  const files = [{ name: 'a.txt', data: 'aGk=' }];
  const images = [{ mime: 'image/png', data: 'AAAA' }];
  const out = assistantSendOpts({ effort: 'low', images, files });
  assert.deepEqual(out, { effort: 'low', images, files });
  // 全空 → undefined
  assert.equal(assistantSendOpts(undefined), undefined);
  assert.equal(assistantSendOpts({ images: [], files: [] }), undefined);
  // 只文件也成立
  assert.deepEqual(assistantSendOpts({ files }), { files });
});

// ---------- ④ demo 源：assistantSend 只附件放行 ----------

test('④ demo assistantSend 只附件（files）也发 user 消息', async () => {
  const src = fs.readFileSync(
    new URL('../src/agent/demo/index.ts', import.meta.url), 'utf8');
  assert.ok(src.includes('opts?.images?.length || opts?.files?.length'),
    'demo assistantSend 的放行门计 files');
  assert.ok(src.includes('[文件]×'), 'demo 正文标记如实呈现文件数');
});
