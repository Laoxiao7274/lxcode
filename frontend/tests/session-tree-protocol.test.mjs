// 会话树的协议对接（批次 4）：chat.setLeaf / chat.tree / chat.leafChanged 的 wire
// 形状、leafChanged 的重拉纪律、以及 files:true 冲突时的乐观截断回滚。
//
// 为什么值得单独钉住：这几条都是"错了不会编译报错、只静默错"的地方——
//   · wire 字段名（`max_nodes` 写成 `maxNodes` → 后端当缺省处理，用户点「加载全部」没反应）；
//   · `chat.tree` 不在 HEAVY_METHODS 里 → 大会话 10s 快判误伤，右栏一直空着；
//   · files:true 冲突是「请求成功、但什么都没改」——没有事件来纠正本地乐观截断，
//     漏了这条重拉，用户会看到时间线被截断了而历史其实没动（刷新一次全回来）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { WSAgent } from '../src/agent/ws/index.ts';
import { mapEvent } from '../src/agent/ws/events.ts';
import { treeFromWire } from '../src/agent/ws/tree.ts';
import { rewindParams, setLeafParams } from '../src/shared/blocks.ts';

/** 插桩一个 WSAgent：call 换成记录器（不起真连接）。 */
function stubAgent(responder = () => ({})) {
  const agent = new WSAgent('localhost:1');
  const calls = [];
  agent.emit = () => {};
  agent.call = (method, params) => {
    calls.push({ method, params });
    return Promise.resolve(responder(method, params));
  };
  return { agent, calls };
}

// ---------- 1. 参数形状与 Go 侧逐字一致 ----------

test('chat.rewind / chat.setLeaf 的参数与 Go 侧逐字一致（files 只在 true 时出现）', () => {
  assert.deepEqual(rewindParams('s1', 7), { session_id: 's1', seq: 7 });
  assert.deepEqual(rewindParams('s1', 7, true), { session_id: 's1', seq: 7, files: true });
  assert.deepEqual(rewindParams('s1', 7, false), { session_id: 's1', seq: 7 }, 'false 不写这个键（omitempty 语义）');
  assert.deepEqual(setLeafParams('s1', 0), { session_id: 's1', seq: 0 }, 'seq=0 是合法的空路径');
  assert.deepEqual(setLeafParams('s1', 12, true), { session_id: 's1', seq: 12, files: true });
  assert.deepEqual(Object.keys(setLeafParams('s1', 12)).sort(), ['seq', 'session_id']);
});

test('setLeaf 走 chat.setLeaf 并重拉历史（应答不回 messages）', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.setLeaf') return { session_id: 's1', leaf_seq: 12, removed: 2 };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  const out = await agent.setLeaf('s1', 12);
  assert.deepEqual(out, { leafSeq: 12, removed: 2 });
  assert.deepEqual(calls.map((c) => c.method), ['chat.setLeaf', 'chat.history'], '切叶后必须重拉历史（对齐路径/统计/上下文）');
  assert.deepEqual(calls[0].params, { session_id: 's1', seq: 12 });
});

test('setLeaf 的 files 结果原样回给调用方（含冲突清单）', async () => {
  const files = { conflicts: [{ path: 'a.go', reason: '磁盘内容与快照期望不符' }] };
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.setLeaf') return { leaf_seq: 5, removed: 0, files };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  const out = await agent.setLeaf('s1', 5, true);
  assert.deepEqual(calls[0].params, { session_id: 's1', seq: 5, files: true });
  assert.deepEqual(out.files, files, '冲突清单必须带给调用方（弹窗逐文件展示）');
  assert.equal(out.removed, 0, '冲突时 leaf 未变（removed=0）');
});

test('setLeaf 失败必须抛（本地什么都没改，不许静默 no-op）', async () => {
  const { agent } = stubAgent((method) => {
    if (method === 'chat.setLeaf') throw new Error('这条消息后面还有未完成的工具调用');
    return {};
  });
  await assert.rejects(() => agent.setLeaf('s1', 3), /未完成的工具调用/);
});

// ---------- 2. chat.tree ----------

test('chat.tree 的参数：max_nodes 只在 >0 时带（0 = 后端缺省）', async () => {
  const { agent, calls } = stubAgent(() => ({ leaf_seq: 0, nodes: [], total: 0 }));
  await agent.tree('s1');
  assert.deepEqual(calls.at(-1), { method: 'chat.tree', params: { session_id: 's1' } }, '缺省不带 max_nodes');
  await agent.tree('s1', { maxNodes: 400 });
  assert.deepEqual(calls.at(-1).params, { session_id: 's1', max_nodes: 400 });
  await agent.tree('s1', { maxNodes: 0 });
  assert.deepEqual(calls.at(-1).params, { session_id: 's1' }, 'max_nodes=0 不写这个键（后端按缺省上限）');
  await agent.tree('s1', { maxNodes: 400, include: 'full' });
  assert.deepEqual(calls.at(-1).params, { session_id: 's1', max_nodes: 400, include: 'full' });
});

test('treeFromWire：snake_case → camelCase，可选键缺席不落成假零值', () => {
  const tree = treeFromWire({
    leaf_seq: 3,
    total: 3,
    truncated: true,
    nodes: [
      { seq: 1, parent_seq: 0, kind: 'user', turn: 1, summary: '你好', children: 1, on_path: true },
      { seq: 2, parent_seq: 1, kind: 'assistant', children: 1, on_path: true, files: 2, child_files: 1, external: 1 },
      { seq: 3, parent_seq: 2, kind: 'checkpoint', children: 0, on_path: true, shadowed: 4 },
    ],
  });
  assert.equal(tree.leafSeq, 3);
  assert.equal(tree.truncated, true);
  assert.equal(tree.nodes[0].parentSeq, 0);
  assert.equal(tree.nodes[0].summary, '你好');
  assert.equal(tree.nodes[0].files, undefined, '没有 files 的节点不带这个键（不显示「0 文件」）');
  assert.equal(tree.nodes[1].childFiles, 1);
  assert.equal(tree.nodes[1].external, 1);
  assert.equal(tree.nodes[2].shadowed, 4);
  assert.equal(tree.nodes[2].turn, undefined, 'checkpoint 没有轮次');
});

test('treeFromWire 容错：畸形载荷不抛（未知 kind / 缺 seq / 非数组 nodes）', () => {
  const tree = treeFromWire({
    leaf_seq: 5,
    nodes: [
      { seq: 1, parent_seq: 0, kind: 'weird-kind', children: 0, on_path: true },
      { parent_seq: 0, kind: 'user', children: 0, on_path: true }, // 缺 seq → 跳过
      null,
    ],
  });
  assert.equal(tree.nodes.length, 1, '缺 seq 的节点被跳过（假 seq 会被当成跳转锚点）');
  assert.equal(tree.nodes[0].kind, 'assistant', '未知 kind 归到 assistant（不丢节点、不炸）');
  assert.equal(tree.total, 1, 'total 缺席时回落节点数');
  assert.equal(treeFromWire(null).nodes.length, 0, 'null 载荷 → 空树');
  assert.equal(treeFromWire({}).nodes.length, 0, 'nodes 非数组 → 空树');
});

// ---------- 3. chat.leafChanged ----------

test('chat.leafChanged 映射成 leafChanged 事件（prev 缺席不落成 0）', () => {
  const ev = mapEvent('chat.leafChanged', {
    session_id: 's1', leaf_seq: 12, prev_leaf_seq: 9, removed: 3,
    context: { used: 100, window: 8192 },
    files: { child_sessions: ['c1'] },
  });
  assert.equal(ev.type, 'leafChanged');
  assert.equal(ev.leafSeq, 12);
  assert.equal(ev.prevLeafSeq, 9);
  assert.equal(ev.removed, 3);
  assert.deepEqual(ev.context, { used: 100, window: 8192 });
  assert.deepEqual(ev.files, { child_sessions: ['c1'] });

  // prev_leaf_seq 缺席（0）→ 不写这个键（0 是合法的「空路径」，编不出来源）
  const noPrev = mapEvent('chat.leafChanged', { session_id: 's1', leaf_seq: 0, removed: 1 });
  assert.equal('prevLeafSeq' in noPrev, false);
  assert.equal(noPrev.leafSeq, 0, 'leaf=0 是空路径（不是"未知"）');
  assert.equal(noPrev.context, undefined, '未知占用 = undefined（指示器中性态）');
});

test('leafChanged 到达 → 重拉本会话历史（leaf 是共享状态，可能别的客户端动的）', async () => {
  // 走真链路：直接驱动连接的 handleEvent（与 ws.onmessage 同一条路径）
  const agent = new WSAgent('localhost:1');
  const calls = [];
  agent['call'] = (method, params) => {
    calls.push({ method, params });
    return Promise.resolve({ session_id: params?.session_id ?? '', messages: [], busy: false, todos: [] });
  };
  const conn = agent['conn'];
  conn['handleEvent']('chat.leafChanged', { session_id: 's9', leaf_seq: 4, removed: 1 });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(
    calls.map((c) => [c.method, c.params?.session_id]),
    [['chat.history', 's9']],
    'leafChanged 之后必须重拉那条会话的历史',
  );
});

test('本端 rewind 成功 → 同会话 leafChanged 判重跳过重拉（乐观截断已是终态，不闪回）', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') return { removed: 3, leaf_seq: 6 };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  await agent.rewind('s1', 7);
  calls.length = 0;
  // rewind 成功后后端广播 chat.leafChanged——它只是本次操作的回声
  agent['conn']['handleEvent']('chat.leafChanged', { session_id: 's1', leaf_seq: 6, removed: 3 });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(calls, [], '回声不该触发全量重拉（时间线清空又闪回来的根因）');
  // 消费一次之后，同会话**再来一条** leafChanged（别的来源动了叶）照常重拉
  agent['conn']['handleEvent']('chat.leafChanged', { session_id: 's1', leaf_seq: 4, removed: 1 });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(
    calls.map((c) => [c.method, c.params?.session_id]),
    [['chat.history', 's1']],
    '判重只消费一次——别的来源的叶变更不能被误伤',
  );
  // 换会话的 leafChanged 从来不受判重影响
  const { agent: a2, calls: c2 } = stubAgent();
  await a2.rewind('s1', 7);
  c2.length = 0;
  a2['conn']['handleEvent']('chat.leafChanged', { session_id: 'other', leaf_seq: 4, removed: 1 });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(c2.map((c) => c.params?.session_id), ['other'], '别的会话的 leafChanged 照常重拉');
});

test('rewind 失败 → 显式重拉不受判重影响（纠错不是回声）', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') throw new Error('拒绝');
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  await assert.rejects(() => agent.rewind('s1', 7), /拒绝/);
  assert.deepEqual(
    calls.filter((c) => c.method === 'chat.history').map((c) => c.params?.session_id),
    ['s1'],
    '失败后必须重放历史对齐（本地截断是错的）',
  );
});

// ---------- 4. files:true 冲突 → 乐观截断回滚 ----------

test('rewind 带 files 冲突（removed=0）→ 重拉历史把乐观截断回滚', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') {
      return { removed: 0, leaf_seq: 9, branched: true, files: { conflicts: [{ path: 'a.go', reason: 'x' }] } };
    }
    return { session_id: 's1', messages: [{ role: 'user', content: '还在后端', seq: 7 }], busy: false, todos: [] };
  });
  const out = await agent.rewind('s1', 7, true);
  assert.equal(out.removed, 0);
  assert.equal(out.files.conflicts.length, 1);
  assert.deepEqual(calls.map((c) => c.method), ['chat.rewind', 'chat.history'],
    '请求成功但什么都没改 → 必须重拉历史（否则本地截断骗过用户）');
});

test('rewind 带 files 且 removed=0（即使没有 conflicts 键）也重拉', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') return { removed: 0, leaf_seq: 9 };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  await agent.rewind('s1', 7, true);
  assert.deepEqual(calls.map((c) => c.method), ['chat.rewind', 'chat.history'], 'removed=0 是另一个事实源（不只靠 conflicts）');
});

test('rewind 不带 files：成功时不重拉（乐观截断就是最终状态）', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') return { removed: 3, leaf_seq: 4, branched: true };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  const out = await agent.rewind('s1', 7);
  assert.deepEqual(calls.map((c) => c.method), ['chat.rewind'], '纯历史指针移动不需要重拉（rewound 事件已让归约器截断）');
  assert.equal(out.leaf_seq, 4, 'leaf_seq 用 wire 名（RewindOutcome 是协议形态）');
  assert.equal(out.branched, true);
});

test('rewind 的 files 参数上车 + 结果里的 leaf_seq/branched 透传', async () => {
  const { agent, calls } = stubAgent((method) => {
    if (method === 'chat.rewind') return { removed: 2, leaf_seq: 5, branched: true, files: { restored: [{ path: 'a.go', from_seq: 6, bytes: 10 }] } };
    return { session_id: 's1', messages: [], busy: false, todos: [] };
  });
  const out = await agent.rewind('s1', 7, true);
  assert.deepEqual(calls[0].params, { session_id: 's1', seq: 7, files: true });
  assert.equal(out.files.restored.length, 1);
});

// ---------- 5. 老客户端路径保留 ----------

test('老客户端路径保留：chat.rewound 仍映射成 rewound 事件（归约器照旧截断）', () => {
  const ev = mapEvent('chat.rewound', { session_id: 's1', seq: 20, removed: 3 });
  assert.equal(ev.type, 'rewound');
  assert.equal(ev.seq, 20);
  assert.equal(ev.removed, 3);
});

// ---------- 6. HEAVY_METHODS ----------

test('chat.tree / chat.setLeaf 在宽容超时集合里（大会话扫骨架 / 磁盘 IO）', async () => {
  const { WsConnection } = await import('../src/agent/ws/connection.ts');
  // HEAVY_METHODS 是模块级常量，不导出——用行为断言：call 的超时计时器按 30s 起算。
  // 直接读模块源码（比拿 30s 真等更可靠，且这条纪律是"常量表里有这几个方法"）。
  const src = await import('node:fs').then((fs) =>
    fs.readFileSync(new URL('../src/agent/ws/connection.ts', import.meta.url), 'utf8'));
  const block = src.slice(src.indexOf('const HEAVY_METHODS'), src.indexOf(']);', src.indexOf('const HEAVY_METHODS')));
  for (const method of ['chat.tree', 'chat.setLeaf', 'chat.rewind', 'chat.history', 'chat.compact', 'session.resume', 'session.list']) {
    assert.ok(block.includes(`"${method}"`), `HEAVY_METHODS 应包含 ${method}`);
  }
  assert.ok(typeof WsConnection === 'function');
});
