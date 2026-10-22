// Go/TS 协议一致性黄金测试（前端侧）。
//
// 读 internal/protocol/testdata/golden.json ——与 Go 侧测试
//（internal/protocol/golden_test.go）共用**同一份**样本文件。Go 侧负责「wire
// 字段必须被样本覆盖」（反射）与「golden 与内存样本逐字节一致」；本文件负责：
//
// ① 每个样本 JSON.parse → 规范化序列化 → 与原值深比较（往返稳定）；
// ② 样本 `_type` 标注的 TS 接口在 types.ts 里存在，且样本顶层键都被该接口
//    声明（或列入 ADAPTER_ONLY——适配层剥离/外移的键，逐键注明原因）；
// ③ 关键字段存在性/类型抽检（FIELD_CHECKS），含「缺席 vs 显式零值」语义：
//    context 整键缺席 = 未知、busy:false 是显式零值、data:"" 是无 omitempty
//    字段的显式空串。
//
// 设计目标：给 wire 类型加字段必须同时更新 golden（Go 侧反射覆盖检查强制）
// 与 types.ts（这里的键声明检查强制）——漏任何一侧都在测试阶段爆掉。
// 重新生成 golden：go test ./internal/protocol -run TestGolden -update

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const GOLDEN = new URL('../../internal/protocol/testdata/golden.json', import.meta.url);
const TYPES = new URL('../src/shared/types.ts', import.meta.url);

const golden = JSON.parse(readFileSync(GOLDEN, 'utf8'));
const typesSrc = readFileSync(TYPES, 'utf8');

// ---- types.ts 解析辅助 ----

/** 取 `export interface NAME { ... }` 的声明体（花括号配平；找不到返回 null）。 */
function interfaceBody(name) {
  const m = typesSrc.match(new RegExp(`export interface ${name}\\b`));
  if (!m) return null;
  const open = typesSrc.indexOf('{', m.index);
  let depth = 0;
  for (let i = open; i < typesSrc.length; i++) {
    if (typesSrc[i] === '{') depth++;
    else if (typesSrc[i] === '}') {
      depth--;
      if (depth === 0) return typesSrc.slice(open + 1, i);
    }
  }
  return null;
}

/** 解析接口体里的字段声明：键 → 是否可选（?）。忽略注释行。 */
function declaredKeys(body) {
  const keys = new Map();
  for (const rawLine of body.split('\n')) {
    const line = rawLine.replace(/\/\/.*$/, '');
    const m = line.match(/^\s*(\w+)\s*(\?)?\s*:/);
    if (m) keys.set(m[1], Boolean(m[2]));
  }
  return keys;
}

// 适配层做了转换的键：wire 上有、TS 接口里没有（或由外层携带）。逐键注明原因。
const ADAPTER_ONLY = {
  // SendOptions 只镜像 chat.send 的请求级参数；session_id/text 由 send() 顶层
  // 携带，approval 是后端持久化的会话级状态（协议侧保留解析，前端不再发送）。
  SendOptions: ['session_id', 'text', 'approval'],
  // 前端 confirmRequest 事件外层带 sessionId，适配层把 session_id 从请求里剥离。
  ConfirmRequest: ['session_id'],
  // busy_sessions 适配层转 camelCase busySessions（HelloResult.snapshot）。
  HelloResult: ['busy_sessions'],
};

// ---- 字段抽检表：样本名 → [点分路径, 期望] ----
// 期望：'string'|'number'|'boolean'|'array'|'object'（typeof/array.isArray 判定）、
// 'absent'（键必须不存在——缺席语义）、['=', 值]（精确相等）。
const FIELD_CHECKS = {
  // chat.send：最小样本的请求级参数整键缺席（不是零值）。
  chatSendParamsMinimal: [
    ['session_id', 'string'],
    ['text', 'string'],
    ['effort', 'absent'],
    ['approval', 'absent'],
    ['agent', 'absent'],
    ['images', 'absent'],
    ['files', 'absent'],
  ],
  chatSendParamsFull: [
    ['effort', ['=', 'high']],
    ['approval', ['=', 'auto']],
    ['images', 'array'],
    ['files', 'array'],
  ],
  // 图片两种传输形态：path 形态 data 是**显式空串**（无 omitempty），
  // base64 形态 path 整键缺席。
  chatSendImagePathForm: [
    ['data', ['=', '']],
    ['path', 'string'],
    ['mime', 'string'],
  ],
  chatSendFileBase64: [['path', 'absent'], ['data', 'string']],
  // chat.done：子会话轮没有 context/stats/计时/模型——整键缺席（不发 0 冒充）。
  chatDoneDispatch: [
    ['dispatch_id', 'string'],
    ['context', 'absent'],
    ['stats', 'absent'],
    ['first_token_ms', 'absent'],
    ['duration_ms', 'absent'],
    ['model', 'absent'],
    ['usage_tokens', 'number'],
  ],
  chatDoneFull: [
    ['context', 'object'],
    ['stats', 'object'],
    ['first_token_ms', ['=', 420]],
    ['model', 'string'],
    ['finish_reason', 'string'],
  ],
  // 工具结果事件带 dispatch_id 与 attachments（share_file）。
  toolResultWithAttachments: [
    ['dispatch_id', 'string'],
    ['attachments', 'array'],
    ['attachments.0.kind', ['=', 'image']],
    ['attachments.1.mime', 'absent'], // 文件附件不做 mime 白名单 → 缺席
  ],
  confirmRequestAsk: [
    ['kind', ['=', 'ask']],
    ['options', 'array'],
    ['dispatch_id', 'string'],
  ],
  todoItem: [['content', 'string'], ['status', ['=', 'done']]],
  todoUpdatedParams: [['items', 'array'], ['items.1.status', ['=', 'active']]],
  // 历史回放：全量 vs 冷态（未知 = 整键缺席；busy 是显式 false——无 omitempty）。
  chatHistoryResultFull: [
    ['checkpoints', 'array'],
    ['context', 'object'],
    ['stats', 'object'],
    ['approval', ['=', 'confirm']],
    ['pending', 'object'],
    ['model', 'string'],
    // 运行中派发快照（观察端回填卡的钥匙——dispatch_id + child_session_id）。
    ['running_dispatches', 'array'],
    ['running_dispatches.0.dispatch_id', 'string'],
    ['running_dispatches.0.child_session_id', 'string'],
    ['running_dispatches.0.agent_name', 'string'],
    ['running_dispatches.0.task', 'string'],
  ],
  chatHistoryResultMinimal: [
    ['busy', ['=', false]],
    ['context', 'absent'],
    ['stats', 'absent'],
    ['pending', 'absent'],
    ['todos', 'absent'],
    ['checkpoints', 'absent'],
    ['model', 'absent'],
    ['approval', 'absent'],
    ['session_id', 'absent'],
    // 运行中派发快照：空闲会话不导出（omitempty——整键缺席，不是空数组）。
    ['running_dispatches', 'absent'],
  ],
  // 上下文占用：估算位 vs 冷态（只有 used，其余整键缺席 = 中性态）。
  contextUsageEstimated: [
    ['estimated', ['=', true]],
    ['used', 'number'],
    ['window', 'number'],
    ['tool_results', 'number'],
  ],
  contextUsageUnknown: [
    ['used', 'number'],
    ['window', 'absent'],
    ['system', 'absent'],
    ['estimated', 'absent'],
  ],
  sessionStatsFull: [['legacy_tokens', 'number'], ['decode_tokens', 'number'], ['turns', 'number']],
  compactResult: [['compacted', ['=', false]], ['reason', 'string'], ['shadowed', 'number']],
  // 幂等空操作：removed 是**显式 0**（无 omitempty），不是缺席。
  // 带文件的那份（chatRewindResult）另见下方——它的 removed 是真实条数。
  chatRewindResultIdempotent: [['removed', ['=', 0]], ['leaf_seq', 'absent']],
  // 会话树（批次 2/3 的新契约）：节点字段的缺席语义 + 结果顶层键。
  // rewind 的结果在分支化之后多了 leaf_seq/branched/files（removed 口径不变）。
  chatRewindResult: [
    ['removed', 'number'],
    ['leaf_seq', 'number'],
    ['branched', ['=', true]],
    ['files.conflicts.0.reason', 'string'],
    ['files.child_sessions', 'array'],
  ],
  chatSetLeafResult: [['leaf_seq', 'number'], ['removed', 'number'], ['files.restored.0.path', 'string']],
  chatTreeResult: [
    ['leaf_seq', 'number'],
    ['total', 'number'],
    ['truncated', ['=', true]],
    ['nodes.0.parent_seq', ['=', 0]],
    ['nodes.1.files', 'number'],
    ['nodes.1.child_files', 'number'],
    ['nodes.1.external', 'number'],
    ['nodes.2.shadowed', 'number'],
    // 缺席语义：没有文件改动的节点不带 files（0 不落成假事实）
    ['nodes.0.files', 'absent'],
    ['nodes.2.files', 'absent'],
  ],
  chatLeafChanged: [
    ['leaf_seq', 'number'],
    ['prev_leaf_seq', 'number'],
    ['removed', 'number'],
    ['context', 'object'],
    ['files.child_sessions', 'array'],
  ],
  // 会话管理。
  sessionMeta: [['updated_at', 'string'], ['workspace', 'string'], ['archived', ['=', true]]],
  sessionArchiveResult: [['archived', ['=', true]]],
  // 目录域。
  agentEntry: [['tools', 'array'], ['is_main', ['=', true]], ['delegates', 'array']],
  mcServerEntry: [['status', ['=', 'connected']], ['tool_count', 'number'], ['stderr', 'string'], ['args', 'array']],
  mcServerEntrySse: [['status', ['=', 'error']], ['last_error', 'string'], ['url', 'string'], ['command', 'absent']],
  toolEntry: [['params', 'array'], ['risk', ['=', 'low']], ['command', 'string']],
  modelListResult: [['roles', 'object'], ['models.0.capabilities', 'object'], ['models.0.context_window', 'number']],
  // git 域：主检出分支的会话字段整键缺席。
  gitOverviewResult: [['branches', 'array'], ['dirty.0.kind', ['=', 'modified']]],
  gitBranchInfoMain: [
    ['current', ['=', true]],
    ['session_id', 'absent'],
    ['session_title', 'absent'],
    ['has_worktree', 'absent'],
    ['dirty_count', 'absent'],
    ['merged', 'absent'],
  ],
  // settings 域。
  searchChannelsResult: [['channels', 'array'], ['primary', 'string'], ['ready', ['=', true]], ['channels.2.opt_in', ['=', true]]],
  searchTestResult: [['elapsed_ms', 'number'], ['results', 'array']],
  todoPromptResult: [['todoPrompt', 'string']],
  // jobs 域。
  jobInfo: [['ended_by', ['=', 'user']], ['finished_at', 'string'], ['output_tail', 'string']],
  jobLogResult: [['truncated', ['=', true]], ['data', 'string']],
  // 派发。
  dispatchStartParams: [['owner_session_id', 'string'], ['session_id', 'string'], ['dispatch_id', 'string']],
  projectInstructionsResult: [['exists', ['=', true]], ['note', 'string']],
};

// ---- 工具 ----

/** 规范化序列化：键按字典序深排序（JS 对象键序保持插入序，需显式排序）。 */
function stableStringify(v) {
  if (Array.isArray(v)) return `[${v.map(stableStringify).join(',')}]`;
  if (v !== null && typeof v === 'object') {
    const keys = Object.keys(v).sort();
    return `{${keys.map((k) => `${JSON.stringify(k)}:${stableStringify(v[k])}`).join(',')}}`;
  }
  return JSON.stringify(v);
}

function get(obj, path) {
  return path.split('.').reduce((o, k) => (o === undefined || o === null ? undefined : o[k]), obj);
}

function checkExpectation(value, expect, label) {
  if (Array.isArray(expect) && expect[0] === '=') {
    assert.deepStrictEqual(value, expect[1], `${label}: 期望值 ${JSON.stringify(expect[1])}`);
    return;
  }
  switch (expect) {
    case 'absent':
      assert.ok(!(label.split('.').pop() in (value ?? {})), `${label}: 键应缺席（absent 语义）`);
      return;
    case 'array':
      assert.ok(Array.isArray(value), `${label}: 应为数组`);
      return;
    case 'object':
      assert.ok(value !== null && typeof value === 'object' && !Array.isArray(value), `${label}: 应为对象`);
      return;
    default:
      assert.ok(typeof value === expect, `${label}: 应为 ${expect}，实际 ${JSON.stringify(value)}`);
  }
}

// ---- 测试 ----

test('golden 文件存在且样本齐全（覆盖各域）', () => {
  const names = Object.keys(golden);
  assert.ok(names.length >= 30, `样本数应 >= 30，实际 ${names.length}`);
  // 每个域至少一个代表性样本（域 → 样本锚点）。
  const anchors = [
    'chatSendParamsFull', 'chatDoneFull', 'chatHistoryResultFull', 'confirmRequestAsk',
    'todoItem', 'agentEntry', 'modelListResult', 'gitOverviewResult', 'gitBranchInfoMain',
    'jobInfo', 'searchChannelsResult', 'todoPromptResult', 'dispatchStartParams',
    'contextUsageEstimated', 'sessionStatsFull',
  ];
  for (const a of anchors) assert.ok(a in golden, `缺少覆盖锚点样本 ${a}`);
  for (const [name, sample] of Object.entries(golden)) {
    assert.ok('value' in sample, `样本 ${name} 缺 value`);
    assert.ok('_type' in sample, `样本 ${name} 缺 _type 元数据`);
  }
});

test('每个样本 JSON.parse → 序列化往返稳定', () => {
  for (const [name, sample] of Object.entries(golden)) {
    const reparsed = JSON.parse(JSON.stringify(sample.value));
    assert.deepStrictEqual(reparsed, sample.value, `样本 ${name} 往返不一致`);
    assert.equal(stableStringify(reparsed), stableStringify(sample.value), `样本 ${name} 规范化序列化不稳定`);
  }
});

test('每个 _type 都在 types.ts 里有对应接口/类型', () => {
  for (const [name, sample] of Object.entries(golden)) {
    if (!sample._type) continue;
    const found = interfaceBody(sample._type) !== null || typesSrc.includes(`export type ${sample._type} `) || typesSrc.includes(`export type ${sample._type} =`);
    assert.ok(found, `样本 ${name} 的 _type "${sample._type}" 在 types.ts 里找不到对应声明`);
  }
});

test('样本顶层键都被对应 TS 接口声明（适配层豁免键除外）', () => {
  for (const [name, sample] of Object.entries(golden)) {
    if (!sample._type) continue;
    const body = interfaceBody(sample._type);
    if (body === null) continue; // 联合类型（AgentEvent）无法逐键解析——由抽检表兜底
    const declared = declaredKeys(body);
    const allowed = new Set([...declared.keys(), ...(ADAPTER_ONLY[sample._type] ?? [])]);
    for (const key of Object.keys(sample.value)) {
      assert.ok(allowed.has(key), `样本 ${name} 的顶层键 "${key}" 未在 types.ts 的 ${sample._type} 里声明`);
    }
    // 反向：接口里声明为必填（非 ?）的键必须在样本中出现。
    for (const [key, optional] of declared) {
      if (!optional) {
        assert.ok(key in sample.value, `${sample._type} 的必填键 "${key}" 不在样本 ${name} 里`);
      }
    }
  }
});

test('关键字段抽检（存在性/类型/缺席 vs 显式零值）', () => {
  for (const [name, checks] of Object.entries(FIELD_CHECKS)) {
    const sample = golden[name];
    assert.ok(sample, `FIELD_CHECKS 引用了不存在的样本 ${name}`);
    for (const [path, expect] of checks) {
      const value = get(sample.value, path);
      if (expect === 'absent') {
        const parts = path.split('.');
        const parent = get(sample.value, parts.slice(0, -1).join('.'));
        const key = parts[parts.length - 1];
        assert.ok(parent === undefined || parent === null || !(key in parent), `${name}.${path}: 键应缺席（absent 语义）`);
      } else {
        assert.ok(value !== undefined, `${name}.${path}: 键应存在`);
        checkExpectation(value, expect, `${name}.${path}`);
      }
    }
  }
});
