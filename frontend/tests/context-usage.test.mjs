// 上下文指示器的展示口径：**估算值必须和真实用量分得开**。
//
// 为什么这是一条纪律而不是审美：后端 used 有两个来源——provider 回报的真实 prompt_tokens，
// 和按固定密度折算的估算（端点不回报 usage；或库里没有真实测量、按已加载的历史回落，
// 见后端 internal/agent/context_usage.go 的 estimated 位）。估算按字节算对中文是**低估**的，
// 不标注的话用户会拿它当真实用量做预算判断（"还剩 60%"其实是假的）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { contextUsageDisplay } from '../src/shared/context-usage.ts';

test('真实用量：纯百分比，说明里不出现"估算"', () => {
  const d = contextUsageDisplay({ used: 4096, window: 8192 });
  assert.equal(d.known, true);
  assert.equal(d.estimated, false);
  assert.equal(d.pct, 50);
  assert.equal(d.pctText, '50%');
  assert.ok(!d.title.includes('估算'), d.title);
});

test('估算用量（estimated=true）：百分比带「估」字，说明里明说不是真实用量', () => {
  const d = contextUsageDisplay({ used: 4096, window: 8192, estimated: true });
  assert.equal(d.estimated, true);
  assert.equal(d.pct, 50);
  // 关键断言：数字本身必须带标记——只在 title 里说明的话，用户一眼看到的是
  // 一个和真实用量一模一样的 "50%"
  assert.equal(d.pctText, '50%估');
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
