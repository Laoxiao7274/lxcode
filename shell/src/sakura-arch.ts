// sakura-arch.ts —— frpc 下载项匹配与隧道归属判定的纯函数
// （sakura.ts 领域逻辑拆出，无 electron 依赖，可单测）。
//
// 背景（2026-12 mac 修复）：downloadFrpc 原先只匹配 /win/i 键名——natfrp
// /system/clients 的 archs 字典按平台命名发行（win-amd64 / linux-amd64 /
// darwin-arm64 之类），mac 上匹配不到直接抛「无法获取 frpc 的 Windows 下载项」，
// mac 节点根本起不来。darwin 键的真实命名未在 API 文档钉死（官方 OpenAPI 只
// 给了字典形状），所以非 Windows 侧做**宽松匹配**：平台词 + 架构词都命中才算，
// 匹配不到时错误信息给出可手动放置的出路（frpcHint 已有同款文案）。
//
// Windows 行为一字不变：win 侧沿用原判定（/win/i + 64 位词），且**优先**——
// 原实现只有 win 一个分支，语义上「win 键永远优先于其它键」，保持不变。

/** 平台判定输入（调用方传真实值；测试注入任意组合）。 */
export interface ArchSelection {
  platform: NodeJS.Platform | string;
  arch: string;
}

/**
 * 从 archs 字典里挑出当前平台的下载项键名。
 *
 * - win32：沿用原 win 判定（/win/i 且含 64 位词）——**行为与旧版逐字节一致**；
 * - 其它平台（darwin/linux）：先挑平台词（darwin|mac / linux），再在平台命中的
 *   键里按架构词（arm64|aarch64 vs amd64|x86_64|x64）挑；平台词没有命中时
 *   回落做平台+架构联合词匹配（防键名形如 "macos-arm64" 而平台词列表漏收）；
 * - 匹配失败返回 null——调用方负责把「可手动放置」的出路带进错误信息。
 *
 * 不依赖真实 API 结构做死键名：键名由调用方传字典键列表，匹配只认词根。
 */
export function pickFrpcArchKey(
  keys: string[],
  sel: ArchSelection,
): string | null {
  if (sel.platform === "win32") {
    // /win/i 会命中 "dar**win**"（旧表达式同样有这个潜伏缺陷，被单测
    // darwin 键在前的用例抓出来）——win 分支排除 darwin 键，其余词形
    // （win64 / win-amd64 / frp-win-x64）与旧判定一致。
    return keys.find((k) => /win/i.test(k) && !/darwin/i.test(k) && /amd64|x64|64/i.test(k)) ?? null;
  }
  const wantArm = sel.arch === "arm64" || sel.arch === "arm";
  const archWord = wantArm ? /arm64|aarch64/i : /amd64|x86_64|x64/i;
  const platformWord =
    sel.platform === "darwin"
      ? /darwin|mac/i
      : sel.platform === "linux"
        ? /linux/i
        : null;
  if (platformWord) {
    const platformKeys = keys.filter((k) => platformWord.test(k));
    const hit = platformKeys.find((k) => archWord.test(k));
    if (hit) return hit;
    // 平台词命中但架构词没有：宁缺毋滥（拿 linux-amd64 塞给 darwin 就是
    // 坏二进制），直接回 null 走手动放置路径。
    if (platformKeys.length > 0) return null;
  }
  // 平台词没命中任何键：兜底做平台+架构联合匹配（键名形如 "macos-arm64"）。
  const platRe =
    sel.platform === "darwin"
      ? /darwin|mac|osx/i
      : sel.platform === "linux"
        ? /linux/i
        : null;
  if (!platRe) return null;
  return keys.find((k) => platRe.test(k) && archWord.test(k)) ?? null;
}

/** 下载项是否是压缩包（按 URL 后缀或 content-type 判断，mac 自带 bsdtar 可解）。 */
export function isFrpcArchive(url: string, contentType: string | null): boolean {
  if (/(\.tar\.gz|\.tgz|\.zip|\.tar\.xz|\.tar\.bz2|\.tar)([?#]|$)/i.test(url)) return true;
  if (contentType && /gzip|zip|x-tar|compressed/i.test(contentType)) return true;
  return false;
}

// ---- 隧道归属判定（2026-12 防两端互认）----
//
// 背景：新建隧道原恒名 lxcode-backend，首运行的播种按 /^lxcode/i 前缀认领
// 账户里任何机器的隧道——两台机器登同一账户会互相认走对方的隧道（启动/停/
// 删都打到别人头上）。修复三层：
//   1. 新建隧道名带本机 hostname：lxcode-<净化hostname>（保留 lxcode 前缀
//      兼容现有播种正则）；
//   2. 播种/认领只认「本机隧道」：isOwnTunnel——名字 === lxcode-<本机
//      hostname>，或 === lxcode-backend（历史遗留，归最先用的那台；第二台
//      播种时排除它，UI 标注「可能属于其它设备」）；
//   3. 列表里非本机归属的隧道带 owned 标注（见 SakuraTunnelView.owned）。
// 服务端可能把连字符规范成下划线（原播种注释已记录此行为），匹配对 - / _
// 一视同仁。

/** 本机 hostname 净化：去非法字符（隧道名限制）+ 截断，保 lxcode-<x> 总长合理。 */
export function sanitizeHostname(raw: string): string {
  const cleaned = raw.toLowerCase().replace(/[^a-z0-9-]/g, "").replace(/^-+|-+$/g, "");
  return cleaned.slice(0, 24) || "unknown";
}

/** 本机默认隧道名：lxcode-<净化hostname>（如 lxcode-desk01）。 */
export function defaultTunnelName(hostname: string): string {
  return `lxcode-${sanitizeHostname(hostname)}`;
}

/** 历史遗留默认名（归最先用的那台；播种时第二台排除它）。 */
export const LEGACY_TUNNEL_NAME = "lxcode-backend";

/** 隧道名 → 本机 hostname 净化值（- / _ 一视同仁，服务端可能规范化连字符）。 */
function tunnelNameHost(name: string): string {
  return name.replace(/^lxcode[-_]/i, "").toLowerCase().replace(/_/g, "-");
}

/**
 * 这条隧道是否「本机拥有」（认领/播种判定，纯函数）：
 *  - 名字 === lxcode-<本机hostname>（新建的带 hostname，第一优先）；
 *  - 名字 === lxcode-backend（历史遗留默认名——归最先播种它的那台；本机
 *    hostname 净化后恰好是 "backend" 的机器两者重合，无害）。
 * 其余 lxcode-* 一律 false（别的机器创建的，播种必须排除）。
 */
export function isOwnTunnel(name: string | undefined, hostname: string): boolean {
  if (!name) return false;
  if (name.replace(/_/g, "-").toLowerCase() === LEGACY_TUNNEL_NAME) return true;
  const h = sanitizeHostname(hostname);
  if (!h || h === "unknown") return false;
  // 服务端规范化：name 里的连字符可能变下划线，比对前统一回连字符形态。
  return tunnelNameHost(name) === h;
}

/**
 * 从压缩包里解出 frpc 二进制（spawnSync tar，mac/Linux 自带 bsdtar/GNU tar，
 * 两种格式通吃；Windows 不走这里——Windows 保持「直接落盘 exe」不变）。
 * 返回解出的 frpc 的**绝对路径**；找不到 frpc 形态的条目返回 null。
 *
 * 解包到临时目录：压缩包里可能带一层目录（frp_x.y.z/frpc），扫一层目录树
 * 找名为 frpc 的普通文件（不含 .exe——那是 Windows 条目，不会出现在这里）。
 */
export function extractFrpcFromArchive(
  archivePath: string,
  workDir: string,
): string | null {
  // 延迟 require：纯函数文件的其它消费方（单测）不需要 child_process/fs。
  const { spawnSync } = require("node:child_process") as typeof import("node:child_process");
  const fs = require("node:fs") as typeof import("node:fs");
  const path = require("node:path") as typeof import("node:path");
  fs.mkdirSync(workDir, { recursive: true });
  const r = spawnSync("tar", ["-xf", archivePath, "-C", workDir], {
    shell: false,
    windowsHide: true,
  });
  if (r.status !== 0) return null;
  // 找 frpc：先扫根，再扫一层子目录（发行包常见 frp_x.y.z/ 目录壳）。
  const isFrpcName = (n: string) => n === "frpc" || /^frpc(-|\.|$)/.test(n);
  const entries = fs.readdirSync(workDir);
  for (const name of entries) {
    const p = path.join(workDir, name);
    let st;
    try {
      st = fs.statSync(p);
    } catch {
      continue;
    }
    if (st.isFile() && isFrpcName(name)) return p;
  }
  for (const name of entries) {
    const sub = path.join(workDir, name);
    let st;
    try {
      st = fs.statSync(sub);
    } catch {
      continue;
    }
    if (!st.isDirectory()) continue;
    let subEntries: string[];
    try {
      subEntries = fs.readdirSync(sub);
    } catch {
      continue;
    }
    for (const n of subEntries) {
      const p = path.join(sub, n);
      let fst;
      try {
        fst = fs.statSync(p);
      } catch {
        continue;
      }
      if (fst.isFile() && isFrpcName(n)) return p;
    }
  }
  return null;
}
