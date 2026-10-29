import { buildApplySh } from "file:///C:/Users/xzy/Desktop/my/lxcode/shell/src/apply-update.ts";
import { spawnSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync, rmSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sha256 = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const results = [];
const roots = [];
const report = (name, ok, detail) => { results.push({name, ok}); console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? " — " + detail : ""}`); };
for (const root of []) {}
const waitMark = async (mark, timeoutMs = 20000) => {
  const dl = Date.now() + timeoutMs;
  while (Date.now() < dl) { if (existsSync(mark)) return true; await sleep(200); }
  return false;
};
const fakeShell = "/tmp/lx-e2e-sh/fakeshell.sh";

// apply-update-sh.e2e.mjs —— apply-update.sh（macOS 等价链）三路径真实 e2e。
// 与 apply-update.e2e.mjs（Windows bat 版）同构：成功/失败回滚/超时强杀。
// POSIX sh 在 Windows 上也有（Git Bash /usr/bin/sh），本机即可复跑——注意：
// Git Bash 的 coreutils sha256sum 对反斜杠 Windows 路径会输出二进制模式标记
// '' 前缀，脚本内已剥离（sha256_of 的 sed），这里同样真实覆盖了那条分支。
// 运行：node tests/apply-update-sh.e2e.mjs（用 temp 目录，跑完自清）。
// 路径1：成功
{
  const root = mkdtempSync(join(tmpdir(), "lx-sh-e2e-ok-"));
  roots.push(root);
  const install = join(root, "install"), pending = join(root, "pending"), staged = join(pending, "staged");
  mkdirSync(join(install, "resources", "bin"), { recursive: true });
  mkdirSync(join(staged, "resources", "bin"), { recursive: true });
  writeFileSync(join(install, "resources/app.asar"), "OLD-ASAR");
  writeFileSync(join(install, "resources/bin/lxcode"), "OLD-EXE");
  writeFileSync(join(staged, "resources/app.asar"), "NEW-ASAR-" + "x".repeat(100));
  writeFileSync(join(staged, "resources/bin/lxcode"), "NEW-EXE-" + "y".repeat(200));
  const mark = join(root, "mark");
  const plan = {
    pendingDir: pending, stagedDir: staged, shellExe: fakeShell,
    shellArgs: [mark, "new-shell"], pids: [999999], appVersion: "0.3.30-e2e",
    pidTimeoutSec: 3,
    files: ["resources/app.asar", "resources/bin/lxcode"].map(rel => ({
      staged: join(staged, rel), target: join(install, rel), sha256: sha256(join(staged, rel)),
    })),
  };
  const shPath = join(pending, "apply-update.sh");
  writeFileSync(shPath, buildApplySh(plan), { mode: 0o755 });
  const r = spawnSync("sh", [shPath], { timeout: 60000, cwd: root });
  const log = readFileSync(join(pending, "apply-update.log"), "utf8");
  report("成功路径: exit 0", r.status === 0, `exit=${r.status}`);
  report("成功路径: 替换落位", readFileSync(join(install, "resources/app.asar"), "utf8").startsWith("NEW-ASAR") && readFileSync(join(install, "resources/bin/lxcode"), "utf8").startsWith("NEW-EXE"));
  report("成功路径: .old 清理/暂存/清单/bat 自删除", !existsSync(join(install, "resources/app.asar.old")) && !existsSync(staged) && !existsSync(shPath));
  report("成功路径: 新壳被拉起", await waitMark(mark) && readFileSync(mark, "utf8").trim() === "new-shell");
  report("成功路径: 日志 DONE", log.includes("apply-update DONE (success"));
  
}
// 路径2：失败回滚（坏哈希）
{
  const root = mkdtempSync(join(tmpdir(), "lx-sh-e2e-rb-"));
  roots.push(root);
  const install = join(root, "install"), pending = join(root, "pending"), staged = join(pending, "staged");
  mkdirSync(join(install, "resources", "bin"), { recursive: true });
  mkdirSync(join(staged, "resources", "bin"), { recursive: true });
  writeFileSync(join(install, "resources/app.asar"), "OLD-ASAR");
  writeFileSync(join(install, "resources/bin/lxcode"), "OLD-EXE");
  writeFileSync(join(staged, "resources/app.asar"), "NEW-ASAR");
  writeFileSync(join(staged, "resources/bin/lxcode"), "CORRUPTED");
  const mark = join(root, "mark");
  const plan = {
    pendingDir: pending, stagedDir: staged, shellExe: fakeShell,
    shellArgs: [mark, "old-shell"], pids: [999999], appVersion: "0.3.30-e2e",
    pidTimeoutSec: 3,
    files: ["resources/app.asar", "resources/bin/lxcode"].map((rel, i) => ({
      staged: join(staged, rel), target: join(install, rel), sha256: i === 1 ? "c".repeat(64) : sha256(join(staged, rel)),
    })),
  };
  const shPath = join(pending, "apply-update.sh");
  writeFileSync(shPath, buildApplySh(plan), { mode: 0o755 });
  const r = spawnSync("sh", [shPath], { timeout: 60000, cwd: root });
  const log = readFileSync(join(pending, "apply-update.log"), "utf8");
  report("回滚路径: exit 1", r.status === 1, `exit=${r.status}`);
  report("回滚路径: 全部目标恢复原件", readFileSync(join(install, "resources/app.asar"), "utf8") === "OLD-ASAR" && readFileSync(join(install, "resources/bin/lxcode"), "utf8") === "OLD-EXE");
  report("回滚路径: 无 .old 残留", !existsSync(join(install, "resources/app.asar.old")) && !existsSync(join(install, "resources/bin/lxcode.old")));
  report("回滚路径: 脚本保留（失败留证）", existsSync(shPath));
  report("回滚路径: 旧壳被拉起", await waitMark(mark) && readFileSync(mark, "utf8").trim() === "old-shell");
  report("回滚路径: 日志 sha256 mismatch + END(failed)", log.includes("sha256 mismatch") && log.includes("apply-update END (failed, rolled back)"));
  report("回滚路径: 失败标记文件", existsSync(join(pending, "apply-update.failed")));
  
}
// 路径3：超时强杀
{
  const root = mkdtempSync(join(tmpdir(), "lx-sh-e2e-kill-"));
  roots.push(root);
  const install = join(root, "install"), pending = join(root, "pending"), staged = join(pending, "staged");
  mkdirSync(join(install, "resources"), { recursive: true });
  mkdirSync(join(staged, "resources"), { recursive: true });
  writeFileSync(join(install, "resources/app.asar"), "OLD-ASAR");
  writeFileSync(join(staged, "resources/app.asar"), "NEW-ASAR");
  const sleeper = spawn("sh", ["/tmp/lx-e2e-sh/sleeper.sh"], { stdio: "ignore", detached: false });
  await sleep(300);
  const mark = join(root, "mark");
  const plan = {
    pendingDir: pending, stagedDir: staged, shellExe: fakeShell,
    shellArgs: [mark, "new-shell"], pids: [sleeper.pid], appVersion: "0.3.30-e2e",
    pidTimeoutSec: 4,
    files: [{ staged: join(staged, "resources/app.asar"), target: join(install, "resources/app.asar"), sha256: sha256(join(staged, "resources/app.asar")) }],
  };
  const shPath = join(pending, "apply-update.sh");
  writeFileSync(shPath, buildApplySh(plan), { mode: 0o755 });
  const r = spawnSync("sh", [shPath], { timeout: 60000, cwd: root });
  const log = readFileSync(join(pending, "apply-update.log"), "utf8");
  await sleep(500);
  const chk = spawnSync("sh", ["-c", `ps -p ${sleeper.pid} >/dev/null 2>&1 && echo alive || echo dead`]);
  const alive = chk.stdout.toString().trim() === "alive";
  report("强杀路径: 替换照常完成", readFileSync(join(install, "resources/app.asar"), "utf8") === "NEW-ASAR");
  report("强杀路径: 常驻进程确实被杀", !alive, alive ? "进程还活着" : "");
  report("强杀路径: 日志 BEGIN+DONE", log.includes("BEGIN") && log.includes("DONE (success"));
  report("强杀路径: 新壳被拉起", existsSync(mark) && readFileSync(mark, "utf8").trim() === "new-shell");
  if (alive) spawnSync("sh", ["-c", `kill -9 ${sleeper.pid}`]);
  sleeper.kill("SIGKILL");
  
}
for (const r of roots) { try { rmSync(r, { recursive: true, force: true }); } catch { /* temp 残留无害 */ } }
const failed = results.filter(r => !r.ok);
console.log(`\nsh e2e: ${results.length - failed.length}/${results.length} 通过`);
process.exit(failed.length ? 1 : 0);
