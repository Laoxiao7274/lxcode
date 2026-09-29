// 「子 Agent 执行」列表的纯函数（components/panels/dispatch-list.ts）。
//
// 为什么值得单独钉住：面板要回答的是「子 Agent 跑到哪一步了」。判定一旦从 kind 放宽成
// 「带 task 字段的块」或「agent_dispatch 的工具行」，同一件事会在面板里列两遍——而工具行
// 并不携带子 Agent 的状态与用量（running/done、usageTokens 都在 dispatch 块上），混进来
// 只会得到一行没有状态、点了也没有卡可跳的空壳。这个仓库为同一类放宽吃过亏
// （tests/notices.test.mjs：按"带 text"筛会把提示条列进「我发过的消息」）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { dispatchItems } from '../src/components/panels/dispatch-list.ts';

// 造块的小工具：只填判定用得到的字段，其余与本文件无关
let uid = 0;
const dispatch = (over = {}) => ({
  kind: 'dispatch', uid: ++uid, id: 'd' + uid, agentId: 'coder', agentName: 'coder',
  agentColor: '#10a37f', task: '实现 X', status: 'running', subBlocks: [], ...over,
});
const user = (text) => ({ kind: 'user', uid: ++uid, text });
const assistant = (content) => ({ kind: 'assistant', uid: ++uid, content, reasoning: '', streaming: false });
const tool = (name) => ({ kind: 'tool', uid: ++uid, id: 't' + uid, name, arguments: '{}' });
// 反例对照的靶子：一个**非 dispatch** 的块，但它带着 task 字段（后台任务卡的命令摘要在
// 别处的实现里就叫 task）。把判定放宽成「所有带 task 字段的块」时它会立刻混进列表。
const jobWithTask = () => ({ kind: 'job', uid: ++uid, job: { id: 'j1', label: '跑测试' }, task: '跑测试' });

// ① 只取 dispatch 块：user / assistant / tool（含 agent_dispatch 工具行）/ 带 task 的 job 都不算
test('只取 dispatch 块：user / assistant / tool / 带 task 的非 dispatch 块都不进列表', () => {
  const blocks = [
    user('帮我看下构建'),
    assistant('好的'),
    tool('agent_dispatch'),   // 派发那条工具调用行本身：有 arguments，没有状态与用量
    dispatch({ task: '实现 X' }),
    jobWithTask(),            // 带 task 字段但不是派发
    dispatch({ task: '验证 Y' }),
  ];
  const items = dispatchItems(blocks);
  assert.equal(items.length, 2, '只有两个块是真派发');
  assert.deepEqual(items.map((i) => i.task), ['实现 X', '验证 Y']);
  assert.deepEqual(items.map((i) => i.uid), [blocks[3].uid, blocks[5].uid], 'uid 必须是块自己的 uid（跳转锚点）');
  // 反例对照就靠这两条：判定放宽成「带 task 字段的块」→ items.length 立刻变 3
  assert.equal(items.some((i) => i.task === '跑测试'), false, '带 task 字段的非 dispatch 块不许混进来');
  assert.equal(items.some((i) => i.agentName === undefined), false, '工具行（无 agentName）也不许混进来');
});

// ② 状态三态映射：running / done / done + isError（三态要能一眼分开）
test('状态三态映射：running / done / done+isError，isError 与 status 正交', () => {
  const [running, done, failed] = dispatchItems([
    dispatch({ status: 'running' }),
    dispatch({ status: 'done', result: '改完了' }),
    dispatch({ status: 'done', isError: true, result: '编译失败' }),
  ]);
  assert.equal(running.status, 'running');
  assert.equal(running.isError, false, '运行中不是错误');
  assert.equal(done.status, 'done');
  assert.equal(done.isError, false);
  assert.equal(failed.status, 'done', '失败也是 done（子 Agent 跑完了，只是没跑成）');
  assert.equal(failed.isError, true, '失败靠 isError 区分——面板按它选危险色');
  // 老后端不带 isError 字段，或状态是意料外的字符串：都要收敛成面板认得的取值
  const noFlag = dispatchItems([dispatch({ status: 'done', isError: undefined })])[0];
  assert.equal(noFlag.isError, false, 'isError 缺席按 false，不是 undefined（面板按它选色）');
  const weird = dispatchItems([dispatch({ status: 'cancelled' })])[0];
  assert.equal(weird.status, 'running', '意料外的状态收敛成 running，不留空白状态');
});

// ③ 空列表 → 空数组（面板显示空态一句话，不显示空框）
test('空列表 → 空数组；只有非派发块时同样为空', () => {
  assert.deepEqual(dispatchItems([]), []);
  assert.deepEqual(dispatchItems([user('你好'), assistant('在'), tool('bash'), jobWithTask()]), []);
});

// ④ 缺 result / usageTokens / sessionId 不崩：字段照常缺席，不编造
test('缺 result / usageTokens / sessionId 不崩：缺席就是 undefined，不许编一个数', () => {
  const blocks = [
    dispatch({ status: 'done', result: undefined }),
    // 老后端的派发块：连 sessionId / usageTokens / isError 都没有
    { kind: 'dispatch', uid: 999, id: 'd999', agentId: 'coder', agentName: 'coder', agentColor: '#10a37f', task: '老后端的派发', status: 'running', subBlocks: [] },
  ];
  const items = dispatchItems(blocks);
  assert.equal(items.length, 2);
  assert.equal(items[0].usageTokens, undefined);
  assert.equal(items[0].sessionId, undefined);
  assert.equal(items[1].usageTokens, undefined, '没有用量就是 undefined——不许编 0（0 tk 是假用量）');
  assert.equal(items[1].sessionId, undefined);
  assert.equal(items[1].task, '老后端的派发');
  // 整条链路上都不能抛（历史来自 SQLite，残缺记录是既成事实）
  assert.doesNotThrow(() => dispatchItems([dispatch({ task: undefined, agentColor: undefined, usageTokens: undefined })]));
});

// ⑤ 顺序与时间线一致，uid 唯一（跳转要按时间读）
test('条目顺序与时间线一致，uid 唯一', () => {
  const blocks = [dispatch({ task: '一' }), user('插一条'), dispatch({ task: '二' }), dispatch({ task: '三' })];
  const items = dispatchItems(blocks);
  assert.deepEqual(items.map((i) => i.task), ['一', '二', '三']);
  assert.equal(new Set(items.map((i) => i.uid)).size, 3);
});
