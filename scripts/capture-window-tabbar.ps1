# 抓用户正在运行的 Electron 窗口真实表面（PrintWindow + PW_RENDERFULLCONTENT），
# 扫描标签条区域逐行墨迹——定位「字上半部分没了」是否真在用户窗口里发生。
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win {
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hwnd, IntPtr hdc, uint flags);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hwnd, out RECT r);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr hwnd);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
}
"@

$proc = Get-Process electron -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $proc) { Write-Host "没有找到 Electron 窗口"; exit 1 }
$hwnd = $proc.MainWindowHandle
Write-Host "hwnd=$hwnd title=$($proc.MainWindowTitle)"

$rect = New-Object Win+RECT
[void][Win]::GetWindowRect($hwnd, [ref]$rect)
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
$dpi = [Win]::GetDpiForWindow($hwnd)
Write-Host "window=${w}x${h} at ($($rect.Left),$($rect.Top)) dpi=$dpi scale=$([math]::Round($dpi/96,2))"

$bmp = New-Object System.Drawing.Bitmap($w, $h)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$hdc = $g.GetHdc()
$ok = [Win]::PrintWindow($hwnd, $hdc, 2)  # PW_RENDERFULLCONTENT
$g.ReleaseHdc($hdc)
$g.Dispose()
Write-Host "PrintWindow ok=$ok"
$out = "C:\Users\xzy\Desktop\my\lxcode\temp\window-capture.png"
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
Write-Host "saved $out"

# 扫标签条区域（CSS y=44..80；按 DPI 缩放）——逐行墨迹数
$scale = $dpi / 96.0
$y0 = [int](40 * $scale)
$y1 = [int](86 * $scale)
Write-Host "scan rows device $y0..$y1 (client px 40..86)"
for ($y = $y0; $y -lt $y1; $y++) {
  $ink = 0
  for ($x = 0; $x -lt [Math]::Min($w, 700); $x++) {
    $c = $bmp.GetPixel($x, $y)
    $lum = 0.114 * $c.B + 0.587 * $c.G + 0.299 * $c.R
    if ($lum -lt 170) { $ink++ }
  }
  $cssY = [math]::Round($y / $scale, 1)
  Write-Host ("row $y (css $cssY): $ink")
}
$bmp.Dispose()
