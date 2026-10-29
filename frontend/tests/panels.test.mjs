// 「已发送消息」大纲的纯函数（components/panels/outline.ts）。
//
// 为什么值得单独钉住：面板列的是**用户自己说过的话**，而历史里 notice 提示条
// 同样是 user 角色消息（后端必须让模型把它当用户回合）。一旦判定从 kind 放宽成
// 「所有带 text 的块」，提示条就会静默混进「我发过的消息」——用户看到一条自己
// 没发过的话，以为会话被串了。这个仓库已经踩过一次（tests/notices.test.mjs）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { outlineItems, outlineLabel } from '../src/components/panels/outline.ts';

// 造块的小工具：只填判定用得到的字段（uid/kind/text/seq），其余字段与本文件无关
let uid = 0;
const user = (text, seq) => ({ kind: 'user', uid: ++uid, text, ...(seq === undefined ? {} : { seq }) });
const notice = (text) => ({ kind: 'notice', uid: ++uid, label: '重复调用提醒', text });
const assistant = (content) => ({ kind: 'assistant', uid: ++uid, content, reasoning: '', streaming: false });
const tool = (name) => ({ kind: 'tool', uid: ++uid, id: 't' + uid, name, arguments: '{}' });

// ① 只取 user 块：notice 在历史里也是 user 角色，但它不是用户说的话
test('只取 user 块：notice / assistant / tool 都不进大纲（notice 不是用户说的话）', () => {
  const blocks = [
    user('帮我看下构建', 1),
    assistant('好的'),
    notice('[重复调用提醒] 你在重复完全相同的工具调用。'),
    tool('bash'),
    user('再跑一次测试', 2),
  ];
  const items = outlineItems(blocks);
  assert.equal(items.length, 2, '只有两条是真用户消息');
  assert.deepEqual(items.map((i) => i.text), ['帮我看下构建', '再跑一次测试']);
  assert.deepEqual(items.map((i) => i.uid), [blocks[0].uid, blocks[4].uid], 'uid 必须是块自己的 uid（跳转锚点）');
  assert.deepEqual(items.map((i) => i.index), [0, 4], 'index 是块在时间线里的下标，不是大纲里的下标');
  // 反例对照就靠这条：把 notice 也放进来，items.length 立刻变 3
  assert.equal(items.some((i) => i.text.includes('重复调用提醒')), false, '提示条正文不许出现在大纲里');
});

// ② 长文本单行截断
test('outlineLabel：折叠换行 + 超长截断成单行', () => {
  assert.equal(outlineLabel('第一行\n第二行\t第三行'), '第一行 第二行 第三行', '换行/制表符折叠成空格');
  assert.equal(outlineLabel('  两边有空白  '), '两边有空白');
  const long = 'あ'.repeat(120);
  const label = outlineLabel(long);
  assert.equal([...label].length, 41, '默认 40 字 + 省略号');
  assert.equal(label.endsWith('…'), true);
  assert.equal(label.includes('\n'), false);
  // 刚好等于上限不截断（边界：40 不加省略号）
  assert.equal(outlineLabel('a'.repeat(40)), 'a'.repeat(40));
  assert.equal([...outlineLabel('a'.repeat(41))].length, 41);
  // 自定义上限
  assert.equal(outlineLabel('abcdefghij', 4), 'abcd…');
  // 空文本不崩
  assert.equal(outlineLabel(''), '');
  assert.equal(outlineLabel('   '), '');
});

// ③ 空列表 → 空数组
test('outlineItems：空列表 → 空数组（面板显示空态，不显示空框）', () => {
  assert.deepEqual(outlineItems([]), []);
  // 只有非用户块时同样是空——面板的空态判定看的就是这个长度
  assert.deepEqual(outlineItems([assistant('你好'), notice('[后台任务通告] 任务结束'), tool('bash')]), []);
});

// ④ 没有 seq 的块不崩（老后端 / 更早落库的历史都不带 seq）
test('没有 seq 的块不崩：seq 缺席时条目照常产出，seq 为 undefined 而不是编一个数', () => {
  const blocks = [user('老后端的历史消息'), user('有 seq 的', 7)];
  const items = outlineItems(blocks);
  assert.equal(items.length, 2);
  assert.equal(items[0].seq, undefined, '没有 seq 就是 undefined——不许拿 index 顶替（撤回锚点会锚错位置）');
  assert.equal(items[1].seq, 7);
  assert.equal(items[0].text, '老后端的历史消息');
  // 整条链路上都不能抛（历史来自 SQLite，残缺记录是既成事实）
  assert.doesNotThrow(() => outlineItems([user(undefined)]));
});

// ⑤ 保留原始顺序：大纲的顺序必须与时间线一致（跳转要按时间读）
test('条目顺序与时间线一致，uid 唯一', () => {
  const blocks = [user('一'), assistant('答'), user('二'), user('三')];
  const items = outlineItems(blocks);
  assert.deepEqual(items.map((i) => i.text), ['一', '二', '三']);
  assert.equal(new Set(items.map((i) => i.uid)).size, 3);
});
