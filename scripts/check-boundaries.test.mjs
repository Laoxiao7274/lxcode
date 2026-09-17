import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkFrontendSource } from './check-boundaries.mjs';

test('正常视图依赖与注释不误报', () => {
  assert.deepEqual(checkFrontendSource('components/Test.tsx', '// fetch and electron examples\nimport React from "react"; import type { AgentSource } from "../shared/types";'), []);
});
test('拒绝运行时平台能力及越界导入', () => {
  for (const code of ['import fs from "node:fs";', 'import { ipcRenderer } from "electron";', 'import x from "../../internal/agent";', 'const x = import(variable);', 'window.fetch("/rpc");']) {
    assert.ok(checkFrontendSource('components/Test.tsx', code).length, code);
  }
});
test('组件只能依赖数据源抽象，工厂可选择具体适配器', () => {
  assert.ok(checkFrontendSource('shared/settings.tsx', 'import { WSAgent } from "../agent/ws";').length);
  assert.deepEqual(checkFrontendSource('agent/index.ts', 'import { WSAgent } from "./ws";'), []);
  assert.deepEqual(checkFrontendSource('agent/ws/transport.ts', 'const ws = new WebSocket("ws://localhost/rpc");'), []);
});
test('宿主桥限制在两个宿主 UI 入口', () => {
  assert.ok(checkFrontendSource('agent/ws/index.ts', 'window.__LX__.execute();').length);
  assert.deepEqual(checkFrontendSource('components/topbar/Topbar.tsx', 'window.__LX__.close();'), []);
});
