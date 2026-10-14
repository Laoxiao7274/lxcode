// update-script.ts —— asar 冷替换脚本的生成（纯函数，不 import electron，
// node:test 直测）。被 updater.ts 消费：applyUpdate 暂存好新 asar 后生成脚本、
// 脱离进程 spawn（等壳退出 → 换 asar → 拉起新壳）。
//
// 为什么是 PowerShell 而不是 cmd（2026-10-14 黑窗事故的根因修复）：
//   cmd 版脚本的等待循环每轮都要跑 tasklist / find / timeout——这些**控制台子
//   命令是 cmd 的孙进程**，spawn 的 windowsHide 只管得到直接子进程，管不到孙
//   进程新分配的控制台 → 用户桌面上弹出一个标题带 lxcode 路径的黑窗，还因为
//   等待条件卡死（见下）只能手动 Ctrl+C，Ctrl+C 短路了 && 链脚本才继续走。
//   PowerShell 版的等待/替换/拉起全部是**进程内 cmdlet**（Get-CimInstance /
//   Move-Item / Copy-Item / Start-Process），零控制台子进程，零弹窗。
//
// 顺带修掉 cmd 版的两个脚本缺陷：
//   1. 等待条件 `tasklist /FI "IMAGENAME eq lxcode.exe"` 按镜像名匹配且不分
//      大小写——Go 后端也叫 lxcode.exe（resources\bin 下），SCM 服务或任何游离
//      后端在场时循环永远等不到（死等）。现在按**可执行文件完整路径**匹配
//      （ExecutablePath -ieq 壳 exe 路径），后端/服务/其他实例都不干扰；
//   2. `timeout /t 1 /nul`——/nul 不是 timeout 的合法参数，每轮报错且退化为
//      忙循环。现在用 Start-Sleep -Milliseconds 500，且等待有上限（壳真退不
//      出来就带着错误退出、放弃替换——宁可停在旧版也不能死等或删掉现役 asar）。

/** 冷替换脚本的输入：全部绝对路径 + 壳镜像名（CIM Name 过滤的第一道筛）。 */
export interface ApplyScriptInput {
  /** 现役 app.asar 的绝对路径（将被改名 .old 再替换）。 */
  asar: string;
  /** 暂存的新 asar 绝对路径（update-pending/app.asar）。 */
  pendingAsar: string;
  /** 壳可执行文件绝对路径（等它退出；最后 Start-Process 拉起它）。 */
  shellExe: string;
  /** 壳镜像名（如 Lxcode.exe）。 */
  imageName: string;
  /** 等待壳退出的上限秒数（默认 90；测试注入小值）。 */
  waitTimeoutSec?: number;
}

/** WQL 字符串字面量转义：'' 表示一个单引号（WQL 的 LIKE/等值比较同 SQL）。 */
export function wqlQuote(s: string): string {
  return s.replace(/'/g, "''");
}

/** PowerShell 单引号字符串字面量（无插值，路径安全——Windows 文件名禁止 '）。 */
export function psQuote(s: string): string {
  return `'${s.replace(/'/g, "''")}'`;
}

/** 生成冷替换 PowerShell 脚本文本。 */
export function buildApplyScript(input: ApplyScriptInput): string {
  const wait = Math.max(1, input.waitTimeoutSec ?? 90);
  const filter = `Name='${wqlQuote(input.imageName)}'`;
  return [
    `$ErrorActionPreference = 'Stop'`,
    `$target = ${psQuote(input.asar)}`,
    `$pending = ${psQuote(input.pendingAsar)}`,
    `$exe = ${psQuote(input.shellExe)}`,
    `$deadline = (Get-Date).AddSeconds(${wait})`,
    // 等壳退出：先按镜像名粗筛（WQL 服务端过滤，快），再按完整路径精确匹配——
    // Go 后端同名（lxcode.exe）但在 resources\bin 下，路径不同，不会被误等。
    `while ((Get-Date) -lt $deadline) {`,
    `  $me = Get-CimInstance Win32_Process -Filter "${filter}" |`,
    `    Where-Object { $_.ExecutablePath -and $_.ExecutablePath -ieq $exe }`,
    `  if (-not $me) { break }`,
    `  Start-Sleep -Milliseconds 500`,
    `}`,
    `if (Get-CimInstance Win32_Process -Filter "${filter}" | Where-Object { $_.ExecutablePath -and $_.ExecutablePath -ieq $exe }) {`,
    `  throw 'Lxcode shell did not exit in time; update aborted'`,
    `}`,
    // 壳已退出：改名现役 → 落新 → 清旧 → 拉起新壳。任何一步失败都会因
    // $ErrorActionPreference='Stop' 中止（旧 asar 已改名时新壳拉不起旧版——
    // 所以 Move 放在等待确认之后，且 .old 保留到 Copy 成功才删）。
    `Move-Item -Force -LiteralPath $target -Destination ($target + '.old')`,
    `Copy-Item -Force -LiteralPath $pending -Destination $target`,
    `Remove-Item -Force -LiteralPath ($target + '.old') -ErrorAction SilentlyContinue`,
    `Start-Process -FilePath $exe`,
    ``,
  ].join("\r\n");
}
