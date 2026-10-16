<#
.SYNOPSIS
  Builds the player release: a folder and a zip with Start-Server.exe, Start-Client.exe, the match
  host's dgsim.dll and Lua scripts, the licences and a README, and no game file.

.DESCRIPTION
  1. dgsim.dll: <GmsRoot>\gms\build\Release\dgsim.dll, refused when it is older than its sources or not the binary
     its build stamp names (re\scripts\gms_stale.ps1's rule, as run_session.ps1 checks it); the Lua scripts from
     <GmsRoot>\gms\engine\lua. GmsRoot is this checkout unless given (e.g. the main checkout from a worktree that
     didn't build gms\).
  2. Start-Server.exe and Start-Client.exe: go build of server\cmd\start-server and server\cmd\start-client from this
     checkout (windows/amd64, -trimpath, GOTOOLCHAIN=local, CGO off), with the release's name as their version
     (-X dawngate/server/internal/version.release=<Name>): a player's Start-Client logs in only on servers of the same
     version, so two releases with different names never play together.
  3. The folder <Out>\<Name>\: the two programs, host\dgsim.dll, host\lua\*.lua, README.txt (re\scripts\release),
     LICENSE.txt (the AGPL text: the checkout's LICENSE, else branch public's), THIRD-PARTY-NOTICES.txt (RakNet 4.081's LICENSE and PATENTS
     from tools\raknet\RakNet and a note on this project's RakNet patches (gms\cmake\raknet_patches),
     Go's LICENSE and (ADR-4) the licence files of every outside Go module the two programs link, from the
     module cache: modernc.org/sqlite and its dependencies, golang.org/x/crypto, golang.org/x/sys),
     BUILD.txt (commit, Go version, every file's SHA-256). The server picker's compiled class is checked to be in
     the SWF tags Start-Client.exe embeds (picker_tags.hex), and those in the built Start-Client.exe; the notices name
     its AGPL source and the development-only tools (the Flex SDK compiler, FFDec), which aren't shipped.
  4. The check: no file of the release has the name or the SHA-256 of a file of either build in
     docs\file_manifest.csv, and only the expected kinds of files are in it; then the listing.
  5. <Out>\<Name>.zip (unless -NoZip).
  Nothing is written outside <Out> (default re\out\release, gitignored).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1
  powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1 -GmsRoot <another checkout> -Out re\out\release\test
#>
param(
    [string]$Out,
    [string]$Name,
    [string]$GmsRoot,
    [switch]$NoZip
)
$ErrorActionPreference = 'Stop'
$Repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
# the main checkout (tools\raknet, the public branch's LICENSE are there also for a worktree)
$Main = $Repo
if (-not (Test-Path (Join-Path $Repo 'tools'))) {
    $common = "$(& git -C $Repo rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
    if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'tools')) { $Main = $cand } }
}
if (-not $GmsRoot) { $GmsRoot = $Repo }
$GmsRoot = (Resolve-Path $GmsRoot).Path
if (-not $Out) { $Out = Join-Path $Repo 're\out\release' }
New-Item -ItemType Directory -Force $Out | Out-Null
$Out = (Resolve-Path $Out).Path
$commit = "$(& git -C $Repo rev-parse --short=10 HEAD)".Trim()
$dirty = "$(& git -C $Repo status --porcelain --untracked-files=no -- server re/scripts/release)".Trim()
if (-not $Name) { $Name = "Dawngate-Restoration-$(Get-Date -Format 'yyyyMMdd')-$commit" }
function Fail([string]$m) { Write-Host $m -ForegroundColor Red; exit 1 }
# the name is the build's version (server\internal\version): Start-Client plays only on servers of the same one
if ($Name -notmatch '^[A-Za-z0-9._+-]{1,64}$') { Fail "-Name $Name`: 1-64 of letters, digits and . _ + - (it is the release's version)" }

# 1. the match host
. (Join-Path $PSScriptRoot 'gms_stale.ps1')
$dll = Join-Path $GmsRoot 'gms\build\Release\dgsim.dll'
$stale = Get-GmsBuildStale -Binary $dll -GmsDir (Join-Path $GmsRoot 'gms')
if ($stale) { Fail "dgsim.dll: $stale. Build gms\ in $GmsRoot first (cmake --build gms\build --config Release)." }
if (-not ([IO.File]::ReadAllText($dll, [Text.Encoding]::GetEncoding(28591))).Contains('dgsim.dll loaded')) { Fail "$dll is not dgsim.dll" }
$lua = @(Get-ChildItem (Join-Path $GmsRoot 'gms\engine\lua') -Filter '*.lua' -File)
if (-not $lua.Count) { Fail "no Lua scripts in $GmsRoot\gms\engine\lua" }

# 2. the programs
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go -and (Test-Path 'C:\Program Files\Go\bin\go.exe')) { $go = 'C:\Program Files\Go\bin\go.exe' }
if (-not $go) { Fail 'Go is needed to build the release (docs/development/building.md)' }
$dir = Join-Path $Out $Name
if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
New-Item -ItemType Directory -Force (Join-Path $dir 'host\lua') | Out-Null
$saved = @{}
foreach ($k in 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOTOOLCHAIN') { $saved[$k] = [Environment]::GetEnvironmentVariable($k) }
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'; $env:GOTOOLCHAIN = 'local'
    foreach ($p in @(@('Start-Server.exe', './cmd/start-server'), @('Start-Client.exe', './cmd/start-client'))) {
        Write-Host "building $($p[0]) ..." -ForegroundColor Cyan
        & $go build -C (Join-Path $Repo 'server') -trimpath -ldflags "-s -w -X dawngate/server/internal/version.release=$Name" -o (Join-Path $dir $p[0]) $p[1]
        if ($LASTEXITCODE -ne 0) { Fail "go build $($p[1]) failed" }
        $said = "$(& (Join-Path $dir $p[0]) --version)".Trim()
        if ($said -ne $Name) { Fail "$($p[0]) --version says '$said', not its version $Name (the linker's -X)" }
    }
    # the outside Go modules the two programs link, for their licences below ("path version dir")
    $modules = @(& $go list -C (Join-Path $Repo 'server') -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/start-server ./cmd/start-client |
        Where-Object { $_ } | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0) { Fail 'go list of the modules failed' }
} finally {
    foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
}
$goVersion = "$(& $go version)".Trim()

# the server picker: its compiled class (client_ui\serverpicker\ServerPicker.abc.hex) must be inside the four SWF tags
# that Start-Client.exe embeds (server\internal\setup\moviepatch\picker_tags.hex, go:embed), and those tags' text
# must be in the built Start-Client.exe (the release ships no other picker file)
function Read-HexFile([string]$path) {
    $t = ([IO.File]::ReadAllText($path)) -replace '\s', ''
    $b = New-Object byte[] ($t.Length / 2)
    for ($i = 0; $i -lt $b.Length; $i++) { $b[$i] = [Convert]::ToByte($t.Substring(2 * $i, 2), 16) }
    , $b
}
$tagsHex = Join-Path $Repo 'server\internal\setup\moviepatch\picker_tags.hex'
$abcHex = Join-Path $Repo 'client_ui\serverpicker\ServerPicker.abc.hex'
foreach ($f in @($tagsHex, $abcHex, (Join-Path $Repo 'client_ui\serverpicker\ServerPicker.as'))) { if (-not (Test-Path $f)) { Fail "the server picker: no $f" } }
$latin1 = [Text.Encoding]::GetEncoding(28591)
$tagsBytes = Read-HexFile $tagsHex; $abcBytes = Read-HexFile $abcHex
if (-not $latin1.GetString($tagsBytes).Contains($latin1.GetString($abcBytes))) {
    Fail "the server picker: picker_tags.hex doesn't hold ServerPicker.abc.hex's class (rebuild both with re\scripts\build_picker.py build)"
}
$firstLine = ([IO.File]::ReadAllLines($tagsHex) | Where-Object { $_.Trim() } | Select-Object -First 1).Trim()
if (-not ([IO.File]::ReadAllText((Join-Path $dir 'Start-Client.exe'), $latin1)).Contains($firstLine)) {
    Fail 'the server picker: Start-Client.exe does not embed picker_tags.hex'
}
Write-Host "the server picker: its class ($($abcBytes.Length) bytes) in the tags ($($tagsBytes.Length) bytes), embedded in Start-Client.exe" -ForegroundColor Green

# 3. the files
Copy-Item $dll (Join-Path $dir 'host\dgsim.dll')
foreach ($f in $lua) { Copy-Item $f.FullName (Join-Path $dir "host\lua\$($f.Name)") }
$readme = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'release\README.txt')).Replace('@VERSION@', $Name)
[IO.File]::WriteAllText((Join-Path $dir 'README.txt'), ($readme -replace "`r?`n", "`r`n"), (New-Object Text.UTF8Encoding($false)))
# the AGPL text: the checkout's own LICENSE (the public copy has it at its root), else branch public's
$license = $null
if (Test-Path (Join-Path $Repo 'LICENSE')) { $license = [IO.File]::ReadAllLines((Join-Path $Repo 'LICENSE')) }
else { $license = & git -C $Repo show public:LICENSE; if ($LASTEXITCODE -ne 0) { $license = $null } }
if (-not $license) { Fail 'no LICENSE: neither in the checkout nor on branch public' }
$licenseText = (($license -join "`n") + "`n") -replace "`r?`n", "`r`n"
[IO.File]::WriteAllText((Join-Path $dir 'LICENSE.txt'), $licenseText)
$raknet = Join-Path $Main 'tools\raknet\RakNet'
$goRoot = "$(& $go env GOROOT)".Trim()
$notices = @(
    'Third-party notices for the Dawngate Restoration release',
    '=========================================================',
    '',
    'host\dgsim.dll links RakNet 4.081 (https://github.com/facebookarchive/RakNet, commit 1a169895) statically.',
    'Its licence and its additional patent grant follow.',
    '',
    '---- RakNet: LICENSE ----', ''
)
foreach ($part in @(@('LICENSE', '---- RakNet: PATENTS ----'), @('PATENTS', $null))) {
    $f = Join-Path $raknet $part[0]
    if (-not (Test-Path $f)) { Fail "no $f (RakNet's licence files: setup_all.py's RakNet step)" }
    $notices += @([IO.File]::ReadAllLines($f))
    if ($part[1]) { $notices += @('', $part[1], '') }
}
# The RakNet in dgsim.dll is modified by the patch set gms\cmake\raknet_patches
$patches = @(Get-ChildItem (Join-Path $GmsRoot 'gms\cmake\raknet_patches') -Filter '*.patch' -File | Sort-Object Name | ForEach-Object { $_.Name })
if (-not $patches.Count) { Fail "no RakNet patches in $GmsRoot\gms\cmake\raknet_patches (the build applies them; the notice names them)" }
$notices += @('', '---- RakNet: the modifications in this release ----', '',
    'The RakNet in host\dgsim.dll is a modified copy of 4.081: at build time this project applies its own patch set',
    "(gms/cmake/raknet_patches in the source: $($patches -join ', ')).",
    'The patches fix defects in the receive path (split messages, ACK/NAK ranges, RangeList and BitStream reads) and',
    'harden the handshake and the receive path (handshake cookies with SipHash-2-4, written from its public',
    'specification; no IP ban of unverified senders; a limit on ordered messages waiting per connection). The',
    'defects were found by SLikeNet (MIT licence) and public reports; no SLikeNet code is used. The modified files',
    'remain under RakNet''s licence and patent grant above; the patches themselves are this project''s code (LICENSE.txt).')
$notices += @('', 'Start-Server.exe and Start-Client.exe contain the Go standard library and runtime.', '', '---- Go: LICENSE ----', '')
$notices += @([IO.File]::ReadAllLines((Join-Path $goRoot 'LICENSE')))
# ADR-4: every outside module in the programs, with each licence file at its root (LICENSE, PATENTS,
# SQLITE-LICENSE, ...), taken from the module cache the build used
if ($modules.Count) {
    $notices += @('', "Start-Server.exe and Start-Client.exe also contain these $($modules.Count) Go modules (server\go.mod, pinned by server\go.sum; ADR-4):", '')
    $notices += @($modules | ForEach-Object { '  ' + (($_ -split ' ')[0..1] -join ' ') })
    foreach ($m in $modules) {
        $path, $ver, $mdir = $m -split ' ', 3
        $lic = @(Get-ChildItem -LiteralPath $mdir -File | Where-Object { $_.Name -match '(?i)(licen[cs]e|copying|notice|patents)' } | Sort-Object Name)
        if (-not $lic.Count) { Fail "no licence file in $mdir ($path $ver): look it up before releasing" }
        foreach ($f in $lic) {
            $notices += @('', "---- $path ${ver}: $($f.Name) ----", '')
            $notices += @([IO.File]::ReadAllLines($f.FullName))
        }
    }
}
$notices += @('', 'The MSVC static runtime in dgsim.dll needs no notice.')
$notices += @('', '---- The server picker on the login screen ----', '',
    "Start-Client.exe embeds the compiled ActionScript 3 class of the server picker (four SWF tags, $([int][Math]::Ceiling($tagsBytes.Length / 1024)) KB). It is",
    "this project's own code under the GNU AGPL v3 (LICENSE.txt); its source is client_ui/serverpicker/ServerPicker.as",
    'in the source repository, published with the rest of the code. On the player''s PC, Start-Client adds these tags to',
    'a copy of the login screen''s movie taken from the player''s own game files; no file of the game is in this release.',
    'Two tools are used only during development, to compile and inspect that class; nothing of them is in this release',
    'and none of their code is in the compiled class: the Apache Flex SDK compiler (mxmlc, Apache License 2.0) and',
    'JPEXS Free Flash Decompiler (FFDec, GNU GPL v3).')
[IO.File]::WriteAllText((Join-Path $dir 'THIRD-PARTY-NOTICES.txt'), (($notices -join "`r`n") + "`r`n"))

# 4. no game file: neither a name nor a SHA-256 of either build's files (docs\file_manifest.csv)
$gameHashes = @{}; $gameNames = @{}
foreach ($row in Import-Csv (Join-Path $Repo 'docs\file_manifest.csv')) {
    foreach ($h in $row.sha256_A, $row.sha256_B) { if ($h) { $gameHashes[$h.ToLower()] = $row.path } }
    $gameNames[(Split-Path $row.path -Leaf).ToLower()] = $row.path
}
$allowed = '^(Start-Server\.exe|Start-Client\.exe|README\.txt|LICENSE\.txt|THIRD-PARTY-NOTICES\.txt|BUILD\.txt|host\\dgsim\.dll|host\\lua\\[A-Za-z0-9_]+\.lua)$'
$files = @(Get-ChildItem $dir -Recurse -File | Sort-Object FullName)
$build = @("release  $Name", "version  $Name (Start-Server.exe and Start-Client.exe; a player's Start-Client and the server must have the same)",
    "commit   $commit$(if ($dirty) { ' (with uncommitted changes in server\ or re\scripts\release)' })",
    "built    $(Get-Date -Format s)", "go       $goVersion", "dgsim    $dll (current: newer than its sources, its build stamp's bytes)",
    '', 'SHA-256 of the files:')
$problems = @()
foreach ($f in $files) {
    $rel = $f.FullName.Substring($dir.Length + 1)
    $h = (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower()
    if ($gameHashes.ContainsKey($h)) { $problems += "$rel is the game's $($gameHashes[$h])" }
    # by name: every file but our texts (the game has a readme.txt and a LICENSE.TXT too; their bytes differ, above)
    if ($f.Extension -ne '.txt' -and $gameNames.ContainsKey($f.Name.ToLower())) { $problems += "$rel has the name of the game's $($gameNames[$f.Name.ToLower()])" }
    if ($rel -notmatch $allowed) { $problems += "$rel is not a file the release should hold" }
    $build += "$h  $rel"
}
if ($problems.Count) { Fail ("the release would hold files it must not:`n  " + ($problems -join "`n  ")) }
[IO.File]::WriteAllText((Join-Path $dir 'BUILD.txt'), (($build -join "`r`n") + "`r`n"))
Write-Host "checked: none of the $($files.Count + 1) files is a game file (by name or by SHA-256 against both builds)" -ForegroundColor Green

# 5. listing and zip
$total = 0
foreach ($f in Get-ChildItem $dir -Recurse -File | Sort-Object FullName) {
    $total += $f.Length
    if ($f.DirectoryName -notlike '*\host\lua') { '{0,12:N0}  {1}' -f $f.Length, $f.FullName.Substring($dir.Length + 1) }
}
'{0,12:N0}  host\lua\*.lua ({1} files)' -f ($lua | Measure-Object Length -Sum).Sum, $lua.Count
'{0,12:N0}  in all' -f $total
if (-not $NoZip) {
    $zip = Join-Path $Out "$Name.zip"
    if (Test-Path $zip) { Remove-Item -Force $zip }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::CreateFromDirectory($dir, $zip, [IO.Compression.CompressionLevel]::Optimal, $true)
    '{0,12:N0}  {1}' -f (Get-Item $zip).Length, $zip
}
Write-Host "release: $dir" -ForegroundColor Green
