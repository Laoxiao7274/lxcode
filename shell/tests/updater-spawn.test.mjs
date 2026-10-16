// updater-spawn.test.mjs —— asar 冷替换链路的结构性钉子（node --test 直读
// 源码文本，updater.ts 引 electron 不能整模块加载）。
//
// 历史：0.2.9→0.2.10 事故（PS 脚本 spawn 的 detached+windowsHide 组合导致
// PowerShell 静默空退、asar 没换、新壳没人拉起）。2026-12 重构：PS 冷替换
// 整体退役，改为「后端引导替换」——壳只写 update-pending 清单，壳退出后由
// 老后端进程（--stdin-watch 的 EOF 路径）执行换 asar + 拉新壳。
// 这组钉子保证：PS 链路不许复活、引导清单契约不许悄悄漂移。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const src = readFileSync(
  join(fileURLToPath(new URL("../src/", import.meta.url)), "updater.ts"),
  "utf8",
);

test("PS 冷替换脚本已退役（不得再 spawn powershell，也不得再 import update-script）", () => {
  assert.ok(!src.includes("update-script"), "updater.ts 不得再引用 update-script（PS 链路已退役）");
  assert.ok(!src.includes('"powershell"'), "updater.ts 不得再 spawn powershell（冷替换改由后端引导）");
});

test("applyUpdate 必须写引导清单（manifest.json，v1）", () => {
  assert.ok(src.includes("manifest.json"), "applyUpdate 必须把引导清单写进 update-pending/manifest.json");
  assert.ok(src.includes("version: 1"), "清单格式版本必须是 1（后端只认认识的版本）");
  // 后端契约字段（cmd/lxcode/applyupdate.go UpdateManifestV1）——缺一个后端就拒绝执行
  for (const field of ["new_asar", "target_asar", "shell_exe", "created_at_unix"]) {
    assert.ok(src.includes(field), `清单缺少后端契约字段 ${field}`);
  }
});

test("清单写入必须原子（tmp + rename，防后端读到半份 JSON）", () => {
  assert.ok(src.includes("manifest.json.tmp"), "清单要先写 .tmp 再 rename，原子落盘");
});

test("清单写完不 spawn 任何脱离进程（接手方是后端，不是壳再造一个执行体）", () => {
  // downloadAndStage 里的 spawnSync("tar") 是解压，合法；除此之外不得有 spawn。
  const spawnCalls = [...src.matchAll(/spawn(Sync)?\(/g)].length;
  assert.equal(spawnCalls, 1, `updater.ts 只允许 tar 解压的 spawnSync（当前 ${spawnCalls} 处 spawn）`);
});

test("bin 热替换必须在 asar 冷替换排程之前，且失败即中止（0.2.13→0.2.14 事故钉子）", () => {
  // 0.2.13→0.2.14 事故：bin 热替换留了个损坏的 exe、流程照走、冷替换照排程
  // ——新壳起来报「127.0.0.1:7789 不在线，且壳内没有可用的后端二进制」。
  // 结构钉子：applyUpdate 里「后端热替换开始」必须先于「asar 冷替换已排程」，
  // 且热替换段必须有 sha256 预检（stagedManifest.files 逐文件哈希对 newExe）。
  const start = src.indexOf("后端热替换开始");
  const schedule = src.indexOf("asar 冷替换已排程");
  assert.ok(start >= 0, "applyUpdate 必须有 bin 热替换段");
  assert.ok(schedule >= 0, "applyUpdate 必须有 asar 冷替换排程段");
  assert.ok(start < schedule, "bin 热替换必须发生在 asar 冷替换排程之前（bin 没换上就排冷替换 = 造出「新 asar + 无 bin」残局）");
  assert.ok(src.includes('stagedManifest?.files?.["resources/bin/lxcode.exe"]'), "bin 落位前后必须按暂存清单逐文件哈希校验（copyFileSync 不校验内容）");
  assert.ok(src.includes("bin_dir"), "引导清单必须带 bin_dir（后端引导日志要能回答 bin 在不在）");
});
