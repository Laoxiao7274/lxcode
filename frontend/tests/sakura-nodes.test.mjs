// 樱花frp 节点选择的前端契约：
//   ① 展示口径（shared/sakura-nodes.ts 纯函数）：负载/延迟的「—」占位、推荐项
//      （延迟优先、同延迟比负载）、初次选中项、还有几个在测；
//   ② 面板结构（SakuraNodeList 静态渲染）：每台节点一行、推荐标记只挂在推荐项、
//      不可用项置灰 + 原因文案、「自动选择」始终是第一项；
//   ③ 隧道列表的负载占位：load 为 null 显示「—」而不是 null%（壳侧曾用强转绕过
//      类型检查，这里把渲染结果钉住）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  initialSelection,
  latencyText,
  latencyTone,
  loadText,
  pendingLatencyCount,
  recommendNode,
} from '../src/shared/sakura-nodes.ts';
import { SakuraNodeList } from '../src/components/connections/SakuraNodePicker.tsx';

/** 一台节点的默认形状（按需覆盖）。 */
const node = (over = {}) => ({
  id: 1, name: '节点', host: 'frp-a.com', load: 10, usable: true, reason: null, latency: null, ...over,
});

const renderList = (props) => renderToStaticMarkup(createElement(SakuraNodeList, {
  selected: 'auto', recommended: null, disabled: false, probing: true, onSelect: () => {}, ...props,
}));

// ---------- ① 展示口径 ----------

test('loadText：未知负载显示「—」，不显示 0%', () => {
  assert.equal(loadText(null), '—');
  assert.equal(loadText(undefined), '—');
  assert.equal(loadText(0), '0%');
  assert.equal(loadText(38.4), '38.4%');
});

test('latencyText：未测出显示「—」（不是 0 ms）；数值取整', () => {
  assert.equal(latencyText(null), '—');
  assert.equal(latencyText(undefined), '—');
  assert.equal(latencyText(0), '0 ms');
  assert.equal(latencyText(41.6), '42 ms');
});

test('latencyTone：<80 快 / <180 中 / 其余慢；未知无色', () => {
  assert.equal(latencyTone(12), 'fast');
  assert.equal(latencyTone(120), 'mid');
  assert.equal(latencyTone(400), 'slow');
  assert.equal(latencyTone(null), null);
});

test('recommendNode：延迟最低者胜出（不看负载）', () => {
  const pick = recommendNode([
    node({ id: 1, latency: 120, load: 1 }),
    node({ id: 2, latency: 30, load: 90 }),
  ]);
  assert.equal(pick.id, 2, '延迟 30ms 应胜过延迟 120ms（哪怕负载高）');
});

test('recommendNode：延迟相同 → 负载低者；都未知 → 负载低者；再相同按 id', () => {
  assert.equal(recommendNode([
    node({ id: 1, latency: 40, load: 80 }),
    node({ id: 2, latency: 40, load: 20 }),
  ]).id, 2);

  assert.equal(recommendNode([
    node({ id: 1, latency: null, load: 80 }),
    node({ id: 2, latency: null, load: 20 }),
  ]).id, 2, '都还没测出时按负载推荐');

  assert.equal(recommendNode([
    node({ id: 7, latency: 10, load: 5 }),
    node({ id: 3, latency: 10, load: 5 }),
  ]).id, 3, '完全同分时 id 升序（结果稳定）');
});

test('recommendNode：不可用节点不参与推荐（哪怕延迟最低）', () => {
  const pick = recommendNode([
    node({ id: 1, latency: 5, usable: false, reason: '需更高等级' }),
    node({ id: 2, latency: 90 }),
  ]);
  assert.equal(pick.id, 2);
});

test('recommendNode：没有可用节点 → null（面板回落到「自动选择」）', () => {
  assert.equal(recommendNode([node({ usable: false, reason: '离线' })]), null);
  assert.equal(recommendNode([]), null);
});

test('initialSelection：有推荐选推荐，没推荐选自动', () => {
  assert.equal(initialSelection([node({ id: 4, latency: 20 })]), 4);
  assert.equal(initialSelection([node({ usable: false })]), 'auto');
});

test('pendingLatencyCount：只数可用且还没测出的节点', () => {
  assert.equal(pendingLatencyCount([
    node({ id: 1, latency: null }),
    node({ id: 2, latency: 30 }),
    node({ id: 3, latency: null, usable: false, reason: '离线' }),
  ]), 1);
});

// ---------- ② 面板结构 ----------

test('SakuraNodeList：每台节点一行，「自动选择」恒为第一项', () => {
  const html = renderList({
    nodes: [node({ id: 1, name: '天津联通' }), node({ id: 2, name: '日本1' })],
    recommended: node({ id: 2, name: '日本1', latency: 30 }),
  });
  assert.equal((html.match(/data-conn="node-item"/g) ?? []).length, 2);
  assert.equal((html.match(/data-conn="node-auto"/g) ?? []).length, 1);
  assert.ok(html.indexOf('data-conn="node-auto"') < html.indexOf('data-conn="node-item"'),
    '「自动选择」必须在节点之前');
  assert.ok(html.includes('天津联通') && html.includes('日本1'), html);
});

test('SakuraNodeList：推荐标记只挂在推荐项上（「自动选择」靠文案标注，不挂 tag）', () => {
  const html = renderList({
    nodes: [node({ id: 1, name: 'A', latency: 200 }), node({ id: 2, name: 'B', latency: 30 })],
    recommended: node({ id: 2, name: 'B', latency: 30 }),
  });
  // tag 只出现一次（推荐节点那行）——「自动选择（推荐）」是文案，不算推荐标记
  assert.equal((html.match(/conn-node-tag/g) ?? []).length, 1, html);
  assert.ok(html.includes('自动选择（推荐）'), '「自动选择」自己的推荐语义写在文案里');
  const bStart = html.indexOf('data-node-id="2"');
  const aStart = html.indexOf('data-node-id="1"');
  assert.ok(html.slice(bStart, aStart > bStart ? aStart : undefined).includes('conn-node-tag'), 'B 那行该有推荐标记');
  assert.ok(!html.slice(aStart, bStart).includes('conn-node-tag'), 'A 那行不该有推荐标记');
});

test('SakuraNodeList：不可用节点置灰 + 显示原因，且不显示延迟', () => {
  const html = renderList({
    nodes: [
      node({ id: 1, name: 'VIP 节点', usable: false, reason: '需更高等级', vip: 4 }),
      node({ id: 2, name: '日本1', latency: 42 }),
    ],
  });
  assert.ok(html.includes('data-node-id="1" data-usable="false"') || html.includes('data-usable="false"'),
    '不可用项要有 data-usable=false: ' + html);
  assert.ok(html.includes('off'), '不可用项要有 off 类（置灰）');
  assert.ok(html.includes('需更高等级'), '必须显示不可用原因');
  assert.ok(html.includes('42 ms'), '可用节点显示实测延迟');
});

test('SakuraNodeList：延迟未测出显示「测试中…」，负载未知显示「—」', () => {
  const html = renderList({ nodes: [node({ id: 1, latency: null, load: null })] });
  assert.ok(html.includes('测试中…'), html);
  assert.ok(html.includes('负载 —'), '负载未知必须显示「—」而不是 0%: ' + html);
  assert.ok(!html.includes('null%'), '不许把 null 直接渲染进界面: ' + html);
});

test('SakuraNodeList：没有探测时（演示模式）延迟显示「—」，不装作在测', () => {
  const html = renderList({ nodes: [node({ id: 1, latency: null })], probing: false });
  assert.ok(!html.includes('测试中…'), '没有探测就不该显示「测试中…」: ' + html);
  assert.ok(html.includes('—'), html);
});

test('SakuraNodeList：选中项带 on 类，选中态由 aria-selected 表达', () => {
  const html = renderList({ nodes: [node({ id: 5 })], selected: 5 });
  assert.ok(html.includes('data-node-id="5" data-usable="true"') || html.includes('data-node-id="5"'));
  assert.ok(/class="conn-node-item on"[\s\S]*?data-node-id="5"/.test(html) || /data-node-id="5"[\s\S]*?aria-selected="true"/.test(html),
    html);
  assert.ok(/aria-selected="false" class="conn-node-item auto"/.test(html), '自动选择此时不该被选中: ' + html);
});

// ---------- ③ 隧道列表负载占位 ----------

test('SakuraBlock 源码：负载用 loadText 渲染（load=null → 「—」而不是 null%）', async () => {
  const { readFileSync } = await import('node:fs');
  const src = readFileSync(new URL('../src/components/connections/SakuraBlock.tsx', import.meta.url), 'utf8');
  assert.ok(src.includes('loadText(t.load)'), '隧道行的负载必须走 loadText（未知显示「—」）');
  assert.ok(!src.includes('负载 {t.load}%'), '旧写法（null 会渲染成 null%）必须已经消失');
});
