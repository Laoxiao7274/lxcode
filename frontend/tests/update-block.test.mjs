// update-block.test.mjs —— 设置页「软件更新」区块的呈现钉子。
//
// 背景（2026-12 样式重做）：旧版检查按钮只在 idle 态渲染——检查过一次
// （latest/available/downloading/ready）入口就整个消失，用户反馈的核心回归。
// 本文件钉住两条硬契约：
//   1. 「检查更新」按钮在**全部状态**（idle/latest/available/downloading/ready，
//      含 error 在场）下可见可点；检查中禁用防连点，恢复后可点；
//   2. 每种状态一个明确的视觉形态（data-phase + 状态行/错误行/面板 tone），
//      不共用模糊样式。
// UpdateBlockView 是纯展示组件（静态渲染即可测，无需 DOM/Provider）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { UpdateBlockView } from '../src/components/settings/UpdateBlock.tsx';
import { relTime } from '../src/shared/update.tsx';

const NOW = 1_800_000_000_000;

const render = (over = {}) => renderToStaticMarkup(createElement(UpdateBlockView, {
  current: '0.1.0',
  phase: 'idle',
  manifest: null,
  progress: 0,
  checkedAt: null,
  error: null,
  now: NOW,
  onCheck: () => {},
  onDownload: () => {},
  onRestart: () => {},
  ...over,
}));

const MANIFEST = { version: '0.2.0', size: 12_400_000, notes: ['a', 'b', 'c', 'd', 'e'] };

test('检查按钮在全部状态下渲染且可点（disabled 缺席）', () => {
  for (const phase of ['idle', 'latest', 'available', 'downloading', 'ready']) {
    const html = render({ phase });
    assert.ok(html.includes('data-su="check"'), `${phase}: 检查按钮必须在场`);
    assert.ok(!html.includes('data-su="check"') || !/<button[^>]*data-su="check"[^>]*disabled/.test(html), `${phase}: 按钮不该被禁用`);
  }
});

test('检查中：按钮禁用防连点且带 aria-busy；恢复（latest）后可点', () => {
  const checking = render({ phase: 'checking' });
  assert.match(checking, /<button[^>]*data-su="check"[^>]*disabled/);
  assert.match(checking, /aria-busy="true"/);
  assert.ok(checking.includes('检查中…'), '检查中态要有进行中文案');
  const after = render({ phase: 'latest' });
  assert.ok(!/<button[^>]*data-su="check"[^>]*disabled/.test(after), '检查完成后按钮必须恢复可点');
});

test('error 在场时检查按钮仍在场（失败态也保留手动入口）', () => {
  const html = render({ phase: 'idle', error: 'manifest 拉取失败：HTTP 503' });
  assert.ok(html.includes('data-su="check"'));
  assert.match(html, /manifest 拉取失败：HTTP 503/);
  assert.ok(html.includes('重试'), '错误行必须带重试入口');
  assert.ok(html.includes('role="alert"'), '错误行必须有 alert 语义');
});

test('状态形态：每态各一个 data-phase 与对应状态文案，不共用', () => {
  const cases = [
    ['idle', 'lxcode 0.1.0'],
    ['checking', '正在检查更新'],
    ['latest', '已是最新版本'],
    ['available', '新版本'],
    ['downloading', '下载中'],
    ['ready', '更新已就绪'],
  ];
  for (const [phase, text] of cases) {
    const html = render({ phase, manifest: MANIFEST, progress: 40 });
    assert.ok(html.includes(`data-phase="${phase}"`), `${phase}: data-phase 缺失`);
    assert.ok(html.includes(text), `${phase}: 状态文案「${text}」缺失`);
  }
});

test('available：面板 info tone + 下载主按钮；ready：面板 ok tone + 重启主按钮', () => {
  const avail = render({ phase: 'available', manifest: MANIFEST });
  assert.match(avail, /data-tone="info"/);
  assert.ok(avail.includes('data-su="download"'));
  const ready = render({ phase: 'ready', manifest: MANIFEST });
  assert.match(ready, /data-tone="ok"/);
  assert.ok(ready.includes('data-su="restart"'));
});

test('downloading：进度条与百分比渲染', () => {
  const html = render({ phase: 'downloading', manifest: MANIFEST, progress: 62 });
  assert.match(html, /width:62%/);
  assert.ok(html.includes('62%'));
});

test('notes：默认只露前 3 条，其余折叠进展开入口', () => {
  const html = render({ phase: 'available', manifest: MANIFEST });
  assert.ok(html.includes('>a<') && html.includes('>b<') && html.includes('>c<'));
  assert.ok(!html.includes('>d<'), '折叠的 notes 不该出现');
  assert.ok(html.includes('展开其余 2 条'));
});

test('checkedAt：渲染相对时间「N 分钟前检查」；checking 中不显示', () => {
  const html = render({ phase: 'latest', checkedAt: NOW - 10 * 60_000 });
  assert.ok(html.includes('10 分钟前检查'));
  const checking = render({ phase: 'checking', checkedAt: NOW - 10 * 60_000 });
  assert.ok(!checking.includes('分钟前检查'), '检查进行中不显示旧时间戳');
  const never = render({ phase: 'idle', checkedAt: null });
  assert.ok(!never.includes('检查</span>') || !never.includes('前检查'), '从未检查过不该编一个时间');
});

test('relTime：秒级→刚刚，分钟/小时/天分段', () => {
  assert.equal(relTime(NOW - 30_000, NOW), '刚刚');
  assert.equal(relTime(NOW - 10 * 60_000, NOW), '10 分钟前');
  assert.equal(relTime(NOW - 90 * 60_000, NOW), '1 小时前');
  assert.equal(relTime(NOW - 26 * 3600_000, NOW), '1 天前');
  assert.equal(relTime(NOW + 5000, NOW), '刚刚', '未来时间戳钳到刚刚');
});
