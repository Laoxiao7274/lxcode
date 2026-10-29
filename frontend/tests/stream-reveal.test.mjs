// stream-reveal 的 hidden/恢复纪律（2026-12 卡顿修复）：
//
// 页面隐藏（document.hidden）时 rAF 停转，浇注循环跑不动；修复前积压一旦
// 超过 REVEAL_MAX_LAG 还会**每条 delta 全量对齐**（setShown(全文) → Markdown
// 对全文重新解析），为不可见的内容持续烧 CPU；恢复瞬间 pending rAF 又用
// 「整个隐藏时长」当 elapsed 一次吐完，再叠加恢复后 ≤1000cps 的逐帧追赶
// （每帧 O(全文) 解析 + 置底强制布局）——大积压时恢复后数秒的主线程风暴，
// 即用户报的「最小化一段时间后打开，界面卡顿一段时间」。
//
// 修复后的契约（本文件钉住）：
// 1. hidden → 冻结（hold）：无论积压多大都不 setShown、不对齐——低成本累积；
// 2. 恢复可见 → 统一落账：积压 > REVEAL_RESUME_JUMP 直接整块落下（snap，
//    一次提交、跳过动画）；小积压照常浇注（pour），观感不变；
// 3. 可见时的正常路径行为不变（pour 的推进公式与原实现逐字段一致）。
//
// 测试环境没有 DOM（react-dom 的 effect 跑不起来），所以把判定与推进
// 提成纯函数（revealPlan / pourStep）直接测；hook 只是把它们接进
// useEffect / visibilitychange，接线很薄。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  documentHidden, pourStep, revealPlan, REVEAL_RESUME_JUMP,
} from '../src/shared/stream-reveal.ts';

const P = (shown, text, over = {}) => revealPlan(shown, text, {
  visible: true, motion: true, lagLimit: 4000, ...over,
});

test('hidden 一律冻结（hold）：积压再大也不对齐、不浇注', () => {
  // 修复核心：修复前 hidden 期间积压超限走「每条 delta 全量对齐」——
  // 现在无论积压多大都是 hold（不 setShown，TextBlocks memo 全命中）。
  assert.equal(P('abc', 'abcd'.repeat(3000), { visible: false }), 'hold', '积压 1.2 万字也冻结');
  assert.equal(P('abc', 'abcdef', { visible: false }), 'hold', '小积压同样冻结');
  assert.equal(P('abc', 'xyz', { visible: false }), 'hold', '分叉流也冻结（恢复时再 snap）');
});

test('node（无 DOM）环境视为可见：documentHidden=false，正常走可见路径', () => {
  assert.equal(documentHidden(), false);
  assert.equal(P('abc', 'abcdefg'), 'pour');
});

test('可见正常路径：小积压浇注、无积压不动、超限与分叉整块落下', () => {
  assert.equal(P('abc', 'abcdef'), 'pour');
  assert.equal(P('abc', 'abc'), 'none', '无积压不安排动画');
  assert.equal(P('abc', 'x'.repeat(5000)), 'snap', '积压超过 REVEAL_MAX_LAG 对齐（分叉/超限不能回放）');
  assert.equal(P('abc', 'xbcdef'), 'snap', '前缀分叉直接整块落下');
  assert.equal(P('abc', 'abcdef', { motion: false }), 'snap', '动效禁用（测试通道）整块落下');
});

test('恢复落账：积压超过 REVEAL_RESUME_JUMP 跳过动画整块落下', () => {
  // 「hidden 期间积压 N → 恢复时整块落下」的主断言：落账判定用的是
  // REVEAL_RESUME_JUMP（不是 REVEAL_MAX_LAG）——超过它浇注要 >1s，
  // 逐帧 O(全文) 解析 + 置底强制布局比一次性提交更贵。
  const backlog = '前缀'.repeat(10);
  const grown = backlog + '正文'.repeat(2000); // 积压 4000 字 > 1000
  assert.equal(
    revealPlan(backlog, grown, { visible: true, motion: true, lagLimit: REVEAL_RESUME_JUMP }),
    'snap',
    '恢复时大积压整块落下（一次提交，不逐帧追赶）',
  );
  assert.equal(grown.length - backlog.length, 4000);
});

test('恢复落账：小积压照常浇注（观感保留）', () => {
  const backlog = '前缀'.repeat(10);
  const grown = backlog + '又来了一段'; // 积压 5 字 ≤ 1000
  assert.equal(
    revealPlan(backlog, grown, { visible: true, motion: true, lagLimit: REVEAL_RESUME_JUMP }),
    'pour',
  );
  // 分叉/动效禁用在恢复落账时同样整块落下
  assert.equal(
    revealPlan(backlog, 'x'.repeat(50), { visible: true, motion: true, lagLimit: REVEAL_RESUME_JUMP }),
    'snap',
  );
  assert.equal(
    revealPlan(backlog, backlog + '更多', { visible: true, motion: false, lagLimit: REVEAL_RESUME_JUMP }),
    'snap',
  );
});

test('pourStep：大积压限速 1000 字/秒，小积压按 0.35s 浇注窗口加速', () => {
  // 大积压：rate 封顶 1000 → 16ms 一步 16 字（4s 的积压不会一帧吐完，也不会更慢）
  assert.equal(pourStep('a'.repeat(100), 'a'.repeat(100) + 'b'.repeat(4000), 16).length, 116);
  // 小积压：gap=70 → rate=200 → 16ms 一步 round(3.2)=3 字
  assert.equal(pourStep('a'.repeat(30), 'a'.repeat(30) + 'b'.repeat(70), 16).length, 33);
  // 极小 elapsed 也至少推进 1 字（不会停住）
  assert.equal(pourStep('a', 'ab', 0).length, 2);
  // 追上即全文（gap≤0 原样）。极小积压走最低速 30cps：16ms 一步 1 字
  assert.equal(pourStep('abcd', 'abcdef', 16), 'abcde');
  assert.equal(pourStep('abcd', 'abcdef', 300), 'abcdef', 'elapsed 足够大一步追上');
  // 分叉（shown 不是前缀）直接返回全文（整块落下，与 snap 同口径）
  assert.equal(pourStep('xbcd', 'abcdef', 16), 'abcdef');
});

test('端到端口径：hidden 冻结 → 恢复整块落下，两步拼起来等于最终全文', () => {
  // 复刻真实时序：流式中最小化 → hidden 期间 delta 继续到达（全部 hold）→
  // 流结束 → 恢复可见 → 落账。shown 全程只能是 text 的前缀（浇注/冻结都不
  // 产生回放），恢复后一次 snap 就到最终全文。
  let shown = '';
  const deltas = ['# 标题\n\n', '一段'.repeat(3000), '收尾'];
  let text = '';
  for (const d of deltas) {
    text += d;
    const plan = revealPlan(shown, text, { visible: false, motion: true, lagLimit: 4000 });
    assert.equal(plan, 'hold', 'hidden 期间每条 delta 都冻结');
  }
  assert.ok(text.startsWith(shown), '冻结期间 shown 仍是前缀');
  const resume = revealPlan(shown, text, { visible: true, motion: true, lagLimit: REVEAL_RESUME_JUMP });
  assert.equal(resume, 'snap', '恢复时积压超阈值整块落下');
  shown = text;
  assert.equal(shown, '# 标题\n\n' + '一段'.repeat(3000) + '收尾');
});
