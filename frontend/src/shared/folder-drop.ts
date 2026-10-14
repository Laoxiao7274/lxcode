// folder-drop.ts —— 拖入文件夹的展开收集（纯逻辑 + 依赖注入）。
//
// 背景：Composer 的 drop 曾把 DataTransfer 项一律当 File 读——拖入文件夹时
// 那个「File」只是目录占位（size 0、无内容），blob 与真实路径两条读入通道
// 都失败，报裸 NotFoundError「A requested file or directory could not be
// found…」。正确行为：drop 事件同步栈里用 webkitGetAsEntry() 判定
// isDirectory（**异步取 entry 会被 Chromium 置空**——该 API 的经典坑，调用
// 方必须保证在事件处理函数里同步取），文件夹递归展开为多个附件。
//
// 展开本身是纯逻辑（entry 树 → 收集结果 + 人话说明），node 测试用假 entry
// 直测；resolveFile 依赖注入（真实实现 = FileSystemFileEntry.file() 回调）。
//
// 上限口径：单次拖入 ≤16 个文件、总量 ≤20MB（与单文件 20MB 上限同套口径）；
// 跳过隐藏项（点开头，含 .git 这类目录）与超单文件上限的文件；超限不静默
// 截断——notice 带人话说明；个别文件读取失败跳过并说明，不整批失败。

import { MAX_FILE_BYTES, formatBytes } from "./attachments";

/** 单次拖入文件夹最多收几个文件。 */
export const MAX_FOLDER_FILES = 16;
/** 单次拖入文件夹的总字节上限（解码前文件字节——与单文件上限同套口径）。 */
export const MAX_FOLDER_TOTAL_BYTES = 20 << 20;

/** FileSystemEntry 的最小 duck type：测试用假对象即可，不依赖 DOM lib。
 *  file/createReader 是回调式 API（真实实现见 defaultFolderDeps）。 */
export interface EntryLike {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  file?: (success: (f: File) => void, error?: (e: unknown) => void) => void;
  createReader?: () => {
    readEntries: (success: (entries: EntryLike[]) => void, error?: (e: unknown) => void) => void;
  };
}

export interface FolderCollectDeps {
  /** entry → File；解析失败（文件消失/无权读）返回 null——跳过该文件。 */
  resolveFile: (entry: EntryLike) => Promise<File | null>;
  maxFiles?: number;
  maxTotalBytes?: number;
  maxFileBytes?: number;
}

export interface FolderCollectResult {
  files: File[];
  /** 与 files 平行的**相对路径数组**（相对拖入的文件夹根，保留目录结构——
   *  展示层「内部是地址」：`docs/api/auth.md`、`assets/logo.png`）。length
   *  与 files 一致，下标一一对应。 */
  relPaths: string[];
  /** 人话说明（空串 = 没有可说的）：空文件夹 / 截断 / 跳过原因。 */
  notice: string;
}

/** FileSystemFileEntry.file() 的 Promise 化；失败/缺 API 回 null（跳过不整批失败）。 */
function entryFile(entry: EntryLike): Promise<File | null> {
  return new Promise((resolve) => {
    if (!entry.file) {
      resolve(null);
      return;
    }
    entry.file((f) => resolve(f), () => resolve(null));
  });
}

/** 真实实现的收集依赖：entry.file() 解析 File；上限用本模块默认值。 */
export const defaultFolderDeps: FolderCollectDeps = {
  resolveFile: entryFile,
};

/** 读全目录条目：Chromium 的 readEntries 每次最多回 100 条——必须循环读到
 *  空批才算完（只读一批会静默丢文件）。读目录失败时已拿到的照收（不整批失败）。 */
function readAllEntries(dir: EntryLike): Promise<EntryLike[]> {
  return new Promise((resolve) => {
    const reader = dir.createReader?.();
    if (!reader) {
      resolve([]);
      return;
    }
    const out: EntryLike[] = [];
    const step = () => {
      reader.readEntries(
        (batch) => {
          if (batch.length === 0) {
            resolve(out);
            return;
          }
          out.push(...batch);
          step();
        },
        () => resolve(out),
      );
    };
    step();
  });
}

/** 展开一个拖入的目录 entry：递归收集文件（深度优先、同名序即遍历序）。
 *  - 隐藏项（名字点开头）的文件与目录都跳过——拖项目目录不该捞进 .git；
 *  - 个数到 maxFiles / 总量将超 maxTotalBytes 时停止并记截断（不静默）；
 *  - 单文件超 maxFileBytes、解析失败：跳过计数，不中断整批；
 *  - 一个文件都没收到且无截断/跳过 → notice「文件夹是空的」。 */
export async function collectFolderEntries(root: EntryLike, deps: FolderCollectDeps): Promise<FolderCollectResult> {
  const maxFiles = deps.maxFiles ?? MAX_FOLDER_FILES;
  const maxTotal = deps.maxTotalBytes ?? MAX_FOLDER_TOTAL_BYTES;
  const maxFile = deps.maxFileBytes ?? MAX_FILE_BYTES;

  const files: File[] = [];
  /** 与 files 平行的相对路径（相对拖入文件夹根；walk 途中拼接）。 */
  const relPaths: string[] = [];
  let total = 0;
  let truncatedCount = false;
  let truncatedTotal = false;
  let unreadable = 0;
  let oversize = 0;

  // 返回 false = 已到上限，整体停止（不再往深处走）；rel 是当前目录相对根的
  // 路径前缀（根为空串，子目录逐级 `docs/api`）。
  const walk = async (dir: EntryLike, rel: string): Promise<boolean> => {
    const entries = await readAllEntries(dir);
    for (const e of entries) {
      if (e.name.startsWith(".")) continue; // 隐藏文件/目录
      if (e.isDirectory) {
        if (!(await walk(e, rel ? `${rel}/${e.name}` : e.name))) return false;
        continue;
      }
      if (!e.isFile) continue;
      if (files.length >= maxFiles) {
        truncatedCount = true;
        return false;
      }
      const f = await deps.resolveFile(e);
      if (!f) {
        unreadable++;
        continue;
      }
      if (f.size > maxFile) {
        oversize++;
        continue;
      }
      if (total + f.size > maxTotal) {
        truncatedTotal = true;
        return false;
      }
      files.push(f);
      relPaths.push(rel ? `${rel}/${e.name}` : e.name);
      total += f.size;
    }
    return true;
  };
  await walk(root, "");

  const notes: string[] = [];
  if (files.length === 0 && !truncatedCount && !truncatedTotal) {
    if (oversize === 0 && unreadable === 0) return { files, relPaths, notice: "文件夹是空的" };
    // 没收到任何文件，但原因明确：如实说为什么
    if (oversize > 0) notes.push(`${oversize} 个文件超过单文件 20MB 上限已跳过`);
    if (unreadable > 0) notes.push(`${unreadable} 个文件读取失败已跳过`);
    return { files, relPaths, notice: `文件夹里没有可添加的文件：${notes.join("，")}` };
  }
  if (truncatedCount) {
    notes.push(`已收录前 ${files.length} 个，其余未加入（可压缩后分批发送）`);
  }
  if (truncatedTotal) {
    notes.push(`文件夹内容总量超过 ${formatBytes(maxTotal)}：已收录前 ${files.length} 个，其余未加入（可压缩后分批发送）`);
  }
  if (oversize > 0) notes.push(`${oversize} 个文件超过单文件 20MB 上限已跳过`);
  if (unreadable > 0) notes.push(`${unreadable} 个文件读取失败已跳过`);
  return { files, relPaths, notice: notes.join("；") };
}
