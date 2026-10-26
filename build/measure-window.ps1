Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win {
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out RECT r);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
}
"@
$p = Get-Process TaskFlow -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $p) { "window not found"; exit }
$h = $p.MainWindowHandle
$wr = New-Object Win+RECT
$cr = New-Object Win+RECT
[void][Win]::GetWindowRect($h, [ref]$wr)
[void][Win]::GetClientRect($h, [ref]$cr)
"title: $($p.MainWindowTitle)"
"window rect: $($wr.L),$($wr.T) - $($wr.R),$($wr.B)  ($($wr.R-$wr.L) x $($wr.B-$wr.T))"
"client size: $($cr.R-$cr.L) x $($cr.B-$cr.T)"
