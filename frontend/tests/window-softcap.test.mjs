// 跳转扩窗软上限（2026-12 渲染性能 Top4）的钉子：
//
// Thread 的大纲跳转原先「一次撑够」：目标在 1000 块会话的开头就一次性挂 990
// 块——一帧内的 mount 风暴就是「点一下大纲卡一秒」。软上限后一次提交最多多挂
// WINDOW_SOFT_CAP（400）块，目标更远时 effect 重跑分批推进，目标最终仍要到达。
//
// 本文件钉住 expandWindowForJump（Thread 导出的纯函数）：
// 1. 单步增长 ≤ WINDOW_SOFT_CAP（一次提交的挂载量有界）；
// 2. 反复应用必然收敛：目标进入窗口（跳转目标仍要到达）；
// 3. 不过冲：到达所需量（blockCount - targetIndex + 一批余量）就停；
// 4. 目标已在窗口内时原样返回（不触发多余的扩窗/重挂）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { WINDOW_INITIAL, WINDOW_BATCH, WINDOW_SOFT_CAP, expandWindowForJump } from '../src/components/thread/Thread.tsx';

test('单步增长不超过软上限（一次提交的挂载量有界）', () => {
  // 1000 块会话、目标在第 10 块：需要 990 + 40，一步只能到 40 + 400
  const next = expandWindowForJump(WINDOW_INITIAL, 10, 1000);
  assert.equal(next, WINDOW_INITIAL + WINDOW_SOFT_CAP);
  assert.ok(next - WINDOW_INITIAL <= WINDOW_SOFT_CAP);
});

test('分批推进收敛：目标最终进入窗口，且每一步都有界', () => {
  const blockCount = 1000, targetIndex = 10;
  let windowSize = WINDOW_INITIAL;
  let steps = 0;
  while (blockCount - targetIndex > windowSize) {
    const prev = windowSize;
    windowSize = expandWindowForJump(windowSize, targetIndex, blockCount);
    steps++;
    assert.ok(windowSize - prev <= WINDOW_SOFT_CAP, '每步增长 ≤ 软上限');
    assert.ok(windowSize > prev, '分批推进必须实际前进（不许原地打转）');
    assert.ok(steps < 10, '几百步还不收敛 = 没在推进');
  }
  // 目标已在窗口内（hidden = blockCount - windowSize ≤ targetIndex）
  assert.ok(blockCount - windowSize <= targetIndex, '跳转目标仍要到达');
  // 不过冲：恰好停在「需要量」（990 + 一批余量）
  assert.equal(windowSize, blockCount - targetIndex + WINDOW_BATCH);
});

test('目标已在窗口内：原样返回（不触发多余扩窗）', () => {
  // 100 块会话、窗口 40、目标在第 80 块（窗口内）→ 不动
  assert.equal(expandWindowForJump(40, 80, 100), 40);
  // 恰好等于需要量时也不动
  assert.equal(expandWindowForJump(990 + WINDOW_BATCH, 10, 1000), 990 + WINDOW_BATCH);
});

test('短会话：需要量小于软上限时一步到位（软上限不拖慢常规跳转）', () => {
  // 120 块会话、目标在第 5 块：需要 115 + 40 < 40 + 400 → 一步到达
  assert.equal(expandWindowForJump(WINDOW_INITIAL, 5, 120), 120 - 5 + WINDOW_BATCH);
});
