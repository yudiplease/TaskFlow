# TTS synth helper for TaskFlow assistant (ASCII only: text comes from a file).
# Uses SAPI COM directly and selects the voice by exact token description -
# immune to duplicate voice names. Prefers RHVoice female voices (Elena first).
param([string]$InFile, [string]$OutFile)
$ErrorActionPreference = 'Stop'
$text = [System.IO.File]::ReadAllText($InFile, [System.Text.Encoding]::UTF8)
$v = New-Object -ComObject SAPI.SpVoice
$prefer = @('Elena', 'Tatiana', 'Anna', 'Polina')
$voices = @($v.GetVoices())
$token = $null
foreach ($n in $prefer) {
  foreach ($t in $voices) {
    if ($t.GetDescription() -eq $n) { $token = $t; break }
  }
  if ($token) { break }
}
if (-not $token) {
  foreach ($n in $prefer) {
    foreach ($t in $voices) {
      if ($t.GetDescription() -match $n) { $token = $t; break }
    }
    if ($token) { break }
  }
}
if (-not $token) { $token = $voices | Select-Object -First 1 }
if (-not $token) { throw 'no TTS voices installed' }
$v.Voice = $token
$v.Rate = 0
$fs = New-Object -ComObject SAPI.SpFileStream
$fs.Open($OutFile, 3, $false)
$v.AudioOutputStream = $fs
[void]$v.Speak($text)
$fs.Close()
