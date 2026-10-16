// apply-update.e2e.mjs —— apply-update.bat 三路径真实端到端（本机可复跑）：
//   1. 成功：替换落位 + sha 复核过 + 暂存清理 + 假壳被拉起 + bat 自删除
//   2. 失败回滚：某个暂存文件缺 sha 对应（目标落位后 certutil 必失败）→
//      整体回滚（.old 移回）+ 旧版壳被拉起 + bat 留日志退出
//   3. 宽限窗强杀：等待名单里的 PID 活着 → 宽限窗后 taskkill /F 强杀 → 替换照常完成
//      （零探测设计：探测类命令在 wscript 隐藏会话对活 PID 会挂死，见 apply-update.ts）
// 假壳 = node 脚本包一层（被 bat 的 start 拉起时写标记文件后退出）——
// 不依赖 Go 构建；start "" node.exe script args 的形态与真实 start Lxcode.exe 同构。
// 运行：node tests/apply-update.e2e.mjs（Windows only；用 temp 目录，跑完自清）。
import { spawnSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildApplyBat, buildApplyVbs, toCrlf } from "../src/apply-update.ts";

const sha256 = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 假壳脚本：`node fake-shell.js <标记文件> <内容>` 写标记后退出。
const fakeShellJs = join(tmpdir(), `lxcode-e2e-fakeshell-${process.pid}.js`);
writeFileSync(fakeShellJs, `
const [,, mark, content] = process.argv;
require("fs").writeFileSync(mark, content ?? "launched");
`);

// 假常驻进程：睡 120s（供超时强杀路径用）。
const sleeperJs = join(tmpdir(), `lxcode-e2e-sleeper-${process.pid}.js`);
writeFileSync(sleeperJs, `
const [,, mark] = process.argv;
require("fs").writeFileSync(mark, "alive");
setTimeout(() => process.exit(0), 120_000);
`);

function waitMark(mark, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (existsSync(mark)) return true;
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
  }
  return false;
}

function runBat(pendingDir, batPath, timeoutMs = 90_000) {
  // 真实执行路径与生产一致：wscript 隐藏启动 VBS → VBS Run bat。
  // e2e 里用同步 spawn wscript 并等它返回（VBS 的 Run 是不等待的——bat 在
  // 独立 cmd 里跑，wscript 秒回；所以这里轮询产物状态而不是等 wscript）。
  const r = spawnSync("wscript.exe", [join(pendingDir, "apply-update.vbs")], { timeout: 10_000 });
  if (r.status !== 0) throw new Error(`wscript 启动失败: exit ${r.status} ${r.stderr}`);
  // 轮询终态：bat 自删除（成功）或日志出现结束标记（失败），超时抛出。
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const logPath = join(pendingDir, "apply-update.log");
    const log = existsSync(logPath) ? readFileSync(logPath, "utf8") : "";
    if (!existsSync(batPath) || log.includes("apply-update END (failed, rolled back)")) return log;
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 300);
  }
  throw new Error("bat 未在超时内到达终态");
}

const results = [];
function report(name, ok, detail) {
  results.push({ name, ok, detail });
  console.log(`${ok ? "✔" : "✖"} ${name}${detail ? ` — ${detail}` : ""}`);
}

// ---------- 路径 1：成功 ----------
function e2eSuccess() {
  const root = mkdtempSync(join(tmpdir(), "lxcode-e2e-success-"));
  const install = join(root, "install");
  const pending = join(root, "pending");
  const staged = join(pending, "staged");
  mkdirSync(join(install, "resources", "bin"), { recursive: true });
  mkdirSync(join(staged, "resources", "bin"), { recursive: true });

  const targets = {
    "resources\\app.asar": "OLD-ASAR-BYTES",
    "resources\\bin\\lxcode.exe": "OLD-EXE-BYTES",
  };
  const news = {
    "resources\\app.asar": "NEW-ASAR-BYTES-" + "x".repeat(1000),
    "resources\\bin\\lxcode.exe": "NEW-EXE-BYTES-" + "y".repeat(2000),
  };
  for (const [rel, content] of Object.entries(targets)) writeFileSync(join(install, rel), content);
  for (const [rel, content] of Object.entries(news)) writeFileSync(join(staged, rel), content);

  const shellExe = process.execPath; // start "" <node 绝对路径> <script> <mark>
  const mark = join(root, "launched.mark");
  const plan = {
    pendingDir: pending,
    stagedDir: staged,
    shellExe,
    shellArgs: [`"${fakeShellJs}"`, `"${mark}"`, "new-shell"],
    pids: [process.pid], // e2e 进程自己——不退出，走 taskkill 兜底？不行：
    // process.pid 是本进程，被强杀 e2e 就没了。用 sleep 窗口内必然退出的
    // 惰性 PID 代替（等待循环照样真实跑）。
    appVersion: "0.3.0-e2e",
    pidTimeoutSec: 3,
    files: Object.keys(news).map((rel) => ({
      staged: join(staged, rel),
      target: join(install, rel),
      sha256: sha256(join(staged, rel)),
    })),
  };
  // 用一个瞬时已死 PID：tasklist 查不到 → 立即过，等待逻辑仍被真实执行。
  plan.pids = [999999];

  writeFileSync(join(pending, "apply-update.bat"), Buffer.from(toCrlf(buildApplyBat(plan)), "utf8"));
  writeFileSync(join(pending, "apply-update.vbs"), buildApplyVbs(join(pending, "apply-update.bat")), "utf8");

  const log = runBat(pending, join(pending, "apply-update.bat"));

  const okAsar = readFileSync(join(install, "resources\\app.asar"), "utf8") === news["resources\\app.asar"];
  const okExe = readFileSync(join(install, "resources\\bin\\lxcode.exe"), "utf8") === news["resources\\bin\\lxcode.exe"];
  const noOld = Object.keys(targets).every((rel) => !existsSync(join(install, rel) + ".old"));
  const stagedGone = !existsSync(staged);
  const manifestGone = !existsSync(join(pending, "update-manifest.json"));
  const batGone = !existsSync(join(pending, "apply-update.bat"));
  const launched = waitMark(mark) && readFileSync(mark, "utf8") === "new-shell";
  report("成功路径：替换落位", okAsar && okExe);
  report("成功路径：.old 已清理、暂存/清单/bat 自删除", noOld && stagedGone && manifestGone && batGone);
  report("成功路径：新壳被拉起（标记文件出现）", launched);
  report("成功路径：日志记录成功收尾", log.includes("apply-update DONE (success"));
  rmSync(root, { recursive: true, force: true });
}

// ---------- 路径 2：失败回滚 ----------
function e2eRollback() {
  const root = mkdtempSync(join(tmpdir(), "lxcode-e2e-rollback-"));
  const install = join(root, "install");
  const pending = join(root, "pending");
  const staged = join(pending, "staged");
  mkdirSync(join(install, "resources", "bin"), { recursive: true });
  mkdirSync(join(staged, "resources", "bin"), { recursive: true });

  const targets = {
    "resources\\app.asar": "OLD-ASAR-BYTES",
    "resources\\bin\\lxcode.exe": "OLD-EXE-BYTES",
  };
  const news = {
    "resources\\app.asar": "NEW-ASAR-BYTES",
    // 坏文件：暂存内容与计划哈希不一致 → 落位后 certutil 必失败 → 回滚
    "resources\\bin\\lxcode.exe": "CORRUPTED-EXE-BYTES",
  };
  for (const [rel, content] of Object.entries(targets)) writeFileSync(join(install, rel), content);
  for (const [rel, content] of Object.entries(news)) writeFileSync(join(staged, rel), content);

  const mark = join(root, "launched.mark");
  const plan = {
    pendingDir: pending,
    stagedDir: staged,
    shellExe: process.execPath,
    shellArgs: [`"${fakeShellJs}"`, `"${mark}"`, "old-shell"],
    pids: [999999],
    appVersion: "0.3.0-e2e",
    pidTimeoutSec: 3,
    files: Object.keys(news).map((rel, i) => ({
      staged: join(staged, rel),
      target: join(install, rel),
      // 第二个文件给一个与暂存内容不符的哈希（模拟落位后内容损坏）
      sha256: i === 1 ? "c".repeat(64) : sha256(join(staged, rel)),
    })),
  };

  writeFileSync(join(pending, "apply-update.bat"), Buffer.from(toCrlf(buildApplyBat(plan)), "utf8"));
  writeFileSync(join(pending, "apply-update.vbs"), buildApplyVbs(join(pending, "apply-update.bat")), "utf8");

  const log = runBat(pending, join(pending, "apply-update.bat"));

  const rolledBack = Object.keys(targets).every(
    (rel) => readFileSync(join(install, rel), "utf8") === targets[rel],
  );
  const noOld = Object.keys(targets).every((rel) => !existsSync(join(install, rel) + ".old"));
  const noStagedFile = !existsSync(join(install, "resources\\bin\\lxcode.exe")) ||
    readFileSync(join(install, "resources\\bin\\lxcode.exe"), "utf8") === targets["resources\\bin\\lxcode.exe"];
  const batStillThere = existsSync(join(pending, "apply-update.bat"));
  const launched = waitMark(mark) && readFileSync(mark, "utf8") === "old-shell";
  report("回滚路径：全部目标恢复为原件", rolledBack);
  report("回滚路径：.old 已移回、无残留坏文件", noOld && noStagedFile);
  report("回滚路径：bat 保留（失败现场留证）", batStillThere);
  report("回滚路径：旧版壳被拉起", launched);
  report("回滚路径：日志记录失败与回滚", log.includes("apply-update END (failed, rolled back)"));
  // 日志里必须有 sha 校验失败的记录
  report("回滚路径：日志定位到 sha256 校验失败", log.includes("sha256 mismatch"));
  rmSync(root, { recursive: true, force: true });
}

// ---------- 路径 3：超时强杀 ----------
function e2eTimeoutKill() {
  const root = mkdtempSync(join(tmpdir(), "lxcode-e2e-timeout-"));
  const install = join(root, "install");
  const pending = join(root, "pending");
  const staged = join(pending, "staged");
  mkdirSync(join(install, "resources"), { recursive: true });
  mkdirSync(join(staged, "resources"), { recursive: true });

  writeFileSync(join(install, "resources\\app.asar"), "OLD-ASAR");
  writeFileSync(join(staged, "resources\\app.asar"), "NEW-ASAR");

  // 起一个真实常驻进程（占住 PID 不退出）→ 等待逻辑超时 → taskkill 强杀
  const sleeperMark = join(root, "sleeper.mark");
  const sleeper = spawn(process.execPath, [sleeperJs, sleeperMark], { stdio: "ignore" });
  waitMark(sleeperMark);

  const mark = join(root, "launched.mark");
  const plan = {
    pendingDir: pending,
    stagedDir: staged,
    shellExe: process.execPath,
    shellArgs: [`"${fakeShellJs}"`, `"${mark}"`, "new-shell"],
    pids: [sleeper.pid],
    appVersion: "0.3.0-e2e",
    pidTimeoutSec: 4, // e2e 缩窗：4s 超时强杀
    files: [{
      staged: join(staged, "resources\\app.asar"),
      target: join(install, "resources\\app.asar"),
      sha256: sha256(join(staged, "resources\\app.asar")),
    }],
  };
  writeFileSync(join(pending, "apply-update.bat"), Buffer.from(toCrlf(buildApplyBat(plan)), "utf8"));
  writeFileSync(join(pending, "apply-update.vbs"), buildApplyVbs(join(pending, "apply-update.bat")), "utf8");

  const log = runBat(pending, join(pending, "apply-update.bat"));

  const okAsar = readFileSync(join(install, "resources\\app.asar"), "utf8") === "NEW-ASAR";
  const killed = !waitMark(join(root, "sleeper-still.alive"), 100) && !existsSync(join(pending, "apply-update.bat"));
  // 验证 sleeper 真的没了：tasklist 查它的 PID
  const chk = spawnSync("tasklist", ["/FI", `PID eq ${sleeper.pid}`], { timeout: 10_000 });
  const stillAlive = chk.status === 0 && chk.stdout.toString().includes(String(sleeper.pid));
  report("超时路径：强杀后替换照常完成", okAsar);
  report("超时路径：常驻进程确实被 taskkill", !stillAlive);
  report("超时路径：日志覆盖宽限窗后的强杀收尾", log.includes("BEGIN") && log.includes("DONE (success"));
  report("超时路径：新壳被拉起", existsSync(mark) && readFileSync(mark, "utf8") === "new-shell");
  rmSync(root, { recursive: true, force: true });
  if (stillAlive) spawnSync("taskkill", ["/F", "/PID", String(sleeper.pid)]);
}

try {
  e2eSuccess();
  e2eRollback();
  e2eTimeoutKill();
} finally {
  rmSync(fakeShellJs, { force: true });
  rmSync(sleeperJs, { force: true });
}

const failed = results.filter((r) => !r.ok);
console.log(`\ne2e 结果：${results.length - failed.length}/${results.length} 通过`);
process.exit(failed.length ? 1 : 0);
