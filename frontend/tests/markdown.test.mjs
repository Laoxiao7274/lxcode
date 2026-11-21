import fs from 'node:fs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseBlocks, parseInline, parseTable, splitRow, parseBlocksCached } from '../src/components/markdown.tsx';

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

// ---------- 定型块解析缓存（2026-12 会话切换优化批 4）----------
//
// 键 = 块对象（WeakMap）：块不可变共享 → 切走再切回（重挂载）命中同一份
// MdBlock[] 引用；rebuiltGen 重建换新块对象 → 缓存自动失效重解析（正确行为）；
// S3 骨架化淘汰丢块 → 缓存随块 GC。同块二次挂载不重解析的断言 = 返回**同一引用**
// （命中路径不走 parseBlocks，引用即证据）。

test('解析缓存：同块同段命中同一引用；文本变了重解析；换块对象各归各', () => {
  const block = { uid: 1 };
  const a1 = parseBlocksCached(block, '### 标题\n\n正文');
  const a2 = parseBlocksCached(block, '### 标题\n\n正文');
  assert.equal(a1, a2, '命中返回缓存引用（不重解析）');
  assert.deepEqual(kinds(a1), ['heading', 'para']);

  const a3 = parseBlocksCached(block, '### 标题\n\n正文二');
  assert.notEqual(a3, a1, '文本变了 = 新解析（块内容只增不突变，但防御性按 text 判）');

  const other = { uid: 2 };
  const b1 = parseBlocksCached(other, '### 标题\n\n正文');
  assert.notEqual(b1, a1, '不同块对象 = 不同缓存条目（rebuiltGen 重建语义）');
  assert.deepEqual(kinds(b1), kinds(a1));
});

test('解析缓存：同一块对象缓存多个切段（两段渲染 × 围栏段），互不串扰', () => {
  const block = { uid: 9 };
  const committed = parseBlocksCached(block, '首段');
  const tail = parseBlocksCached(block, '尾段');
  assert.notEqual(committed, tail, 'committed/tail 是不同的段');
  assert.equal(parseBlocksCached(block, '首段'), committed, '各段各自命中');
  assert.equal(parseBlocksCached(block, '尾段'), tail);
});

test('接线：AnswerBody 只在定型块传缓存键（流式段现解析，缓存不成为流式垃圾场）', () => {
  const src = fs.readFileSync(new URL('../src/components/thread/blocks/AnswerBody.tsx', import.meta.url), 'utf8');
  assert.match(src, /scope=\{streaming \? undefined : block\}/,
    'scope 必须以 streaming 为门：流式每帧新文本，块键控缓存会把中间态全存下来');
  assert.match(src, /block\?: object/, 'AnswerBody 接受块对象作为缓存键');
  const blockSrc = fs.readFileSync(new URL('../src/components/thread/blocks/Block.tsx', import.meta.url), 'utf8');
  assert.match(blockSrc, /<AnswerBody text=\{block\.content\} streaming=\{block\.streaming\} block=\{block\} \/>/,
    'Block 的 assistant 分支把块对象传给 AnswerBody');
});
