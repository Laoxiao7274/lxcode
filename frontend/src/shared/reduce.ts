// 事件流 → UI 状态的归约（纯函数，无 React——可直接单测）。

import type { AgentEvent, ConfirmRequest, JobInfo, RunningDispatchInfo } from "./types";
import { type AssistantBlock, type ThreadBlock, type UIState, DISPATCH_TOOL_NAME, initial, nextUid, withBlock, placeConfirm, planRewind, finalizeStreaming, timelineOf } from "./blocks";
import { DISPATCH_INTERRUPTED, mergeOlderHistory, reduceHistory } from "./history";
import { isHiddenNotice, noticeBody, noticeLabel } from "./notices";
import { attachmentsFromMessage, parseAttachmentLines } from "./attachments";

/** 单个事件类型的窄化类型（reduce 的每个分支提取成函数后，参数类型要收窄到那一个变体）。 */
type Ev<T extends AgentEvent["type"]> = Extract<AgentEvent, { type: T }>;

export function reduce(state: UIState, ev: AgentEvent): UIState {
  switch (ev.type) {
    case "userMessage":
      return reduceUserMessage(state, ev);
    case "optimisticUser":
      return reduceOptimisticUser(state, ev);
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
      // 消息流里再插一份是重复呈现；历史回放同样不重建清单卡）。
      // todo 契约批②：带 dispatch_id 的是子会话的清单更新——归约进主时间线
      // 派发卡的 todos 徽标（不碰本会话的计划条；清单归子会话自己）；子会话
      // 自己的 state 由双投路径吃到（withoutDispatch 后走本分支）。
      if (eventDispatchId(ev)) return applyDispatchTodos(state, ev);
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
      // 带归属的 busy = **子会话**的忙闲翻转：不碰主会话的 busy/sending
      //（子会话在跑不代表主会话在生成），也不该清主会话的 pending。
      // 子会话自己的 state 由双投路径（reduceSessionStates 的 withoutDispatch
      // 副本）正常走这条归约——那边没有归属键，语义与主会话一致。
      if (ev.dispatchId) return state;
      // busy=true = 生成已经开始：发送中态交接给生成态；busy=false 一并收尾
      //（防sending 卡死：done/error 之外的任何忙闲变化都意味着发送阶段已结束）。
      return { ...state, busy: ev.busy, sending: false, pending: ev.busy ? state.pending : null };
    case "sessionsChanged":
      // 列表变化不改 UI 状态本身——新对象触发重渲染（侧栏重读 sessions()）
      return { ...state };
    case "projectsChanged":
      return { ...state };
    case "operationError":
      return { ...state, operationError: ev.message };
    case "filesChanged": {
      // 一轮任务的产物汇总（验收视图——Codex 的 diff 中心形态）。
      // 追加前先定型流式块：不变量是「streaming 恒为逻辑上的最后一块」，
      // 否则新块会插在正在输出的正文之前。
      const sealed = finalizeStreaming(state);
      return { ...sealed, blocks: [...sealed.blocks, { kind: "files", uid: nextUid(), files: ev.files }] };
    }
    case "compacted":
      return reduceCompacted(state, ev);
    case "jobStarted":
    case "jobSettled":
      return upsertJobBlock(state, ev.job);
    case "rewound":
      return reduceRewound(state, ev);
    case "historyLoaded":
      return reduceHistoryLoaded(state, ev);
    case "modelChanged":
      // 会话**选中的模型**变了（chat.modelChanged / 演示态的本地广播）：与
      // approvalChanged 同一条会话级路由——reduceSessionStates 按 sessionId 分发，
      // 每个会话的 state 只收自己的模型变更（切会话时显示的是那条会话自己的模型）。
      return { ...state, model: ev.model };
    case "approvalChanged":
      // 权限档是**会话级状态**（后端落库，AGENTS.md §2 协议表）：事件带 sessionId，
      // reduceSessionStates 按 id 路由——每个会话的 state 只收自己的档位变更，
      // 主会话之间不串台。载荷档位已由 ws 层 normalizeApproval 归一。
      return { ...state, approval: ev.approval };
    default:
      return state;
  }
}

/** userMessage 事件的处理（从 reduce 的 switch 里提出来——提示条识别要读前缀）。
 *
 * 系统提示条（重复调用提醒）在**历史里与实时流里都是真实 user 角色消息**
 * （模型必须把它当用户回合才能回应），但它不是用户说的话——按**文本前缀**识别
 *（不能按角色，种类表见 shared/notices.ts），渲染成提示条而不是用户气泡，否则用户
 * 会以为是自己发的。后台任务通告是 hidden 种类：不产生任何块（任务状态在任务卡
 * 与 Jobs 面板，不在对话时间线）。
 *
 * **hidden 的系统注记（如「用户中断了这次生成」）不产生任何块**：它是写给模型的
 * 注记，用户界面上当它不存在——原样返回 state（连乐观 pending 的去重也不做，
 * 它本来就不是任何发送的回执）。与回放路径 history.ts 的同款判定共用一份前缀表。 */
function reduceUserMessage(state: UIState, ev: Ev<"userMessage">): UIState {
  // 子会话的消息（派发方注入的任务说明书，带 dispatch_id）：**主时间线不画**——
  // dispatch 卡 = 一行摘要（docs/child-session-ui.md §2.3），任务全文属于子会话
  // 自己的时间线（双投路径已把 dispatchId 剥掉、打上 task 标记后送到那里）。
  if (ev.dispatchId) return state;
  if (isHiddenNotice(ev.text)) return state;
  const label = noticeLabel(ev.text);
  // 乐观 pending 块的去重（FIFO 移除最旧的——连续发多条按发送顺序与回执一一对应）。
  // 只对**真正的用户消息**做：提示条（notice）虽然也是 userMessage 事件，但它不是
  // 本次发送的回执，动了会把还没确认的 pending 块错删掉。任务消息（task）同样不是
  // 发送回执（它是主 Agent 派发进子会话的）——也不动。没有 pending 时照常渲染
  //（不重复——removeOldestPending 找不到就原样返回）。
  const base = (label || ev.task) ? state : removeOldestPending(state);
  // seq 只在真的给了时才写进块：`seq: undefined` 会让既有断言多出一个键，
  // 而 canRewind 判的是 typeof === "number"，两种写法行为完全一致。
  // 附件归一化（结构化引用优先；「[附件]」行解析成卡并从正文剥离——正文与
  // 卡区各渲染一份，正文里再留一行「[附件] … → attachments/…」是噪音）。
  // 与回放路径 history.ts 共用同一份纯函数（attachmentsFromMessage），两条
  // 路径不许分叉。
  const atts = attachmentsFromMessage({ attachments: ev.attachments, content: ev.text });
  const displayText = atts ? parseAttachmentLines(ev.text).text : ev.text;
  const block: ThreadBlock = label
    ? { kind: "notice", uid: nextUid(), label, text: noticeBody(ev.text) }
    : ev.task
      ? // 任务说明书（子会话时间线）：渲染成「任务」块——用户气泡的样式会让用户
        // 以为是自己发的（用户实测报的问题 1 的实时路径）。回放路径（history.ts
        // 的 child 快照）给出同一个块种类，两条路径不许分叉。
        { kind: "task", uid: nextUid(), text: ev.text }
      : typeof ev.seq === "number"
        ? { kind: "user", uid: nextUid(), text: displayText, seq: ev.seq, ...(atts ? { attachments: atts } : {}) }
        : { kind: "user", uid: nextUid(), text: displayText, ...(atts ? { attachments: atts } : {}) };
  const sealed = finalizeStreaming(base);
  return { ...sealed, blocks: [...sealed.blocks, block] };
}

/** 乐观用户气泡（本地事件，不经后端——send 发起后立即给视觉反馈）。
 *
 *  真正的用户气泡要等后端 chat.userMessage 回执，而回执排在 worktree 准备等
 *  工作之后（首条消息会空白等待数百毫秒）——pending 块先把「发出去了」这件事画出来。
 *
 *  pending 块挂在**当前会话**的 blocks 上（与会话 state 同生命周期）：切走再切回，
 *  没等到回执的 pending 块还留在那个会话里——不全局悬浮、不串会话。
 *  failed = 发送失败（ws 的 catch 发出的失败变体）：移除最旧的 pending 块（与成功
 *  路径同一个 FIFO 口径）并结束发送中态，错误提示走既有 operationError 通道。 */
function reduceOptimisticUser(state: UIState, ev: Ev<"optimisticUser">): UIState {
  if (ev.failed) {
    return { ...removeOldestPending(state), sending: false };
  }
  // 附件只留计数（base64 已随请求交出去，块里不需要也不该留一份拷贝）。
  // 条件展开：无附件的块不出现 atts 键（与 seq 同款纪律——多余键会进测试断言）。
  const hasAtts = Boolean(ev.atts && (ev.atts.images.length > 0 || ev.atts.files.length > 0));
  // 追加前定型流式块（不变量：streaming 恒为逻辑上的最后一块）
  const sealed = finalizeStreaming(state);
  return {
    ...sealed,
    sending: true,
    blocks: [...sealed.blocks, {
      kind: "user", uid: nextUid(), text: ev.text, pending: true,
      ...(hasAtts ? { atts: { images: ev.atts!.images.length, files: ev.atts!.files.length } } : {}),
    }],
  };
}

/** 移除最旧的 pending 用户块（FIFO）；没有 pending 块时原样返回（对象都不换）。 */
function removeOldestPending(state: UIState): UIState {
  const idx = state.blocks.findIndex((b) => b.kind === "user" && b.pending);
  if (idx < 0) return state;
  const blocks = state.blocks.slice();
  blocks.splice(idx, 1);
  return { ...state, blocks };
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
  if (!plan) {
    // 锚点不在（本地乐观截断已经把它删了 / 别的客户端早撤过）= 幂等 no-op，
    // **但后端随真事件带来的重算值仍要消费**（2026-12）：chat.rewound 的
    // context/stats 是按新路径重算的，丢了它们指示器就停在过期数字上。
    // 只更新这两键（blocks 已是截断态，不能再切一刀）；事件不带（回声/老后端）
    // 才原样返回——同键不换对象，重复到达依旧不触发重渲染。
    if (ev.context === undefined && ev.stats === undefined) return state;
    return { ...state, context: ev.context ?? state.context, stats: ev.stats ?? state.stats };
  }
  // 流式块逻辑上在末尾：撤回锚点之前的截断一定把它一起丢掉（否则它会接在被
  // 截断的旧时间线后面继续流，用户看到「撤回之后还在写」）。
  //
  // **context/stats 保留旧值**（2026-12「撤回后指示器眨成 —」）：本地乐观回声
  // 不带这两键（它发在请求之前，什么都不知道），真事件（后端带重算值）到达时
  // 正常覆盖——中间隔的几百毫秒里沿用撤回前的数字，比闪一下「—」诚实（数字是
  // 略过期的，不是未知的；下一轮 chat.done 或 rewound 广播会校正它）。
  return {
    ...state,
    blocks: plan.blocks,
    streaming: null,
    context: ev.context ?? state.context,
    stats: ev.stats ?? state.stats,
  };
}

/** delta 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDelta(state: UIState, ev: Ev<"delta">): UIState {
// dispatch 归属：子执行的事件路由进 dispatch 块（子上下文隔离）
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub, subStream) => reduceSub(sub, subStream, ev));
// reasoning/text 增量写进**正在流式的那一块**（state.streaming；没有则开一块）。
//
// **不许整数组复制**（2026-12 渲染性能）：delta 是 token 级的，原先每帧
// `state.blocks.slice()` 把整条时间线复制一遍——2000 块的会话在流式期间每秒
// 几十次 O(块数) 复制。现在流式块单独放在 state.streaming 里，每帧只换这一个
// 对象（O(1)）；blocks 一个字节都不动，memo(Block) 的既有块引用因此完全稳定
// （切会话/工具行重渲染的收益都在这里）。不可突变旧块对象——每条 delta 都以
// 新对象替换，保证引用变化（AnswerBody 靠它重渲染）。
// 开新块 = 前一块已定格：中间轮的 usage 一并清（tokens 只在整轮
// 的最终 assistant 显示——工具行上方的「已完成 · N tokens」是
// 错位的中间轮统计，DSH 的 stats 在轮末）。
const cur = state.streaming;
if (cur) {
  const target: AssistantBlock = { ...cur };
  if (ev.kind === "text") target.content += ev.text;
  else target.reasoning += ev.text;
  return { ...state, streaming: target };
}
const target: AssistantBlock = { kind: "assistant", uid: nextUid(), content: "", reasoning: "", streaming: true };
if (ev.kind === "text") target.content += ev.text;
else target.reasoning += ev.text;
// 清中间轮 usage：只有真的存在这样的块才换数组（常见情形是空循环，零开销）
let blocks = state.blocks;
for (let i = 0; i < blocks.length; i++) {
  const b = blocks[i];
  if (b.kind === "assistant" && b.usageTokens !== undefined) {
    if (blocks === state.blocks) blocks = state.blocks.slice();
    blocks[i] = { ...b, usageTokens: undefined };
  }
}
return { ...state, blocks, streaming: target };
}

/** toolCall 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceToolCall(state: UIState, ev: Ev<"toolCall">): UIState {
if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub, subStream) => reduceSub(sub, subStream, ev));
// 工具调用打断流式中的 assistant 块——立刻定格（否则它永远挂着
// "思考中" shimmer，成为僵尸块；中间插工具/清单后再开新块续写）。
// 定型 = 把 state.streaming 并入 blocks（一次性 O(块数)，不是每帧）。
const sealed = finalizeStreaming(state);
// 调度工具不建工具行：它的渲染形态就是 dispatch 卡
//（随后 dispatchStart 挂卡）。两处都建 = 同一个调度渲染两遍——
// 外面一个工具行、卡里一份执行过程（用户报的「重复」）。
// 判定用 blocks.ts 的**唯一字面量**：回放路径（history.ts）必须与这里一致，
// 否则刷新之后同一张卡换一张脸（见 DISPATCH_TOOL_NAME 的注释）。
if (ev.name === DISPATCH_TOOL_NAME) return sealed;
return {
  ...sealed,
  blocks: [...sealed.blocks, { kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments }],
};
}

/** toolResult 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceToolResult(state: UIState, ev: Ev<"toolResult">): UIState {
// 调度结果（agent_dispatch）不建工具行（它的形态是 dispatch 卡）——但子会话
// 共享到对话的附件随这条结果回流（后端 DispatchResult.Attachments → 工具结果
// 事件同一份元数据）：挂到卡上，主时间线渲染附件条。卡 = 一行摘要的例外：
// 附件不是子过程，是**成果**（docs/child-session-ui.md §2.6）。没带附件的调度
// 结果照旧是 no-op（与旧行为逐字节一致）。
if (ev.name === DISPATCH_TOOL_NAME) {
  if (!ev.attachments?.length) return state;
  const didx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === ev.id);
  if (didx < 0) return state;
  const d = state.blocks[didx];
  if (d.kind !== "dispatch") return state; // 不可达（findIndex 已按 kind 过滤）——收窄用
  const dblocks = state.blocks.slice();
  dblocks[didx] = { ...d, attachments: ev.attachments };
  return { ...state, blocks: dblocks };
}
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
    next[idx] = { ...b, result: ev.content, isError: ev.isError,
      ...(ev.attachments?.length ? { attachments: ev.attachments } : {}) };
  } else if (b.kind === "confirm") {
    next[idx] = {
      kind: "tool", uid: b.uid, id: b.request.id,
      name: b.request.name, arguments: b.request.arguments,
      result: ev.content, isError: ev.isError,
      ...(ev.attachments?.length ? { attachments: ev.attachments } : {}),
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
const sealed = finalizeStreaming(state);
return { ...sealed, pending: ev.request, blocks: placeConfirm(sealed.blocks, ev.request) };
}

/** done 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDone(state: UIState, ev: Ev<"done">): UIState {
// dispatch 归属：子轮的 done 定格子时间线里最后一个 assistant
if (ev.dispatchId) {
  return applyToDispatch(state, ev.dispatchId, (sub, subStream) => {
    const patch = { usageTokens: ev.usageTokens, firstTokenMs: ev.firstTokenMs, durationMs: ev.durationMs, model: ev.model };
    // 正在流式的那一块就是本轮的最终 assistant：定型时带上用量/计时/模型
    if (subStream) return { blocks: [...sub, { ...subStream, ...patch, streaming: false }], streaming: null };
    for (let i = sub.length - 1; i >= 0; i--) {
      const b = sub[i];
      if (b.kind === "assistant") {
        const next = sub.slice();
        next[i] = { ...b, streaming: false, ...patch };
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
// 正在流式的那一块就是本轮的最终 assistant：定型时一并带上用量/计时/模型
// （O(1)——不用为了改它把整条时间线复制一遍）。
if (withStats.streaming) {
  return {
    ...finalizeStreaming(withStats, {
      usageTokens: ev.usageTokens, firstTokenMs: ev.firstTokenMs, durationMs: ev.durationMs, model: ev.model,
    }),
    sending: false,
  };
}
let lastA: AssistantBlock | undefined;
for (let i = withStats.blocks.length - 1; i >= 0; i--) {
  const b = withStats.blocks[i];
  if (b.kind === "assistant") { lastA = b; break; }
}
if (!lastA) return { ...withStats, sending: false };
return { ...withBlock(withStats, lastA.uid, (b) => {
  const a = b as AssistantBlock;
  return { ...a, streaming: false, usageTokens: ev.usageTokens, firstTokenMs: ev.firstTokenMs, durationMs: ev.durationMs, model: ev.model };
}), sending: false };
}

/** error 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceError(state: UIState, ev: Ev<"error">): UIState {
// 中断保留已生成部分：把进行中的 assistant 块定格（并入 blocks，一次 O(块数)）
const sealed = finalizeStreaming(state);
return {
  ...sealed,
  sending: false, // 轮已出错收尾：发送中态一并结束（错误条随后渲染）
  blocks: [...sealed.blocks, { kind: "error", uid: nextUid(), message: ev.message, aborted: ev.aborted }],
};
}

/** dispatchStart 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDispatchStart(state: UIState, ev: Ev<"dispatchStart">): UIState {
// 派发打断流式中的 assistant 块——立刻定格（否则主 Agent 那句
// 「我派 X 去做」后面永远挂着闪烁光标，直到整轮结束）。定型是一次 O(块数)
// 的数组合并（不在流式热路径上）。
const sealed = finalizeStreaming(state);
const blocks = sealed.blocks;
// **复用已有卡**（2026-12 修复「子 Agent 清单不显示」的另一半）：历史回放
// （history.ts）已经从 assistant 的 agent_dispatch 调用建过一张同 id 的派发卡
// （先按「中断」占位定格）。刷新的时序是「先 historyLoaded 建占位卡 → 子 Agent
// 继续跑 → dispatchStart 到」——无条件追加会**并排长两张同 id 的卡**：新的那张
// 收 todo.updated，而用户点进去/看到的是历史那张（占位 done，被迟到守卫丢弃）；
// 安卓端 parseHistoryBlocks + dispatchStart 的复用分支（RealBackend 的 idx >= 0）
// 早就是这么做的，桌面端漏了。
// 续跑（同一 dispatch_id 再派一次）也走这里：状态回到 running、旧结论清掉。
const idx = blocks.findIndex((b) => b.kind === "dispatch" && b.id === ev.dispatchId);
if (idx >= 0) {
  const prev = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
  blocks[idx] = {
    ...prev,
    sessionId: ev.childSessionId ?? prev.sessionId,
    // 身份字段以实时载荷为准，空值回落旧值（回放路径的 agentName 恒空）
    agentId: ev.agentId || prev.agentId,
    agentName: ev.agentName || prev.agentName,
    agentColor: ev.agentColor || prev.agentColor,
    task: ev.task || prev.task,
    status: "running",
    isError: undefined,
    result: undefined,
    usageTokens: undefined,
    // 清单：初稿非空才覆盖（nil = 没带初稿，别把回放/实时已有的清单抹掉）
    ...(ev.todos && ev.todos.length > 0 ? { todos: ev.todos } : {}),
  };
  return { ...sealed, blocks };
}
return {
  ...sealed,
  blocks: [...blocks, {
    kind: "dispatch", uid: nextUid(), id: ev.dispatchId, sessionId: ev.childSessionId,
    agentId: ev.agentId, agentName: ev.agentName, agentColor: ev.agentColor,
    task: ev.task, status: "running", subBlocks: [],
    // todo 契约批③：主 Agent 预拆的清单初稿（卡上第一秒显示计划；子会话修订后
    // 由带 dispatch_id 的 todo.updated 实时覆盖）。
    todos: ev.todos,
  }],
};
}

/** 带归属的 todo.updated → 更新主时间线派发卡上的清单徽标（实时进度）。
 *  卡不存在（历史回放外的边缘时序）就忽略——徽标是附属于卡的呈现，不是独立状态。 */
function applyDispatchTodos(state: UIState, ev: Ev<"todoUpdated">): UIState {
  const did = ev.dispatchId;
  if (!did) return state;
  const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === did);
  if (idx < 0) return state;
  const blocks = state.blocks.slice();
  const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
  // 迟到守卫：卡已**真定格**就不再回写——终态以 dispatchEnd 的 finalTodos 为准，
  // 迟到的旧快照（网络重排 / 历史回放外的边缘时序）不许把定格的终态覆盖回去。
  // 续跑（新一轮派发）会先到 dispatchStart 重开卡，那之后的更新照常生效。
  //
  // **回放占位态不是终态**：历史里没有配对结果的调度先按「中断」定格
  //（status=done + isError + DISPATCH_INTERRUPTED），那是没有证据时的诚实默认。
  // 子会话仍在跑时，todo.updated 恰恰是「它还活着」的第一手证据——按 done 一律
  // 丢弃会让清单进度永远停在初稿（用户报的「任务列表有时不显示」根因之一）。
  // 真终态（成功 / 真失败）仍受守卫保护。
  if (b.status === "done" && !(b.isError && b.result === DISPATCH_INTERRUPTED)) return state;
  blocks[idx] = { ...b, todos: ev.items };
  return { ...state, blocks };
}

/** dispatchEnd 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceDispatchEnd(state: UIState, ev: Ev<"dispatchEnd">): UIState {// 子 Agent 最终回复作为结果回填（卡定格——结果在卡内展示，
// 主会话后续由主 Agent 继续汇总会话）
const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === ev.dispatchId);
if (idx < 0) return state;
const blocks = state.blocks.slice();
const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
blocks[idx] = {
  ...b, status: "done", result: ev.result, isError: ev.isError,
  usageTokens: ev.usageTokens, sessionId: ev.childSessionId ?? b.sessionId,
  // todo 契约批③：派发结束定格终态清单（FinalTodos 非空才覆盖——运行中徽标
  // 由带 dispatch_id 的 todo.updated 实时喂；nil = 子会话没建过清单，不动）。
  todos: ev.finalTodos ?? b.todos,
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
// ——子会话的占用不写主指示器（chat.done 同一条纪律）。
if (ev.dispatchId) {
  const applied = applyToDispatch(state, ev.dispatchId, (sub) => [...sub, block]);
  return applied;
}
// 上下文占用随压缩更新（与 chat.rewound 同款：`ev.context ?? state.context`——
// 缺席 = 老后端没带，保持旧值不编数）。压缩改写了历史形状，旧数字一定是错的；
// 后端（CompactedParams.context）或随后的历史重拉都能把它带正，两路取先到者。
const withCtx: UIState = ev.context ? { ...state, context: ev.context } : state;
// 历史被压缩：插一条标记块（摘要可展开查看）。压缩不改动已有块——
// 被压缩的那些消息本来就不在 UI 里（它们是更早的轮次，早已滚出视图）
const sealed = finalizeStreaming(withCtx);
return { ...sealed, blocks: [...sealed.blocks, block] };
}

/** historyLoaded 事件的处理（从 reduce 的 switch 里提出来——原来 213 行的 switch
 *  每个分支都是一段独立逻辑，混在一起只能整体读）。 */
function reduceHistoryLoaded(state: UIState, ev: Ev<"historyLoaded">): UIState {
// **上翻分页的增量装载**（before_seq 应答）：把更早的一页**前插**，不重建时间线。
//
// 判据是事件自带的 `paging` 标记（historyBefore 发的事件才置真），**不是**快照里的
// has_more——首屏 tail 应答的 has_more 是前端推导的，重拉（rewind/setLeaf 副作用）
// 走的也是 loadHistory；那些必须**重建**（否则回放出来的「中断占位卡」永远修不
// 回来）。state 还没装载过（historyReady=false）时也走重建：没有基线可前插。
if (ev.paging && state.historyReady) {
  return mergeOlderHistory(state, ev.history);
}// **流式尾巴不许被快照冲掉**（2026-12 用户报「子 Agent 正文输出后消失」的根因）：
// assistant 消息由后端**整轮结束**才 append（turn.go 的 s.append 在 streamRound
// 返回之后），正在流式的那段正文永远不在 chat.history 里；而子会话的快照是
// 「现建运行时 + AttachTo」（server/session_ops.go 的 session()：子会话不进运行时
// 缓存），AttachTo 出来的 Session busy 恒 false —— 快照的 busy 键对子会话不携带
// 任何忙闲事实。旧守卫 `history.busy && state.busy && historyReady` 因此对子会话
// 恒不成立，重开视图（ChildSessionPage 挂载即 childHistory，StrictMode 下两次）
// 就把流式块整数组换掉。现在的口径：**重建照常做**（快照里可能有更多历史），但
// 本地那份流式尾巴必须接回去（appendLiveTail 去重：已落库时不追加、只续写同一块）。
const live = liveTail(state);
// 当前会话仍在生成且此前已完成过历史回放时，后端快照只含已追加消息；
// 本地还可能积累着**快照里没有的非流式实时块**（dispatch 卡、确认卡、任务卡）。
// 冷恢复/首次载入则必须重建确认卡等历史状态——所以这里保留旧快照语义。
if (ev.history.busy && state.busy && state.historyReady) {
  return {
    ...state,
    pending: ev.history.pending ?? state.pending,
    // 清单：**空快照不覆盖**（键缺席与空数组在 wire 上同形，HistoryResult.todos
    // 是 omitempty）。否则「子 Agent 建了清单 → 重开视图 → 清单消失」——
    // 用户报的「任务列表有时不显示」的另一半。
    todos: ev.history.todos && ev.history.todos.length > 0 ? ev.history.todos : state.todos,
    context: ev.history.context ?? state.context,
    // 统计同样保持旧值：忙碌中的快照不带 stats 时（后端读不到库）不该把
    // 已经显示的数字擦掉——读不到 ≠ 没有
    stats: ev.history.stats ?? state.stats,
    // 权限档同款：快照没带 approval（老后端/演示源）不写——不猜（不把已显示
    // 的档位抹成 null）
    approval: ev.history.approval ?? state.approval,
    historyReady: true,
  };
}
// 快照说会话在生成而本地不知道（新连上的客户端错过了那次 busy=true 广播）：
// 以服务端事实为准置真。反向不写（busy=false 抹掉本地真值是既有行为——
// rebuilt 展开已带 busy: ev.history.busy）。
const busySnapshot = ev.history.busy;
// 冷恢复/空闲会话：messages → blocks（工具调用与结果配对）。
// **任务卡不在历史里**（契约只把后台任务定义成 job.started/settled 事件，
// ChatHistoryResult 不带 jobs）——整块重建会把它们一起丢掉：切走再切回一个正在
// 跑 dev server 的会话，卡片不该凭空消失（它还在跑，顶栏面板里也还看得见）。
// 保留的是本会话状态里的卡（state 本就按会话隔离），位置接在重建后的历史之后。
const jobs = state.blocks.filter((b) => b.kind === "job");
const rebuilt = reduceHistory(state, ev.history);
// **观察端回填**（chat.history 的 running_dispatches）：本窗口错过 dispatchStart
// 广播（安卓重进会话/新开的桌面窗口）时，回放重建的派发卡没有子会话 id——
// 点不开子会话、todo/子工具事件按卡反查也无处可投（「任务进度消失」）。
// 后端导出**此刻正在跑**的派发快照（跑完即注销），按 dispatch_id 补进重建的卡：
// 只补空缺（id/名字/任务），**绝不**把已收尾的卡翻回运行中（回放占位卡的
// 运行态由 reviveRunningDispatch 按会话忙碌判定，口径不变）。
const filled = fillRunningDispatches(rebuilt.blocks, ev.history.runningDispatches);
// **重建代次号**（Thread 判「同会话全量重建 → 不重播入场动画」的标记）：
// blocks 被整体换掉（全新 uid）就算一次重建，从旧 state 递增。快照 busy 提前
// 返回的那条分支不重建 blocks，不递增——它一个块都没换。
const rebuiltGen = (state.rebuiltGen ?? 0) + 1;
// 会话仍在跑：最后那张「中断」占位卡其实是执行中（见 reviveRunningDispatch）
const revived = reviveRunningDispatch(filled, state.blocks, busySnapshot || state.busy);
// 流式尾巴接回（去重见 appendLiveTail）——它回到 state.streaming，不并入 blocks
//（之后还有 delta 到达时要继续续写同一块，见 liveTail）。
const { blocks: withLive, streaming: liveStream } = appendLiveTail(revived, live, ev.history);
const base = jobs.length ? [...withLive, ...jobs] : withLive;
return {
  ...rebuilt,
  rebuiltGen,
  busy: busySnapshot || ev.history.busy || live.length > 0,
  // 历史全量重建：pending 乐观块一起被重建抹掉——回放的历史里要么已有这条消息
  //（真块回来了），要么发送还没落库（留着会变僵尸 pending）。发送中态一并结束。
  sending: false,
  blocks: base,
  streaming: liveStream,
  // 分页元数据随快照走（键缺席 = 老后端/非分页应答 → 不分页，行为不变）
  historyMore: Boolean(ev.history.hasMore),
  historyFirstSeq: ev.history.firstSeq ?? 0,
  // 清单同上面那条守卫的理由：空快照不覆盖本地清单。
  todos: ev.history.todos && ev.history.todos.length > 0 ? ev.history.todos : state.todos,
  // 权限档按快照对齐（后端 LiveApproval——会话级状态持久化的显示依据）；
  // 快照没带（老后端/演示源）保持原值不猜。
  approval: ev.history.approval ?? state.approval,
};
}

/** 本地时间线里**还没落库的实时尾巴**（重建时接在快照之后，不许被抹掉）。
 *
 *  口径只有一条：尾块是流式中的 assistant。理由（2026-12 用户报「子 Agent 正文
 *  输出后消失」的根因）：assistant 消息由后端**整轮结束**才 append（turn.go 的
 *  s.append 在 streamRound 返回之后），所以正在流式的那段正文在 chat.history 里
 *  根本不存在；子会话的 chat.history 又是「现建运行时 + AttachTo」（session_ops.go
 *  的 session()），AttachTo 出来的 Session busy 恒 false —— 快照的 busy 键对子会话
 *  不携带任何忙闲事实，于是旧守卫（`history.busy && state.busy`）恒不成立：重开
 *  视图（ChildSessionPage 每次挂载都 childHistory，StrictMode 下还是两次）就把
 *  流式块整数组换掉 → 用户看到文字凭空消失。
 *
 *  只取尾块而不是「所有 streaming 块」：reduceDelta 开新块时会把前一块定格
 *  （streaming=false），所以流式块恒在末尾；多接就是重复。 */
function liveTail(state: UIState): ThreadBlock[] {
  return state.streaming ? [state.streaming] : [];
}

/** 把实时尾巴接回重建结果（**去重**：同一块可能已经落库并在快照里了）。
 *
 *  竞态窗口：后端 append 之后、chat.done 到达之前拉一次 chat.history——快照里
 *  已经有这条 assistant，而本地那块还是 streaming。此时直接追加就是**同一条回复
 *  出现两遍**。判定用快照最后一条消息：角色 assistant 且正文是实时块正文的前缀
 *  （deltas 只追加不修改，所以「已落库的部分」一定是前缀）→ 判定为同一条，不追加；
 *  并把重建出来的那块重新标成 streaming，让后续 delta 续写同一块（否则 reduceDelta
 *  会在它后面另开一块，把一条回复劈成两半）。 */
function appendLiveTail(
  blocks: ThreadBlock[],
  live: ThreadBlock[],
  history: Ev<"historyLoaded">["history"],
): { blocks: ThreadBlock[]; streaming: AssistantBlock | null } {
  if (live.length === 0) return { blocks, streaming: null };
  const tail = live[0] as AssistantBlock;
  // 重建结果里的**最后一块 assistant**（同一条回复只可能落在它身上）
  let idx = -1;
  for (let i = blocks.length - 1; i >= 0; i--) {
    if (blocks[i].kind === "assistant") { idx = i; break; }
  }
  const last = idx >= 0 ? (blocks[idx] as AssistantBlock) : null;
  // ① 它就是这条回复（相同 / 互为前缀）→ 合并，正文取较长的那份（本地领先 = 快照
  //    落后几个 delta；快照领先 = 本地还差几条，别把已落库的文字吞回去），并标回
  //    streaming 让后续 delta 续写同一块（否则 reduceDelta 会在后面另开一块，把一条
  //    回复劈成两半）。收尾由 chat.done 负责。
  if (last && sameReply(last.content, tail.content)) {
    const content = tail.content.length >= last.content.length ? tail.content : last.content;
    const reasoning = tail.reasoning.length >= last.reasoning.length ? tail.reasoning : last.reasoning;
    // 合并结果回到 streaming（后续 delta 续写同一块；收尾由 chat.done 负责）。
    // 用重建块的 uid：同一段回复的 React key 不因一次重拉而变（不重挂）。
    // **必须从 blocks 里摘掉**，否则同一条回复会在时间线里出现两遍。
    // 只在它是**尾块**时摘（正常情形：这条回复就是最后一条消息）；它后面还有块
    // （工具行/后续轮次）说明这条回复早就写完了，本地那份是陈旧的实时尾巴——
    // 丢弃即可，不动重建出来的历史（与情形 ② 同一条判定）。
    if (idx === blocks.length - 1) {
      return {
        blocks: blocks.slice(0, idx),
        streaming: { ...last, streaming: true, content, reasoning },
      };
    }
    return { blocks, streaming: null };
  }
  // ② 快照里已有这条回复、但它后面还有更新的块 → 本地那份是**陈旧的实时尾巴**
  //    （已落库并被后续轮次接上）：丢弃，不许追加（否则同一条回复出现两遍），也不许
  //    覆盖最后一块（那会把新一轮的正文替换成上一轮的字）。
  const persisted = (history.messages ?? []).some(
    (m) => m.role === "assistant" && sameReply(m.content ?? "", tail.content),
  );
  if (persisted) return { blocks, streaming: null };
  // ③ 确实是快照之外的实时块（后端整轮结束才 append，正在写的新回复）→ 作为
  //    流式块续上（它还没落库，仍在流；append 之后 delta 还要写它）
  return { blocks, streaming: tail };
}

/** 两条正文是不是**同一条回复**：完全相同，或一方是另一方的前缀（delta 只追加不修改）。
 *  空串不参与前缀判定（空 assistant 块与任何正文都"前缀相容"，那会把不同回复并成一条）。 */
function sameReply(a: string, b: string): boolean {
  if (a === b) return true;
  if (a.length === 0 || b.length === 0) return false;
  return a.startsWith(b) || b.startsWith(a);
}

/** 回放占位卡在会话仍然忙碌时恢复成「执行中」（对齐安卓端 markDispatchRunning）。
 *
 *  历史里没有配对结果的调度会被 history.ts 先按「中断」定格（status done +
 *  isError + DISPATCH_INTERRUPTED）——那是**没有证据时的诚实默认**。但如果这个
 *  会话此刻仍在跑，最后那张占位卡其实正在执行：不回填的话（a）用户看到一张红着的
 *  「✗ 失败」卡，子 Agent 明明还在干活（安卓端同一问题的修复见 DispatchState.kt）；
 *  （b）`applyDispatchTodos` 的迟到守卫按 `status === "done"` 丢弃后续 todo.updated
 *  ——卡上的清单进度永远不动，这正是「任务列表有时不显示」的另一半根因。
 *
 *  只复活**最后一张**占位卡：更早的占位卡是历史上真断掉的派发，本会话后来的忙碌
 *  与它无关（把它们一起点亮 = 把中断说成在跑）。子块与清单从本地那份同 id 的卡
 *  继承（重建出来的占位卡这两项恒空——回放无从得知）。 */
function reviveRunningDispatch(rebuilt: ThreadBlock[], prevBlocks: ThreadBlock[], busy: boolean): ThreadBlock[] {
  if (!busy) return rebuilt;
  let idx = -1;
  for (let i = rebuilt.length - 1; i >= 0; i--) {
    if (rebuilt[i].kind === "dispatch") { idx = i; break; }
  }
  if (idx < 0) return rebuilt;
  const card = rebuilt[idx];
  if (card.kind !== "dispatch") return rebuilt;
  if (card.status !== "done" || !card.isError || card.result !== DISPATCH_INTERRUPTED) return rebuilt;
  // 本地同 id 的那份（可能带实时子块/清单）——只补空项，不覆盖重建出来的历史事实
  const prev = prevBlocks.find((b) => b.kind === "dispatch" && b.id === card.id) as
    | Extract<ThreadBlock, { kind: "dispatch" }>
    | undefined;
  const next = rebuilt.slice();
  next[idx] = {
    kind: "dispatch",
    uid: card.uid,
    id: card.id,
    sessionId: card.sessionId ?? prev?.sessionId,
    agentId: card.agentId,
    agentName: card.agentName,
    agentColor: card.agentColor,
    task: card.task,
    status: "running",
    subBlocks: card.subBlocks.length > 0 ? card.subBlocks : (prev?.subBlocks ?? []),
    ...(card.subStreaming ?? prev?.subStreaming ? { subStreaming: card.subStreaming ?? prev?.subStreaming } : {}),
    ...(card.todos ?? prev?.todos ? { todos: card.todos ?? prev?.todos } : {}),
    ...(card.attachments ? { attachments: card.attachments } : {}),
  };
  return next;
}

/** 观察端回填（chat.history 的 running_dispatches → 重建的派发卡）。
 *
 *  归约是**纯函数**：重建只看快照里的消息，而 dispatchStart 是事件、不在历史里
 *  ——错过它的客户端（安卓重进会话、新开的桌面窗口）重建出来的派发卡没有
 *  子会话 id。后端在应答里带**此刻正在跑**的派发快照（agent.Session 的运行中
 *  登记，跑完即注销），这里按 dispatch_id 处理重建结果：
 *   - 卡不在（回放窗口被 tail 截掉）→ 追加一张运行中卡（那张卡上的进度事件是
 *     **现在**发出来的，没有卡就全部丢弃）；
 *   - 卡是回放「中断」占位态（done + isError + 占位文案）→ 恢复成运行中并补
 *     id/名字/任务——快照是「这个派发**正在跑**」的直接证据，比 reviveRunningDispatch
 *     的 busy 启发式强（那个只认最后一张卡）；
 *   - 卡是真终态（成功收尾/带真实错误的失败）→ 绝不碰：快照说"在跑"只对活着
 *     的派发成立（后端跑完即注销，终态卡被翻回运行中 = 把历史说成现在）；
 *   - 卡已在运行中 → 只补空缺（id/名字/任务）。 */
function fillRunningDispatches(
  blocks: ThreadBlock[],
  running: RunningDispatchInfo[] | undefined,
): ThreadBlock[] {
  if (!running || running.length === 0) return blocks;
  const next = blocks.slice();
  for (const r of running) {
    const idx = next.findIndex((b) => b.kind === "dispatch" && b.id === r.dispatchId);
    if (idx < 0) {
      // 卡不在：尾部窗口没截到它的历史（派发在窗口之前发起）——按快照补一张
      // 运行中卡。agentColor 留空：渲染层按 agentId 从注册表回落（与回放路径
      // 同一条纪律，纯函数里没有注册表）。
      next.push({
        kind: "dispatch", uid: nextUid(), id: r.dispatchId,
        sessionId: r.childSessionId || undefined,
        agentId: "", agentName: r.agentName, agentColor: "",
        task: r.task, status: "running", subBlocks: [],
      });
      continue;
    }
    const card = next[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
    const placeholder = card.status === "done" && card.isError === true && card.result === DISPATCH_INTERRUPTED;
    if (card.status === "done" && !placeholder) continue; // 真终态绝不翻回运行中
    next[idx] = {
      ...card,
      status: placeholder ? "running" : card.status,
      isError: placeholder ? undefined : card.isError,
      result: placeholder ? undefined : card.result,
      sessionId: card.sessionId ?? (r.childSessionId || undefined),
      agentName: card.agentName || r.agentName,
      task: card.task || r.task,
    };
  }
  return next;
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
    // 追加前定型流式块（不变量：streaming 恒为逻辑上的最后一块）
    const sealed = finalizeStreaming(state);
    return { ...sealed, blocks: [...sealed.blocks, { kind: "job", uid: nextUid(), job }] };
  }
  const blocks = state.blocks.slice();
  const prev = blocks[idx] as Extract<ThreadBlock, { kind: "job" }>;
  blocks[idx] = { ...prev, job };
  return { ...state, blocks };
}

/** 把子事件应用进 dispatch 块的 subBlocks（子上下文隔离的归属路由）。
 *  fn 拿到当前 subBlocks 返回新的；dispatch 块不存在时原样返回。
 *
 *  **流式增量走 subStreaming**（与主时间线的 state.streaming 同一个理由与不变量）：
 *  fn 返回 {blocks, streaming} 时块照旧落 subBlocks，正在流式的那一块单独记在
 *  卡的 subStreaming 里——每帧只换这一个对象，不复制整个时间线与整张卡。
 *  注意子块不参与渲染（卡 = 一行摘要，见 docs/child-session-ui.md），这里分开
 *  存是为了**归约语义与主时间线同构**（toolCall/done 定型时的行为逐条一致）。 */
function applyToDispatch(
  state: UIState,
  dispatchId: string,
  fn: (subBlocks: ThreadBlock[], subStreaming: AssistantBlock | null) => ThreadBlock[] | SubReduce | null,
): UIState {
  const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === dispatchId);
  if (idx < 0) return state;
  const blocks = state.blocks.slice();
  const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
  const next = fn(b.subBlocks, b.subStreaming ?? null);
  if (!next) return state;
  blocks[idx] = Array.isArray(next)
    ? { ...b, subBlocks: next }
    : { ...b, subBlocks: next.blocks, subStreaming: next.streaming ?? undefined };
  return { ...state, blocks };
}

/** 子时间线的归约产出：已定型块 + 正在流式的那一块（见 DispatchBlock.subStreaming）。 */
interface SubReduce {
  blocks: ThreadBlock[];
  streaming: AssistantBlock | null;
}

/** dispatch 卡是否已在时间线里（确认卡归属的前置检查——卡不在就回落，
 *  否则 applyToDispatch 静默丢弃该确认）。 */
function hasDispatch(state: UIState, dispatchId: string): boolean {
  return state.blocks.some((b) => b.kind === "dispatch" && b.id === dispatchId);
}

/** 子事件在子时间线上的归约（与主时间线同款语义，作用域是 subBlocks +
 *  subStreaming）。返回数组 = 只改已定型块；返回 SubReduce = 连流式块一起换。 */
function reduceSub(blocks: ThreadBlock[], subStreaming: AssistantBlock | null, ev: AgentEvent): ThreadBlock[] | SubReduce | null {
  switch (ev.type) {
    case "delta": {
      // 流式增量：只换 streaming 这一个对象（不复制子时间线——与主时间线同一条纪律）
      const target: AssistantBlock = { ...(subStreaming ?? { kind: "assistant", uid: nextUid(), content: "", reasoning: "", streaming: true }) };
      if (ev.kind === "text") target.content += ev.text;
      else target.reasoning += ev.text;
      return { blocks, streaming: target };
    }
    case "toolCall": {
      // 工具调用打断流式：先把流式块定型进 subBlocks（一次 O(块数)），再挂工具行
      const next = subStreaming
        ? [...blocks, { ...subStreaming, streaming: false }]
        : blocks.slice();
      next.push({ kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments });
      return { blocks: next, streaming: null };
    }
    case "compacted": {
      // 子会话自己的压缩：卡内插一条标记块（与主时间线同一个组件）
      const base = subStreaming ? [...blocks, { ...subStreaming, streaming: false }] : blocks;
      return {
        blocks: [...base, {
          kind: "compacted" as const, uid: nextUid(), before: ev.before, after: ev.after,
          shadowed: ev.shadowed, summary: ev.summary, manual: Boolean(ev.manual),
        }],
        streaming: null,
      };
    }
    default:
      return null;
  }
}

/** 将一条服务端事件只归约进它所属的 Session；导出供并发隔离测试直接验证。
 *
 *  **子事件要双投**（2026-09-30 用户拍板：子会话视图也要看到实时流）：
 *  子 Agent 的事件按**父会话 id** 广播（后端由父会话的 emitter 发出，带 dispatch_id
 *  归属进卡），所以上面那一份进的是父会话的 dispatch 卡（摘要 + 状态）；
 *  而子会话**自己**那个 id 的 state 也要拿到同一份（子会话视图里看到的就是它），
 *  否则视图只有一次性的历史快照——「点开之后他里面就没有接着思考」。
 *
 *  双投时把 dispatchId 清掉：对子会话自己的时间线来说，它就是一条普通事件
 *  （delta 续写 assistant、toolCall 建工具行、confirmRequest 挂待裁决、compacted
 *  插压缩标记）——带着 dispatchId 会被当成"进某张卡"而在子会话里找不到那张卡。
 *
 *  子会话自己的 state 还不存在（标签从没打开过）时不建：没有历史基线的话，
 *  光靠实时增量拼出来的时间线是半截的；打开标签时用历史重建（见 ChildSessionPage
 *  装载 + source.childHistory 发的 historyLoaded）。
 *
 *  dispatchChild（可选）：store 维护的 dispatch id → 子会话 id 映射（与标签过滤用的
 *  子→父映射同源，见 workspace-tabs.ts 的 ChildLinks）。双投归属先查表（O(1)），
 *  查不到再回落 dispatchChildSession 的全量反查——直接调用本函数的测试没传表时
 *  语义不变（保守处理：全量反查仍是兜底，只是不再是热路径）。 */
export function reduceSessionStates(
  states: Record<string, UIState>,
  ev: AgentEvent,
  dispatchChild?: Record<string, string>,
): Record<string, UIState> {
  if (!("sessionId" in ev) || !ev.sessionId) return states;
  const id = ev.sessionId;
  const previous = states[id] ?? { ...initial, currentId: id };
  const next = { ...states, [id]: { ...reduce(previous, ev), currentId: id } };
  const did = eventDispatchId(ev);
  const childId = did ? (dispatchChild?.[did] ?? dispatchChildSession(states, did)) : "";
  if (did && childId && childId !== id && states[childId]) {
    if (ev.type === "dispatchEnd") {
      // dispatchEnd 是卡生命周期事件（不整投进子会话 state），但它的 `finalTodos`
      // 是**子会话清单的终态**（todo 契约批③），不是卡呈现——按 todoUpdated 的
      // 归约路径写进子会话自己的 state：子会话视图的计划条据此从「最后一次
      // todo.updated」定格到终态。不这样做的话，模型做完最后一步工作直接给结论
      //（没再调 todo 把它标 done）时，计划条上那一项永远挂着 active——这正是
      // 「子会话结束后清单最后一项没显示完成」的根因。清单为空（没建过清单）
      // 不写（wire 约定：键缺席 = 未知，不猜）。
      if (ev.finalTodos && ev.finalTodos.length > 0) {
        next[childId] = {
          ...reduce(states[childId], { type: "todoUpdated", sessionId: childId, items: ev.finalTodos }),
          currentId: childId,
        };
      }
    } else if (!isCardLifecycle(ev)) {
      const child = states[childId];
      // 任务消息的标记（实时路径的问题 1 修复）：带 dispatch_id 的 userMessage 是
      // 主 Agent 派发的任务说明书。dispatchId 必须剥掉（带着它，子会话里的归约会
      // 去找一张不存在的 dispatch 卡——delta 等事件就是这么被静默丢弃的），但「这是
      // 任务消息」的信息不能跟着丢：剥掉之后子会话无法把它与真实用户消息区分开。
      // task 标记就是干这个的——reduceUserMessage 据此渲染「任务」块而不是用户气泡。
      // 注入通告（中断注记 / 重复调用提醒）同样带 dispatch_id 上抛，它们不是任务——
      // reduceUserMessage 的前缀表判定优先于 task 标记，照旧走 notice / hidden。
      let childEv = withoutDispatch(ev);
      if (ev.type === "userMessage" && did !== "") {
        childEv = { ...childEv, task: true } as AgentEvent;
      }
      let childNext = { ...reduce(child, childEv), currentId: childId };
      // 生成中指示的兜底（2026-12 用户报「子会话视图缺少生成中样式」）：busy=true
      // 只在轮开始那一刻发**一次**（session.go 的 Send），而子会话 state 是双投的
      // 「不建」规则——视图打开之前那条 busy=true 无人接收；打开视图时
      // chat.history 的快照又来自按 id 现建的运行时，busy 恒 false。结果整轮都
      // 没有生成中指示。子会话侧的活动事件（delta/toolCall/toolResult/任务消息/
      // 待裁决确认）本身就证明子会话正在跑——见到就把 busy 置真；翻回 false 仍由
      // 轮末那条 busy=false 事件负责（它无条件发，不会漏）。主会话的归约不受影响
      //（忙闲仍由它自己的 busy 事件驱动）。
      if (CHILD_BUSY_HINTS.has(ev.type)) childNext = { ...childNext, busy: true };
      next[childId] = childNext;
    }
  }
  return next;
}

/** 这些事件只可能发生在会话正在跑的时候——双投进子会话 state 时顺带把 busy 置真
 *  （子会话「生成中」指示的兜底，见 reduceSessionStates 里的注释）。 */
const CHILD_BUSY_HINTS = new Set<AgentEvent["type"]>([
  "delta", "toolCall", "toolResult", "userMessage", "confirmRequest",
]);

/** dispatch 卡的生命周期事件（卡的开/关）：它们是**父会话时间线**上的呈现——
 *  双投时绝不送进子会话自己的 state，否则 reduceDispatchStart 会在子会话时间线里
 *  再画一张「调用子 Agent」卡（2026-12 用户报：子 Agent 深度恒 1，不可能再派发，
 *  那张卡纯属归属错误）。子会话视图里它的过程由 delta/toolCall 等事件本身呈现。 */
function isCardLifecycle(ev: AgentEvent): boolean {
  return ev.type === "dispatchStart" || ev.type === "dispatchEnd";
}

/** 事件归属的那个 dispatch id（子事件双投用的键）。
 *
 *  多数带归属的事件在**顶层** `dispatchId`（events.ts 的映射），而**确认请求是个
 *  例外**：它的归属在 `request.dispatch_id` 里（协议 ConfirmRequest.dispatch_id，
 *  与 toProtocolConfirm 一致）。漏了这一条，子 Agent 的确认卡就只落在父会话的卡里——
 *  子会话视图里看不到待裁决的确认（用户没打开视图时反倒只能去主会话批）。 */
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

/** dispatchId → 子会话 id（子事件双投用的归属键；**兜底路径**）。
 *
 *  热路径已改为 store 维护的 dispatch→子会话映射查表（O(1)，见 reduceSessionStates
 *  的 dispatchChild 参数）——这里只剩两处用：映射还没记上时（理论上不该发生：
 *  dispatchStart 先于一切子事件到达并记映射）、以及没传表的直接调用方（测试）。
 *  反查逻辑保留原样：子会话 id 记在**卡上**（实时 dispatchStart 的 childSessionId，
 *  或历史回放的工具结果里那行 `[子会话 id: …]`——见 history.ts），两处都会把它写进
 *  `block.sessionId`。没见过这张卡（应用刚起、dispatchStart 早于连接）时返回空串
 *  （不猜）。 */
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
 *  **ask 提问不在清单里**：切「完全访问」（auto）放行的是高危确认打扰，
 *  提问是模型向用户要决策——auto 不能替用户回答，UI 也不能替它定格
 *  （后端对 ask 保持挂起，见 Session.SetApproval 的同一条判定）。
 *
 *  用途：切「完全访问」时后端沿确认通道一次性放行挂起确认（含子会话的——
 *  确认门代理走父通道），UI 的待裁决卡片要同步定格，不等 toolResult 回执。
 *  导出供测试直接验证清单形状。 */
export function pendingConfirmIds(st: UIState): string[] {
  const ids: string[] = [];
  if (st.pending && st.pending.kind !== "ask") ids.push(st.pending.id);
  for (const b of st.blocks) {
    if (b.kind === "confirm" && b.request.kind !== "ask") ids.push(b.request.id);
    if (b.kind === "dispatch") {
      for (const s of b.subBlocks) {
        if (s.kind === "confirm" && s.request.kind !== "ask") ids.push(s.request.id);
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
 *  否则每次裁决都会把所有会话的 pending 清掉（那是别人的挂起确认）。
 *  answer 只在 ask 提问的回答时有值（显示在已决卡上）；二元裁决忽略它。 */
export function resolveConfirmEverywhere(
  states: Record<string, UIState>,
  id: string,
  outcome: "allow" | "deny",
  answer?: string,
): Record<string, UIState> {
  const holds = (st: UIState): boolean =>
    st.pending?.id === id ||
    st.blocks.some((b) => (b.kind === "confirm" && b.request.id === id)
      || (b.kind === "dispatch" && b.subBlocks.some((s) => s.kind === "confirm" && s.request.id === id)));
  let next: Record<string, UIState> | null = null;
  for (const [sid, st] of Object.entries(states)) {
    if (!holds(st)) continue;
    next = next ?? { ...states };
    next[sid] = resolveConfirm(st, id, outcome, answer);
  }
  return next ?? states;
}

/** 确认裁决后：允许 → 卡片就地变成工具行（后续 toolResult 填结果——
 *  与 DSH 同款：授权后看的是工具执行，不是审批表单）；拒绝 → 卡片
 *  定格为「已跳过」（没有工具执行可展示）。主时间线与 dispatch 卡内
 *  的确认都走这里（按 request.id 定位）。
 *
 *  **ask 提问是例外**：它没有工具执行可看——回答后卡片保留原位定格
 *  「已回答」（resolvedAnswer = 用户答案摘要）；跳过定格「已跳过」。
 *  后端随后的 toolResult（「用户回答：…」）在卡上找不到未裁决位就自然丢弃
 *  （答案已经在卡上了，不必重复呈现）。
 * 导出供测试直接驱动真实现——测试若复刻一份逻辑，断言的是副本，
 * 真实现漂移时不会红（假绿）。 */
export function resolveConfirm(state: UIState, id: string, outcome: "allow" | "deny", answer?: string): UIState {
  const convert = (blocks: ThreadBlock[]): ThreadBlock[] | null => {
    const idx = blocks.findIndex((b) => b.kind === "confirm" && b.request.id === id);
    if (idx < 0) return null;
    const next = blocks.slice();
    const b = next[idx] as Extract<ThreadBlock, { kind: "confirm" }>;
    if (b.request.kind === "ask") {
      // 提问卡不转工具行：保留原位显示「已回答（答案摘要）/已跳过」
      next[idx] = { ...b, resolved: outcome, resolvedAnswer: outcome === "allow" ? answer : undefined };
      return next;
    }
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
