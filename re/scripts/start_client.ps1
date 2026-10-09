<#
.SYNOPSIS
  Starts the game against the local server: double-click Start-Client.cmd after Start-Server.cmd.

.DESCRIPTION
  The client half of play.ps1, on its own. It checks that a server listens on 127.0.0.1 (the redirector port 42127,
  Start-Server.cmd) and that no other Dawngate client runs, then starts the patched working copy client\run_854
  through launch_monitor.ps1 (it creates the launcher's "Global\Dawngate L2G" event, without which Dawngate.exe
  won't start) with no time limit and no capture. The window waits until you close the game.
  Log in with any e-mail and password (the first login with an e-mail creates that account), then PLAY and
  PRACTICE DAWNGATE, or the VERSUS tab's QUEUE SOLO; see docs/PLAYING.md.
  The client must be the patched copy: the original one talks to EA's servers (gone) and trusts only EA's
  certificates; setup_all.py (or play.ps1's setup check) makes and patches client\run_854.
  Logs: the newest re\logs\manual_*\client\ (the game's own log stays in %APPDATA%\DawngateData).
#>
$ErrorActionPreference = 'Stop'
$Repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $Repo
$gameDir = Join-Path $Repo 'client\run_854\Dawngate'

if (-not (Test-Path (Join-Path $gameDir 'game\Dawngate.exe'))) {
    Write-Host "No client copy in $($gameDir): run setup_all.py first (docs/PLAYING.md *One-time setup*)." -ForegroundColor Red; exit 1
}
$srv = @(Get-NetTCPConnection -State Listen -LocalPort 42127 -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in @('127.0.0.1', '0.0.0.0') })
if (-not $srv.Count) {
    Write-Host 'No server is running on 127.0.0.1: double-click Start-Server.cmd first and wait for "The server runs".' -ForegroundColor Red; exit 1
}
# one client at a time (the game's Global\Dawngate mutex); the match hosts run with -multipleInstances and don't count
$clients = @(Get-CimInstance Win32_Process -Filter "Name = 'Dawngate.exe'" | Where-Object { "$($_.CommandLine)" -notmatch '-multipleInstances' })
if ($clients.Count) {
    Write-Host "Another Dawngate client runs (pid $($clients[0].ProcessId)): close it first." -ForegroundColor Red; exit 1
}

$session = Get-ChildItem (Join-Path $Repo 're\logs') -Directory -Filter 'manual_*' -ErrorAction SilentlyContinue | Sort-Object Name | Select-Object -Last 1
$base = if ($session) { $session.FullName } else { Join-Path $Repo ("re\logs\manual_" + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
$out = Join-Path $base ("client_" + (Get-Date -Format 'HHmmss'))
New-Item -ItemType Directory -Force $out | Out-Null

Write-Host 'Starting the game (the window opens in a few seconds). Close the game to end; this window then closes too.' -ForegroundColor Green
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'launch_monitor.ps1') `
    -GameDir $gameDir -OutDir $out -Seconds 0 -NoCapture -ShotTimes none
exit $LASTEXITCODE
