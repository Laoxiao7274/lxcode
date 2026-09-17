import test from 'node:test';
import assert from 'node:assert/strict';
import { mapModels, modelForProvider, applyModelPatch } from '../src/shared/settings-models.ts';
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
test('browser is explicitly live without Electron and demo remains default', () => {
  assert.equal(sourceMode('Chrome', ''), 'demo');
  assert.equal(sourceMode('Chrome', '?mode=live'), 'live');
  assert.equal(sourceMode('Electron', ''), 'live');
  assert.equal(sourceMode('Electron', '?mode=demo'), 'demo');
  assert.equal(sourceMode('', '?mode=unknown'), 'demo');
});
