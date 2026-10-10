// 图片批次 B（前端 UI + 文件通道）的测试：
// ① 附件纯逻辑（shared/attachments.ts）：加入 / 超限拒绝 / 移除 / wire 组装；
// ② submit 门：只附件没文本也放行（routeSubmit 的 hasAttachments）；
// ③ 队列联动：条目携带附件（按引用不复制）、编辑装回的数据形状、
//    直接发送/自动发送把附件带给 sendWithOptions（drainQueueHead）；
// ④ 失败保留：optimisticUser failed 带原附件 → onSendFailed 钩子（App 装回输入框）；
// ⑤ 乐观气泡附件标记（pending 块的 atts 计数 → Block 渲染 [图片]×N）；
// ⑥ vision 禁用判定（visionBlockNotice 纯函数）；
// ⑦ Composer 静态渲染：队列条目附件标记可见、「+」入口随 readOnly 消失。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import {
  addPendingFiles,
  addPendingImages,
  attsFromPending,
  formatBytes,
  hasPendingAtts,
  removePending,
  visionBlockNotice,
} from '../src/shared/attachments.ts';
import { enqueueSend, routeSubmit } from '../src/shared/send-queue.ts';
import { drainQueueHead, handleAgentEvent } from '../src/shared/store.ts';
import { reduce } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';
import { SettingsProvider } from '../src/shared/settings.tsx';
import { AgentsProvider } from '../src/shared/agents.tsx';
import { Block } from '../src/components/thread/blocks/Block.tsx';
import { Composer } from '../src/components/composer/Composer.tsx';

// ---------- ① 附件纯逻辑 ----------

const img = (name = 'a.png', data = 'aGVsbG8=') => ({ mime: 'image/png', name, data });

test('① 选图加入附件区；png/jpeg/webp/gif 之外拒绝', () => {
  const r1 = addPendingImages([], [img()]);
  assert.equal(r1.images.length, 1);
  assert.equal(r1.notice, '');
  const r2 = addPendingImages(r1.images, [{ mime: 'image/bmp', name: 'x.bmp', data: 'aGk=' }]);
  assert.equal(r2.images.length, 1, '白名单外整批拒收');
  assert.ok(r2.notice.includes('不支持的图片类型'), r2.notice);
});

test('① 超限拒绝：单图 >5MB、图片总数 >4、单文件 >20MB、文件总数 >4', () => {
  // 5MB（base64 解码后）：7MB 的 base64 字符 ≈ 5.25MB 字节
  const big = { mime: 'image/png', name: 'big.png', data: 'A'.repeat(7 * 1024 * 1024) };
  const r = addPendingImages([], [big]);
  assert.ok(r.notice.includes('5MB'), r.notice);
  assert.equal(r.images.length, 0);
  // 第 5 张拒
  let imgs = [];
  for (let i = 0; i < 4; i++) imgs = addPendingImages(imgs, [img(`a${i}.png`)]).images;
  const fifth = addPendingImages(imgs, [img('e.png')]);
  assert.ok(fifth.notice.includes('最多 4 张'), fifth.notice);
  assert.equal(fifth.images.length, 4);
  // 单文件 20MB
  const bigFile = { name: 'big.bin', size: 20 * 1024 * 1024 + 1, data: 'A'.repeat(100) };
  const rf = addPendingFiles([], [bigFile]);
  assert.ok(rf.notice.includes('20MB'), rf.notice);
  // 第 5 个文件拒
  let files = [];
  for (let i = 0; i < 4; i++) files = addPendingFiles(files, [{ name: `f${i}.txt`, size: 1, data: 'aGk=' }]).files;
  const fifthF = addPendingFiles(files, [{ name: 'g.txt', size: 1, data: 'aGk=' }]);
  assert.ok(fifthF.notice.includes('最多 4 个'), fifthF.notice);
  assert.equal(fifthF.files.length, 4);
});

test('① 移除（id 不存在时引用不变）；hasPendingAtts；formatBytes', () => {
  const r = addPendingImages([], [img(), img('b.png')]);
  const [first] = r.images;
  const after = removePending(r.images, first.id);
  assert.equal(after.length, 1);
  assert.equal(removePending(r.images, 'nope'), r.images, '移除不存在的 id 原样返回');
  assert.equal(hasPendingAtts([], []), false);
  assert.equal(hasPendingAtts(r.images, []), true);
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(2048), '2 KB');
  assert.ok(formatBytes(5 * 1024 * 1024).includes('MB'));
});

test('① attsFromPending：wire 形状（mime/data、name/data），数组按引用', () => {
  const imgs = addPendingImages([], [img()]).images;
  const files = addPendingFiles([], [{ name: '报告.txt', size: 8, data: 'aGVsbG8=' }]).files;
  const atts = attsFromPending(imgs, files);
  assert.deepEqual(atts.images, [{ mime: 'image/png', data: 'aGVsbG8=' }]);
  assert.deepEqual(atts.files, [{ name: '报告.txt', data: 'aGVsbG8=' }]);
  assert.equal(atts.images[0].data, imgs[0].data, 'data 按引用共享（不复制 base64）');
});

// ---------- ② submit 门 ----------

test('② 只附件没文本也放行（routeSubmit 的 hasAttachments）；锁定/斜杠门不变', () => {
  const base = { text: '', busy: false, sending: false, locked: false, hasQueue: true, hasAttachments: true };
  assert.equal(routeSubmit(base), 'send', '只附件不打字 = 正常发送（后端以「[附件]」行充当正文）');
  assert.equal(routeSubmit({ ...base, busy: true }), 'queue', 'busy 时照样入队（带附件）');
  assert.equal(routeSubmit({ ...base, hasAttachments: false }), 'none', '没附件没文本照旧不发');
  assert.equal(routeSubmit({ ...base, locked: true }), 'none', '锁定视图不发');
  assert.equal(routeSubmit({ ...base, text: '/compact' }), 'none', '斜杠命令仍由面板接管');
});

// ---------- ③ 队列联动 ----------

test('③ 入队携带附件（引用同一数组，不复制）；drainQueueHead 把附件带给自动发送', () => {
  const images = [{ mime: 'image/png', data: 'aGVsbG8=' }];
  const files = [{ name: '报告.txt', data: 'aGVsbG8=' }];
  let q = enqueueSend([], 'a', '看看', { images, files });
  assert.equal(q[0].images, images, 'images 按引用入队');
  assert.equal(q[0].files, files, 'files 按引用入队');
  // 不带附件的条目不出现该键（wire 干净）
  let q2 = enqueueSend([], 'b', '纯文本');
  assert.equal(q2[0].images, undefined);
  assert.equal(q2[0].files, undefined);

  // 自动发送：队首的附件透传给 autoSend 出口（App 的 sendWithOptions 补请求级参数）
  const c = makeController();
  c.currentId = 's1';
  c.sendQueues = { s1: q };
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: true }, c);
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: false }, c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: '看看', images, files }], '自动发送带附件（同一引用）');
  assert.deepEqual(c.sendQueues.s1, [], '发出即移除');
  // 空队列再 drain：无副作用
  drainQueueHead(c, 's1');
  assert.equal(c.sent.length, 1);
});

test('③ 「直接发送」（send-now 路径由 App 组装）——队列条目保留附件字段供取用', () => {
  // App 的 handleQueueSend 从 sendQueues 按 id 取条目后调 sendWithOptions(text, atts)；
  // 这里钉住数据形状：条目上的 images/files 与 SendOptions.images/files 同形。
  const images = [{ mime: 'image/jpeg', data: 'eGw=' }];
  const q = enqueueSend([], 'a', '看看', { images });
  assert.deepEqual(q[0].images, images);
  assert.equal(q[0].images[0].mime, 'image/jpeg');
});

// ---------- ④ 失败保留 ----------

test('④ 发送失败：failed 乐观事件带原附件 → onSendFailed 钩子（装回输入框附件区）', () => {
  const atts = { images: [{ mime: 'image/png', data: 'aGVsbG8=' }], files: [{ name: 'a.txt', data: 'aGk=' }] };
  const c = makeController();
  handleAgentEvent({ type: 'optimisticUser', sessionId: 's1', text: '看图', failed: true, atts }, c);
  assert.deepEqual(c.failedAtts, [{ sessionId: 's1', atts }], '附件经 onSendFailed 钩子回滚');
  // 无附件的失败不发回滚（文本本来就不回填，没有附件可装）
  const c2 = makeController();
  handleAgentEvent({ type: 'optimisticUser', sessionId: 's1', text: '看图', failed: true }, c2);
  assert.deepEqual(c2.failedAtts, []);
});

// ---------- ⑤ 乐观气泡附件标记 ----------

test('⑤ 乐观气泡带 atts 计数（[图片]×N [文件]×M），无附件不出现该键', () => {
  let s = { ...initial, currentId: 's1' };
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '看图', atts: { images: [{ mime: 'image/png', data: 'aA==' }], files: [{ name: 'a.txt', data: 'aA==' }] } });
  const pending = s.blocks.find((b) => b.kind === 'user' && b.pending);
  assert.deepEqual(pending.atts, { images: 1, files: 1 }, 'pending 块只留计数，不留 base64');
  s = reduce(s, { type: 'optimisticUser', sessionId: 's1', text: '纯文本' });
  const plain = s.blocks.filter((b) => b.kind === 'user' && b.pending)[1];
  assert.equal(plain.atts, undefined, '无附件的块不出现 atts 键');
});

test('⑤ Block 静态渲染：pending 气泡显示 [图片]×N [文件]×M 标记', () => {
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(Block, {
      block: { kind: 'user', uid: 1, text: '看看这两张', pending: true, atts: { images: 2, files: 1 } },
      onConfirm: () => {},
    }),
  ));
  assert.ok(html.includes('[图片]×2'), html);
  assert.ok(html.includes('[文件]×1'), html);
});

// ---------- ⑥ vision 禁用判定 ----------

test('⑥ visionBlockNotice：声明 false 才禁用；未知/查不到不禁用', () => {
  const models = [
    { id: 'm1', vision: false },
    { id: 'm2', vision: true },
    { id: 'm3' }, // 未声明 = 未知
  ];
  assert.ok(visionBlockNotice('m1', models).includes('当前模型不支持视觉：m1'));
  assert.equal(visionBlockNotice('m2', models), null);
  assert.equal(visionBlockNotice('m3', models), null, '未声明 ≠ 不支持（后端兜底）');
  assert.equal(visionBlockNotice('ghost', models), null, '注册表查不到不禁用');
  assert.equal(visionBlockNotice('', models), null, '模型未知（新会话）不禁用');
});

// ---------- ⑦ Composer 静态渲染 ----------

const queueAtts = {
  images: [{ mime: 'image/png', data: 'aGVsbG8=' }],
  files: [{ name: '季度报告.pdf', data: 'aGVsbG8=' }],
};

test('⑦ Composer：队列条目的附件标记可见（缩略图容器 + 文件名 chip）；「+」入口存在', () => {
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(AgentsProvider, { source: fakeSource() },
      createElement(Composer, {
        busy: false,
        onSend: () => {},
        onCancel: () => {},
        queue: [{ id: 'a', text: '排队的一条', ...queueAtts }],
        onQueue: () => {},
        onQueueEdit: () => {},
        onQueueDelete: () => {},
        onQueueSend: () => {},
      }),
    ),
  ));
  assert.ok(html.includes('排队的一条'), html);
  assert.ok(html.includes('send-queue-atts'), '队列条目应有附件标记区: ' + html);
  assert.ok(html.includes('季度报告.pdf'), '文件名 chip 应可见');
  assert.ok(html.includes('att-add'), '「+」添加入口应存在');
  assert.ok(html.includes('添加图片或文件'), '「+」按钮应有中文 title');
});

test('⑦ Composer：只读视图（子会话页）不渲染「+」入口', () => {
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(AgentsProvider, { source: fakeSource() },
      createElement(Composer, {
        busy: false,
        readOnly: true,
        onSend: () => {},
        onCancel: () => {},
      }),
    ),
  ));
  assert.ok(!html.includes('att-add'), '只读视图不该有「+」入口: ' + html);
});

// ---------- 测试脚手架 ----------

function makeController() {
  return {
    currentId: '',
    sessionStates: {},
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    revisions: 0,
    sent: [],
    failedAtts: [],
    operationError: null,
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setOperationError(message) { this.operationError = message; },
    bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend(sessionId, text, atts) { this.sent.push({ sessionId, text, ...(atts ?? {}) }); },
    sendFailed(sessionId, atts) { this.failedAtts.push({ sessionId, atts }); },
    reset() {},
  };
}

/** SettingsProvider/AgentsProvider 静态渲染所需的最小 source（渲染期只读
 *  modelAdmin/agentAdmin 的存在性，不调方法——useEffect 在 SSR 不执行）。 */
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
