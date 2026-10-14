// attachment-send.ts —— 附件的**发送侧**纯逻辑（2026-12 用户拍板：本地路径
// 优先，base64 兜底）。
//
// 与 attachments.ts（暂存侧）/ attachment-read.ts（读入通道）的分工：
// - 暂存侧决定「一个文件记成什么形态」（有真实路径记 path，没有读 base64）；
// - 本模块决定「发送时 wire 上放什么」（path 形态只传路径，**file 对象绝不
//   上 wire**）与「后端读不到 path 时怎么救」（用暂存时留下的 File 对象把同一
//   个文件读成 base64 重发一次——文件在 staging 之后被移动/删除是真实场景，
//   File 快照可能还在）。
//
// 全部依赖注入，node:test 直测；WSAgent 只做接线。

import type { ChatSendFile, ChatSendImage } from "./types";
import { defaultReadDeps, readBase64With } from "./attachment-read";

/** wire 形状：chat.send 的 images/files 参数元素。path 形态不带 data（后端按
 *  路径读盘），base64 形态不带 path——两形态互斥由后端校验兜底，这里从源头
 *  就只发其一。file 是客户端内存凭据，**在这里剥掉**（JSON.stringify 不会带它，
 *  但显式剥掉让「file 不上 wire」成为类型层的既成事实）。 */
export function wireImage(im: ChatSendImage): Record<string, unknown> {
  const out: Record<string, unknown> = im.path
    ? { mime: im.mime, path: im.path }
    : { mime: im.mime, data: im.data };
  if (im.name) out.name = im.name;
  return out;
}

export function wireFile(f: ChatSendFile): Record<string, unknown> {
  const out: Record<string, unknown> = f.path
    ? { name: f.name, path: f.path }
    : { name: f.name, data: f.data };
  if (f.rel_path) out.rel_path = f.rel_path;
  return out;
}

/** 这批发送附件里有没有 path 形态（决定 chat.send 失败后要不要尝试降级）。 */
export function hasPathAtts(images?: ChatSendImage[], files?: ChatSendFile[]): boolean {
  return Boolean(images?.some((im) => im.path) || files?.some((f) => f.path));
}

/** 后端错误是否为「path 形态读不到文件」（后端 readLocalAttachment 的特征串
 *  ——只对这一类错误降级，别的参数错误（mime 白名单/vision 门）重发也是同样
 *  的错，白读一遍文件）。大小写不敏感的子串匹配：错误文案会拼进别的短语。 */
export function isPathReadError(message: string): boolean {
  return typeof message === "string" && message.includes("本地文件读取失败");
}

/** 降级读入依赖（测试注入 fake；default = readBase64With 的真实通道——blob
 *  优先，File 快照失效再按真实路径重读）。 */
export type DegradeRead = (file: File) => Promise<string>;

export const defaultDegradeRead: DegradeRead = (file) => readBase64With(file, defaultReadDeps);

export interface DegradeDeps {
  read: DegradeRead;
}

/** 把 path 形态的附件**全部**读成 base64（降级重发的载荷）：File 对象在暂存时
 *  就留在了手上，这里逐个读；任何一个读不出来（快照失效 + 真实路径也没了）就
 *  整批失败——部分降级会让一条消息里同一个文件发两遍或漏发，宁缺毋滥。
 *  返回与 ChatSendImage/ChatSendFile 同形的 base64 形态数组。 */
export async function readAttsAsBase64(
  images: ChatSendImage[] | undefined,
  files: ChatSendFile[] | undefined,
  deps: DegradeDeps = { read: defaultDegradeRead },
): Promise<{ images: ChatSendImage[]; files: ChatSendFile[] }> {
  const outImages: ChatSendImage[] = [];
  for (const im of images ?? []) {
    if (im.path) {
      if (!im.file) throw new Error(`图片 ${im.name || im.path}：文件对象已不可用，无法重读内容`);
      outImages.push({ mime: im.mime, data: await deps.read(im.file), name: im.name });
    } else {
      outImages.push(im);
    }
  }
  const outFiles: ChatSendFile[] = [];
  for (const f of files ?? []) {
    if (f.path) {
      if (!f.file) throw new Error(`文件 ${f.name || f.path}：文件对象已不可用，无法重读内容`);
      outFiles.push({ name: f.name, data: await deps.read(f.file) });
    } else {
      outFiles.push(f);
    }
  }
  return { images: outImages, files: outFiles };
}
