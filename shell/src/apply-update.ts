// apply-update.ts —— 更新执行器生成（纯函数，无 Electron 依赖，可单测）。
//
// 0.2.14 事故后的链路重构（用户拍板）：「壳热替换 bin + 后端引导换 asar」的
// 双进程协作退役，改为 **一个独立 bat 进程完成全部替换与拉起**——与被更新
// 对象完全解耦（Chrome/Edge/Squirrel 的本质做法）。壳只负责：下载校验 →
// 暂存（update-pending/staged，与目标结构同构）→ 生成 apply-update.bat +
// apply-update.vbs + update-manifest.json → spawn wscript 跑 bat → app.quit()。
// bat 等 PID 退出后：全部原件挪 .old → 落位 → 逐文件 sha256 校验 →
// 成功：删 .old、清理暂存、拉起新壳、自删除；失败：整体回滚、拉起旧壳、留日志。
//
// 隐藏窗口方案 = **wscript + VBS**（零窗口、经典做法），依据：
//   - cmd /c start "" /min 与 powershell -WindowStyle Hidden 都会闪控制台：
//     powershell.exe 是控制台子系统进程，OS 在它能自隐藏之前就建好了窗口；
//   - conhost --headless 未文档化、依赖 Windows Terminal 版本；
//   - wscript.exe 是 GUI 子系统进程，本身就不创建控制台；VBS 的
//     WScript.Shell.Run(..., 0, False) 以隐藏样式启动目标、不等待——
//     整条链路从头到尾没有任何可见窗口。

import { join } from "node:path";

/** 单个待替换文件：staged（暂存绝对路径）→ target（安装目录内绝对路径），
 *  sha256 为落位后目标文件应有的哈希（来自更新包逐文件清单，暂存时已校验过）。 */
export interface ApplyFileEntry {
  staged: string;
  target: string;
  sha256: string;
}

/** 组装替换计划：staged 必须指向 **pending/staged 里的副本**（bat 从这里取文件），
 *  target 是安装目录里的落位点。
 *
 *  2026-10-20 事故（「更新报错、暂存目录有文件但没排程」）的根因就在这一行：
 *  早先 staged 取的是 `update-staging/dl-<ts>/payload/<rel>`（下载解压区），
 *  而 copyTree 把副本落到了 `update-pending/staged/<rel>`——两者不在同一棵树，
 *  verifyStagedFiles 算出的相对路径以 `..` 开头，于是排程前就抛「暂存路径异常」。
 *  抽成纯函数是为了让这条映射有单测钉子（applyUpdate 本身依赖 electron 不可直测）。 */
export function buildApplyFileEntries(
  installRoot: string,
  stagedRoot: string,
  hashes: Record<string, string>,
): ApplyFileEntry[] {
  return Object.entries(hashes).map(([rel, sha256]) => ({
    staged: join(stagedRoot, rel),
    target: join(installRoot, rel),
    sha256,
  }));
}

/** bat/manifest 的生成输入。pids = 壳进程 PID + 壳自己拉起的后端 PID
 *  （直连的 SCM 服务不在列——它的二进制不在安装目录，无需等待也无需替换）。 */
export interface ApplyPlan {
  pendingDir: string;
  stagedDir: string;
  shellExe: string;
  shellArgs: string[];
  pids: number[];
  appVersion: string;
  files: ApplyFileEntry[];
  /** 等待进程退出的超时秒数（超时 taskkill 强杀）。生产 60s；e2e 测试缩短。 */
  pidTimeoutSec?: number;
  /** taskkill 前的宽限秒数（等壳自然退出）。默认 5。 */
  graceSec?: number;
  /** stash 重试之间的间隔秒数。默认 2。 */
  retrySec?: number;
}

/** update-manifest.json（v2）：bat 是可执行真相，这份清单是人对账/取证的
 *  记录（暂存→目标映射、期望哈希、PID、版本）。旧 v1（后端引导格式）作废。 */
export interface ApplyManifestV2 {
  version: 2;
  app_version: string;
  created_at_unix: number;
  shell_exe: string;
  shell_args: string[];
  pids: number[];
  pid_timeout_sec: number;
  files: ApplyFileEntry[];
}

/** bat 里的字面量转义：`%` 必须翻倍（bat 的变量展开前缀），否则路径里的
 *  百分号会吞掉后两个字符。引号在 Windows 路径里非法，不需要处理。 */
function batLiteral(s: string): string {
  return s.replace(/%/g, "%%");
}
/**
 * 生成 apply-update.bat 全文。幂等：重跑时 .old 残留会被 move /y 覆盖、
 *  已替换文件重新落位。失败语义：
 *  - 备份阶段（stash）失败：没有任何文件被改动过内容——只把已挪走的 .old
 *    移回即可（回滚里的「删除新文件」分支此时不生效，见 DEPLOYED 门）；
 *  - 落位/校验阶段失败：DEPLOYED 已置位——所有目标「有 .old 移回原件，
 *    无 .old（更新新增的文件）则删除」，旧版壳原样恢复并被拉起。
 *
 *  Windows 批处理的四条硬约束（e2e / 真机实测抓到，缺一不可）：
 *  - **行尾必须是 CRLF**：bat 以 LF 跑时 cmd 会把「注释行 + 下一行」拼成
 *    一行解析（rem 行吞掉下一行的换行），rem 里的中文被当命令执行——
 *    症状是「'xx' 不是内部或外部命令」。生成后写文件时统一 CRLF
 *    （本函数返回 LF 文本，调用方用 toCrlf 落盘）；
 *  - **内容必须是 ASCII 可编码进系统 ANSI 代码页**：cmd 在 GBK 代码页下
 *    把 UTF-8 中文按 GBK 乱解，注释/日志里的多字节序列会切割出假命令、
 *    假路径（症状是「'xx' 不是内部或外部命令」「找不到路径」）。所以
 *    bat 里的 rem 与 echo 文本一律 ASCII——bat 落盘的 apply-update.log
 *    是英文，中文说明在壳侧 update.log（UTF-8）与这份注释里。
 *  - **每个外部命令都写绝对路径（%SYSROOT% 下的完整路径）**：本 bat 由
 *    wscript 以隐藏窗口启动，那个会话的 PATH 不保证与用户 shell 一致
 *    （精简 PATH / 组策略 / 被劫持的环境变量都可能让 `taskkill`、`certutil`
 *    变成「不是内部或外部命令」，cmd 直接报 9009 并把流程带进回滚）。
 *    绝对路径是这条链路上唯一稳定的寻址方式——cmd 内建命令（echo / set /
 *    if / for / call / goto / move / copy / del / mkdir / rmdir / start）
 *    不走 PATH，保持裸写；
 *  - **不依赖 `findstr` 做哈希比对**：certutil 的输出格式随系统语言变化
 *    （中文系统是「SHA256 的 h.txt 哈希:」），而 `findstr` 本身也是外部
 *    命令。改为「certutil 算出的哈希落临时文件 → 用 set /p 读回 → 纯
 *    cmd 字符串比较」，既少一个外部依赖，也不受本地化文本影响。
 *    临时文件落在 pending 目录（可写、随更新一起清理）。
 *
 *  `find` / `findstr` / `tasklist` / `powershell` 一律不许出现在本 bat 里：
 *  前两个是 PATH 依赖 + 本地化敏感，后两个在无控制台的隐藏会话里对活进程
 *  会随机挂死（用户现场症状：一个标题为「find lxcode.exe」的 cmd 窗口卡住，
 *  Ctrl+C 打断后更新才继续——那就是老的探测式等待循环）。 */
export function buildApplyBat(plan: ApplyPlan): string {
  const files = plan.files;
  const timeout = plan.pidTimeoutSec ?? 60;
  const graceSec = plan.graceSec ?? 5;
  const retrySec = plan.retrySec ?? 2;
  const stashRetries = Math.max(1, Math.floor((timeout - graceSec) / retrySec));
  const L = (s: string) => batLiteral(s);
  const args = plan.shellArgs.join(" ");

  // staged/target 都写绝对路径（计划即真相，不依赖 %STAGED% 拼接——
  // 「%STAGED% + 绝对路径」会拼出 C:\p\stagedC:\abs 这类畸形串）。
  const stashAllLines = files.map(
    (f) => `call :stash "${L(f.staged)}" "${L(f.target)}" || exit /b 1`,
  );
  const deployLines = files.map(
    (f) => `call :deploy "${L(f.staged)}" "${L(f.target)}" "${f.sha256}" || goto :rollback`,
  );
  // deploy 内部落位后立即做 sha256 校验（见 :deploy 尾部 certutil 段）。
  const cleanOldLines = files.map(
    (f) => `if exist "${L(f.target)}.old" del /f /q "${L(f.target)}.old"`,
  );
  // 回滚行：有 .old = 原件被挪走过 → 移回；无 .old 且 DEPLOYED 已置位 =
  // 更新新增的文件 → 删掉（不能删「备份失败但原件还在」的目标——那样会把
  // 用户唯一的好文件删掉）。
  const rollbackLines = files.map(
    (f) =>
      `if exist "${L(f.target)}.old" (move /y "${L(f.target)}.old" "${L(f.target)}" >nul 2>&1) else (if defined DEPLOYED if exist "${L(f.target)}" del /f /q "${L(f.target)}")`,
  );

  return `@echo off
setlocal EnableExtensions
rem lxcode apply-update v2 -- generated by shell/src/apply-update.ts, do not edit.
rem Standalone batch process, fully decoupled from the app being updated
rem   (0.2.14 incident rework: one process does ALL replacement).
rem Flow: grace window then force-kill stale pids -> stash originals
rem   as .old -> deploy staged files -> certutil sha256 verify ->
rem   success: delete .old, cleanup, start new shell, self-delete;
rem   failure: rollback all, start old shell, keep log, exit.
rem ALL external commands use absolute paths under %SYSROOT% (see buildApplyBat
rem   docstring): a wscript-hidden session does not guarantee a usable PATH.
set "SYSROOT=%SystemRoot%"
if not exist "%SYSROOT%\\System32\\certutil.exe" set "SYSROOT=C:\\Windows"
set "CERTUTIL=%SYSROOT%\\System32\\certutil.exe"
set "TASKKILL=%SYSROOT%\\System32\\taskkill.exe"
set "PING=%SYSROOT%\\System32\\ping.exe"
set "PENDING=${L(plan.pendingDir)}"
set "STAGED=%PENDING%\\staged"
set "LOG=%PENDING%\\apply-update.log"
set "HASHFILE=%PENDING%\\apply-update.sha256"
set "SHELL=${L(plan.shellExe)}"
set "SHELL_ARGS=${L(args)}"
set "PIDS=${plan.pids.join(",")}"
set "PID_TIMEOUT=${timeout}"
set "GRACE_SEC=${graceSec}"
set "RETRY_SEC=${retrySec}"
set "STASH_RETRIES=${stashRetries}"
set "FAILED=%PENDING%\\apply-update.failed"

>>"%LOG%" echo [%date% %time%] ========== apply-update BEGIN (target ${L(plan.appVersion)}, ${files.length} files) ==========
if exist "%FAILED%" del /f /q "%FAILED%"
if not exist "%STAGED%" (
  rem bare parens in echo must be escaped inside an if (...) block
  rem (an unescaped close-paren terminates the block early; found by e2e)
  >>"%LOG%" echo [%date% %time%] staged dir missing ^(%STAGED%^) -- abort, keep current install
  exit /b 1
)
if not exist "%SHELL%" (
  >>"%LOG%" echo [%date% %time%] shell exe missing ^(%SHELL%^) -- abort, keep current install
  exit /b 1
)
if not exist "%CERTUTIL%" (
  >>"%LOG%" echo [%date% %time%] certutil missing ^(%CERTUTIL%^) -- abort, keep current install
  exit /b 1
)

rem ---- phase 1: grace window, force-kill stale pids, then RETRYING stash.
rem      No polling: tasklist AND powershell both hang at random on a LIVE
rem      pid in a windowless session (wscript hidden start; found by e2e,
rem      reproduced many times -- user saw a stuck cmd window titled
rem      "find lxcode.exe" and had to Ctrl+C it). Grace window then
rem      unconditional taskkill is the only stable shape -- taskkill on an
rem      exited pid is a harmless no-op. Stash retries with RETRY_SEC gaps:
rem      a relaunch during the grace window locks app.asar; single-shot
rem      stash used to roll back silently with no user-visible error. ----
"%PING%" -n %GRACE_SEC% 127.0.0.1 >nul
if not "%PIDS%"=="" for %%P in (%PIDS%) do "%TASKKILL%" /F /PID %%P >nul 2>&1
set "STASH_TRY=0"
:tryStash
set /a STASH_TRY+=1
call :stashAll
if not errorlevel 1 goto :stashed
if %STASH_TRY% GEQ %STASH_RETRIES% goto :stashGiveUp
>>"%LOG%" echo [%date% %time%] stash attempt %STASH_TRY% of %STASH_RETRIES% failed -- retrying
"%PING%" -n %RETRY_SEC% 127.0.0.1 >nul
goto :tryStash

:stashGiveUp
>>"%LOG%" echo [%date% %time%] stash failed after %STASH_RETRIES% attempts -- another instance is holding the install files
>>"%FAILED%" echo lxcode update failed: install files are locked by another running instance. Close all Lxcode windows and tray icons, wait a few seconds, then install again.
goto :rollback

:stashed

rem ---- phase 3: deploy staged files (rollback may delete added files) ----
set "DEPLOYED=1"
${deployLines.join("\n")}

>>"%LOG%" echo [%date% %time%] all verified -- cleanup, start new shell
${cleanOldLines.join("\n")}
if exist "%STAGED%" rmdir /s /q "%STAGED%"
if exist "%PENDING%\\update-manifest.json" del /f /q "%PENDING%\\update-manifest.json"
if exist "%PENDING%\\apply-update.vbs" del /f /q "%PENDING%\\apply-update.vbs"
if exist "%HASHFILE%" del /f /q "%HASHFILE%"
start "" "%SHELL%" %SHELL_ARGS%
>>"%LOG%" echo [%date% %time%] ========== apply-update DONE (success, new shell started) ==========
if exist "%FAILED%" del /f /q "%FAILED%"
(goto) 2>nul & del /f /q "%~f0"
exit /b 0

:rollback
if not exist "%FAILED%" >>"%FAILED%" echo lxcode update failed: replacement could not be completed and the previous version was restored. See apply-update.log in the update-pending folder.
>>"%LOG%" echo [%date% %time%] update FAILED -- rolling back to pre-update state
${rollbackLines.join("\n")}
if exist "%HASHFILE%" del /f /q "%HASHFILE%"
>>"%LOG%" echo [%date% %time%] rollback done -- starting old shell (still usable)
start "" "%SHELL%" %SHELL_ARGS%
>>"%LOG%" echo [%date% %time%] ========== apply-update END (failed, rolled back) ==========
exit /b 1

:stashAll
${stashAllLines.join("\n")}
exit /b 0

:stash
rem Stash: move original to .old (move /y overwrites stale .old from a prior
rem failed run). Missing target (file added by the update) is skipped.
if not exist "%~2" exit /b 0
move /y "%~2" "%~2.old" >nul 2>&1 || (
  >>"%LOG%" echo [%date% %time%] stash failed ^(target locked?^): %~2
  exit /b 1
)
exit /b 0

:deploy
rem Deploy: staged file must exist (missing = staging tampered, refuse to
rem update) -> ensure target dir -> copy (target is a freshly freed slot)
rem -> certutil sha256 verify (guards silent short writes / disk corruption).
rem Hash check reads certutil's raw output from a file instead of piping into
rem findstr: certutil's wording is localized and findstr itself is another
rem PATH-dependent external command (see buildApplyBat docstring).
if not exist "%~1" (
  >>"%LOG%" echo [%date% %time%] staged file missing: %~1
  exit /b 1
)
if not exist "%~dp2" mkdir "%~dp2" >nul 2>&1
copy /y "%~1" "%~2" >nul 2>&1 || (
  >>"%LOG%" echo [%date% %time%] deploy failed: %~2
  exit /b 1
)
"%CERTUTIL%" -hashfile "%~2" SHA256 >"%HASHFILE%" 2>nul
rem tokens=1 strips leading whitespace and takes the first field of each line;
rem only the bare hash line can equal the expected value (the header/footer
rem lines are localized prose). No findstr, no PATH, no locale assumptions.
set "MATCH="
for /f "usebackq tokens=1" %%H in ("%HASHFILE%") do if /i "%%H"=="%~3" set "MATCH=1"
if not defined MATCH (
  >>"%LOG%" echo [%date% %time%] sha256 mismatch: %~2 ^(expect %~3^)
  type "%HASHFILE%" >>"%LOG%" 2>nul
  exit /b 1
)
exit /b 0
`;
}

/** bat 落盘统一 CRLF（依据见 buildApplyBat 的 docstring）。 */
export function toCrlf(s: string): string {
  return s.replace(/\r?\n/g, "\r\n");
}

// ---- macOS 等价链（2026-10 用户翻案：macOS 壳列入支持，更新架构不变）----
//
// buildApplySh 生成 apply-update.sh——与 buildApplyBat 同一套语义的 POSIX sh
// 版本：按 PID 等壳与后端退出（宽限窗后 kill -9）→ 原件挪 .old → 落位 →
// shasum/sha256sum 复核 → 成功：清 .old/暂存/清单、拉起新壳、自删除；
// 失败：整体回滚、拉起旧壳、留日志。幂等性与失败语义与 bat 逐条对齐
// （DEPLOYED 门、stash 重试、失败标记文件——失败标记的内容与 bat 的
// 两条英文文本一致，updater.ts 的 FAILURE_MARKERS 消费口径单处收口）。
//
// 隐藏窗口/脱离终端方案（macOS，零新依赖）：
//   - Node spawn 直接起 /bin/sh 跑脚本即可——Unix 上没有「控制台窗口」的
//     概念，detached + stdio ignore + unref 后脚本完全脱离壳的会话与进程组
//     （setsid 等价物是 Node 的 detached 语义，见 updater.ts 的 darwin 分支），
//     壳退出不影响它；无需 osascript/nohup 包装（nohup 只防 SIGHUP，对
//     detached spawn 是多余的）。
//   - 脚本自身用 `#!/bin/sh` + chmod 755，从排程命令、日志到自删除全部
//     POSIX（dash/ash/bash 通吃），不依赖 bash 4+ 特性。
//
// 与 bat 的差异（都是有依据的平台事实，不是随手简化）：
//   - **不用 PATH 之外的绝对路径纪律**——Unix 上 /bin/sh、kill、rm、mv、
//     mkdir、rmdir 全是 POSIX 钉死的路径/内建（/bin、/usr/bin 在 mac 上恒在，
//     launchd 会话 PATH 也恒含它们），bat 那条「外部命令必须 %SYSROOT% 绝对
//     路径」的约束针对的是 wscript 隐藏会话 PATH 不可靠的 Windows 现实；
//   - 等待用 `sleep`（POSIX 内建语义的外部命令，/bin/sleep 恒在）——bat 用
//     ping 是因为 cmd 没有睡眠原语；
//   - 强杀先 SIGTERM 再 SIGKILL（等 2s 让进程优雅收尾；后端要落 SQLite）——
//     Windows taskkill /F 无优雅语义可用；
//   - 哈希用 `shasum -a 256`（macOS 自带，/usr/bin/shasum），没有时回落
//     `sha256sum`（coreutils，Linux 形态兼容）——输出裸哈希无本地化文本，
//     直接字符串比较即可（certutil 输出本地化的问题在这不存在）。

/** darwin 计划与 bat 共用 ApplyPlan（字段同义；shellExe 在 mac 上是 .app 内的
 *  可执行文件路径，即 <install>/Contents/MacOS/<productName>）。
 *
 *  控制流说明：sh 没有标签/goto，回滚做成真正的 shell 函数 `rollback`，
 *  两条失败路径（stash 重试耗尽 / deploy+校验失败）都汇到它——语义与 bat 的
 *  :rollback 段逐条对齐（有 .old 移回原件；无 .old 且 DEPLOYED 已置位 = 更新
 *  新增文件 → 删掉；写失败标记 → 拉旧壳 → 留日志 exit 1）。成功路径与 bat
 *  一致：清 .old / 暂存 / 清单 → 拉新壳 → 日志 → 自删除（rm -f "$0"）。 */
export function buildApplySh(plan: ApplyPlan): string {
  const files = plan.files;
  const timeout = plan.pidTimeoutSec ?? 60;
  const graceSec = plan.graceSec ?? 5;
  const retrySec = plan.retrySec ?? 2;
  const stashRetries = Math.max(1, Math.floor((timeout - graceSec) / retrySec));
  const shq = (s: string) => `'${s.replace(/'/g, `'\''`)}'`;
  const pending = shq(plan.pendingDir);
  const shellExe = shq(plan.shellExe);
  // 壳参数：POSIX sh 没有数组，用 set -- 恢复成 "$@"（词法级引号安全，不走
  // 无引号展开——那会在带空格的路径上拆词，且被 set -u 咬）。
  const shellArgs = plan.shellArgs.map(shq).join(" ");
  const appVersion = shq(plan.appVersion);
  const killLines = plan.pids
    .map((p) => `  kill ${shq(String(p))} 2>/dev/null || :`)
    .join("\n");
  const stashAllLines = files.map(
    (f) => `  do_stash ${shq(f.staged)} ${shq(f.target)} || return 1`,
  );
  const deployLines = files.map(
    (f) => `do_deploy ${shq(f.staged)} ${shq(f.target)} ${shq(f.sha256)} || { rollback; exit 1; }`,
  );
  const cleanOldLines = files.map(
    (f) => `[ ! -f ${shq(`${f.target}.old`)} ] || rm -f ${shq(`${f.target}.old`)}`,
  );
  const rollbackLines = files.map((f) => {
    const t = shq(f.target);
    return `  if [ -f ${t}.old ]; then mv -f ${t}.old ${t} 2>/dev/null; elif [ "$deployed" = 1 ] && [ -f ${t} ]; then rm -f ${t}; fi`;
  });
  const termKillLines = plan.pids
    .map((p) => `  kill ${shq(String(p))} 2>/dev/null || :`)
    .join("\n");
  const hardKillLines = plan.pids
    .map((p) => `  kill -9 ${shq(String(p))} 2>/dev/null || :`)
    .join("\n");
  const emptyPids = plan.pids.length === 0;

  return `#!/bin/sh
# lxcode apply-update v2 (macOS) -- generated by shell/src/apply-update.ts, do not edit.
# Standalone process, fully decoupled from the app being updated (POSIX sh;
# the shell main process launches it via Node spawn detached + stdio ignore --
# no console-window problem exists on Unix, no osascript/nohup wrapper needed).
# Flow mirrors the Windows apply-update.bat exactly: grace window then
# force-kill stale pids (TERM then KILL) -> stash originals as .old -> deploy
# staged files -> sha256 verify (shasum -a 256, fallback sha256sum; bare digest,
# no localized prose) -> success: delete .old, cleanup, start new shell,
# self-delete; failure: rollback all, start old shell, keep log, exit 1.
set -u
PENDING=${pending}
STAGED="$PENDING/staged"
LOG="$PENDING/apply-update.log"
FAILED="$PENDING/apply-update.failed"
SHELL_EXE=${shellExe}
# Launch args are baked straight into launch_shell (generator shell-quotes
# them -- shq -- so this is injection-safe; POSIX sh has no arrays and an
# unquoted expansion would split on spaces / trip set -u).
launch_shell() { ( "$SHELL_EXE" ${shellArgs || ""} >/dev/null 2>&1 & ); }
DEPLOYED=0

log_line() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOG"; }

sha256_of() {
  # NB: GNU coreutils sha256sum prefixes the digest with a binary-mode marker
  # ('\') when the input path is Windows-style backslash (MSYS/Git-Bash) or
  # '-b' was given -- strip any leading marker char ('\' or '*') so the value
  # is a bare 64-hex digest on every platform. macOS shasum never emits one.
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1 | sed 's/^[\\*]//'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1 | sed 's/^[\\*]//'
  else
    return 1
  fi
}

do_stash() {
  # stash: move original to .old (mv -f overwrites stale .old from a prior
  # failed run). Missing target (file added by the update) is skipped.
  [ ! -f "$2" ] && return 0
  if ! mv -f "$2" "$2.old" 2>>"$LOG"; then
    log_line "stash failed (target locked?): $2"
    return 1
  fi
  return 0
}

do_deploy() {
  # deploy: staged file must exist (missing = staging tampered, refuse to
  # update) -> ensure target dir -> copy (target is a freshly freed slot) ->
  # sha256 verify (guards silent short writes / disk corruption).
  if [ ! -f "$1" ]; then
    log_line "staged file missing: $1"
    return 1
  fi
  mkdir -p "$(dirname "$2")" 2>/dev/null
  if ! cp -f "$1" "$2" 2>>"$LOG"; then
    log_line "deploy failed: $2"
    return 1
  fi
  actual=$(sha256_of "$2") || { log_line "sha256 tool missing (shasum/sha256sum)"; return 1; }
  if [ "$actual" != "$3" ]; then
    log_line "sha256 mismatch: $2 (expect $3)"
    return 1
  fi
  return 0
}

rollback() {
  # rollback: .old present = original was stashed -> move back; no .old and
  # DEPLOYED set = file added by the update -> delete (never delete a target
  # whose stash failed but original is still in place -- that would destroy
  # the user's only good file; same gate as the bat).
  if [ ! -f "$FAILED" ]; then
    echo "lxcode update failed: replacement could not be completed and the previous version was restored. See apply-update.log in the update-pending folder." >> "$FAILED"
  fi
  log_line "update FAILED -- rolling back to pre-update state"
${rollbackLines.join("\n")}
  log_line "rollback done -- starting old shell (still usable)"
  launch_shell
  log_line "========== apply-update END (failed, rolled back) =========="
}

log_line "========== apply-update BEGIN (target ${appVersion}, ${files.length} files) =========="
[ ! -f "$FAILED" ] || rm -f "$FAILED"
if [ ! -d "$STAGED" ]; then
  log_line "staged dir missing ($STAGED) -- abort, keep current install"
  exit 1
fi
if [ ! -f "$SHELL_EXE" ]; then
  log_line "shell exe missing ($SHELL_EXE) -- abort, keep current install"
  exit 1
fi

# ---- phase 1: grace window, then force-kill stale pids. TERM first (the
#      backend closes SQLite gracefully), short wait, then KILL -- kill on an
#      exited pid is a harmless no-op. (The Windows bat has no graceful
#      option; taskkill /F is unconditional -- this split is a Unix affordance,
#      not a semantic deviation.) ----
sleep ${graceSec}
${emptyPids ? ": # empty pid list" : `${termKillLines}
sleep 2
${hardKillLines}`}

# ---- phase 2: RETRYING stash (a relaunch during the grace window locks
#      app.asar; single-shot stash used to roll back silently -- mirroring the
#      bat's :tryStash loop). ----
stash_try=0
stash_all() {
${stashAllLines.join("\n")}
  return 0
}
while : ; do
  stash_try=$((stash_try + 1))
  if stash_all; then break; fi
  if [ "$stash_try" -ge ${stashRetries} ]; then
    log_line "stash failed after ${stashRetries} attempts -- another instance is holding the install files"
    echo "lxcode update failed: install files are locked by another running instance. Close all Lxcode windows and tray icons, wait a few seconds, then install again." >> "$FAILED"
    rollback
    exit 1
  fi
  log_line "stash attempt $stash_try of ${stashRetries} failed -- retrying"
  sleep ${retrySec}
done

# ---- phase 3: deploy staged files (rollback may delete added files) ----
DEPLOYED=1
${deployLines.join("\n")}

log_line "all verified -- cleanup, start new shell"
${cleanOldLines.join("\n")}
rm -rf "$STAGED"
[ ! -f "$PENDING/update-manifest.json" ] || rm -f "$PENDING/update-manifest.json"
launch_shell
log_line "========== apply-update DONE (success, new shell started) =========="
[ ! -f "$FAILED" ] || rm -f "$FAILED"
rm -f "$0"
exit 0
`;
}

/** do_stash_all 的占位回填：sh 里没有子例程标签，重试循环直接展开逐文件 stash。 */
/** 生成 apply-update.vbs：以完全隐藏窗口运行 bat（依据见文件头注释）。
 *  VBS 字符串里引号写两个双引号，反斜杠无特殊含义（原样保留路径分隔符）。
 *  注释只用 ASCII——wscript 按 ANSI 代码页（GBK）读 UTF-8 无 BOM 文件，
 *  多字节序列会破坏解析（e2e 实测：中文注释的 VBS wscript 静默不执行）。 */
export function buildApplyVbs(batPath: string): string {
  const q = (s: string) => `"${s.replace(/"/g, '""')}"`;
  return `' lxcode apply-update launcher (generated by shell/src/apply-update.ts, do not edit):
' wscript.exe is a GUI-subsystem process (no console window ever appears);
' Run args: 0 = hidden window, False = do not wait.
CreateObject("WScript.Shell").Run ${q(batPath)}, 0, False
`;
}

/** 组装 update-manifest.json 的内容（对账记录；bat 才是可执行真相）。 */
export function buildApplyManifest(plan: ApplyPlan, createdAtUnix: number): ApplyManifestV2 {
  return {
    version: 2,
    app_version: plan.appVersion,
    created_at_unix: createdAtUnix,
    shell_exe: plan.shellExe,
    shell_args: plan.shellArgs,
    pids: plan.pids,
    pid_timeout_sec: plan.pidTimeoutSec ?? 60,
    files: plan.files,
  };
}
