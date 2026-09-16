// build.mjs —— 一条命令产出 Windows 全套制品（仅 Windows，AGENTS.md §2.1 定案）：
//   node scripts/build.mjs   （或 ./build.sh）
// 流程：1. Go 后端 → shell/resources/bin/lxcode.exe（ldflags 烙版本号）
//      2. 渲染层 frontend → dist（tsc 类型检查 + vite build，base './'）
//      3. stage：frontend/dist → shell/renderer（electron-builder files 源）
//      4. 壳主进程 esbuild → shell/dist/main.js
//      5. electron-builder → NSIS one-click per-user + 签名（shell/release/）
//      6. 更新制品：update-<version>.zip（app.asar + 后端 exe）+ manifest.json
//
// 版本号机制：唯一版本源 = shell/package.json 的 version（electron-builder
// 原生读它出安装包名），本脚本把它烙进 Go 二进制（--version 可查）并写进
// manifest——三方（安装包/二进制/更新清单）永远同源。
//
// 更新包约定（客户端更新器未实现，先定产物契约）：
//   zip 内路径 = 安装目录相对路径（resources/app.asar、resources/bin/lxcode.exe）
//   manifest   = {version, url, sha256(zip), size, files{逐文件 sha256}}
//   Electron/Chromium 升级不走 zip（发全量安装包）。
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, rmSync, existsSync, readFileSync, writeFileSync, copyFileSync, statSync, readdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { createHash } from "node:crypto";

const ROOT = process.cwd();
const SHELL = join(ROOT, "shell");
const FRONTEND = join(ROOT, "frontend");

function run(label, cmd, args, opts = {}) {
  console.log(`\n=== ${label} ===`);
  const t0 = Date.now();
  const r = spawnSync(cmd, args, { cwd: opts.cwd || ROOT, stdio: "inherit", shell: opts.shell ?? true });
  const secs = ((Date.now() - t0) / 1000).toFixed(1);
  if (r.status !== 0) {
    console.error(`${label} 失败（exit ${r.status}）`);
    process.exit(1);
  }
  console.log(`--- ${label} 完成（${secs}s）`);
}

const sha256 = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");

console.log("=== lxcode 打包（Windows / NSIS one-click per-user） ===");

// 版本号（唯一源）
const { version } = JSON.parse(readFileSync(join(SHELL, "package.json"), "utf8"));
console.log(`版本: ${version}`);

// 前置检查：依赖装过没有
if (!existsSync(join(SHELL, "node_modules")) || !existsSync(join(FRONTEND, "node_modules"))) {
  console.error("依赖未安装：先在 shell/ 与 frontend/ 下各自 npm install");
  process.exit(1);
}

// 1. Go 后端（产物直接落 extraResources 源目录；版本号烙进二进制）
//    注意本步 shell:false——ldflags 的 -X 必须空格分隔（linker 语义），
//    走 shell:true 会被拆参（go/npm.cmd 这类才需要 shell）。
run(
  "Go 后端编译",
  "go",
  ["build", "-ldflags", `-X main.version=${version}`, "-o", "shell/resources/bin/lxcode.exe", "./cmd/lxcode"],
  { shell: false },
);

// 2. 渲染层（tsc -b 类型检查 + vite build）
run("渲染层构建", "npm", ["run", "build"], { cwd: FRONTEND });

// 3. stage 渲染层进壳目录（electron-builder 的 files 均相对 shell/，不跨目录引用）
rmSync(join(SHELL, "renderer"), { recursive: true, force: true });
mkdirSync(join(SHELL, "renderer"), { recursive: true });
cpSync(join(FRONTEND, "dist"), join(SHELL, "renderer"), { recursive: true });
console.log("--- 渲染层 stage 完成（frontend/dist → shell/renderer）");

// 4. 壳主进程
run("壳主进程构建", "node", ["build.mjs"], { cwd: SHELL });

// 5. electron-builder（NSIS + 签名）
//    先清 release/：产物目录每轮全新（否则旧 blockmap/旧版本安装包等残留物
//    会混在产物清单里，上一轮实测踩过）
rmSync(join(SHELL, "release"), { recursive: true, force: true });
run("electron-builder 打包", "npx", ["electron-builder", "--win", "--x64"], { cwd: SHELL });

// 6. 更新制品：update-<version>.zip + manifest.json
const unpacked = join(SHELL, "release", "win-unpacked");
const releaseDir = join(SHELL, "release");
const UPDATE_FILES = ["resources/app.asar", "resources/bin/lxcode.exe"]; // zip 内路径=安装目录相对路径
const zipName = `update-${version}.zip`;

console.log("\n=== 更新制品 ===");
// stage（zip 内要带 resources/ 目录结构，先聚到暂存目录）
const staging = join(releaseDir, ".update-staging");
rmSync(staging, { recursive: true, force: true });
for (const f of UPDATE_FILES) {
  const src = join(unpacked, f);
  if (!existsSync(src)) {
    console.error(`更新制品缺文件: ${src}（electron-builder 产物结构变了？）`);
    process.exit(1);
  }
  mkdirSync(dirname(join(staging, f)), { recursive: true });
  copyFileSync(src, join(staging, f));
}
// 用系统自带 bsdtar 造 zip（-a 按扩展名自动选 zip 格式，保留相对路径，零新增依赖）
run(`更新包 ${zipName}`, "tar", ["-a", "-c", "-f", zipName, "-C", ".update-staging", "resources"], { cwd: releaseDir });
rmSync(staging, { recursive: true, force: true });

const manifest = {
  version,
  url: zipName,
  sha256: sha256(join(releaseDir, zipName)),
  size: statSync(join(releaseDir, zipName)).size,
  files: Object.fromEntries(UPDATE_FILES.map((f) => [f, sha256(join(unpacked, f))])),
};
writeFileSync(join(releaseDir, "manifest.json"), JSON.stringify(manifest, null, 2));
console.log(`--- manifest.json 完成（版本 ${version}，${UPDATE_FILES.length} 个文件哈希）`);

// 产物清单
console.log("\n=== 全部完成，产物（shell/release/）===");
for (const f of readdirSync(releaseDir)) {
  const st = statSync(join(releaseDir, f));
  if (st.isFile()) console.log(`  ${f}（${(st.size / 1048576).toFixed(2)} MB）`);
}
console.log("  win-unpacked/（免安装版）");
console.log("发布：安装包+win-unpacked 全量分发；manifest.json+update-*.zip 供更新（Electron 升级发全量安装包）");
