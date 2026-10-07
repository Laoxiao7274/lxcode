import test from 'node:test';
import assert from 'node:assert/strict';
import { mapModels, modelForProvider, applyModelPatch, applyProviderPatch, validateProviderPatch, catalogMetadata, catalogTags } from '../src/shared/settings-models.ts';
import { kfmtLimit, kfmtTokens } from '../src/shared/format.ts';
import { sourceMode } from '../src/agent/source-mode.ts';
const entry = { id: 'one', model: 'model-one', base_url: 'https://gateway.test/a', api_key: 'secret', format: 'anthropic', enabled: true };
test('invalid and empty URLs remain editable without throwing; endpoint paths stay separate', () => {
  const groups = mapModels([entry, { ...entry, id: 'two', base_url: 'https://gateway.test/b' }, { ...entry, id: 'bad', base_url: 'broken' }, { ...entry, id: 'empty', base_url: '' }]);
  assert.equal(groups.length, 4);
  assert.equal(groups[0].models[0].name, 'model-one');
  assert.deepEqual(groups[0].models[0].efforts, []);
  assert.deepEqual(mapModels([]), []);
});
test('reasoning capability drives effort levels and the tag; others hide the selector', () => {
  const reasoning = mapModels([{ ...entry, capabilities: { reasoning: true } }]);
  assert.deepEqual(reasoning[0].models[0].efforts, ['minimal', 'low', 'medium', 'high']);
  assert.ok(reasoning[0].models[0].tags.includes('推理'));
  const plain = mapModels([{ ...entry, capabilities: { tools: true } }]);
  assert.deepEqual(plain[0].models[0].efforts, []);
  assert.ok(!plain[0].models[0].tags.includes('推理'));
});
test('add inherits provider connection fields, not arbitrary model metadata', () => {
  const added = modelForProvider([entry], entry.base_url, ' next ');
  assert.deepEqual(added, { id: 'next', model: 'next', base_url: entry.base_url, api_key: 'secret', format: 'anthropic', enabled: true });
  assert.throws(() => modelForProvider([entry], entry.base_url, 'one'), /已存在/);
  assert.throws(() => modelForProvider([entry], 'missing', 'x'), /不存在/);
});
test('edit applies supported fields and preserves connection and capabilities', () => {
  const patch = { id: 'one', name: 'Name', tags: ['视觉'], contextWindow: 4000, maxOutput: 1000 };
  const edited = applyModelPatch({ ...entry, capabilities: { tools: true, json_output: true } }, patch);
  assert.equal(edited.api_key, entry.api_key);
  assert.equal(edited.context_window, 4000);
  assert.deepEqual(edited.capabilities, { tools: false, vision: true, json_output: true, reasoning: false });
  // 推理标签翻转 capabilities.reasoning（模型选择器档位显隐的数据源）
  const patched = applyModelPatch(entry, { ...patch, tags: ['推理'] });
  assert.equal(patched.capabilities.reasoning, true);
  assert.throws(() => applyModelPatch(entry, { ...patch, id: 'renamed' }), /不支持/);
});
test('提供商连接配置：只改三个连接字段，模型自身的配置一个不动', () => {
  // 这条是「改 key 不用移除重加」的全部价值所在：移除重加会把窗口/输出/能力丢掉。
  const full = { ...entry, display_name: '网关 · 主力', context_window: 200_000, max_output_tokens: 8_192,
    capabilities: { tools: true, vision: true, json_output: true, reasoning: true } };
  const out = applyProviderPatch(full, { baseUrl: '  https://new.test/v1  ', apiKey: ' sk-new ', format: 'openai' });
  assert.equal(out.base_url, 'https://new.test/v1'); // 首尾空白去掉
  assert.equal(out.api_key, 'sk-new');
  assert.equal(out.format, 'openai');
  // 其余字段逐项不变（display_name 是模型级的，不该被提供商级改动碰到）
  assert.equal(out.display_name, full.display_name);
  assert.equal(out.context_window, 200_000);
  assert.equal(out.max_output_tokens, 8_192);
  assert.deepEqual(out.capabilities, full.capabilities);
  assert.equal(out.enabled, true);
  assert.equal(out.model, full.model);
  assert.equal(out.id, full.id);
  // 清空 key 是合法操作（该端点不需要 key），不是错误
  assert.equal(applyProviderPatch(full, { baseUrl: 'https://new.test', apiKey: '', format: 'openai' }).api_key, '');
});

test('提供商连接配置校验：地址与格式的边界，错误要说人话', () => {
  const ok = { baseUrl: 'https://api.test/v1', apiKey: '', format: 'openai' };
  assert.equal(validateProviderPatch(ok), null);
  assert.equal(validateProviderPatch({ ...ok, format: 'anthropic' }), null);
  assert.match(validateProviderPatch({ ...ok, baseUrl: '   ' }), /不能为空/);
  assert.match(validateProviderPatch({ ...ok, baseUrl: 'api.test/v1' }), /不是合法地址/);
  assert.match(validateProviderPatch({ ...ok, baseUrl: 'ftp://api.test' }), /http\(s\)/);
  // "https://" 连 URL 都解析不出来 → 走「不是合法地址」那条（比"缺 host"更准确）
  assert.match(validateProviderPatch({ ...ok, baseUrl: 'https://' }), /不是合法地址/);
  // "https:///path" 实测被 WHATWG 解析成 host="path"（多余斜杠被吸收）→ 放行是对的，
  // 后端 url.Parse 同样认它；这条断言钉住「前后端判据一致」而不是自造更严的规则。
  assert.equal(validateProviderPatch({ ...ok, baseUrl: 'https:///path' }), null);
  assert.match(validateProviderPatch({ ...ok, format: 'gemini' }), /openai 或 anthropic/);
});

test('提供商的连接配置来自分组内首个条目（分组依据是完整 URL）', () => {
  const [p] = mapModels([entry]);
  assert.equal(p.baseUrl, 'https://gateway.test/a');
  assert.equal(p.apiKey, 'secret');
  assert.equal(p.format, 'anthropic');
  // 没填 key 的老条目回落空串（不是 undefined——编辑框是受控输入）
  const [noKey] = mapModels([{ ...entry, api_key: undefined, format: undefined }]);
  assert.equal(noKey.apiKey, '');
  assert.equal(noKey.format, 'openai');
});

test('browser is explicitly live without Electron and demo remains default', () => {
  assert.equal(sourceMode('Chrome', ''), 'demo');
  assert.equal(sourceMode('Chrome', '?mode=live'), 'live');
  assert.equal(sourceMode('Electron', ''), 'live');
  assert.equal(sourceMode('Electron', '?mode=demo'), 'demo');
  assert.equal(sourceMode('', '?mode=unknown'), 'demo');
});

test('未配窗口/输出显示「未知」而不是编一个数字', () => {
  // 旧实现硬兜 128k/8k：窗口未知的模型看起来像有 128k，而压缩其实不触发。
  const [p] = mapModels([{ ...entry, context_window: undefined, max_output_tokens: undefined }]);
  assert.equal(p.models[0].contextWindow, 0);
  assert.equal(p.models[0].maxOutput, 0);
  assert.equal(kfmtLimit(0), '未知');
  assert.equal(kfmtLimit(128_000), '128k');
  assert.equal(kfmtLimit(1000), '1k');
  // kfmtTokens 自身不变（上下文指示器还在用它）
  assert.equal(kfmtTokens(0), '0');
});

test('目录元数据不猜：输出上限不小于窗口时留空', () => {
  // 目录里 14%（932/6554）的条目把输出报得 >= 窗口，后端 validate 硬拒这种组合
  // （输入+输出会超限）——照抄会让这些模型整条加不进去。
  const ok = catalogMetadata({ id: 'a', context_window: 128_000, max_output_tokens: 8_000, tools: true, reasoning: true });
  assert.equal(ok.context_window, 128_000);
  assert.equal(ok.max_output_tokens, 8_000);
  assert.deepEqual(ok.capabilities, { tools: true, vision: false, json_output: false, reasoning: true });

  const equal = catalogMetadata({ id: 'b', context_window: 262_144, max_output_tokens: 262_144 });
  assert.equal(equal.context_window, 262_144);
  assert.equal(equal.max_output_tokens, undefined, '输出 == 窗口时必须留空');

  const over = catalogMetadata({ id: 'c', context_window: 128_000, max_output_tokens: 262_144 });
  assert.equal(over.max_output_tokens, undefined, '输出 > 窗口时必须留空');

  // 目录没给窗口但给了输出：窗口留空，输出照填（后端只校验「两者都有时」的大小关系）
  const partial = catalogMetadata({ id: 'd', max_output_tokens: 4_000 });
  assert.equal(partial.context_window, undefined);
  assert.equal(partial.max_output_tokens, 4_000);

  const empty = catalogMetadata({ id: 'e' });
  assert.equal(empty.context_window, undefined);
  assert.equal(empty.max_output_tokens, undefined);
  assert.deepEqual(empty.capabilities, { tools: false, vision: false, json_output: false, reasoning: false });
});

test('目录标签措辞与注册表行逐字一致', () => {
  // 候选行与已注册行的标签必须能对上：勾选时看到「工具/推理」，加进去不能变成别的字。
  assert.deepEqual(catalogTags({ id: 'a', tools: true, reasoning: true }), ['工具', '推理']);
  assert.deepEqual(catalogTags({ id: 'b', vision: true, tools: true }), ['工具', '视觉']);
  assert.deepEqual(catalogTags({ id: 'c' }), []);
  const [p] = mapModels([{ ...entry, capabilities: { tools: true, vision: true, reasoning: true } }]);
  assert.deepEqual(p.models[0].tags, catalogTags({ id: 'x', tools: true, vision: true, reasoning: true }));
});

test('探测结果与目录模型同构：DiscoveredModel 直接复用 catalogMetadata 映射', () => {
  // 后端探测时按 id 从目录回填了同款字段（best-effort，缺省 = 未知）；前端
  // addDiscovered 拿同一份映射写注册表——结构化契约漂移了这里先炸。
  const discovered = { id: 'glm-5.3-flash', name: 'GLM-5.3 Flash', context_window: 128_000, max_output_tokens: 8_192, tools: true, vision: true, reasoning: true };
  const meta = catalogMetadata(discovered);
  assert.equal(meta.context_window, 128_000);
  assert.equal(meta.max_output_tokens, 8_192);
  assert.deepEqual(meta.capabilities, { tools: true, vision: true, json_output: false, reasoning: true });

  // 目录查不到的探测结果（自建端点自定义模型）：保持未知，一个数都不编。
  const bare = catalogMetadata({ id: 'custom-local' });
  assert.equal(bare.context_window, undefined);
  assert.equal(bare.max_output_tokens, undefined);
  assert.deepEqual(bare.capabilities, { tools: false, vision: false, json_output: false, reasoning: false });
});
