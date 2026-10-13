// 附件（图片批次 B）的纯逻辑：暂存条目的增删、上限校验、wire 形状组装。
//
// 独立成纯函数模块的原因与 send-queue.ts 同款：交互路径（选文件/粘贴）没法在
// node 测试环境跑，把「能加什么、加到哪、为什么拒」钉在纯函数上，组件只负责
// 读文件（异步）与画——测试直接驱动同一份判定，不复刻副本。
//
// 上限与后端 server 侧校验同一套（单图 ≤5MB 解码后 / ≤4 张；单文件 ≤20MB /
// ≤4 个）——前端先拦是体验（选完立刻知道），后端再拦是权威（wire 可被绕过）。

import type { AttachmentRef, ChatSendFile, ChatSendImage, SendAttachments } from "./types";

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

// ===== 附件展示通道（2026-12 第一版）=====
//
// 后端把附件落盘到 <sessions>/attachments/<session-id>/，消息上带**引用 +
// 展示元数据**（llm.AttachmentRef）。这里负责三件事：
// ① 由引用构造 HTTP 取件 URL（后端 7789 的 /attachments 只读端点）；
// ② 老历史的回落解析：attachments 列上线前的 user 消息只有文本里的
//    「[附件] 名字 → attachments/...」行与 images 图片引用——归一化成同一份
//    AttachmentMeta，两条路径（实时/回放）渲染同一张卡；
// ③ 大小人话化（formatBytes 已在上面）。

/** 附件卡的展示条目（归一化产物——AttachmentRef 或老历史回落的合成值）。 */
export interface AttachmentMeta {
  path: string;
  name: string;
  size?: number;
  mime?: string;
  kind: "image" | "file";
  caption?: string;
}

/** 附件静态端点的 HTTP 源（wsagent 连接时注入——同一台后端主机）。
 *  空串 = 同源（相对路径 /attachments/...，浏览器模式下页面本身就由后端服务）。 */
let attachmentOrigin = "";

/** 注入附件端点的 HTTP 源（WSAgent 建立/重连时调用；demo 源不调 = 同源回落）。 */
export function setAttachmentOrigin(origin: string): void {
  attachmentOrigin = (origin || "").replace(/\/+$/, "");
}

/** 由相对引用构造取件 URL：`<origin>/attachments/<path>`。
 *  path 只接受 "<session-id>/<file>" 形态（服务端生成），任何带 .. 的输入
 *  原样拒绝（返回 null——卡显示为不可点，不做穿越尝试）。 */
export function attachmentURL(path: string): string | null {
  if (!path || path.includes("..") || path.startsWith("/") || path.includes("\\")) return null;
  const segs = path.split("/").filter(Boolean);
  if (segs.length !== 2) return null;
  return `${attachmentOrigin}/attachments/${segs[0]}/${encodeURIComponent(segs[1])}`;
}

/** 「[附件] 名字 → attachments/<sid>/<file>」行的定界（与后端 server 侧
 *  sendAttachments 的写入格式逐字一致——写错了解析不出，卡片就消失）。 */
const ATTACHMENT_LINE = /^\[附件\] (.+?) → attachments\/(.+)$/;

/** 从消息文本解析出「[附件]」行并从正文剥离：老历史（attachments 列上线前）
 *  的文件附件只有这一种载体。返回解析出的条目与剥离后的正文。
 *  解析不出的行原样保留在正文里（宁可多一行字，不静默吞内容）。 */
export function parseAttachmentLines(text: string): { atts: AttachmentMeta[]; text: string } {
  const atts: AttachmentMeta[] = [];
  const kept: string[] = [];
  for (const line of text.split("\n")) {
    const m = ATTACHMENT_LINE.exec(line.trim());
    if (m && attachmentURL(m[2])) {
      atts.push({ path: m[2], name: m[1], kind: "file" });
    } else {
      kept.push(line);
    }
  }
  return { atts, text: kept.join("\n").replace(/\n{3,}/g, "\n\n").trim() };
}

/** 消息 → 附件卡条目（唯一归一化入口）：结构化 attachments 优先；老历史回落
 *  images 引用（→ 图片卡）+ 文本「[附件]」行（→ 文件卡）。
 *  返回 undefined = 无附件（不渲染卡区，与后端 omitempty 同语义）。 */
export function attachmentsFromMessage(m: {
  attachments?: AttachmentRef[];
  images?: Array<{ path: string; mime: string }>;
  content: string;
}): AttachmentMeta[] | undefined {
  if (m.attachments?.length) {
    // 键缺席纪律：undefined 的可选键不写进条目（size/mime/caption 缺席就是缺席
    // ——不是 undefined 值的键；测试按形状断言，渲染层也不得以 undefined 显示）。
    return m.attachments.map((a) => {
      const out: AttachmentMeta = { path: a.path, name: a.name, kind: a.kind === "image" ? "image" : "file" };
      if (a.size) out.size = a.size;
      if (a.mime) out.mime = a.mime;
      if (a.caption) out.caption = a.caption;
      return out;
    });
  }
  const fromLines = parseAttachmentLines(m.content ?? "");
  const imgs: AttachmentMeta[] = (m.images ?? []).map((im) => ({
    path: im.path, name: "", mime: im.mime, kind: "image",
  }));
  const all = [...imgs, ...fromLines.atts];
  return all.length ? all : undefined;
}
