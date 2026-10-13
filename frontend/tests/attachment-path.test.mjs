// 附件 path 形态（2026-12 用户拍板：本地路径优先，base64 兜底）的发送侧钉子：
// ① staging 双轨（stageFile）：有真实路径 → 记 path 不读内容；无路径 → 走
//    base64 读入（行为与原先一致）；
// ② 暂存纯逻辑双形态（addPendingImages/addPendingFiles/attsFromPending）：
//    path 条目直通、大小校验用 File.size；
// ③ wire 组装（wireImage/wireFile）：path 形态只带路径不带 data，**file 对象
//    绝不上 wire**；base64 形态逐字节同旧形状（老后端兼容）；
// ④ 降级链路（isPathReadError + readAttsAsBase64）：后端「本地文件读取失败」
//    才降级；用暂存时留下的 File 对象读 base64 重发；File 缺失报人话错误；
//    无 path 条目原样保留（混合消息降级后仍是同一批附件）；
// ⑤ 失败回填（pendingFromAtts）：path/file 保留，降级凭据不丢。
import test from 'node:test';
import assert from 'node:assert/strict';

import { stageFile } from '../src/shared/attachment-read.ts';
import {
  addPendingFiles,
  addPendingImages,
  attsFromPending,
} from '../src/shared/attachments.ts';
import {
  hasPathAtts,
  isPathReadError,
  readAttsAsBase64,
  wireFile,
  wireImage,
} from '../src/shared/attachment-send.ts';
import { pendingFromAtts } from '../src/shared/session-drafts.ts';

const fileOf = (name, content, type = 'text/plain') => new File([content], name, { type });
const png = (name = 'shot.png', content = 'PNGDATA') => fileOf(name, content, 'image/png');

// ---------- ① staging 双轨 ----------

test('① stageFile：有真实路径 → 记 path，不读内容（read 不被调）', async () => {
  let readCalls = 0;
  const f = png();
  const staged = await stageFile(f, {
    getPath: () => 'C:\\Users\\x\\shot.png',
    read: async () => { readCalls++; return '不该被读'; },
  });
  assert.equal(staged.path, 'C:\\Users\\x\\shot.png');
  assert.equal(staged.data, null, '路径形态不读内容');
  assert.equal(staged.file, f, 'File 对象随行（降级凭据）');
  assert.equal(readCalls, 0, '路径形态不触发 base64 读入');
});

test('① stageFile：无路径（虚拟文件/浏览器）→ 走 base64 读入，path 为空', async () => {
  const f = png('paste.png');
  const staged = await stageFile(f, {
    getPath: () => null,
    read: async () => 'UORWRFdBVEE=',
  });
  assert.equal(staged.path, null);
  assert.equal(staged.data, 'UORWRFdBVEE=');
  assert.equal(staged.file, f);
});

// ---------- ② 暂存纯逻辑双形态 ----------

test('② addPending*：path 条目直通（data 空串、path/file/size 保留）', () => {
  const f = png();
  const r = addPendingImages([], [{ mime: 'image/png', name: 'shot.png', path: 'C:\\x\\shot.png', file: f, size: 7 }]);
  assert.equal(r.notice, '');
  assert.equal(r.images.length, 1);
  assert.equal(r.images[0].path, 'C:\\x\\shot.png');
  assert.equal(r.images[0].file, f);
  assert.equal(r.images[0].size, 7);
  assert.equal(r.images[0].data, '', '路径形态 data 为空串');

  const rf = addPendingFiles([], [{ name: 'doc.txt', size: 12, path: 'C:\\x\\doc.txt', file: fileOf('doc.txt', 'x') }]);
  assert.equal(rf.files[0].path, 'C:\\x\\doc.txt');
  assert.ok(rf.files[0].file instanceof File, 'file 随行');
  assert.equal(rf.files[0].data, '');
});

test('② addPendingImages：path 形态的大小校验用 File.size（超 5MB 拒）', () => {
  const f = png('big.png');
  const r = addPendingImages([], [{ mime: 'image/png', name: 'big.png', path: 'C:\\big.png', file: f, size: 6 * 1024 * 1024 }]);
  assert.ok(r.notice.includes('5MB'), r.notice);
  assert.equal(r.images.length, 0);
});

test('② base64 形态行为不变（老路径回归）：data 校验、加归属 id', () => {
  const r = addPendingImages([], [{ mime: 'image/png', name: 'a.png', data: 'aGVsbG8=' }]);
  assert.equal(r.images.length, 1);
  assert.equal(r.images[0].data, 'aGVsbG8=');
  assert.equal(r.images[0].path, undefined);
});

test('② attsFromPending：双形态直通（path 条目带 path/file，base64 条目同旧形状）', () => {
  const f = png();
  const imgs = addPendingImages([], [
    { mime: 'image/png', name: 'shot.png', path: 'C:\\x\\shot.png', file: f, size: 7 },
    { mime: 'image/jpeg', name: 'b.jpg', data: 'aGk=' },
  ]).images;
  const files = addPendingFiles([], [
    { name: 'doc.txt', size: 3, path: 'C:\\x\\doc.txt', file: fileOf('doc.txt', 'x') },
    { name: 'inline.txt', size: 3, data: 'aGk=' },
  ]).files;
  const atts = attsFromPending(imgs, files);
  assert.equal(atts.images[0].path, 'C:\\x\\shot.png');
  assert.equal(atts.images[0].file, f);
  assert.equal(atts.images[0].data, '');
  assert.equal(atts.images[1].data, 'aGk=');
  assert.equal(atts.images[1].path, undefined);
  assert.equal(atts.files[0].path, 'C:\\x\\doc.txt');
  assert.equal(atts.files[1].data, 'aGk=');
});

// ---------- ③ wire 组装 ----------

test('③ wireImage：path 形态只带 path/mime（data 绝不上 wire），file 被剥掉', () => {
  const f = png();
  const w = wireImage({ mime: 'image/png', data: '', path: 'C:\\x\\shot.png', file: f, name: 'shot.png' });
  assert.deepEqual(w, { mime: 'image/png', path: 'C:\\x\\shot.png', name: 'shot.png' });
});

test('③ wireImage：base64 形态逐字节同旧形状（老后端兼容）', () => {
  const w = wireImage({ mime: 'image/png', data: 'aGVsbG8=' });
  assert.deepEqual(w, { mime: 'image/png', data: 'aGVsbG8=' });
});

test('③ wireFile：path 形态只带 name/path；base64 形态带 name/data', () => {
  assert.deepEqual(wireFile({ name: 'doc.txt', data: '', path: 'C:\\x\\doc.txt', file: fileOf('d', 'x') }), { name: 'doc.txt', path: 'C:\\x\\doc.txt' });
  assert.deepEqual(wireFile({ name: 'inline.txt', data: 'aGk=' }), { name: 'inline.txt', data: 'aGk=' });
});

test('③ hasPathAtts：任一条目带 path 即 true；纯 base64 为 false（不进降级分支）', () => {
  assert.equal(hasPathAtts([{ mime: 'image/png', data: 'x', path: 'C:\\a.png' }], undefined), true);
  assert.equal(hasPathAtts(undefined, [{ name: 'a', data: 'x', path: 'C:\\a.txt' }]), true);
  assert.equal(hasPathAtts([{ mime: 'image/png', data: 'x' }], [{ name: 'a', data: 'x' }]), false);
  assert.equal(hasPathAtts(undefined, undefined), false);
});

// ---------- ④ 降级链路 ----------

test('④ isPathReadError：只认后端「本地文件读取失败」特征串', () => {
  assert.equal(isPathReadError('第 1 个文件: 本地文件读取失败：C:\\gone.txt（文件不存在或无法访问）'), true);
  assert.equal(isPathReadError('第 1 张图片: 本地文件读取失败：/tmp/x.png（no such file）'), true);
  assert.equal(isPathReadError('一条消息最多带 4 个文件（收到 5 个）'), false);
  assert.equal(isPathReadError('当前模型不支持视觉（m1 未声明 vision 能力）'), false, '别的参数错误不降级');
  assert.equal(isPathReadError('发送失败: connection closed'), false);
});

test('④ readAttsAsBase64：path 条目用 File 读 base64（降级重发的载荷），无 path 条目原样保留', async () => {
  const f = png('shot.png', 'PNGDATA');
  const reads = [];
  const out = await readAttsAsBase64(
    [{ mime: 'image/png', data: '', path: 'C:\\x\\shot.png', file: f, name: 'shot.png' }],
    [
      { name: 'doc.txt', data: '', path: 'C:\\x\\doc.txt', file: fileOf('doc.txt', 'x') },
      { name: 'inline.txt', data: 'aGk=' },
    ],
    { read: async (file) => { reads.push(file.name); return 'ZGVncmFkZWQ='; } },
  );
  assert.deepEqual(reads, ['shot.png', 'doc.txt']);
  assert.deepEqual(out.images, [{ mime: 'image/png', data: 'ZGVncmFkZWQ=', name: 'shot.png' }]);
  assert.equal(out.files[0].data, 'ZGVncmFkZWQ=');
  assert.equal(out.files[0].path, undefined, '降级后不再是 path 形态');
  assert.deepEqual(out.files[1], { name: 'inline.txt', data: 'aGk=' }, 'base64 条目原样保留');
});

test('④ readAttsAsBase64：File 缺失（降级凭据丢了）→ 人话错误，不静默漏发', async () => {
  await assert.rejects(
    () => readAttsAsBase64([{ mime: 'image/png', data: '', path: 'C:\\x.png' }], undefined, { read: async () => 'x' }),
    /文件对象已不可用/,
  );
  await assert.rejects(
    () => readAttsAsBase64(undefined, [{ name: 'a.txt', data: '', path: 'C:\\a.txt' }], { read: async () => 'x' }),
    /文件对象已不可用/,
  );
});

test('④ readAttsAsBase64：File 读不出来（快照与真实路径都没了）→ 整批失败', async () => {
  const f = png();
  await assert.rejects(
    () => readAttsAsBase64([{ mime: 'image/png', data: '', path: 'C:\\x.png', file: f }], undefined, { read: async () => { throw new Error('shot.png：文件内容读不出来'); } }),
    /读不出来/,
  );
});

// ---------- ⑤ 失败回填 ----------

test('⑤ pendingFromAtts：path/file 保留（装回附件区后重发仍走 path 形态）', () => {
  const f = png();
  const pending = pendingFromAtts({
    images: [{ mime: 'image/png', data: '', path: 'C:\\x\\shot.png', file: f, name: 'shot.png' }],
    files: [{ name: 'doc.txt', data: '', path: 'C:\\x\\doc.txt', file: fileOf('d', 'x') }],
  });
  assert.equal(pending.images[0].path, 'C:\\x\\shot.png');
  assert.equal(pending.images[0].file, f);
  assert.equal(pending.images[0].name, 'shot.png');
  assert.equal(pending.files[0].path, 'C:\\x\\doc.txt');
  assert.ok(pending.files[0].file instanceof File, 'file 随行');
  // base64 形态的回填行为不变
  const b64 = pendingFromAtts({ images: [{ mime: 'image/png', data: 'aGk=' }], files: [] });
  assert.equal(b64.images[0].data, 'aGk=');
  assert.equal(b64.images[0].path, undefined);
});
