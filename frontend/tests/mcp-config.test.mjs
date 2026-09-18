// MCP 配置导入解析的契约测试——YAML/JSON 双格式、字段推断、查重跳过。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseMcpConfig } from '../src/shared/mcp-config.ts';

test('YAML（mcpServers 键 + stdio 字段推断 + env）', () => {
  const r = parseMcpConfig(`
mcpServers:
  filesystem:
    description: 文件系统服务
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-filesystem"
      - /
    env:
      ROOT: /tmp
`, new Set());
  assert.ok(r.ok);
  const s = r.servers[0];
  assert.equal(s.id, "filesystem");
  assert.equal(s.transport, "stdio");
  assert.equal(s.command, "npx");
  assert.deepEqual(s.args, ["-y", "@modelcontextprotocol/server-filesystem", "/"]);
  assert.deepEqual(s.env, { ROOT: "/tmp" });
  assert.equal(s.desc, "文件系统服务");
});

test('裸顶层映射（YAML 与 JSON 都接受）——url 推断 sse', () => {
  const yml = parseMcpConfig(`
remote:
  url: https://example.com/mcp/sse
`, new Set());
  assert.ok(yml.ok);
  assert.equal(yml.servers[0].transport, "sse");
  assert.equal(yml.servers[0].url, "https://example.com/mcp/sse");
  assert.equal(yml.servers[0].desc, "导入的 MCP 服务器");

  const json = parseMcpConfig('{"mcpServers":{"a":{"command":"node"}}}', new Set());
  assert.ok(json.ok);
  assert.equal(json.servers[0].command, "node");
  assert.deepEqual(json.servers[0].args, []);
});

test('Claude Desktop 的 JSON 配置直接贴', () => {
  const r = parseMcpConfig(JSON.stringify({
    mcpServers: {
      github: { command: "npx", args: ["-y", "@mcp/github"], env: { TOKEN: "x" } },
    },
  }), new Set());
  assert.ok(r.ok);
  assert.deepEqual(r.servers[0].env, { TOKEN: "x" });
});

test('坏格式与字段错误的定位报错', () => {
  // 坏文本按结构报错（YAML 容忍裸标量——解析得出来但不是配置形状）
  assert.match(parseMcpConfig(":::not yaml:::[", new Set()).error, /顶层必须是对象/);
  assert.match(parseMcpConfig("just a scalar", new Set()).error, /顶层必须是对象/);
  assert.match(parseMcpConfig('{"mcpServers":{"a":{"foo":1}}}', new Set()).error, /缺 command/);
  assert.match(parseMcpConfig('{"mcpServers":{"a":{"command":"x","url":"https://x"}}}', new Set()).error, /不能同时有/);
  assert.match(parseMcpConfig('{"mcpServers":{"a":{"command":"x","args":[1]}}}', new Set()).error, /args/);
  assert.match(parseMcpConfig('{"mcpServers":{"a":{"command":"x","env":{"K":1}}}}', new Set()).error, /env/);
  assert.match(parseMcpConfig('{"mcpServers":{}}', new Set()).error, /没有服务器条目/);
});

test('已存在的服务器跳过（重贴配置常见工作流）——新条目照常导入', () => {
  const r = parseMcpConfig('{"mcpServers":{"filesystem":{"command":"x"},"new-one":{"command":"y"}}}', new Set(["filesystem"]));
  assert.ok(r.ok);
  assert.deepEqual(r.skipped, ["filesystem"]);
  assert.equal(r.servers.length, 1);
  assert.equal(r.servers[0].id, "new-one");
  // 全部已存在 → 失败并带 skipped
  const all = parseMcpConfig('{"mcpServers":{"filesystem":{"command":"x"}}}', new Set(["filesystem"]));
  assert.ok(!all.ok);
  assert.deepEqual(all.skipped, ["filesystem"]);
});
