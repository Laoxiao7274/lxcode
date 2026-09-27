// 两步删除确认（useConfirmClick）的状态机契约。
//
// 覆盖用户报的缺陷：条目清除后**不卸载**（SearchSection 的渠道卡清除后
// 只是变成未配置态），而旧实现确认后不复位 confirming —— 按钮停在
// 「确认清除」，用户重新配置后第一次点击就直接删除（二次确认失效）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { nextConfirmState } from '../src/shared/confirm-click.ts';

/** 按事件序列走一遍状态机，返回 [最终确认态, 执行次数]。 */
function run(events) {
  let confirming = false;
  let fires = 0;
  for (const ev of events) {
    const next = nextConfirmState(confirming, ev);
    confirming = next.confirming;
    if (next.fire) fires += 1;
  }
  return [confirming, fires];
}

test('首次点击只进入确认态，不执行', () => {
  const [confirming, fires] = run(['click']);
  assert.equal(confirming, true, '首次点击应进入确认态');
  assert.equal(fires, 0, '首次点击绝不能执行');
});

test('确认态下点击执行并复位（回归：确认后必须回到未确认态）', () => {
  const next = nextConfirmState(true, 'click');
  assert.equal(next.fire, true, '确认态下点击应执行');
  assert.equal(next.confirming, false, '执行后必须复位——否则按钮停在「确认」态，下一次点击直接删除');
});

test('确认一次后需要重新走两步（清除 → 重新配置 → 首次点击不删）', () => {
  // 用户报的场景：点「清除」两下删掉渠道，卡片留下（变成未配置态），
  // 重新配置后再点一下 —— 这一次必须是「进入确认态」而不是「执行」。
  const [confirming, fires] = run(['click', 'click', 'click']);
  assert.equal(fires, 1, '只有第二次点击执行；第三次点击不得删除');
  assert.equal(confirming, true, '第三次点击后应停在确认态（等待用户再确认一次）');
});

test('失焦复位：确认态下离开按钮则不执行', () => {
  const [confirming, fires] = run(['click', 'blur']);
  assert.equal(confirming, false, 'blur 必须复位');
  assert.equal(fires, 0, 'blur 不执行任何动作');
});

test('未确认时的 blur 是空操作', () => {
  const next = nextConfirmState(false, 'blur');
  assert.equal(next.confirming, false);
  assert.equal(next.fire, false);
});

test('连续两轮确认各自只执行一次', () => {
  const [confirming, fires] = run(['click', 'click', 'click', 'click']);
  assert.equal(fires, 2, '四次要两次执行');
  assert.equal(confirming, false, '偶数次点击应停在未确认态');
});
