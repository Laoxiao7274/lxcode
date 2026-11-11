// useAgent：订阅 AgentSource 并归约成 UI 状态（会话 id 与操作错误也在这里——
// 单一事实源，消灭 App 的并行订阅）。归约逻辑已按职责拆到 blocks.ts /
// history.ts / reduce.ts；这里只留 React 绑定，并把公共名字重新导出，调用方
// （组件与测试）的 import 路径不变。
//
// resolve：确认裁决后把对应卡片定格（allow/deny 徽标）——裁决是本地 UI 状态
//（后端事件流没有「卡片已裁决」事件，toolResult 才是回执）。
// send/resolve 身份稳定：下游 React.memo(Block) 依赖 onConfirm 稳定。
//
// 事件处理本体在 handleAgentEvent（导出供测试直接驱动——驱动的是真实现，
// 复刻副本的断言在真实现漂移时不会红）。它经 AgentEventController 进出状态：
// React 侧把 setState 包装成 controller（写口同步刷新 ref——同一 tick 内
// 连续事件读到的是最新值），测试侧用可变对象直填。
//
// operationError 之外还维护 operationErrors 环形缓冲（最近 50 条带时间戳，
// 2026-12 桌面错误日志查看器）：设置页「日志」分区的数据源——提示条是
// 一次性提示（从末条取，行为不变），环形缓冲让用户回头翻看连续失败的历史。
// **所有写点收敛在 controller 的 setOperationError 一个口**（事件侧、
// reportError、失败回滚共用它），缓冲不会漏记。
//
// 两份**独立于归约流**的会话级状态也挂在这里（都是键值表，事件只读不写、
// 用户操作只写不读，互不干扰）：
// - childParents：子→父会话映射（子会话视图的标题反查 + 双投路由，见
//   workspace-tabs.ts）；数据源 = dispatchStart 事件自带的 owner_session_id，
//   以及 historyLoaded 回放时对 blocks 的一次性扫描；
// - sendQueues：发送缓冲区（busy 时 chat.send 被后端拒收，排队等空闲再发，
//   见 send-queue.ts）。自动发送的挂点就在 handleAgentEvent 的 busy 分支——
//   **绝不在归约器里做副作用**。
import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentEvent, AgentSource, SendAttachments, SendOptions } from "./types";
import { type UIState, initial } from "./blocks";
import { reduceSessionStates, allowAllPendingConfirms, resolveConfirm, resolveConfirmEverywhere } from "./reduce";
import { childLinksFromBlocks, emptyChildLinks, recordChildLink, type ChildLinks, type ChildParents } from "./workspace-tabs";
import {
  autoSendPick,
  enqueueSend as enqueueSendItem,
  moveQueuedToFront as moveQueuedToFrontItem,
  newQueueId,
  removeQueuedSend as removeQueuedSendItem,
  type SendQueueItem,
} from "./send-queue";
import type { ChatSendFile, ChatSendImage } from "./types";

export type { AssistantBlock, ThreadBlock, UIState } from "./blocks";
export { checkpointBody } from "./blocks";
export { reduce, reduceSessionStates, pendingConfirmIds, allowAllPendingConfirms, resolveConfirm, resolveConfirmEverywhere } from "./reduce";
export type { ChildLinks, ChildParents } from "./workspace-tabs";
export type { SendQueueItem } from "./send-queue";

/** 一条操作错误的历史记录（operationErrors 环形缓冲的条目）。
 *  at 是发生时刻（ISO 字符串——设置页「日志」分区按它展示时间）。 */
export interface OperationErrorEntry {
  message: string;
  at: string;
}

/** operationErrors 环形缓冲的容量（最近 50 条——设置页翻得动的量）。 */
export const OPERATION_ERRORS_MAX = 50;

/** 追加一条操作错误进环形缓冲（纯函数——写点收敛与测试共用这一份逻辑）。
 *  超 50 条丢最旧（末条永远最新，提示条从末条取的行为不受影响）。 */
export function pushOperationError(buf: OperationErrorEntry[], message: string, at = new Date().toISOString()): OperationErrorEntry[] {
  const next = [...buf, { message, at }];
  return next.length > OPERATION_ERRORS_MAX ? next.slice(next.length - OPERATION_ERRORS_MAX) : next;
}

/** 事件处理的进出状态口。React 侧的写口**同步刷新 ref**（同一 tick 内连续事件
 *  读到最新值——自动发送逐条发出就靠这个：队首移除后立刻可见）；测试侧用
 *  可变对象直填（get 返回可变对象本身、set 原地覆盖字段）。 */
export interface AgentEventController {
  getCurrentId(): string;
  getSessionStates(): Record<string, UIState>;
  /** 助手域的会话状态表（assistant.* 事件归约的目标 map——与主域空间隔离，
   *  reducer 复用同一份 reduceSessionStates）。 */
  getAssistantStates(): Record<string, UIState>;
  getChildLinks(): ChildLinks;
  getSendQueues(): Record<string, SendQueueItem[]>;
  getPrevBusy(): Record<string, boolean>;
  setCurrentId(id: string): void;
  setSessionStates(next: Record<string, UIState>): void;
  setAssistantStates(next: Record<string, UIState>): void;
  /** 报一条操作错误（**所有写点的收敛口**）：提示条照旧置末条，同时追加进
   *  operationErrors 环形缓冲（设置页「日志」分区读它）。 */
  setOperationError(message: string): void;
  /** 读取环形缓冲（同一 tick 内连续事件读到最新值——ref 镜像同款纪律）。 */
  getOperationErrors(): OperationErrorEntry[];
  bumpRevision(): void;
  setChildLinks(next: ChildLinks): void;
  setSendQueues(next: Record<string, SendQueueItem[]>): void;
  setPrevBusy(next: Record<string, boolean>): void;
  /** 缓冲区自动发送的出口（App 的 sendWithOptions 经 ref 桥接）。
   *  atts = 队首条目携带的附件（按引用透传——不复制 base64）。 */
  autoSend(sessionId: string, text: string, atts?: SendAttachments): void;
  /** 发送失败时把附件装回输入框附件区的出口（App 的 onSendFailed 经 ref 桥接）：
   *  文本丢了附件不能丢——用户改完重发不该重新选一遍文件。 */
  sendFailed(sessionId: string, atts: SendAttachments): void;
  /** 重挂订阅（source 变化）时清运行态——与旧版 useEffect 的清理语义一致。 */
  reset(): void;
}

/** 从缓冲区发出队首：先移除（发出即移除——重复触发空队列无副作用），再喊出口。
 *
 *  发送被拒（后端 busy/断连/超时）时：队首已移除、剩余队列保留、busy 维持
 *  false（不再有 true→false 的轮次边界）→ 自动发送就此停止，剩余队列等
 *  用户手动处理（直接发送/编辑/删除都可用）。 */
export function drainQueueHead(c: AgentEventController, sessionId: string): void {
  const queue = c.getSendQueues()[sessionId];
  if (!queue || queue.length === 0) return;
  const head = queue[0];
  c.setSendQueues({ ...c.getSendQueues(), [sessionId]: removeQueuedSendItem(queue, head.id) });
  // 附件按引用透传（不复制 base64——队列条目与发送参数共享同一数组）
  const atts: SendAttachments | undefined =
    head.images?.length || head.files?.length
      ? { images: head.images ?? [], files: head.files ?? [] }
      : undefined;
  c.autoSend(sessionId, head.text, atts);
}

/** store 订阅回调的本体（每条服务端事件走一次；无 React、无 I/O——可单测）。 */
export function handleAgentEvent(ev: AgentEvent, c: AgentEventController): void {
  // 助手域分流（助手模式 P1）：assistant.* 事件经 mapEvent 归一成 chat 同构形状
  // 后打上 space 标记——归约进**独立的** assistantStates（reducer 复用同一份
  // reduceSessionStates 与同一份 dispatchChild 映射），绝不进主域 sessionStates
  //（两域的会话 id 空间隔离，混流会让侧栏/主时间线被助手会话污染）。
  // 断言说明：AgentEvent 各变体没有逐个声明 space 键（机械地给 20 个变体加可选键
  // 的噪声远大于收益）——标记只在 events.ts 的 assistant 分支写入，运行时判定一处。
  if ((ev as { space?: string }).space === "assistant") {
    handleAssistantEvent(ev, c);
    return;
  }
  if (ev.type === "sessionFocused") {
    c.setCurrentId(ev.id);
    const all = c.getSessionStates();
    const existed = Boolean(all[ev.id]);
    if (!existed) c.setSessionStates({ ...all, [ev.id]: { ...initial, currentId: ev.id } });
    // 切到有缓冲的会话：**切换前 state 就已存在**且明确报空闲才接着发——
    // state 不存在（刚建的默认态忙闲恒是 false）= 忙闲未知，后端可能正在跑，
    // 保守挂起，等 busy 事件或用户手动发。
    if (existed && !all[ev.id].busy && !all[ev.id].sending) drainQueueHead(c, ev.id);
    return;
  }
  if (ev.type === "operationError") {
    c.setOperationError(ev.message);
    return;
  }
  // 发送失败的附件回滚（图片批次 B）：failed 变体带着原附件——装回输入框
  // 附件区，用户改完重发不用重新选文件（文本本来就不回填，附件必须回）。
  if (ev.type === "optimisticUser" && ev.failed && ev.atts && (ev.atts.images.length > 0 || ev.atts.files.length > 0)) {
    c.sendFailed(ev.sessionId, ev.atts);
  }
  if (ev.type === "sessionsChanged" || ev.type === "projectsChanged" || ev.type === "sessionChanged" || ev.type === "ready") {
    c.bumpRevision();
    return;
  }
  // ---- 子→父映射的记录（在归约之前——双投归属查表要用它）----
  if (ev.type === "dispatchStart" && ev.childSessionId) {
    // dispatchStart 自带归属：sessionId 就是 owner_session_id（父），
    // childSessionId 是子。这是映射最干净的数据源，不用反查任何块。
    c.setChildLinks(recordChildLink(c.getChildLinks(), {
      childId: ev.childSessionId, parentId: ev.sessionId, dispatchId: ev.dispatchId,
    }));
  }
  if (ev.type === "historyLoaded") {
    // 历史回放路径（刷新/冷恢复/切回老会话）：dispatchStart 早于本次连接，
    // 事件侧没有映射可记——从重建的 blocks 里一次性扫出该会话派发的
    // 子会话（dispatch 块的 sessionId 就是子会话 id）。只在回放时跑，
    // 不是逐事件扫描。
    //
    // **归约先行，扫描消费归约产出的 blocks**（2026-12 会话切换优化批 1）：
    // 原实现在这里额外跑一遍 reduceHistory 只为扫接链——两次 O(n)，大会话
    // 装载多一份整时间线的复制。blocks 是不可变共享的（后续事件只 slice /
    // 换对象，绝不改写块对象），扫描直接拿归约结果安全。归约在前、扫接链在后
    // 对语义无影响：historyLoaded 不带 dispatchId，双投路由用不到本次扫出的
    // 映射；扫描范围（含分页前插与 running_dispatches 补卡后的终态 blocks）
    // 是旧扫描范围（纯快照重建）的超集，recordChildLink 幂等——记录的事实一致。
    // 本分支**自己归约并提前 return**：落到底部通用归约会把重建再跑一遍
    //（两份 UIState 各自 rebuiltGen 递增——恰好是要消灭的那份重复）。
    c.setSessionStates(reduceSessionStates(c.getSessionStates(), ev, c.getChildLinks().dispatchChild));
    const rebuilt = c.getSessionStates()[ev.sessionId]?.blocks ?? [];
    // 回放的 dispatch 卡带两把键（子会话 id + 调用 id）——childLinksFromBlocks
    // 一次扫出两张映射，这里全量记（与旧实现逐条 recordChildLink 等价）。
    const scanned = childLinksFromBlocks(ev.sessionId, rebuilt);
    let next = c.getChildLinks();
    for (const [childId, parentId] of Object.entries(scanned.childParents)) {
      next = recordChildLink(next, { childId, parentId });
    }
    for (const [did, childId] of Object.entries(scanned.dispatchChild)) {
      next = recordChildLink(next, { childId, parentId: next.childParents[childId] ?? "", dispatchId: did });
    }
    // **运行中派发快照**（running_dispatches）是回放扫不出来的那部分的补集：
    // 正在跑的派发在历史里没有结果提示行、（回放窗口被截时）也可能没有卡——
    // 后端按此刻的运行中登记导出，dispatch_id → 子会话 id 直接记进映射，
    // 后续带归属的子事件按它路由（「进度消失」的修复）。
    for (const d of ev.history.runningDispatches ?? []) {
      if (!d.childSessionId) continue;
      next = recordChildLink(next, {
        childId: d.childSessionId, parentId: ev.sessionId, dispatchId: d.dispatchId,
      });
    }
    c.setChildLinks(next);
    return;
  }
  // ---- 发送缓冲区的自动发送（busy true→false 的轮次边界）----
  if (ev.type === "busy") {
    // 带归属的 busy = 子会话的忙闲：**不记 prevBusy、不触发自动发送**——
    // 子会话跑完不代表主会话空闲（主会话此刻多半还在等 dispatch 结论），
    // 误触发会把队首那条在主会话忙时发出去、被后端以 busy 拒收。子会话自己的
    // 归约照走（双投，下面那行）——只是边界检测这一份跳过。
    if (!ev.dispatchId) {
      const prev = c.getPrevBusy()[ev.sessionId];
      c.setPrevBusy({ ...c.getPrevBusy(), [ev.sessionId]: ev.busy });
      // done/error 与 busy=false 可能同 tick——这里只消费 busy 事件，且只有
      // true→false 的翻转才算边界（false→false 是重播/重复事件）；队首发出
      // 即移除，重复触发空队列无副作用（幂等）。
      const head = autoSendPick(prev, ev.busy, c.getSendQueues()[ev.sessionId]);
      if (head) drainQueueHead(c, ev.sessionId);
    }
    c.setSessionStates(reduceSessionStates(c.getSessionStates(), ev, c.getChildLinks().dispatchChild));
    return;
  }
  if ("sessionId" in ev) c.setSessionStates(reduceSessionStates(c.getSessionStates(), ev, c.getChildLinks().dispatchChild));
}

/** 助手域事件处理（assistant.*，space 标记已由 events.ts 打上）。
 *
 * 与主域 handleAgentEvent 的差别只有三点，其余语义**逐条对齐**：
 * ① 归约目标是 assistantStates（独立 map，同一份 reducer）；
 * ② 没有 sessionFocused / 发送缓冲区（助手页签自己持有当前会话 id，
 *    assistant.send 显式寻址——不存在「连接焦点」这个概念）；
 * ③ dispatchStart 同样记 childLinks（run_task 子会话与主域子会话共用同一张
 *    子→父映射——child:<id> 临时视图的反查与双投路由都要用）。 */
function handleAssistantEvent(ev: AgentEvent, c: AgentEventController): void {
  if (ev.type === "dispatchStart" && ev.childSessionId) {
    c.setChildLinks(recordChildLink(c.getChildLinks(), {
      childId: ev.childSessionId, parentId: ev.sessionId, dispatchId: ev.dispatchId,
    }));
  }
  if (ev.type === "historyLoaded") {
    // 助手会话/助手子会话的历史回放：子→父映射同主域一条纪律——**归约先行，
    // 扫描消费归约产出的 blocks**（两域同一条注释、同一份理由，见
    // handleAgentEvent 的 historyLoaded 分支）。回放的 blocks 属于助手域
    // state——映射记录本身是跨域索引（childLinks），照常进同一张表。
    c.setAssistantStates(reduceSessionStates(c.getAssistantStates(), ev, c.getChildLinks().dispatchChild));
    const rebuilt = c.getAssistantStates()[ev.sessionId]?.blocks ?? [];
    const scanned = childLinksFromBlocks(ev.sessionId, rebuilt);
    let next = c.getChildLinks();
    for (const [childId, parentId] of Object.entries(scanned.childParents)) {
      next = recordChildLink(next, { childId, parentId });
    }
    for (const [did, childId] of Object.entries(scanned.dispatchChild)) {
      next = recordChildLink(next, { childId, parentId: next.childParents[childId] ?? "", dispatchId: did });
    }
    for (const d of ev.history.runningDispatches ?? []) {
      if (!d.childSessionId) continue;
      next = recordChildLink(next, {
        childId: d.childSessionId, parentId: ev.sessionId, dispatchId: d.dispatchId,
      });
    }
    if (next !== c.getChildLinks()) c.setChildLinks(next);
    return;
  }
  c.setAssistantStates(reduceSessionStates(c.getAssistantStates(), ev, c.getChildLinks().dispatchChild));
}

/** useAgent 的旁路回调。onAutoSend：缓冲区自动发送的出口——store 判定「该发了」
 *  之后只负责喊一声，真正怎么发（带哪些请求级参数）由调用方（App 的
 *  sendWithOptions）决定，store 不碰发送配置。onSendFailed：发送失败时把附件
 *  装回输入框附件区（文本丢了附件不能丢）。 */
export interface AgentHooks {
  onAutoSend?: (sessionId: string, text: string, atts?: SendAttachments) => void;
  onSendFailed?: (sessionId: string, atts: SendAttachments) => void;
}

export function useAgent(source: AgentSource, hooks?: AgentHooks): {
  state: UIState;
  sessionStates: Record<string, UIState>;
  /** 助手域的会话状态表（assistant.* 事件的归约目标——与主域空间隔离）。 */
  assistantStates: Record<string, UIState>;
  /** 子会话 id → 父会话 id（子会话视图标题反查与双投路由的唯一事实源）。 */
  childParents: ChildParents;
  /** 发送缓冲区（按会话隔离；busy 期间提交的文本在这里排队）。 */
  sendQueues: Record<string, SendQueueItem[]>;
  send: AgentSource["send"];
  resolve: (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;
  /** 助手域版本的 resolve（两处定格落在 assistantStates——助手会话与它的
   *  run_task 子会话的确认卡都在助手域）。 */
  resolveAssistant: (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;
  /** 操作错误环形缓冲（最近 50 条带时间戳——设置页「日志」分区的数据源；
   *  提示条从 state.operationError 末条取，行为不变）。 */
  operationErrors: OperationErrorEntry[];
  clearError: () => void;
  reportError: (message: string) => void;
  /** 入队一条缓冲消息（busy/sending 期间 Composer 的 submit 走这里）。
   *  atts = 随消息的附件（按引用入队——发送时要用，不复制 base64）。 */
  enqueueSend: (sessionId: string, text: string, atts?: { images?: ChatSendImage[]; files?: ChatSendFile[] }) => void;
  /** 从缓冲区移除一条（删除 / 编辑取出共用）。 */
  removeQueuedSend: (sessionId: string, id: string) => void;
  /** 把一条移到队首（「直接发送」在忙时按下：本轮结束后第一个发它）。 */
  moveQueuedToFront: (sessionId: string, id: string) => void;
} {
  const [sessionStates, setSessionStates] = useState<Record<string, UIState>>({});
  const [assistantStates, setAssistantStates] = useState<Record<string, UIState>>({});
  const [currentId, setCurrentId] = useState("");
  const [operationError, setOperationError] = useState<string | null>(null);
  const [operationErrors, setOperationErrors] = useState<OperationErrorEntry[]>([]);
  const [childLinks, setChildLinks] = useState<ChildLinks>(emptyChildLinks);
  const [sendQueues, setSendQueues] = useState<Record<string, SendQueueItem[]>>({});
  const [, setRevision] = useState(0);

  // ref 镜像：controller 的写口同步刷 ref（同一 tick 内连续事件读到最新值），
  // 渲染期再同步一次（兜住 React 侧其他 setState 路径——如 resolve 的函数式更新）。
  const sessionStatesRef = useRef(sessionStates);
  const assistantStatesRef = useRef(assistantStates);
  const currentIdRef = useRef(currentId);
  const childLinksRef = useRef(childLinks);
  const sendQueuesRef = useRef(sendQueues);
  const prevBusyRef = useRef<Record<string, boolean>>({});
  const operationErrorsRef = useRef(operationErrors);
  sessionStatesRef.current = sessionStates;
  assistantStatesRef.current = assistantStates;
  currentIdRef.current = currentId;
  childLinksRef.current = childLinks;
  sendQueuesRef.current = sendQueues;
  operationErrorsRef.current = operationErrors;
  /** 自动发送出口（App 的 sendWithOptions）：身份随设置变化，经 ref 进 controller。 */
  const autoSendRef = useRef(hooks?.onAutoSend);
  autoSendRef.current = hooks?.onAutoSend;
  /** 发送失败附件回滚出口（App 经 ref 桥接）。 */
  const sendFailedRef = useRef(hooks?.onSendFailed);
  sendFailedRef.current = hooks?.onSendFailed;

  // controller 只建一次：setters（useState 的）与 refs 都身份稳定。
  const controllerRef = useRef<AgentEventController | null>(null);
  if (controllerRef.current === null) {
    controllerRef.current = {
      getCurrentId: () => currentIdRef.current,
      getSessionStates: () => sessionStatesRef.current,
      getAssistantStates: () => assistantStatesRef.current,
      getChildLinks: () => childLinksRef.current,
      getSendQueues: () => sendQueuesRef.current,
      getPrevBusy: () => prevBusyRef.current,
      setCurrentId: (id) => { currentIdRef.current = id; setCurrentId(id); },
      setSessionStates: (next) => { sessionStatesRef.current = next; setSessionStates(next); },
      setAssistantStates: (next) => { assistantStatesRef.current = next; setAssistantStates(next); },
      setOperationError: (message) => {
        // 写点收敛（2026-12 错误日志查看器）：提示条置末条 + 环形缓冲追加，
        // 同一 tick 内连续事件读 ref——写口先刷 ref 再 setState。
        operationErrorsRef.current = pushOperationError(operationErrorsRef.current, message);
        setOperationErrors(operationErrorsRef.current);
        setOperationError(message);
      },
      getOperationErrors: () => operationErrorsRef.current,
      bumpRevision: () => setRevision((n) => n + 1),
      setChildLinks: (next) => { childLinksRef.current = next; setChildLinks(next); },
      setSendQueues: (next) => { sendQueuesRef.current = next; setSendQueues(next); },
      setPrevBusy: (next) => { prevBusyRef.current = next; },
      autoSend: (sessionId, text, atts) => autoSendRef.current?.(sessionId, text, atts),
      sendFailed: (sessionId, atts) => sendFailedRef.current?.(sessionId, atts),
      reset: () => {
        controllerRef.current?.setSessionStates({});
        controllerRef.current?.setAssistantStates({});
        controllerRef.current?.setCurrentId("");
        setOperationError(null);
        // 重挂订阅（source 变化）清运行态：环形缓冲一并清——缓冲是本连接的
        // 运行态（换后端 = 旧后端的错误历史作废），与 operationError 同语义。
        operationErrorsRef.current = [];
        setOperationErrors([]);
        controllerRef.current?.setPrevBusy({});
      },
    };
  }
  const controller = controllerRef.current;

  useEffect(() => {
    controller.reset();
    return source.subscribe((ev) => handleAgentEvent(ev, controller));
  }, [source, controller]);

  const state = {
    ...(sessionStates[currentId] ?? initial),
    currentId,
    operationError,
  };
  const send = useCallback((sessionId: string, text: string, opts?: SendOptions) => source.send(sessionId, text, opts), [source]);
  const resolve = useCallback((sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => {
    // 裁决要**两处同时定格**：确认在父会话的卡里与子会话自己的时间线里各有一份
    //（子事件双投的必然结果）——只定格用户点的那一处，另一处会永远挂着「待确认」。
    // 先补上被裁决的那个会话（它可能还没有 state——确认卡在 blocks 里但 state 未建）。
    // answer 只在 ask 提问的回答时有值（已决卡上的答案摘要）。
    setSessionStates((all) => {
      const base = all[sessionId] ? all : { ...all, [sessionId]: { ...initial, currentId: sessionId } };
      return resolveConfirmEverywhere(base, id, outcome, answer);
    });
  }, []);
  // 助手域版本：确认 id 在助手域可能是跨会话的（父会话卡 + 子会话双投 state 各
  // 一份）——resolveConfirmEverywhere 按持有者逐个定格，跨会话两处同时收口。
  const resolveAssistant = useCallback((sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => {
    void sessionId; // 参数保留与主域 resolve 同形（调用点一致）；定格按持有者扫全表
    controller.setAssistantStates(resolveConfirmEverywhere(controller.getAssistantStates(), id, outcome, answer));
  }, [controller]);
  const clearError = useCallback(() => setOperationError(null), []);
  const reportError = useCallback((message: string) => setOperationError(message), []);
  const enqueueSend = useCallback((sessionId: string, text: string, atts?: { images?: ChatSendImage[]; files?: ChatSendFile[] }) => {
    if (!sessionId || (!text && !(atts?.images?.length || atts?.files?.length))) return;
    controller.setSendQueues({
      ...controller.getSendQueues(),
      [sessionId]: enqueueSendItem(controller.getSendQueues()[sessionId] ?? [], newQueueId(), text, atts),
    });
  }, [controller]);
  const removeQueuedSend = useCallback((sessionId: string, id: string) => {
    controller.setSendQueues({
      ...controller.getSendQueues(),
      [sessionId]: removeQueuedSendItem(controller.getSendQueues()[sessionId] ?? [], id),
    });
  }, [controller]);
  const moveQueuedToFront = useCallback((sessionId: string, id: string) => {
    controller.setSendQueues({
      ...controller.getSendQueues(),
      [sessionId]: moveQueuedToFrontItem(controller.getSendQueues()[sessionId] ?? [], id),
    });
  }, [controller]);
  return {
    state,
    sessionStates,
    assistantStates,
    childParents: childLinks.childParents,
    sendQueues,
    send,
    resolve,
    resolveAssistant,
    operationErrors,
    clearError,
    reportError,
    enqueueSend,
    removeQueuedSend,
    moveQueuedToFront,
  };
}
