// OOM 三件套 S4 的钉子：窗口上界（WINDOW_MAX）+ 滑动窗口（丢弃最旧）+ 贴底恢复。
//
// olderWindowTransition 是 useWindowedHistory 里 expandOlder 的纯转移函数
// （hook 与测试共用一份）：
//   - span + BATCH ≤ WINDOW_MAX：真扩窗（纯前插，end 不动）；
//   - 已到上界：滑动（span 收到 WINDOW_MAX，end -= BATCH——头部补/尾部丢）。
// 窗口语义（三处消费点共用）：显示 = blocks.slice(max(0, end - span), end)；
// hidden = max(0, end - span)；被丢的尾部块数据仍在 state，只是 DOM 不渲染，
// 贴底帧的渲染期整备恢复完整尾部窗口。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  WINDOW_INITIAL,
  WINDOW_BATCH,
  WINDOW_MAX,
  olderWindowTransition,
} from '../src/components/thread/useWindowedHistory.ts';

const initial = (total) => ({ span: Math.min(WINDOW_INITIAL, total), end: total });
const display = (w) => ({ head: Math.max(0, w.end - w.span), size: w.span });

// ---------- ① 常量契约 ----------

test('WINDOW_MAX = 160 = 4 批（WINDOW_INITIAL + 3×WINDOW_BATCH）', () => {
  assert.equal(WINDOW_MAX, 160);
  assert.equal(WINDOW_MAX, WINDOW_INITIAL + 3 * WINDOW_BATCH);
});

// ---------- ② 上界前的真扩窗（既有语义不变） ----------

test('上界前扩窗：span += BATCH、end 不动（纯前插，首屏 40 起）', () => {
  let w = olderWindowTransition(null, 1000);
  assert.deepEqual(w, { span: WINDOW_INITIAL + WINDOW_BATCH, end: 1000 }, '首扩 = 40 + 40');
  // 从任意已扩窗口继续扩，end 恒不动（end 只在滑动时变）
  w = olderWindowTransition({ span: 120, end: 1000 }, 1000);
  assert.deepEqual(w, { span: 160, end: 1000 }, '120 + 40 = 160，恰好到上界');
});

test('恰好到上界的那一步仍是真扩窗（不上滑）', () => {
  // span=120 + 40 = 160 = WINDOW_MAX → 走纯前插分支
  const w = olderWindowTransition({ span: 120, end: 500 }, 500);
  assert.deepEqual(w, { span: WINDOW_MAX, end: 500 });
  assert.equal(display(w).size, WINDOW_MAX, '显示块数 = 上界');
});

// ---------- ③ 上界后滑动：丢弃最旧、挂载量有界 ----------

test('到上界后继续上翻：滑动窗口（头部补 BATCH、尾部丢 BATCH）', () => {
  const w = olderWindowTransition({ span: WINDOW_MAX, end: 1000 }, 1000);
  // 头部：新显示头 = end - span = 1000 - 160 = 840 → 800（补 40 块更早的）
  assert.equal(display(w).head, 800, '头部上移 40（补更早的块）');
  assert.equal(w.end, 960, '尾部丢 40（end -= BATCH）');
  assert.equal(display(w).size, WINDOW_MAX, '挂载块数恒 ≤ WINDOW_MAX');
});

test('连续上翻：窗口恒为 WINDOW_MAX，头部持续上移，挂载量不再增长', () => {
  let w = olderWindowTransition({ span: WINDOW_MAX, end: 5000 }, 5000);
  const heads = [display(w).head];
  for (let i = 0; i < 50; i++) {
    w = olderWindowTransition(w, 5000);
    heads.push(display(w).head);
    assert.equal(display(w).size, WINDOW_MAX, `第 ${i + 1} 次滑动后挂载量仍 = 上界`);
    assert.ok(w.end >= w.span, 'end 永远 ≥ span（窗口不越过历史头部）');
  }
  // 头部每步上移 BATCH（ Archaeology 深度增长 → 挂载量不变——这就是 S4 的目标）
  for (let i = 1; i < heads.length; i++) {
    assert.equal(heads[i - 1] - heads[i], WINDOW_BATCH, '头部每步上移恰好一批');
  }
});

test('滑到历史头部附近：end 钳住不越界（span ≤ end 恒成立）', () => {
  // end 接近 span：end - BATCH < span → end 钳到 span（再往下丢就没有可丢的了）
  const w = olderWindowTransition({ span: WINDOW_MAX, end: 170 }, 170);
  assert.equal(w.end, WINDOW_MAX, 'end 钳到 ≥ span');
  assert.equal(display(w).head, 0, '窗口已顶到历史开头');
  assert.equal(display(w).size, WINDOW_MAX);
});

// ---------- ④ 短会话：不滑动（上界永远够用） ----------

test('短会话（total ≤ WINDOW_MAX）：永远走纯前插，end = total 不动', () => {
  for (const total of [10, 40, 100, 160]) {
    let w = olderWindowTransition(null, total);
    while (w && w.span < total) {
      const prev = w.span;
      w = olderWindowTransition(w, total);
      assert.equal(w.end, total, `total=${total}：end 恒 = total`);
      assert.ok(w.span > prev, '持续扩窗');
    }
    assert.ok(w.span >= total, `total=${total}：最终窗口覆盖全量`);
  }
});

// ---------- ⑤ 恢复路径的窗口形状（贴底整备渲染期给出的转移目标） ----------

test('贴底恢复目标：完整尾部窗口（end=total、span=初始窗口）', () => {
  // 恢复不经过 olderWindowTransition（那是上翻方向）；贴底帧整备直接 setWin 到
  // {span: min(WINDOW_INITIAL, total), end: total}——钉住目标形状：
  const total = 1000;
  const restored = { span: Math.min(WINDOW_INITIAL, total), end: total };
  assert.deepEqual(restored, { span: 40, end: 1000 });
  assert.equal(display(restored).head, total - WINDOW_INITIAL, '头部 = total - 40（尾部窗口）');
  // 短会话恢复 = 全量
  const small = { span: Math.min(WINDOW_INITIAL, 30), end: 30 };
  assert.deepEqual(small, { span: 30, end: 30 });
});
