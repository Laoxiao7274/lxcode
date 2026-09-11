// dev.mjs —— 一条命令启动完整开发环境：
// 1. 编译 Go 后端 → 复制到 Tauri 旁边
// 2. 启动 vite dev server (:5190)
// 3. cargo build Tauri → 启动窗口
// 用法: node scripts/dev.mjs
import { spawn, execSync } from "node:child_process";
import { existsSync, mkdirSync, copyFileSync } from "node:fs";
import { join } from "node:path";

const ROOT = process.cwd();
const FRONTEND = join(ROOT, "frontend");
const SRC_TAURI = join(FRONTEND, "src-tauri");
const DEBUG_DIR = join(SRC_TAURI, "target", "debug");

console.log("=== lxcode dev 启动 ===\n");

// 1. 编译 Go 后端
console.log("[1/3] 编译 Go 后端...");
try {
  execSync("go build -o lxcode-backend.exe ./cmd/lxcode", { cwd: ROOT, stdio: "inherit" });
} catch {
  console.error("Go 后端编译失败");
  process.exit(1);
}

// 2. 复制后端 + config 到 Tauri debug 目录
console.log("[2/3] 复制后端到 Tauri debug 目录...");
const backendDst = join(DEBUG_DIR, "lxcode-backend.exe");
const configDir = join(DEBUG_DIR, "config");
const sessionsDir = join(DEBUG_DIR, "sessions");
mkdirSync(configDir, { recursive: true });
mkdirSync(sessionsDir, { recursive: true });
copyFileSync(join(ROOT, "lxcode-backend.exe"), backendDst);
if (existsSync(join(ROOT, "config", "local.json"))) {
  copyFileSync(join(ROOT, "config", "local.json"), join(configDir, "local.json"));
}

// 3. 启动 vite（后台）→ cargo build Tauri → 打开窗口
console.log("[3/3] 启动 Tauri 开发窗口...\n");

// vite 已在跑的话跳过
const vite = spawn("npx", ["vite"], {
  cwd: FRONTEND,
  stdio: "pipe",
  shell: true,
  detached: true,
});
vite.unref(); // 后台跑

// 等 vite 就绪再启 Tauri
setTimeout(() => {
  const tauri = spawn("cargo", ["build"], {
    cwd: SRC_TAURI,
    stdio: "inherit",
    shell: true,
  });
  tauri.on("close", (code) => {
    if (code !== 0) {
      console.error("Tauri 构建失败");
      process.exit(1);
    }
    console.log("\n✓ Tauri 构建完成，启动窗口...\n");
    const win = spawn(join(DEBUG_DIR, "deps", "lxcode.exe"), [], {
      cwd: join(DEBUG_DIR, "deps"),
      stdio: "inherit",
      detached: true,
    });
    win.unref();
    console.log(`\n=== 开发环境就绪 ===`);
    console.log(`  Tauri 窗口已启动（后端自动 spawn）`);
    console.log(`  vite dev: http://localhost:5190`);
    console.log(`  按 Ctrl+C 退出（窗口关闭时后端自动清理）\n`);
  });
}, 2000);
