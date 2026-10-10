// 发送缓冲区（体验修复批次 5）：busy 时 chat.send 会被后端拒收（ErrBusy，
// 消息不入历史），而 Composer 先清空输入框——被拒的文本就丢了。缓冲区把
// busy 期间提交的文本**排进队列**，等会话空闲（busy true→false）再逐条发出。
//
// 独立于 reduce 流的原因：归约流是「事件重放安全」的（同一条事件重复归约
// 幂等），而队列是**用户操作态**——入队/删除/置顶是本地动作，不是后端事实，
// 混进归约会被事件重放或会话切换搅乱。所以它按 sessionId 键独立存放，
// 生命周期 = 应用会话（页面刷新丢失，可接受：没发出去的话本来也没落库）。
//
// 本文件全部是纯函数（无 React、无 I/O）——队列的每一步变换都可单测。

import type { ChatSendFile, ChatSendImage } from "./types";

/** 队列条目：本地生成的 id（删除/置顶/直接发送的寻址凭据）+ 原文 + 附件。
 *
 *  附件（图片批次 B）：busy 时带图/带文件的消息入队必须**携带附件**，否则
 *  自动发出时附件就丢了。images/files 与 SendOptions 同形，且**引用同一数组**
 *  ——base64 在队列里是必须的（发送要用），但绝不复制多份。 */
export interface SendQueueItem {
  id: string;
  text: string;
  images?: ChatSendImage[];
  files?: ChatSendFile[];
}

/** 本地 id：单调计数器（同毫秒多次入队也不撞；不引 crypto 依赖）。 */
let queueSeq = 0;
export function newQueueId(): string {
  queueSeq += 1;
  return `q-${Date.now().toString(36)}-${queueSeq}`;
}

/** 入队（追加到尾部——先提交的先发）。附件按引用携带（不复制）。 */
export function enqueueSend(
  queue: SendQueueItem[],
  id: string,
  text: string,
  atts?: { images?: ChatSendImage[]; files?: ChatSendFile[] },
): SendQueueItem[] {
  const item: SendQueueItem = { id, text };
  if (atts?.images?.length) item.images = atts.images;
  if (atts?.files?.length) item.files = atts.files;
  return [...queue, item];
}

/** 从队列移除一条（删除 / 编辑取出 / 发出后的清理共用）。没有该 id 时原样返回。 */
export function removeQueuedSend(queue: SendQueueItem[], id: string): SendQueueItem[] {
  const next = queue.filter((item) => item.id !== id);
  return next.length === queue.length ? queue : next;
}

/** 把一条移到队首（「直接发送」在忙时按下：本轮结束后第一个发它）。没有该 id 时原样返回。 */
export function moveQueuedToFront(queue: SendQueueItem[], id: string): SendQueueItem[] {
  const item = queue.find((entry) => entry.id === id);
  if (!item) return queue;
  return [item, ...removeQueuedSend(queue, id)];
}

/** 队首（下一条该发的）；空队列返回 null。 */
export function queuedHead(queue: SendQueueItem[] | undefined): SendQueueItem | null {
  return queue && queue.length > 0 ? queue[0] : null;
}

// ---- 两处 UI 决策门（纯函数——Composer 的 submit 与队列的「直接发送」都驱动
// 真实现，测试直接驱动同一份判定，不复刻副本）----

/** Composer submit 的去向：send = 正常发送路径（乐观气泡照常）；queue = 入发送
 *  缓冲区（busy/sending 期间后端会拒收 chat.send）；none = 不发（空文本/锁定视图/
 *  斜杠命令——命令由面板接管，不进对话流）。hasQueue = 调用方接了缓冲区
 *  （onQueue 传了）；没接的调用方保持旧语义：忙时不发。
 *  hasAttachments = 输入框里挂了附件（图片批次 B）：只附件没文本也允许发送
 *  （后端把附件落盘并以「[附件]」行充当消息正文）——锁定与斜杠命令的门不变。 */
export type SubmitRoute = "send" | "queue" | "none";
export function routeSubmit(opts: {
  text: string;
  busy: boolean;
  sending: boolean;
  locked: boolean;
  hasQueue: boolean;
  hasAttachments?: boolean;
}): SubmitRoute {
  if ((!opts.text && !opts.hasAttachments) || opts.locked) return "none";
  if (opts.text.startsWith("/")) return "none";
  if (opts.busy || opts.sending) return opts.hasQueue ? "queue" : "none";
  return "send";
}

/** 队列条目「直接发送」的去向：send-now = 会话空闲，立即按正常路径发送；
 *  to-front = 忙（或发送中——sending 期间后端同样拒收），把该条置顶等本轮结束。 */
export function queueSendAction(opts: { busy: boolean; sending: boolean }): "send-now" | "to-front" {
  return opts.busy || opts.sending ? "to-front" : "send-now";
}

/** 自动发送的**触发判定**（store 订阅层调用——绝不在归约器里做副作用）。
 *
 *  触发条件全部满足才发：
 *  - 这是一条 busy 事件（busy 翻转是「可以发下一条」的权威信号——done/error
 *    与 busy=false 可能同 tick，但只有 busy=false 事件被这里消费，天然幂等）；
 *  - 该会话 busy 由 true → false（false → false 是重播/重复事件，不是轮次边界）；
 *  - 事件归属的会话就是当前活跃会话（非活跃会话的队列保持挂起，等它被
 *    选中且空闲时再发——见 store 的 sessionFocused 分支）；
 *  - 队列非空。
 *
 *  返回该发出的队首条目（调用方负责发出并把它从队列移除——发出即移除，
 *  重复触发时空队列无副作用）；返回 null = 不触发。 */
export function autoSendPick(
  prevBusy: boolean | undefined,
  busy: boolean,
  sessionId: string,
  currentId: string,
  queue: SendQueueItem[] | undefined,
): SendQueueItem | null {
  if (prevBusy !== true || busy !== false) return null;
  if (sessionId !== currentId) return null;
  return queuedHead(queue);
}
