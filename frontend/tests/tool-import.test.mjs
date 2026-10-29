// 工具目录导入格式 v1 的校验测试——固定契约用测试钉住
// （后端化复用同一格式，格式漂移在这里就该红）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseToolImport } from '../src/shared/tool-import.ts';

const doc = (tools) => JSON.stringify({ version: 1, tools });

test('合法导入：必填齐全 + 可选项归一', () => {
  const r = parseToolImport(doc([{
    id: "my-tool", desc: " 说明 ", risk: "high", source: "binary",
    params: [{ name: "path" }, { name: "n", type: "int", required: true, desc: "数量" }],
    doc: "# 文档",
  }]), new Set());
  assert.ok(r.ok);
  assert.equal(r.tools[0].id, "my-tool");
  assert.equal(r.tools[0].desc, "说明");
  assert.equal(r.tools[0].custom, true);
  assert.equal(r.tools[0].params[0].type, "string");
  assert.equal(r.tools[0].params[1].required, true);
});

test('version 必须是 1；tools 必须是非空数组', () => {
  assert.match(parseToolImport('{"version":2,"tools":[]}', new Set()).error, /version/);
  assert.match(parseToolImport('{"version":1,"tools":{}}', new Set()).error, /数组/);
  assert.match(parseToolImport('{"version":1,"tools":[]}', new Set()).error, /不能为空/);
  assert.match(parseToolImport('{bad json', new Set()).error, /JSON/);
});

test('id 查重：与现有目录冲突、批次内重复都拒绝', () => {
  assert.match(parseToolImport(doc([{ id: "read_file", desc: "x", risk: "low", source: "builtin" }]), new Set(["read_file"])).error, /已存在/);
  const r = parseToolImport(doc([
    { id: "a", desc: "x", risk: "low", source: "builtin" },
    { id: "a", desc: "x", risk: "low", source: "builtin" },
  ]), new Set());
  assert.match(r.error, /已存在/);
});

test('枚举字段与参数形状的定位报错；mcp 来源被拒（服务器注册生成，不走此格式）', () => {
  const base = { id: "t", desc: "x" };
  assert.match(parseToolImport(doc([{ ...base, risk: "mid", source: "builtin" }]), new Set()).error, /risk/);
  assert.match(parseToolImport(doc([{ ...base, risk: "low", source: "exe" }]), new Set()).error, /source/);
  assert.match(parseToolImport(doc([{ ...base, risk: "low", source: "mcp" }]), new Set()).error, /source/);
  assert.match(parseToolImport(doc([{ ...base, risk: "low", source: "builtin", params: [{}] }]), new Set()).error, /params\[0\].name/);
});

test('缺必填字段报错到条目下标', () => {
  assert.match(parseToolImport(doc([{ risk: "low", source: "builtin" }]), new Set()).error, /tools\[0\].id/);
  assert.match(parseToolImport(doc([{ id: "t", risk: "low", source: "builtin" }]), new Set()).error, /desc/);
});
