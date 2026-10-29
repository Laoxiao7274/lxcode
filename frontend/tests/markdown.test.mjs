import test from 'node:test';
import assert from 'node:assert/strict';
import { parseBlocks, parseInline, parseTable, splitRow } from '../src/shared/markdown.tsx';

const kinds = (blocks) => blocks.map((b) => b.kind);

test('headings, hr, paragraphs and gap marking', () => {
  const blocks = parseBlocks('### 功能清单\n\n正文一段\n正文二行\n---\n再一段');
  assert.deepEqual(kinds(blocks), ['heading', 'para', 'para', 'hr', 'para']);
  assert.equal(blocks[0].level, 3);
  assert.equal(blocks[0].text, '功能清单');
  assert.equal(blocks[2].gap, false);
  assert.equal(blocks[4].gap, true, '分割线后的段落应带段距');
});

test('lists: ul/ol runs, nesting by indent, mixed starts new block', () => {
  const blocks = parseBlocks('- 一\n- 二\n  - 嵌套\n1. 甲\n2. 乙');
  assert.deepEqual(kinds(blocks), ['list', 'list']);
  assert.deepEqual(blocks[0].items, [
    { level: 0, text: '一' },
    { level: 0, text: '二' },
    { level: 1, text: '嵌套' },
  ]);
  assert.equal(blocks[1].ordered, true);
  assert.deepEqual(blocks[1].items.map((i) => i.text), ['甲', '乙']);
});

test('quote run groups and strips markers', () => {
  const blocks = parseBlocks('> 引用一\n> 引用二\n\n正文');
  assert.deepEqual(kinds(blocks), ['quote', 'para']);
  assert.deepEqual(blocks[0].lines, ['引用一', '引用二']);
});

test('table: separator confirms header and alignment; rows stream in', () => {
  const blocks = parseBlocks('| 模块 | 路径 | 说明 |\n|:---|:---:|---:|\n| a | b | c |');
  assert.deepEqual(kinds(blocks), ['table']);
  const t = parseTable(blocks[0].lines);
  assert.deepEqual(t.header, ['模块', '路径', '说明']);
  assert.deepEqual(t.aligns, ['left', 'center', 'right']);
  assert.deepEqual(t.body, [['a', 'b', 'c']]);
  // 流式：只有首行（分隔线未到）也渲染为表头
  const partial = parseTable(['| 模块 | 路径 |']);
  assert.deepEqual(partial.header, ['模块', '路径']);
  assert.deepEqual(partial.body, []);
  assert.deepEqual(partial.aligns, ['left', 'left'], '无分隔线时默认左对齐');
  // 容忍流式截断（缺尾竖线）
  assert.deepEqual(splitRow('| a | b'), ['a', 'b']);
});

test('inline: closed pairs render, unclosed stay literal', () => {
  const md = (t) => parseInline(t).map((s) => s.t);
  assert.deepEqual(md('加 **粗** 与 `code` 和 [链接](https://x.dev) 及 *斜* 与 ~~删~~'),
    ['text', 'strong', 'text', 'code', 'text', 'link', 'text', 'em', 'text', 'del']);
  // 未闭合（流式中途）——按原文，不吞不藏
  assert.deepEqual(md('孤立的 ** 星号'), ['text']);
  assert.deepEqual(md('半个 ` 反引号'), ['text']);
  // 乘号不误伤（单星斜体要求首尾非空白）
  assert.deepEqual(md('2 * 3 * 4'), ['text']);
});

test('streaming prefix property: earlier blocks stable while text grows', () => {
  const prefix = '### 标题\n\n- 项一\n- 项二\n';
  const before = parseBlocks(prefix);
  const after = parseBlocks(prefix + '正文开始');
  // 已完成块的结构不变（打字只追加）
  assert.deepEqual(kinds(before), kinds(after.slice(0, before.length)));
  assert.equal(before[0].text, after[0].text);
  // 表格行到达即成块（首行独立到达）
  const t1 = parseBlocks('| a | b |');
  assert.equal(t1[0].kind, 'table');
  // 追加行后原首行仍是表头
  const t2 = parseBlocks('| a | b |\n| c | d |');
  assert.equal(t2[0].lines.length, 2);
});

test('parser semantics are context-free (fences are stripped upstream)', () => {
  // ``` 围栏切分在 Markdown 组件层完成（text.split）——parseBlocks 收到的
  // 是围栏外/内两段各自独立解析。这里只钉住解析器语义本身：
  // # 开头就是标题、| 开头就是表格（与是否曾处于代码围栏无关）。
  const blocks = parseBlocks('# 是标题\n| 是表格 |');
  assert.deepEqual(kinds(blocks), ['heading', 'table']);
});
