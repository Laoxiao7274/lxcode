// dev.mjs —— 一条命令启动完整开发环境：
//   node scripts/dev.mjs            浏览器全栈模式（后端 + vite，浏览器打开 5190）
//   node scripts/dev.mjs --electron 壳开发模式（再加 Electron 窗口加载 dev server）
// 流程：1. 编译 Go 后端 → bin/lxcode.exe（bin/ 已 gitignore）
//      2. 壳模式下编译壳主进程 TS → shell/dist/main.js（esbuild，秒级）
//      3. 启动后端 --serve（config/local.json + temp/smoke-sessions，127.0.0.1:7789）
//      4. 启动 vite dev server（:5190）；壳模式等它就绪后拉起 Electron
// 渲染层 getAgentSource() 探测环境：壳里 WSAgent 连真实后端；浏览器跑 DemoAgent。
// 进程纪律：vite/electron 都用 node 直启其 cli.js（不经 npx.cmd/cmd 包装——
// kill 包装层会留下孙进程孤儿，冒烟实测踩过）；退出统一 taskkill /T 杀整棵树。
import { spawn, spawnSync, execSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import net from "node:net";

const ROOT = process.cwd();
const SHELL = join(ROOT, "shell");
const FRONTEND = join(ROOT, "frontend");
const ELECTRON = process.argv.includes("--electron");
const VITE_PORT = 5190;

console.log(`=== lxcode dev 启动${ELECTRON ? "（壳模式）" : ""} ===\n`);

// 1. 编译后端
console.log("[1] 编译 Go 后端...");
mkdirSync(join(ROOT, "bin"), { recursive: true });
try {
  execSync("go build -o bin/lxcode.exe ./cmd/lxcode", { cwd: ROOT, stdio: "inherit" });
} catch {
  console.error("Go 后端编译失败");
  process.exit(1);
}

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

// 3. 启动后端（本地配置优先——含 API key，gitignored）
console.log(`[${ELECTRON ? 3 : 2}] 启动后端 127.0.0.1:7789...`);
const useLocal = existsSync(join(ROOT, "config", "local.json"));
const backend = spawn(join(ROOT, "bin", "lxcode.exe"), [
  "--serve",
  ...(useLocal ? ["--config", "config/local.json"] : []),
  "--sessions", "temp/smoke-sessions",
], { cwd: ROOT, stdio: "inherit" });

// 4. 启动 vite（前台挂着，Ctrl+C 全体收尾；node 直启 cli.js，不经 cmd 包装）
console.log(`[${ELECTRON ? 4 : 3}] 启动 vite...\n`);
const vite = spawn(process.execPath, [join(FRONTEND, "node_modules", "vite", "bin", "vite.js")], {
  cwd: FRONTEND,
  stdio: "inherit",
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
        LXCODE_BACKEND_EXE: join(ROOT, "bin", "lxcode.exe"),
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
