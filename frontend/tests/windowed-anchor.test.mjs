// 扩窗锚定判定纯函数测试：「切换会话后滚动条不在最下面」修复的锁定。
// shouldAnchorForExpand 同时被主时间线（Thread）与子会话页（ChildSessionPage）
// 消费——贴底时不锚定（跟随 RO 保持贴底，锚定补偿反而把视口挪到中间）、
// 非贴底且距底超过贴底阈值才锚定（用户正在上翻，需要补偿 scrollTop）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { shouldAnchorForExpand } from '../src/components/thread/useWindowedHistory.ts';
import { NEAR_BOTTOM_PX } from '../src/shared/scroll-metrics.ts';

const VIEWPORT = 800;

/** 距底 distance px 的滚动位置（视口 800）。 */
const at = (distance) => ({
  scrollTop: 1000,
  scrollHeight: 1000 + VIEWPORT + distance,
});

// ① 贴底（sticky）时不锚定：切会话首帧哨兵初始交付 intersecting 触发扩窗，
//    贴底场景必须交给 RO 跟随保持贴底——这正是「切会话后视口停在中间」的根因。
test('贴底时不锚定（sticky=true 恒 false，与滚动位置无关）', () => {
  assert.equal(shouldAnchorForExpand(true, 0, 10000, VIEWPORT), false);
  assert.equal(shouldAnchorForExpand(true, at(0).scrollTop, at(0).scrollHeight, VIEWPORT), false);
  assert.equal(shouldAnchorForExpand(true, at(5000).scrollTop, at(5000).scrollHeight, VIEWPORT), false);
});

// ② 非贴底时按距离阈值判定：距底 > NEAR_BOTTOM_PX 锚定，阈值内（含）不锚定。
//    阈值必须与贴底判定（isNearBottom）同一份常量。
test('非贴底时按距底阈值锚定（阈值边界）', () => {
  assert.equal(NEAR_BOTTOM_PX, 200, '阈值必须与 isNearBottom 共用同一份常量');
  const bottom = at(0);
  assert.equal(shouldAnchorForExpand(false, bottom.scrollTop, bottom.scrollHeight, VIEWPORT), false, '已在底部且未贴底标记 → 不锚定');
  const near = at(NEAR_BOTTOM_PX);
  assert.equal(shouldAnchorForExpand(false, near.scrollTop, near.scrollHeight, VIEWPORT), false, '恰好等于阈值 = 视为贴底区，不锚定');
  const far = at(NEAR_BOTTOM_PX + 1);
  assert.equal(shouldAnchorForExpand(false, far.scrollTop, far.scrollHeight, VIEWPORT), true, '距底超过阈值 = 用户在上翻，要锚定补偿');
  const veryFar = at(5000);
  assert.equal(shouldAnchorForExpand(false, veryFar.scrollTop, veryFar.scrollHeight, VIEWPORT), true);
});

// ③ 不可滚动（内容不足一屏）：距底为 0，不锚定。
test('内容不足一屏不锚定', () => {
  assert.equal(shouldAnchorForExpand(false, 0, 300, VIEWPORT), false);
});
