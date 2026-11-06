// 协议定制与默认归一的测试——defaultProtocol / 保存归一语义。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { defaultProtocol } from '../src/shared/agent-protocol.ts';

test('主 Agent 默认协议包含调度语义', () => {
  const p = defaultProtocol('main');
  assert.match(p, /主 Agent/);
  // 提示词里点名的调度工具 id 必须与注册表/前端判定一致（改名点号事故的后遗症）
  assert.match(p, /agent_dispatch/);
  assert.match(p, /验收/);
});

test('子 Agent 默认协议包含执行语义', () => {
  const p = defaultProtocol('sub');
  assert.match(p, /执行 Agent/);
  assert.match(p, /白名单/);
  assert.match(p, /如实回传/);
});

test('两类协议不同且非空', () => {
  const main = defaultProtocol('main');
  const sub = defaultProtocol('sub');
  assert.notEqual(main, sub);
  assert.ok(main.length > 50 && sub.length > 50);
});

test('助手协议含 MCP 管理段（工具点名 + 装前确认 + 自动绑定）', () => {
  const p = defaultProtocol('assistant');
  // 与后端 compose_test 的关键词钉子同族：助手知道自己有 mcp_manage、
  // 用户说「装个 xxx 的 MCP」时用它（不派发）、add 前复述 command/url、
  // 新装默认自动绑定。
  for (const kw of ['mcp_manage', 'MCP 服务器', '复述 command/url', '自动绑定']) {
    assert.match(p, new RegExp(kw.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `助手协议缺关键词 ${kw}`);
  }
});

// ---------- 前后端协议文本逐字一致（对齐测试）----------
// 默认协议文本此前靠人工双份同步（Go compose.go 与本文件各一份），
// 漂移无告警——从 compose.go 的源码里提取 mainProtocol/subProtocol 的
// 字符串字面量逐字比对，双份不同步即爆。

test('前后端默认协议逐字一致（Go compose.go ↔ agent-protocol.ts）', async () => {
  const goSrc = fs.readFileSync(
    new URL('../../internal/agent/compose.go', import.meta.url), 'utf8',
  );
  // assistantProtocol（2026-12 工具白名单完全可配）带 allow 参数、边界句是
  // 局部变量（按白名单条件化）：提取固定行 + **默认态边界句**（boundary :=
  // 的赋值——只读面文本）；写类态句（if 分支）不进镜像（前端不组装协议，
  // 镜像固定为默认态——见 agent-protocol.ts 头注）。
  const extract = (fn) => {
    const sig = fn === 'assistantProtocol'
      ? `func ${fn}(allow map[string]bool) string {`
      : `func ${fn}() string {`;
    const start = goSrc.indexOf(sig);
    assert.ok(start >= 0, `Go 侧缺少 ${fn}`);
    const end = goSrc.indexOf('\n}\n', start);
    assert.ok(end > start, `Go 侧 ${fn} 结尾未找到`);
    const body = goSrc.slice(start, end);
    // 提取 strings.Join 的字符串切片字面量
    const lines = [...body.matchAll(/^\s*"((?:[^"\\]|\\.)*)",$/gm)].map((m) => m[1]);
    assert.ok(lines.length > 0, `Go 侧 ${fn} 中未提取到字符串字面量`);
    if (fn === 'assistantProtocol') {
      const m = body.match(/boundary := "((?:[^"\\]|\\.)*)"/);
      assert.ok(m, 'Go 侧 assistantProtocol 应有默认态边界句（boundary :=）');
      lines.push(m[1]);
    }
    return lines.map((s) => s.replace(/\\"/g, '"').replace(/\\\\/g, '\\')).join('\n');
  };
  const goMain = extract('mainProtocol');
  const goSub = extract('subProtocol');
  const goAssistant = extract('assistantProtocol');
  assert.equal(defaultProtocol('main'), goMain, '主协议文本与 Go 侧不一致——两份必须逐字同步');
  assert.equal(defaultProtocol('sub'), goSub, '子协议文本与 Go 侧不一致——两份必须逐字同步');
  // assistant 协议（2026-12 已落地）：与 compose.go 的 assistantProtocol 逐字同步。
  assert.equal(defaultProtocol('assistant'), goAssistant, 'assistant 协议文本与 Go 侧不一致');
  assert.notEqual(defaultProtocol('assistant'), goSub, 'assistant 不该再回落 sub（助理不是纯执行者）');
});
