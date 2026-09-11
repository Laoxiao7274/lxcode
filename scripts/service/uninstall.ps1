# uninstall.ps1 —— 卸载 Windows 服务（保留配置/会话/日志——对齐参考项目
# rollback.sh 保留数据目录的做法；要彻底清掉手动删 %ProgramData%\lxcode）。
param([string]$Addr = "127.0.0.1:7789")

$ErrorActionPreference = "Stop"
$svc = "lxcode"
$root = Join-Path $env:ProgramData $svc

# Wait-Stopped 等服务真正停下（SCM stop 异步；没停就 delete 会失败）。
function Wait-Stopped([string]$name, [int]$timeoutSec) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Service -Name $name -ErrorAction SilentlyContinue).Status -ne "Stopped") {
        if ((Get-Date) -gt $deadline) { return }
        Start-Sleep -Milliseconds 300
    }
}

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "需要管理员权限" -ForegroundColor Red
    exit 1
}
$existing = Get-Service -Name $svc -ErrorAction SilentlyContinue
if (-not $existing) {
    Write-Host "服务未安装（无操作）"
    exit 0
}

sc.exe stop $svc | Out-Null
Wait-Stopped $svc 10
sc.exe delete $svc | Out-Null
Write-Host "服务已卸载。数据保留在 $root（config/sessions/logs），彻底清除请手动删除该目录"
