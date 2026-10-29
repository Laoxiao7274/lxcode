// 可组装 Agent 名单的纯函数测试：有效委派名单的合成语义
// （会话覆盖 ?? 名单默认 ∩ 启用子 Agent——三处 UI 共用，语义漂移会直接影响
// 主 Agent 的调度面，值得钉住）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { effectiveDelegates } from '../src/shared/agent-delegation.ts';

const sub = (id, enabled = true) => ({ id, enabled, isMain: false, delegates: [] });
const mainOf = (delegates) => ({ id: 'main', isMain: true, enabled: true, delegates });

test('无会话覆盖时使用名单默认（∩ 启用的子 Agent）', () => {
  const agents = [mainOf(['a', 'b', 'c']), sub('a'), sub('b', false), sub('c')];
  assert.deepEqual(effectiveDelegates(agents, null), ['a', 'c']);
});

test('会话覆盖替换名单默认，同样过滤停用条目', () => {
  const agents = [mainOf(['a']), sub('a'), sub('b', false), sub('c')];
  assert.deepEqual(effectiveDelegates(agents, ['a', 'b', 'c']), ['a', 'c']);
});

test('空覆盖数组 = 本轮不可委派（不是回落默认）', () => {
  const agents = [mainOf(['a']), sub('a')];
  assert.deepEqual(effectiveDelegates(agents, []), []);
});

test('名单默认指向已删除的 Agent 时自动出局', () => {
  const agents = [mainOf(['a', 'ghost']), sub('a')];
  assert.deepEqual(effectiveDelegates(agents, null), ['a']);
});

test('没有主 Agent 时得到空名单', () => {
  assert.deepEqual(effectiveDelegates([sub('a')], null), []);
  assert.deepEqual(effectiveDelegates([sub('a')], ['a']), []);
});
