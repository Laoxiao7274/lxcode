// updater-platform.test.mjs —— 更新链路平台契约的单测（2026-12：manifest 按
// 平台分文件 + 平台守卫）。
//
// 背景与调研结论：Windows 与 mac 的更新包内容不同（lxcode.exe vs lxcode），
// site-backend 按平台出两份同构 manifest（manifest.json = Windows 老契约、
// manifest-mac.json = mac，同目录）；mac 客户端若因故拿到 Windows 更新包，
// apply 阶段会把 exe 替换进 .app——平台守卫是最后一道闸。
//
// 纯函数在 shell/src/update-platform.ts（无 Electron 依赖，直接 import；
// ts-loader.mjs 的转译擦除 type-only 导入，与 update-poll.test.mjs 同款）。
// updater.ts 的接线（manifestUrl 传 process.platform、守卫插在
// verifyStagedFiles 之后）用源码文本钉子钉住（updater-spawn.test.mjs 同款
// 手法——updater.ts 引 electron 不能整模块加载）。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { assertStagedPlatform, platformManifestUrl } from "../src/update-platform.ts";

const updaterSrc = readFileSync(
  join(fileURLToPath(new URL("../src/", import.meta.url)), "updater.ts"),
  "utf8",
);

// ---------- manifest URL 按平台 ----------

test("platformManifestUrl：Windows（win32）原样返回——老契约一字不动", () => {
  const def = "https://api.r96314722.nyat.app:15714/site/manifest.json";
  assert.equal(platformManifestUrl(def, "win32"), def);
  // linux 无壳分发（AGENTS.md §2.1），也不改写
  assert.equal(platformManifestUrl(def, "linux"), def);
});

test("platformManifestUrl：darwin 拼同目录 manifest-mac.json（默认 URL）", () => {
  assert.equal(
    platformManifestUrl("https://api.r96314722.nyat.app:15714/site/manifest.json", "darwin"),
    "https://api.r96314722.nyat.app:15714/site/manifest-mac.json",
  );
});

test("platformManifestUrl：env 指向完整 manifest 路径时只换末段文件名，目录与查询串保留", () => {
  assert.equal(
    platformManifestUrl("https://example.com/updates/beta/manifest.json?tag=v9", "darwin"),
    "https://example.com/updates/beta/manifest-mac.json?tag=v9",
  );
  assert.equal(
    platformManifestUrl("https://example.com/manifest.json", "darwin"),
    "https://example.com/manifest-mac.json",
  );
});

test("platformManifestUrl：base 不是合法 URL 时原样返回（退回旧行为，错误交给 fetch 报）", () => {
  assert.equal(platformManifestUrl("not-a-url", "darwin"), "not-a-url");
});

test("updater.ts 接线：manifestUrl 走 platformManifestUrl 且传 process.platform", () => {
  assert.ok(updaterSrc.includes("platformManifestUrl(base, process.platform)"), "manifestUrl 必须经 platformManifestUrl 按平台拼");
});

// ---------- 平台守卫：mac 拉 Windows 包 → 中止 ----------

// 守卫输入 = ApplyFileEntry 的 target 子集；路径用各平台真实分隔符形态。
const winEntries = (extra = []) => [
  { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\app.asar" },
  { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\bin\\lxcode.exe" },
  ...extra,
];
const macEntries = (extra = []) => [
  { target: "/Applications/Lxcode.app/Contents/resources/app.asar" },
  { target: "/Applications/Lxcode.app/Contents/resources/bin/lxcode" },
  ...extra,
];

test("守卫：darwin + 清单含 .exe → 中止（防 mac 拉 Windows 包把 exe 替换进 .app）", () => {
  assert.throws(
    () => assertStagedPlatform(winEntries(), "darwin"),
    (e) => e.message.includes("与当前平台不匹配") && e.message.includes(".exe"),
  );
  // 即使 darwin 后端也在场（混装包），只要出现 .exe 就拒——宁可不更新
  assert.throws(
    () => assertStagedPlatform(macEntries([{ target: "/Applications/Lxcode.app/Contents/resources/bin/lxcode.exe" }]), "darwin"),
    (e) => e.message.includes("与当前平台不匹配"),
  );
});

test("守卫：darwin + 清单缺 resources/bin/lxcode → 中止（不完整/他平台包）", () => {
  assert.throws(
    () => assertStagedPlatform([{ target: "/Applications/Lxcode.app/Contents/resources/app.asar" }], "darwin"),
    (e) => e.message.includes("resources/bin/lxcode"),
  );
});

test("守卫：darwin + 正常 mac 清单（app.asar + bin/lxcode）→ 通过", () => {
  assert.doesNotThrow(() => assertStagedPlatform(macEntries(), "darwin"));
});

test("守卫：win32 + 正常 Windows 清单（含 ffmpeg 可选文件）→ 通过", () => {
  assert.doesNotThrow(() => assertStagedPlatform(winEntries([
    { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\bin\\ffmpeg.exe" },
  ]), "win32"));
});

test("守卫：win32 + 清单缺 lxcode.exe → 中止", () => {
  assert.throws(
    () => assertStagedPlatform([{ target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\app.asar" }], "win32"),
    (e) => e.message.includes("lxcode.exe"),
  );
});

test("守卫：win32 + 清单混入 darwin 后端（无 .exe 的 bin/lxcode）→ 中止", () => {
  assert.throws(
    () => assertStagedPlatform([
      { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\app.asar" },
      { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\bin\\lxcode.exe" },
      { target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\bin\\lxcode" },
    ], "win32"),
    (e) => e.message.includes("与当前平台不匹配"),
  );
});

test("守卫接线：applyUpdate 在 verifyStagedFiles 之后、任何排程写入之前执行守卫", () => {
  // 钉住守卫位置：verifyStagedFiles 在前（先复核文件形态），守卫紧随其后，
  // 且必须在 bat/sh 排程写入（buildApplyBat/buildApplySh）之前——守卫失败时
  // 排程从未发生，现有安装不受影响。
  const guardIdx = updaterSrc.indexOf("assertStagedPlatform(files, process.platform)");
  assert.ok(guardIdx > 0, "applyUpdate 必须调用平台守卫");
  const verifyIdx = updaterSrc.indexOf("verifyStagedFiles(files, payloadDir, stagedTarget)");
  assert.ok(verifyIdx > 0 && verifyIdx < guardIdx, "守卫在 verifyStagedFiles 之后");
  for (const marker of ["buildApplyBat(plan)", "buildApplySh(plan)"]) {
    const idx = updaterSrc.indexOf(marker, guardIdx);
    assert.ok(idx > guardIdx, `守卫必须在 ${marker}（排程写入）之前`);
  }
});
