// FilesCard 收缩/展开（2026-12）的钉子测试：
//
// 改动文件多时全量铺开很难看——> FILES_COLLAPSE_THRESHOLD（5）默认收起，
// 头部下挂一行紧凑摘要 + 箭头；≤ 阈值照常全显且**无箭头**。展开状态是
// 组件内 useState：每张卡独立、不做跨会话记忆，同一轮内多次渲染（回放）
// 都回到同一默认态（判定提成纯函数 filesDefaultExpanded，钉在这里）。
//
// 测试环境没有 DOM（gsap 的 effect 跑不起来），所以渲染断言用
// renderToStaticMarkup（SSR 初始态 = useState 初值——正是「默认态」），
// 高度过渡本身（gsap 补间）不在此钉——它是纯视觉行为。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import {
  FILES_COLLAPSE_THRESHOLD,
  filesDefaultExpanded,
  FilesCard,
} from '../src/components/thread/blocks/FilesCard.tsx';

const file = (i) => ({ path: `src/mod/file-${i}.tsx`, added: 3, deleted: 1, diff: '+line' });

// ---------- ① 阈值与默认态判定（纯函数） ----------

test('① filesDefaultExpanded：≤ 阈值恒展开（无折叠概念），> 阈值默认收起', () => {
  assert.equal(FILES_COLLAPSE_THRESHOLD, 5, '阈值 = 5（需求拍板值，改动会被审查）');
  for (let n = 0; n <= FILES_COLLAPSE_THRESHOLD; n++) {
    assert.equal(filesDefaultExpanded(n), true, `n=${n} 照常全显`);
  }
  assert.equal(filesDefaultExpanded(FILES_COLLAPSE_THRESHOLD + 1), false, '超阈值默认收起');
  assert.equal(filesDefaultExpanded(50), false);
});

// ---------- ② ≤ 阈值：全显、无箭头（不可折叠的卡不装可折叠的样子） ----------

test('② ≤ 阈值：文件行全显，无展开/收起按钮', () => {
  const html = renderToStaticMarkup(createElement(FilesCard, { files: [1, 2, 3].map(file) }));
  assert.equal((html.match(/files-file/g) || []).length, 3, '三行全铺开');
  assert.ok(!html.includes('files-toggle'), '无箭头按钮——没有折叠能力的卡不渲染它');
  assert.ok(!html.includes('files-list'), '无列表包裹层');
});

// ---------- ③ > 阈值：默认收起 + 头部计数正确 + 摘要是最近的 1~2 个文件名 ----------

test('③ > 阈值：默认收起（aria-expanded=false），摘要显示最近两个文件名，列表保持挂载', () => {
  const files = [1, 2, 3, 4, 5, 6, 7].map(file);
  const html = renderToStaticMarkup(createElement(FilesCard, { files }));
  assert.ok(html.includes('files-toggle'), '有展开/收起按钮');
  assert.ok(html.includes('aria-expanded="false"'), '默认收起');
  assert.ok(html.includes('改动 7 个文件'), '头部计数正确');
  assert.ok(html.includes('file-6.tsx') && html.includes('file-7.tsx'), '摘要 = 最近两个文件名');
  // 收起由高度补间负责（列表保持挂载、height:0），DOM 里仍含全部文件名——
  // 视觉上不铺开是 CSS/动画的事，这里钉的是结构契约：计数 + 摘要 + 收起态。
  assert.equal((html.match(/files-file/g) || []).length, 7, '列表保持挂载（收起由高度补间负责，不在 DOM 里摘除）');
});

// ---------- ④ 回放一致默认态：同一份 files 每次渲染都回到同一初始态 ----------

test('④ 回放一致：同一份 files 渲染多次，默认态逐字节一致', () => {
  const files = Array.from({ length: 9 }, (_, i) => file(i));
  const a = renderToStaticMarkup(createElement(FilesCard, { files }));
  const b = renderToStaticMarkup(createElement(FilesCard, { files }));
  assert.equal(a, b, '无跨会话记忆、无随机态——回放回到同一默认态（收起）');
});

// ---------- ⑤ 头部行（计数 + 增删徽标）在两种形态下都在 ----------

test('⑤ 头部行不变：图标 + 计数 + 增删徽标，折叠与否都渲染', () => {
  for (const n of [3, 8]) {
    const html = renderToStaticMarkup(createElement(FilesCard, { files: Array.from({ length: n }, (_, i) => file(i)) }));
    assert.ok(html.includes('files-head'), '头部行');
    assert.ok(html.includes(`改动 ${n} 个文件`), '计数');
    assert.ok(html.includes('stat-add') && html.includes('stat-del'), '增删徽标');
  }
});
