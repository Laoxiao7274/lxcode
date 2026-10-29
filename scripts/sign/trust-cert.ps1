# trust-cert.ps1 —— 把 lxcode 自签证书公钥装进本机信任（Root + TrustedPublisher）。
# 用途：新机器上跑 lxcode 安装包前执行一次，之后 SmartScreen/AV 不再对
# CN=lxcode-selfsign 签名的产物报警。只需公钥（.cer），私钥永远留在构建机。
# 用法（管理员 PowerShell）：
#   .\scripts\sign\trust-cert.ps1 -CerPath <开发机导出的 lxcode-selfsign.cer>
# 开发机导出公钥：Export-Certificate -Cert Cert:\CurrentUser\My\<指纹> -FilePath lxcode-selfsign.cer
param(
    [Parameter(Mandatory = $true)]
    [string]$CerPath
)

if (-not (Test-Path $CerPath)) {
    Write-Error "证书文件不存在: $CerPath"
    exit 1
}

# Root：让签名链可验证；TrustedPublisher：SmartScreen/AV 对该发布者静默
Import-Certificate -FilePath $CerPath -CertStoreLocation Cert:\LocalMachine\Root | Out-Null
Import-Certificate -FilePath $CerPath -CertStoreLocation Cert:\LocalMachine\TrustedPublisher | Out-Null
Write-Host "已信任: $CerPath（Root + TrustedPublisher）"
