// 发送缓冲区（体验修复批次 5）：busy 时提交的消息在这里排队。
// 审查 M5 前端重构：从 App.tsx 纯移动，语义零改动。
//
// submit 入队走 Composer 的 onQueue（→ enqueueSend，仍在 AppBody——它直通
// store）；这里管队列条目的三个动作与一次性提示：
// 编辑 = 取出回填输入框（改完再发，或再入队）；删除 = 移除；直接发送 =
// 空闲立即按正常路径发（乐观气泡照常），忙时把该条置顶等本轮结束。
import { useCallback, useRef, useState } from "react";
import { queueSendAction, type SendQueueItem } from "../shared/send-queue";
import type { SendAttachments } from "../shared/types";

/** 队列条目的附件形态（与发送参数同构——按引用共享，不复制 base64）。 */
function attsOf(item: SendQueueItem): SendAttachments | undefined {
  return item.images?.length || item.files?.length
    ? { images: item.images ?? [], files: item.files ?? [] }
    : undefined;
}

/** 发送缓冲区条目的三个动作（编辑/删除/直接发送）+ 一次性提示。 */
export function useSendQueue({ sendQueues, currentId, removeQueuedSend, moveQueuedToFront, injectDraft, sendWithOptions, busy, sending }: {
  sendQueues: Record<string, SendQueueItem[]>;
  currentId: string;
  removeQueuedSend: (sessionId: string, id: string) => void;
  moveQueuedToFront: (sessionId: string, id: string) => void;
  /** 队列「编辑」把文本/附件装回输入框：注入当前会话的草稿（文本 + 附件记账
   *  一并落——原实现就是 injectDraft 一处写两份语义）。 */
  injectDraft: (sid: string, text: string, atts?: SendAttachments) => void;
  sendWithOptions: (text: string, atts?: SendAttachments) => void;
  busy: boolean;
  sending: boolean;
}) {
  // 一次性提示（「已置顶，本轮结束后立即发送」）——4s 自动消失。
  const [queueNotice, setQueueNotice] = useState("");
  const queueNoticeTimerRef = useRef<number | null>(null);
  const flashQueueNotice = useCallback((text: string) => {
    setQueueNotice(text);
    if (queueNoticeTimerRef.current !== null) window.clearTimeout(queueNoticeTimerRef.current);
    queueNoticeTimerRef.current = window.setTimeout(() => setQueueNotice(""), 4000);
  }, []);

  const handleQueueEdit = useCallback((id: string) => {
    const item = sendQueues[currentId]?.find((entry) => entry.id === id);
    if (!item) return;
    removeQueuedSend(currentId, id);
    // 附件随文本一并装回输入框（图片批次 B：编辑 = 文本 + 附件都回 Composer）
    injectDraft(currentId, item.text, attsOf(item));
  }, [sendQueues, currentId, removeQueuedSend, injectDraft]);

  const handleQueueDelete = useCallback((id: string) => {
    removeQueuedSend(currentId, id);
  }, [currentId, removeQueuedSend]);

  const handleQueueSend = useCallback((id: string) => {
    const item = sendQueues[currentId]?.find((entry) => entry.id === id);
    if (!item) return;
    // 附件按引用透传（队列条目与发送参数共享同一数组——不复制 base64）
    // 去向判定在 send-queue.ts 的 queueSendAction（纯函数，测试直测同一份）：
    // 空闲 → 立即按正常路径发（乐观气泡照常）；忙/发送中 → 置顶（自动发送在
    // busy true→false 时先发它）+ 一次性提示。
    if (queueSendAction({ busy, sending }) === "send-now") {
      removeQueuedSend(currentId, id);
      sendWithOptions(item.text, attsOf(item));
      return;
    }
    moveQueuedToFront(currentId, id);
    flashQueueNotice("当前会话正在生成，已置顶，本轮结束后立即发送");
  }, [sendQueues, currentId, busy, sending, removeQueuedSend, sendWithOptions, moveQueuedToFront, flashQueueNotice]);

  /** 当前会话的队列（Composer 直读）。 */
  const currentQueue = sendQueues[currentId] ?? [];

  return { queueNotice, handleQueueEdit, handleQueueDelete, handleQueueSend, currentQueue };
}
