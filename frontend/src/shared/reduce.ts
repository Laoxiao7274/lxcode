// 事件流 → UI 状态的归约（纯函数，无 React——可直接单测）。

import type { AgentEvent, ConfirmRequest, JobInfo } from "./types";
import { type AssistantBlock, type ThreadBlock, type UIState, DISPATCH_TOOL_NAME, initial, nextUid, withBlock, placeConfirm, planRewind } from "./blocks";
import { reduceHistory } from "./history";
import { noticeBody, noticeLabel } from "./notices";

/** 单个事件类型的窄化类型（reduce 的每个分支提取成函数后，参数类型要收窄到那一个变体）。 */
type Ev<T extends AgentEvent["type"]> = Extract<AgentEvent, { type: T }>;

export function reduce(state: UIState, ev: AgentEvent): UIState {
  switch (ev.type) {
    case "userMessage":
      return reduceUserMessage(state, ev);
    case "delta":
      return reduceDelta(state, ev);
    case "toolCall":
      return reduceToolCall(state, ev);
    case "toolResult":
      return reduceToolResult(state, ev);
    case "confirmRequest":
      return reduceConfirmRequest(state, ev);
    case "todoUpdated":
      // 任务清单不进对话流（用户拍板：只做输入框上方的计划条——
      // 消息流里再插一份是重复呈现；历史回放同样不重建清单卡）
      return { ...state, todos: ev.items };
    case "done":
      return reduceDone(state, ev);
    case "error":
      return reduceError(state, ev);
    case "dispatchStart":
      return reduceDispatchStart(state, ev);
    case "dispatchEnd":
      return reduceDispatchEnd(state, ev);
    case "busy":
      return { ...state, busy: ev.busy, pending: ev.busy ? state.pending : null };
    case "sessionsChanged":
      // 列表变化不改 UI 状态本身——新对象触发重渲染（侧栏重读 sessions()）
      return { ...state };
    case "projectsChanged":
      return { ...state };
    case "operationError":
      return { ...state, operationError: ev.message };
    case "filesChanged":
      // 一轮任务的产物汇总（验收视图——Codex 的 diff 中心形态）
      return { ...state, blocks: [...state.blocks, { kind: "files", uid: nextUid(), files: ev.files }] };
    case "compacted":
      return reduceCompacted(state, ev);
    case "jobStarted":
    case "jobSettled":
      return upsertJobBlock(state, ev.job);
    case "rewound":
      return reduceRewound(state, ev);
    case "historyLoaded":
      return reduceHistoryLoaded(state, ev);
    default:
      return state;
  }
}

/** userMessage 事件的处理（从 reduce 的 switch 里提出来——提示条识别要读前缀）。
 *
 * 系统提示条（后台任务通告 / 重复调用提醒）在**历史里与实时流里都是真实 user 角色
 * 消息**（模型必须把它当用户回合才能回应），但它不是用户说的话——按**文本前缀**识别
 *（不能按角色，种类表见 shared/notices.ts），渲染成提示条而不是用户气泡，否则用户
 * 会以为是自己发的。 */
function reduceUserMessage(state: UIState, ev: Ev<"userMessage">): UIState {
  const label = noticeLabel(ev.text);
  // seq 只在真的给了时才写进块：`seq: undefined` 会让既有断言多出一个键，
  // 而 canRewind 判的是 typeof === "number"，两种写法行为完全一致。
  const block: ThreadBlock = label
    ? { kind: "notice", uid: nextUid(), label, text: noticeBody(ev.text) }
    : typeof ev.seq === "number"
      ? { kind: "user", uid: nextUid(), text: ev.text, seq: ev.seq }
      : { kind: "user", uid: nextUid(), text: ev.text };
  return { ...state, blocks: [...state.blocks, block] };
}

/** 会话回退（chat.rewound）：时间线截断到锚点之前（锚点自己也消失）。
 *
 *  **与本地乐观截断共用 planRewind 一份判定**——两条路径分头实现的话，
 *  "乐观删的"与"广播删的"迟早不一致。
 *
 *  **幂等**：乐观路径已经先把锚点删了，后端广播随后到达时找不到锚点 → 原样返回
 *  （连对象都不换——没有变化就不该触发重渲染）。
 *
 *  **context 置 null**：删掉历史之后旧占用一定是错的，而 chat.rewound 不带重算
 *  后的占用（协议只有 session_id/seq/removed）。编一个数不如显示中性态「—」，
 *  下一轮 chat.done 会把真值带回来。 */
function reduceRewound(state: UIState, ev: Ev<"rewound">): UIState {
  const plan = planRewind(state.blocks, ev.seq);
  if (!plan) return state;
  return { ...state, blocks: plan.blocks, context: ev.context ?? null, stats: ev.stats ?? null };
}

/** delta 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDelta(state: UIState, ev: Ev<"delta">): UIState {
// dispatch 归属：子执行的事件路由进 dispatch 块（子上下文隔离）
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub) => reduceSub(sub, ev));
// reasoning/text 增量写进最近的 assistant 块（没有则开一块）。
// 不可突变旧块对象——每条 delta 都以新对象替换，保证引用变化。
// 开新块 = 前一块已定格：中间轮的 usage 一并清（tokens 只在整轮
// 的最终 assistant 显示——工具行上方的「已完成 · N tokens」是
// 错位的中间轮统计，DSH 的 stats 在轮末）。
const blocks = state.blocks.slice();
const last = blocks[blocks.length - 1];
let target: AssistantBlock;
if (last && last.kind === "assistant" && last.streaming) {
  target = { ...last };
  blocks[blocks.length - 1] = target;
} else {
  target = { kind: "assistant", uid: nextUid(), content: "", reasoning: "", streaming: true };
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i];
    if (b.kind === "assistant" && b.usageTokens !== undefined) {
      blocks[i] = { ...b, usageTokens: undefined };
    }
  }
  blocks.push(target);
}
if (ev.kind === "text") target.content += ev.text;
else target.reasoning += ev.text;
return { ...state, blocks };
}

/** toolCall 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceToolCall(state: UIState, ev: Ev<"toolCall">): UIState {
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub) => reduceSub(sub, ev));
// 工具调用打断流式中的 assistant 块——立刻定格（否则它永远挂着
// "思考中" shimmer，成为僵尸块；中间插工具/清单后再开新块续写）
const blocks = state.blocks.slice();
for (let i = 0; i < blocks.length; i++) {
  const b = blocks[i];
  if (b.kind === "assistant" && b.streaming) blocks[i] = { ...b, streaming: false };
}
// 调度工具不建工具行：它的渲染形态就是 dispatch 卡
//（随后 dispatchStart 挂卡）。两处都建 = 同一个调度渲染两遍——
// 外面一个工具行、卡里一份执行过程（用户报的「重复」）。
// 判定用 blocks.ts 的**唯一字面量**：回放路径（history.ts）必须与这里一致，
// 否则刷新之后同一张卡换一张脸（见 DISPATCH_TOOL_NAME 的注释）。
if (ev.name === DISPATCH_TOOL_NAME) return { ...state, blocks };
return {
  ...state,
  blocks: [...blocks, { kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments }],
};
}

/** toolResult 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceToolResult(state: UIState, ev: Ev<"toolResult">): UIState {
// 结果按 id 回填。两级查找：工具行 → **未裁决的确认卡**。
// 后者不能少：App.tsx 是「confirm 成功后」才把卡定格成工具行（避免确认失败
// 却已定格），而后端的 ack 响应与工具执行是并发的——工具跑得快时结果先到，
// 此时还没有工具行，只找工具行就会把结果静默丢弃，卡随后转成工具行却永远
// 停在「执行中…」（用户报的僵尸行；demo 同步 emit 结果必然触发，真实后端
// 也非绝对有序）。结果到达即证明该工具已有结论（放行执行 / 用户拒绝 /
// 只读拒绝都会回填结果），就地转工具行带上结果是唯一不会丢的处置。
const route = (blocks: ThreadBlock[]) => {
  let idx = blocks.findIndex((b) => b.kind === "tool" && b.id === ev.id);
  if (idx < 0) {
    // 已裁决为 deny 的卡不动：它的结论是「已跳过」，不被后到的拒绝结果覆盖
    idx = blocks.findIndex((b) => b.kind === "confirm" && b.request.id === ev.id && !b.resolved);
  }
  if (idx < 0) return null;
  const next = blocks.slice();
  const b = next[idx];
  if (b.kind === "tool") {
    next[idx] = { ...b, result: ev.content, isError: ev.isError };
  } else if (b.kind === "confirm") {
    next[idx] = {
      kind: "tool", uid: b.uid, id: b.request.id,
      name: b.request.name, arguments: b.request.arguments,
      result: ev.content, isError: ev.isError,
    };
  } else {
    return null; // 两级查找只认这两种，其余不可达
  }
  return next;
};
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub) => route(sub) ?? sub);
const next = route(state.blocks);
return next ? { ...state, blocks: next } : state;
}

/** confirmRequest 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceConfirmRequest(state: UIState, ev: Ev<"confirmRequest">): UIState {
// 子 Agent 的确认归属进 dispatch 卡（与它的工具行同层）。卡不在
//（刷新后丢卡、或事件早于 dispatchStart）则回落主时间线——宁可在
// 主时间线可见可批准，也不能静默丢弃：pending 被设置却没有任何
// 组件渲染它 = 会话卡在忙态且无法裁决（死锁）。
const did = ev.request.dispatch_id;
if (did && hasDispatch(state, did)) {
  const next = applyToDispatch(state, did, (sub) => placeConfirm(sub, ev.request));
  return { ...next, pending: ev.request };
}
return { ...state, pending: ev.request, blocks: placeConfirm(state.blocks, ev.request) };
}

/** done 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDone(state: UIState, ev: Ev<"done">): UIState {
// dispatch 归属：子轮的 done 定格子时间线里最后一个 assistant
if (ev.dispatchId) {
  return applyToDispatch(state, ev.dispatchId, (sub) => {
    for (let i = sub.length - 1; i >= 0; i--) {
      const b = sub[i];
      if (b.kind === "assistant") {
        const next = sub.slice();
        next[i] = { ...b, streaming: false, usageTokens: ev.usageTokens, firstTokenMs: ev.firstTokenMs, durationMs: ev.durationMs, model: ev.model };
        return next;
      }
    }
    return sub;
  });
}
// 定格最后一个 assistant 块（按 uid 定位，不按 index——
// 工具/清单块可能插在 assistant 之后）
// 上下文占用随主轮更新：与块定格解耦（没有 assistant 块的轮次也要
// 更新指示器，否则它会停在旧值上骗人）
// 整段统计同样随主轮更新，且**缺席时保持旧值**：后端读不到库（纯内存模式）
// 或还没有任何一步时不带 stats，那不代表"统计清零了"——把它当成 null 会把
// 用户已经看到的数字擦掉（读不到 ≠ 没有）。
const withCtx = ev.context ? { ...state, context: ev.context } : state;
const withStats = ev.stats ? { ...withCtx, stats: ev.stats } : withCtx;
let lastA: AssistantBlock | undefined;
for (let i = withStats.blocks.length - 1; i >= 0; i--) {
  const b = withStats.blocks[i];
  if (b.kind === "assistant") { lastA = b; break; }
}
if (!lastA) return withStats;
return withBlock(withStats, lastA.uid, (b) => {
  const a = b as AssistantBlock;
  return { ...a, streaming: false, usageTokens: ev.usageTokens, firstTokenMs: ev.firstTokenMs, durationMs: ev.durationMs, model: ev.model };
});
}

/** error 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceError(state: UIState, ev: Ev<"error">): UIState {
// 中断保留已生成部分：把进行中的 assistant 块定格
const blocks = state.blocks.slice();
for (let i = 0; i < blocks.length; i++) {
  const b = blocks[i];
  if (b.kind === "assistant" && b.streaming) blocks[i] = { ...b, streaming: false };
}
return {
  ...state,
  blocks: [...blocks, { kind: "error", uid: nextUid(), message: ev.message, aborted: ev.aborted }],
};
}

/** dispatchStart 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDispatchStart(state: UIState, ev: Ev<"dispatchStart">): UIState {
// 派发打断流式中的 assistant 块——立刻定格（否则主 Agent 那句
// 「我派 X 去做」后面永远挂着闪烁光标，直到整轮结束）
const blocks = state.blocks.slice();
for (let i = 0; i < blocks.length; i++) {
  const b = blocks[i];
  if (b.kind === "assistant" && b.streaming) blocks[i] = { ...b, streaming: false };
}
return {
  ...state,
  blocks: [...blocks, {
    kind: "dispatch", uid: nextUid(), id: ev.dispatchId, sessionId: ev.childSessionId,
    agentId: ev.agentId, agentName: ev.agentName, agentColor: ev.agentColor,
    task: ev.task, status: "running", subBlocks: [],
  }],
};
}

/** dispatchEnd 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDispatchEnd(state: UIState, ev: Ev<"dispatchEnd">): UIState {
// 子 Agent 最终回复作为结果回填（卡定格——结果在卡内展示，
// 主会话后续由主 Agent 继续汇总会话）
const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === ev.dispatchId);
if (idx < 0) return state;
const blocks = state.blocks.slice();
const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
blocks[idx] = {
  ...b, status: "done", result: ev.result, isError: ev.isError,
  usageTokens: ev.usageTokens, sessionId: ev.childSessionId ?? b.sessionId,
};
return { ...state, blocks };
}

/** compacted 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceCompacted(state: UIState, ev: Ev<"compacted">): UIState {
const block = {
  kind: "compacted" as const, uid: nextUid(), before: ev.before, after: ev.after,
  shadowed: ev.shadowed, summary: ev.summary, manual: Boolean(ev.manual),
};
// 子会话自己的压缩：归属进 dispatch 卡（主时间线只标主会话的压缩）
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub) => [...sub, block]);
// 历史被压缩：插一条标记块（摘要可展开查看）。压缩不改动已有块——
// 被压缩的那些消息本来就不在 UI 里（它们是更早的轮次，早已滚出视图）
return { ...state, blocks: [...state.blocks, block] };
}

/** historyLoaded 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceHistoryLoaded(state: UIState, ev: Ev<"historyLoaded">): UIState {
// 当前会话仍在生成且此前已完成过历史回放时，后端快照只含已追加消息；
// 全量重建会抹掉本地实时流。冷恢复/首次载入则必须重建确认卡等历史状态。
if (ev.history.busy && state.busy && state.historyReady) {
  return {
    ...state,
    pending: ev.history.pending ?? state.pending,
    todos: ev.history.todos ?? state.todos,
    context: ev.history.context ?? state.context,
    // 统计同样保持旧值：忙碌中的快照不带 stats 时（后端读不到库）不该把
    // 已经显示的数字擦掉——读不到 ≠ 没有
    stats: ev.history.stats ?? state.stats,
  };
}
// 冷恢复/空闲会话：messages → blocks（工具调用与结果配对）。
// **任务卡不在历史里**（契约只把后台任务定义成 job.started/settled 事件，
// ChatHistoryResult 不带 jobs）——整块重建会把它们一起丢掉：切走再切回一个正在
// 跑 dev server 的会话，卡片不该凭空消失（它还在跑，顶栏面板里也还看得见）。
// 保留的是本会话状态里的卡（state 本就按会话隔离），位置接在重建后的历史之后。
const jobs = state.blocks.filter((b) => b.kind === "job");
const rebuilt = reduceHistory(state, ev.history);
return {
  ...rebuilt,
  busy: ev.history.busy,
  blocks: jobs.length ? [...rebuilt.blocks, ...jobs] : rebuilt.blocks,
};
}

/** jobStarted / jobSettled 共用：同一个任务只留一张卡（就地更新快照）。
 *
 * 两个事件都 upsert 而不是「started 建卡、settled 改卡」：刷新/重连后
 * 只剩 settled 可收（后端不重放历史事件），那时若没有卡，用户就永远看不到
 * 这个任务发生过——补一张已结束的卡比静默丢弃诚实。
 * 块对象必须换新引用（Block 是 memo 的，原地改 job 字段不会触发重渲染）。 */
function upsertJobBlock(state: UIState, job: JobInfo): UIState {
  const idx = state.blocks.findIndex((b) => b.kind === "job" && b.job.id === job.id);
  if (idx < 0) {
    return { ...state, blocks: [...state.blocks, { kind: "job", uid: nextUid(), job }] };
  }
  const blocks = state.blocks.slice();
  const prev = blocks[idx] as Extract<ThreadBlock, { kind: "job" }>;
  blocks[idx] = { ...prev, job };
  return { ...state, blocks };
}

/** 把子事件应用进 dispatch 块的 subBlocks（子上下文隔离的归属路由）。
 *  fn 拿到当前 subBlocks 返回新的；dispatch 块不存在时原样返回。 */
function applyToDispatch(state: UIState, dispatchId: string, fn: (subBlocks: ThreadBlock[]) => ThreadBlock[] | null): UIState {
  const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === dispatchId);
  if (idx < 0) return state;
  const blocks = state.blocks.slice();
  const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
  const next = fn(b.subBlocks);
  blocks[idx] = next ? { ...b, subBlocks: next } : b;
  return { ...state, blocks };
}

/** dispatch 卡是否已在时间线里（确认卡归属的前置检查——卡不在就回落，
 *  否则 applyToDispatch 静默丢弃该确认）。 */
function hasDispatch(state: UIState, dispatchId: string): boolean {
  return state.blocks.some((b) => b.kind === "dispatch" && b.id === dispatchId);
}

/** 子事件在子时间线上的归约（与主时间线同款语义，作用域是 subBlocks）。 */
function reduceSub(blocks: ThreadBlock[], ev: AgentEvent): ThreadBlock[] | null {
  switch (ev.type) {
    case "delta": {
      const next = blocks.slice();
      const last = next[next.length - 1];
      let target: AssistantBlock;
      if (last && last.kind === "assistant" && last.streaming) {
        target = { ...last };
        next[next.length - 1] = target;
      } else {
        target = { kind: "assistant", uid: nextUid(), content: "", reasoning: "", streaming: true };
        next.push(target);
      }
      if (ev.kind === "text") target.content += ev.text;
      else target.reasoning += ev.text;
      return next;
    }
    case "toolCall": {
      const next = blocks.slice();
      for (let i = 0; i < next.length; i++) {
        const b = next[i];
        if (b.kind === "assistant" && b.streaming) next[i] = { ...b, streaming: false };
      }
      next.push({ kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments });
      return next;
    }
    case "compacted":
      // 子会话自己的压缩：卡内插一条标记块（与主时间线同一个组件）
      return [...blocks, {
        kind: "compacted" as const, uid: nextUid(), before: ev.before, after: ev.after,
        shadowed: ev.shadowed, summary: ev.summary, manual: Boolean(ev.manual),
      }];
    default:
      return null;
  }
}

/** 将一条服务端事件只归约进它所属的 Session；导出供并发隔离测试直接验证。
 *
 *  **子事件要双投**（2026-09-30 用户拍板：子会话标签页也要看到实时流）：
 *  子 Agent 的事件按**父会话 id** 广播（后端由父会话的 emitter 发出，带 dispatch_id
 *  归属进卡），所以上面那一份进的是父会话的 dispatch 卡（摘要 + 状态）；
 *  而子会话**自己**那个 id 的 state 也要拿到同一份（标签页里看到的就是它），
 *  否则标签页只有一次性的历史快照——「点开之后他里面就没有接着思考」。
 *
 *  双投时把 dispatchId 清掉：对子会话自己的时间线来说，它就是一条普通事件
 *  （delta 续写 assistant、toolCall 建工具行、confirmRequest 挂待裁决、compacted
 *  插压缩标记）——带着 dispatchId 会被当成"进某张卡"而在子会话里找不到那张卡。
 *
 *  子会话自己的 state 还不存在（标签从没打开过）时不建：没有历史基线的话，
 *  光靠实时增量拼出来的时间线是半截的；打开标签时用历史重建（见 ChildSessionPage
 *  装载 + source.childHistory 发的 historyLoaded）。 */
export function reduceSessionStates(states: Record<string, UIState>, ev: AgentEvent): Record<string, UIState> {
  if (!("sessionId" in ev) || !ev.sessionId) return states;
  const id = ev.sessionId;
  const previous = states[id] ?? { ...initial, currentId: id };
  const next = { ...states, [id]: { ...reduce(previous, ev), currentId: id } };
  const did = eventDispatchId(ev);
  const childId = did ? dispatchChildSession(states, did) : "";
  if (did && childId && childId !== id && states[childId]) {
    const child = states[childId];
    next[childId] = { ...reduce(child, withoutDispatch(ev)), currentId: childId };
  }
  return next;
}

/** 事件归属的那个 dispatch id（子事件双投用的键）。
 *
 *  多数带归属的事件在**顶层** `dispatchId`（events.ts 的映射），而**确认请求是个
 *  例外**：它的归属在 `request.dispatch_id` 里（协议 ConfirmRequest.dispatch_id，
 *  与 toProtocolConfirm 一致）。漏了这一条，子 Agent 的确认卡就只落在父会话的卡里——
 *  子会话标签页里看不到待裁决的确认（用户没开标签时反倒只能去主会话批）。 */
function eventDispatchId(ev: AgentEvent): string {
  if ("dispatchId" in ev && ev.dispatchId) return ev.dispatchId;
  if (ev.type === "confirmRequest") return ev.request.dispatch_id ?? "";
  return "";
}

/** 去掉 dispatchId 的事件副本（双投进子会话自己的时间线时用）。
 *
 *  类型上要一次断言：AgentEvent 是联合类型，只有 delta/toolCall/toolResult/done/
 *  compacted 这几个变体声明了 dispatchId——`{ ...ev, dispatchId: undefined }` 在
 *  "本来就没有这个键"的变体上会被 TS 判为多余属性。断言是安全的：这里只删一个键，
 *  各分支的归约函数只读自己认识的字段（多余键一律忽略）。 */
function withoutDispatch(ev: AgentEvent): AgentEvent {
  const plain = { ...ev } as Record<string, unknown>;
  delete plain.dispatchId;
  return plain as unknown as AgentEvent;
}

/** dispatchId → 子会话 id（子事件双投用的归属键）。
 *
 *  子会话 id 记在**卡上**（实时 dispatchStart 的 childSessionId，或历史回放的
 *  工具结果里那行 `[子会话 id: …]`——见 history.ts），两处都会把它写进
 *  `block.sessionId`。所以这里扫所有会话的卡：命中就返回那个子会话 id，
 *  没见过这张卡（应用刚起、dispatchStart 早于连接）时返回空串（不猜）。 */
function dispatchChildSession(states: Record<string, UIState>, dispatchId: string): string {
  for (const st of Object.values(states)) {
    for (const b of st.blocks) {
      if (b.kind === "dispatch" && b.id === dispatchId) return b.sessionId ?? "";
    }
  }
  return "";
}

/** 收集一个会话 state 里**所有未裁决**的确认请求 id：挂起中的（pending）+
 *  时间线上的确认卡 + dispatch 卡内的确认卡。kind==="confirm" 的块只存在于
 *  未裁决态（裁决后就地转工具行或定格 outcome），所以块类型本身就是裁决位。
 *
 *  用途：切「完全访问」时后端沿确认通道一次性放行挂起确认（含子会话的——
 *  确认门代理走父通道），UI 的待裁决卡片要同步定格，不等 toolResult 回执。
 *  导出供测试直接验证清单形状。 */
export function pendingConfirmIds(st: UIState): string[] {
  const ids: string[] = [];
  if (st.pending) ids.push(st.pending.id);
  for (const b of st.blocks) {
    if (b.kind === "confirm") ids.push(b.request.id);
    if (b.kind === "dispatch") {
      for (const s of b.subBlocks) {
        if (s.kind === "confirm") ids.push(s.request.id);
      }
    }
  }
  return [...new Set(ids)];
}

/** 切「完全访问」的一次性定格：把该会话 state 里**所有未裁决**的确认就地定格为
 *  「已允许」。后端在 SetApproval(auto) 时已沿确认通道放行挂起确认（含子会话的
 *  ——确认门代理走父通道），UI 在 approvalChanged(auto) 到达时调它同步定格，
 *  不等 toolResult 回执。state 不存在 = 无事发生（原样返回）。子会话 state 里
 *  那份双投的卡由 resolveConfirmEverywhere 一并定格。导出供测试直接验证。 */
export function allowAllPendingConfirms(
  states: Record<string, UIState>,
  sessionId: string,
): Record<string, UIState> {
  const st = states[sessionId];
  if (!st) return states;
  let next = states;
  for (const id of pendingConfirmIds(st)) {
    next = resolveConfirmEverywhere(next, id, "allow");
  }
  return next;
}

/** 裁决要**两处同时定格**：确认在父会话的卡里与子会话自己的时间线里各有一份
 *  （双投的必然结果——只定格一边，另一边会永远挂着「待确认」）。
 *
 *  只对**真的有这个确认**的会话动手：没有它的会话原样返回（对象都不换），
 *  否则每次裁决都会把所有会话的 pending 清掉（那是别人的挂起确认）。 */
export function resolveConfirmEverywhere(
  states: Record<string, UIState>,
  id: string,
  outcome: "allow" | "deny",
): Record<string, UIState> {
  const holds = (st: UIState): boolean =>
    st.pending?.id === id ||
    st.blocks.some((b) => (b.kind === "confirm" && b.request.id === id)
      || (b.kind === "dispatch" && b.subBlocks.some((s) => s.kind === "confirm" && s.request.id === id)));
  let next: Record<string, UIState> | null = null;
  for (const [sid, st] of Object.entries(states)) {
    if (!holds(st)) continue;
    next = next ?? { ...states };
    next[sid] = resolveConfirm(st, id, outcome);
  }
  return next ?? states;
}

/** 确认裁决后：允许 → 卡片就地变成工具行（后续 toolResult 填结果——
 *  与 DSH 同款：授权后看的是工具执行，不是审批表单）；拒绝 → 卡片
 *  定格为「已跳过」（没有工具执行可展示）。主时间线与 dispatch 卡内
 *  的确认都走这里（按 request.id 定位）。
 *  导出供测试直接驱动真实现——测试若复刻一份逻辑，断言的是副本，
 *  真实现漂移时不会红（假绿）。 */
export function resolveConfirm(state: UIState, id: string, outcome: "allow" | "deny"): UIState {
  const convert = (blocks: ThreadBlock[]): ThreadBlock[] | null => {
    const idx = blocks.findIndex((b) => b.kind === "confirm" && b.request.id === id);
    if (idx < 0) return null;
    const next = blocks.slice();
    const b = next[idx] as Extract<ThreadBlock, { kind: "confirm" }>;
    if (outcome === "allow") {
      // 就地转成工具行：uid 不变（React key 稳定——卡片不重挂），
      // id = 工具调用 id（toolResult 按它回填结果）
      next[idx] = {
        kind: "tool", uid: b.uid, id: b.request.id,
        name: b.request.name, arguments: b.request.arguments,
      };
    } else {
      next[idx] = { ...b, resolved: outcome };
    }
    return next;
  };
  // dispatch 卡内的确认（子 Agent）优先——按 id 遍历两级。
  // 两条分支都清 pending：裁决完就不该再有挂起确认（不清会留一个无人渲染的
  // 陈旧 pending，将来一旦有组件消费它就会显示过期的待确认态）。
  for (const b of state.blocks) {
    if (b.kind === "dispatch" && b.subBlocks.some((s) => s.kind === "confirm" && s.request.id === id)) {
      return { ...applyToDispatch(state, b.id, (sub) => convert(sub) ?? sub), pending: null };
    }
  }
  const next = convert(state.blocks);
  return next ? { ...state, pending: null, blocks: next } : { ...state, pending: null };
}
