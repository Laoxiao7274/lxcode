// C 批修复钉子：远程/切走再回来时助手会话的「进行中进度」可见性。
//
// 诊断结论（详见派发卡）：后端 assistant.history 对生成中的会话返回内存快照
//（busy=true + 落库部分），delta 广播不分端——缺口在前端：本端曾装载
//（historyReady=true）时 boot 秒回闸挡住重拉，重连过的客户端拿不到「此刻在跑」
// 的事实。修复 = AssistantPage 的 busy 重拉闸：本地 idle + 列表 running →
// 强制重拉一次历史（reduce 冷恢复重建分支带 busySnapshot + reviveRunningDispatch）。
//
// 本文件钉两条源码接缝 + 一条数据面（reduce 已有行为，钉住助手域路径命中）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { handleAgentEvent } from '../src/shared/store.ts';

const here = new URL('.', import.meta.url);
const read = (p) => readFileSync(new URL(p, here), 'utf8');

const asstPageSrc = read('../src/components/assistant/AssistantPage.tsx');
const reduceSrc = read('../src/shared/reduce.ts');

test('AssistantPage：busy 重拉闸接在 boot 重试之后（本地 idle + 列表 running → 重拉）', () => {
  // 判定四要素齐全（对账口径）
  assert.match(asstPageSrc, /Boolean\(sessionId\) && running && Boolean\(st\?\.historyReady\) && !st\?\.busy/,
    '只有「已装载 + 本地 idle + 列表说在跑」才重拉');
  // 事实翻转才拉（防抖——不随列表刷新反复打后端）
  assert.match(asstPageSrc, /mismatch === busyMismatchRef\.current/);
  // 重拉走 assistant.history（域内寻址，事件带 space 标记进助手域 state）
  assert.match(asstPageSrc, /a\.history\(sessionId\)/);
});

test('reduce：busy 快照的冷恢复重建带 busy 投影（远程端重拉后「正在跑」可见）', () => {
  // busySnapshot 以服务端事实置真 + reviveRunningDispatch 回填运行中派发卡
  assert.match(reduceSrc, /const busySnapshot = ev\.history\.busy;/);
  assert.match(reduceSrc, /busy: busySnapshot \|\| ev\.history\.busy \|\| live\.length > 0/);
  assert.match(reduceSrc, /reviveRunningDispatch\(filled, state\.blocks, busySnapshot/);
});

test('数据面：助手域 busy 快照装载 → 后续实时 delta 归约进同一份 state（流式续上）', () => {
  function makeController() {
    return {
      currentId: '',
      sessionStates: {},
      assistantStates: {},
      childLinks: { childParents: {}, dispatchChild: {} },
      sendQueues: {},
      prevBusy: {},
      revisions: 0,
      getCurrentId() { return this.currentId; },
      getSessionStates() { return this.sessionStates; },
      getAssistantStates() { return this.assistantStates; },
      getChildLinks() { return this.childLinks; },
      getSendQueues() { return this.sendQueues; },
      getPrevBusy() { return this.prevBusy; },
      setCurrentId() {},
      setSessionStates() {},
      setAssistantStates(next) { this.assistantStates = next; },
      setOperationError() {},
      bumpRevision() { this.revisions += 1; },
      setChildLinks(next) { this.childLinks = next; },
      setSendQueues() {},
      setPrevBusy() {},
      autoSend() {},
      sendFailed() {},
      reset() {},
    };
  }
  const c = makeController();
  // ① 重拉回来的助手域快照（space 标记 = AssistantDomain.emitHistoryLoaded 的
  //    事件形状）：busy=true + 落库历史（流式中这一轮不在——整轮结束才 append）。
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'asst-1', space: 'assistant',
    history: {
      sessionId: 'asst-1', busy: true, blocks: [],
      messages: [{ role: 'user', content: '帮我整理目录', seq: 1 }, { role: 'assistant', content: '上一个结论', seq: 2 }],
      todos: [], historyReady: true, currentId: 'asst-1', operationError: null, context: null,
      runningDispatches: [],
    },
  }, c);
  const st = c.assistantStates['asst-1'];
  assert.ok(st, '快照归约进助手域 state');
  assert.equal(st.busy, true, 'busy 投影来自快照（正在跑可见）');
  // ② 之后实时 delta 到达：开流式块续写（进度可见）。
  handleAgentEvent({ type: 'delta', sessionId: 'asst-1', kind: 'text', text: '正在处理中…', space: 'assistant' }, c);
  const st2 = c.assistantStates['asst-1'];
  assert.ok(st2.streaming, 'delta 开流式块');
  assert.match(st2.streaming.content, /正在处理中/);
  assert.equal(st2.busy, true);
});
