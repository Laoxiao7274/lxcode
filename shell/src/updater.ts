// updater.ts —— 自更新编排（AGENTS.md §2.1 的 zip 产物契约）：
//   manifest.json {version,url,sha256,size,files{逐文件 sha256}}
//   update-<version>.zip 内路径 = 安装目录相对路径（resources/app.asar、
//   resources/bin/lxcode.exe）
// 两级更新：
//   后端热替换 = 停后端 → 旧 exe 改名 .old → 落新 exe → 重启后端（壳不退）
//   asar 冷替换 = 暂存到 userData/update-pending + 脱离进程的替换脚本：
//     等壳退出 → 换 asar → 重新拉起（运行中 asar 被 Chromium 映射，换不了）
// 解压用系统自带 bsdtar（与 scripts/build.mjs 造 zip 同一套零依赖纪律）。
import { spawn, spawnSync } from "node:child_process";
import { appendFileSync, createWriteStream, existsSync, mkdirSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { readdirSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { pipeline } from "node:stream/promises";
import { Readable, Transform } from "node:stream";
import { app, BrowserWindow, ipcMain } from "electron";
import { backendExePath, restartBackend } from "./sidecar";
import { createUpdatePoller, manualCheckOutcome, nextCheckDelayMs, type UpdatePoller } from "./update-poll";
import { notifyTrayUpdate } from "./tray";

/** 更新源（env 覆盖；默认 = 官网更新服务器的 manifest——site-backend 按契约生成，
 *  url 为相对文件名，按 manifest 所在目录解析）。 */
function manifestUrl(): string {
  return process.env.LXCODE_UPDATE_URL ?? "https://api.r96314722.nyat.app:15714/site/manifest.json";
}

export interface UpdateManifest {
  version: string;
  url: string;
  sha256: string;
  size: number;
  notes?: string[];
  files?: Record<string, string>;
}

/** 语义化版本比较（负数 = a<b）。逐段数值比较，段数不齐补零（0.2 vs 0.2.1）。 */
export function compareVersions(a: string, b: string): number {
  const pa = a.replace(/^v/i, "").split(/[.-]/).map((x) => parseInt(x, 10) || 0);
  const pb = b.replace(/^v/i, "").split(/[.-]/).map((x) => parseInt(x, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d < 0 ? -1 : 1;
  }
  return 0;
}

const sha256 = (p: string): string => createHash("sha256").update(loadOriginalFs().readFileSync(p)).digest("hex");

// ---- 日志与错误包装 ----
// 2026-10-09 EPERM 事故：Node 的 rmSync 在 Windows 上删被占用的目录时，报错只有
// 裸 `EPERM ... \\?\C:\...\update-staging`——路径指向顶层目录而不是被锁的具体文件，
// 用户既看不出卡在哪一步也读不懂原因。两条对策：每一步落日志 + 所有 fs 错误包人话。
//
// 2026-10-10 根因定位（0.2.0 升级再次 EPERM）：占用者是**应用自身进程**——Electron
// 给 fs 打了 asar 补丁，`readFileSync/copyFileSync/existsSync` 碰到 `*.asar` 路径会把
// 整个路径当「归档+内部路径」解析：readFileSync 直接抛 `ENOENT: not found in <asar>`
// （内部路径为空，实测），打包态还会把打开的归档句柄缓存在 ArchiveCache 里常驻到
// 进程退出——于是逐文件校验必挂，且之后同一会话里任何对 update-staging 的删除都
// EPERM，重试多久都没用。对策：所有可能触碰 *.asar 的 fs 调用一律走 originalFs
// （Electron 注入的未打补丁 Node fs，读的是原始字节）；staging 同时改为唯一子目录
// 兜底（见 downloadAndStage）。

/** 未打补丁的 Node fs：优先 electron.originalFs，回落 Electron 内置模块 original-fs
 *  （构建时 external，见 build.mjs；运行时由 Electron 注入）。 */
function loadOriginalFs(): typeof import("node:fs") {
  const ef = require("electron") as { originalFs?: typeof import("node:fs") };
  if (ef.originalFs) return ef.originalFs;
  return require("original-fs");
}

/** 更新器日志：主进程 console + userData/update.log（产线主进程 console 不可见，落文件才能事后对账）。 */
function ulog(step: string, detail = ""): void {
  const line = `[update] ${new Date().toISOString()} ${step}${detail ? ` — ${detail}` : ""}`;
  console.log(line);
  try {
    appendFileSync(join(app.getPath("userData"), "update.log"), line + "\n");
  } catch {
    // 日志写不进去不影响更新流程
  }
}

/** fs 错误 → 人话（按错误码分类：占用/权限/磁盘满；裸消息只在未知错误时兜底透出）。 */
function humanFsError(e: unknown, what: string): Error {
  const code = (e as NodeJS.ErrnoException | null)?.code ?? "";
  const raw = e instanceof Error ? e.message : String(e);
  if (code === "EPERM" || code === "EACCES") {
    return new Error(`${what}：文件被占用或权限不足（常见是杀毒软件正在扫描刚下载/解压的文件）。` +
      `本次下载已改用独立的暂存子目录，直接重试通常可以成功；若反复失败，退出 Lxcode（注意托盘也要退出）等十几秒再打开重试，` +
      `仍不行则手动删除 Roaming\\lxcode 下的 update-staging / update-pending 目录后再试（删除它们不影响现有安装与数据）。`);
  }
  if (code === "EBUSY") return new Error(`${what}：文件正被其他程序使用，请稍后重试。`);
  if (code === "ENOSPC") return new Error(`${what}：磁盘空间不足，请清理后重试。`);
  return new Error(`${what}失败：${raw}`);
}

/** rmSync 带自实现指数退避（总窗 ~10s）。Node 自带的 maxRetries 在 Windows 上对目录树
 *  EPERM 不生效（实测 v24 1ms 内直接抛，一次都不重试）。0.1.4 的 8×250ms≈2s 实测不够：
 *  0.2.0 升级时占用持续超过 2s（杀软扫描 19MB exe / asar 归档句柄常驻）。指数退避
 *  500ms→8s，总窗 10s 封顶；bestEffort=true 时超窗不抛错只记日志（清理类调用：删不掉
 *  就留着，下次下载/再清），非占用类错误立即包装上抛。 */
const RM_RETRY_DELAYS_MS = [500, 1000, 2000, 4000, 8000];
const RM_RETRY_WINDOW_MS = 10_000;

async function rmWithRetry(path: string, what: string, recursive: boolean, bestEffort = false): Promise<void> {
  const t0 = Date.now();
  for (let i = 0; ; i++) {
    try {
      rmSync(path, { recursive, force: true });
      return;
    } catch (e) {
      const code = (e as NodeJS.ErrnoException)?.code ?? "";
      const transient = code === "EPERM" || code === "EBUSY" || code === "ENOTEMPTY";
      const waited = Date.now() - t0;
      if (!transient || waited >= RM_RETRY_WINDOW_MS) {
        if (bestEffort) {
          ulog(`${what}——跳过（不影响本次更新，之后重清）`, `code=${code || "unknown"}，已退避 ${(waited / 1000).toFixed(1)}s`);
          return;
        }
        throw humanFsError(e, what);
      }
      const delay = Math.min(RM_RETRY_DELAYS_MS[Math.min(i, RM_RETRY_DELAYS_MS.length - 1)], RM_RETRY_WINDOW_MS - waited);
      ulog("删除被占用，退避重试", `${what}（第 ${i + 1} 次，code=${code}，已等 ${(waited / 1000).toFixed(1)}s）`);
      await new Promise((r) => setTimeout(r, delay));
    }
  }
}

// 状态：检查到的可用更新（download 消费）+ 下载完成并校验通过的暂存目录（apply 消费）
let lastManifest: UpdateManifest | null = null;
let stagedDir: string | null = null; // dl-<ts>/payload——apply 取 exe/asar 的地方
let stagedRoot: string | null = null; // dl-<ts>——apply 完成后整体清理的单位
let downloading = false; // 并发防护：下载中再点下载，第二次的 rmSync 会撞上第一次打开中的 zip 句柄 → EPERM

/** 装配 IPC 面（main.ts 启动时调一次）。manifest 由主进程持有——渲染层不传
 *  更新元数据（防伪造：url/哈希只来自主进程拉取的 manifest）。 */
export function updateHandlers(): void {
  ipcMain.handle("update:version", () => app.getVersion());
  // 手动检查走三态结局（latest/available/error）——error 带人话原因（网络差、
  // manifest 格式坏），渲染层直接展示，不静默（manualCheck 的容错语义与自动检查不同）。
  ipcMain.handle("update:check", () => manualCheckOutcome(checkUpdate));
  ipcMain.handle("update:download", () => downloadAndStage());
  ipcMain.handle("update:apply", () => applyUpdate());
  ipcMain.handle("update:restart", () => {
    // 冷替换脚本在壳退出后完成换 asar 并重新拉起
    app.quit();
  });
}

/** 检查更新：拉 manifest → 版本比较。网络失败如实上抛（UI 显示检查失败）。 */
async function checkUpdate(): Promise<{ available: boolean; manifest: UpdateManifest | null }> {
  const res = await fetch(manifestUrl(), { signal: AbortSignal.timeout(8_000) });
  if (!res.ok) throw new Error(`manifest 拉取失败：HTTP ${res.status}`);
  const manifest = (await res.json()) as UpdateManifest;
  if (typeof manifest.version !== "string" || typeof manifest.url !== "string" || typeof manifest.sha256 !== "string") {
    throw new Error("manifest 格式不合法（缺 version/url/sha256）");
  }
  const available = compareVersions(manifest.version, app.getVersion()) > 0;
  ulog("检查更新", `当前 ${app.getVersion()}，远端 ${manifest.version}，${available ? "有更新" : "已是最新"}`);
  lastManifest = available ? manifest : null;
  return { available, manifest: lastManifest };
}

/** 陈旧暂存清理（best-effort，单次尝试不重试）：0.2.1 起 staging 用唯一子目录
 *  dl-<ts>，旧目录被锁不再阻断新下载，但目录会堆积——下载开始时顺带清一遍：
 *  非 dl-* 的旧布局残留（0.2.0 及更早的平铺 update.zip/payload）直接清；dl-* 只清
 *  超过 24h 的。锁住就跳过（下次下载再清），绝不抛错、不阻塞下载。 */
async function cleanupStaleStaging(staging: string): Promise<void> {
  let names: string[] = [];
  try {
    names = readdirSync(staging);
  } catch {
    return; // 目录不存在 = 没有残留
  }
  for (const name of names) {
    const p = join(staging, name);
    try {
      const stale = !name.startsWith("dl-") || Date.now() - statSync(p).mtimeMs > 24 * 3600_000;
      if (!stale) continue;
      rmSync(p, { recursive: true, force: true });
      ulog("陈旧暂存已清理", name);
    } catch (e) {
      const code = (e as NodeJS.ErrnoException)?.code ?? "";
      ulog("陈旧暂存清理跳过（仍被占用，下次再清）", `${name}（code=${code}）`);
    }
  }
}

/** 下载 + 整包 sha256 校验 + 解压 + 逐文件校验 → 暂存目录就绪。 */
async function downloadAndStage(): Promise<{ ok: boolean }> {
  if (!lastManifest) throw new Error("没有待下载的更新（先 check）");
  if (downloading) throw new Error("已有一次更新下载正在进行，请等它结束再试");
  downloading = true;
  const manifest = lastManifest;
  try {
    ulog("开始下载", `目标版本 ${manifest.version}`);
    const staging = join(app.getPath("userData"), "update-staging");
    // 0.2.1 起 staging 改为唯一子目录：每次下载在 dl-<ts>/ 里解压、校验——旧子目录
    // 被占用（杀软扫描、历史泄漏的句柄）完全不影响新下载；旧目录降级为 best-effort
    // 清理（见 cleanupStaleStaging），不再「必须清空才能下载」。
    await cleanupStaleStaging(staging);
    const stageRoot = join(staging, `dl-${Date.now()}`);
    mkdirSync(stageRoot, { recursive: true });
    stagedRoot = stageRoot;
    const zipPath = join(stageRoot, "update.zip");

    // 流式下载（进度经 webContents 广播；节流 100ms——渲染层刷条不要每块都画）。
    // url 允许是**相对文件名**（lxcode-site 的 manifest 契约：url = update-<v>.zip），
    // 按 manifest 所在目录解析；绝对 URL（本地 e2e 源）原样用。
    const updateUrl = new URL(manifest.url, manifestUrl()).toString();
    const res = await fetch(updateUrl, { signal: AbortSignal.timeout(30_000) });
    if (!res.ok || !res.body) throw new Error(`更新包下载失败：HTTP ${res.status}`);
    const total = manifest.size || Number(res.headers.get("content-length") ?? 0);
    let done = 0;
    let lastEmit = 0;
    const counter = new Transform({
      transform(chunk, _enc, cb) {
        done += chunk.length;
        const now = Date.now();
        if (now - lastEmit > 100) {
          lastEmit = now;
          broadcast({ percent: total > 0 ? Math.min(99, Math.round((done / total) * 100)) : 0, downloaded: done, total });
        }
        cb(null, chunk);
      },
    });
    await pipeline(Readable.fromWeb(res.body as never), counter, createWriteStream(zipPath));
    broadcast({ percent: 100, downloaded: done, total });
    ulog("下载完成", `${done} 字节`);

    // 整包校验
    const actual = sha256(zipPath);
    if (actual !== manifest.sha256) {
      ulog("整包校验失败", `期望 ${manifest.sha256} 实际 ${actual}`);
      throw new Error(`更新包校验失败（sha256 不匹配，疑似下载损坏或被篡改）`);
    }
    ulog("整包校验通过");
    // 解压 + 逐文件校验（zip 内路径 = 安装目录相对路径）
    const extractDir = join(stageRoot, "payload");
    mkdirSync(extractDir, { recursive: true });
    const r = spawnSync("tar", ["-xf", zipPath, "-C", extractDir], { shell: false });
    if (r.status !== 0) {
      const stderr = r.stderr?.toString().trim().slice(0, 300) ?? "";
      ulog("解压失败", `tar exit ${r.status} ${stderr}`);
      throw new Error(`更新包解压失败（tar exit ${r.status}）${stderr ? `：${stderr}` : ""}`);
    }
    ulog("解压完成");
    if (manifest.files) {
      for (const [rel, expect] of Object.entries(manifest.files)) {
        const f = join(extractDir, rel);
        if (!loadOriginalFs().existsSync(f)) {
          ulog("逐文件校验失败", `更新包缺文件: ${rel}`);
          throw new Error(`更新包缺文件: ${rel}`);
        }
        if (sha256(f) !== expect) {
          ulog("逐文件校验失败", `${rel}（sha256 不匹配）`);
          throw new Error(`文件校验失败: ${rel}`);
        }
      }
    }
    ulog("逐文件校验通过", `${Object.keys(manifest.files ?? {}).length} 个文件`);
    stagedDir = extractDir;
    // zip 只服务于解压，删不删都不影响本次更新（它在本次的 dl-<ts> 子目录里，不会
    // 混入下次校验）——被占用（杀软正在扫它）时不应让整个下载报错，降级为日志。
    await rmWithRetry(zipPath, "清理暂存 zip", false, true);
    ulog("暂存就绪，等待应用");
    return { ok: true };
  } finally {
    downloading = false;
  }
}

/** 应用更新：后端热替换（壳不退）+ asar 冷替换排程（退出时换）+ 提示重启。 */
async function applyUpdate(): Promise<{ ok: boolean; needsRestart: boolean }> {
  if (!stagedDir) throw new Error("更新尚未暂存（先下载校验）");

  // 1) 后端热替换：停 → 旧改名 .old → 落新 → 重启。失败要回滚（别把用户后端弄没了）
  const newExe = join(stagedDir, "resources", "bin", "lxcode.exe");
  const currentExe = backendExePath();
  if (existsSync(newExe) && currentExe && existsSync(currentExe)) {
    ulog("后端热替换开始", currentExe);
    const oldPath = `${currentExe}.old`;
    try {
      await rmWithRetry(oldPath, "清理旧后端备份（lxcode.exe.old）", false);
      // 运行中的 exe 不能覆盖，但能改名（Windows 允许 rename 正在执行的映像文件）
      renameSync(currentExe, oldPath);
    } catch (e) {
      // 改名失败 = 旧后端文件被别的句柄锁住（不是自身进程——Windows 允许 rename
      // 运行中的映像；常见是杀软/备份软件占用）。此时新旧都还在原位，直接报人话。
      ulog("旧后端改名失败", e instanceof Error ? e.message : String(e));
      throw humanFsError(e, "后端替换失败：无法改名旧后端 lxcode.exe（文件被占用）");
    }
    try {
      loadOriginalFs().copyFileSync(newExe, currentExe);
    } catch (e) {
      renameSync(oldPath, currentExe); // 回滚
      ulog("新后端落位失败，已回滚", e instanceof Error ? e.message : String(e));
      throw humanFsError(e, "后端替换失败（已回滚，旧后端原样保留）");
    }
    const ok = await restartBackend();
    if (!ok) throw new Error("后端已替换但重启失败——查看 userData/backend.log");
    ulog("后端热替换完成");
  }

  // 2) asar 冷替换排程（仅产线）：暂存 + 脱离进程的替换脚本（等壳退出 → 换 → 重启壳）
  //    注意 newAsar 的判定与拷贝必须走 originalFs：Electron 补丁版 fs 对 *.asar 路径
  //    会当归档解析（existsSync 语义漂移、copyFileSync 直接 ENOENT）。
  const newAsar = join(stagedDir, "resources", "app.asar");
  if (loadOriginalFs().existsSync(newAsar) && app.isPackaged) {
    const pending = join(app.getPath("userData"), "update-pending");
    try {
      await rmWithRetry(pending, "清理冷替换暂存目录（update-pending）", true);
      mkdirSync(pending, { recursive: true });
      loadOriginalFs().copyFileSync(newAsar, join(pending, "app.asar"));
    } catch (e) {
      throw humanFsError(e, "asar 冷替换暂存失败");
    }
    const asar = join(process.resourcesPath, "app.asar");
    const installDir = join(process.resourcesPath, "..");
    const script = [
      "@echo off",
      ":wait",
      `tasklist /FI "IMAGENAME eq ${app.getName()}.exe" | find /I "${app.getName()}.exe" >nul && (timeout /t 1 /nul >nul & goto wait)`,
      `move /y "${asar}" "${asar}.old"`,
      `copy /y "${join(pending, "app.asar")}" "${asar}"`,
      `if exist "${asar}.old" del "${asar}.old"`,
      `start "" "${join(installDir, `${app.getName()}.exe`)}"`,
    ].join("\r\n");
    const scriptPath = join(pending, "apply-update.cmd");
    try {
      writeFileSync(scriptPath, script, "utf8");
    } catch (e) {
      throw humanFsError(e, "冷替换脚本写入失败");
    }
    spawn("cmd", ["/d", "/s", "/c", scriptPath], { detached: true, stdio: "ignore", windowsHide: true }).unref();
    ulog("asar 冷替换已排程", "壳退出后由 apply-update.cmd 完成");
  }

  // 清理本次的暂存子目录（exe 已落位、asar 已暂存到 pending）。更新本身已完成，
  // 清理失败（占用残留）只记日志——之后由 cleanupStaleStaging 兜底重清。
  await rmWithRetry(stagedRoot ?? stagedDir, "清理更新暂存子目录", true, true);
  stagedDir = null;
  stagedRoot = null;
  ulog("更新应用完成", "needsRestart=true");
  return { ok: true, needsRestart: true };
}

function broadcast(progress: { percent: number; downloaded: number; total: number }): void {
  for (const win of BrowserWindow.getAllWindows()) {
    win.webContents.send("update:progress", progress);
  }
}

// ---- 运行期周期检查（update-poll.ts 的装配；纯逻辑在 update-poll.ts 单测） ----

let poller: UpdatePoller | null = null;

/** 启动运行期周期检查（main.ts 装配时调一次）：
 *  - 首次检查延迟一个完整周期（6h+抖动）——启动时的检查由渲染层负责，不重复；
 *  - 发现新版本：update:available 推给渲染层（Toast/设置角标消费）+ 托盘气泡，
 *    都是非侵入提示，不打断（不弹模态）；manifest 已留在 lastManifest，渲染层
 *    走既有 download/apply 管线，语义不变；
 *  - 检查失败静默（网络差不能烦人；checkUpdate 已落 update.log 有迹可查）。
 *  与「更新必须重启才生效」的既有语义协调：提示文案由渲染层/托盘给出
 *  （「重启后生效」），这里只负责发现与通知。 */
export function startUpdatePolling(): void {
  if (poller) return;
  poller = createUpdatePoller({
    check: () => checkUpdate(),
    nextDelay: () => nextCheckDelayMs(),
    onAvailable: () => {
      const m = lastManifest;
      if (!m) return;
      const view = { version: m.version, size: m.size, notes: m.notes ?? [] };
      for (const win of BrowserWindow.getAllWindows()) {
        win.webContents.send("update:available", view);
      }
      notifyTrayUpdate(m.version);
      ulog("周期检查发现新版本", `${m.version}（已通知渲染层与托盘）`);
    },
    setTimer: (fn, ms) => setTimeout(fn, ms),
    clearTimer: (h) => clearTimeout(h as NodeJS.Timeout),
  });
  poller.start();
}

/** 停止周期检查（will-quit 收尾——不留悬挂定时器）。 */
export function stopUpdatePolling(): void {
  poller?.stop();
  poller = null;
}
