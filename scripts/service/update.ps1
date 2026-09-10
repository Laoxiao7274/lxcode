# update.ps1 —— 更新服务二进制（对齐参考项目 scripts/container/update.sh：
# 停服务 → 校验 → 原子替换 → 起服务 → 健康验收 → 失败自动回滚）。
# 用法（管理员 PowerShell）：
#   .\update.ps1 -File .\myt-harness.exe                # 直接更新
#   .\update.ps1 -File .\myt-harness.exe -Sha256 <hex>  # 先校验哈希再更新
param(
    [Parameter(Mandatory = $true)][string]$File,
    [string]$Sha256 = "",
    [string]$Addr = "127.0.0.1:7789"
)

$ErrorActionPreference = "Stop"
$svc = "myt-harness"
$root = Join-Path $env:ProgramData $svc
$exe = "$root\bin\myt-harness.exe"
$bak = "$root\bin\myt-harness.exe.bak"

# Wait-Stopped 等服务真正停下（SCM stop 异步；exe 还被占用时替换会失败）。
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
if (-not (Test-Path $File)) {
    Write-Host "新二进制不存在: $File" -ForegroundColor Red
    exit 1
}
if (-not (Test-Path $exe)) {
    Write-Host "服务未安装（$exe 不存在）——先 .\install.ps1" -ForegroundColor Red
    exit 1
}

# 哈希校验（对齐 update.sh --sha256：传输完整性在动手前确认）
if ($Sha256 -ne "") {
    $actual = (Get-FileHash $File -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $Sha256.ToLower()) {
        Write-Host "SHA256 不符（期望 $Sha256，实际 $actual）——拒绝更新" -ForegroundColor Red
        exit 1
    }
    Write-Host "SHA256 校验通过"
}

# 停服务（exe 被服务进程占用时 Copy-Item 会失败——先停）
sc.exe stop $svc | Out-Null
Wait-Stopped $svc 15

# 备份 → 替换（备份保留一份，验收失败时回滚用）
Copy-Item $exe $bak -Force
Copy-Item $File $exe -Force
Write-Host "二进制已替换"

# 起服务 + 验收
sc.exe start $svc | Out-Null
Start-Sleep -Seconds 2

& $exe --probe $Addr
if ($LASTEXITCODE -le 2) {  # 0=全好 2=没配模型（都算服务本身健康）
    Write-Host "`n更新完成（旧版本备份在 $bak）" -ForegroundColor Green
    exit 0
}

# 验收失败：自动回滚（对齐 deploy.sh 的失败回退）
Write-Host "`n验收失败——回滚到上一版本" -ForegroundColor Red
sc.exe stop $svc | Out-Null
Wait-Stopped $svc 15
Copy-Item $bak $exe -Force
sc.exe start $svc | Out-Null
Start-Sleep -Seconds 2
& $exe --probe $Addr
if ($LASTEXITCODE -le 2) {
    Write-Host "已回滚到旧版本" -ForegroundColor Yellow
} else {
    Write-Host "回滚后验收也失败——查看日志: $root\logs\myt-harness.log" -ForegroundColor Red
}
exit 1
