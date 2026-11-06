// dev.mjs —— 一条命令启动完整开发环境：
//   node scripts/dev.mjs            浏览器全栈模式（后端 + vite，浏览器打开 5190）
//   node scripts/dev.mjs --electron 壳开发模式（再加 Electron 窗口加载 dev server）
// 流程：1. 编译 Go 后端 → bin/lxcode.exe（bin/ 已 gitignore）
//      2. 壳模式下编译壳主进程 TS → shell/dist/main.js（esbuild，秒级）
//      3. 启动后端 --serve（config/local.json + OS 应用数据目录下的 dev 会话，127.0.0.1:7790）
//      4. 启动 vite dev server（:5190）；壳模式等它就绪后拉起 Electron
// 渲染层 getAgentSource() 探测环境：壳里 WSAgent 连真实后端；浏览器跑 DemoAgent。
//
// 与生产实例完全隔离（2026-12 复盘拍板——事故：dev 后端抢生产 7789 端口，子会话
// 工具误杀了生产后端 PID）：
//   端口      dev 后端监听 127.0.0.1:7790（生产 SCM 服务恒在 7789，永不冲突；
//             先例：local-myt-agent 用 7788，同样错开）
//   会话目录  %LOCALAPPDATA%/lxcode-dev/sessions（devSessionsDir——与生产
//             %ProgramData%/lxcode/sessions 完全两份，互不可见）
//   配置      仓库 config/local.json（gitignored，生产走 %ProgramData%/lxcode/config）
// 浏览器 live 模式连 7790 的通路：vite 以 LXCODE_DEV_BACKEND_PORT 环境变量把端口
// 传进前端（frontend/src/agent/index.ts 的 defaultLiveAddr 读 import.meta.env——
// 仅 dev server 构建存在该值，打包产物不受影响）。
// 进程纪律：vite/electron 都用 node 直启其 cli.js（不经 npx.cmd/cmd 包装——
// kill 包装层会留下孙进程孤儿，冒烟实测踩过）；退出 Windows 统一 taskkill /T
// 杀整棵树，darwin/linux 直接 SIGTERM（子进程都是 node 直启、无中间包装层，
// 树深只有一层；杀不干净的残留由后端 --stdin-watch 兜底：vite/壳死亡 →
// 管道 EOF → 后端优雅退出）。

// 后端产物名按平台（darwin/linux 无 .exe——与 shell/src/sidecar.ts 同一规则）。
const BACKEND_BIN = process.platform === "win32" ? "lxcode.exe" : "lxcode";
import { spawn, spawnSync, execSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import net from "node:net";
import { readHead } from "./check-stack.mjs";

const ROOT = process.cwd();
const SHELL = join(ROOT, "shell");
const FRONTEND = join(ROOT, "frontend");
const ELECTRON = process.argv.includes("--electron");
const VITE_PORT = 5190;

// 开发态会话目录必须落在**仓库之外**。会话的项目工作树是
// <sessions>/worktrees/<项目>/<会话>——那是别的项目的**完整检出**，一旦落在
// lxcode 仓库里就混进了本模块：go build ./... 会去编译它们的散文件
// （报 expected 'package', found 'EOF'），gofmt -l . 会列出上千个外来文件
// （2026-09-28 实测 1730 个）。gofmt 既不认 Go 模块边界、也不跳下划线/点号
// 前缀目录（实测都试过），所以唯一稳的解法是让这些文件根本不在树里。
// 放 OS 应用数据目录，与壳的 userData 同一条纪律；LXCODE_DEV_SESSIONS 可覆盖。
function devSessionsDir() {
  if (process.env.LXCODE_DEV_SESSIONS) return process.env.LXCODE_DEV_SESSIONS;
  const base = process.platform === "win32"
    ? process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local")
    : process.env.XDG_DATA_HOME || join(homedir(), ".local", "share");
  return join(base, "lxcode-dev", "sessions");
}

// dev 壳的 userData（main.ts 读 LXCODE_DEV_USER_DATA 时 app.setPath 重定向）。
// 与生产壳 %APPDATA%/lxcode 完全两份：单实例锁、models.json/sessions/frpc/
// 更新暂存等一切落 userData 的状态互不可见——dev/生产两壳可并存。与
// devSessionsDir 同一条纪律：落在仓库之外、OS 应用数据目录。
function devUserDataDir() {
  const base = process.platform === "win32"
    ? process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local")
    : process.env.XDG_DATA_HOME || join(homedir(), ".local", "share");
  return join(base, "lxcode-dev", "shell");
}

console.log(`=== lxcode dev 启动${ELECTRON ? "（壳模式）" : ""} ===\n`);

// 1. 编译后端
console.log("[1] 编译 Go 后端...");
mkdirSync(join(ROOT, "bin"), { recursive: true });
try {
  execSync(`go build -o bin/${BACKEND_BIN} ./cmd/lxcode`, { cwd: ROOT, stdio: "inherit" });
} catch {
  console.error("Go 后端编译失败");
  process.exit(1);
}
// 打印编译所用的提交：实测事故（前端热的、后端不热——栈里跑着几天前的后端）
// 的全部代价就在「当前栈是哪个提交」不可答。一行日志把它变成可见事实；
// 事后核对用 node scripts/check-stack.mjs（比较二进制内嵌版本与 HEAD）。
const rev = readHead();
console.log(`    后端编译于 ${rev ? rev.slice(0, 8) : "(未知——非 git 仓库)"}`);

// 2. 壳模式下编译壳主进程（electron 依赖必须先 npm install 过）
let electron = null;
if (ELECTRON) {
  console.log("[2] 编译壳主进程...");
  if (!existsSync(join(SHELL, "node_modules"))) {
    console.error("shell/ 依赖未安装：先在 shell/ 下跑 npm install");
    process.exit(1);
  }
  try {
    execSync("node build.mjs", { cwd: SHELL, stdio: "inherit" });
  } catch {
    console.error("壳主进程编译失败");
    process.exit(1);
  }
}

// 3. 启动后端（本地配置优先——含 API key，gitignored）。
// 端口恒 7790（与生产 7789 错开，见文件头隔离说明）；--stdin-watch 兜底：
// 本进程（或它起的 vite/壳）死亡 → 管道 EOF → 后端优雅退出，不留孤儿。
const BACKEND_PORT = 7790;
const BACKEND_ADDR = `127.0.0.1:${BACKEND_PORT}`;
console.log(`[${ELECTRON ? 3 : 2}] 启动后端 ${BACKEND_ADDR}...`);
const useLocal = existsSync(join(ROOT, "config", "local.json"));
const sessionsDir = devSessionsDir();
mkdirSync(sessionsDir, { recursive: true });
console.log(`    会话目录 ${sessionsDir}`);
// 安全提示（不阻塞）：7789 被占 = 生产后端在线——dev 栈已在 7790 隔离，二者互不干扰；
// 打出来是防止误判「dev 没连上后端」时跑去动生产进程。
if (await portBusy(7789)) {
  console.log(`    提示：生产后端在 127.0.0.1:7789 运行中，dev 栈使用 ${BACKEND_ADDR} 隔离，二者互不影响`);
}
const backend = spawn(join(ROOT, "bin", BACKEND_BIN), [
  "--serve",
  "--addr", BACKEND_ADDR,
  ...(useLocal ? ["--config", "config/local.json"] : []),
  "--sessions", sessionsDir,
  "--stdin-watch",
  // stdin 必须是本进程持有的 pipe（不能 inherit 终端）：--stdin-watch 靠「宿主
  // 死亡 → 管道 EOF」触发优雅退出——inherit 时后端拿的是控制台输入缓冲，
  // dev.mjs 被杀它也不会 EOF，孤儿兜底落空（与 shell/src/sidecar.ts 同一手法）。
  // 本进程从不写 stdin，正常退出走 process.on("exit") 的 killTree。
], { cwd: ROOT, stdio: ["pipe", "inherit", "inherit"] });

// 4. 启动 vite（前台挂着，Ctrl+C 全体收尾；node 直启 cli.js，不经 cmd 包装）。
// LXCODE_DEV_BACKEND_PORT 把 dev 后端端口传进前端（import.meta.env 只在 dev
// server 构建存在——浏览器 live 模式默认连 dev 后端而非生产 7789）。
console.log(`[${ELECTRON ? 4 : 3}] 启动 vite...\n`);
const vite = spawn(process.execPath, [join(FRONTEND, "node_modules", "vite", "bin", "vite.js")], {
  cwd: FRONTEND,
  stdio: "inherit",
  env: {
    ...process.env,
    LXCODE_DEV_BACKEND_PORT: String(BACKEND_PORT),
  },
});

// 5. 壳模式：vite 就绪后拉起 Electron（同样 node 直启 cli.js）
if (ELECTRON) {
  waitForPort(VITE_PORT, 30_000).then(() => {
    console.log("[5] 拉起 Electron 壳...");
    electron = spawn(process.execPath, [join(SHELL, "node_modules", "electron", "cli.js"), "."], {
      cwd: SHELL,
      stdio: "inherit",
      env: {
        ...process.env,
        LXCODE_DEV_URL: `http://127.0.0.1:${VITE_PORT}`,
        LXCODE_BACKEND_EXE: join(ROOT, "bin", BACKEND_BIN),
        // 后端地址：sidecar/backend-remote/screen/tailscale/sakura 五处读它——
        // 也顺带修掉 7789 离线时 dev 壳 spawn 后端 --addr 7789 撞生产端口的 bug。
        LXCODE_ADDR: BACKEND_ADDR,
        // 独立 userData（见 devUserDataDir 注释）：单实例锁随之隔离，两壳并存。
        LXCODE_DEV_USER_DATA: devUserDataDir(),
        // CDP 调试口与生产/dev 常驻壳的默认 9229 错开（main.ts 读它）。
        LXCODE_CDP_PORT: "9230",
      },
    });
    electron.on("close", (code) => process.exit(code ?? 0));
  });
}

const bye = () => {
  killTree(backend);
  killTree(vite);
  killTree(electron);
};
process.on("exit", bye);
process.on("SIGINT", () => process.exit(0));

vite.on("close", (code) => process.exit(code ?? 0));

/** 杀整棵进程树（kill 包装层会孤儿化孙进程；Windows 用 taskkill /T）。 */
function killTree(p) {
  if (!p || p.pid === undefined || p.exitCode !== null) return;
  if (process.platform === "win32") {
    spawnSync("taskkill", ["/PID", String(p.pid), "/T", "/F"], { stdio: "ignore" });
  } else {
    p.kill("SIGTERM");
  }
}

/** 端口是否已被监听（一次连接探测，不等待）。 */
function portBusy(port) {
  return new Promise((resolve) => {
    const s = net.connect(port, "127.0.0.1");
    s.once("connect", () => { s.destroy(); resolve(true); });
    s.once("error", () => { s.destroy(); resolve(false); });
  });
}

/** 轮询等端口可连（vite 就绪信号）。 */
function waitForPort(port, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    (function tryOnce() {
      const s = net.connect(port, "127.0.0.1");
      s.once("connect", () => { s.destroy(); resolve(); });
      s.once("error", () => {
        s.destroy();
        if (Date.now() > deadline) return reject(new Error(`端口 ${port} 等待超时`));
        setTimeout(tryOnce, 200);
      });
    })();
  });
}
