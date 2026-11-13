// 助手页签（助手模式 P1）的域路由钉子：
//   ① workspace-tabs：assistant 是固定页签（isWorkspacePage / focus / close 语义与其余页签一致）；
//   ② 事件映射：assistant.* 十四事件归一成 chat 同构形状 + space 标记（方法名与
//      后端 assistantEventName 逐字对齐——读两边源码对照，键名漂移在这里爆）；
//   ③ store 域分流：assistant 事件归约进 assistantStates、不进主域 sessionStates；
//      dispatchStart 记 childLinks（run_task 子会话视图的归属反查）；
//   ④ 派发归属双投：带 dispatch_id 的子事件双投进助手域的子会话 state（同一份
//      reducer、独立 map）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  closeWorkspacePage,
  createWorkspaceTabs,
  focusWorkspacePage,
  isWorkspacePage,
} from '../src/shared/workspace-tabs.ts';
import {
  enterAssistantMode,
  enterProjectMode,
  readLastAssistantSession,
  resetLastAssistantSession,
  resetLastProjectView,
  writeLastAssistantSession,
} from '../src/shared/app-mode.ts';
import { mapEvent } from '../src/agent/ws/events.ts';
import { handleAgentEvent } from '../src/shared/store.ts';

const here = dirname(fileURLToPath(import.meta.url));

// ---------- ① workspace-tabs：assistant 已摘除页签（顶层模式） ----------

test('① assistant 不再是主域页签：isWorkspacePage 拒认（顶层模式 P2）', () => {
  assert.equal(isWorkspacePage('assistant'), false, 'assistant 已摘出 WorkspacePage');
  assert.equal(isWorkspacePage('child:s1'), false);
  assert.equal(isWorkspacePage('chat'), false);
  // 真正的入口（顶层模式）由 AppModeSwitch + AssistantWorkspace 承担，见 ①′
});

// ---------- ①′ 顶层模式：assistant 从页签升级为顶层模式 ----------

test("①′ 顶层模式：enterAssistantMode 记住项目视图、enterProjectMode 恢复", () => {
  resetLastProjectView();
  const enter = enterAssistantMode('git');
  assert.equal(enter.mode, 'assistant');
  assert.equal(enter.savedView, 'git', '离开时的项目视图被记住');
  const back = enterProjectMode();
  assert.equal(back.mode, 'project');
  assert.equal(back.restoredView, 'git', '切回项目模式恢复离开时的视图（不掉回 chat）');
  // 再次切换刷新记忆
  enterAssistantMode('chat');
  assert.equal(enterProjectMode().restoredView, 'chat');
});

// ---------- ①″ lastAssistantSession 记忆（2026-12 修复批）----------
// localStorage 在 node 单测里手工桩（theme.test.mjs 同款）——读写失败静默当
// 无记忆（隐私模式），语义由 AssistantWorkspace 的消费方钉住。

test("①″ lastAssistantSession：写入/读取/清除 + localStorage 失败静默", () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  };
  try {
    assert.equal(readLastAssistantSession(), '', '无记忆 = 空串（调用方按中性态处理）');
    writeLastAssistantSession('a1');
    assert.equal(readLastAssistantSession(), 'a1', '写后读到');
    assert.equal(store.get('lx.app.lastAssistantSession'), 'a1', '固定持久化键');
    writeLastAssistantSession('a2');
    assert.equal(readLastAssistantSession(), 'a2', '覆盖写');
    writeLastAssistantSession('');
    assert.equal(readLastAssistantSession(), '', '空 id = 清除记忆');
    assert.equal(store.has('lx.app.lastAssistantSession'), false, '清除走 removeItem');
    // localStorage 抛异常：静默当无记忆/不持久化（theme.ts 同款纪律）
    globalThis.localStorage = {
      getItem: () => { throw new Error('denied'); },
      setItem: () => { throw new Error('denied'); },
      removeItem: () => { throw new Error('denied'); },
    };
    assert.equal(readLastAssistantSession(), '', '读失败静默 = 无记忆');
    writeLastAssistantSession('a1'); // 不抛
  } finally {
    delete globalThis.localStorage;
  }
  resetLastAssistantSession(); // 测试口本身可调用（无 localStorage 也不抛）
});

// ---------- ② 事件映射：assistant.* → chat 同构 + space 标记 ----------

// 后端事件名常量表（internal/protocol/assistant.go）——与前端 ASSISTANT_TO_CHAT
// 的键逐字对齐（assistantEventName 的值 = 这里的键）。源码对照，改名两边一起改。
const goAssistantSrc = readFileSync(join(here, '../../internal/protocol/assistant.go'), 'utf8');
const goEmitSrc = readFileSync(join(here, '../../internal/server/emit.go'), 'utf8');
const eventsSrc = readFileSync(join(here, '../src/agent/ws/events.ts'), 'utf8');

test('② ASSISTANT_TO_CHAT 的键与后端 assistant.go 事件常量逐字对齐', () => {
  // 后端常量名 → 字符串字面量（assistant.go 的事件常量块）
  const goConstants = new Map();
  for (const m of goAssistantSrc.matchAll(/EventAssistant(\w+)\s+=\s+"(assistant\.[a-zA-Z]+)"/g)) {
    goConstants.set(m[2], m[1]);
  }
  assert.ok(goConstants.size >= 14, `assistant.go 应有 ≥14 个事件常量，实得 ${goConstants.size}`);
  // assistantEventName 的映射表：case protocol.EventXxx: return protocol.EventAssistantXxx
  const goMapping = new Map();
  for (const m of goEmitSrc.matchAll(/case protocol\.(Event\w+):\s*\n\s*return protocol\.(EventAssistant\w+)/g)) {
    goMapping.set(m[1], m[2]);
  }
  assert.ok(goMapping.size >= 14, `assistantEventName 应映射 ≥14 条，实得 ${goMapping.size}`);

  // 前端表：键 = assistant.* 字面量；值 = chat 域等价方法名
  const feTable = new Map();
  for (const m of eventsSrc.matchAll(/"(assistant\.[a-zA-Z]+)":\s*"(chat\.[a-zA-Z]+|todo\.updated|files\.changed)",/g)) {
    feTable.set(m[1], m[2]);
  }
  assert.equal(feTable.size, 16, `前端映射表应恰好 16 条（十六事件——2026-12 加 chat.compacting），实得 ${feTable.size}`);
  // 每条后端映射在两边都存在且键一致（wire 名漂移即爆）。主域事件常量
  //（EventUserMsg 等）声明在 methods.go——字面量查表放宽到两个源文件。
  const goMethodsSrc = readFileSync(join(here, '../../internal/protocol/methods.go'), 'utf8');
  const goAll = goAssistantSrc + goMethodsSrc;
  for (const [goFrom, goToConst] of goMapping) {
    const goToStr = goAll.match(new RegExp(`${goToConst}\\s+=\\s+"([a-zA-Z.]+)"`))?.[1];
    assert.ok(goToStr, `后端常量 ${goToConst} 缺字面量`);
    const feKey = goAll.match(new RegExp(`${goFrom}\\s+=\\s+"([a-zA-Z.]+)"`))?.[1];
    assert.ok(feKey, `主域事件 ${goFrom} 缺字面量`);
    assert.equal(feTable.get(goToStr), feKey, `assistant 域事件 ${goToStr} 的 chat 等价名应为主域 ${feKey}`);
  }
});

test('② mapEvent：assistant.* 事件映射成 chat 同构形状 + space 标记；未知方法返回 null', () => {
  const ev = mapEvent('assistant.delta', { session_id: 'a1', kind: 'text', text: '你好' });
  assert.equal(ev.type, 'delta');
  assert.equal(ev.sessionId, 'a1');
  assert.equal(ev.kind, 'text');
  assert.equal(ev.text, '你好');
  assert.equal(ev.space, 'assistant', 'space 标记要带上（store 的域分流依据）');

  const done = mapEvent('assistant.done', { session_id: 'a1', usage_tokens: 7 });
  assert.equal(done.type, 'done');
  assert.equal(done.usageTokens, 7);
  assert.equal(done.space, 'assistant');

  // dispatchStart 按 owner_session_id 归属（助手会话），子会话 id 另带
  const ds = mapEvent('assistant.dispatchStart', {
    owner_session_id: 'a1', session_id: 'c1', dispatch_id: 'd1',
    agent_id: 'coder', agent_name: '代码', agent_color: '#f00', task: '跑测试',
  });
  assert.equal(ds.sessionId, 'a1');
  assert.equal(ds.childSessionId, 'c1');
  assert.equal(ds.space, 'assistant');

  // 确认请求：payload 与 chat 同构（载荷 = ConfirmRequest 本体，session_id 是外层路由键）
  const cr = mapEvent('assistant.confirmRequest', { session_id: 'a1', id: 'x1', name: 'bash', arguments: '{}' });
  assert.equal(cr.type, 'confirmRequest');
  assert.equal(cr.request.id, 'x1');
  assert.equal(cr.space, 'assistant');

  // todo/files 域事件同样映射
  assert.equal(mapEvent('assistant.todoUpdated', { session_id: 'a1', items: [] }).type, 'todoUpdated');
  assert.equal(mapEvent('assistant.filesChanged', { session_id: 'a1', files: [] }).type, 'filesChanged');
  assert.equal(mapEvent('assistant.approvalChanged', { session_id: 'a1', approval: 'auto' }).type, 'approvalChanged');
  assert.equal(mapEvent('assistant.modelChanged', { session_id: 'a1', model_id: 'm2' }).type, 'modelChanged');

  // 未知 assistant.* 方法名不猜测归属（不产出前端事件）
  assert.equal(mapEvent('assistant.unknown', {}), null);
  // 主域方法不带 space 标记
  assert.equal(mapEvent('chat.delta', { session_id: 'm1', kind: 'text', text: 'x' }).space, undefined);
});

// ---------- ③④ store 域分流 ----------

const base = { blocks: [], streaming: null, busy: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null, stats: null };

function newController(states, assistantStates) {
  return {
    currentId: 's1',
    sessionStates: states,
    assistantStates: assistantStates ?? {},
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    revisions: 0,
    sent: [],
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getAssistantStates() { return this.assistantStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setAssistantStates(next) { this.assistantStates = next; },
    setOperationError() {},
    bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend(sessionId, text) { this.sent.push({ sessionId, text }); },
    sendFailed() {},
    reset() {},
  };
}

test('③ assistant 事件进 assistantStates、不进主域 sessionStates', () => {
  const c = newController({ s1: { ...base } });
  handleAgentEvent({ type: 'delta', sessionId: 'a1', kind: 'text', text: '你好', space: 'assistant' }, c);
  assert.ok(c.assistantStates['a1'], '助手事件应建助手域 state');
  // delta 进流式块（与主时间线同一条归约：streaming 恒为逻辑尾块）
  assert.equal(c.assistantStates['a1'].streaming?.content, '你好');
  assert.equal(c.sessionStates['a1'], undefined, '主域不得被助手事件污染');
  assert.equal(c.sessionStates['s1'].blocks.length, 0, '主会话时间线不受影响');
  assert.equal(c.sessionStates['s1'].streaming, null, '主会话流式块不受影响');
});

test('③ 助手会话的 busy 翻转与 done 收敛都在助手域内', () => {
  const c = newController({ s1: { ...base } });
  const a = (ev) => handleAgentEvent({ ...ev, space: 'assistant' }, c);
  a({ type: 'userMessage', sessionId: 'a1', text: '帮我看看', seq: 1 });
  a({ type: 'busy', sessionId: 'a1', busy: true });
  assert.equal(c.assistantStates['a1'].busy, true);
  a({ type: 'delta', sessionId: 'a1', kind: 'text', text: '回答' });
  a({ type: 'done', sessionId: 'a1', usageTokens: 7, finishReason: 'stop' });
  a({ type: 'busy', sessionId: 'a1', busy: false });
  assert.equal(c.assistantStates['a1'].busy, false, 'busy=false 要翻回');
  // done 定型流式块：user + assistant 两块
  assert.equal(c.assistantStates['a1'].blocks.filter((b) => b.kind === 'user').length, 1);
  assert.equal(c.assistantStates['a1'].blocks.filter((b) => b.kind === 'assistant').length, 1);
  // 主域自始至终没有 a1
  assert.equal(c.sessionStates['a1'], undefined);
});

test('④ 助手域 dispatchStart 记 childLinks；子事件双投进助手域子会话 state', () => {
  const c = newController({ s1: { ...base } });
  // 助手会话派发（run_task）
  handleAgentEvent({
    type: 'dispatchStart', sessionId: 'a1', dispatchId: 'd1', childSessionId: 'ac1',
    agentId: 'coder', agentName: '代码', agentColor: '#000', task: '跑测试', space: 'assistant',
  }, c);
  assert.equal(c.childLinks.childParents['ac1'], 'a1', '子→父映射要记（子会话视图反查 + 双投路由）');
  assert.equal(c.childLinks.dispatchChild['d1'], 'ac1', 'dispatch→子会话映射要记（子事件归属键）');

  // 子会话 historyLoaded（视图装载）先建基线 state（双投的「不建」规则：没有
  // 历史基线就不建——与主域子会话同一份契约）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'ac1',
    history: { sessionId: 'ac1', messages: [{ role: 'user', content: '跑测试', seq: 1 }], busy: false, pending: null, child: true },
    space: 'assistant',
  }, c);
  assert.ok(c.assistantStates['ac1'], '子会话 state 应在助手域建立');
  assert.ok(c.assistantStates['ac1'].blocks.some((b) => b.kind === 'task'), '助手子会话回放的 user 消息渲染成任务块');
  assert.equal(c.sessionStates['ac1'], undefined, '主域不得出现助手子会话');

  // 子会话的活动事件（带归属）：进父会话的卡 + 双投进助手域子会话自己的 state
  handleAgentEvent({
    type: 'toolCall', sessionId: 'a1', dispatchId: 'd1', id: 't1', name: 'bash', arguments: '{}', space: 'assistant',
  }, c);
  const parent = c.assistantStates['a1'];
  const card = parent.blocks.find((b) => b.kind === 'dispatch');
  assert.ok(card, '父（助手）时间线应有 dispatch 卡');
  assert.ok(card.subBlocks.some((b) => b.kind === 'tool'), '卡内应有工具行');
  assert.ok(c.assistantStates['ac1'].blocks.some((b) => b.kind === 'tool'), '子会话自己的时间线应吃到工具行（实时流）');
  assert.equal(c.assistantStates['ac1'].busy, true, '活动事件兜底置真（CHILD_BUSY_HINTS）');
});
