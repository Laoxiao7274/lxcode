// update-script.test.mjs —— asar 冷替换脚本生成的单测（shell/src/update-script.ts）。
//
// 钉的是黑窗事故（2026-10-14）的三条修复纪律：
//   ① 零控制台子命令：脚本里不得出现 tasklist / find / timeout / start 等
//      cmd 时代命令（它们是孙进程，spawn 的 windowsHide 管不到 → 弹黑窗）；
//      等待/替换/拉起必须是进程内 cmdlet（Get-CimInstance / Move-Item /
//      Copy-Item / Start-Process / Start-Sleep）。
//   ② 等待条件按**可执行文件完整路径**匹配，不按镜像名——Go 后端与壳同名
//      （lxcode.exe），按名等待在服务/游离后端在场时死等（Ctrl+C 事故根因）。
//   ③ 等待有上限 + 超时放弃（throw），绝不变死循环；WQL/PS 字面量转义正确。
// 以及 0.2.9→0.2.10 重启失灵事故（2026-10-15）的三条新纪律：
//   ④ 兜底拉起：替换成败都必须拉起壳（finally 语义；超时 abort 除外）；
//   ⑤ 失败回滚：Move 成功而 Copy 失败时把 .old 移回原位，拉起的必须是完整版；
//   ⑥ 日志落盘：每步写 apply-update.log，失败可事后对账。
// spawn 侧的根因（detached+windowsHide → PowerShell 静默空退）在 updater.ts，
// 由「无 detached」断言在 updater 侧测试钉住（若有的话），脚本侧钉结构语义。
import test from "node:test";
import assert from "node:assert/strict";
import { buildApplyScript, psQuote, wqlQuote } from "../src/update-script.ts";

const base = {
  asar: "C:\\Apps\\Lxcode\\resources\\app.asar",
  pendingAsar: "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending\\app.asar",
  shellExe: "C:\\Apps\\Lxcode\\Lxcode.exe",
  imageName: "Lxcode.exe",
};

test("buildApplyScript：零控制台子命令（黑窗根因①）", () => {
  const s = buildApplyScript(base);
  for (const banned of ["tasklist", "find ", "timeout", "start \"\"", "@echo", "cmd /c"]) {
    assert.ok(!s.includes(banned), `脚本不得包含控制台子命令: ${banned}`);
  }
  for (const required of ["Get-CimInstance", "Move-Item", "Copy-Item", "Start-Process", "Start-Sleep"]) {
    assert.ok(s.includes(required), `脚本必须用进程内 cmdlet: ${required}`);
  }
});

test("buildApplyScript：等待按完整路径匹配而不是镜像名（死锁根因②）", () => {
  const s = buildApplyScript(base);
  // WQL 只做镜像名粗筛，真正的判定是 ExecutablePath -ieq 壳 exe 路径
  assert.ok(s.includes("ExecutablePath"), "必须按 ExecutablePath 过滤");
  assert.ok(s.includes(psQuote(base.shellExe)), "必须与壳 exe 完整路径比较");
  // 镜像名只出现在 WQL 过滤里（第一道筛），不得作为独立等待条件
  assert.ok(!/-ieq \$image\b/.test(s) && !s.includes("IMAGENAME"), "不得按镜像名做等待判定");
});

test("buildApplyScript：等待有上限且超时放弃（防死循环③）", () => {
  const s = buildApplyScript(base);
  assert.ok(s.includes("AddSeconds(90)"), `默认 90s 上限`);
  const custom = buildApplyScript({ ...base, waitTimeoutSec: 5 });
  assert.ok(custom.includes("AddSeconds(5)"), "可注入小值便于测试");
  assert.ok(custom.includes("throw"), "超时必须 throw 放弃替换");
  assert.ok(custom.includes("Start-Sleep -Milliseconds 500"), "轮询间隔 500ms（不再用非法的 timeout /nul）");
});

test("buildApplyScript：超时 abort 在替换之前、且 abort 路径不动现役 asar", () => {
  const s = buildApplyScript(base);
  const throwAt = s.indexOf("throw");
  const moveAt = s.indexOf("Move-Item");
  assert.ok(throwAt < moveAt, "超时判定必须先于 Move（超时就不动现役 asar）");
});

test("buildApplyScript：替换顺序——Move 在前、.old 在 Copy 成功后才删", () => {
  const s = buildApplyScript(base);
  const moveAt = s.indexOf("Move-Item");
  const copyAt = s.indexOf("Copy-Item");
  const delAt = s.indexOf("Remove-Item");
  assert.ok(moveAt < copyAt, "先改名旧再落新");
  assert.ok(copyAt < delAt, ".old 在 Copy 成功后才删（Copy 失败时旧版可回滚）");
  assert.ok(s.includes("'.old'") || s.includes(".old"), "保留 .old 备份语义");
});

test("buildApplyScript：兜底拉起——finally 里 Start-Process，替换失败也拉起（重启失灵根因④）", () => {
  const s = buildApplyScript(base);
  const finallyAt = s.indexOf("} finally {");
  const relaunchAt = s.indexOf("Start-Process -FilePath $exe");
  assert.ok(finallyAt > 0, "必须有 finally 块");
  assert.ok(relaunchAt > finallyAt, "拉起新壳必须在 finally 里（替换成败都拉起）");
  // 回滚在 catch 里，位于 finally 之前
  const rollbackAt = s.indexOf("rolled back");
  const catchAt = s.indexOf("} catch {");
  assert.ok(catchAt > 0 && rollbackAt > catchAt && rollbackAt < finallyAt, "回滚必须在 catch 内、finally 之前");
});

test("buildApplyScript：失败回滚——Copy 失败且 .old 在、新 asar 不在时移回（⑤）", () => {
  const s = buildApplyScript(base);
  // 回滚条件：.old 存在且 target 不存在（Move 成功 Copy 失败的精确特征）
  assert.ok(s.includes("Test-Path -LiteralPath ($target + '.old')"), "回滚判定必须检查 .old 存在");
  assert.ok(s.includes("-not (Test-Path -LiteralPath $target)"), "回滚判定必须检查新 asar 未落位");
  assert.ok(/Move-Item -Force -LiteralPath \(\$target \+ '\.old'\) -Destination \$target/.test(s), "回滚动作必须把 .old 移回原位");
});

test("buildApplyScript：日志落盘——每步写 apply-update.log（⑥）", () => {
  const s = buildApplyScript(base);
  assert.ok(s.includes("apply-update.log"), "必须有日志文件名");
  assert.ok(s.includes("Add-Content"), "必须用 Add-Content 追加日志");
  assert.ok(s.includes("Write-ULog"), "必须有日志函数");
  // 日志写入自身失败不能反噬主流程（SilentlyContinue / 空 catch）
  const logFn = s.slice(s.indexOf("function Write-ULog"), s.indexOf("function Write-ULog") + 300);
  assert.ok(logFn.includes("SilentlyContinue"), "日志函数必须吞掉自身错误");
});

test("buildApplyScript：路径含中文/空格安全（psQuote/WQL 转义）", () => {
  const cjk = {
    ...base,
    asar: "C:\\Users\\晓 字\\AppData\\Roaming\\lxcode\\resources\\app.asar",
  };
  const s = buildApplyScript(cjk);
  assert.ok(s.includes(psQuote(cjk.asar)), "中文与空格路径原样进 PS 单引号字面量");
  // WQL 过滤里镜像名合法（无引号）；wqlQuote 对单引号翻倍
  assert.equal(wqlQuote("a'b"), "a''b");
  assert.equal(psQuote("a'b"), `'a''b'`);
});

test("buildApplyScript：CRLF 行尾 + 结尾换行（PowerShell 解析器友好）", () => {
  const s = buildApplyScript(base);
  assert.ok(!s.includes("\n") || s.includes("\r\n"), "统一 CRLF");
  assert.ok(s.endsWith("\r\n") || s.endsWith("\n"), "结尾有换行");
});
