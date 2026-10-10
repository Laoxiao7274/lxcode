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
// 两份**独立于归约流**的会话级状态也挂在这里（都是键值表，事件只读不写、
// 用户操作只写不读，互不干扰）：
// - childParents：子→父会话映射（子会话标签按需显示 + 标题反查，见
//   workspace-tabs.ts）；数据源 = dispatchStart 事件自带的 owner_session_id，
//   以及 historyLoaded 回放时对 blocks 的一次性扫描；
// - sendQueues：发送缓冲区（busy 时 chat.send 被后端拒收，排队等空闲再发，
//   见 send-queue.ts）。自动发送的挂点就在 handleAgentEvent 的 busy 分支——
//   **绝不在归约器里做副作用**。
import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentEvent, AgentSource, SendAttachments, SendOptions } from "./types";
import { type UIState, initial } from "./blocks";
import { reduceHistory } from "./history";
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

/** 事件处理的进出状态口。React 侧的写口**同步刷新 ref**（同一 tick 内连续事件
 *  读到最新值——自动发送逐条发出就靠这个：队首移除后立刻可见）；测试侧用
 *  可变对象直填（get 返回可变对象本身、set 原地覆盖字段）。 */
export interface AgentEventController {
  getCurrentId(): string;
  getSessionStates(): Record<string, UIState>;
  getChildLinks(): ChildLinks;
  getSendQueues(): Record<string, SendQueueItem[]>;
  getPrevBusy(): Record<string, boolean>;
  setCurrentId(id: string): void;
  setSessionStates(next: Record<string, UIState>): void;
  setOperationError(message: string): void;
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
    // 事件侧没有映射可记——从回放重建的 blocks 里一次性扫出该会话派发的
    // 子会话（dispatch 块的 sessionId 就是子会话 id）。只在回放时跑，
    // 不是逐事件扫描。
    const rebuilt = reduceHistory({ ...initial, currentId: ev.sessionId }, ev.history);
    const scanned = childLinksFromBlocks(ev.sessionId, rebuilt.blocks);
    let next = c.getChildLinks();
    for (const [childId, parentId] of Object.entries(scanned.childParents)) {
      next = recordChildLink(next, { childId, parentId });
    }
    for (const [did, childId] of Object.entries(scanned.dispatchChild)) {
      next = recordChildLink(next, { childId, parentId: next.childParents[childId] ?? "", dispatchId: did });
    }
    c.setChildLinks(next);
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
      const head = autoSendPick(prev, ev.busy, ev.sessionId, c.getCurrentId(), c.getSendQueues()[ev.sessionId]);
      if (head) drainQueueHead(c, ev.sessionId);
    }
    c.setSessionStates(reduceSessionStates(c.getSessionStates(), ev, c.getChildLinks().dispatchChild));
    return;
  }
  if ("sessionId" in ev) c.setSessionStates(reduceSessionStates(c.getSessionStates(), ev, c.getChildLinks().dispatchChild));
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
  /** 子会话 id → 父会话 id（子会话标签按需显示与标题反查的唯一事实源）。 */
  childParents: ChildParents;
  /** 发送缓冲区（按会话隔离；busy 期间提交的文本在这里排队）。 */
  sendQueues: Record<string, SendQueueItem[]>;
  send: AgentSource["send"];
  resolve: (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;
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
  const [currentId, setCurrentId] = useState("");
  const [operationError, setOperationError] = useState<string | null>(null);
  const [childLinks, setChildLinks] = useState<ChildLinks>(emptyChildLinks);
  const [sendQueues, setSendQueues] = useState<Record<string, SendQueueItem[]>>({});
  const [, setRevision] = useState(0);

  // ref 镜像：controller 的写口同步刷 ref（同一 tick 内连续事件读到最新值），
  // 渲染期再同步一次（兜住 React 侧其他 setState 路径——如 resolve 的函数式更新）。
  const sessionStatesRef = useRef(sessionStates);
  const currentIdRef = useRef(currentId);
  const childLinksRef = useRef(childLinks);
  const sendQueuesRef = useRef(sendQueues);
  const prevBusyRef = useRef<Record<string, boolean>>({});
  sessionStatesRef.current = sessionStates;
  currentIdRef.current = currentId;
  childLinksRef.current = childLinks;
  sendQueuesRef.current = sendQueues;
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
      getChildLinks: () => childLinksRef.current,
      getSendQueues: () => sendQueuesRef.current,
      getPrevBusy: () => prevBusyRef.current,
      setCurrentId: (id) => { currentIdRef.current = id; setCurrentId(id); },
      setSessionStates: (next) => { sessionStatesRef.current = next; setSessionStates(next); },
      setOperationError: (message) => setOperationError(message),
      bumpRevision: () => setRevision((n) => n + 1),
      setChildLinks: (next) => { childLinksRef.current = next; setChildLinks(next); },
      setSendQueues: (next) => { sendQueuesRef.current = next; setSendQueues(next); },
      setPrevBusy: (next) => { prevBusyRef.current = next; },
      autoSend: (sessionId, text, atts) => autoSendRef.current?.(sessionId, text, atts),
      sendFailed: (sessionId, atts) => sendFailedRef.current?.(sessionId, atts),
      reset: () => {
        controllerRef.current?.setSessionStates({});
        controllerRef.current?.setCurrentId("");
        setOperationError(null);
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
    childParents: childLinks.childParents,
    sendQueues,
    send,
    resolve,
    clearError,
    reportError,
    enqueueSend,
    removeQueuedSend,
    moveQueuedToFront,
  };
}
