// build.mjs —— 生产构建完整流程：
// 1. npm run build（前端产物 → dist/）
// 2. go build -ldflags="-s -w"（后端 release 二进制）
// 3. cargo build --release（Tauri release exe）
// 4. 复制后端 + config 到 release 目录
// 5. （可选）tauri build --bundles nsis（NSIS 安装包）
// 用法: node scripts/build.mjs [--nsis]
import { spawn, execSync } from "node:child_process";
import { copyFileSync, mkdirSync, existsSync } from "node:fs";
import { join } from "node:path";

const ROOT = process.cwd();
const FRONTEND = join(ROOT, "frontend");
const SRC_TAURI = join(FRONTEND, "src-tauri");
const RELEASE_DIR = join(SRC_TAURI, "target", "release");
const NSIS = process.argv.includes("--nsis");

console.log("=== lxcode 生产构建 ===\n");

// 1. 前端
console.log("[1/4] 前端构建...");
execSync("npm run build", { cwd: FRONTEND, stdio: "inherit" });

// 2. Go 后端（release + strip）
console.log("\n[2/4] Go 后端构建...");
execSync('go build -ldflags "-s -w" -o lxcode-backend.exe ./cmd/lxcode', { cwd: ROOT, stdio: "inherit" });

// 3. Tauri（release）
console.log("\n[3/4] Tauri release 构建...");
execSync("cargo build --release", { cwd: SRC_TAURI, stdio: "inherit" });

// 4. 复制后端 + config
console.log("\n[4/4] 复制文件...");
const configDir = join(RELEASE_DIR, "config");
const sessionsDir = join(RELEASE_DIR, "sessions");
mkdirSync(configDir, { recursive: true });
mkdirSync(sessionsDir, { recursive: true });
copyFileSync(join(ROOT, "lxcode-backend.exe"), join(RELEASE_DIR, "lxcode-backend.exe"));
if (existsSync(join(ROOT, "config", "local.json"))) {
  copyFileSync(join(ROOT, "config", "local.json"), join(configDir, "local.json"));
}

console.log(`\n✓ 构建完成`);
console.log(`  Tauri exe: ${join(RELEASE_DIR, "lxcode.exe")}`);
console.log(`  后端:      ${join(RELEASE_DIR, "lxcode-backend.exe")}`);
console.log(`  前端产物:  ${join(FRONTEND, "dist")}`);

if (NSIS) {
  console.log("\n[NSIS] 打包安装包...");
  execSync("cargo tauri build --bundles nsis", { cwd: SRC_TAURI, stdio: "inherit" });
  console.log("✓ NSIS 安装包已生成");
}
