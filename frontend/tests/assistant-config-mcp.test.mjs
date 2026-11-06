// 助手配置页签的 MCP 服务器绑定（2026-12 助手拓展能力完整化）：
//   ① 源码断言：AssistantConfigTab 有 MCP 服务器 Chips 区（绑定服务器 id，
//      数据源 useAgents().mcpServers）+ 安全明示文案 + 管理入口；
//   ② demo 数据源：assistantSetConfig/GetConfig 对 mcpServers 透明往返；
//   ③ wire 层：WSAssistant 的 get/set 透传 mcpServers（源码形状断言）。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const tabSrc = fs.readFileSync(
  new URL('../src/components/catalog/AssistantConfigTab.tsx', import.meta.url), 'utf8');

test('① AssistantConfigTab 有 MCP 服务器 Chips 区：绑服务器 id、值随 cfg.mcpServers', () => {
  assert.ok(/value=\{cfg\.mcpServers \?\? \[\]\}/.test(tabSrc), 'Chips 的 value 绑 cfg.mcpServers');
  assert.ok(/onChange=\{\(mcpServers\) => setCfg\(\(c\) => \(\{ \.\.\.c, mcpServers \}\)\)\}/.test(tabSrc),
    '勾选写回 cfg.mcpServers');
  assert.match(tabSrc, /ariaLabel="助手 MCP 服务器（多选——绑定的是服务器 id，非工具名）"/, '绑定语义在无障碍标签明示');
  assert.ok(tabSrc.includes('mcpServers.map((s) => ({ value: s.id'), 'chips 数据源 = mcpServers 列表（服务器 id）');
  // 计数徽章把 mcpServers 也算进「上下文」数
  assert.ok(tabSrc.includes('(cfg.mcpServers?.length ?? 0)'), '上下文计数含 MCP 绑定数');
});

test('① 安全语义明示：MCP 区说明「自动执行」档位（用户知情——MCP 工具不经确认）', () => {
  assert.match(tabSrc, /安装\/启用的 MCP 服务器，其工具将直接对助手可用（助手当前权限档为自动执行）。/,
    'MCP 区的安全说明文案');
});

test('① 管理入口：「管理技能 / 管理 MCP 服务器」→ onOpenCatalog（拓展页对应页签）', () => {
  assert.ok(/manageEntry\("skills", "管理技能"\)/.test(tabSrc), '技能组带管理入口');
  assert.ok(/manageEntry\("mcp", "管理 MCP 服务器"\)/.test(tabSrc), 'MCP 组带管理入口');
  assert.match(tabSrc, /data-cg=\{`manage-\$\{target\}`\}/, '入口有稳定 data-cg 钩子');
  assert.match(tabSrc, /onOpenCatalog \? /, '无 onOpenCatalog 的调用方（演示态等）不渲染入口');
});

test('① 空态入口（2026-12 用户反馈：空组不能只有 hint——「还没有…——去建一个」可点）', () => {
  // 两个分组（技能 / MCP）空时都渲染 EmptyEntry，而非裸 Chips。
  assert.match(tabSrc, /skillChips\.length > 0 \?[\s\S]*?EmptyEntry target="skills" label="还没有技能——去建一个"/,
    '技能组空态入口');
  assert.match(tabSrc, /mcpChips\.length > 0 \?[\s\S]*?EmptyEntry target="mcp" label="还没有 MCP 服务器——去建一个"/,
    'MCP 组空态入口');
  // 工具组（2026-12 白名单完全可配）换成 ToolCardPicker——目录空时组件自带
  // 「没有匹配的工具」空态，不再需要 EmptyEntry（可选面是全目录，不再是
  // 「去建一个自定义工具」的引导语义）。
  assert.match(tabSrc, /<ToolCardPicker/, '工具组空态由 ToolCardPicker 兜住');
  // 空态走同一条 onOpenCatalog 通路，data-cg 钩子同款命名。
  assert.match(tabSrc, /data-cg=\{`empty-\$\{target\}`\}/, '空态入口有稳定 data-cg 钩子');
  assert.match(tabSrc, /className="ag-chip-empty"/, '空态入口有样式类');
});

test('② demo 数据源：mcpServers 透明往返（演示态自管内存对象）', async () => {
  const { DemoAgent } = await import('../src/agent/demo/index.ts');
  const demo = new DemoAgent();
  await demo.assistantSetConfig({ name: '小灵', mcpServers: ['filesystem', 'web-search'] });
  const cfg = await demo.assistantGetConfig();
  assert.deepEqual(cfg.mcpServers, ['filesystem', 'web-search'], 'mcpServers 原样保留');
  await demo.assistantSetConfig({ name: '小灵' });
  assert.equal((await demo.assistantGetConfig()).mcpServers, undefined, '全量替换：不带 = 清空');
});

test('③ WSAgent wire：get 解析 mcpServers、set 透传 mcpServers 键', () => {
  const src = fs.readFileSync(new URL('../src/agent/ws/assistant.ts', import.meta.url), 'utf8');
  assert.match(src, /Array\.isArray\(cfg\.mcpServers\) && cfg\.mcpServers\.length[\s\S]*?mcpServers: cfg\.mcpServers\.map\(String\)/,
    'get 的 mcpServers 解析守卫');
  assert.match(src, /cfg\.mcpServers\?\.length \? \{ mcpServers: cfg\.mcpServers \} : \{\}/,
    'set 的 mcpServers 透传');
});

test('③ shared 类型：AssistantConfig.mcpServers（可选 string[]）', () => {
  const src = fs.readFileSync(new URL('../src/shared/types.ts', import.meta.url), 'utf8');
  assert.match(src, /mcpServers\?: string\[];/, '类型字段在场');
});
