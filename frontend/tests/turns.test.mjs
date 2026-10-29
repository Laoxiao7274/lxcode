// 「轮次树」的纯函数（components/panels/turns.ts）。
//
// 为什么值得单独钉住：右栏要回答的是「某个子 Agent 是哪一轮派出去的」（用户原话「不方便
// 找到对应的子 Agent」）。分组边界一旦从 kind === 'user' 放宽成「所有带 text 的块」，提示条
// notice 也会开一轮——它在历史里同样是 user 角色消息，于是同一轮被劈成两半，子 Agent 归到
// 错误的轮次上。这个仓库为同一类放宽吃过两次亏（tests/notices.test.mjs、tests/panels.test.mjs）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { turnGroups } from '../src/components/panels/turns.ts';

// 造块的小工具：只填判定用得到的字段（uid/kind/text/...），其余字段与本文件无关
let uid = 0;
const user = (text, seq) => ({ kind: 'user', uid: ++uid, text, ...(seq === undefined ? {} : { seq }) });
const notice = (text) => ({ kind: 'notice', uid: ++uid, label: '重复调用提醒', text });
const assistant = (content) => ({ kind: 'assistant', uid: ++uid, content, reasoning: '', streaming: false });
const tool = (name) => ({ kind: 'tool', uid: ++uid, id: 't' + uid, name, arguments: '{}' });
const dispatch = (over = {}) => ({
  kind: 'dispatch', uid: ++uid, id: 'd' + uid, agentId: 'coder', agentName: 'coder',
  agentColor: '#10a37f', task: '实现 X', status: 'running', subBlocks: [], ...over,
});

// ① 一条 user 开一轮，其后的 dispatch 归入该轮
test('一条 user 开一轮，其后的 dispatch 归入该轮', () => {
  const blocks = [user('帮我看下构建'), assistant('好的'), dispatch({ task: '实现 X' }), dispatch({ task: '验证 Y' })];
  const groups = turnGroups(blocks);
  assert.equal(groups.length, 1, '只有一条 user → 只有一轮');
  assert.equal(groups[0].turn, 1);
  assert.equal(groups[0].uid, blocks[0].uid, '组头的锚点就是那条 user 块的 uid');
  assert.deepEqual(groups[0].agents.map((a) => a.task), ['实现 X', '验证 Y'], '组内按时间顺序列出这一轮的派发');
  assert.deepEqual(groups[0].agents.map((a) => a.uid), [blocks[2].uid, blocks[3].uid], 'uid 是派发块自己的 uid（跳转锚点）');
});

// ② 两条 user → 两轮，dispatch 各归其主（**用户的核心诉求：能看出子 Agent 属于哪一轮**）
test('两条 user → 两轮，dispatch 各归其主', () => {
  const blocks = [
    user('先做 A'),
    dispatch({ task: 'A-1' }),
    assistant('A 做完了'),
    user('再做 B'),
    dispatch({ task: 'B-1' }),
    dispatch({ task: 'B-2' }),
  ];
  const groups = turnGroups(blocks);
  assert.equal(groups.length, 2);
  assert.deepEqual(groups.map((g) => g.turn), [1, 2]);
  assert.deepEqual(groups.map((g) => g.text), ['先做 A', '再做 B']);
  assert.deepEqual(groups.map((g) => g.uid), [blocks[0].uid, blocks[3].uid], '组头锚点各是自己的那条 user');
  assert.deepEqual(groups[0].agents.map((a) => a.task), ['A-1'], '第一条 user 之后的派发属于第一轮');
  assert.deepEqual(groups[1].agents.map((a) => a.task), ['B-1', 'B-2'], '第二条 user 之后的派发属于第二轮');
});

// ③ notice 不开启新轮（它在历史里也是 user 角色消息——后端必须让模型把它当用户回合）
test('notice 块不开启新轮：轮内的提示条不会把一轮劈成两半', () => {
  const blocks = [
    user('帮我看下构建'),
    notice('[重复调用提醒] 你在重复完全相同的工具调用。'),
    dispatch({ task: 'A-1' }),
  ];
  const groups = turnGroups(blocks);
  assert.equal(groups.length, 1, 'notice 不是轮次边界——它前后仍是同一轮');
  assert.equal(groups[0].uid, blocks[0].uid, '组头仍是那条真 user，不是提示条');
  assert.equal(groups[0].text, '帮我看下构建', '提示条正文不许当组头');
  assert.deepEqual(groups[0].agents.map((a) => a.task), ['A-1'], '提示条之后的派发仍归这一轮');
  // 反例对照就靠这两条：把判定放宽成「所有带 text 的块」→ groups.length 立刻变 2，
  // 而且第二轮会被提示条顶成组头（用户看到一条自己没发过的话）
  assert.equal(groups.some((g) => g.text.includes('重复调用提醒')), false, '提示条正文不许出现在任何组头上');
  // 只有提示条、没有真 user 时连一组都不该产出
  assert.deepEqual(turnGroups([notice('[后台任务通告] 任务结束')]), []);
});

// ④ 第一条 user 之前的 dispatch → 自成一组且 uid: null（没有可跳的锚点）
test('第一条 user 之前的 dispatch 自成一组，uid 为 null（没有锚点就不编一个）', () => {
  const blocks = [dispatch({ task: '开头就派发了' }), user('接着说'), dispatch({ task: 'B-1' })];
  const groups = turnGroups(blocks);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].uid, null, '没有可跳的锚点 → null（面板据此渲染成不可点的「会话开始」行）');
  assert.equal(groups[0].text, '', '这一组没有用户消息，text 是空串');
  assert.deepEqual(groups[0].agents.map((a) => a.task), ['开头就派发了']);
  assert.deepEqual(groups[1].agents.map((a) => a.task), ['B-1'], 'user 之后的派发归 user 那一轮，不留在开头组');
  // 序号是**产出的分组**的序号（不跳号：1、3 会让人以为丢了一轮）
  assert.deepEqual(groups.map((g) => g.turn), [1, 2]);
  // 第一条 user 之前**没有**派发时不许产出空组（否则顶出一个没有正文、点了也没反应的组头）
  assert.equal(turnGroups([assistant('在'), user('你好')]).length, 1);
});

// ⑤ 没有 dispatch 的轮次 → agents 为空数组，且组照样产出（用户要能点它跳转）
test('没有派发的轮次照样产出，agents 是空数组（组头可点、跳转入口不能因为没派发就消失）', () => {
  const blocks = [user('只是聊两句'), assistant('好的'), user('还是聊天')];
  const groups = turnGroups(blocks);
  assert.equal(groups.length, 2);
  assert.deepEqual(groups.map((g) => g.agents), [[], []], '没有派发就是空数组，不是 null');
  assert.deepEqual(groups.map((g) => g.uid), [blocks[0].uid, blocks[2].uid]);
});

// ⑥ 空列表 → 空数组；没有 seq 的块不崩
test('空列表 → 空数组；没有 seq 的块不崩', () => {
  assert.deepEqual(turnGroups([]), []);
  // 只有非 user / 非 dispatch 的块 → 没有任何组（面板显示空态，不显示空框）
  assert.deepEqual(turnGroups([assistant('你好'), tool('bash'), notice('[后台任务通告] 任务结束')]), []);
  // 老后端 / 更早落库的历史不带 seq：整条链路上都不能抛
  const groups = turnGroups([user('老后端的历史消息'), dispatch({ task: '老派发' })]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].text, '老后端的历史消息');
  assert.doesNotThrow(() => turnGroups([user(undefined), dispatch({ task: undefined, agentColor: undefined })]));
});

// ⑦ 组头的单行摘要：复用 outlineLabel（折叠换行 + 按码点截断），原文照旧保留在 text 里
test('组头 label 是单行摘要，text 保留原文', () => {
  const blocks = [user('第一行\n第二行'), user('あ'.repeat(120))];
  const groups = turnGroups(blocks);
  assert.equal(groups[0].label, '第一行 第二行', '换行折叠成空格（侧栏一行放不下两行字）');
  assert.equal(groups[0].text, '第一行\n第二行', '原文不动——纯函数不预设侧栏有多宽');
  assert.equal([...groups[1].label].length, 41, '默认 40 字 + 省略号');
  assert.equal(groups[1].label.endsWith('…'), true);
});
