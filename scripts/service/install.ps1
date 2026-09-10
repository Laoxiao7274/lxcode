# install.ps1 —— 安装 myt-harness 后端为 Windows 服务（对齐参考项目
# scripts/container/deploy.sh 的形态：部署 → 健康验收 → 失败自动回退）。
#
# 布局（安装形态的固定根，等价于参考项目的 /mmc/myt-agent/）：
#   %ProgramData%\myt-harness\
#     ├─ bin\myt-harness.exe     二进制
#     ├─ config\models.json      配置（-ConfigPath 可预置；没有则写空模板，
#     │                          服务起来后热加载手改即可）
#     ├─ sessions\               会话存储（自动推导）
#     └─ logs\myt-harness.log    日志（16MB 轮转 ×3）
#
# 用法（管理员 PowerShell）：
#   .\install.ps1                                   # 用 .\myt-harness.exe 安装
#   .\install.ps1 -ExePath C:\build\myt-harness.exe # 指定二进制
#   .\install.ps1 -ConfigPath .\config\local.json   # 顺带预置配置（含 key 的那份）
param(
    [string]$ExePath = ".\myt-harness.exe",
    [string]$ConfigPath = "",                # 可选：预置的 models.json
    [string]$Addr = "127.0.0.1:7789"
)

$ErrorActionPreference = "Stop"
$svc = "myt-harness"
$root = Join-Path $env:ProgramData $svc

# Wait-Stopped 等服务真正停下（SCM stop 是异步的；没停就 delete 会失败）。
function Wait-Stopped([string]$name, [int]$timeoutSec) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Service -Name $name -ErrorAction SilentlyContinue).Status -ne "Stopped") {
        if ((Get-Date) -gt $deadline) { return }
        Start-Sleep -Milliseconds 300
    }
}

# ---- 前置检查 ----
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "需要管理员权限（sc create/管理服务）——右键 PowerShell 选「以管理员身份运行」" -ForegroundColor Red
    exit 1
}
if (-not (Test-Path $ExePath)) {
    Write-Host "二进制不存在: $ExePath（先 go build -o myt-harness.exe ./cmd/myt-harness）" -ForegroundColor Red
    exit 1
}

# 端口预检（对齐 deploy.sh 的端口预检：被占就硬拦并给指引，别让服务陷入
# 启动→失败→重启循环）
$port = ($Addr -split ":")[1]
$listening = netstat -ano | Select-String ":$port "
if ($listening) {
    $own = Get-CimInstance Win32_Service -Filter "Name='$svc'" -ErrorAction SilentlyContinue
    if ($own -and $own.State -eq "Running") {
        Write-Host "服务已在运行（占着 :$port）——如需重装先 .\uninstall.ps1" -ForegroundColor Yellow
    } else {
        Write-Host "端口 $port 已被其它进程占用（netstat -ano | findstr :$port 查 PID）。" -ForegroundColor Red
        Write-Host "先停掉占用者，或换端口：.\install.ps1 -Addr 127.0.0.1:7790"
    }
    exit 1
}

# ---- 布置文件 ----
New-Item -ItemType Directory -Force -Path "$root\bin", "$root\config", "$root\sessions", "$root\logs" | Out-Null

# 配置：预置 > 已有 > 空模板（空模板服务能起，chat.send 会报「未绑定」，
# 热加载 30s 接住手改）。.NET 写法保证无 BOM（PS5.1 的 Out-File utf8 带
# BOM，Go 的 json.Unmarshal 会直接吃不了）
$cfg = "$root\config\models.json"
if ($ConfigPath -ne "" -and (Test-Path $ConfigPath)) {
    Copy-Item $ConfigPath $cfg -Force
    Write-Host "配置已预置: $cfg"
} elseif (-not (Test-Path $cfg)) {
    [System.IO.File]::WriteAllText($cfg, '{"version":1,"models":[],"roles":{"default":"","vision":""}}', (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "空配置已生成: $cfg（编辑后 30s 内热加载生效）"
}

# 已存在同名服务：先清掉（幂等重装）
$existing = Get-Service -Name $svc -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "已存在服务 $svc，先卸载旧版…"
    & "$PSScriptRoot\uninstall.ps1" | Out-Null
}

# ---- 装服务 ----
$exe = "$root\bin\myt-harness.exe"
Copy-Item $ExePath $exe -Force
$binPath = "`"$exe`" --serve --config `"$cfg`" --addr $Addr"
sc.exe create $svc binPath= $binPath start= auto DisplayName= "myt-harness 后端" | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "sc create 失败（退出码 $LASTEXITCODE）" -ForegroundColor Red
    exit 1
}
# 崩溃自动重启（对齐 docker --restart unless-stopped：5s/5s/60s，24h 重置计数）
sc.exe failure $svc reset= 86400 actions= restart/5000/restart/5000/restart/60000 | Out-Null
Write-Host "服务已创建（开机自启 + 崩溃自动重启）"

# ---- 启动 + 健康验收（失败自动回退，对齐 deploy.sh） ----
sc.exe start $svc | Out-Null
Start-Sleep -Seconds 2

& $exe --probe $Addr
if ($LASTEXITCODE -eq 0) {
    Write-Host "`n安装完成：服务运行中（$Addr），日志 $root\logs\myt-harness.log" -ForegroundColor Green
    exit 0
} elseif ($LASTEXITCODE -eq 2) {
    Write-Host "`n安装完成：服务运行中（$Addr），但配置还没有可用模型——编辑 $cfg 后 30s 内热加载生效" -ForegroundColor Yellow
    exit 0
} else {
    Write-Host "`n验收失败——回退（停服务 + 删除）" -ForegroundColor Red
    sc.exe stop $svc | Out-Null
    Wait-Stopped $svc 10
    sc.exe delete $svc | Out-Null
    Get-Content "$root\logs\myt-harness.log" -Tail 20 -ErrorAction SilentlyContinue
    exit 1
}
