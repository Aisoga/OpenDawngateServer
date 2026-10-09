<#
.SYNOPSIS
  Starts the local Dawngate server for playing by hand: double-click Start-Server.cmd in the repository root.

.DESCRIPTION
  The server half of play.ps1, on its own, so the server and the game can be started separately
  (Start-Client.cmd starts the game):
    1. refuses while another lane-a session runs (re\logs\session.lock: a test session or play.ps1), or while
       another program holds the server's redirector port 42127 on 127.0.0.1;
    2. checks and builds the setup like play.ps1 -SetupOnly -NoCapture (the client copy, the certificates, the gms\
       build and the match host's dgsim.dll in client\run_854_server);
    3. builds server\bin\dgserver.exe (Go) and runs it in this window on 127.0.0.1, with the match-host copy
       client\run_854_server: PRACTICE DAWNGATE or the PvP queue starts a headless match host per game.
  While it runs it holds lane a's session lock (mode=play), so automated lane-a test sessions refuse to start
  (exit 2) instead of stopping it. Ctrl+C or closing the window stops the server; its match hosts end with it.
  No capture and no egress check (play.ps1 has them); the patched copies contact nothing outside 127.0.0.1.
  Logs: re\logs\manual_<date>-<time>\ (dgserver's log, host\<n>\ per match).

.PARAMETER NoShaperSelect
  No shaper select before a match: you play Fenmore (as play.ps1 without -ShaperSelect).

.PARAMETER ServerArgs
  Extra dgserver flags, split at blanks, e.g. '--owned starter' (new accounts own only the starter items).

.EXAMPLE
  Start-Server.cmd
  powershell -ExecutionPolicy Bypass -File re\scripts\start_server.ps1 -NoShaperSelect
#>
param(
    [switch]$NoShaperSelect,
    [string]$ServerArgs
)
$ErrorActionPreference = 'Stop'
$Repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $Repo
$LockFile = Join-Path $Repo 're\logs\session.lock'

function Get-StartStamp([int]$id) { try { (Get-Process -Id $id -ErrorAction Stop).StartTime.ToString('o') } catch { $null } }
function Get-LiveLock {
    # run_session.ps1's rule: the lock counts while the process it names (pid and start time) still runs
    if (-not (Test-Path $LockFile)) { return $null }
    $text = "$(Get-Content $LockFile -Raw -ErrorAction SilentlyContinue)".Trim()
    if ($text -match 'pid=(\d+) start=(\S+)' -and [int]$Matches[1] -ne $PID -and (Get-StartStamp ([int]$Matches[1])) -eq $Matches[2]) { return $text }
    $null
}

$live = Get-LiveLock
if ($live) {
    Write-Host "Lane a is in use: $live" -ForegroundColor Red
    Write-Host 'Wait until that session has ended (an automated test, play.ps1 or another Start-Server window), then start again.'
    exit 2
}
$held = @(Get-NetTCPConnection -State Listen -LocalPort 42127 -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in @('127.0.0.1', '0.0.0.0') })
if ($held.Count) {
    $p = Get-Process -Id $held[0].OwningProcess -ErrorAction SilentlyContinue
    Write-Host "Port 42127 (the redirector) is held by $($p.Name) (pid $($held[0].OwningProcess)): is a server already running?" -ForegroundColor Red
    exit 2
}

Write-Host 'Checking the setup (play.ps1 -SetupOnly) ...' -ForegroundColor Cyan
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'play.ps1') -SetupOnly -NoCapture
if ($LASTEXITCODE -ne 0) { Write-Host 'The setup is not ready (see above).' -ForegroundColor Red; exit 1 }

$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go -and (Test-Path 'C:\Program Files\Go\bin\go.exe')) { $go = 'C:\Program Files\Go\bin\go.exe' }
$exe = Join-Path $Repo 'server\bin\dgserver.exe'
if ($go) {
    Write-Host 'Building server\bin\dgserver.exe ...' -ForegroundColor Cyan
    & $go build -C server -o bin\dgserver.exe .\cmd\dgserver
    if ($LASTEXITCODE -ne 0) { Write-Host 'The server did not build (see above).' -ForegroundColor Red; exit 1 }
} elseif (-not (Test-Path $exe)) {
    Write-Host 'No Go and no server\bin\dgserver.exe: install Go (docs/SETUP.md).' -ForegroundColor Red; exit 1
}

$dir = Join-Path $Repo ("re\logs\manual_" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Force $dir | Out-Null
$hostCopy = Join-Path $Repo 'client\run_854_server\Dawngate'
$argList = @('--log', $dir, '--gms-dir', $hostCopy, '--gms-args=-multipleInstances', '--gms-data', (Join-Path $dir 'host'))
if (-not $NoShaperSelect) { $argList += '--shaper-select' }
if ($ServerArgs) { $argList += @($ServerArgs -split '\s+' | Where-Object { $_ }) }

Set-Content -Path $LockFile -Encoding ASCII -Value "pid=$PID start=$(Get-StartStamp $PID) name=$(Split-Path $dir -Leaf) mode=play lane=a since=$(Get-Date -Format s)"
try {
    Write-Host ''
    Write-Host 'The server runs on 127.0.0.1. Now double-click Start-Client.cmd to play.' -ForegroundColor Green
    Write-Host "Logs: $dir"
    Write-Host 'Stop the server with Ctrl+C or by closing this window.' -ForegroundColor Green
    Write-Host ''
    & $exe @argList
} finally {
    if ((Test-Path $LockFile) -and "$(Get-Content $LockFile -Raw)" -match "pid=$PID ") { Remove-Item $LockFile -Force }
    Write-Host 'Server stopped.'
}
