// 协议定制与默认归一的测试——defaultProtocol / 保存归一语义。
import test from 'node:test';
import assert from 'node:assert/strict';
import { defaultProtocol } from '../src/shared/agent-protocol.ts';

test('主 Agent 默认协议包含调度语义', () => {
  const p = defaultProtocol(true);
  assert.match(p, /主 Agent/);
  assert.match(p, /agent\.dispatch/);
  assert.match(p, /验收/);
});

test('子 Agent 默认协议包含执行语义', () => {
  const p = defaultProtocol(false);
  assert.match(p, /执行 Agent/);
  assert.match(p, /白名单/);
  assert.match(p, /如实回传/);
});

test('两类协议不同且非空', () => {
  const main = defaultProtocol(true);
  const sub = defaultProtocol(false);
  assert.notEqual(main, sub);
  assert.ok(main.length > 50 && sub.length > 50);
});
