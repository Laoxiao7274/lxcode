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

// ---------- 前后端协议文本逐字一致（对齐测试）----------
// 默认协议文本此前靠人工双份同步（Go compose.go 与本文件各一份），
// 漂移无告警——从 compose.go 的源码里提取 mainProtocol/subProtocol 的
// 字符串字面量逐字比对，双份不同步即爆。

test('前后端默认协议逐字一致（Go compose.go ↔ agent-protocol.ts）', async () => {
  const goSrc = fs.readFileSync(
    new URL('../../internal/agent/compose.go', import.meta.url), 'utf8',
  );
  const extract = (fn) => {
    const start = goSrc.indexOf(`func ${fn}() string {`);
    assert.ok(start >= 0, `Go 侧缺少 ${fn}`);
    const end = goSrc.indexOf('\n}\n', start);
    assert.ok(end > start, `Go 侧 ${fn} 结尾未找到`);
    const body = goSrc.slice(start, end);
    // 提取 strings.Join 的字符串切片字面量
    const lines = [...body.matchAll(/^\s*"((?:[^"\\]|\\.)*)",$/gm)].map((m) => m[1]);
    assert.ok(lines.length > 0, `Go 侧 ${fn} 中未提取到字符串字面量`);
    return lines.map((s) => s.replace(/\\"/g, '"').replace(/\\\\/g, '\\')).join('\n');
  };
  const goMain = extract('mainProtocol');
  const goSub = extract('subProtocol');
  assert.equal(defaultProtocol('main'), goMain, '主协议文本与 Go 侧不一致——两份必须逐字同步');
  assert.equal(defaultProtocol('sub'), goSub, '子协议文本与 Go 侧不一致——两份必须逐字同步');
  // assistant 占位回落 sub 文本（后续功能提交再定）
  assert.equal(defaultProtocol('assistant'), goSub);
});
