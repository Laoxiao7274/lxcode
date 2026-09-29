// 系统提示条（notice）的前缀 → 标签表：与 Go 侧常量逐字一致 + 两条渲染路径
// （历史回放 / 实时事件）都不能把提示条变成用户气泡。
//
// 为什么这些断言值得单独一个文件：这类消息在历史里是**真实 user 角色消息**
// （模型必须把它当用户回合才会回应），所以前端识别**只能按文本前缀**。前缀写错
// 一个字/少一个尾空格不会让任何东西编译失败——只会静默把一句用户没说过的话渲染
// 成用户自己的气泡（用户以为会话被串了）。这是 jobs.test.mjs 那条通告断言的泛化版：
// 新增一类提示条（如重复调用提醒）时，这里必须同时钉住「标签取自表」与「两条路径
// 都变提示条」。
import test from 'node:test';
import assert from 'node:assert/strict';
import { REPEAT_NOTICE_PREFIX } from '../src/shared/jobs.ts';
import { NOTICE_KINDS, noticeBody, noticeLabel } from '../src/shared/notices.ts';
import { reduce } from '../src/shared/store.ts';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: null, historyReady: false };

// ---------- 与 Go 侧常量逐字一致 ----------

test('REPEAT_NOTICE_PREFIX 与后端 agent.RepeatNoticePrefix 逐字一致（含尾空格）', () => {
  // 尾空格是契约的一部分：后端用它做前缀匹配，前端少一个空格就永远认不出来
  //（重复调用提醒会静默变回用户气泡——用户以为是自己说的话）
  assert.equal(REPEAT_NOTICE_PREFIX, '[重复调用提醒] ');
  assert.equal(REPEAT_NOTICE_PREFIX.length, '[重复调用提醒] '.length);
  assert.equal(REPEAT_NOTICE_PREFIX.endsWith(' '), true, '尾空格必须保留');
});

// ---------- 前缀表：两种前缀各一条 + 没命中 = 原样 ----------

test('noticeLabel / noticeBody：两种前缀各一条，没命中的普通用户文本原样返回', () => {
  assert.equal(noticeLabel('[后台任务通告] 后台任务 go test 结束（退出码 0）。'), '后台任务通告');
  assert.equal(noticeBody('[后台任务通告] 正文在此 '), '正文在此');

  assert.equal(noticeLabel(REPEAT_NOTICE_PREFIX + '你在重复完全相同的工具调用。'), '重复调用提醒');
  assert.equal(noticeBody(REPEAT_NOTICE_PREFIX + '你在重复完全相同的工具调用。'), '你在重复完全相同的工具调用。');

  // 没命中：null / 原样（调用方可以直接把它当普通文本用）
  assert.equal(noticeLabel('用户自己打的一句话'), null);
  assert.equal(noticeBody('用户自己打的一句话'), '用户自己打的一句话');
  assert.equal(noticeLabel(''), null);
  assert.equal(noticeBody(''), '');
});

test('前缀在中间出现不算（只有开头才算——否则用户引用提示条文本会被误判）', () => {
  assert.equal(noticeLabel('看看这个 [后台任务通告] 是什么'), null);
  assert.equal(noticeBody('看看这个 [后台任务通告] 是什么'), '看看这个 [后台任务通告] 是什么');
  assert.equal(noticeLabel('引用一下 ' + REPEAT_NOTICE_PREFIX + '你在重复调用'), null);
  assert.equal(noticeBody('引用一下 ' + REPEAT_NOTICE_PREFIX + '你在重复调用'), '引用一下 ' + REPEAT_NOTICE_PREFIX + '你在重复调用');
});

test('前缀表顺序敏感：取数组里第一个命中的前缀', () => {
  // 表是导出的可变数组（新增一类就是往它加一行）。这里临时插一个「更长、包含已有
  // 前缀」的假种类，验证匹配真的是按数组顺序取第一个——若实现改成取最长匹配或取
  // 最后一个，这条会红。
  const probe = { prefix: '[后台任务通告] 后台任务', label: '探针种类' };
  NOTICE_KINDS.unshift(probe);
  try {
    assert.equal(noticeLabel('[后台任务通告] 后台任务 go test 结束'), '探针种类');
    assert.equal(noticeBody('[后台任务通告] 后台任务 go test 结束'), 'go test 结束');
  } finally {
    NOTICE_KINDS.shift();
  }
  assert.equal(noticeLabel('[后台任务通告] 后台任务 go test 结束'), '后台任务通告', '临时插入必须还原');
});

test('标签取自 NOTICE_KINDS：后台任务通告那条的标签仍是「后台任务通告」（旧行为不许变）', () => {
  assert.equal(NOTICE_KINDS.length, 2);
  const job = NOTICE_KINDS.find((k) => k.label === '后台任务通告');
  assert.ok(job, '后台任务通告必须在表里');
  assert.equal(job.prefix, '[后台任务通告] ');
  const repeat = NOTICE_KINDS.find((k) => k.label === '重复调用提醒');
  assert.ok(repeat, '重复调用提醒必须在表里');
  assert.equal(repeat.prefix, REPEAT_NOTICE_PREFIX);
});

// ---------- 回归钉子：两条路径都不能变成用户气泡 ----------

// 刻意写**字面量**（照抄 Go 的 RepeatNoticePrefix）而不是拼 REPEAT_NOTICE_PREFIX：
// 后端真正发出来的是 Go 那份常量，若前端常量漂移（少尾空格/改字），用常量拼出来的
// 样本会跟着一起漂、断言照样绿——那是假绿。字面量才能让漂移表现为「提醒变成了用户气泡」。
const REPEAT_TEXT = '[重复调用提醒] 你在重复完全相同的工具调用，参数一个字都没变。先仔细看上一次的结果再决定下一步。';

test('重复调用提醒在实时路径上是提示条（label 来自表，不是用户气泡）', () => {
  const s = reduce(base, { type: 'userMessage', sessionId: 's1', text: REPEAT_TEXT });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'notice', 'user 角色的提醒不能变成用户气泡');
  assert.equal(s.blocks[0].label, '重复调用提醒');
  assert.equal(s.blocks[0].text, '你在重复完全相同的工具调用，参数一个字都没变。先仔细看上一次的结果再决定下一步。', '前缀由标签承担，正文不带前缀');
  // 普通用户消息照旧是气泡
  const s2 = reduce(s, { type: 'userMessage', sessionId: 's1', text: '帮我看下构建' });
  assert.equal(s2.blocks.at(-1).kind, 'user');
});

test('重复调用提醒在历史回放里同样是提示条（两条路径必须一致）', () => {
  const s = reduce(base, {
    type: 'historyLoaded', sessionId: 's1',
    history: {
      sessionId: 's1', busy: false, pending: null, todos: [],
      messages: [
        { role: 'user', content: '帮我看下构建' },
        { role: 'assistant', content: '好' },
        { role: 'user', content: REPEAT_TEXT },
      ],
    },
  });
  assert.equal(s.blocks[0].kind, 'user');
  const last = s.blocks.at(-1);
  assert.equal(last.kind, 'notice', '刷新后同一句话不能换张脸');
  assert.equal(last.label, '重复调用提醒');
  assert.equal(last.text, '你在重复完全相同的工具调用，参数一个字都没变。先仔细看上一次的结果再决定下一步。');
});
