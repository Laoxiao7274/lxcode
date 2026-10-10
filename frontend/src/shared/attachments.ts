// 附件（图片批次 B）的纯逻辑：暂存条目的增删、上限校验、wire 形状组装。
//
// 独立成纯函数模块的原因与 send-queue.ts 同款：交互路径（选文件/粘贴）没法在
// node 测试环境跑，把「能加什么、加到哪、为什么拒」钉在纯函数上，组件只负责
// 读文件（异步）与画——测试直接驱动同一份判定，不复刻副本。
//
// 上限与后端 server 侧校验同一套（单图 ≤5MB 解码后 / ≤4 张；单文件 ≤20MB /
// ≤4 个）——前端先拦是体验（选完立刻知道），后端再拦是权威（wire 可被绕过）。

import type { ChatSendFile, ChatSendImage, SendAttachments } from "./types";

/** 图片 mime 白名单（与后端 llm.ValidImageMime 同一份清单）。 */
export const IMAGE_MIMES = ["image/png", "image/jpeg", "image/webp", "image/gif"] as const;

/** 单张图片上限：5MB（base64 解码后——与后端一致）。 */
export const MAX_IMAGE_BYTES = 5 << 20;
/** 一条消息最多几张图。 */
export const MAX_IMAGE_COUNT = 4;
/** 单个文件上限：20MB（base64 解码后——与后端一致）。 */
export const MAX_FILE_BYTES = 20 << 20;
/** 一条消息最多几个文件。 */
export const MAX_FILE_COUNT = 4;

/** Composer 暂存的一张图片：data 为纯 base64（不带 data: 前缀——发送直接用）。 */
export interface PendingImage {
  id: string;
  mime: string;
  name: string;
  data: string;
}

/** Composer 暂存的一个文件。 */
export interface PendingFile {
  id: string;
  name: string;
  size: number;
  data: string;
}

export function isImageMime(mime: string): boolean {
  return (IMAGE_MIMES as readonly string[]).includes(mime);
}

/** 字节数 → 人话大小（附件卡片/提示用；1024 进制，一位小数只在 MB 级出现）。 */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/** 追加图片（选择/粘贴共用一条路径）：逐张校验 mime 白名单、单张大小、总数。
 *  全部合法才收（一次选择里挑出非法的会拒掉整批并说明原因——半收半拒会让
 *  「刚才选中的哪几张进了」变成猜谜）。返回新数组（不原地改——React state）。
 *  notice 非空 = 有拒绝原因，调用方显示为一次性提示。 */
export function addPendingImages(
  existing: PendingImage[],
  incoming: { mime: string; name: string; data: string }[],
): { images: PendingImage[]; notice: string } {
  if (incoming.length === 0) return { images: existing, notice: "" };
  for (const im of incoming) {
    if (!isImageMime(im.mime)) {
      return { images: existing, notice: `不支持的图片类型：${im.name || im.mime}（支持 png/jpeg/webp/gif）` };
    }
    // base64 解码后的字节数 ≈ data 长度 × 3/4——判定用真字节（与后端口径一致）
    if (base64Bytes(im.data) > MAX_IMAGE_BYTES) {
      return { images: existing, notice: `图片 ${im.name || ""} 超过 5MB 上限` };
    }
  }
  if (existing.length + incoming.length > MAX_IMAGE_COUNT) {
    return { images: existing, notice: `一条消息最多 ${MAX_IMAGE_COUNT} 张图片` };
  }
  const base = existing.length;
  return {
    images: [...existing, ...incoming.map((im, i) => ({ id: `img-${Date.now().toString(36)}-${base + i}-${im.name}`, mime: im.mime, name: im.name, data: im.data }))],
    notice: "",
  };
}

/** 追加文件：逐个校验大小与总数（文件不做类型白名单——Agent 什么都要能收）。 */
export function addPendingFiles(
  existing: PendingFile[],
  incoming: { name: string; size: number; data: string }[],
): { files: PendingFile[]; notice: string } {
  if (incoming.length === 0) return { files: existing, notice: "" };
  for (const f of incoming) {
    if (f.size > MAX_FILE_BYTES) {
      return { files: existing, notice: `文件 ${f.name} 超过 20MB 上限（当前 ${formatBytes(f.size)}）` };
    }
  }
  if (existing.length + incoming.length > MAX_FILE_COUNT) {
    return { files: existing, notice: `一条消息最多 ${MAX_FILE_COUNT} 个文件` };
  }
  const base = existing.length;
  return {
    files: [...existing, ...incoming.map((f, i) => ({ id: `file-${Date.now().toString(36)}-${base + i}-${f.name}`, name: f.name, size: f.size, data: f.data }))],
    notice: "",
  };
}

/** 移除一张图/一个文件（id 不存在时原样返回——引用不变，React 跳过重渲染）。 */
export function removePending<T extends { id: string }>(list: T[], id: string): T[] {
  const next = list.filter((item) => item.id !== id);
  return next.length === list.length ? list : next;
}

/** 暂存区是否挂了附件（submit 门与「+」按钮角标共用）。 */
export function hasPendingAtts(images: PendingImage[], files: PendingFile[]): boolean {
  return images.length > 0 || files.length > 0;
}

/** 暂存 → wire 形状（SendOptions.images/files / 队列条目共用——images/files
 *  数组按引用给出，调用方不再复制 base64）。 */
export function attsFromPending(images: PendingImage[], files: PendingFile[]): SendAttachments {
  const wireImages: ChatSendImage[] = images.map((im) => ({ mime: im.mime, data: im.data }));
  const wireFiles: ChatSendFile[] = files.map((f) => ({ name: f.name, data: f.data }));
  return { images: wireImages, files: wireFiles };
}

/** base64 字符串对应的解码后字节数（不真的解码——只算大小）。 */
function base64Bytes(b64: string): number {
  const padding = b64.endsWith("==") ? 2 : b64.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((b64.length * 3) / 4) - padding);
}

/** vision 可用性感知（图片批次 B，尽力而为）的**纯判定**：
 *
 *  - modelId 为空（会话还没跑过任何一轮 / 后端没报）→ 不禁用；
 *  - 注册表里查不到该模型 → 不禁用（能力未知）；
 *  - 明确声明 vision === false → 禁用（人话原因，Composer 拿它做 title）；
 *  - 声明缺失（undefined）/ true → 不禁用。
 *
 *  不禁用的路径靠后端 vision 校验的人话错误兜底（不为感知新增协议字段）。
 *  App 传入的模型清单是 providers 投影（ModelMeta——vision 从注册表直通）。 */
export function visionBlockNotice(
  modelId: string,
  models: { id: string; vision?: boolean }[],
): string | null {
  if (!modelId) return null;
  const entry = models.find((m) => m.id === modelId);
  if (!entry || entry.vision !== false) return null;
  return `当前模型不支持视觉：${modelId}——文件不受影响；要发图请先到 设置 → 模型 勾选「视觉」能力`;
}
