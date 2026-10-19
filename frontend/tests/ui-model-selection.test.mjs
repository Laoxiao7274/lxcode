// 三个 UI 问题的钉子（2026-12 用户报）：
//   ① 新建自定义提供商「获取模型」一次性全加、无法选择 —— 新建与已有提供商必须
//      **共用同一个候选选择组件**（CandidatePicker），且默认全不选；
//   ② Composer 模型下拉太长 —— 面板固定 max-height + 列表内部滚动；
//   ③ 设置页提供商区块默认展开 —— 默认**全部收缩**，展开状态记 localStorage，
//      新建/编辑后自动展开对应项。
//
// ① 与 ③ 各有一条源码断言（钉住「只有一份实现」与「默认值」），其余是运行时行为：
// CandidatePicker 与 provider-expanded 都是纯模块，可直接渲染/调用断言。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { CandidatePicker } from '../src/components/settings/CandidatePicker.tsx';
import { readExpanded, writeExpanded, isExpanded, rememberExpand } from '../src/shared/provider-expanded.ts';

const read = (rel) => fs.readFileSync(new URL(`../src/${rel}`, import.meta.url), 'utf8');

// ---- 问题 1：共用候选选择组件 ----

test('① 新建自定义提供商与已有提供商共用 CandidatePicker（同一份实现，两处不再各写一套）', () => {
  const connect = read('components/settings/ConnectProviderDialog.tsx');
  const probe = read('components/settings/ProbeResultDialog.tsx');
  const picker = read('components/settings/CandidatePicker.tsx');

  // 两处都从 CandidatePicker 导入并使用
  assert.ok(/import \{ CandidatePicker \} from "\.\/CandidatePicker"/.test(connect),
    '新建自定义提供商的对话框必须复用 CandidatePicker');
  assert.ok(/import \{ CandidatePicker \} from "\.\/CandidatePicker"/.test(probe),
    '已有提供商的「获取模型列表」结果弹窗必须复用 CandidatePicker');
  assert.ok(/<CandidatePicker\b/.test(connect), '新建路径必须渲染 CandidatePicker');
  assert.ok(/<CandidatePicker\b/.test(probe), '已有提供商路径必须渲染 CandidatePicker');

  // 旧的内联候选行实现不得回潮（否则又是两套）
  assert.ok(!/export function CandidateRows/.test(connect),
    'CandidateRows 已迁到 CandidatePicker，对话框里不许再有一份');
  assert.ok(picker.includes('export function CandidateRows'),
    '候选行只允许 CandidatePicker 这一份实现');

  // 唯一入口：搜索/全选/已添加禁用都只在 CandidatePicker 里
  assert.ok(picker.includes('onPickAll') && picker.includes('已添加'),
    'CandidatePicker 必须承担全选与「已添加」标记');
});

test('① 新建路径探测后默认全不选（不再 setPicked(全部)——用户必须先勾选）', () => {
  const connect = read('components/settings/ConnectProviderDialog.tsx');
  const runProbe = connect.slice(connect.indexOf('const runProbe = async () => {'));
  const body = runProbe.slice(0, runProbe.indexOf('const submit = () =>'));
  assert.ok(!/setPicked\(new Set\(res\.models\.map/.test(body),
    '探测完不得预勾选全部模型（这是「一次性全加」的根因）');
  assert.ok(/setPicked\(new Set\(\)\)/.test(body), '重新获取后应清空勾选，从零开始选');
});

test('① CandidatePicker：默认零选中、已添加项置灰禁勾、搜索可过滤', () => {
  const html = renderToStaticMarkup(createElement(CandidatePicker, {
    items: [
      { id: 'a', name: 'Model A' },
      { id: 'b', name: 'Model B', disabled: true, tags: ['已添加'] },
    ],
    picked: new Set(),
    onToggle: () => {},
    onPickAll: () => {},
  }));
  assert.ok(html.includes('type="search"'), '候选清单必须带搜索框');
  assert.ok(html.includes('已选 <b>0</b>'), '默认一个都不选');
  assert.ok(/class="mset-model-row added"/.test(html), '已添加的候选整行置灰');
  assert.ok(/aria-disabled="true"/.test(html), '已添加项的开关必须禁用（不可重复勾）');
  assert.ok(html.includes('已添加'), '已添加项要有可见标签');
  assert.ok(html.includes('1 个已添加'), '计数里如实说明已添加数');
});

test('① CandidatePicker：空候选给人话提示，不白屏', () => {
  const html = renderToStaticMarkup(createElement(CandidatePicker, {
    items: [],
    picked: new Set(),
    onToggle: () => {},
    onPickAll: () => {},
    emptyText: '端点没有报告任何模型',
  }));
  assert.ok(html.includes('端点没有报告任何模型'), '空清单必须有可读提示');
});

// ---- 问题 2：模型下拉框固定高度 + 内部滚动 ----

test('② 模型下拉面板有 max-height，列表自身 overflow-y:auto（不撑满屏幕）', () => {
  const css = read('styles/composer.css');
  const left = css.slice(css.indexOf('.mp-left {'));
  const block = left.slice(0, left.indexOf('}'));
  assert.ok(/max-height:\s*min\(420px,\s*60vh\)/.test(block),
    '.mp-left 必须有 max-height: min(420px, 60vh)');
  assert.ok(/display:\s*flex/.test(block) && /flex-direction:\s*column/.test(block),
    '面板应是 flex 列：标题/搜索/强度行钉住，只有列表滚');

  const list = css.slice(css.indexOf('.mp-list {'));
  const listBlock = list.slice(0, list.indexOf('}'));
  assert.ok(/overflow-y:\s*auto/.test(listBlock), '.mp-list 必须内部滚动');
  assert.ok(/min-height:\s*0/.test(listBlock), 'flex 子项 min-height:0 才允许收缩出滚动条');
  assert.ok(/scrollbar-color:\s*var\(--scrollbar-thumb\)/.test(listBlock),
    '滚动条走现有 token 样式');

  // 分组标题吸顶（长列表里仍知道在哪家提供商下）
  const smCss = read('styles/settings-models.css');
  const title = smCss.slice(smCss.indexOf('.mp-group-title {'));
  assert.ok(/position:\s*sticky/.test(title.slice(0, title.indexOf('}'))), '分组标题应吸顶');
});

test('② 键盘可达：↑/↓ 移动高亮、Enter 选中、高亮项滚进视野', () => {
  const src = read('components/model-picker/ModelPicker.tsx');
  assert.ok(src.includes('ArrowDown') && src.includes('ArrowUp'), '必须支持上下键');
  assert.ok(/e\.key === "Enter" && active >= 0/.test(src), 'Enter 选中当前高亮项');
  assert.ok(src.includes('scrollIntoView'), '高亮项要滚进视野（否则键盘光标跑到看不见的地方）');
  assert.ok(src.includes('className={"mp-item"') && src.includes('" kbd"'), '高亮行要有独立样式类');
  assert.ok(/\.mp-item\.kbd/.test(read('styles/settings-models.css')), 'kbd 高亮样式必须存在');
  assert.ok(src.includes('搜索模型'), '顶部搜索框（模型多时唯一可用入口）');
});

// ---- 问题 3：提供商区块默认收缩 ----

test('③ 没有记忆时提供商卡默认收缩（isExpanded 只在显式 true 时展开）', () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  };
  assert.deepEqual(readExpanded(), {}, '初始无记忆');
  assert.equal(isExpanded('https://api.a/v1'), false, '没有记忆 = 收缩（默认收起）');

  writeExpanded('https://api.a/v1', true);
  assert.equal(isExpanded('https://api.a/v1'), true, '用户展开后记住');
  assert.equal(isExpanded('https://api.b/v1'), false, '其它提供商仍是收缩');

  writeExpanded('https://api.a/v1', false);
  assert.equal(isExpanded('https://api.a/v1'), false, '收起也要记住');

  // 新建/编辑路径：rememberExpand 让该提供商下次挂载即展开
  rememberExpand('https://api.c/v1');
  assert.equal(isExpanded('https://api.c/v1'), true, '新建后自动展开对应项');

  // 坏数据/无存储都退回「全部收缩」，不抛异常
  store.set('lxcode.settings.providerExpanded', 'not json');
  assert.deepEqual(readExpanded(), {}, '坏 JSON 当没有记忆');
  delete globalThis.localStorage;
  assert.deepEqual(readExpanded(), {}, '没有 localStorage（隐私模式）也不抛');
  assert.equal(isExpanded('https://api.a/v1'), false);
});

test('③ ProviderBlock 初始展开态读记忆（不是写死 true），新建路径写入展开', () => {
  const block = read('components/settings/ProviderBlock.tsx');
  assert.ok(/useState\(\(\) => isExpanded\(provider\.id\)\)/.test(block),
    '初始态必须来自记忆（默认收缩），不能是 useState(true)');
  assert.ok(!/useState\(true\)/.test(block), '不得写死默认展开');
  assert.ok(/writeExpanded\(provider\.id, expanded\)/.test(block), '用户展开/收起要落存储');
  assert.ok(/setExpanded\(true\);\s*setEditingConn\(true\)/.test(block),
    '编辑连接配置时自动展开对应项');

  const connect = read('components/settings/ConnectProviderDialog.tsx');
  assert.ok(/rememberExpand\(providerKey\(input\.baseUrl\)\)/.test(connect),
    '新建提供商成功后记住展开（回到设置页该卡就是开的）');
});
