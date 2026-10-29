// 分页装载（2026-12 装载性能）的钉子：
//
// 后端协议 `ChatHistoryParams.tail / before_seq` 已实现（internal/server/session_ops.go
// 的 history()：tail → Session.TailHistory 配平回退；before_seq → Session.HistoryBefore
// 返回前缀窗口 + has_more/first_seq）。本文件钉前端这一侧的三件事：
//
// ① 参数构造（historyParams）：首屏只拉尾部窗口；上翻必须**同时**带 tail 与
//    before_seq（协议要求，缺 tail 后端判 CodeInvalidParams）；字段名与 Go 侧逐字一致。
// ② 首屏 tail 应答的分页推导（deriveTailPaging）：协议只在 before_seq 应答里带
//    has_more/first_seq，首屏必须从**真实返回条数**推出「还有更早的」——不是编一个值。
// ③ 增量合并（mergeOlderHistory）：更早一页前插、不动实时状态、确认卡不重复、
//    分页元数据随应答更新（下一次上翻的 anchor）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  deriveTailPaging,
  historyParams,
  HISTORY_PAGE,
  HISTORY_TAIL,
  snapshotFromWire,
} from '../src/agent/ws/history-snapshot.ts';
import { mergeOlderHistory } from '../src/shared/history.ts';
import { reduce as reduceImpl } from '../src/shared/store.ts';
import { initial, timelineOf } from '../src/shared/blocks.ts';

const snapshot = (messages, extra = {}) => ({
  sessionId: 's1', messages, busy: false, pending: null, todos: [], ...extra,
});

test('① historyParams：首屏只拉尾部窗口（tail），不带 before_seq', () => {
  assert.deepEqual(historyParams('s1'), { session_id: 's1', tail: HISTORY_TAIL });
  // 0/负 anchor 不构成分页（后端把 ≤0 判 CodeInvalidParams——不许发出去）
  assert.deepEqual(historyParams('s1', 0), { session_id: 's1', tail: HISTORY_TAIL });
  assert.deepEqual(historyParams('s1', -5), { session_id: 's1', tail: HISTORY_TAIL });
});

test('①b historyParams：上翻必须 tail + before_seq 同时带（字段名与 Go 逐字一致）', () => {
  assert.deepEqual(historyParams('s1', 42), { session_id: 's1', tail: HISTORY_PAGE, before_seq: 42 });
  assert.equal(HISTORY_PAGE > 0, true, '窗口大小必须是正整数（后端 n≤0 报错）');
});

test('② deriveTailPaging：条数达到窗口 ⇒ 可能还有更早的，anchor = 首条 seq', () => {
  const messages = Array.from({ length: HISTORY_TAIL }, (_, i) => ({ role: 'user', content: `m${i}`, seq: 100 + i }));
  const snap = deriveTailPaging(snapshot(messages), HISTORY_TAIL);
  assert.equal(snap.hasMore, true, '窗口被截过 → 还能继续上翻');
  assert.equal(snap.firstSeq, 100, 'anchor = 窗口首条的库序号（客户端原样传回 before_seq）');
});

test('②b deriveTailPaging：条数少于窗口 ⇒ 后端已给全量，不再上翻', () => {
  const messages = [{ role: 'user', content: 'a', seq: 3 }, { role: 'assistant', content: 'b', seq: 4 }];
  const snap = deriveTailPaging(snapshot(messages), HISTORY_TAIL);
  assert.equal(snap.hasMore, false, '少于请求窗口 = 已到历史开头');
  assert.equal(snap.firstSeq, 3);
});

test('②c deriveTailPaging：分页应答（后端权威 has_more/first_seq）不被覆盖', () => {
  const wire = snapshotFromWire({
    session_id: 's1',
    messages: [{ role: 'user', content: 'a', seq: 7 }],
    has_more: true,
    first_seq: 7,
  });
  const snap = deriveTailPaging(wire, HISTORY_TAIL);
  assert.equal(snap.hasMore, true, '后端明确说还有 → 即使条数少也照它（不猜）');
  assert.equal(snap.firstSeq, 7);
  // 空窗口（anchor 已是历史第一条）：has_more=false，first_seq=0 → 前端不该拿 0 发请求
  const empty = deriveTailPaging(snapshotFromWire({ session_id: 's1', messages: [], has_more: false }), HISTORY_TAIL);
  assert.equal(empty.hasMore, false);
  assert.equal(empty.firstSeq, undefined);
});

test('③ mergeOlderHistory：更早一页前插、分页元数据更新、实时状态不动', () => {
  const live = {
    ...initial,
    blocks: [{ kind: 'user', uid: 1, text: '当前可见的第一条', seq: 500 }],
    streaming: { kind: 'assistant', uid: 2, content: '正在写', reasoning: '', streaming: true },
    historyReady: true,
    historyMore: true,
    historyFirstSeq: 500,
  };
  const merged = mergeOlderHistory(live, snapshot(
    [{ role: 'user', content: '更早的问题', seq: 100 }, { role: 'assistant', content: '更早的回答', seq: 101 }],
    { hasMore: true, firstSeq: 100 },
  ));
  // 顺序：更早的在前，原有块在后；流式块仍单独存（逻辑时间线末尾）
  assert.deepEqual(timelineOf(merged).map((b) => b.content ?? b.text),
    ['更早的问题', '更早的回答', '当前可见的第一条', '正在写']);
  assert.equal(merged.historyMore, true);
  assert.equal(merged.historyFirstSeq, 100, '下一次 before_seq 的 anchor 跟着前移');
  // 原有块对象引用不变（memo(Block) 的稳定性）
  assert.strictEqual(merged.blocks.at(-1), live.blocks[0]);
  assert.strictEqual(merged.streaming, live.streaming);
});

test('③b mergeOlderHistory：确认卡不重复（分页应答同样带 pending 卡）', () => {
  const request = { id: 'c1', name: 'bash', arguments: '{}', prompt: 'run' };
  const live = {
    ...initial,
    blocks: [{ kind: 'confirm', uid: 9, request }],
    historyReady: true,
  };
  const merged = mergeOlderHistory(live, {
    ...snapshot([{ role: 'user', content: '更早', seq: 1 }]),
    pending: request,
    hasMore: false,
    firstSeq: 1,
  });
  assert.equal(merged.blocks.filter((b) => b.kind === 'confirm').length, 1,
    '同一张待裁决确认卡不许在时间线里出现两遍');
  assert.equal(merged.historyMore, false, '后端说已到头 → 顶部不再显示「加载更早」');
});

test('③d 子会话分页走 childHistory（child 标记不能丢——任务说明书不是用户气泡）', () => {
  const merged = mergeOlderHistory({ ...initial, blocks: [], historyReady: true }, {
    ...snapshot([{ role: 'user', content: '更早的任务说明书', seq: 1 }]),
    child: true, hasMore: false, firstSeq: 1,
  });
  assert.equal(merged.blocks[0].kind, 'task', '子会话历史里的 user 消息是派发的任务说明书 → 任务块');
});

test('③c 归约：只有 paging:true 的 historyLoaded 才前插（重拉必须重建）', () => {
  const base = {
    ...initial,
    historyReady: true,
    blocks: [{ kind: 'user', uid: 1, text: '当前', seq: 50 }],
    historyMore: true,
    historyFirstSeq: 50,
  };
  const older = snapshot([{ role: 'user', content: '更早', seq: 10 }], { hasMore: false, firstSeq: 10 });
  // 首屏/重拉（没有 paging 标记）→ 重建：本地块被快照整体替换（回放语义）
  const rebuilt = reduceImpl(base, { type: 'historyLoaded', sessionId: 's1', history: older });
  assert.deepEqual(rebuilt.blocks.map((b) => b.text), ['更早'], '非分页应答照旧重建整条时间线');
  // 上翻分页 → 前插：本地块保留在后面
  const merged = reduceImpl(base, { type: 'historyLoaded', sessionId: 's1', history: older, paging: true });
  assert.deepEqual(merged.blocks.map((b) => b.text), ['更早', '当前'], '分页应答只前插，不动本地时间线');
  assert.equal(merged.historyMore, false);
  assert.equal(merged.historyFirstSeq, 10);
  // 还没装载过（historyReady=false）→ 没有基线可前插，仍走重建
  const cold = reduceImpl({ ...initial }, { type: 'historyLoaded', sessionId: 's1', history: older, paging: true });
  assert.deepEqual(cold.blocks.map((b) => b.text), ['更早']);
});

test('④ 流式热路径：delta 不复制已定型数组（引用与元素身份都不变）', () => {
  let s = { ...initial, blocks: [
    { kind: 'user', uid: 1, text: '问' },
    { kind: 'assistant', uid: 2, content: '第一轮', reasoning: '', streaming: false },
    { kind: 'tool', uid: 3, id: 't1', name: 'read_file', arguments: '{}', result: 'ok' },
  ] };
  const blocks0 = s.blocks;
  s = reduceImpl(s, { type: 'delta', kind: 'text', text: '新' });
  assert.strictEqual(s.blocks, blocks0, '开新流式块时不复制已定型数组（O(1)）');
  const streaming0 = s.streaming;
  s = reduceImpl(s, { type: 'delta', kind: 'text', text: '回复' });
  assert.strictEqual(s.blocks, blocks0, '逐帧 delta 仍然不复制已定型数组');
  assert.notStrictEqual(s.streaming, streaming0, '流式块换新对象（引用变化驱动重渲染）');
  assert.equal(s.streaming.content, '新回复');
  // 定型（done）时一次性并入：块对象引用保持（memo(Block) 的稳定性）
  s = reduceImpl(s, { type: 'done', usageTokens: 3, finishReason: 'stop' });
  assert.equal(s.streaming, null);
  assert.strictEqual(s.blocks[0], blocks0[0], '已定型块的对象引用不变（未动的块跳过协调）');
  assert.equal(s.blocks.at(-1).content, '新回复');
  assert.equal(s.blocks.at(-1).streaming, false);
});
