// 调度工具 id 的跨端一致性守卫。
//
// 事故背景（2026-09-23）：工具名 `agent.dispatch` 的点号违反 OpenAI/Anthropic 的
// 工具名字符集 `^[a-zA-Z0-9_-]{1,64}$`，严格网关 400 拒收**整轮**。改成
// `agent_dispatch` 后，前端有两处必须跟着走：MAIN_TOOL.id（演示名单）与 store 的
// 渲染判定（调度调用不建工具行，否则同一个调度会渲染两遍）。两处散落字面量——
// 这个文件把它们钉在一起，顺带钉住字符集，防止再引入点号。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
import { MAIN_TOOL } from '../src/shared/agent-seeds.ts';

/** OpenAI 与 Anthropic 的工具名约束。 */
const TOOL_NAME_PATTERN = /^[a-zA-Z0-9_-]{1,64}$/;

const base = {
  blocks: [],
  busy: true,
  pending: null,
  todos: [],
  currentId: '',
  operationError: null,
  context: null,
};

test('调度工具 id 必须匹配工具名字符集（点号会被严格网关 400 拒收）', () => {
  assert.match(MAIN_TOOL.id, TOOL_NAME_PATTERN);
});

test('MAIN_TOOL.id 的调用不建工具行，普通工具建（两处字面量必须一致）', () => {
  const dispatch = reduce(base, { type: 'toolCall', id: 'c1', name: MAIN_TOOL.id, arguments: '{}' });
  assert.equal(
    dispatch.blocks.filter((b) => b.kind === 'tool').length,
    0,
    `${MAIN_TOOL.id} 不该建工具行——store 里的判定字符串与 MAIN_TOOL.id 不一致了`,
  );
  // 对照：普通工具必须建行（否则上一条可能是"什么都不建"而假绿）
  const normal = reduce(base, { type: 'toolCall', id: 'c2', name: 'read_file', arguments: '{}' });
  assert.equal(normal.blocks.filter((b) => b.kind === 'tool').length, 1);
});
