// MCP 服务器卡的状态判定。
//
// 钉住的核心区分：**enabled ≠ 已连接**。enabled 是「用户想开」（磁盘上的意图），
// 连接状态是后端运行时报的——把前者当后者显示，用户会在命令根本不存在（npx 没装）
// 时看到绿色的「已连接」，然后困惑为什么工具调不动。
import test from 'node:test';
import assert from 'node:assert/strict';
import { mcpStatusPill } from '../src/shared/mcp-status.ts';

const rt = (status, extra = {}) => ({ status, toolCount: 0, lastError: '', stderr: '', ...extra });

test('mcpStatusPill：连上了就说已连接、能力可用', () => {
  const p = mcpStatusPill(true, rt('connected'));
  assert.equal(p.text, '已连接');
  assert.equal(p.note, '能力可用');
  assert.equal(p.cls, 'risk-low');
});

test('mcpStatusPill：用户想开但连不上 → 连接失败（不能说已连接，也不能说已停止）', () => {
  // 关键回归：enabled=true 而 status=error——本轮之前这里显示的是绿色的「已连接」。
  const p = mcpStatusPill(true, rt('error'));
  assert.equal(p.text, '连接失败');
  assert.equal(p.note, '能力不可用');
  assert.notEqual(p.cls, 'risk-low');
});

test('mcpStatusPill：停用是**有意**的，与故障分开表述', () => {
  const p = mcpStatusPill(false, rt('stopped'));
  assert.equal(p.text, '已停止');
  assert.equal(p.note, '能力挂起');
});

test('mcpStatusPill：enabled 但状态是 stopped（还没对账到）→ 未连接', () => {
  const p = mcpStatusPill(true, rt('stopped'));
  assert.equal(p.text, '未连接');
  assert.equal(p.note, '能力挂起');
});

test('mcpStatusPill：没有运行期状态时回落到按 enabled 显示（老后端/演示态）', () => {
  // 兼容姿势（AGENTS.md §5 坑 11）：后端没热重载时不返回 status——
  // 此时行为必须与加运行期状态之前逐字一致。
  assert.equal(mcpStatusPill(true, undefined).text, '已连接');
  assert.equal(mcpStatusPill(true, undefined).note, '能力可用');
  assert.equal(mcpStatusPill(false, undefined).text, '未连接');
  assert.equal(mcpStatusPill(false, undefined).note, '能力挂起');
});
