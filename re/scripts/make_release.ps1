<#
.SYNOPSIS
  Builds the player release: a folder and a zip with Start-Server.exe, Start-Client.exe, the match
  host's dgsim.dll and Lua scripts, the licences and a README, and no game file; beside it the Linux
  server's folder and archive <Name>-linux.tar.gz.

.DESCRIPTION
  1. dgsim.dll: <GmsRoot>\gms\build\Release\dgsim.dll, refused when it is older than its sources or not the binary
     its build stamp names (re\scripts\gms_stale.ps1's rule, as run_session.ps1 checks it); the Lua scripts from
     <GmsRoot>\gms\engine\lua. GmsRoot is this checkout unless given (e.g. the main checkout from a worktree that
     didn't build gms\).
  2. Start-Server.exe and Start-Client.exe: go build of server\cmd\start-server and server\cmd\start-client from this
     checkout (windows/amd64, -trimpath, GOTOOLCHAIN=local, CGO off), with the release's name as their version
     (-X dawngate/server/internal/version.release=<Name>): a player's Start-Client logs in only on servers of the same
     version, so two releases with different names never play together. Their icon is linked from each command's
     rsrc_windows_amd64.syso (made from server\cmd\assets\dawngate.ico); the script checks that both programs hold
     every image of that .ico. The Linux server start-server is the same
     build of server\cmd\start-server for linux/amd64 with the same version (not <Name>-linux: Start-Client.exe
     refuses a server of any other version). Both programs and host\dgsim.dll get a version resource (server\tools\
     peversion: product name "OpenDawngateServer", product and file version <Name>), which code signing requires
     (SignPath: the project's name and one product version per build); it is checked on all three. With
     -SignedFiles <folder> (the CI's second run, with the files the signing service returned) those three are
     replaced by their signed copies after checking that each is this build's file plus a valid signature.
  3. The folder <Out>\<Name>\: the two programs, host\dgsim.dll, host\lua\*.lua, README.txt (re\scripts\release),
     LICENSE.txt (the AGPL text: the checkout's LICENSE, else branch public's), THIRD-PARTY-NOTICES.txt (RakNet 4.081's LICENSE and PATENTS
     from tools\raknet\RakNet and a note on this project's RakNet patches (gms\cmake\raknet_patches),
     Go's LICENSE and (ADR-4) the licence files of every outside Go module the two programs link, from the
     module cache: modernc.org/sqlite and its dependencies, golang.org/x/crypto, golang.org/x/sys),
     BUILD.txt (commit, Go version, every file's SHA-256). The compiled classes of the server picker, the bot check box and
     the chat rooms opened by default are checked to be in the SWF tags Start-Client.exe embeds (picker_tags.hex,
     matchoptions_tags.hex, chatrooms_tags.hex), and those in
     the built Start-Client.exe; the notices name their AGPL sources and the development-only tools (the Flex SDK compiler, FFDec), which aren't shipped.
     The folder <Out>\<Name>-linux\ (unless -NoLinux): start-server, the same host\ files, dawngate.service (the
     systemd unit) and README.txt (re\scripts\release\README-Linux.txt), LICENSE.txt, THIRD-PARTY-NOTICES.txt (for
     start-server's own modules) and BUILD.txt, every text with LF line ends.
  4. The check: no file of the release has the name or the SHA-256 of a file of either build in
     docs\file_manifest.csv, and only the expected kinds of files are in it; then the listing.
  5. <Out>\<Name>.zip and <Out>\<Name>-linux.tar.gz (unless -NoZip). The tar archive is written here, not by a tar
     program, so its modes don't depend on the file system it is built on: one top folder <Name>-linux/, owner and
     group 0, start-server and the folders 0755, every other file 0644, the commit's time as every entry's time.
     It is read back with the system's tar (bsdtar on Windows): the modes, the owner and every file's SHA-256.
  Nothing is written outside <Out> (default re\out\release, gitignored).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1
  powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1 -GmsRoot <another checkout> -Out re\out\release\test
#>
param(
    [string]$Out,
    [string]$Name,
    [string]$GmsRoot,
    [switch]$NoZip,
    [switch]$NoLinux,
    [string]$SignedFiles
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
$commitTime = [long]"$(& git -C $Repo log -1 --format=%ct HEAD)".Trim()
$dirty = "$(& git -C $Repo status --porcelain --untracked-files=no -- server re/scripts/release)".Trim()
if (-not $Name) { $Name = "OpenDawngateServer-$(Get-Date -Format 'yyyyMMdd')-$commit" }
function Fail([string]$m) { Write-Host $m -ForegroundColor Red; exit 1 }
# the name is the build's version (server\internal\version): Start-Client plays only on servers of the same one
if ($Name -notmatch '^[A-Za-z0-9._+-]{1,64}$') { Fail "-Name $Name`: 1-64 of letters, digits and . _ + - (it is the release's version)" }
$Linux = -not $NoLinux
$LinuxName = "$Name-linux"

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
$ldir = Join-Path $Out $LinuxName
foreach ($d in @($dir, $ldir)) { if (Test-Path $d) { Remove-Item -Recurse -Force $d } }
New-Item -ItemType Directory -Force (Join-Path $dir 'host\lua') | Out-Null
if ($Linux) { New-Item -ItemType Directory -Force (Join-Path $ldir 'host\lua') | Out-Null }
$latin1 = [Text.Encoding]::GetEncoding(28591)
function Set-PeVersion([string]$path, [string]$desc) {
    & $peversion set -version $Name -description $desc $path | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "peversion set $path failed" }
}
$saved = @{}
foreach ($k in 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOTOOLCHAIN') { $saved[$k] = [Environment]::GetEnvironmentVariable($k) }
$listModules = {
    param([string[]]$pkgs)
    # the outside Go modules these programs link, for their licences below ("path version dir")
    $m = @(& $go list -C (Join-Path $Repo 'server') -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' @pkgs |
        Where-Object { $_ } | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0) { Fail 'go list of the modules failed' }
    , $m
}
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'; $env:GOTOOLCHAIN = 'local'
    $ico = [IO.File]::ReadAllBytes((Join-Path $Repo 'server\cmd\assets\dawngate.ico'))
    # the version resource of the signed files (SignPath: product name and one product version per build), written
    # by server\tools\peversion after each build; the tool itself is built into <Out> and removed at the end
    $peversion = Join-Path $Out "peversion-$PID.exe"
    & $go build -C (Join-Path $Repo 'server') -o $peversion ./tools/peversion
    if ($LASTEXITCODE -ne 0) { Fail 'go build ./tools/peversion failed' }
    foreach ($p in @(@('Start-Server.exe', './cmd/start-server', 'OpenDawngateServer: the server'),
            @('Start-Client.exe', './cmd/start-client', "OpenDawngateServer: the game's launcher"))) {
        Write-Host "building $($p[0]) ..." -ForegroundColor Cyan
        & $go build -C (Join-Path $Repo 'server') -trimpath -ldflags "-s -w -X dawngate/server/internal/version.release=$Name" -o (Join-Path $dir $p[0]) $p[1]
        if ($LASTEXITCODE -ne 0) { Fail "go build $($p[1]) failed" }
        Set-PeVersion (Join-Path $dir $p[0]) $p[2]
        $said = "$(& (Join-Path $dir $p[0]) --version)".Trim()
        if ($said -ne $Name) { Fail "$($p[0]) --version says '$said', not its version $Name (the linker's -X)" }
        # the program's icon: the linker links the command's rsrc_windows_amd64.syso (made from this .ico by
        # go generate); every image of the .ico must be in the program
        $exe = $latin1.GetString([IO.File]::ReadAllBytes((Join-Path $dir $p[0])))
        for ($i = 0; $i -lt [BitConverter]::ToUInt16($ico, 4); $i++) {
            $img = $latin1.GetString($ico, [BitConverter]::ToUInt32($ico, 18 + 16 * $i), [BitConverter]::ToUInt32($ico, 14 + 16 * $i))
            if (-not $exe.Contains($img)) { Fail "$($p[0]) lacks image $i of server\cmd\assets\dawngate.ico (its .syso: go generate ./cmd/... in server\)" }
        }
    }
    $modules = & $listModules @('./cmd/start-server', './cmd/start-client')
    if ($Linux) {
        $env:GOOS = 'linux'
        Write-Host 'building start-server (linux/amd64) ...' -ForegroundColor Cyan
        $elf = Join-Path $ldir 'start-server'
        & $go build -C (Join-Path $Repo 'server') -trimpath -ldflags "-s -w -X dawngate/server/internal/version.release=$Name" -o $elf ./cmd/start-server
        if ($LASTEXITCODE -ne 0) { Fail 'go build ./cmd/start-server for linux/amd64 failed' }
        # it can't run here: check that it is an x86-64 ELF program and that the linker's -X put the version in
        $b = [IO.File]::ReadAllBytes($elf)
        if ($b.Length -lt 64 -or $b[0] -ne 0x7F -or $latin1.GetString($b, 1, 3) -ne 'ELF' -or $b[4] -ne 2 -or [BitConverter]::ToUInt16($b, 18) -ne 0x3E) {
            Fail "$elf is not a 64-bit x86-64 ELF program"
        }
        if (-not $latin1.GetString($b).Contains($Name)) { Fail "start-server (linux) doesn't hold its version $Name (the linker's -X)" }
        $linuxModules = & $listModules @('./cmd/start-server')
    }
} finally {
    foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
}
$goVersion = "$(& $go version)".Trim()

# our classes in the game's UI movies, the server picker, the CREATE GAME dialog's bot check box and the chat rooms
# opened by default: each compiled
# class (client_ui\<folder>\<Class>.abc.hex) must be inside the four SWF tags that Start-Client.exe embeds
# (server\internal\setup\moviepatch\<name>_tags.hex, go:embed), and those tags' text must be in the built
# Start-Client.exe (the release ships no other file of them)
function Read-HexFile([string]$path) {
    $t = ([IO.File]::ReadAllText($path)) -replace '\s', ''
    $b = New-Object byte[] ($t.Length / 2)
    for ($i = 0; $i -lt $b.Length; $i++) { $b[$i] = [Convert]::ToByte($t.Substring(2 * $i, 2), 16) }
    , $b
}
function Test-UiClass([string]$what, [string]$tagsName, [string]$folder, [string]$class) {
    $tagsHex = Join-Path $Repo "server\internal\setup\moviepatch\$tagsName"
    $abcHex = Join-Path $Repo "client_ui\$folder\$class.abc.hex"
    foreach ($f in @($tagsHex, $abcHex, (Join-Path $Repo "client_ui\$folder\$class.as"))) { if (-not (Test-Path $f)) { Fail "${what}: no $f" } }
    $tb = Read-HexFile $tagsHex; $ab = Read-HexFile $abcHex
    if (-not $latin1.GetString($tb).Contains($latin1.GetString($ab))) {
        Fail "${what}: $tagsName doesn't hold $class.abc.hex's class (rebuild both with re\scripts\build_picker.py build)"
    }
    $first = ([IO.File]::ReadAllLines($tagsHex) | Where-Object { $_.Trim() } | Select-Object -First 1).Trim()
    if (-not ([IO.File]::ReadAllText((Join-Path $dir 'Start-Client.exe'), $latin1)).Contains($first)) {
        Fail "${what}: Start-Client.exe does not embed $tagsName"
    }
    Write-Host "${what}: its class ($($ab.Length) bytes) in the tags ($($tb.Length) bytes), embedded in Start-Client.exe" -ForegroundColor Green
    @{ Bytes = $tb; First = $first }
}
$picker = Test-UiClass 'the server picker' 'picker_tags.hex' 'serverpicker' 'ServerPicker'
$botBox = Test-UiClass 'the bot check box' 'matchoptions_tags.hex' 'matchoptions' 'BotCheckbox'
$chatRooms = Test-UiClass 'the chat rooms opened by default' 'chatrooms_tags.hex' 'chatrooms' 'AutoRooms'
$tagsBytes = $picker.Bytes; $firstLine = $picker.First
# start-server links the setup with the tags too (only Start-Client.exe uses them); its notice names them if it holds them
$linuxPicker = $Linux -and ([IO.File]::ReadAllText((Join-Path $ldir 'start-server'), $latin1)).Contains($firstLine)

# 3. the files
$utf8 = New-Object Text.UTF8Encoding($false)
function Write-Text([string]$path, [string[]]$lines, [string]$eol) { [IO.File]::WriteAllText($path, (($lines -join $eol) + $eol), $utf8) }
# the DLL with its version resource (as the programs'); the Linux folder gets the same file
Copy-Item $dll (Join-Path $dir 'host\dgsim.dll')
Set-PeVersion (Join-Path $dir 'host\dgsim.dll') 'OpenDawngateServer: the match host'
& $peversion check -version $Name (Join-Path $dir 'Start-Server.exe') (Join-Path $dir 'Start-Client.exe') (Join-Path $dir 'host\dgsim.dll')
if ($LASTEXITCODE -ne 0) { Fail 'a signed file lacks its version resource (product name, product version)' }
# -SignedFiles: the three files as the code signing service returned them (CI: SignPath). Each must be this build's
# file with an Authenticode signature added and nothing else changed (peversion signed) and Windows must find the
# signature valid; they replace the built ones before BUILD.txt and the archives, so BUILD.txt's SHA-256 (which
# Start-Client's update checks) are the signed files'
$signer = ''
if ($SignedFiles) {
    if (-not (Test-Path $SignedFiles)) { Fail "-SignedFiles $SignedFiles`: no such folder" }
    foreach ($f in @('Start-Server.exe', 'Start-Client.exe', 'host\dgsim.dll')) {
        $leaf = Split-Path $f -Leaf
        $found = @(Get-ChildItem $SignedFiles -Recurse -File -Filter $leaf)
        if ($found.Count -ne 1) { Fail "-SignedFiles: $($found.Count) files named $leaf in $SignedFiles, not one" }
        & $peversion signed -version $Name (Join-Path $dir $f) $found[0].FullName
        if ($LASTEXITCODE -ne 0) { Fail "-SignedFiles: $leaf is not this build's file with a signature added" }
        $sig = Get-AuthenticodeSignature $found[0].FullName
        if ($sig.Status -ne 'Valid') { Fail "-SignedFiles: the signature of $leaf is $($sig.Status) ($($sig.StatusMessage))" }
        Copy-Item $found[0].FullName (Join-Path $dir $f) -Force
        $signer = $sig.SignerCertificate.Subject
        Write-Host "signed: $f ($signer)" -ForegroundColor Green
    }
}
Remove-Item $peversion
foreach ($d in @($dir) + @(if ($Linux) { $ldir })) {
    if ($d -ne $dir) { Copy-Item (Join-Path $dir 'host\dgsim.dll') (Join-Path $d 'host\dgsim.dll') }
    foreach ($f in $lua) { Copy-Item $f.FullName (Join-Path $d "host\lua\$($f.Name)") }
}
$readme = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'release\README.txt')).Replace('@VERSION@', $Name)
[IO.File]::WriteAllText((Join-Path $dir 'README.txt'), ($readme -replace "`r?`n", "`r`n"), $utf8)
if ($Linux) {
    $readme = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'release\README-Linux.txt')).Replace('@VERSION@', $Name)
    [IO.File]::WriteAllText((Join-Path $ldir 'README.txt'), ($readme -replace "`r?`n", "`n"), $utf8)
    $unit = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'release\dawngate.service'))
    [IO.File]::WriteAllText((Join-Path $ldir 'dawngate.service'), ($unit -replace "`r?`n", "`n"), $utf8)
}
# the AGPL text: the checkout's own LICENSE (the public copy has it at its root), else branch public's
$license = $null
if (Test-Path (Join-Path $Repo 'LICENSE')) { $license = [IO.File]::ReadAllLines((Join-Path $Repo 'LICENSE')) }
else { $license = & git -C $Repo show public:LICENSE; if ($LASTEXITCODE -ne 0) { $license = $null } }
if (-not $license) { Fail 'no LICENSE: neither in the checkout nor on branch public' }
$licenseText = (($license -join "`n") + "`n") -replace "`r?`n", "`r`n"
[IO.File]::WriteAllText((Join-Path $dir 'LICENSE.txt'), $licenseText)
if ($Linux) { [IO.File]::WriteAllText((Join-Path $ldir 'LICENSE.txt'), ($licenseText -replace "`r`n", "`n")) }
$raknet = Join-Path $Main 'tools\raknet\RakNet'
$goRoot = "$(& $go env GOROOT)".Trim()
$patches = @(Get-ChildItem (Join-Path $GmsRoot 'gms\cmake\raknet_patches') -Filter '*.patch' -File | Sort-Object Name | ForEach-Object { $_.Name })
if (-not $patches.Count) { Fail "no RakNet patches in $GmsRoot\gms\cmake\raknet_patches (the build applies them; the notice names them)" }
# the notices: $sep the path separator of the release's platform, $programs the Go programs ("X and Y" or one),
# $contain its verb, $mods their outside modules, $pickerFirst the picker paragraph's first line ($null: none)
function New-Notices([string]$sep, [string]$programs, [string]$contain, [string[]]$mods, [string]$pickerFirst) {
    $n = @(
        'Third-party notices for the OpenDawngateServer release',
        '=========================================================',
        '',
        "host${sep}dgsim.dll links RakNet 4.081 (https://github.com/facebookarchive/RakNet, commit 1a169895) statically.",
        'Its licence and its additional patent grant follow.',
        '',
        '---- RakNet: LICENSE ----', ''
    )
    foreach ($part in @(@('LICENSE', '---- RakNet: PATENTS ----'), @('PATENTS', $null))) {
        $f = Join-Path $raknet $part[0]
        if (-not (Test-Path $f)) { Fail "no $f (RakNet's licence files: setup_all.py's RakNet step)" }
        $n += @([IO.File]::ReadAllLines($f))
        if ($part[1]) { $n += @('', $part[1], '') }
    }
    $n += @('', '---- RakNet: the modifications in this release ----', '',
        "The RakNet in host${sep}dgsim.dll is a modified copy of 4.081: at build time this project applies its own patch set",
        "(gms/cmake/raknet_patches in the source: $($patches -join ', ')).",
        'The patches fix defects in the receive path (split messages, ACK/NAK ranges, RangeList and BitStream reads) and',
        'harden the handshake and the receive path (handshake cookies with SipHash-2-4, written from its public',
        'specification; no IP ban of unverified senders; a limit on ordered messages waiting per connection). The',
        'defects were found by SLikeNet (MIT licence) and public reports; no SLikeNet code is used. The modified files',
        'remain under RakNet''s licence and patent grant above; the patches themselves are this project''s code (LICENSE.txt).')
    $n += @('', "$programs $contain the Go standard library and runtime.", '', '---- Go: LICENSE ----', '')
    $n += @([IO.File]::ReadAllLines((Join-Path $goRoot 'LICENSE')))
    # ADR-4: every outside module in the programs, with each licence file at its root (LICENSE, PATENTS,
    # SQLITE-LICENSE, ...), taken from the module cache the build used
    if ($mods.Count) {
        $n += @('', "$programs also $contain these $($mods.Count) Go modules (server${sep}go.mod, pinned by server${sep}go.sum; ADR-4):", '')
        $n += @($mods | ForEach-Object { '  ' + (($_ -split ' ')[0..1] -join ' ') })
        foreach ($m in $mods) {
            $path, $ver, $mdir = $m -split ' ', 3
            $lic = @(Get-ChildItem -LiteralPath $mdir -File | Where-Object { $_.Name -match '(?i)(licen[cs]e|copying|notice|patents)' } | Sort-Object Name)
            if (-not $lic.Count) { Fail "no licence file in $mdir ($path $ver): look it up before releasing" }
            foreach ($f in $lic) {
                $n += @('', "---- $path ${ver}: $($f.Name) ----", '')
                $n += @([IO.File]::ReadAllLines($f.FullName))
            }
        }
    }
    $n += @('', 'The MSVC static runtime in dgsim.dll needs no notice.')
    if ($pickerFirst) {
        $n += @('', '---- The server picker, the bot check box of CREATE GAME and the chat rooms opened by default ----', '',
            $pickerFirst,
            "this project's own code under the GNU AGPL v3 (LICENSE.txt); their sources are client_ui/serverpicker/ServerPicker.as",
            'client_ui/matchoptions/BotCheckbox.as and client_ui/chatrooms/AutoRooms.as in the source repository, published with',
            'the rest of the code. On the player''s PC, Start-Client adds these tags to copies of the login screen''s, the CREATE',
            'GAME dialog''s and the chat''s movies taken',
            'from the player''s own game files; no file of the game is in this release.',
            'Two tools are used only during development, to compile and inspect those classes; nothing of them is in this release',
            'and none of their code is in the compiled classes: the Apache Flex SDK compiler (mxmlc, Apache License 2.0) and',
            'JPEXS Free Flash Decompiler (FFDec, GNU GPL v3).')
    }
    , $n
}
$tagsKB = [int][Math]::Ceiling($tagsBytes.Length / 1024)
$botKB = [int][Math]::Ceiling($botBox.Bytes.Length / 1024)
$roomsKB = [int][Math]::Ceiling($chatRooms.Bytes.Length / 1024)
$notices = New-Notices '\' 'Start-Server.exe and Start-Client.exe' 'contain' $modules `
    "Start-Client.exe embeds the compiled ActionScript 3 classes of the server picker, the bot check box and the chat rooms opened by default (four SWF tags each, $tagsKB KB, $botKB KB and $roomsKB KB). They are"
[IO.File]::WriteAllText((Join-Path $dir 'THIRD-PARTY-NOTICES.txt'), (($notices -join "`r`n") + "`r`n"))
if ($Linux) {
    $pf = if ($linuxPicker) { "start-server holds the compiled ActionScript 3 classes of the server picker, the bot check box and the chat rooms opened by default (four SWF tags each, $tagsKB KB, $botKB KB and $roomsKB KB; only the Windows Start-Client.exe uses them). They are" } else { $null }
    $notices = New-Notices '/' 'start-server' 'contains' $linuxModules $pf
    [IO.File]::WriteAllText((Join-Path $ldir 'THIRD-PARTY-NOTICES.txt'), (($notices -join "`n") + "`n"))
}

# 4. no game file: neither a name nor a SHA-256 of either build's files (docs\file_manifest.csv)
$gameHashes = @{}; $gameNames = @{}
foreach ($row in Import-Csv (Join-Path $Repo 'docs\file_manifest.csv')) {
    foreach ($h in $row.sha256_A, $row.sha256_B) { if ($h) { $gameHashes[$h.ToLower()] = $row.path } }
    $gameNames[(Split-Path $row.path -Leaf).ToLower()] = $row.path
}
$built = Get-Date -Format s
# the BUILD.txt lines of folder $d ($head, then every file's SHA-256 with its path joined by $sep); refuses a game
# file and any file that $allowed (a pattern over the Windows-style relative path) doesn't name
function Get-BuildLines([string]$d, [string]$allowed, [string]$sep, [string[]]$head) {
    $lines = @($head) + @('', 'SHA-256 of the files:')
    $problems = @()
    $fs = @(Get-ChildItem $d -Recurse -File | Sort-Object FullName)
    foreach ($f in $fs) {
        $rel = $f.FullName.Substring($d.Length + 1)
        $h = (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower()
        if ($gameHashes.ContainsKey($h)) { $problems += "$rel is the game's $($gameHashes[$h])" }
        # by name: every file but our texts (the game has a readme.txt and a LICENSE.TXT too; their bytes differ, above)
        if ($f.Extension -ne '.txt' -and $gameNames.ContainsKey($f.Name.ToLower())) { $problems += "$rel has the name of the game's $($gameNames[$f.Name.ToLower()])" }
        if ($rel -notmatch $allowed) { $problems += "$rel is not a file the release should hold" }
        $lines += "$h  $($rel.Replace('\', $sep))"
    }
    if ($problems.Count) { Fail ("the release would hold files it must not:`n  " + ($problems -join "`n  ")) }
    Write-Host "checked: none of the $($fs.Count + 1) files in $(Split-Path $d -Leaf) is a game file (by name or by SHA-256 against both builds)" -ForegroundColor Green
    , $lines
}
$dirtyNote = if ($dirty) { ' (with uncommitted changes in server\ or re\scripts\release)' } else { '' }
$build = Get-BuildLines $dir '^(Start-Server\.exe|Start-Client\.exe|README\.txt|LICENSE\.txt|THIRD-PARTY-NOTICES\.txt|BUILD\.txt|host\\dgsim\.dll|host\\lua\\[A-Za-z0-9_]+\.lua)$' '\' @(
    "release  $Name", "version  $Name (Start-Server.exe and Start-Client.exe; a player's Start-Client and the server must have the same)",
    "commit   $commit$dirtyNote",
    "built    $built", "go       $goVersion", "dgsim    $dll (current: newer than its sources, its build stamp's bytes)",
    "signed   $(if ($signer) { "Start-Server.exe, Start-Client.exe, host\dgsim.dll: $signer" } else { 'no (unsigned)' })")
[IO.File]::WriteAllText((Join-Path $dir 'BUILD.txt'), (($build -join "`r`n") + "`r`n"))
if ($Linux) {
    $build = Get-BuildLines $ldir '^(start-server|dawngate\.service|README\.txt|LICENSE\.txt|THIRD-PARTY-NOTICES\.txt|BUILD\.txt|host\\dgsim\.dll|host\\lua\\[A-Za-z0-9_]+\.lua)$' '/' @(
        "release  $Name (the Linux server: start-server for linux/amd64, its match hosts under Wine)",
        "version  $Name (start-server; a player's Start-Client.exe and the server must have the same)",
        "commit   $commit$($dirtyNote.Replace('\', '/'))",
        "built    $built", "go       $goVersion", "dgsim    host/dgsim.dll is the Windows release's (the same file: current, newer than its sources, its build stamp's bytes)")
    Write-Text (Join-Path $ldir 'BUILD.txt') $build "`n"
}

# 5. listing and archives
function Show-Listing([string]$d) {
    $total = 0
    foreach ($f in Get-ChildItem $d -Recurse -File | Sort-Object FullName) {
        $total += $f.Length
        if ($f.DirectoryName -notlike '*\host\lua') { '{0,12:N0}  {1}' -f $f.Length, $f.FullName.Substring($d.Length + 1) }
    }
    '{0,12:N0}  host\lua\*.lua ({1} files)' -f ($lua | Measure-Object Length -Sum).Sum, $lua.Count
    '{0,12:N0}  in all' -f $total
}
Show-Listing $dir
if (-not $NoZip) {
    $zip = Join-Path $Out "$Name.zip"
    if (Test-Path $zip) { Remove-Item -Force $zip }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::CreateFromDirectory($dir, $zip, [IO.Compression.CompressionLevel]::Optimal, $true)
    '{0,12:N0}  {1}' -f (Get-Item $zip).Length, $zip
}
Write-Host "release: $dir" -ForegroundColor Green
if (-not $Linux) { exit 0 }

# The Linux archive: a POSIX ustar stream in gzip, written here so that the modes and the owner are the layout's
# whatever the file system (NTFS has no Unix modes). Entries: each folder, then its files and subfolders by name.
function Get-TarMode([string]$rel, [bool]$isDir) { if ($isDir -or $rel -eq 'start-server') { 0x1ED } else { 0x1A4 } } # 0755, 0644
function New-TarHeader([string]$name, [int]$mode, [long]$size, [long]$mtime, [char]$type) {
    if ($name -cmatch '[^\x21-\x7E]') { Fail "tar: $name has a character other than printable ASCII" }
    $prefix = ''
    if ($name.Length -gt 100) { # ustar: the path split at a '/' into prefix (<= 155) and name (<= 100)
        $cut = -1
        for ($i = [Math]::Min(155, $name.Length - 2); $i -gt 0; $i--) {
            if ($name[$i] -eq '/' -and $name.Length - $i - 1 -le 100) { $cut = $i; break }
        }
        if ($cut -lt 0) { Fail "tar: the path $name is too long for ustar" }
        $prefix = $name.Substring(0, $cut); $name = $name.Substring($cut + 1)
    }
    $h = New-Object byte[] 512
    $put = { param($off, $s) $b = [Text.Encoding]::ASCII.GetBytes($s); [Array]::Copy($b, 0, $h, $off, $b.Length) }
    $oct = { param($v, $w) [Convert]::ToString([long]$v, 8).PadLeft($w, '0') }
    & $put 0 $name
    & $put 100 ((& $oct $mode 7) + "`0")
    & $put 108 '0000000'; & $put 116 '0000000' # uid, gid 0 (no user or group name: numeric)
    & $put 124 ((& $oct $size 11) + "`0")
    & $put 136 ((& $oct $mtime 11) + "`0")
    & $put 148 '        '
    $h[156] = [byte]$type
    & $put 257 "ustar`0"; & $put 263 '00'
    & $put 329 '0000000'; & $put 337 '0000000'
    & $put 345 $prefix
    $sum = 0; foreach ($x in $h) { $sum += $x }
    & $put 148 ((& $oct $sum 6) + "`0 ")
    , $h
}
$tarPath = Join-Path $Out "$LinuxName.tar.gz"
if (-not $NoZip) {
    if (Test-Path $tarPath) { Remove-Item -Force $tarPath }
    $expect = [ordered]@{} # path in the archive -> mode, for the read-back
    $fsOut = [IO.File]::Create($tarPath)
    try {
        $gz = New-Object IO.Compression.GZipStream($fsOut, [IO.Compression.CompressionLevel]::Optimal)
        $written = [long]0
        $emit = { param([byte[]]$b, [int]$n) $gz.Write($b, 0, $n); $script:written += $n }
        $walk = {
            param([string]$full, [string]$rel)
            $arcName = if ($rel) { "$LinuxName/$($rel.Replace('\', '/'))/" } else { "$LinuxName/" }
            & $emit (New-TarHeader $arcName 0x1ED 0 $commitTime '5') 512
            $expect[$arcName] = 0x1ED
            [string[]]$kids = @(Get-ChildItem -LiteralPath $full | ForEach-Object { $_.Name })
            [Array]::Sort($kids, [StringComparer]::Ordinal)
            foreach ($k in $kids) {
                $kf = Join-Path $full $k
                $kr = if ($rel) { "$rel\$k" } else { $k }
                if (Test-Path -LiteralPath $kf -PathType Container) { & $walk $kf $kr; continue }
                $bytes = [IO.File]::ReadAllBytes($kf)
                $mode = Get-TarMode $kr $false
                $an = "$LinuxName/$($kr.Replace('\', '/'))"
                & $emit (New-TarHeader $an $mode $bytes.Length $commitTime '0') 512
                if ($bytes.Length) { & $emit $bytes $bytes.Length }
                $pad = (512 - $bytes.Length % 512) % 512
                if ($pad) { & $emit (New-Object byte[] $pad) $pad }
                $expect[$an] = $mode
            }
        }
        & $walk $ldir ''
        & $emit (New-Object byte[] 1024) 1024 # the end: two zero blocks
        $rec = (10240 - $written % 10240) % 10240 # whole 10 KiB records, as tar writes them
        if ($rec) { & $emit (New-Object byte[] $rec) $rec }
        $gz.Dispose()
    } finally { $fsOut.Dispose() }

    # the read-back with another tar: every entry's mode and owner, nothing else in it, every file's bytes
    # Windows' own bsdtar (System32), not a GNU tar from Git or MSYS on the PATH (that one reads C: as a host name)
    $tarExe = Join-Path $env:SystemRoot 'System32\tar.exe'
    if (-not (Test-Path $tarExe)) { Fail "no $tarExe to read $tarPath back (Windows 10 1803 and later have it)" }
    $listing = @(& $tarExe -tvzf $tarPath --numeric-owner)
    if ($LASTEXITCODE -ne 0) { Fail "tar -tvzf $tarPath failed" }
    $want = @{ 0x1ED = 'rwxr-xr-x'; 0x1A4 = 'rw-r--r--' }
    $seen = @{}
    foreach ($l in $listing) {
        if ($l -notmatch '^([d-])(\S{9})\s+\d+\s+0\s+0\s+\d+\s+.*?\s(\S+)$') { Fail "tar listing: unexpected line '$l' (owner not 0 0?)" }
        $an = $Matches[3]; if ($Matches[1] -eq 'd' -and -not $an.EndsWith('/')) { $an += '/' }
        if (-not $expect.Contains($an)) { Fail "tar listing: $an is not in the folder" }
        if ($Matches[2] -ne $want[$expect[$an]]) { Fail "tar listing: $an has mode $($Matches[2]), not $($want[$expect[$an]])" }
        $seen[$an] = $true
    }
    if ($seen.Count -ne $expect.Count) { Fail "tar listing: $($seen.Count) entries, the folder has $($expect.Count)" }
    $back = Join-Path ([IO.Path]::GetTempPath()) ("make_release_" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory $back | Out-Null
    try {
        & $tarExe -xzf $tarPath -C $back
        if ($LASTEXITCODE -ne 0) { Fail "tar -xzf $tarPath failed" }
        foreach ($f in Get-ChildItem $ldir -Recurse -File) {
            $o = Join-Path $back "$LinuxName\$($f.FullName.Substring($ldir.Length + 1))"
            if (-not (Test-Path -LiteralPath $o) -or (Get-FileHash $o -Algorithm SHA256).Hash -ne (Get-FileHash $f.FullName -Algorithm SHA256).Hash) {
                Fail "tar read-back: $($f.FullName.Substring($ldir.Length + 1)) differs from the folder's"
            }
        }
    } finally { Remove-Item -Recurse -Force $back -ErrorAction SilentlyContinue }
    Write-Host "checked: $LinuxName.tar.gz read back by $(Split-Path $tarExe -Leaf): $($expect.Count) entries, owner 0:0, start-server and the folders 0755, the files 0644, every file's bytes" -ForegroundColor Green
}
Show-Listing $ldir
if (-not $NoZip) { '{0,12:N0}  {1}' -f (Get-Item $tarPath).Length, $tarPath }
Write-Host "release for Linux: $ldir" -ForegroundColor Green
