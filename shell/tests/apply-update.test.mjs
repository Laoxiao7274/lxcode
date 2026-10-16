// apply-update.test.mjs —— apply-update.ts（bat/VBS 生成纯函数）的单测。
import test from "node:test";
import assert from "node:assert/strict";
import { buildApplyBat, buildApplyVbs, buildApplyManifest, toCrlf } from "../src/apply-update.ts";

const basePlan = () => ({
  pendingDir: "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending",
  stagedDir: "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending\\staged",
  shellExe: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\Lxcode.exe",
  shellArgs: [],
  pids: [4242, 818],
  appVersion: "0.3.0",
  files: [
    {
      staged: "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending\\staged\\resources\\app.asar",
      target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\app.asar",
      sha256: "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111",
    },
    {
      staged: "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending\\staged\\resources\\bin\\lxcode.exe",
      target: "C:\\Users\\u\\AppData\\Local\\Programs\\lxcode\\resources\\bin\\lxcode.exe",
      sha256: "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222",
    },
  ],
});

test('等待逻辑：固定宽限窗后无条件强杀 PID（零探测——探测类命令在无控制台会话挂死）', () => {
  const bat = buildApplyBat(basePlan());
  // 零探测钉子：tasklist / powershell 不得出现在 bat 里（e2e 实测在 wscript
  // 隐藏会话对活 PID 探测会随机挂死，见 apply-update.ts 的 phase 1 注释）
  assert.ok(!/^tasklist /m.test(bat), "不得用 tasklist 探测（无控制台会话挂死）");
  assert.ok(!/powershell/i.test(bat.replace(/^rem .*/gm, "")), "不得用 powershell 探测（同上 + 闪窗）");
  assert.ok(bat.includes("taskkill /F /PID %%P"), "宽限窗后无条件强杀名单内 PID");
  assert.ok(bat.includes("PIDS=4242,818"), "PID 列表来自计划（壳 + 后端）");
  assert.ok(bat.includes("PID_TIMEOUT=60"), "宽限窗默认 60s");
  assert.ok(bat.includes("ping -n %WAIT_SEC% 127.0.0.1 >nul"), "用 ping 固定等待（零探测）");
  assert.ok(bat.includes("WAIT_SEC=62"), "等待窗 = PID_TIMEOUT + 2s 缓冲");
  // 强杀必须先于替换（行首标签定位子例程区）
  const killIdx = bat.indexOf("taskkill /F /PID %%P");
  const stashLabel = bat.indexOf("\n:stash\n");
  assert.ok(killIdx >= 0 && killIdx < stashLabel, "强杀在替换之前");
  assert.ok(bat.includes("DEPLOYED=1"), "落位阶段要有 DEPLOYED 门（回滚时区分新增文件）");
});

test("逐文件替换带 sha256 校验（certutil），且先备份全部原件再落位", () => {
  const bat = buildApplyBat(basePlan());
  assert.ok(bat.includes("certutil -hashfile"), "落位后必须 certutil 校验");
  for (const f of basePlan().files) {
    assert.ok(bat.includes(`"${f.target}"`), `计划里的每个目标都要出现: ${f.target}`);
    assert.ok(bat.includes(f.sha256), `每个文件都要带期望哈希: ${f.sha256}`);
    // staged/target 都是绝对路径，不做 %STAGED% + 绝对路径的畸形拼接
    assert.ok(bat.includes(`call :stash "${f.staged}" "${f.target}"`), "stash 调用带绝对路径");
  }
  // 阶段顺序：全部 stash 完才进入 deploy（备份先行——半备份半落位的中间态最危险）
  const stashLast = bat.lastIndexOf("call :stash");
  const deployFirst = bat.indexOf("call :deploy");
  assert.ok(stashLast < deployFirst, "先全部备份再全部落位");
});

test("失败回滚：.old 移回、无 .old 的新增文件仅回滚期才删、拉起旧壳、留日志", () => {
  const bat = buildApplyBat(basePlan());
  const rbLabel = bat.indexOf("\n:rollback\n");
  assert.ok(rbLabel > 0, "要有回滚标签");
  assert.ok(bat.includes("if defined DEPLOYED"), "新增文件只在 DEPLOYED 置位后才删（备份失败时不能删唯一原件）");
  const oldShell = bat.indexOf('start "" "%SHELL%"', rbLabel);
  assert.ok(oldShell > rbLabel, "回滚后必须拉起旧版壳");
  const okLine = bat.indexOf("all verified");
  assert.ok(okLine < rbLabel, "回滚段必须在成功段之后（独立标签）");
  // 失败路径的 del 只允许出现在「新增文件」分支里（被 if defined DEPLOYED 门控
  // ——有 .old 的目标一律 move 回原件，绝不删；否则会把唯一原件删掉）
  const rbBlock = bat.slice(rbLabel, bat.indexOf("exit /b 1", rbLabel));
  for (const line of rbBlock.split("\n")) {
    if (!line.includes("del /f /q")) continue;
    assert.ok(line.includes("if defined DEPLOYED") && line.includes(".old"), `del 必须被 DEPLOYED + .old 门控: ${line}`);
  }
  assert.ok(rbBlock.includes("update FAILED -- rolling back"), "失败要落日志（bat 内文本必须 ASCII，见 buildApplyBat docstring）");
});

test("成功收尾：删 .old、清暂存与清单/VBS、拉起新壳、bat 自删除", () => {
  const bat = buildApplyBat(basePlan());
  assert.ok(bat.includes('del /f /q "%~f0"'), "bat 必须自删除");
  assert.ok(bat.includes('rmdir /s /q "%STAGED%"'), "成功后清暂存目录");
  assert.ok(bat.includes("update-manifest.json"), "成功后清清单");
  assert.ok(bat.includes("apply-update.vbs"), "成功后清 VBS");
  const okLine = bat.indexOf("all verified");
  const cleanIdx = bat.indexOf('rmdir /s /q "%STAGED%"');
  const selfDel = bat.indexOf('del /f /q "%~f0"');
  assert.ok(okLine < cleanIdx && cleanIdx < selfDel, "顺序：校验通过 → 清理 → 自删除");
  assert.ok(bat.includes('start "" "%SHELL%" %SHELL_ARGS%'), "成功后拉起新壳");
  assert.ok(bat.includes("apply-update DONE (success"), "成功收尾落日志");
});

test("隐藏启动包装：wscript + VBS，Run(...,0,False)；不许再出现 powershell", () => {
  const vbs = buildApplyVbs("C:\\p\\apply-update.bat");
  assert.ok(vbs.includes("WScript.Shell"), "VBS 用 WScript.Shell.Run 启动");
  assert.ok(vbs.includes('"C:\\p\\apply-update.bat"'), "VBS 原样携带 bat 路径");
  assert.ok(/, 0, False/.test(vbs), "Run 第二参数 0（隐藏）第三参数 False（不等待）");
  const bat = buildApplyBat(basePlan());
  assert.ok(!/powershell/i.test(bat.replace(/^rem .*/gm, "")), "bat 不得依赖 powershell（控制台进程会闪窗；rem 注释除外）");
});

test("bat 对路径里的 % 转义（翻倍），避免吞变量展开", () => {
  const plan = basePlan();
  plan.pendingDir = "C:\\weird (100%)\\update-pending";
  plan.stagedDir = "C:\\weird (100%)\\update-pending\\staged";
  const bat = buildApplyBat(plan);
  assert.ok(bat.includes('set "PENDING=C:\\weird (100%%)\\update-pending"'), "% 必须翻倍");
});

test("bat 落盘约定：toCrlf 出 CRLF；模板文本纯 ASCII（GBK 代码页兼容）", () => {
  const bat = buildApplyBat(basePlan());
  // 生成器返回 LF 文本（无 CR）——CRLF 由 toCrlf 在落盘时统一（buildApplyBat
  // docstring：LF 会让 cmd 把 rem 行与下一行拼行解析）
  assert.ok(!bat.includes("\r"), "生成器返回 LF 文本");
  const crlf = toCrlf(bat);
  assert.ok(crlf.includes("\r\n"), "toCrlf 产出 CRLF");
  assert.ok(!crlf.replace(/\r\n/g, "").includes("\n"), "CRLF 之外不得残留裸 LF");
  // bat 模板里的可见文本必须 ASCII（GBK 代码页下 UTF-8 中文会被乱解出假命令）
  const nonAscii = [...bat].filter((ch) => ch.charCodeAt(0) > 127);
  assert.equal(nonAscii.length, 0, `bat 文本必须纯 ASCII，发现非 ASCII: ${nonAscii.slice(0, 5).join("")}`);
});

test("manifest v2：版本恒 2、字段齐全（与 bat 同源计划生成）", () => {
  const plan = basePlan();
  const m = buildApplyManifest(plan, 1735000000);
  assert.equal(m.version, 2);
  assert.equal(m.app_version, "0.3.0");
  assert.deepEqual(m.pids, [4242, 818]);
  assert.equal(m.pid_timeout_sec, 60);
  assert.deepEqual(m.files, plan.files);
  assert.equal(m.shell_exe, plan.shellExe);
});
