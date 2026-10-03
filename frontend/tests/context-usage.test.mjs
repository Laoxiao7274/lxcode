// 上下文指示器的展示口径：**估算值必须和真实用量分得开**。
//
// 为什么这是一条纪律而不是审美：后端 used 有两个来源——provider 回报的真实 prompt_tokens，
// 和按固定密度折算的估算（端点不回报 usage；或库里没有真实测量、按已加载的历史回落，
// 见后端 internal/agent/context_usage.go 的 estimated 位）。估算按字节算对中文是**低估**的，
// 不标注的话用户会拿它当真实用量做预算判断（"还剩 60%"其实是假的）。
//
// 标注的出口（2026-09-30 用户拍板）：**可见记号只有 `~`**（对齐 DSH 的 ContextMeter），
// 「这份数字是估的」写在 title（悬停说明）里——原先百分比后面挂「估」字，DSH 没这个记号。
import test from 'node:test';
import assert from 'node:assert/strict';
import { contextUsageDisplay, contextFiguresText, contextSegments } from '../src/shared/context-usage.ts';

test('真实用量：纯百分比，说明里不出现"估算"', () => {
  const d = contextUsageDisplay({ used: 4096, window: 8192 });
  assert.equal(d.known, true);
  assert.equal(d.estimated, false);
  assert.equal(d.pct, 50);
  assert.equal(d.pctText, '50%');
  assert.ok(!d.title.includes('估算'), d.title);
});

test('估算用量（estimated=true）：百分比仍是纯数字，估算说明走 title', () => {
  const d = contextUsageDisplay({ used: 4096, window: 8192, estimated: true });
  assert.equal(d.estimated, true);
  assert.equal(d.pct, 50);
  // 2026-09-30 用户拍板：可见记号只有 `~`（DSH 的 ContextMeter 也没有「估」字），
  // 百分比是纯数字——"这份数字是估的"必须出现在 title 里（否则用户看不出区别）
  assert.equal(d.pctText, '50%');
  assert.ok(d.title.includes('估算'), d.title);
  assert.ok(d.title.includes('不是真实用量'), d.title);
});

test('未知占用（null/undefined）：中性态「—」，不编 0%', () => {
  for (const v of [null, undefined]) {
    const d = contextUsageDisplay(v);
    assert.equal(d.known, false);
    assert.equal(d.pctText, '—');
    assert.equal(d.estimated, false);
  }
});

test('窗口未知（模型没配 context_window）：不给百分比，也不标估算', () => {
  // used 有值但没有分母 → 算不出占比。显示 0% 会把"未知"说成"空"
  const d = contextUsageDisplay({ used: 4096, estimated: true });
  assert.equal(d.known, false);
  assert.equal(d.pctText, '—');
  assert.equal(d.estimated, false);
});

test('百分比封顶 100（真实用量偶尔会略微超过窗口）', () => {
  const d = contextUsageDisplay({ used: 9000, window: 8192 });
  assert.equal(d.pct, 100);
  assert.equal(d.pctText, '100%');
});

test('弹层读数带 ~（近似值不许看起来像精确读数）', () => {
  // 总量取真实用量、分类是估算拆分，两者都不是精确到个位的读数（DSH 的
  // ContextMeter 同款记号）。少了 ~ 用户会把它当成精确值做预算判断。
  assert.equal(contextFiguresText({ used: 4096, window: 8192 }), '~4.1k / 8.2k');
  // 窗口未知 → 算不出读数（不显示那一行，不编一个分母）
  assert.equal(contextFiguresText({ used: 4096 }), null);
  assert.equal(contextFiguresText(null), null);
});

test('分类拆分：工具声明单列一类，零值分类不出现', () => {
  const segs = contextSegments({
    used: 1000, window: 8192,
    system: 100, tools: 200, tool_results: 300, messages: 300, reasoning: 100,
  });
  // 工具声明必须自己一类（后端已从 system 里拆出来）——混在系统提示里就答不了
  // "工具占了窗口多少"这个问题
  assert.deepEqual(segs.map((s) => s.label), ['系统提示', '工具声明', '工具结果', '对话消息', '思考链']);
  assert.equal(segs[1].tokens, 200);
  // 零值分类不画（不是画一段宽度为 0 的色块 + 图例里一行 "0"）
  const some = contextSegments({ used: 500, window: 8192, system: 100, messages: 400 });
  assert.deepEqual(some.map((s) => s.label), ['系统提示', '对话消息']);
  assert.deepEqual(contextSegments(null), []);
});
