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
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import {
  addPendingFiles,
  addPendingImages,
  attsFromPending,
  formatBytes,
  groupFolderFiles,
  groupMetaFolders,
  hasPendingAtts,
  removePending,
  visionBlockNotice,
  attachmentsFromMessage,
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

test('⑦b Composer：暂存附件区在输入卡内部（topContent 槽位）——64px 方格 + 文件卡行 + 移除钮', () => {
  // initialDraft 装两张图 + 一个散文件：暂存区渲染进 .pi 卡内、pi-row 之前；
  // 类名钉住新形态（att-thumbs/att-thumb/att-file-card/att-remove），DSH 对齐。
  const html = renderToStaticMarkup(createElement(
    SettingsProvider,
    { source: fakeSource() },
    createElement(AgentsProvider, { source: fakeSource() },
      createElement(Composer, {
        busy: false,
        onSend: () => {},
        onCancel: () => {},
        initialDraft: {
          text: '',
          images: [
            { id: 'im1', mime: 'image/png', name: 'shot1.png', data: 'aGVsbG8=' },
            { id: 'im2', mime: 'image/png', name: 'shot2.png', data: 'aGVsbG8=' },
          ],
          files: [{ id: 'f1', mime: '', name: '季度报告.pdf', size: 2048, file: undefined }],
        },
      }),
    ),
  ));
  const piAt = html.indexOf('class="pi"');
  const previewAt = html.indexOf('att-preview');
  const rowAt = html.indexOf('class="pi-row"');
  assert.ok(piAt >= 0 && rowAt >= 0, '输入卡与输入行应存在');
  assert.ok(previewAt > piAt && previewAt < rowAt, '附件预览区应在 .pi 卡内、pi-row 之前');
  assert.ok(html.includes('att-thumbs'), '图片网格容器应存在');
  assert.equal((html.match(/class="att-thumb"/g) || []).length, 2, '两张图各一格（64px 方格形态）');
  assert.ok(html.includes('att-file-card'), '散文件应是文件卡行');
  assert.ok(html.includes('季度报告.pdf'), '文件名应可见');
  assert.ok(html.includes('att-remove'), '移除钮应存在');
});

test('⑦c CSS：暂存区 DSH 形态的源码钉子——64px 方格 radius16 细边框、文件卡 240×64、+钮 32px 正圆无边框、移除钮悬停显、发送钮 32px 灰底禁用', () => {
  const css = readFileSync('src/styles/composer.css', 'utf8');
  const rule = (selector) => {
    const at = css.indexOf(selector);
    if (at < 0) return null;
    const start = css.indexOf('{', at);
    return css.slice(start + 1, css.indexOf('}', start));
  };
  const thumb = rule('.att-thumb {');
  assert.ok(thumb?.includes('width: 64px') && thumb.includes('height: 64px'), '缩略图 64px 方格（DSH thumbnail）: ' + (thumb ?? ''));
  assert.ok(thumb?.includes('border-radius: 16px'), '方格 radius 16（DSH 语义）: ' + (thumb ?? ''));
  const thumbImg = rule('.att-thumb img {');
  assert.ok(thumbImg?.includes('border: 1px solid'), '缩略图细边框（DSH .5px 的 1px 映射）: ' + (thumbImg ?? ''));
  assert.ok(thumbImg?.includes('cursor: zoom-in'), '缩略图 zoom-in 光标: ' + (thumbImg ?? ''));
  const fileCard = rule('.att-file-card {');
  assert.ok(fileCard?.includes('width: 240px') && fileCard.includes('height: 64px'), '文件卡 240×64（DSH FileCard）: ' + (fileCard ?? ''));
  assert.ok(fileCard?.includes('border-radius: 16px'), '文件卡 radius 16: ' + (fileCard ?? ''));
  const remove = rule('.att-remove {');
  assert.ok(remove?.includes('opacity: 0'), '移除钮默认隐藏（DSH 悬停显）: ' + (remove ?? ''));
  assert.ok(remove?.includes('top: 4px') && remove.includes('right: 4px'), '移除钮卡内 4px（DSH 位置）: ' + (remove ?? ''));
  const hover = rule('.att-thumb:hover .att-remove,\n.att-file-card:hover .att-remove,\n.att-remove:focus-visible {');
  assert.ok(hover?.includes('opacity: 1'), 'hover/焦点显移除钮: ' + (hover ?? ''));
  const coarse = css.includes('@media (pointer: coarse) {\n  .att-remove {\n    opacity: 1;\n  }\n}');
  assert.ok(coarse, '触屏恒显移除钮（DSH pointer:coarse 规则）');
  const add = rule('.att-add {');
  assert.ok(add?.includes('width: 32px') && add.includes('height: 32px'), '+按钮 32px 正圆: ' + (add ?? ''));
  assert.ok(add?.includes('background: transparent') && !add.includes('border: 1px'), '+按钮无边框透明底: ' + (add ?? ''));
  assert.ok(add?.includes('border-radius: 999px'), '+按钮正圆: ' + (add ?? ''));
  const send = rule('\n.send-btn {');
  assert.ok(send?.includes('width: 32px') && send.includes('height: 32px'), '发送钮 32px: ' + (send ?? ''));
  const sendDisabled = rule('.send-btn:disabled {');
  assert.ok(sendDisabled?.includes('background: var(--border-strong)'), '禁用态实色灰底: ' + (sendDisabled ?? ''));
  const pi = rule('.pi {');
  assert.ok(pi?.includes('border-radius: 20px'), '输入卡大圆角: ' + (pi ?? ''));
  const preview = rule('.att-preview {');
  assert.ok(preview?.includes('padding: 12px 16px 0'), '附件区卡内内距（与 textarea 左右对齐）: ' + (preview ?? ''));
});

// ---------- ⑦d 「+」按钮与首行文字的对齐钉子（2026-12 用户截图「+ 偏上」） ----------
// 偏移根因（盒模型，单行态）：.pi-row 是 flex-end（贴底对齐盒底，不是文字基线）；
// textarea min-height:44px 顶住（自增高 scrollHeight≈41 < 44），首行文字中心
// 距盒顶 = padding-top 14 + 21/2 = 24.5px（font 14 × line-height 1.5 = 21）；
// 按钮中心距盒底 = margin-bottom + 32/2。对齐 ⇔ 44 − (mb + 16) = 24.5 ⇔ mb = 3.5。
// 这里按同一算式从 CSS 里解析出各值做断言——将来改 padding/行高/按钮尺寸时，
// 断言会用新值重算并把漏改的 margin-bottom 指出来。
test('⑦d CSS：单行态「+」按钮中心与 textarea 首行文字中心重合（margin-bottom 补偿）', () => {
  const css = readFileSync('src/styles/composer.css', 'utf8');
  const rule = (selector) => {
    const at = css.indexOf(selector);
    if (at < 0) return null;
    const start = css.indexOf('{', at);
    return css.slice(start + 1, css.indexOf('}', start));
  };
  const num = (body, prop) => {
    const m = body?.match(new RegExp(prop + ':\\s*([\\d.]+)px'));
    return m ? parseFloat(m[1]) : null;
  };
  const ta = rule('.pi textarea.piInput {');
  const add = rule('.att-add {');
  assert.ok(ta && add, '两条规则都在 composer.css');
  const pt = num(ta, 'padding-top') ?? num(ta, 'padding'); // padding 简写取第一个值（上）
  const minH = num(ta, 'min-height');
  const fs_ = num(ta, 'font-size');
  const btn = num(add, 'height');
  const mb = num(add, 'margin-bottom') ?? (() => { // margin 简写取第三个值（下；上值可能是无单位的 0）
    const m = add?.match(/margin:\s*([\d.]+)(?:px)?\s+[\d.]+px\s+([\d.]+)px/);
    return m ? parseFloat(m[2]) : null;
  })();
  assert.ok([pt, minH, fs_, btn, mb].every((v) => v !== null), `盒模型各值可解析: pt=${pt} minH=${minH} fs=${fs_} btn=${btn} mb=${mb}`);
  const rowH = Math.max(minH, fs_ * 1.5 + pt); // 单行态：自增高 scrollHeight = 行高 + 上 padding（无下 padding 参与）
  const lineCenter = pt + (fs_ * 1.5) / 2; // 首行文字中心距盒顶
  const btnCenter = rowH - mb - btn / 2; // 贴底对齐下按钮中心距盒顶
  assert.ok(
    Math.abs(btnCenter - lineCenter) <= 0.5,
    `单行态按钮中心(${btnCenter}px) 应≈首行文字中心(${lineCenter}px)——margin-bottom 需 = ${rowH - lineCenter - btn / 2}px`,
  );
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

// ---------- 文件夹分组（拖文件夹改造，2026-12）----------

test('④ groupFolderFiles：按 group 分组成文件夹卡，散文件留 loose', () => {
  const files = addPendingFiles([], [
    { name: 'auth.md', size: 30, group: 'docs', relPath: 'docs/api/auth.md' },
    { name: 'api.md', size: 20, group: 'docs', relPath: 'docs/api.md' },
    { name: 'loose.txt', size: 5 },
    { name: 'logo.png', size: 10, group: 'assets', relPath: 'assets/logo.png' },
  ]).files;
  const { groups, loose } = groupFolderFiles(files);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].name, 'docs');
  assert.deepEqual(groups[0].files.map((f) => f.relPath), ['docs/api/auth.md', 'docs/api.md']);
  assert.equal(groups[0].totalBytes, 50);
  assert.equal(groups[1].name, 'assets');
  assert.equal(loose.length, 1);
  assert.equal(loose[0].name, 'loose.txt');
});

test('④ groupMetaFolders：消息侧 AttachmentMeta 按 relPath 归组；老消息条目留 loose', () => {
  const { groups, loose } = groupMetaFolders([
    { path: 's/1.md', name: 'docs/api/auth.md', kind: 'file', group: 'docs', relPath: 'docs/api/auth.md' },
    { path: 's/2.txt', name: '旧文件.txt', kind: 'file' },
    { path: 's/3.md', name: 'assets/logo.png', kind: 'file', group: 'assets', relPath: 'assets/logo.png' },
  ]);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].name, 'docs');
  assert.equal(groups[0].files[0].relPath, 'docs/api/auth.md');
  assert.equal(loose.length, 1, '老消息（无 relPath）不进文件夹卡');
});

test('⑤ attachmentsFromMessage：Name 含「/」推导 group/relPath；老消息不带这两个键', () => {
  const meta = attachmentsFromMessage({
    attachments: [
      { path: 's/a.md', name: 'docs/api/auth.md', size: 3, kind: 'file' },
      { path: 's/b.txt', name: '旧.txt', size: 1, kind: 'file' },
    ],
    content: '',
  });
  assert.equal(meta[0].group, 'docs');
  assert.equal(meta[0].relPath, 'docs/api/auth.md');
  assert.equal(meta[1].group, undefined);
  assert.equal(meta[1].relPath, undefined);
});
