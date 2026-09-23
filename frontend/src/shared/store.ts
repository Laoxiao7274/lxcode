// 事件流 → UI 状态的归约。Thread 的渲染单元是"块"（block）：
// 一条 user 消息、一条 assistant 回复（含正文/思考链/流式态）、一次
// 工具调用（含结果）、一张确认卡、一份任务清单、一条错误。
import { useCallback, useEffect, useState } from "react";
import type { AgentEvent, AgentSource, ConfirmRequest, ContextUsage, FileChange, HistorySnapshot, SendOptions, TodoItem } from "./types";

export interface AssistantBlock {
  kind: "assistant";
  uid: number;
  content: string;
  reasoning: string;
  streaming: boolean;
  usageTokens?: number;
}

export type ThreadBlock =
  | { kind: "user"; uid: number; text: string }
  | AssistantBlock
  | { kind: "tool"; uid: number; id: string; name: string; arguments: string; result?: string; isError?: boolean }
  | { kind: "confirm"; uid: number; request: ConfirmRequest; resolved?: "allow" | "deny" }
  | { kind: "files"; uid: number; files: FileChange[] }
  | { kind: "error"; uid: number; message: string; aborted: boolean }
  | {
      /** 历史压缩的标记块（前缀被摘要检查点替换）——可展开看摘要正文。 */
      kind: "compacted";
      uid: number;
      /** 压缩前后的上下文占用（估算 token）。 */
      before: number;
      after: number;
      /** 被替换的历史条数。 */
      shadowed: number;
      /** 摘要正文（markdown）。 */
      summary: string;
      /** 用户主动触发（/compact 或指示器入口）。 */
      manual: boolean;
    }
  | {
      kind: "dispatch";
      uid: number;
      /** dispatch 调用 id（子事件归属键）。 */
      id: string;
      /** 子会话 id（子 Agent 是独立会话：自己的历史/压缩/可续跑）。 */
      sessionId?: string;
      agentId: string;
      agentName: string;
      agentColor: string;
      /** 下发的任务描述（主 Agent 的验收标准在这里）。 */
      task: string;
      /** running | done（done 带 result——子 Agent 的最终回复）。 */
      status: "running" | "done";
      /** 子 Agent 的最终回复（dispatchEnd 回填——验收视图）。 */
      result?: string;
      /** 子执行过程的块（子上下文隔离——delta/工具行挂在这里，不进主时间线）。 */
      subBlocks: ThreadBlock[];
      /** 子 Agent 用量（轮末）。 */
      usageTokens?: number;
      isError?: boolean;
    };

export interface UIState {
  blocks: ThreadBlock[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
  /** 当前会话 id（sessionChanged/new|resumed|started 同步——侧栏高亮与
   *  标题的唯一事实源；并入 store 消灭 App 的第二份订阅与双渲染）。 */
  currentId: string;
  /** 请求类失败的一次性提示（operationError——App 之前自持的状态）。 */
  operationError: string | null;
  /** 上下文占用（后端测量；null = 未知——刚切会话/后端刚重启，指示器显示
   *  中性态而不是编一个数）。 */
  context: ContextUsage | null;
}

const initial: UIState = { blocks: [], busy: false, pending: null, todos: [], currentId: "", operationError: null, context: null };

// 块的唯一序号——React 渲染的稳定 key（index 作 key 在插入新块时
// 会错位复用组件实例，是重复渲染类怪象的根因）。
let uidSeq = 0;
const nextUid = () => ++uidSeq;

/** shell 拷贝 + 单点替换（map 变体的免回调版——toolResult/resolve 这类
 *  「只改个别块」的路径，O(n) 指针拷贝无逐元素闭包）。 */
function withBlock(state: UIState, uid: number, patch: (b: never) => ThreadBlock): UIState {
  const blocks = state.blocks.slice();
  const idx = blocks.findIndex((b) => b.uid === uid);
  if (idx >= 0) blocks[idx] = patch(blocks[idx] as never);
  return { ...state, blocks };
}

export function reduce(state: UIState, ev: AgentEvent): UIState {
  switch (ev.type) {
    case "userMessage":
      return {
        ...state,
        blocks: [...state.blocks, { kind: "user", uid: nextUid(), text: ev.text }],
      };
    case "delta": {
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
    case "toolCall": {
      if (ev.dispatchId) return applyToDispatch(state, ev.dispatchId, (sub) => reduceSub(sub, ev));
      // 工具调用打断流式中的 assistant 块——立刻定格（否则它永远挂着
      // "思考中" shimmer，成为僵尸块；中间插工具/清单后再开新块续写）
      const blocks = state.blocks.slice();
      for (let i = 0; i < blocks.length; i++) {
        const b = blocks[i];
        if (b.kind === "assistant" && b.streaming) blocks[i] = { ...b, streaming: false };
      }
      // agent.dispatch 不建工具行：它的渲染形态就是 dispatch 卡
      //（随后 dispatchStart 挂卡）。两处都建 = 同一个调度渲染两遍——
      // 外面一个工具行、卡里一份执行过程（用户报的「重复」）。
      if (ev.name === "agent.dispatch") return { ...state, blocks };
      return {
        ...state,
        blocks: [...blocks, { kind: "tool", uid: nextUid(), id: ev.id, name: ev.name, arguments: ev.arguments }],
      };
    }
    case "toolResult": {
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
    case "confirmRequest": {
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
    case "todoUpdated":
      // 任务清单不进对话流（用户拍板：只做输入框上方的计划条——
      // 消息流里再插一份是重复呈现；历史回放同样不重建清单卡）
      return { ...state, todos: ev.items };
    case "done": {
      // dispatch 归属：子轮的 done 定格子时间线里最后一个 assistant
      if (ev.dispatchId) {
        return applyToDispatch(state, ev.dispatchId, (sub) => {
          for (let i = sub.length - 1; i >= 0; i--) {
            const b = sub[i];
            if (b.kind === "assistant") {
              const next = sub.slice();
              next[i] = { ...b, streaming: false, usageTokens: ev.usageTokens };
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
      const withCtx = ev.context ? { ...state, context: ev.context } : state;
      let lastA: AssistantBlock | undefined;
      for (let i = withCtx.blocks.length - 1; i >= 0; i--) {
        const b = withCtx.blocks[i];
        if (b.kind === "assistant") { lastA = b; break; }
      }
      if (!lastA) return withCtx;
      return withBlock(withCtx, lastA.uid, (b) => {
        const a = b as AssistantBlock;
        return { ...a, streaming: false, usageTokens: ev.usageTokens };
      });
    }
    case "error": {
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
    case "dispatchStart": {
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
          kind: "dispatch", uid: nextUid(), id: ev.dispatchId, sessionId: ev.sessionId,
          agentId: ev.agentId, agentName: ev.agentName, agentColor: ev.agentColor,
          task: ev.task, status: "running", subBlocks: [],
        }],
      };
    }
    case "dispatchEnd": {
      // 子 Agent 最终回复作为结果回填（卡定格——结果在卡内展示，
      // 主会话后续由主 Agent 继续汇总会话）
      const idx = state.blocks.findIndex((b) => b.kind === "dispatch" && b.id === ev.dispatchId);
      if (idx < 0) return state;
      const blocks = state.blocks.slice();
      const b = blocks[idx] as Extract<ThreadBlock, { kind: "dispatch" }>;
      blocks[idx] = {
        ...b, status: "done", result: ev.result, isError: ev.isError,
        usageTokens: ev.usageTokens, sessionId: ev.sessionId ?? b.sessionId,
      };
      return { ...state, blocks };
    }
    case "busy":
      return { ...state, busy: ev.busy, pending: ev.busy ? state.pending : null };
    case "sessionChanged":
      // reason 语义：new（用户点新对话——清屏）/ resumed（切会话——清屏后
      // 等 historyLoaded 重放）/ started（懒建行——只刷新列表，对话进行中不清屏）
      if (ev.reason !== "new" && ev.reason !== "resumed") return { ...state, currentId: ev.id };
      return { ...initial, currentId: ev.id };
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
    case "compacted": {
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
    case "historyLoaded":
      // 全量重建（连接/切会话后）：messages → blocks（工具调用与结果配对）
      return { ...reduceHistory(state, ev.history), busy: ev.history.busy };
    default:
      return state;
  }
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

/** 确认卡落位：同 id 的工具行（toolCall 已建）就地换成确认卡——uid 不变。
 *  不能"追加一张卡"：那样工具行与确认卡两行并存，批准时 confirm 卡又转
 *  成工具行，同一 id 出现两条工具行，而 toolResult 的 findIndex 只回填
 *  第一条——第二条永远停在"执行中…"且不可展开（running 时 expandable=false）。
 *  没有对应工具行（如确认先于 toolCall 到达）才追加新卡。 */
function placeConfirm(blocks: ThreadBlock[], request: ConfirmRequest): ThreadBlock[] {
  const idx = blocks.findIndex((b) => b.kind === "tool" && b.id === request.id);
  const next = blocks.slice();
  if (idx >= 0) {
    next[idx] = { kind: "confirm", uid: next[idx].uid, request };
    return next;
  }
  next.push({ kind: "confirm", uid: nextUid(), request });
  return next;
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

/** 压缩检查点的定界标记（对齐后端 agent/compaction_prompt.go）。 */
const CHECKPOINT_OPEN = "<compacted-summary>";
const CHECKPOINT_CLOSE = "</compacted-summary>";

/**
 * 从检查点消息内容里剥出摘要正文：后端把摘要包成「前言 + 定界标记 + 正文」，
 * 前端只该展示正文（前言是给模型看的，不该出现在界面上）。
 * 标记缺失时原样返回——历史坏数据不该让界面渲染空白。
 */
export function checkpointBody(content: string): string {
  const start = content.indexOf(CHECKPOINT_OPEN);
  const end = content.lastIndexOf(CHECKPOINT_CLOSE);
  if (start < 0 || end <= start) return content;
  return content.slice(start + CHECKPOINT_OPEN.length, end).trim();
}

/** 历史快照 → UI 状态：消息序列重建 blocks。
 *  配对规则：assistant 的 tool_calls 先开 tool 块；后续 role=tool 的消息
 *  按 tool_call_id 回填对应块的 result（服务端的存储顺序保证可达）。 */
function reduceHistory(state: UIState, h: HistorySnapshot): UIState {
  const blocks: ThreadBlock[] = [];
  const checkpoints = new Set(h.checkpoints ?? []);
  let lastAssistant: AssistantBlock | null = null;
  for (let i = 0; i < h.messages.length; i++) {
    const m = h.messages[i];
    if (m.role === "user") {
      // 压缩检查点不是用户说的话：渲染成标记块（否则会变成一个巨大的用户气泡，
      // 把真正的用户消息淹没——这是回放路径与实时路径必须一致的地方）
      if (checkpoints.has(i)) {
        blocks.push({ kind: "compacted", uid: nextUid(), before: 0, after: 0, shadowed: 0, summary: checkpointBody(m.content), manual: false });
      } else {
        blocks.push({ kind: "user", uid: nextUid(), text: m.content });
      }
      lastAssistant = null;
    } else if (m.role === "assistant") {
      const a: AssistantBlock = {
        kind: "assistant", uid: nextUid(), content: m.content,
        reasoning: m.reasoning_content ?? "", streaming: false,
      };
      blocks.push(a);
      lastAssistant = a;
      // assistant 携带的工具调用：紧跟工具块（保持原顺序）
      for (const tc of m.tool_calls ?? []) {
        blocks.push({
          kind: "tool", uid: nextUid(), id: tc.id ?? "",
          name: tc.function?.name ?? "", arguments: tc.function?.arguments ?? "",
        });
      }
    } else if (m.role === "tool") {
      // 工具结果回填（按 tool_call_id 找块；找不到则丢弃——防御坏数据）
      const target = blocks.find((b) => b.kind === "tool" && b.id === m.tool_call_id) as
        | Extract<ThreadBlock, { kind: "tool" }>
        | undefined;
      if (target && target.result === undefined) {
        target.result = m.content;
        target.isError = false;
      }
      // 工具结果后正文续写：新开 assistant 块（下一条 assistant 自然处理）
      lastAssistant = null;
    }
  }
  // 没有任何输出的进行中轮次不重现（streaming 重建成本高，历史里也少见）。
  // 挂起的确认必须重建：刷新/切会话后待裁决的确认卡不能只留在 state.pending
  //（没有任何组件渲染 pending——只有 blocks 里的 confirm 块才会画出来），
  // 否则会话卡在 busy=true 且用户无从批准（死锁）。
  // 用 placeConfirm 就地替换：后端在确认门挂起前已把 assistant 的 tool_calls
  // 落库，所以历史里必然有一行同 id 的工具行——若直接追加会重现"两行同 id"。
  const pending = h.pending ?? null;
  const withPending = pending ? placeConfirm(blocks, pending) : blocks;
  return {
    ...state,
    blocks: withPending,
    busy: false,
    pending,
    todos: h.todos ?? [],
    // 未知占用（后端刚重启/刚切会话）置 null —— 指示器显示中性态，
    // 不沿用上一会话的数字（那是别人的窗口占用）
    context: h.context ?? null,
  };
}

/** useAgent：订阅 AgentSource 并归约成 UI 状态（会话 id 与操作错误也在
 *  这里——单一事实源，消灭 App 的并行订阅）。
 * resolve：确认裁决后把对应卡片定格（allow/deny 徽标）——裁决是本地
 * UI 状态（后端事件流没有"卡片已裁决"事件，toolResult 才是回执）。
 * send/resolve 身份稳定：下游 React.memo(Block) 依赖 onConfirm 稳定。 */
export function useAgent(source: AgentSource): {
  state: UIState;
  send: AgentSource["send"];
  resolve: (id: string, outcome: "allow" | "deny") => void;
  /** 关闭一次性错误提示。 */
  clearError: () => void;
  /** 请求类失败进一次性提示（同一归约域——App/确认流的 catch 调这里）。 */
  reportError: (message: string) => void;
} {
  const [state, setState] = useState<UIState>(initial);
  useEffect(() => {
    setState(initial);
    return source.subscribe((ev) => setState((s) => reduce(s, ev)));
  }, [source]);
  const send = useCallback((t: string, opts?: SendOptions) => source.send(t, opts), [source]);
  const resolve = useCallback((id: string, outcome: "allow" | "deny") => {
    setState((s) => resolveConfirm(s, id, outcome));
  }, []);
  const clearError = useCallback(() => setState((s) => (s.operationError ? { ...s, operationError: null } : s)), []);
  const reportError = useCallback((message: string) => setState((s) => ({ ...s, operationError: message })), []);
  return { state, send, resolve, clearError, reportError };
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
