// 会话统计展示口径的纯函数测试（shared/session-stats.ts）。
//
// 这一层最容易错的是**"缺席"与"0"分不开**：工具时间 0ms 与"这次会话一次工具都没跑"
// 在界面上必须长得不一样（后者不该出现那一行），而 tok/s 与缓存命中率在缺数据时
// 宁可不显示，也不许编一个数出来。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  statsVisible, hasTokens, totalTokens, promptTokens, decodeTokensPerSec, averageTtftMs,
  formatStatsTokens, formatDuration, cacheHitPercent, timeDialogRows, usageDialogRows,
  timePillLabel, usageTotalLabel,
} from '../src/shared/session-stats.ts';

/** 一份"跑过几步、有完整数据"的统计（各测试按需覆盖字段）。 */
const full = (over = {}) => ({
  turns: 2, steps: 3, llm_ms: 6000, tool_ms: 2000,
  ttft_ms: 900, ttft_steps: 2, decode_ms: 4100, decode_tokens: 300,
  input_tokens: 1050, cache_read_tokens: 11000, cache_write_tokens: 100, output_tokens: 320,
  ...over,
});

test('statsVisible：一步都没有就不渲染（不是显示一排 0）', () => {
  assert.equal(statsVisible(null), false);
  assert.equal(statsVisible(undefined), false);
  assert.equal(
    statsVisible(full({ steps: 0, input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0 })),
    false,
  );
  // 只有 token 没有步数时也**不渲染**：这一行现在只剩时间胶囊（用量那一半并进了
  // 「会话用量」弹层），留一个空行没有意义
  assert.equal(statsVisible(full({ steps: 0 })), false);
  assert.equal(statsVisible(full()), true);
});

test('总量口径：prompt 侧三桶 + 输出（每一步的 prompt 都算一次）', () => {
  assert.equal(promptTokens(full()), 1050 + 11000 + 100);
  assert.equal(totalTokens(full()), 1050 + 11000 + 100 + 320);
});

test('生成速度：分母扣掉首字延迟；缺数据 → null（不编 0）', () => {
  // 300 tokens / 4100ms ≈ 73.2 tok/s
  assert.equal(Math.round(decodeTokensPerSec(full()) * 10) / 10, 73.2);
  // 没有解码窗口（没有任何一步同时有首字与用量）→ null，调用方不显示
  assert.equal(decodeTokensPerSec(full({ decode_ms: 0 })), null);
  assert.equal(decodeTokensPerSec(full({ decode_tokens: 0 })), null);
});

test('首字延迟均值：没有可测的步 → null', () => {
  assert.equal(averageTtftMs(full()), 450); // 900 / 2
  assert.equal(averageTtftMs(full({ ttft_steps: 0 })), null);
  assert.equal(averageTtftMs(full({ ttft_ms: 0 })), null);
});

test('token 数格式：千/百万级，≥100 的换算值取整', () => {
  assert.equal(formatStatsTokens(0), '0');
  assert.equal(formatStatsTokens(999), '999');
  assert.equal(formatStatsTokens(1200), '1.2k');
  assert.equal(formatStatsTokens(128000), '128k');
  assert.equal(formatStatsTokens(1200000), '1.2M');
});

test('时长格式：一分钟以内 45.2s，之后 2m42s', () => {
  assert.equal(formatDuration(45200), '45.2s');
  assert.equal(formatDuration(162000), '2m42s');
});

test('缓存命中率：没有 prompt 输入 → null；部分命中不许四舍五入成 100%', () => {
  assert.equal(cacheHitPercent(full({ input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 })), null);
  assert.equal(cacheHitPercent(full({ input_tokens: 1000, cache_read_tokens: 0, cache_write_tokens: 0 })), '0');
  // 全命中就是 100
  assert.equal(cacheHitPercent(full({ input_tokens: 0, cache_read_tokens: 1000, cache_write_tokens: 0 })), '100');
  // 99.6% 命中显示成 "100%" 会让用户以为全都命中了——必须多带小数
  const near = cacheHitPercent(full({ input_tokens: 4, cache_read_tokens: 996, cache_write_tokens: 0 }));
  assert.notEqual(near, '100');
  assert.equal(near, '99.6');
});

test('弹层明细：缺席的项直接不出现（不是显示 0）', () => {
  // 只有模型时间：工具时间/首字/速度那三行都不该出现
  const rows = timeDialogRows(full({ tool_ms: 0, ttft_ms: 0, ttft_steps: 0, decode_ms: 0, decode_tokens: 0 }));
  assert.deepEqual(rows.map((r) => r.label), ['模型时间']);
  // 一次工具都没跑过 → 没有「工具时间」那一行
  const withTools = timeDialogRows(full());
  assert.deepEqual(withTools.map((r) => r.label), ['模型时间', '工具时间', '首字延迟', '生成速度']);
  assert.equal(withTools[0].value, '6s');
  assert.equal(withTools[1].value, '2s');
  // 时长按 DSH 的秒制口径（450ms → 0.5s）——与胶囊里的速度同一种格式，不混用两种
  assert.equal(withTools[2].value, '0.5s');
  assert.ok(withTools[3].value.endsWith('tok/s'), withTools[3].value);

  // 用量弹层：缓存命中率缺席时不出现那一行，四桶恒在
  const usage = usageDialogRows(full());
  assert.deepEqual(usage.map((r) => r.label), ['缓存命中', '未缓存输入', '缓存读取', '缓存写入', '输出']);
  const noHit = usageDialogRows(full({ input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 }));
  assert.deepEqual(noHit.map((r) => r.label), ['未缓存输入', '缓存读取', '缓存写入', '输出']);
});

test('胶囊标题：缺席的项不拼进标题（不显示 0.0 tok/s）', () => {
  assert.equal(timePillLabel(full()), '2 轮 · 3 步 · 73.2 tok/s');
  // 没有速度可算时只留轮与步，不留一个假的 0.0
  assert.equal(timePillLabel(full({ decode_ms: 0 })), '2 轮 · 3 步');
});

test('会话消耗的总量标签与明细（并进「会话用量」弹层的那一节）', () => {
  // 总量：四桶相加（每一步的 prompt 都算一次）
  assert.equal(usageTotalLabel(full()), '12.5k tok');
  // 明细：缓存命中 / 未缓存输入 / 缓存读取 / 缓存写入 / 输出
  // 11000 / (1050 + 11000 + 100) = 90.53% → 整数位四舍五入成 91
  const rows = usageDialogRows(full());
  assert.deepEqual(rows.map((r) => r.label), ['缓存命中', '未缓存输入', '缓存读取', '缓存写入', '输出']);
  assert.equal(rows[0].value, '91%');
  // 没有 prompt 输入 → 不显示缓存命中那一行（不是显示 0%）
  assert.equal(
    usageDialogRows(full({ input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 }))[0].label,
    '未缓存输入',
  );
  // 没有 token（老会话）→ 明细为空数组 → 调用方据此整节不渲染
  assert.deepEqual(
    usageDialogRows(full({ input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0 })),
    [],
  );
});

test('早期记录（legacy_tokens）：单独一行如实说明，不进四桶、不进速度', () => {
  // 本功能上线前落库的行：那时的 usage_tokens 是 provider 的 total_tokens（输入+输出）。
  // 混进输出桶会把速度报得离谱（实测 687.8 tok/s，真值约 40）——所以后端单独折叠，
  // 前端在明细里如实说明它是什么（不是藏起来，也不是当成输出）。
  const s = full({
    input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0,
    legacy_tokens: 1498, decode_ms: 0, decode_tokens: 0,
  });
  assert.equal(totalTokens(s), 0, '早期记录不进四桶');
  assert.equal(hasTokens(s), false, '只有早期记录时「会话消耗」一节整个不渲染');
  assert.equal(decodeTokensPerSec(s), null, '早期记录不进速度');
  const legacy = timeDialogRows(s).find((r) => r.label === '早期记录');
  assert.ok(legacy, '时间明细里应有一行说明早期记录: ' + JSON.stringify(timeDialogRows(s)));
  assert.ok(legacy.value.includes('1.5k'), '数字按同一套紧凑格式显示: ' + legacy.value);
  assert.ok(legacy.value.includes('输入+输出'), '必须说明那是输入+输出（口径不同）: ' + legacy.value);
  // 与真正的新口径并存时：四桶只装新口径，早期记录另起一行
  const mixed = full({ legacy_tokens: 1498 });
  assert.equal(totalTokens(mixed), 1050 + 11000 + 100 + 320);
  assert.ok(usageDialogRows(mixed).some((r) => r.label === '早期记录'));
});
