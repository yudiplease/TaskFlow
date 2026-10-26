Add-Type -AssemblyName System.Drawing
$exe = Resolve-Path "$PSScriptRoot\..\bin\TaskFlow.exe"
$vi = (Get-Item $exe).VersionInfo
"FileDescription: $($vi.FileDescription)"
"ProductVersion:  $($vi.ProductVersion)"
"ProductName:     $($vi.ProductName)"
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($exe)
if ($icon) { "ICON: embedded OK ($($icon.Width)x$($icon.Height))" } else { "ICON: MISSING" }
