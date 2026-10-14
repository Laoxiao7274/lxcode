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

test("buildApplyScript：替换顺序——Move 在超时判定之后、.old 在 Copy 成功后才删", () => {
  const s = buildApplyScript(base);
  const throwAt = s.indexOf("throw");
  const moveAt = s.indexOf("Move-Item");
  const copyAt = s.indexOf("Copy-Item");
  const delAt = s.indexOf("Remove-Item");
  assert.ok(throwAt < moveAt, "超时判定必须先于 Move（超时就不动现役 asar）");
  assert.ok(moveAt < copyAt, "先改名旧再落新");
  assert.ok(copyAt < delAt, ".old 在 Copy 成功后才删（Copy 失败时旧版可回滚）");
  assert.ok(s.includes("'.old'") || s.includes(".old"), "保留 .old 备份语义");
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
