// 文件夹拖入展开（shared/folder-drop.ts）的测试：
// 钉住「拖文件夹不再报找不到文件」的契约——entry 树递归展开为附件列表 +
// 人话说明；上限截断不静默；隐藏项跳过；个别读取失败跳过不整批失败。
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  collectFolderEntries,
  defaultFolderDeps,
  MAX_FOLDER_FILES,
  MAX_FOLDER_TOTAL_BYTES,
} from '../src/shared/folder-drop.ts';

// ---- 假 entry 工厂 ----

/** 假文件 entry：size 记在 side 表里，defaultDeps 的 entryFile 回调从这取。 */
const fileEntry = (name, size = 10) => ({
  isFile: true,
  isDirectory: false,
  name,
  file: (ok) => ok({ name, size }),
});

/** 假目录 entry：children 可以分批（readEntries 每批最多 100 条的分页语义）。 */
const dirEntry = (name, children, batchOf = Infinity) => {
  const batches = [];
  for (let i = 0; i < children.length; i += batchOf) batches.push(children.slice(i, i + batchOf));
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => ({
      readEntries: (ok) => ok(batches.shift() ?? []),
    }),
  };
};

const deps = (over = {}) => ({
  resolveFile: defaultFolderDeps.resolveFile,
  ...over,
});

test('普通展开：嵌套目录里的文件全部收集，无提示', async () => {
  const root = dirEntry('相册', [
    fileEntry('a.png', 100),
    fileEntry('b.png', 200),
    dirEntry('子目录', [fileEntry('c.jpg', 50)]),
  ]);
  const r = await collectFolderEntries(root, deps());
  assert.equal(r.files.length, 3);
  assert.deepEqual(r.files.map((f) => f.name), ['a.png', 'b.png', 'c.jpg']);
  assert.equal(r.notice, '');
});

test('readEntries 分页：多批读全（Chromium 每批 ≤100 条的坑）', async () => {
  const many = Array.from({ length: 250 }, (_, i) => fileEntry(`f${i}.txt`, 1));
  const r = await collectFolderEntries(dirEntry('big', many, 100), deps({ maxFiles: 300 }));
  assert.equal(r.files.length, 250);
  assert.equal(r.notice, '');
});

test('超个数上限：截到 maxFiles，提示「已收录前 N 个」不静默', async () => {
  const many = Array.from({ length: 5 }, (_, i) => fileEntry(`f${i}.txt`, 1));
  const r = await collectFolderEntries(dirEntry('d', many), deps({ maxFiles: 3 }));
  assert.equal(r.files.length, 3);
  assert.ok(r.notice.includes('已收录前 3 个'), r.notice);
  assert.ok(r.notice.includes('其余未加入'), r.notice);
  assert.ok(r.notice.includes('分批发送'), r.notice);
});

test('默认个数上限是 16', async () => {
  assert.equal(MAX_FOLDER_FILES, 16);
  const many = Array.from({ length: 18 }, (_, i) => fileEntry(`f${i}.txt`, 1));
  const r = await collectFolderEntries(dirEntry('d', many), deps());
  assert.equal(r.files.length, 16);
  assert.ok(r.notice.includes('前 16 个'), r.notice);
});

test('超总量上限：放不下的文件起截断，提示总量超限', async () => {
  const root = dirEntry('d', [fileEntry('big1', 60), fileEntry('big2', 60), fileEntry('small', 1)]);
  const r = await collectFolderEntries(root, deps({ maxTotalBytes: 100 }));
  assert.equal(r.files.length, 1); // big2 放不下即停，small 也不再收
  assert.equal(r.files[0].name, 'big1');
  assert.ok(r.notice.includes('总量超过'), r.notice);
  assert.ok(r.notice.includes('前 1 个'), r.notice);
});

test('默认总量上限 20MB；恰好装满不误报截断', async () => {
  assert.equal(MAX_FOLDER_TOTAL_BYTES, 20 << 20);
  const root = dirEntry('d', [fileEntry('a', MAX_FOLDER_TOTAL_BYTES), fileEntry('b', 1)]);
  const r = await collectFolderEntries(root, deps({ maxFiles: 4 }));
  assert.equal(r.files.length, 1);
  assert.ok(r.notice.includes('总量超过'), r.notice);
  const exact = dirEntry('d', [fileEntry('a', MAX_FOLDER_TOTAL_BYTES)]);
  const r2 = await collectFolderEntries(exact, deps());
  assert.equal(r2.files.length, 1);
  assert.equal(r2.notice, '');
});

test('空文件夹：提示「文件夹是空的」', async () => {
  const r = await collectFolderEntries(dirEntry('empty', []), deps());
  assert.equal(r.files.length, 0);
  assert.equal(r.notice, '文件夹是空的');
});

test('空文件夹（只有空子目录）同样算空', async () => {
  const r = await collectFolderEntries(dirEntry('d', [dirEntry('sub', [])]), deps());
  assert.equal(r.files.length, 0);
  assert.equal(r.notice, '文件夹是空的');
});

test('隐藏文件与隐藏目录都跳过（.git 不该被捞进来）', async () => {
  const root = dirEntry('proj', [
    fileEntry('visible.txt', 10),
    fileEntry('.hidden', 10),
    dirEntry('.git', [fileEntry('config', 10)]),
    dirEntry('src', [fileEntry('.env', 10), fileEntry('main.go', 10)]),
  ]);
  const r = await collectFolderEntries(root, deps());
  assert.deepEqual(r.files.map((f) => f.name), ['visible.txt', 'main.go']);
  assert.equal(r.notice, '');
});

test('个别文件读取失败：跳过该文件并在提示里说明，其余照收', async () => {
  const root = dirEntry('d', [fileEntry('ok1', 10), fileEntry('broken', 10), fileEntry('ok2', 10)]);
  const r = await collectFolderEntries(root, deps({
    resolveFile: async (e) => (e.name === 'broken' ? null : { name: e.name, size: 10 }),
  }));
  assert.deepEqual(r.files.map((f) => f.name), ['ok1', 'ok2']);
  assert.ok(r.notice.includes('1 个文件读取失败已跳过'), r.notice);
});

test('全部文件都读取失败：不静默，说明没有可添加的文件', async () => {
  const root = dirEntry('d', [fileEntry('a', 10), fileEntry('b', 10)]);
  const r = await collectFolderEntries(root, deps({ resolveFile: async () => null }));
  assert.equal(r.files.length, 0);
  assert.ok(r.notice.includes('读取失败'), r.notice);
});

test('超单文件上限的文件跳过并说明（不占总量预算、不整批失败）', async () => {
  const root = dirEntry('d', [fileEntry('huge', 500), fileEntry('ok', 10)]);
  const r = await collectFolderEntries(root, deps({ maxFileBytes: 100, maxTotalBytes: 1000 }));
  assert.deepEqual(r.files.map((f) => f.name), ['ok']);
  assert.ok(r.notice.includes('1 个文件超过单文件 20MB 上限已跳过'), r.notice);
});

test('多个截断/跳过原因合并成一条提示', async () => {
  const root = dirEntry('d', [fileEntry('.x', 1), fileEntry('huge', 500), fileEntry('a', 10), fileEntry('b', 10)]);
  const r = await collectFolderEntries(root, deps({ maxFileBytes: 100, maxFiles: 1 }));
  assert.equal(r.files.length, 1);
  assert.ok(r.notice.includes('已收录前 1 个'), r.notice);
  assert.ok(r.notice.includes('超过单文件'), r.notice);
  assert.ok(r.notice.includes('；'), `多原因应以「；」连接: ${r.notice}`);
});

test('defaultFolderDeps.resolveFile：entry.file 成功回调 → File，失败回调 → null', async () => {
  const ok = await defaultFolderDeps.resolveFile(fileEntry('a.png', 7));
  assert.equal(ok.size, 7);
  const bad = await defaultFolderDeps.resolveFile({
    isFile: true,
    isDirectory: false,
    name: 'a.png',
    file: (_ok, err) => err(new Error('gone')),
  });
  assert.equal(bad, null);
  const noApi = await defaultFolderDeps.resolveFile({ isFile: true, isDirectory: false, name: 'a.png' });
  assert.equal(noApi, null);
});

// ---- 相对路径（拖文件夹改造，2026-12）：内部文件按文件夹根的相对路径收集 ----

test('相对路径：嵌套文件按「目录/文件名」收集，与 files 一一对应', async () => {
  const root = dirEntry('项目', [
    fileEntry('README.md', 10),
    dirEntry('docs', [fileEntry('api.md', 10), dirEntry('api', [fileEntry('auth.md', 10)])]),
    dirEntry('assets', [fileEntry('logo.png', 10)]),
  ]);
  const r = await collectFolderEntries(root, deps());
  assert.equal(r.files.length, 4);
  assert.deepEqual(r.relPaths, [
    'README.md',
    'docs/api.md',
    'docs/api/auth.md',
    'assets/logo.png',
  ]);
  assert.equal(r.notice, '');
});

test('相对路径：截断时 relPaths 与 files 长度一致（平行数组）', async () => {
  const many = Array.from({ length: 5 }, (_, i) => fileEntry(`f${i}.txt`, 1));
  const r = await collectFolderEntries(dirEntry('d', many), deps({ maxFiles: 3 }));
  assert.equal(r.relPaths.length, 3);
  assert.deepEqual(r.relPaths, ['f0.txt', 'f1.txt', 'f2.txt']);
});

test('相对路径：空文件夹 relPaths 为空', async () => {
  const r = await collectFolderEntries(dirEntry('empty', []), deps());
  assert.deepEqual(r.relPaths, []);
});
