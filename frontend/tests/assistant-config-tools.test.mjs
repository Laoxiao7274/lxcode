// 助手工具白名单（2026-12 用户拍板：助手默认工具完全可配——「跟我们的都得
// 一样，完全可控」）：
//   ① 源码断言：AssistantConfigTab 工具区换成 ToolCardPicker（AgentEditor 同款
//      四组内置/第三方/MCP/自定义，全目录可选——内置写类 bash/edit 也可勾入，
//      用户担责）；预填 = whitelist 非空 ? whitelist : builtinDefaults ∪ 旧
//      tools；「恢复默认」清空白名单；写类警示 + 基础能力三件套标注；保存写
//      whitelist 并清空旧 tools（防双源）；
//   ② demo 数据源：assistantSetConfig/GetConfig 对 toolWhitelist 透明往返；
//   ③ wire 层：WSAssistant 的 get 解析 toolWhitelist/builtinDefaults、set 透传；
//   ④ shared 类型：AssistantConfig.toolWhitelist / builtinDefaults；
//   ⑤ golden 抽检：assistantConfigResult 样本的 toolWhitelist/builtinDefaults 键。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const tabSrc = fs.readFileSync(
  new URL('../src/components/catalog/AssistantConfigTab.tsx', import.meta.url), 'utf8');

test('① 工具区是 ToolCardPicker（四组全目录——内置 bash/edit 在可选面内）', () => {
  assert.ok(tabSrc.includes('<ToolCardPicker'), '工具区渲染 ToolCardPicker 卡片选择器');
  assert.ok(/value=\{whitelist\}/.test(tabSrc), 'value 绑预填白名单');
  assert.ok(/onChange=\{\(toolWhitelist\) => setCfg\(\(c\) => \(\{ \.\.\.c, toolWhitelist, tools: undefined \}\)\)\}/.test(tabSrc),
    '勾选写回 cfg.toolWhitelist 并清空旧 tools（防双源）');
  // 四组分组（AgentEditor 同款）：内置/第三方/MCP/自定义全目录，不再过滤 custom binary。
  for (const g of ['title: "内置"', 'title: "第三方 / 插件"', 'title: "MCP"', 'title: "自定义"']) {
    assert.ok(tabSrc.includes(g), `分组载荷含 ${g}`);
  }
  // 旧「只列 custom binary」的可选面过滤已移除——现在的四组过滤都是
  // `!t.custom &&` 开头的来源分组（AgentEditor 同款），不存在旧口径。
  assert.ok(!/toolChips/.test(tabSrc), '旧 toolChips 可选面已移除（工具区整体换成 ToolCardPicker）');
});

test('① 预填：whitelist 非空 ? whitelist : builtinDefaults ∪ 旧 tools（builtinDefaults 是服务端事实源）', () => {
  assert.match(tabSrc, /const builtinDefaults = cfg\.builtinDefaults \?\? \[\];/,
    'builtinDefaults 来自服务端应答（前端不写死 14 项）');
  assert.match(
    tabSrc,
    /const whitelist = cfg\.toolWhitelist\?\.length\s*\?\s*cfg\.toolWhitelist\s*:\s*Array\.from\(new Set\(\[\.\.\.builtinDefaults, \.\.\.\(cfg\.tools \?\? \[\]\)\]\)\);/,
    '预填 = whitelist 非空 ? whitelist : builtinDefaults ∪ 旧 tools（迁移路径）');
});

test('① 「恢复默认」按钮：清空 whitelist（回到内置默认）', () => {
  assert.match(tabSrc, /data-cg="restore-tool-defaults"/, '恢复默认按钮带稳定 data-cg 钩子');
  assert.match(tabSrc, /onClick=\{\(\) => setCfg\(\(c\) => \(\{ \.\.\.c, toolWhitelist: undefined, tools: undefined \}\)\)\}/,
    '恢复默认 = 清空 whitelist 与旧 tools');
  assert.ok(tabSrc.includes('恢复默认 = 清空白名单'), '按钮旁说明语义');
});

test('① 写类警示：勾 bash/edit/write_file 时明示 auto 档不经确认直接运行', () => {
  assert.match(tabSrc, /const WRITE_TOOLS = new Set\(\["bash", "edit", "write_file"\]\);/,
    '写类工具集合');
  assert.match(tabSrc, /const writePicked = whitelist\.some\(\(id\) => WRITE_TOOLS\.has\(id\)\);/,
    '警示按预填面现算');
  assert.ok(tabSrc.includes('不经确认直接运行'), '警示文案明示不经确认');
});

test('① 基础能力三件套标注：只在去勾时显示（2026-12 用户反馈：常驻三行看不懂）', () => {
  assert.match(tabSrc, /const CORE_TOOLS = \["run_task", "update_assistant_config", "mcp_manage"\] as const;/,
    '三件套清单');
  assert.ok(tabSrc.includes('失去向项目派发任务的能力'), 'run_task 后果');
  assert.ok(tabSrc.includes('失去自改配置的能力'), 'update_assistant_config 后果');
  assert.ok(tabSrc.includes('失去管理 MCP 服务器的能力'), 'mcp_manage 后果');
  assert.ok(tabSrc.includes('不强制保留，按需取舍'), '标注不强制');
  // 去勾门：cfg.toolWhitelist 非空（保存过白名单或本轮点过卡片）才对缺失的
  // 三件套显示后果——默认态/预填态不显示。
  assert.match(
    tabSrc,
    /const coreMissing =\s*\n\s*\(cfg\.toolWhitelist\?\.length \?\? 0\) > 0\s*\n\s*\? CORE_TOOLS\.filter\(\(id\) => !whitelist\.includes\(id\)\)\s*\n\s*: \[\];/,
    '警示判定带 toolWhitelist 非空门（去勾才显示）');
  // 旧常驻判定（无条件 filter）已移除。
  assert.ok(!/const coreMissing = CORE_TOOLS\.filter/.test(tabSrc), '旧无条件判定已移除');
});

test('② demo 数据源：toolWhitelist 透明往返（演示态自管内存对象）', async () => {
  const { DemoAgent } = await import('../src/agent/demo/index.ts');
  const demo = new DemoAgent();
  await demo.assistantSetConfig({ name: '小灵', toolWhitelist: ['bash', 'read_file'] });
  const cfg = await demo.assistantGetConfig();
  assert.deepEqual(cfg.toolWhitelist, ['bash', 'read_file'], 'toolWhitelist 原样保留');
  await demo.assistantSetConfig({ name: '小灵' });
  assert.equal((await demo.assistantGetConfig()).toolWhitelist, undefined, '全量替换：不带 = 清空（用内置默认）');
});

test('③ WSAgent wire：get 解析 toolWhitelist/builtinDefaults、set 透传 toolWhitelist', () => {
  const src = fs.readFileSync(new URL('../src/agent/ws/assistant.ts', import.meta.url), 'utf8');
  assert.match(src, /Array\.isArray\(cfg\.toolWhitelist\) && cfg\.toolWhitelist\.length[\s\S]*?toolWhitelist: cfg\.toolWhitelist\.map\(String\)/,
    'get 的 toolWhitelist 解析守卫');
  assert.match(src, /Array\.isArray\(cfg\.builtinDefaults\) && cfg\.builtinDefaults\.length[\s\S]*?builtinDefaults: cfg\.builtinDefaults\.map\(String\)/,
    'get 的 builtinDefaults 解析守卫（预填/恢复默认的事实源）');
  assert.match(src, /cfg\.toolWhitelist\?\.length \? \{ toolWhitelist: cfg\.toolWhitelist \} : \{\}/,
    'set 的 toolWhitelist 透传（非空才带键——空 = 用内置默认）');
});

test('④ shared 类型：AssistantConfig.toolWhitelist / builtinDefaults（可选 string[]）', () => {
  const src = fs.readFileSync(new URL('../src/shared/types.ts', import.meta.url), 'utf8');
  assert.match(src, /toolWhitelist\?: string\[];/, 'toolWhitelist 类型字段在场');
  assert.match(src, /builtinDefaults\?: string\[];/, 'builtinDefaults 类型字段在场');
});

test('⑤ golden：assistantConfigResult 样本的 toolWhitelist/builtinDefaults 键（wire 形状同步）', () => {
  const golden = JSON.parse(readFileSync(new URL('../../internal/protocol/testdata/golden.json', import.meta.url), 'utf8'));
  const full = golden.assistantConfigResultFull?.value;
  assert.ok(Array.isArray(full?.toolWhitelist) && full.toolWhitelist.includes('read_file'),
    '全量样本带 toolWhitelist 数组');
  assert.ok(Array.isArray(full?.builtinDefaults) && full.builtinDefaults.length === 14,
    '全量样本带 builtinDefaults（内置默认 14 项）');
  // 最小样本：用户设置缺席（空 = 用内置值），但 builtinDefaults 在场（服务端
  // 恒带回——UI 预填与恢复默认的事实源）。
  const minimal = golden.assistantConfigResultMinimal?.value ?? {};
  assert.ok(!('toolWhitelist' in minimal), '最小样本 toolWhitelist 整键缺席（空 = 用内置值）');
  assert.ok(Array.isArray(minimal.builtinDefaults) && minimal.builtinDefaults.length === 14,
    '最小样本 builtinDefaults 在场（get 恒带回）');
});

function readFileSync(url) {
  return fs.readFileSync(url, 'utf8');
}
