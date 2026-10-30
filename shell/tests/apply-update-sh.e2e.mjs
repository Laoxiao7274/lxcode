import { buildApplySh } from "file:///C:/Users/xzy/Desktop/my/lxcode/shell/src/apply-update.ts";
import { spawnSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync, appendFileSync, rmSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sha256 = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// POSIX 单引号转义（与 buildApplySh 的 shq 同语义——e2e 路径断言也要进 sh -c）
const shq = (s) => `'${s.replace(/'/g, `'\''`)}'`;
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
  writeFileSync(join(staged, "resources/bin/lxcode"), "#!/bin/sh\nNEW-EXE-" + "y".repeat(200)
    // 本机（Git Bash / MSYS noacl 挂载）平台垫片，真 mac 不需要：
    //   1. MSYS noacl 下 POSIX 位从**内容**推断（第一行为 shebang 才算可执行），
    //      且 chmod 无法把执行位持久到不被判定为可执行的文件上——本机的
    //      test -x 判定的是内容推断结果，不是权限位。真 mac 的 test -x 只看
    //      位（chmod 0755 后恒 true），不受此影响。
    //   2. MSYS chmod 对**零字节**文件同样不持久执行位；staged 内容非空，无关。
    //   3. shebang 必须在文件**头**（放尾部不触发内容推断，实测踩过）。
    // 「替换落位」断言相应改为 startsWith("#!/bin/sh\nNEW-EXE-")。
    );
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
  report("成功路径: 替换落位", readFileSync(join(install, "resources/app.asar"), "utf8").startsWith("NEW-ASAR") && readFileSync(join(install, "resources/bin/lxcode"), "utf8").startsWith("#!/bin/sh\nNEW-EXE-"));
  report("成功路径: .old 清理/暂存/清单/bat 自删除", !existsSync(join(install, "resources/app.asar.old")) && !existsSync(staged) && !existsSync(shPath));
  report("成功路径: 新壳被拉起", await waitMark(mark) && readFileSync(mark, "utf8").trim() === "new-shell");
  report("成功路径: 日志 DONE", log.includes("apply-update DONE (success"));
  // 执行位保障（mac 更新链修复，2026-12）：deploy 阶段对名为 lxcode 的
  // 无扩展名后端二进制补 chmod 0755——staged 副本在 Windows/NTFS 上落 0644
  // （Node copyFileSync 不持久 POSIX 执行位），cp 原样落位后端就是 EACCES。
  // 断言 = POSIX test -x（Git Bash/真 mac 同一份语义位；shebang 垫片见上方注释）。
  const deployed = spawnSync("sh", ["-c", `test -x ${shq(install)}/resources/bin/lxcode && echo exec || echo noexec`]);
  report("成功路径: 落位的 lxcode 具有可执行位", deployed.stdout.toString().trim() === "exec", deployed.stdout.toString().trim());
  // 垫片不能改变内容语义：正文含 NEW-EXE（上面的「替换落位」断言已覆盖，
  // 这里补一个直接读文件的二次确认，防垫片写错位置）。
  report("成功路径: lxcode 正文保持 NEW-EXE 内容", readFileSync(join(install, "resources/bin/lxcode"), "utf8").includes("NEW-EXE-"));

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
