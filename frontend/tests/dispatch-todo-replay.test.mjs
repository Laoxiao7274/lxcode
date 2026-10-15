// 回放路径的清单终态恢复：历史里没有 dispatchEnd 事件可吃 finalTodos——
// 从后端写进结果的「子会话清单最终状态：N/M 完成」行恢复定格清单。
// 恢复不了的分支（没建过清单 / 初稿条数对不上 / 点名段与计数矛盾）必须
// 保持初稿不动——宁缺毋滥，不许半真半假的清单上卡。
import test from 'node:test';
import assert from 'node:assert/strict';
import { splitDispatchResult, replayDispatchTodos } from '../src/shared/history.ts';

const DRAFT = [
  { content: '定位问题', status: 'pending' },
  { content: '修复', status: 'pending' },
  { content: '补测试', status: 'pending' },
];

test('全部完成的终态行 → 初稿全部标 done', () => {
  const raw = '结论正文\n\n子会话清单最终状态：3/3 完成。\n\n[子会话 id: c1 —— …]';
  assert.deepEqual(replayDispatchTodos(raw, DRAFT), [
    { content: '定位问题', status: 'done' },
    { content: '修复', status: 'done' },
    { content: '补测试', status: 'done' },
  ]);
});

test('带残留的终态行 → 点名项按括号注记改状态，其余 done', () => {
  const raw = '结论\n\n子会话清单最终状态：1/3 完成，未完成：修复（进行中）、补测试（待办）';
  assert.deepEqual(replayDispatchTodos(raw, DRAFT), [
    { content: '定位问题', status: 'done' },
    { content: '修复', status: 'active' },
    { content: '补测试', status: 'pending' },
  ]);
});

test('没有终态行（老历史/错误结果）→ undefined，保持初稿', () => {
  assert.equal(replayDispatchTodos('只有结论，没有清单行', DRAFT), undefined);
});

test('没建过清单（无初稿）→ undefined（无从恢复，不编内容）', () => {
  const raw = '结论\n\n子会话清单最终状态：2/2 完成。';
  assert.equal(replayDispatchTodos(raw, undefined), undefined);
  assert.equal(replayDispatchTodos(raw, []), undefined);
});

test('初稿条数与终态对不上（子会话重写过清单）→ undefined，保持初稿', () => {
  const raw = '结论\n\n子会话清单最终状态：2/2 完成。';
  assert.equal(replayDispatchTodos(raw, DRAFT), undefined, '初稿 3 项 ≠ 终态 2 项');
});

test('点名数与 done 计数矛盾（文本截断/格式漂移）→ undefined', () => {
  const raw = '结论\n\n子会话清单最终状态：1/3 完成，未完成：修复（进行中）'; // 应有 2 个残留
  assert.equal(replayDispatchTodos(raw, DRAFT), undefined);
});

test('splitDispatchResult 不受终态行影响（既有行为回归）', () => {
  const raw = '结论正文\n\n子会话清单最终状态：3/3 完成。\n\n[子会话 id: c1 —— 只在没做完时填]';
  const { body, sessionId } = splitDispatchResult(raw);
  assert.equal(sessionId, 'c1');
  assert.ok(body.includes('子会话清单最终状态'), '终态行属于结论正文（模型验收输入）');
  assert.ok(!body.includes('[子会话 id:'), '续跑提示行剥掉');
});
