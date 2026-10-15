// updater-spawn.test.mjs —— 0.2.9→0.2.10 重启失灵事故的 spawn 侧钉子。
//
// 事故根因：applyUpdate 给冷替换脚本的 spawn 同时带 detached:true 与
// windowsHide:true——Windows 上分别映射 DETACHED_PROCESS 与 CREATE_NO_WINDOW，
// 是 MSDN 明文禁止同用的进程创建标志组合；cmd.exe 鲁棒（0.2.8 及更早一直
// 成功），PowerShell 5.1 在该组合下**立即静默退出且 -File 脚本零执行**——
// 用户点「立即重启」后壳退出、asar 没换、新壳没人拉起。
//
// updater.ts 引 electron（app.getPath 等），node --test 里不能整模块加载，
// 这组测试用源码文本做 tripwire：不是行为测试，是「这条事故不许复发」的
// 结构性钉子——任何人往这个 spawn 上加回 detached 会被拦下。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const src = readFileSync(
  join(fileURLToPath(new URL("../src/", import.meta.url)), "updater.ts"),
  "utf8",
);

test("冷替换脚本的 spawn 不得带 detached（0.2.10 事故根因）", () => {
  assert.ok(!src.includes("detached: true"), "updater.ts 里不得出现 detached: true——与 windowsHide 同用会让 PowerShell 5.1 静默空退（DETACHED_PROCESS + CREATE_NO_WINDOW 是 MSDN 禁止同用的创建标志）");
});

test("spawn 必须监听 error 事件（spawn 失败是异步 error，无人监听 = 主进程 uncaughtException）", () => {
  assert.ok(src.includes(`child.on("error"`), "apply 脚本的 spawn 子进程必须挂 error 监听并落日志");
});

test("spawn 用 -File 传脚本路径（不能回退到 cmd /c 内联）", () => {
  const at = src.indexOf('"-File"');
  assert.ok(at > 0, "spawn 参数必须含 -File");
  assert.ok(src.slice(0, at).includes('"powershell"'), "脚本解释器仍是 powershell");
});
