// 附件展示通道（2026-12 第一版）的钉子测试：
// ① attachmentURL：origin 注入 / 穿越拒绝 / 编码；
// ② parseAttachmentLines：「[附件]」行解析成卡 + 从正文剥离（老历史回落）；
// ③ attachmentsFromMessage：结构化引用优先，images + 附件行回落，空 = undefined；
// ④ 实时路径 reduce：userMessage 的附件 → user 块（正文剥离 + attachments），
//    toolResult 的附件 → tool 块；
// ⑤ 回放路径 historyBlocks：同一份归一化（与实时路径同形状——两条路径不许分叉）；
// ⑥ 渲染：用户气泡渲染附件卡，share_file 工具行渲染卡为主体。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import {
  attachmentURL,
  attachmentsFromMessage,
  parseAttachmentLines,
  setAttachmentOrigin,
} from '../src/shared/attachments.ts';
import { reduce } from '../src/shared/store.ts';
import { historyBlocks } from '../src/shared/history.ts';
import { initial } from '../src/shared/blocks.ts';
import { SettingsProvider } from '../src/shared/settings.tsx';
import { Block } from '../src/components/thread/blocks/Block.tsx';
import { ToolBlock } from '../src/components/thread/blocks/ToolBlock.tsx';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: null, historyReady: false };

const imgAtt = (over = {}) => ({ path: 's-1/ab12.png', name: 'chart.png', size: 9, mime: 'image/png', kind: 'image', ...over });
const fileAtt = (over = {}) => ({ path: 's-1/cd34.csv', name: '数据.csv', size: 4, kind: 'file', ...over });

setAttachmentOrigin('http://127.0.0.1:7789');

// ---------- ① attachmentURL ----------

test('① attachmentURL：origin + 编码；穿越/非两段形态一律 null（不做穿越尝试）', () => {
  assert.equal(attachmentURL('s-1/ab12.png'), 'http://127.0.0.1:7789/attachments/s-1/ab12.png');
  assert.equal(attachmentURL('s-1/数 据.csv'), 'http://127.0.0.1:7789/attachments/s-1/%E6%95%B0%20%E6%8D%AE.csv');
  assert.equal(attachmentURL('../secret.png'), null, '.. 直接拒');
  assert.equal(attachmentURL('s-1/../../x.png'), null);
  assert.equal(attachmentURL('/abs/x.png'), null);
  assert.equal(attachmentURL('a\\b.png'), null, '反斜杠拒');
  assert.equal(attachmentURL('only-one-seg'), null);
  assert.equal(attachmentURL(''), null);
});

// ---------- ② parseAttachmentLines ----------

test('② parseAttachmentLines：行解析成卡并从正文剥离；解析不出的行保留', () => {
  const text = '帮我看看\n[附件] 报告.txt → attachments/s-1/ab12cd34.txt';
  const r = parseAttachmentLines(text);
  assert.equal(r.atts.length, 1);
  assert.equal(r.atts[0].name, '报告.txt');
  assert.equal(r.atts[0].path, 's-1/ab12cd34.txt');
  assert.equal(r.atts[0].kind, 'file');
  assert.equal(r.text, '帮我看看', '附件行从正文剥离');
  // 形态不对的行原样保留（不静默吞内容）
  const r2 = parseAttachmentLines('[附件] 坏行\n正文');
  assert.equal(r2.atts.length, 0);
  assert.ok(r2.text.includes('[附件] 坏行'));
});

// ---------- ③ attachmentsFromMessage ----------

test('③ attachmentsFromMessage：结构化优先；老历史回落 images + 附件行；空 = undefined', () => {
  // 结构化优先（正文里的附件行不再产出重复卡）
  const a = attachmentsFromMessage({ attachments: [imgAtt()], content: '看图' });
  assert.equal(a?.length, 1);
  assert.equal(a[0].kind, 'image');
  // 老历史：images 引用 → 图片卡，附件行 → 文件卡
  const b = attachmentsFromMessage({
    images: [{ path: 's-1/old.png', mime: 'image/png' }],
    content: '[附件] 数据.csv → attachments/s-1/cd34.csv',
  });
  assert.equal(b?.length, 2);
  assert.equal(b[0].kind, 'image');
  assert.equal(b[1].kind, 'file');
  // 无任何附件载体：undefined（不渲染卡区）
  assert.equal(attachmentsFromMessage({ content: '纯文本' }), undefined);
});

// ---------- ④ 实时路径 reduce ----------

test('④ userMessage 带附件 → user 块带卡 + 正文剥离；无附件不带键', () => {
  let s = reduce(base, {
    type: 'userMessage', sessionId: 's1', seq: 3,
    text: '看图和文件\n[附件] 数据.csv → attachments/s-1/cd34.csv',
    attachments: [imgAtt(), fileAtt()],
  });
  const blk = s.blocks.find((b) => b.kind === 'user');
  assert.ok(blk, '应建 user 块');
  assert.equal(blk.text, '看图和文件', '正文剥离附件行');
  assert.deepEqual(blk.attachments, [imgAtt(), fileAtt()]);

  const s2 = reduce(base, { type: 'userMessage', sessionId: 's1', seq: 4, text: '纯文本' });
  const blk2 = s2.blocks.find((b) => b.kind === 'user');
  assert.equal(blk2.text, '纯文本');
  assert.equal('attachments' in blk2, false, '无附件不带键（与后端 omitempty 同语义）');
});

test('④ toolResult 带附件 → share_file 工具行带卡', () => {
  let s = reduce(base, { type: 'toolCall', sessionId: 's1', id: 'c1', name: 'share_file', arguments: '{"path":"a.png"}' });
  s = reduce(s, { type: 'toolResult', sessionId: 's1', id: 'c1', name: 'share_file', content: '已共享', isError: false, attachments: [imgAtt()] });
  const blk = s.blocks.find((b) => b.kind === 'tool');
  assert.ok(blk);
  assert.deepEqual(blk.attachments, [imgAtt()]);
  // 普通工具结果不带 attachments 键
  s = reduce(base, { type: 'toolCall', sessionId: 's1', id: 'c2', name: 'read_file', arguments: '{}' });
  s = reduce(s, { type: 'toolResult', sessionId: 's1', id: 'c2', name: 'read_file', content: 'x', isError: false });
  const blk2 = s.blocks.find((b) => b.kind === 'tool');
  assert.equal('attachments' in blk2, false);
});

// ---------- ⑤ 回放路径 historyBlocks ----------

test('⑤ 回放：user 消息的 attachments/images → 与实时同形状；tool 消息的 attachments 回填工具行', () => {
  const blocks = historyBlocks({
    sessionId: 's1',
    messages: [
      { role: 'user', content: '看图和文件\n[附件] 数据.csv → attachments/s-1/cd34.csv', seq: 1, attachments: [imgAtt(), fileAtt()] },
      { role: 'assistant', content: '', tool_calls: [{ id: 'c1', function: { name: 'share_file', arguments: '{"path":"chart.png"}' } }] },
      { role: 'tool', tool_call_id: 'c1', content: '已共享', attachments: [imgAtt()] },
      // 老历史回落：images 引用
      { role: 'user', content: '老图', seq: 2, images: [{ path: 's-1/old.png', mime: 'image/png' }] },
    ],
    busy: false, pending: null, todos: [],
  });
  const users = blocks.filter((b) => b.kind === 'user');
  assert.equal(users.length, 2);
  assert.deepEqual(users[0].attachments, [imgAtt(), fileAtt()], '回放与实时同一份');
  assert.equal(users[0].text, '看图和文件');
  assert.equal(users[1].attachments?.length, 1, '老历史 images 回落成图片卡');
  const tool = blocks.find((b) => b.kind === 'tool');
  assert.deepEqual(tool.attachments, [imgAtt()], 'share_file 的回放结果带卡');
});

// ---------- ⑥ 渲染 ----------

// SettingsProvider 需要 AgentSource（settings 消费它的连接态）——与
// attachments.test.mjs 的 fakeSource 同一份最小桩。
function fakeSource() {
  return {
    subscribe: () => () => {},
    send: () => {}, setApproval: async () => {}, confirm: async () => {}, answer: async () => {},
    cancel: () => {}, compact: async () => ({ compacted: false }), rewind: async () => ({ removed: 0 }),
    newSession: async () => 's1', releaseWorktree: async () => {}, mergeRequest: async () => 'j1',
    resumeSession: () => {}, renameSession: () => {}, archiveSession: async () => ({ ok: true }),
  };
}

const renderBlock = (el) => renderToStaticMarkup(createElement(SettingsProvider, { source: fakeSource() }, el));

test('⑥ 用户气泡渲染附件卡（图片缩略图 + 文件卡），无附件不渲染卡区', () => {
  const atts = [imgAtt(), fileAtt()];
  const html = renderBlock(createElement(Block, {
    block: { kind: 'user', uid: 1, text: '看图', attachments: atts },
    onConfirm: () => {}, onAnswer: () => {},
  }));
  assert.ok(html.includes('att-image'), '图片缩略图');
  assert.ok(html.includes('/attachments/s-1/ab12.png'), '缩略图用端点 URL');
  assert.ok(html.includes('att-file'), '文件卡');
  assert.ok(html.includes('数据.csv'));
  assert.ok(html.includes('4 B'), '大小人话化');

  const plain = renderBlock(createElement(Block, {
    block: { kind: 'user', uid: 2, text: '纯文本' },
    onConfirm: () => {}, onAnswer: () => {},
  }));
  assert.ok(!plain.includes('att-cards'), '无附件不渲染卡区');
});

test('⑥ share_file 工具行渲染附件卡为主体（含 caption）', () => {
  const html = renderToStaticMarkup(createElement(ToolBlock, {
    block: { kind: 'tool', uid: 3, id: 'c1', name: 'share_file', arguments: '{}', result: '已共享', isError: false, attachments: [{ ...imgAtt(), caption: '本月数据' }] },
  }));
  assert.ok(html.includes('共享文件'));
  assert.ok(html.includes('att-image'));
  assert.ok(html.includes('本月数据'));
  // 普通工具不受影响
  const plain = renderToStaticMarkup(createElement(ToolBlock, {
    block: { kind: 'tool', uid: 4, id: 'c2', name: 'read_file', arguments: '{}', result: 'x', isError: false },
  }));
  assert.ok(!plain.includes('att-cards'));
});

test('⑥ 附件引用带穿越形态时渲染死卡（不可点），名字照显', () => {
  const bad = { path: '../evil.png', name: 'evil.png', kind: 'image' };
  const html = renderBlock(createElement(Block, {
    block: { kind: 'user', uid: 5, text: 'x', attachments: [bad] },
    onConfirm: () => {}, onAnswer: () => {},
  }));
  assert.ok(html.includes('att-dead'));
  assert.ok(!html.includes('/attachments/'), '不产出可点的穿越 URL');
});
