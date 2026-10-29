// 模块目录导入/导出格式 v1 的校验测试——与 tool-import 对称的固定契约。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseModuleImport, serializeModuleExport } from '../src/shared/module-import.ts';

const doc = (modules) => JSON.stringify({ version: 1, modules });

test('合法导入：必填齐全，导入条目带 custom 标记', () => {
  const r = parseModuleImport(doc([{
    id: "deploy-checklist", desc: " 摘要 ", kind: "process", body: "# 正文",
  }]), new Set());
  assert.ok(r.ok);
  assert.equal(r.modules[0].id, "deploy-checklist");
  assert.equal(r.modules[0].desc, "摘要");
  assert.equal(r.modules[0].custom, true);
});

test('version / 数组 / 空数组的顶层契约', () => {
  assert.match(parseModuleImport('{"version":2,"modules":[]}', new Set()).error, /version/);
  assert.match(parseModuleImport('{"version":1,"modules":"x"}', new Set()).error, /数组/);
  assert.match(parseModuleImport('{"version":1,"modules":[]}', new Set()).error, /不能为空/);
  assert.match(parseModuleImport('not json', new Set()).error, /JSON/);
});

test('id 查重：目录冲突与批次内重复都拒绝', () => {
  assert.match(parseModuleImport(doc([{ id: "gsap", desc: "x", kind: "skill", body: "b" }]), new Set(["gsap"])).error, /已存在/);
  const r = parseModuleImport(doc([
    { id: "a", desc: "x", kind: "skill", body: "b" },
    { id: "a", desc: "x", kind: "skill", body: "b" },
  ]), new Set());
  assert.match(r.error, /已存在/);
});

test('kind 枚举与必填字段定位到条目下标', () => {
  const base = { id: "t", desc: "x", body: "b" };
  assert.match(parseModuleImport(doc([{ ...base, kind: "flow" }]), new Set()).error, /kind/);
  assert.match(parseModuleImport(doc([{ desc: "x", kind: "skill", body: "b" }]), new Set()).error, /modules\[0\].id/);
  assert.match(parseModuleImport(doc([{ id: "t", kind: "skill", body: "b" }]), new Set()).error, /desc/);
  assert.match(parseModuleImport(doc([{ id: "t", desc: "x", kind: "skill" }]), new Set()).error, /body/);
});

test('导出序列化：v1 形状、只含四个内容字段（不带 custom）', () => {
  const text = serializeModuleExport([
    { id: "a", desc: "d", kind: "process", body: "# b", custom: true },
    { id: "b", desc: "d2", kind: "skill", body: "# b2" },
  ]);
  const back = JSON.parse(text);
  assert.equal(back.version, 1);
  assert.deepEqual(back.modules[0], { id: "a", desc: "d", kind: "process", body: "# b" });
  // 往返：导出的文本能直接再导入
  const r = parseModuleImport(text, new Set());
  assert.ok(r.ok);
  assert.equal(r.modules.length, 2);
});
