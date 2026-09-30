// 滚动判定纯函数测试：「回到底部」按钮的显示/目标位置、标签条溢出方向与滚轮拦截。
// 这些判定同时被两处 UI 消费（按钮 + 渐隐），所以在这里钉死——方向判定被改成
// "永远两侧都渐隐"、或有人另起一个贴底阈值，都必须让这里变红。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  NEAR_BOTTOM_PX,
  distanceFromBottom,
  isNearBottom,
  overflowAttr,
  overflowEdges,
  scrollToBottomTarget,
  shouldInterceptWheel,
  shouldShowScrollToBottom,
} from '../src/shared/scroll-metrics.ts';

/** 构造一个"距底恰好 distance px"的滚动容器（容器高 800）。 */
const view = (distance, clientHeight = 800) => ({
  scrollTop: 0,
  scrollHeight: clientHeight + distance,
  clientHeight,
});

/** 构造一个横向溢出的 strip：内容 content、容器 client、已滚 scrollLeft。 */
const strip = (scrollLeft, content, client) => ({
  scrollLeft,
  scrollWidth: content,
  clientWidth: client,
});

// ① 是否显示「回到底部」：> 阈值显示，<= 阈值不显示；阈值与 Thread 的贴底判定同源
test('回到底部按钮：离开贴底阈值才出现，阈值内（含）不出现', () => {
  assert.equal(NEAR_BOTTOM_PX, 200, '阈值必须与 Thread 既有的 200px 贴底判定同源');
  assert.equal(shouldShowScrollToBottom(view(0)), false);
  assert.equal(shouldShowScrollToBottom(view(199)), false);
  assert.equal(shouldShowScrollToBottom(view(NEAR_BOTTOM_PX)), false, '恰好等于阈值 = 仍在底部');
  assert.equal(shouldShowScrollToBottom(view(NEAR_BOTTOM_PX + 1)), true);
  assert.equal(shouldShowScrollToBottom(view(900)), true);
});

test('回到底部按钮与 Thread 的贴底判定严格互补（不存在第二个判据）', () => {
  for (const distance of [0, 1, 120, 199, 200, 201, 500, 5000]) {
    const metrics = view(distance);
    assert.equal(distanceFromBottom(metrics), distance);
    assert.equal(isNearBottom(metrics), !shouldShowScrollToBottom(metrics), `距底 ${distance}px 时两条判定必须互补`);
  }
});

// ⑤ 回到底部的目标位置
test('回到底部的目标位置就是 scrollHeight', () => {
  assert.equal(scrollToBottomTarget(view(0)), 800);
  assert.equal(scrollToBottomTarget({ scrollTop: 4200, scrollHeight: 5800, clientHeight: 800 }), 5800);
});

// ② 溢出方向：左/右/两侧/无
test('溢出方向：起点只有右渐隐，中间两侧，终点只有左渐隐', () => {
  assert.deepEqual(overflowEdges(strip(0, 1000, 400)), { left: false, right: true });
  assert.deepEqual(overflowEdges(strip(300, 1000, 400)), { left: true, right: true });
  assert.deepEqual(overflowEdges(strip(600, 1000, 400)), { left: true, right: false });
  // 亚像素抖动不算"还有内容"（否则两端会各自常亮一点渐隐）
  assert.deepEqual(overflowEdges(strip(0.4, 1000, 400)), { left: false, right: true });
  assert.deepEqual(overflowEdges(strip(599.6, 1000, 400)), { left: true, right: false });
  assert.equal(overflowAttr({ left: false, right: true }), 'right');
  assert.equal(overflowAttr({ left: true, right: true }), 'both');
  assert.equal(overflowAttr({ left: true, right: false }), 'left');
  assert.equal(overflowAttr({ left: false, right: false }), 'none');
});

// ③ 内容不够长：无渐隐、无滚轮拦截
test('内容不够长（scrollWidth <= clientWidth）时两侧都不渐隐、滚轮一律放行', () => {
  for (const metrics of [strip(0, 300, 400), strip(0, 400, 400), strip(0, 0, 400)]) {
    assert.deepEqual(overflowEdges(metrics), { left: false, right: false });
    assert.equal(overflowAttr(overflowEdges(metrics)), 'none');
    assert.equal(shouldInterceptWheel(metrics, 120), false, '不溢出就必须让事件冒泡（否则吞掉页面滚动）');
    assert.equal(shouldInterceptWheel(metrics, -120), false);
  }
});

test('滚轮只在溢出且该方向还有余量时被吃掉', () => {
  // 中间：两个方向都还有余量 → 都拦
  assert.equal(shouldInterceptWheel(strip(300, 1000, 400), 120), true);
  assert.equal(shouldInterceptWheel(strip(300, 1000, 400), -120), true);
  // 贴右端：继续下滚还给页面，上滚仍拦
  assert.equal(shouldInterceptWheel(strip(600, 1000, 400), 120), false);
  assert.equal(shouldInterceptWheel(strip(600, 1000, 400), -120), true);
  // 贴左端：上滚还给页面，下滚仍拦
  assert.equal(shouldInterceptWheel(strip(0, 1000, 400), -120), false);
  assert.equal(shouldInterceptWheel(strip(0, 1000, 400), 120), true);
  // 纵向滚动本身（deltaY === 0 无意义）不插手：横向由浏览器原生处理
  assert.equal(shouldInterceptWheel(strip(300, 1000, 400), 0), false);
});

// ④ 空容器 / 零宽 / 坏数值都不许崩，且必须落到"中性态"（不亮按钮、不画渐隐）
test('空容器与零宽/坏数值：不崩，且落在中性态', () => {
  const empty = { scrollTop: 0, scrollHeight: 0, clientHeight: 0 };
  assert.equal(distanceFromBottom(empty), 0);
  assert.equal(isNearBottom(empty), true);
  assert.equal(shouldShowScrollToBottom(empty), false, '空容器上按钮不该常亮');
  assert.equal(scrollToBottomTarget(empty), 0);
  assert.deepEqual(overflowEdges(empty), { left: false, right: false });
  assert.equal(shouldInterceptWheel(empty, 120), false);

  const broken = { scrollTop: NaN, scrollHeight: NaN, clientHeight: 0 };
  assert.equal(distanceFromBottom(broken), 0);
  assert.equal(shouldShowScrollToBottom(broken), false);
  assert.equal(scrollToBottomTarget(broken), 0);
  assert.deepEqual(overflowEdges({ scrollLeft: NaN, scrollWidth: NaN, clientWidth: 0 }), { left: false, right: false });
  assert.equal(shouldInterceptWheel({ scrollLeft: NaN, scrollWidth: NaN, clientWidth: 0 }, 120), false);

  // 内容比容器短（不可滚）时 scrollTop 可能被浏览器钳成 0，距底仍应是 0 而不是负数
  assert.equal(distanceFromBottom({ scrollTop: 0, scrollHeight: 100, clientHeight: 400 }), 0);
});
