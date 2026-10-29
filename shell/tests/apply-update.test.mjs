// apply-update.test.mjs —— apply-update.ts（bat/VBS 生成纯函数）的单测。
import test from "node:test";
import assert from "node:assert/strict";
import { buildApplyBat, buildApplyFileEntries, buildApplyVbs, buildApplyManifest, toCrlf } from "../src/apply-update.ts";
import { join, relative } from "node:path";

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
  // 零探测钉子：tasklist / find / findstr / powershell 不得出现在 bat 里（e2e 实测
  // 在 wscript 隐藏会话对活 PID 探测会随机挂死，见 apply-update.ts 的 phase 1 注释；
  // 用户现场症状就是标题为「find lxcode.exe」的 cmd 窗口卡住）
  const code = bat.replace(/^rem .*/gm, ""); // 注释里可以提到这些名字（说明为什么禁用）
  assert.ok(!/\btasklist\b/i.test(code), "不得用 tasklist 探测（无控制台会话挂死）");
  assert.ok(!/\bfindstr\b/i.test(code), "不得用 findstr（PATH 依赖 + certutil 输出本地化）");
  assert.ok(!/(^|[\s|&(])find\s/i.test(code), "不得用 find（同上；现场卡死的那条命令）");
  assert.ok(!/powershell/i.test(code), "不得用 powershell 探测（同上 + 闪窗）");
  assert.ok(bat.includes('"%TASKKILL%" /F /PID %%P'), "宽限窗后无条件强杀名单内 PID（taskkill 绝对路径）");
  assert.ok(bat.includes("PIDS=4242,818"), "PID 列表来自计划（壳 + 后端）");
  assert.ok(bat.includes("PID_TIMEOUT=60"), "宽限窗默认 60s");
  assert.ok(bat.includes('"%PING%" -n %GRACE_SEC% 127.0.0.1 >nul'), "宽限窗用 ping 固定等待（零探测，绝对路径）");
  assert.ok(bat.includes("GRACE_SEC=5"), "宽限窗默认 5s（taskkill 前等壳自然退出）");
  assert.ok(bat.includes("RETRY_SEC=2") && bat.includes("STASH_RETRIES=27"), "stash 重试参数按 PID_TIMEOUT 推导（(60-5)/2=27）");
  assert.ok(bat.includes(":tryStash") && bat.includes("if not errorlevel 1 goto :stashed"), "stash 带重试循环");
  assert.ok(bat.includes("STASH_RETRIES% attempts -- another instance is holding the install files"), "重试耗尽要落日志");
  assert.ok(bat.includes('>>"%FAILED%" echo lxcode update failed: install files are locked'), "重试耗尽要写用户可见失败标记");
  assert.ok(bat.includes('set "FAILED=%PENDING%\\apply-update.failed"') && bat.includes('if exist "%FAILED%" del /f /q "%FAILED%"'), "失败标记文件每次 BEGIN 清空");
  // 强杀必须先于替换（行首标签定位子例程区）
  const killIdx = bat.indexOf('"%TASKKILL%" /F /PID %%P');
  const stashLabel = bat.indexOf("\n:stashAll\n");
  assert.ok(killIdx >= 0 && killIdx < stashLabel, "强杀在替换之前");
  assert.ok(bat.includes("DEPLOYED=1"), "落位阶段要有 DEPLOYED 门（回滚时区分新增文件）");
});

test("所有外部命令必须用绝对路径（wscript 隐藏会话的 PATH 不可靠）", () => {
  const bat = buildApplyBat(basePlan());
  // 只有 cmd 内建命令可以不写路径——它们不经过 PATH 解析。
  const builtins = new Set([
    "echo", "set", "setlocal", "if", "for", "call", "goto", "exit", "rem",
    "move", "copy", "del", "mkdir", "rmdir", "start", "type", "defined", "exist",
  ]);
  const offenders = [];
  for (const raw of bat.split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("rem ") || line.startsWith("@echo")) continue;
    // 取「命令位」：行首，或 call/goto/||/( 之后的第一段
    const cmdMatch = line.match(/^(?:\(?\s*)?([A-Za-z0-9_.\\%:-]+)/);
    if (!cmdMatch) continue;
    const cmd = cmdMatch[1];
    if (cmd.startsWith('"') || cmd.includes("%") || cmd.includes("\\") || cmd.includes(":")) continue; // 变量/绝对路径形态
    const base = cmd.toLowerCase();
    if (builtins.has(base)) continue;
    if (base.startsWith(":")) continue; // 标签
    offenders.push(line);
  }
  assert.deepEqual(offenders, [], `bat 里不得出现裸命令名（必须 %SYSROOT% 绝对路径）: ${offenders.join(" | ")}`);

  // 关键外部命令逐个点名（防止有人把绝对路径又改回裸名）
  for (const tool of ["CERTUTIL", "TASKKILL", "PING"]) {
    assert.ok(bat.includes(`set "${tool}=%SYSROOT%\\System32\\`), `${tool} 必须由 %SYSROOT%\\System32 拼出绝对路径`);
  }
  assert.ok(bat.includes('"%CERTUTIL%" -hashfile'), "certutil 调用走绝对路径");
  // certutil 的调用只能以 %CERTUTIL% 变量形式出现（不留裸调用）。
  // 只看「命令位」：注释行、echo 文本（只是打印，不执行）、以及
  // `set "CERTUTIL=..."` / `exist "%CERTUTIL%"` 这类本来就只提路径的行都不算。
  const offenders2 = [];
  for (const line of bat.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("rem ")) continue;
    for (const m of trimmed.matchAll(/(^|[\s&|(])certutil(\.exe)?(\s|$)/gi)) {
      const before = trimmed.slice(0, m.index);
      if (/\becho\b/i.test(before)) continue; // echo 的文本内容
      if (/set "CERTUTIL=|exist "%CERTUTIL%"/i.test(trimmed)) continue; // 定义/存在性检查
      offenders2.push(trimmed);
      break;
    }
  }
  assert.deepEqual(offenders2, [], `不得出现裸 certutil 调用（必须 %CERTUTIL%）: ${offenders2.join(" | ")}`);
});

test("sha256 校验不用管道 + findstr（改为 certutil 落文件 + cmd 字符串比较）", () => {
  const bat = buildApplyBat(basePlan());
  assert.ok(bat.includes('>"%HASHFILE%"'), "certutil 输出落临时文件（格式本地化也不影响比对）");
  assert.ok(bat.includes('for /f "usebackq tokens=1" %%H in ("%HASHFILE%")'), "用 for /f 读回哈希行");
  assert.ok(bat.includes('if /i "%%H"=="%~3" set "MATCH=1"'), "逐行精确比较期望哈希（大小写不敏感）");
  assert.ok(bat.includes("if not defined MATCH"), "比较失败进回滚");
  assert.ok(bat.includes("sha256 mismatch"), "失败要落日志");
});

test("逐文件替换带 sha256 校验（certutil 绝对路径），且先备份全部原件再落位", () => {
  const bat = buildApplyBat(basePlan());
  assert.ok(bat.includes('"%CERTUTIL%" -hashfile'), "落位后必须 certutil 校验（绝对路径）");
  for (const f of basePlan().files) {
    assert.ok(bat.includes(`"${f.target}"`), `计划里的每个目标都要出现: ${f.target}`);
    assert.ok(bat.includes(f.sha256), `每个文件都要带期望哈希: ${f.sha256}`);
    // staged/target 都是绝对路径，不做 %STAGED% + 绝对路径的畸形拼接
    assert.ok(bat.includes(`call :stash "${f.staged}" "${f.target}"`), "stash 调用带绝对路径");
  }
  // 阶段顺序：全部 stash 完才进入 deploy（备份先行——半备份半落位的中间态最危险）。
  // stash 现在由 :stashAll 子例程统一调用（主流程 call :stashAll 带重试），
  // 顺序看主流程里的两次 call（子例程体统一在文件尾部的子例程区）。
  const stashCall = bat.indexOf("call :stashAll");
  const deployFirst = bat.indexOf("call :deploy");
  assert.ok(stashCall >= 0 && stashCall < deployFirst, "先全部备份再全部落位");
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
  // 失败路径里针对**目标文件**的 del 只允许出现在「新增文件」分支里（被 if defined
  // DEPLOYED 门控——有 .old 的目标一律 move 回原件，绝不删；否则会把唯一原件删掉）。
  // 临时哈希文件的清理不在此列（它不碰安装目录）。
  const rbBlock = bat.slice(rbLabel, bat.indexOf("exit /b 1", rbLabel));
  for (const line of rbBlock.split("\n")) {
    if (!line.includes("del /f /q") || line.includes("%HASHFILE%")) continue;
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

test("替换计划的 staged 必须落在 pending/staged 之下（2026-10-20 事故回归钉子）", () => {
  // 事故形态：staged 取自 update-staging/dl-<ts>/payload（下载解压区），而副本
  // 被 copyTree 落到了 update-pending/staged——verifyStagedFiles 用 relative()
  // 算出的路径以 ".." 开头，于是排程前就抛「暂存路径异常」，用户只看到更新失败。
  const install = "C:\\Users\\u\\AppData\\Local\\Programs\\Lxcode";
  const stagedRoot = "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-pending\\staged";
  const payloadRoot = "C:\\Users\\u\\AppData\\Roaming\\lxcode\\update-staging\\dl-1\\payload";
  const hashes = {
    "resources\\app.asar": "a".repeat(64),
    "resources\\bin\\lxcode.exe": "b".repeat(64),
  };

  const files = buildApplyFileEntries(install, stagedRoot, hashes);
  assert.equal(files.length, 2);
  for (const f of files) {
    assert.ok(f.staged.startsWith(stagedRoot), `staged 必须指向副本目录: ${f.staged}`);
    assert.ok(f.target.startsWith(install), `target 必须落在安装目录: ${f.target}`);
    assert.ok(!relative(stagedRoot, f.staged).startsWith(".."), `不得越出 staged 根: ${f.staged}`);
  }
  assert.equal(files[0].sha256, hashes["resources\\app.asar"], "哈希原样带过来");
  assert.equal(files[0].staged, join(stagedRoot, "resources\\app.asar"), "rel 原样拼到 staged 根");

  // 正对照：事故写法（用 payload 根拼 staged）必然越界——证明这条断言有效
  const buggy = buildApplyFileEntries(install, payloadRoot, hashes);
  assert.ok(relative(stagedRoot, buggy[0].staged).startsWith(".."), "事故写法会被 verifyStagedFiles 拦下");
});

test("install/staged 两个根都不许为空或相对路径（bat 里的路径必须自洽）", () => {
  const files = buildApplyFileEntries("C:\\i", "C:\\i\\staged", { "resources\\app.asar": "c".repeat(64) });
  assert.ok(files[0].staged.startsWith("C:\\i\\staged"), "staged 根参与拼接");
  assert.ok(files[0].target.startsWith("C:\\i"), "install 根参与拼接");
});
