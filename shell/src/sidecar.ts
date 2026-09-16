// sidecar.ts —— Go 后端生命周期（壳主进程唯一"业务"）：
// 探测 7789 → 已在线则直连（SCM 服务/旧实例，绝不重复 spawn）→
// 离线才拉起捆绑的后端 exe。优雅退出由 main 的 will-quit 触发；
// 壳被强杀的兜底是 Windows Job Object（KILL_ON_JOB_CLOSE 连带杀子进程）。
//
// 纪律（AGENTS.md §2.1）：必须显式传 --config/--sessions 指向 userData——
// 否则后端配置解析顺序会落到 %ProgramData% 安装形态配置，两形态数据串台。
import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, openSync } from "node:fs";
import { join } from "node:path";
import { app, dialog } from "electron";

const ADDR = process.env.LXCODE_ADDR ?? "127.0.0.1:7789";
const HEALTH_URL = `http://${ADDR}/health`;
const HEALTH_TIMEOUT_MS = 800;
const START_TIMEOUT_MS = 20_000;

let child: ChildProcess | null = null;
let managed = false; // 是否由本壳拉起（退出时才杀；直连的服务/旧实例不动）

/** 探测 + 必要时拉起后端，返回是否可用。 */
export async function ensureBackend(): Promise<boolean> {
  if (await probeHealth()) {
    console.log(`[shell] 后端已在线（${ADDR}），直连不拉起`);
    return true;
  }
  const exe = backendExePath();
  if (!exe) {
    showGuidance();
    return false;
  }
  await spawnBackend(exe);
  const ok = await waitBackendUp();
  if (ok) console.log(`[shell] 后端已拉起: ${exe}`);
  else showGuidance();
  return ok;
}

/** 优雅收尾：只杀自己拉起的后端（直连的 SCM 服务不归壳管）。 */
export function shutdownBackend(): void {
  if (managed && child) {
    child.kill();
    console.log("[shell] 已请求后端退出");
  }
}

// ---- 内部实现 ----

/** 探测 /health（单次）。 */
function probeHealth(): Promise<boolean> {
  return fetch(HEALTH_URL, { signal: AbortSignal.timeout(HEALTH_TIMEOUT_MS) })
    .then((r) => r.ok)
    .catch(() => false);
}

/** 后端 exe 路径：dev 由 dev.mjs 注入环境变量；产线取 extraResources。 */
function backendExePath(): string | null {
  const dev = process.env.LXCODE_BACKEND_EXE;
  if (dev && existsSync(dev)) return dev;
  if (app.isPackaged) {
    const bundled = join(process.resourcesPath, "bin", "lxcode.exe");
    if (existsSync(bundled)) return bundled;
  }
  return null;
}

/** 拉起后端：配置/会话固定在 userData，防串台到安装形态目录。 */
async function spawnBackend(exe: string): Promise<void> {
  const userData = app.getPath("userData");
  const configPath = join(userData, "models.json");
  const sessionsDir = join(userData, "sessions");
  mkdirSync(sessionsDir, { recursive: true });
  // config 文件不存在是合法首跑（config.Load 返回空注册表），不写种子；
  // 后端日志落 userData/backend.log（dev 下打进壳控制台更直观）
  const logFd = app.isPackaged
    ? openSync(join(userData, "backend.log"), "a")
    : null;
  child = spawn(exe, ["--serve", "--addr", ADDR, "--config", configPath, "--sessions", sessionsDir], {
    stdio: logFd !== null ? ["ignore", logFd, logFd] : "inherit",
    windowsHide: true, // 不弹后端控制台窗口
    // 不 detached：默认留在壳的 Job Object 里——壳被强杀时系统连带收走后端
  });
  managed = true;
  child.on("exit", (code) => {
    // 端口被占（双壳竞态）等早退场景：health 可能由赢的那个实例提供，不在这里下结论
    console.log(`[shell] 后端进程退出码 ${code}`);
  });
}

/** 等后端就绪：轮询 /health。进程早退不立即失败——可能另一个实例赢了端口。 */
async function waitBackendUp(): Promise<boolean> {
  const deadline = Date.now() + START_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (await probeHealth()) return true;
    await sleep(300);
  }
  return false;
}

/** 后端不可用时给启动指引（AGENTS.md：连不上给指引，不放业务）。 */
function showGuidance(): void {
  dialog.showErrorBox(
    "无法连接 lxcode 后端",
    `127.0.0.1:7789 未在线，且壳内没有可用的后端二进制。\n\n` +
      `排查建议：\n` +
      `1. 若装了服务形态：管理员 PowerShell 跑 sc start lxcode\n` +
      `2. 开发形态：node scripts/dev.mjs --electron\n` +
      `3. 安装包可能不完整，重新安装试试`,
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
