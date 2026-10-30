// build.mjs —— 一条命令产出 Windows 全套制品（仅 Windows，AGENTS.md §2.1 定案）：
//   node scripts/build.mjs   （或 ./build.sh）
// 流程：1. Go 后端 → shell/resources/bin/lxcode.exe（ldflags 烙版本号）
//      2. 渲染层 frontend → dist（tsc 类型检查 + vite build，base './'）
//      3. stage：frontend/dist → shell/renderer（electron-builder files 源）
//      4. 壳主进程 esbuild → shell/dist/main.js
//      5. electron-builder → NSIS one-click per-user + 签名（shell/release/）
//      6. 更新制品：update-<version>.zip（app.asar + 后端 exe）+ manifest.json
//         （--mac 在 mac 上额外产出 update-mac-<version>.zip + manifest-mac.json，
//         平台契约见 makeUpdateArtifacts 与 shell/src/update-platform.ts）
//
// 版本号机制：唯一版本源 = shell/package.json 的 version（electron-builder
// 原生读它出安装包名），本脚本把它烙进 Go 二进制（--version 可查）并写进
// manifest——三方（安装包/二进制/更新清单）永远同源。
//
// 更新包约定（客户端更新器已实现——shell/src/updater.ts）：
//   zip 内路径 = 安装目录相对路径（resources/app.asar、resources/bin/lxcode.exe）
//   manifest   = {version, url, sha256(zip), size, files{逐文件 sha256}}
//   Electron/Chromium 升级不走 zip（发全量安装包）。
//
// 发版清单（打包前逐条过）：
//   1. bump shell/package.json 的 version——三方同源（安装包名/二进制 --version/
//      manifest）全靠它，忘了 bump = 更新器判不出新版本
//   2. 产物上传（**两个平台都传，同一更新源目录**）：
//      manifest.json + update-<version>.zip（Windows，客户端拉 manifest.json）
//      manifest-mac.json + update-mac-<version>.zip（mac，客户端拉 manifest-mac.json）
//      两份 manifest 字段同构、url 均为相对文件名；漏传 mac 的会让 mac 客户端
//      永远停在旧版，漏传 Windows 的会让存量 Windows 客户端永远停在旧版。
//      安装包全量分发
//   3. 签名证书：signtoolOptions 按 CN=lxcode-selfsign 找用户证书存储——
//      换构建机要先导入这张自签证书，否则 electron-builder 签名步骤失败
//   4. 卸载保留用户数据（deleteAppDataOnUninstall: false，已显式声明）——
//      Roaming\lxcode 下的 models.json/会话/frpc/token 不随卸载删除
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, rmSync, existsSync, readFileSync, writeFileSync, copyFileSync, statSync, readdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";

// 仓库根 = 脚本自身位置（scripts/ 的上一级），与调用时的 cwd 无关
const ROOT = fileURLToPath(new URL("..", import.meta.url));
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

// makeUpdateArtifacts —— 更新包 + manifest 的公共生成段（Windows 链与 --mac
// 链共用；2026-12 从原 Windows 内联段抽出，Windows 侧字段与顺序一字未动——
// 老客户端契约见下方 Windows 调用处的注释）。
//
// 契约（两端同构，客户端按平台拼 URL，见 shell/src/update-platform.ts）：
//   Windows：manifest.json     + update-<version>.zip（安装根相对路径）
//   mac：    manifest-mac.json + update-mac-<version>.zip（.app/Contents 相对路径）
//   manifest 字段 = {version, url, sha256(zip), size, files{逐文件 sha256}}，
//   url 为相对文件名，按 manifest 所在目录解析；发版时两份 manifest + 两个
//   zip 都上传到同一更新源目录。存量 Windows 客户端（只认 manifest.json 的
//   老版本）不受影响——本函数不改变它读到的任何字段。
function makeUpdateArtifacts({ unpacked, releaseDir, version, files, manifestName, zipName, stagingDir, backendSrc }) {
  console.log("\n=== 更新制品 ===");
  // stage（zip 内要带 resources/ 目录结构，先聚到暂存目录）
  const staging = join(releaseDir, stagingDir);
  rmSync(staging, { recursive: true, force: true });
  for (const f of files) {
    const src = join(unpacked, f);
    if (!existsSync(src) && backendSrc && f.endsWith("/lxcode")) {
      // mac：electron-builder 产物里 resources/bin 缺后端时从 bin-darwin 产物补齐
      mkdirSync(dirname(join(staging, f)), { recursive: true });
      copyFileSync(backendSrc, join(staging, f));
      continue;
    }
    if (!existsSync(src)) {
      console.error(`更新制品缺文件: ${src}（electron-builder 产物结构变了？）`);
      process.exit(1);
    }
    mkdirSync(dirname(join(staging, f)), { recursive: true });
    copyFileSync(src, join(staging, f));
  }
  // zip 生成用当前宿主的系统 tar：
  //   Windows 显式 System32 tar.exe（bsdtar，-a 按扩展名自动选 zip 格式）——
  //   Git Bash 的 PATH 会解析到 GNU tar，不认 -a 的 zip 语义，造出来的是
  //   tar 包改扩展名的假 zip（0.1.3 发版实测踩过，更新器解包失败）；
  //   mac 自带 bsdtar（`tar -a -c` 直接可用）。
  // System32 不存在时（非 Windows）回退 PATH 里的 tar。
  const bsdtar = "C:/Windows/System32/tar.exe";
  const tarBin = existsSync(bsdtar) ? bsdtar : "tar";
  run(`更新包 ${zipName}`, tarBin, ["-a", "-c", "-f", zipName, "-C", stagingDir, "resources"], { cwd: releaseDir });
  rmSync(staging, { recursive: true, force: true });

  const manifest = {
    version,
    url: zipName,
    sha256: sha256(join(releaseDir, zipName)),
    size: statSync(join(releaseDir, zipName)).size,
    files: Object.fromEntries(files.map((f) => {
      const src = join(unpacked, f);
      return [f, sha256(existsSync(src) ? src : backendSrc)]; // mac 的 bin/lxcode 从 bin-darwin 产物取哈希
    })),
  };
  writeFileSync(join(releaseDir, manifestName), JSON.stringify(manifest, null, 2));
  console.log(`--- ${manifestName} 完成（版本 ${version}，${files.length} 个文件哈希）`);
}

console.log("=== lxcode 打包（Windows / NSIS one-click per-user） ===");

// 版本号（唯一源）
const { version } = JSON.parse(readFileSync(join(SHELL, "package.json"), "utf8"));
console.log(`版本: ${version}`);

// --backend-darwin：只产出后端 darwin 交叉编译二进制（落 shell/release/，与
// update zip 产物同级），不跑渲染层/ffmpeg/electron-builder/tar——macOS 远程
// 使用场景：后端二进制在 mac 上独立跑 + CLI/浏览器接入。默认（无参数）行为
// 保持不变。放在依赖前置检查之前：纯 Go 产物不需要 shell/ 与 frontend/ 的
// node_modules。
// buildBackendDarwin(dstDir)：交叉编译 darwin arm64+amd64 后端到 dstDir/lxcode-<arch>
//（CGO_ENABLED=0 纯 Go，modernc.org/sqlite 无 C 依赖）。--backend-darwin 与
// --mac 共用这一段。
function buildBackendDarwin(dstDir) {
  mkdirSync(dstDir, { recursive: true });
  for (const arch of ["arm64", "amd64"]) {
    const out = join(dstDir, `lxcode-darwin-${arch}`);
    console.log(`\n=== Go 后端交叉编译（darwin/${arch}） ===`);
    // shell:false——ldflags 的 -X 必须空格分隔；
    // CGO_ENABLED=0：纯 Go 产物，不依赖 mac 侧 C 工具链
    const r = spawnSync(
      "go",
      ["build", "-ldflags", `-X main.version=${version}`, "-o", out, "./cmd/lxcode"],
      { cwd: ROOT, stdio: "inherit", shell: false, env: { ...process.env, GOOS: "darwin", GOARCH: arch, CGO_ENABLED: "0" } },
    );
    if (r.status !== 0) {
      console.error(`darwin/${arch} 交叉编译失败（exit ${r.status}）`);
      process.exit(1);
    }
    console.log(`--- lxcode-darwin-${arch} 完成（${(statSync(out).size / 1048576).toFixed(2)} MB）`);
  }
}

if (process.argv.includes("--backend-darwin")) {
  buildBackendDarwin(join(SHELL, "release"));
  console.log("\n=== darwin 后端产物（shell/release/）===");
  process.exit(0);
}

// --mac：macOS 壳打包链（2026-10 用户翻案：macOS 壳列入支持）。能在 Windows
// 开发机上交叉推进的部分全部在这里：后端 darwin 双架构 → 渲染层 → stage →
// 壳主进程 esbuild → electron-builder --mac。与 Windows 链的差异（都有依据）：
//   1. 后端二进制落 resources/bin-darwin/（无 .exe；package.json 的
//      mac.extraResources 从这里取，与 win 的 resources/bin 分开）；
//   2. 不做 ffmpeg（投屏 darwin 不装配，见 shell/src/screen.ts 平台门）；
//   3. 更新制品 = update-mac-<version>.zip + manifest-mac.json（2026-12 补齐，
//      在 mac 上随 --mac 产出——electron-builder 的 mac 产物在那台机器上才有；
//      zip 内路径 = .app/Contents 相对路径，与客户端 installDir() 对齐）；
//   4. 不签名/公证（package.json identity: null；未签名 .app 触发 Gatekeeper
//      「右键打开」，见 package.json mac 段注释）。
// 限制（如实）：electron-builder 的 icns 转换与 hdiutil/dmg 制作依赖 mac 工具
// 链——Windows 上 --mac 只能到「产出未签名 .app 目录」这一步；dmg 目标在
// Windows 上会失败（app-builder 无 mac 工具链时跳过/报错，实测结论见验收）。
// 最终 dmg 需在 mac 上跑：cd shell && npx electron-builder --mac。
if (process.argv.includes("--mac")) {
  console.log("=== lxcode 打包（macOS / dmg+zip arm64+x64，未签名） ===");
  if (!existsSync(join(SHELL, "node_modules")) || !existsSync(join(FRONTEND, "node_modules"))) {
    console.error("依赖未安装：先在 shell/ 与 frontend/ 下各自 npm install");
    process.exit(1);
  }
  // 1. darwin 后端 → resources/bin-darwin/lxcode-<arch>（打包脚本按当前
  //    electron-builder 目标 arch 从 release/ 产物选一个拷过去——见 afterPack
  //    替代说明：extraResources 静态目录无法按 arch 区分，这里按 --mac 的
  //    默认单 arch 产物放。两个 arch 都要打包时跑两轮 --mac --x64 / --mac --arm64）。
  const macArch = process.argv.includes("--x64") ? "amd64" : "arm64";
  buildBackendDarwin(join(SHELL, "release"));
  const binDarwin = join(SHELL, "resources", "bin-darwin");
  rmSync(binDarwin, { recursive: true, force: true });
  mkdirSync(binDarwin, { recursive: true });
  copyFileSync(join(SHELL, "release", `lxcode-darwin-${macArch}`), join(binDarwin, "lxcode"));
  console.log(`--- 后端就位 resources/bin-darwin/lxcode（${macArch}）`);
  // 2/3. 渲染层 + stage（与 Windows 链同一套）
  run("渲染层构建", "npm", ["run", "build"], { cwd: FRONTEND });
  rmSync(join(SHELL, "renderer"), { recursive: true, force: true });
  mkdirSync(join(SHELL, "renderer"), { recursive: true });
  cpSync(join(FRONTEND, "dist"), join(SHELL, "renderer"), { recursive: true });
  console.log("--- 渲染层 stage 完成（frontend/dist → shell/renderer）");
  // 4. 壳主进程 esbuild（LOGO 派生链只在 Windows 壳打包时重生——mac 不吃 ico）
  run("壳主进程构建", "node", ["build.mjs"], { cwd: SHELL });
  // 5. electron-builder --mac（未签名；产物在 shell/release/）
  rmSync(join(SHELL, "release", "mac-universal"), { recursive: true, force: true });
  run("electron-builder 打包（mac）", "npx", ["electron-builder", "--mac", macArch === "amd64" ? "--x64" : "--arm64"], { cwd: SHELL });
  // 6. mac 更新制品（2026-12：与 Windows 更新链对齐——mac 客户端的更新器
  //    拉同目录的 manifest-mac.json，见 shell/src/update-platform.ts）。
  //    .app 的 resources 是 extraResources 平铺（bin/ 不在 Contents/resources
  //    之下、Contents 不进 zip）——zip 内路径 = Contents 相对路径，与客户端
  //    installDir()（=<app>.app/Contents）对齐。后端取本分支交叉编译产物
  //    resources/bin-darwin/lxcode（无 .exe；mac 无 ffmpeg/投屏，不进更新包）。
  //    zip 用 mac 自带 bsdtar（-a 按扩展名解析 zip 语义）；本段只在 mac 上跑
  //    （--mac 依赖 electron-builder 的 mac 工具链，Windows 上到不了这里）。
  const macUnpacked = join(SHELL, "release", macArch === "amd64" ? "mac-x64" : "mac-arm64");
  if (existsSync(macUnpacked)) {
    makeUpdateArtifacts({
      unpacked: macUnpacked,
      releaseDir: join(SHELL, "release"),
      version,
      files: ["resources/app.asar", "resources/bin/lxcode"],
      manifestName: "manifest-mac.json",
      stagingDir: ".update-staging-mac",
      backendSrc: join(binDarwin, "lxcode"), // electron-builder 产物缺 bin 时从这里补齐
    });
  } else {
    // electron-builder 产物目录名不匹配（版本/配置漂移）时不静默——更新链是
    // 发版交付物，缺了 manifest-mac.json 老 mac 客户端就永远停在旧版。
    console.error(`[提示] 未找到 ${macUnpacked}（electron-builder 产物结构变了？）——跳过 mac 更新制品，发版前需手工确认`);
  }
  console.log("\n=== --mac 完成 ===");
  console.log("产物在 shell/release/（win-unpacked 同级的 mac 目录 / .app）。");
  console.log("注意：dmg/zip 目标在 Windows 上可能因缺 mac 工具链受限；");
  console.log("最终 dmg 需在 mac 上跑 cd shell && npx electron-builder --mac。");
  console.log("发版上传：manifest-mac.json + update-mac-<version>.zip（与 manifest.json/update-<version>.zip 同目录）。");
  process.exit(0);
}

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

// 1b. 精简 ffmpeg（投屏用，见 docs/ffmpeg-build.md 与 scripts/build-ffmpeg.sh）：
//     仓库里有预编译产物（temp/ffmpeg/ffmpeg.exe，由 scripts/build-ffmpeg.sh 产出）
//     就拷进 extraResources 源目录随壳打包；没有则跳过（壳回落 PATH 查找，不劣化）。
const bundledFfmpeg = join(ROOT, "temp", "ffmpeg", "ffmpeg.exe");
const ffmpegDst = join(SHELL, "resources", "bin", "ffmpeg.exe");
if (existsSync(bundledFfmpeg)) {
  mkdirSync(join(SHELL, "resources", "bin"), { recursive: true });
  copyFileSync(bundledFfmpeg, ffmpegDst);
  console.log("--- bundled ffmpeg 已拷入 shell/resources/bin/ffmpeg.exe");
} else if (!existsSync(ffmpegDst)) {
  console.warn("--- [提示] 无 bundled ffmpeg（temp/ffmpeg/ffmpeg.exe 不存在）：安装包不含 ffmpeg，投屏回落用户 PATH");
}

// 2. 渲染层（tsc -b 类型检查 + vite build）
run("渲染层构建", "npm", ["run", "build"], { cwd: FRONTEND });

// 3. stage 渲染层进壳目录（electron-builder 的 files 均相对 shell/，不跨目录引用）
rmSync(join(SHELL, "renderer"), { recursive: true, force: true });
mkdirSync(join(SHELL, "renderer"), { recursive: true });
cpSync(join(FRONTEND, "dist"), join(SHELL, "renderer"), { recursive: true });
console.log("--- 渲染层 stage 完成（frontend/dist → shell/renderer）");

// 4. 壳主进程（先重生 LOGO 派生链：托盘 ico 与全平台 LOGO 位图都由 build/icon.png
//    派生，源图换了必须同步——见 scripts/gen-tray-icon.cjs 与 gen-app-icons.cjs 头注）
run("托盘图标生成", "node", [join(ROOT, "scripts", "gen-tray-icon.cjs")], { cwd: ROOT });
run("LOGO 派生链", "node", [join(ROOT, "scripts", "gen-app-icons.cjs")], { cwd: ROOT });
run("壳主进程构建", "node", ["build.mjs"], { cwd: SHELL });

// 5. electron-builder（NSIS + 签名）
//    先清 release/：产物目录每轮全新（否则旧 blockmap/旧版本安装包等残留物
//    会混在产物清单里，上一轮实测踩过）
rmSync(join(SHELL, "release"), { recursive: true, force: true });
run("electron-builder 打包", "npx", ["electron-builder", "--win", "--x64"], { cwd: SHELL });

// 6. 更新制品：update-<version>.zip + manifest.json（老 Windows 客户端契约，
//    字段与字段顺序一字未动——见 makeUpdateArtifacts 头注；**不要**在这里改
//    顶层六键的形状，老客户端会直接解析失败）。
const unpacked = join(SHELL, "release", "win-unpacked");
const releaseDir = join(SHELL, "release");
// zip 内路径=安装目录相对路径；bundled ffmpeg 存在才进更新包（未打包 ffmpeg 的旧版产物不带）
const UPDATE_FILES = ["resources/app.asar", "resources/bin/lxcode.exe"];
if (existsSync(join(unpacked, "resources", "bin", "ffmpeg.exe"))) {
  UPDATE_FILES.push("resources/bin/ffmpeg.exe");
}
const zipName = `update-${version}.zip`;

makeUpdateArtifacts({
  unpacked,
  releaseDir,
  version,
  files: UPDATE_FILES,
  zipName,
  manifestName: "manifest.json",
  stagingDir: ".update-staging",
});

// 产物清单
console.log("\n=== 全部完成，产物（shell/release/）===");
for (const f of readdirSync(releaseDir)) {
  const st = statSync(join(releaseDir, f));
  if (st.isFile()) console.log(`  ${f}（${(st.size / 1048576).toFixed(2)} MB）`);
}
console.log("  win-unpacked/（免安装版）");
console.log("发布：安装包+win-unpacked 全量分发；manifest.json+update-*.zip 供更新（Electron 升级发全量安装包）");
