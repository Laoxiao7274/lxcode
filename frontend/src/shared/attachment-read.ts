// attachment-read.ts —— 附件读入通道（File → 纯 base64）。
//
// 三条来路（「+」菜单本地选择 / 拖拽进输入区 / 粘贴）的 File 都先走**blob
// 通道**（FileReader.readAsDataURL）——内存快照，不依赖磁盘路径，浏览器与
// Electron 通吃。但 Chromium 的 File 是「内存快照 + 背后磁盘文件」两层结构：
// 部分来源给的 File 背后文件并不存在（典型：从压缩包/聊天窗口/云盘占位拖出
// 的虚拟文件；资源管理器复制后原文件被移动/删除；快照被清理），一读就报
// NotFoundError「A requested file or directory could not be found at the time
// an operation was processed.」——用户报的「读取文件失败: …」就是它。
//
// 回落通道（仅 Electron 壳）：preload 用 webUtils.getPathForFile(file) 解析
// 真实磁盘路径（File 可过 contextBridge——Electron 官方推荐用法），主进程
// 只读读盘转 base64。真实文件还在就能救回；真实文件也没了（或来源根本不
// 落盘，如剪贴板内存图片走不到这里）时报带文件名的人话错误。
//
// 读入顺序与错误组装是纯逻辑（依赖注入），node 测试直测；defaultReadDeps
// 是浏览器/Electron 的真实实现，组件只调 readFileAsBase64。

/** Electron 壳桥的读文件切片（preload 的 __LX__.readFileBase64；浏览器模式无桥）。 */
type ShellRead = (file: File) => Promise<string>;

/** 读入依赖（测试注入 fake；defaultReadDeps 为真实实现）。 */
export interface ReadDeps {
  /** blob 通道：FileReader 直读 File。 */
  blob: (file: File) => Promise<string>;
  /** 按真实路径重读（Electron 主进程）；null = 无壳（浏览器模式，无回退）。 */
  shell: ShellRead | null;
}

export const defaultReadDeps: ReadDeps = {
  blob: (file) =>
    new Promise<string>((resolve, reject) => {
      const r = new FileReader();
      r.onload = () => {
        const s = String(r.result ?? "");
        const idx = s.indexOf(",");
        resolve(idx >= 0 ? s.slice(idx + 1) : s);
      };
      r.onerror = () => reject(r.error ?? new Error("FileReader 读取失败"));
      r.readAsDataURL(file);
    }),
  // 壳桥在模块加载时解析一次：preload 先于页面脚本运行，Electron 里此刻
  // window.__LX__ 已就位；浏览器/测试环境（无 window 或无桥）= 无回退通道。
  shell: (() => {
    if (typeof window === "undefined") return null;
    const read = (window as unknown as { __LX__?: { readFileBase64?: ShellRead } }).__LX__?.readFileBase64;
    return read ? (file: File) => read(file) : null;
  })(),
};

/** 错误消息是否为「背后文件不存在」类（Chromium NotFoundError 的各语种措辞
 *  + Go/Node 同类错误的特征串——消息组装只做归类，不做精确匹配）。 */
export function isNotFoundMessage(msg: string): boolean {
  return /could not be found|notfound|err_file_not_found|找不到|cannot find|no such file/i.test(msg);
}

function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 组装读入失败的错误：带文件名；NotFound 类给「换来源」的人话指引——
 *  原始的英文系统错误对用户没有 actionable 信息，但对排查有用（壳回落失败
 *  时把主进程带路径的消息一并带上）。 */
export function readError(file: File, blobErr: unknown, shellErr: unknown): Error {
  const name = file.name || "未命名文件";
  const blobMsg = errMessage(blobErr);
  // shellErr === null：浏览器模式（无回退通道，只有 blob 一次机会）
  if (shellErr === null) {
    return new Error(
      isNotFoundMessage(blobMsg)
        ? `${name}：文件内容读不出来——来源可能是压缩包/聊天窗口/云盘里的虚拟文件，或文件在添加前已被移动、删除；请先把文件保存到本地再添加`
        : `${name}：${blobMsg}`,
    );
  }
  const shellMsg = errMessage(shellErr);
  return new Error(
    isNotFoundMessage(blobMsg) || isNotFoundMessage(shellMsg)
      ? `${name}：文件内容读不出来（快照与真实路径都无法读取${shellMsg ? `：${shellMsg}` : ""}）——来源可能是压缩包/聊天窗口/云盘里的虚拟文件，或文件在添加前已被移动、删除；请先把文件保存到本地再添加`
      : `${name}：${blobMsg}`,
  );
}

/** 带回退的读入编排：blob 失败 → 有壳则按真实路径重读 → 仍失败抛带文件名
 *  的人话错误。base64 语义与原先一致（不带 data: 前缀）。 */
export async function readBase64With(file: File, deps: ReadDeps): Promise<string> {
  try {
    return await deps.blob(file);
  } catch (blobErr) {
    if (!deps.shell) throw readError(file, blobErr, null);
    try {
      return await deps.shell(file);
    } catch (shellErr) {
      throw readError(file, blobErr, shellErr);
    }
  }
}

/** 组件入口：File → 纯 base64（三条来路共用）。 */
export function readFileAsBase64(file: File): Promise<string> {
  return readBase64With(file, defaultReadDeps);
}
