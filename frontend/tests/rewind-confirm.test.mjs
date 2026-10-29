// 「回到这里 + 还原文件」的二次确认文案（components/panels/rewind-confirm.ts）。
//
// 为什么值得单独钉住（docs/session-tree.md §G #6 / §H #1，2026-12 用户拍板）：
// `bash`（含子 Agent 里的 bash）改的文件**回滚不了**。文案必须带**具体数字**：
// 只说「部分改动无法还原」用户没法判断要不要继续；而说漏了这一句，用户会以为
// 「回到这里」把工作区也还原干净了——这是本功能最容易被骗的一处。
//
// 三段骨架（固定文案，只填数字）：
//   ① 将把对话回到第 N 轮之前，并还原 X 个文件（含 Y 个子 Agent 的改动，涉及 Z 个子会话）
//   ② 另有 M 处改动无法还原（bash / MCP / 自定义工具）——这些文件会保持现状
//   ③ 子会话的对话历史不会回滚，它们仍可继续
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  conflictHeadline,
  conflictLines,
  restoreSummary,
  rewindConfirmLines,
} from '../src/components/panels/rewind-confirm.ts';

// ---------- 1. 三段骨架：数字都来自 plan ----------

test('① 三段结构：还原数 / 无法还原数 / 子会话历史不动', () => {
  const lines = rewindConfirmLines({ turn: 7, files: 3, childFiles: 1, childSessions: 2, external: 4 });
  assert.equal(lines.length, 3, '三段（有无法还原的改动时）');
  assert.match(lines[0], /第 7 轮之前/, '① 带轮次号');
  assert.match(lines[0], /还原 3 个文件/, '① 带可还原的文件数');
  assert.match(lines[0], /含 1 个子 Agent 的改动/, '① 带子 Agent 的改动数');
  assert.match(lines[0], /涉及 2 个子会话/, '① 带涉及的子会话数');
  assert.match(lines[1], /另有 4 处改动无法还原/, '② 带无法还原的处数');
  assert.match(lines[1], /bash/, '② 点名 bash（这是用户能理解的具体来源）');
  assert.match(lines[2], /子会话的对话历史不会回滚/, '③ 说清子会话历史不动');
});

test('② bash 处数为 0 时整段省略（没有不可还原的东西就不吓人）', () => {
  const lines = rewindConfirmLines({ turn: 3, files: 2, external: 0 });
  assert.equal(lines.length, 2, '② 省略，只剩 ① 与 ③');
  assert.ok(!lines.some((l) => /无法还原/.test(l)), '不该出现「无法还原」');
});

test('② 有 bash 处数时必须出现（这是本功能的诚实边界）', () => {
  const lines = rewindConfirmLines({ turn: 1, files: 1, external: 1 });
  assert.equal(lines.length, 3);
  assert.match(lines[1], /1 处改动无法还原/);
  assert.match(lines[1], /保持现状/, '说清这些文件会保持现状（而不是"可能没还原"）');
});

// ---------- 2. 边界：0 与未知 ----------

test('可还原文件数为 0：如实说「这一段没有可还原的文件改动」', () => {
  const lines = rewindConfirmLines({ turn: 2, files: 0, external: 2 });
  assert.match(lines[0], /这一段没有可还原的文件改动/, '不编一个「还原 0 个文件」');
  assert.ok(!/还原 0 个文件/.test(lines[0]));
});

test('轮次未知：文案回落「这里之前」（不编一个第 0 轮）', () => {
  const lines = rewindConfirmLines({ files: 1, external: 0 });
  assert.match(lines[0], /回到这里之前/);
  assert.ok(!/第 0 轮/.test(lines[0]));
});

test('子 Agent 改动为 0：不写「含 0 个子 Agent」（那是废话）', () => {
  const lines = rewindConfirmLines({ turn: 5, files: 2, childFiles: 0, childSessions: 0, external: 0 });
  assert.ok(!/子 Agent/.test(lines[0]), '没有子 Agent 改动时不提子 Agent');
  assert.match(lines[0], /还原 2 个文件/);
});

test('子会话数未知（确认前不知道）：只说子 Agent 改动数，不编会话数', () => {
  const lines = rewindConfirmLines({ turn: 4, files: 3, childFiles: 1, external: 0 });
  assert.match(lines[0], /含 1 个子 Agent 的改动/);
  assert.ok(!/涉及/.test(lines[0]), '会话数未知时不说「涉及 N 个子会话」');
});

// ---------- 3. 冲突清单 ----------

test('冲突清单：路径 + 原因 + 谁改的；原因缺失给中性说明', () => {
  const lines = conflictLines([
    { path: 'a/b.go', reason: '磁盘内容与快照期望不符', session_id: 'child-1' },
    { path: 'c.md', reason: '' },
  ]);
  assert.equal(lines.length, 2);
  assert.equal(lines[0].path, 'a/b.go');
  assert.equal(lines[0].session, 'child-1');
  assert.equal(lines[1].reason, '磁盘内容与快照期望不符', '原因缺席时给中性说明（不猜具体原因）');
  assert.equal(lines[1].session, undefined, '没有 session 就不写这个键');
});

test('冲突标题说清「整批未执行、什么都没改」（fail-closed）', () => {
  const head = conflictHeadline([{ path: 'a', reason: 'x' }, { path: 'b', reason: 'y' }]);
  assert.match(head, /2 个文件/);
  assert.match(head, /整批未执行/);
  assert.match(head, /都没有改动/, '必须说清对话与文件都没动（否则用户以为覆盖了一半）');
});

// ---------- 4. 成功路径的摘要 ----------

test('还原摘要：只列非零项（不显示一排 0）', () => {
  assert.deepEqual(restoreSummary(undefined), [], '没有文件结果 = 什么都不说');
  assert.deepEqual(restoreSummary({ restored: [], deleted: [], skipped: [] }), [], '全 0 = 空数组');
  const lines = restoreSummary({
    restored: [{}, {}],
    deleted: [{}],
    skipped: [{}],
    child_sessions: ['c1'],
  });
  assert.match(lines[0], /还原 2 个文件/);
  assert.match(lines[1], /删除 1 个/);
  assert.match(lines[2], /1 个文件当时没抓快照/);
  assert.match(lines[3], /涉及 1 个子 Agent 的改动（它们的对话未回滚）/);
});
