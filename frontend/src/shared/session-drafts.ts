// 输入框草稿的**按会话记账**（2026-10-10）：草稿原先是一个全局单值（App 的 draft
// state + Composer 内部的输入状态），切会话不切换——A 会话输入一半切到 B 还在，
// 切回 A 反而没了。改成 per-session 的 Record<sessionId, SessionDraft>（照
// sendQueues 的模式，独立于 reduce 流——它是纯 UI 状态，不进时间线）。
//
// 为什么抽成纯函数模块：App 组件依赖太重（settings/providers/桥接…），node:test
// 钉不住组件——「切会话换草稿、发送清空、失败回填落原会话键」这些时机契约钉在
// 这里，App 只做接线（与 notices.ts / send-queue.ts 同一条纪律：纯函数才能被
// node:test 直接钉住，组件不重复实现判定）。
//
// 附件 base64 按 session 存内存可接受（用户拍板口径）：切走保留、发送成功/移除
// 释放——与发送缓冲区（sendQueues）条目的生命周期语义一致；刷新页面即清空
// （草稿本来就是未发送的临时态，与旧行为一致）。
import type { PendingFile, PendingImage } from "./attachments";
import type { SendAttachments } from "./types";

/** 一个会话的输入框草稿：实时文本 + 暂存附件（Composer 的 images/files 状态原样）。 */
export interface SessionDraft {
  text: string;
  images: PendingImage[];
  files: PendingFile[];
}

/** 全部会话的草稿（键 = session id）。 */
export type SessionDrafts = Record<string, SessionDraft>;

/** 空草稿（新会话 / 记账缺省——输入框是空的，没有就是空）。 */
export function emptyDraft(): SessionDraft {
  return { text: "", images: [], files: [] };
}

/** 读一个会话的草稿（没记过账 = 空草稿——B 的输入框是 B 自己的，没有就是空）。 */
export function draftOf(drafts: SessionDrafts, sessionId: string): SessionDraft {
  return drafts[sessionId] ?? emptyDraft();
}

/** 全量写一个会话的草稿（Composer 的实时同步走这里：text + 附件一起换）。 */
export function writeDraft(drafts: SessionDrafts, sessionId: string, draft: SessionDraft): SessionDrafts {
  return { ...drafts, [sessionId]: draft };
}

/** 只写文本（保留该会话已有的暂存附件）——打字/注入文本走这里。 */
export function writeDraftText(drafts: SessionDrafts, sessionId: string, text: string): SessionDrafts {
  const cur = draftOf(drafts, sessionId);
  if (cur.text === text) return drafts; // 无变化不换对象（打字高频路径，省一次渲染）
  return { ...drafts, [sessionId]: { ...cur, text } };
}

/** 只换附件（保留文本）——发送失败的附件回填走这里：文本不碰（用户可能已经在
 *  重新打字），atts 省略 = 保留现有附件（注入草稿不带附件时不该清掉暂存区）。 */
export function writeDraftAtts(drafts: SessionDrafts, sessionId: string, atts?: SendAttachments): SessionDrafts {
  if (!atts) return drafts;
  const pending = pendingFromAtts(atts);
  const cur = draftOf(drafts, sessionId);
  return { ...drafts, [sessionId]: { ...cur, ...pending } };
}

/** 清空一个会话的草稿（发送成功后当前会话的记账归零——Composer 清空后实时同步
 *  也会写到同样的值，这里给「明确要清」的调用方一个直接入口）。 */
export function clearDraft(drafts: SessionDrafts, sessionId: string): SessionDrafts {
  return writeDraft(drafts, sessionId, emptyDraft());
}

/** 附件回填用的 id 序号：与 Composer 的注入 id（`img-restore-<id>-<i>`）同一条
 *  单调递增思路——同批附件重复回填也要生成新 id（React key 稳定 + 不判重）。 */
let restoreSeq = 0;

/** 发送形状的附件（SendAttachments）→ Composer 暂存形状（Pending*）：发送失败
 *  回填与注入草稿带附件时都要做这次换形。 */
export function pendingFromAtts(atts: SendAttachments): { images: PendingImage[]; files: PendingFile[] } {
  restoreSeq += 1;
  const seq = restoreSeq;
  return {
    images: atts.images.map((im, i) => ({ id: `restore-${seq}-${i}`, mime: im.mime, name: "", data: im.data })),
    files: atts.files.map((f, i) => ({ id: `file-restore-${seq}-${i}`, name: f.name, size: 0, data: f.data })),
  };
}
