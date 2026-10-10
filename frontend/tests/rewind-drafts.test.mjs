// 撤回的回填时机（rewindThenRestore，2026-10-10）与草稿的按会话记账
// （shared/session-drafts.ts）的契约钉子。
//
// 为什么值得单独一个文件：
// ① 撤回「先 rewind 成功才回填输入框」的顺序错了不会让任何东西编译失败——只会
//    在后端拒绝（配对校验）时把原文留在输入框、时间线却复原成没撤过的样子
//    （「到了输入框但会话里还在」，两步不一致）。App 组件依赖太重钉不住，
//    成败分流抽成纯函数在这里钉。
// ② 草稿按会话隔离的时机契约（切走保留/切回还原/失败回填落原会话键）同理：
//    写错一个键名就是 A 的半截输入串到 B，纯函数在这里钉住。
import test from 'node:test';
import assert from 'node:assert/strict';
import { rewindThenRestore } from '../src/shared/blocks.ts';
import {
  clearDraft,
  draftOf,
  emptyDraft,
  pendingFromAtts,
  writeDraft,
  writeDraftAtts,
  writeDraftText,
} from '../src/shared/session-drafts.ts';

// ---------- ① rewindThenRestore：成功才回填，失败只报错 ----------

test('rewind 成功 → 原文注入输入框（inject 恰好一次，文本是撤回计划给的）', async () => {
  const injected = [];
  const reported = [];
  let resolveRewind;
  const rewind = () => new Promise((res) => { resolveRewind = res; });
  rewindThenRestore({ rewind, text: '被撤回的那条', inject: (t) => injected.push(t), report: (m) => reported.push(m) });
  assert.deepEqual(injected, [], 'rewind 还没成功就不能回填（两步一致）');
  resolveRewind({ removed: 3 });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(injected, ['被撤回的那条']);
  assert.deepEqual(reported, [], '成功不报错');
});

test('rewind 被拒 → 不注入 draft、报错照旧（时间线由 ws 重放历史复原，这里不碰）', async () => {
  const injected = [];
  const reported = [];
  let rejectRewind;
  const rewind = () => new Promise((_, rej) => { rejectRewind = rej; });
  rewindThenRestore({ rewind, text: '被撤回的那条', inject: (t) => injected.push(t), report: (m) => reported.push(m) });
  rejectRewind(new Error('撤回锚点不是配对平衡的切点'));
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(injected, [], '失败时文本根本没进输入框——不再误回填');
  assert.equal(reported.length, 1);
  assert.match(reported[0], /^撤回失败: 撤回锚点不是配对平衡的切点$/);
});

test('rewind 被拒（非 Error 抛出）→ 报错不炸，文本照样不进输入框', async () => {
  const injected = [];
  const reported = [];
  rewindThenRestore({
    rewind: () => Promise.reject('字符串异常'),
    text: 'x',
    inject: (t) => injected.push(t),
    report: (m) => reported.push(m),
  });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(injected, []);
  assert.match(reported[0], /^撤回失败: 字符串异常$/);
});

// ---------- ② session-drafts：per-session 记账的存取时机 ----------

test('draftOf：没记过账 = 空草稿（B 的输入框是 B 自己的，没有就是空）', () => {
  const drafts = writeDraft({}, 'A', { text: 'abc', images: [], files: [] });
  assert.equal(draftOf(drafts, 'A').text, 'abc');
  assert.deepEqual(draftOf(drafts, 'B'), emptyDraft(), 'B 的输入框为空');
});

test('切会话语义：A 的半截输入切走保留、切回还在；写 B 不影响 A', () => {
  let drafts = {};
  // A 输入「abc」（Composer 实时回写）
  drafts = writeDraft(drafts, 'A', { text: 'abc', images: [], files: [] });
  // 切到 B：B 是空的；A 的还在
  assert.deepEqual(draftOf(drafts, 'B'), emptyDraft());
  assert.equal(draftOf(drafts, 'A').text, 'abc');
  // B 输入「xyz」
  drafts = writeDraftText(drafts, 'B', 'xyz');
  assert.equal(draftOf(drafts, 'B').text, 'xyz');
  assert.equal(draftOf(drafts, 'A').text, 'abc', '写 B 不影响 A');
  // 切回 A：「abc」还在
  assert.equal(draftOf(drafts, 'A').text, 'abc');
});

test('writeDraftText 保留该会话已有的附件；同文本不换对象（打字高频路径）', () => {
  const atts = pendingFromAtts({ images: [{ mime: 'image/png', data: 'AAAA' }], files: [] });
  let drafts = writeDraft({}, 'A', { text: 'a', images: atts.images, files: atts.files });
  const before = draftOf(drafts, 'A');
  drafts = writeDraftText(drafts, 'A', 'ab');
  const after = draftOf(drafts, 'A');
  assert.equal(after.text, 'ab');
  assert.equal(after.images, before.images, '附件引用不变（只动文本）');
  assert.equal(writeDraftText(drafts, 'A', 'ab'), drafts, '无变化返回原对象');
});

test('writeDraftAtts：换附件、保留文本；atts 省略 = 原样返回（注入草稿不带附件不清暂存区）', () => {
  let drafts = writeDraft({}, 'A', { text: '改到一半', images: [], files: [] });
  drafts = writeDraftAtts(drafts, 'A', { images: [{ mime: 'image/png', data: 'BB' }], files: [{ name: 'a.txt', data: 'CC' }] });
  const d = draftOf(drafts, 'A');
  assert.equal(d.text, '改到一半', '失败回填不碰文本（用户可能已经在重新打字）');
  assert.equal(d.images.length, 1);
  assert.equal(d.files.length, 1);
  assert.equal(writeDraftAtts(drafts, 'A'), drafts, '省略 atts = 不动');
});

test('writeDraftAtts 给别的会话写附件也不串：失败回填落在原会话键', () => {
  let drafts = writeDraft({}, 'A', { text: '发出去的', images: [], files: [] });
  // 发送失败发生在 A，但用户已切到 B（当前看的是 B）
  drafts = writeDraftAtts(drafts, 'A', { images: [{ mime: 'image/jpeg', data: 'DD' }], files: [] });
  assert.equal(draftOf(drafts, 'A').images.length, 1, '落在原会话 A 的键上');
  assert.deepEqual(draftOf(drafts, 'B'), emptyDraft(), 'B 的输入框不受牵连');
  // 切回 A：文本与附件都还原
  const back = draftOf(drafts, 'A');
  assert.equal(back.text, '发出去的');
  assert.equal(back.images[0].mime, 'image/jpeg');
});

test('clearDraft：发送成功后当前会话归零；别的会话不受影响', () => {
  let drafts = {};
  drafts = writeDraft(drafts, 'A', { text: 'abc', images: [], files: [] });
  drafts = writeDraft(drafts, 'B', { text: 'xyz', images: [], files: [] });
  drafts = clearDraft(drafts, 'A');
  assert.deepEqual(draftOf(drafts, 'A'), emptyDraft());
  assert.equal(draftOf(drafts, 'B').text, 'xyz');
});

test('pendingFromAtts：SendAttachments → Composer 暂存形状（id 单调、字段齐全）', () => {
  const a = pendingFromAtts({
    images: [{ mime: 'image/png', data: 'AAAA' }, { mime: 'image/jpeg', data: 'BB' }],
    files: [{ name: '报告.txt', data: 'CC' }],
  });
  assert.equal(a.images.length, 2);
  assert.deepEqual(a.images[0], { id: a.images[0].id, mime: 'image/png', name: '', data: 'AAAA' });
  assert.match(a.images[0].id, /^restore-\d+-0$/);
  assert.notEqual(a.images[0].id, a.images[1].id, '同批 id 不重复（React key）');
  assert.deepEqual(a.files[0], { id: a.files[0].id, name: '报告.txt', size: 0, data: 'CC' });
  // 再回填一批：id 不与上一批撞（同批附件重复回填也要新 id）
  const b = pendingFromAtts({ images: [{ mime: 'image/png', data: 'AA' }], files: [] });
  assert.notEqual(a.images[0].id, b.images[0].id, '跨批次 id 单调递增');
});
