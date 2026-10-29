// 搜索渠道面板的两个纯判定：acceptsKey / hasStoredConfig。
//
// 它们各自对应一个真实缺陷：
//   - acceptsKey：Exa 缺 key 也就绪（免配置通道），但有 key 走直连 API——
//     若面板只看 needs_key 就会把输入框藏掉，用户永远进不了直连路径；
//   - hasStoredConfig：Exa 这类刻意默认就绪的渠道不填任何东西也是就绪的，
//     若拿 configured 当判据，「清除」按钮会出现在一张空卡片上。
import test from 'node:test';
import assert from 'node:assert/strict';
import { acceptsKey, hasStoredConfig } from '../src/shared/search-channels.ts';

test('acceptsKey：需要 key 的渠道当然接受 key', () => {
  assert.equal(acceptsKey({ needs_key: true }), true);
  assert.equal(acceptsKey({ needs_key: true, accepts_key: true }), true);
});

test('acceptsKey：Exa 形态——不缺 key 但接受 key（输入框必须还在）', () => {
  assert.equal(acceptsKey({ needs_key: false, accepts_key: true }), true);
});

test('acceptsKey：不接受 key 的渠道不显示输入框', () => {
  assert.equal(acceptsKey({ needs_key: false, accepts_key: false }), false);
  assert.equal(acceptsKey({ needs_key: false }), false);
});

test('acceptsKey：老后端缺字段时回落到 needs_key', () => {
  // 未热重载的后端不返回 accepts_key（AGENTS.md §5 坑 11）——
  // 此时必须与加这个字段之前的行为逐字一致。
  assert.equal(acceptsKey({ needs_key: true }), true);
  assert.equal(acceptsKey({ needs_key: false }), false);
});

test('hasStoredConfig：后端给了 stored 就以它为准', () => {
  assert.equal(hasStoredConfig({ stored: true }), true);
  // 关键回归：Exa 卡片的 options 里有**默认值**（mcp_url），但磁盘上没有条目——
  // 曾经因为「options 有值」而误判成配过，空卡片上出现了「清除」按钮。
  assert.equal(
    hasStoredConfig({
      stored: false,
      options: { mcp_url: 'https://mcp.exa.ai/mcp' },
      option_specs: [{ key: 'mcp_url', label: '免配置 MCP 端点', default: 'https://mcp.exa.ai/mcp' }],
    }),
    false,
  );
});

test('hasStoredConfig：老后端回落——有 key 或地址就算配过', () => {
  assert.equal(hasStoredConfig({ api_key: 'sk-1' }), true);
  assert.equal(hasStoredConfig({ base_url: 'https://x.example.com' }), true);
});

test('hasStoredConfig：老后端回落——设置项与声明默认值不同才算配过', () => {
  const specs = [{ key: 'zone', label: 'SERP zone', required: true }];
  // Brightdata 只填了 zone：值存在且声明里没有默认值 → 算配过
  assert.equal(hasStoredConfig({ option_specs: specs, options: { zone: 'my_zone' } }), true);
  // 空串/纯空白不算填过——面板里清空输入框再保存就是这个形态
  assert.equal(hasStoredConfig({ option_specs: specs, options: { zone: '' } }), false);
  assert.equal(hasStoredConfig({ option_specs: specs, options: { zone: '   ' } }), false);
  // 值等于声明默认值 → 用户没改过，不算配过
  const withDefault = [{ key: 'mcp_url', label: '端点', default: 'https://mcp.exa.ai/mcp' }];
  assert.equal(
    hasStoredConfig({ option_specs: withDefault, options: { mcp_url: 'https://mcp.exa.ai/mcp' } }),
    false,
  );
  assert.equal(
    hasStoredConfig({ option_specs: withDefault, options: { mcp_url: 'https://self.example.com/mcp' } }),
    true,
  );
});

test('hasStoredConfig：什么都没填就是没有可清的东西（Exa 空卡片不该有清除按钮）', () => {
  assert.equal(hasStoredConfig({}), false);
  assert.equal(hasStoredConfig({ api_key: '', base_url: '', options: {} }), false);
  // 显式 null/undefined 也要安全（wire 上这些键都是 omitempty）
  assert.equal(hasStoredConfig({ options: undefined }), false);
});
