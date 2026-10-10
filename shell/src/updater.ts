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
import { appendFileSync, copyFileSync, createWriteStream, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { pipeline } from "node:stream/promises";
import { Readable, Transform } from "node:stream";
import { app, BrowserWindow, ipcMain } from "electron";
import { backendExePath, restartBackend } from "./sidecar";

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

const sha256 = (p: string): string => createHash("sha256").update(readFileSync(p)).digest("hex");

// ---- 日志与错误包装 ----
// 2026-10-09 EPERM 事故：Node 的 rmSync 在 Windows 上删被占用的目录时，报错只有
// 裸 `EPERM ... \\?\C:\...\update-staging`——路径指向顶层目录而不是被锁的具体文件，
// 用户既看不出卡在哪一步也读不懂原因。两条对策：每一步落日志 + 所有 fs 错误包人话。

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
    return new Error(`${what}：文件被占用或权限不足。常见原因是杀毒软件正在扫描刚下载/解压的文件——请等十几秒重试；` +
      `若反复失败，退出 Lxcode 后手动删除更新缓存目录（Roaming\\lxcode 下的 update-staging / update-pending）再重试。`);
  }
  if (code === "EBUSY") return new Error(`${what}：文件正被其他程序使用，请稍后重试。`);
  if (code === "ENOSPC") return new Error(`${what}：磁盘空间不足，请清理后重试。`);
  return new Error(`${what}失败：${raw}`);
}

/** rmSync 带自实现退避重试。Node 自带的 maxRetries 在 Windows 上对目录树 EPERM
 *  不生效（实测 v24 1ms 内直接抛，一次都不重试），而杀软占用通常几百 ms~几秒——
 *  自己退避（8 次 × 250ms ≈ 2s 窗口）才真有用。非占用类错误立即包装上抛。 */
async function rmWithRetry(path: string, what: string, recursive: boolean): Promise<void> {
  for (let i = 0; ; i++) {
    try {
      rmSync(path, { recursive, force: true });
      return;
    } catch (e) {
      const code = (e as NodeJS.ErrnoException)?.code ?? "";
      const transient = code === "EPERM" || code === "EBUSY" || code === "ENOTEMPTY";
      if (!transient || i >= 7) throw humanFsError(e, what);
      ulog("删除被占用，退避重试", `${what}（第 ${i + 1} 次，code=${code}）`);
      await new Promise((r) => setTimeout(r, 250));
    }
  }
}

// 状态：检查到的可用更新（download 消费）+ 下载完成并校验通过的暂存目录（apply 消费）
let lastManifest: UpdateManifest | null = null;
let stagedDir: string | null = null;
let downloading = false; // 并发防护：下载中再点下载，第二次的 rmSync 会撞上第一次打开中的 zip 句柄 → EPERM

/** 装配 IPC 面（main.ts 启动时调一次）。manifest 由主进程持有——渲染层不传
 *  更新元数据（防伪造：url/哈希只来自主进程拉取的 manifest）。 */
export function updateHandlers(): void {
  ipcMain.handle("update:version", () => app.getVersion());
  ipcMain.handle("update:check", () => checkUpdate());
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

/** 下载 + 整包 sha256 校验 + 解压 + 逐文件校验 → 暂存目录就绪。 */
async function downloadAndStage(): Promise<{ ok: boolean }> {
  if (!lastManifest) throw new Error("没有待下载的更新（先 check）");
  if (downloading) throw new Error("已有一次更新下载正在进行，请等它结束再试");
  downloading = true;
  const manifest = lastManifest;
  try {
    ulog("开始下载", `目标版本 ${manifest.version}`);
    const base = join(app.getPath("userData"), "update-staging");
    // 每次进入都清空暂存目录：上一次失败/中断的残留（半截 zip、解压一半的 payload）
    // 不能留着——否则本次校验对象可能混入旧文件。被占用时重试后退避，仍失败报人话。
    await rmWithRetry(base, "清理更新暂存目录（update-staging）", true);
    ulog("暂存目录已清理", base);
    mkdirSync(base, { recursive: true });
    const zipPath = join(base, "update.zip");

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
    const extractDir = join(base, "payload");
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
        if (!existsSync(f)) throw new Error(`更新包缺文件: ${rel}`);
        if (sha256(f) !== expect) throw new Error(`文件校验失败: ${rel}`);
      }
    }
    ulog("逐文件校验通过", `${Object.keys(manifest.files ?? {}).length} 个文件`);
    stagedDir = extractDir;
    // zip 只服务于解压，删不删都不影响本次更新（下次下载会整体清空）——被占用
    // （杀软正在扫它）时不应让整个下载报错，降级为日志。
    try {
      await rmWithRetry(zipPath, "清理暂存 zip", false);
    } catch (e) {
      ulog("暂存 zip 清理失败（不影响本次更新）", e instanceof Error ? e.message : String(e));
    }
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
      copyFileSync(newExe, currentExe);
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
  const newAsar = join(stagedDir, "resources", "app.asar");
  if (existsSync(newAsar) && app.isPackaged) {
    const pending = join(app.getPath("userData"), "update-pending");
    try {
      await rmWithRetry(pending, "清理冷替换暂存目录（update-pending）", true);
      mkdirSync(pending, { recursive: true });
      copyFileSync(newAsar, join(pending, "app.asar"));
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

  // 清理暂存（exe 已落位、asar 已暂存到 pending）。更新本身已完成，清理失败
  // （杀软占用暂存里的 exe）只记日志——下次 download 进入时会再整体清空。
  try {
    await rmWithRetry(stagedDir, "清理更新暂存目录（update-staging）", true);
  } catch (e) {
    ulog("暂存清理失败（不影响本次更新，下次下载时重清）", e instanceof Error ? e.message : String(e));
  }
  stagedDir = null;
  ulog("更新应用完成", "needsRestart=true");
  return { ok: true, needsRestart: true };
}

function broadcast(progress: { percent: number; downloaded: number; total: number }): void {
  for (const win of BrowserWindow.getAllWindows()) {
    win.webContents.send("update:progress", progress);
  }
}
