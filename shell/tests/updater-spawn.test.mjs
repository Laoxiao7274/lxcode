// updater-spawn.test.mjs —— 更新链路的结构性钉子（node --test 直读
// 源码文本，updater.ts 引 electron 不能整模块加载）。
//
// 历史：0.2.9→0.2.10 事故（PS 脚本 spawn 的 detached+windowsHide 组合导致
// PowerShell 静默空退、asar 没换、新壳没人拉起）→ PS 冷替换退役；改为后端
// 引导替换后，0.2.13→0.2.14 又出事故（bin 半写损坏 + asar 等待超时 + 壳/
// 后端两边状态不一致）。2026-12 用户拍板重构：**独立 bat 完成全部替换与
// 拉起**（apply-update.ts 生成，wscript 隐藏启动）——壳只下载/暂存/排程，
// 壳与后端都不参与替换。这组钉子保证：旧双进程链路不许复活、新 bat 契约
// 不许悄悄漂移。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const src = readFileSync(
  join(fileURLToPath(new URL("../src/", import.meta.url)), "updater.ts"),
  "utf8",
);

test("旧双进程链路已退役（不得热替换后端、不得再引用后端引导清单 v1）", () => {
  // 0.2.13→0.2.14 事故的策源地是壳侧热替换 bin（rename/copy 后端 exe）——
  // 新链路里后端 exe 与 asar 一样交给 bat 统一替换，壳不得再碰它。
  assert.ok(!src.includes("后端热替换"), "updater.ts 不得再有后端热替换段");
  assert.ok(!src.includes("renameSync(currentExe"), "不得 rename 运行中的后端 exe");
  assert.ok(!src.includes("restartBackend"), "壳不得再重启后端（后端随壳退出自行收场）");
  assert.ok(!src.includes("new_asar") && !src.includes("target_asar"), "旧后端引导清单 v1 字段不得复活");
  assert.ok(!src.includes('"powershell"'), "不得 spawn powershell（旧 PS 链路）");
});

test("applyUpdate 必须写 update-manifest.json（v2）且原子落盘", () => {
  assert.ok(src.includes("update-manifest.json"), "必须写 update-manifest.json");
  assert.ok(src.includes("buildApplyManifest"), "清单内容必须来自 apply-update.ts 的纯函数（与 bat 同源）");
  assert.ok(src.includes("update-manifest.json.tmp"), "清单要先写 .tmp 再 rename，原子落盘");
});

test("applyUpdate 必须生成并隐藏启动 apply-update.bat（wscript + VBS 包装）", () => {
  assert.ok(src.includes("buildApplyBat"), "bat 内容必须来自 apply-update.ts 的纯函数");
  assert.ok(src.includes("buildApplyVbs"), "必须有 VBS 隐藏启动包装");
  assert.ok(src.includes('"wscript.exe"'), "启动方式 = wscript（GUI 子系统进程，零窗口）");
  assert.ok(src.includes("detached: true"), "bat 进程必须脱离壳（壳退出后 bat 独立存活）");
});

test("壳退出由 update:restart IPC 承载（applyUpdate 只排程，退出动作在 IPC handler）", () => {
  // applyUpdate 本身不调 app.quit——用户点「立即重启」（或渲染层 apply 成功
  // 后提示重启）才触发 update:restart → app.quit()。这里钉住接线语义。
  const restartIdx = src.indexOf("update:restart");
  const quitIdx = src.indexOf("app.quit()", restartIdx);
  assert.ok(restartIdx > 0 && quitIdx > restartIdx, "update:restart handler 内必须 app.quit()");
  assert.ok(src.includes("update:apply"), "apply 通道存在（渲染层 apply 成功后引导用户重启）");
});

test("排程的等待名单按 PID（壳 + 壳拉起的后端），不含按名匹配", () => {
  assert.ok(src.includes("managedBackendPid()"), "后端 PID 来自 sidecar 的 managedBackendPid");
  assert.ok(src.includes("process.pid"), "壳自身 PID 在等待名单里");
  assert.ok(!src.includes("taskkill /IM") && !src.includes("tasklist /FI \"IM eq\""), "不得按进程名匹配（按名会误杀同名进程）");
});

test("暂存必须与安装目录同构（update-pending/staged + copyTree 到 staged）", () => {
  assert.ok(src.includes('join(pending, "staged")'), "暂存目录 = update-pending/staged");
  assert.ok(src.includes("copyTree(payloadDir, stagedTarget)"), "payload 整体复制进 staged");
  assert.ok(src.includes("originalFs"), "触碰 *.asar 的 fs 必须走 originalFs（Electron 补丁规避）");
});

test("旧格式残留清理：update-pending 全量重置（v1 清单 / apply-update.ps1 一并清）", () => {
  assert.ok(src.includes('rmWithRetry(pending, "清理 update-pending（含旧链路残留）", true)'), "排程前必须全量清 update-pending");
});
