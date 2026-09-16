// dev.mjs —— 一条命令启动完整开发环境（浏览器全栈模式）：
// 1. 编译 Go 后端 → bin/lxcode.exe（bin/ 已 gitignore）
// 2. 启动后端 --serve（config/local.json + temp/smoke-sessions，127.0.0.1:7789）
// 3. 启动 vite dev server（:5190）
// 前端 getAgentSource() 探测后端在线 → WSAgent 真实模式；后端不在则 DemoAgent。
// Electron 壳动工后在此追加壳的启动步骤。
// 用法: node scripts/dev.mjs（或 frontend 下 npm run dev:all）
import { spawn, execSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const ROOT = process.cwd();
console.log("=== lxcode dev 启动 ===\n");

// 1. 编译后端
console.log("[1/3] 编译 Go 后端...");
mkdirSync(join(ROOT, "bin"), { recursive: true });
try {
  execSync("go build -o bin/lxcode.exe ./cmd/lxcode", { cwd: ROOT, stdio: "inherit" });
} catch {
  console.error("Go 后端编译失败");
  process.exit(1);
}

// 2. 启动后端（本地配置优先——含 API key，gitignored）
console.log("[2/3] 启动后端 127.0.0.1:7789...");
const useLocal = existsSync(join(ROOT, "config", "local.json"));
const backend = spawn(join(ROOT, "bin", "lxcode.exe"), [
  "--serve",
  ...(useLocal ? ["--config", "config/local.json"] : []),
  "--sessions", "temp/smoke-sessions",
], { cwd: ROOT, stdio: "inherit" });

// 3. 启动 vite（前台挂着，Ctrl+C 全体收尾）
console.log("[3/3] 启动 vite...\n");
const vite = spawn("npx", ["vite"], { cwd: join(ROOT, "frontend"), stdio: "inherit", shell: true });

const bye = () => {
  backend.kill();
  vite.kill();
};
process.on("exit", bye);
process.on("SIGINT", () => process.exit(0));

vite.on("close", (code) => process.exit(code ?? 0));
