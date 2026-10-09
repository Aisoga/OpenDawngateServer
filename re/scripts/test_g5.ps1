<#
  Tests G5: the gms\ build check (re\scripts\gms_stale.ps1, used by run_session.ps1 and play.ps1) and the build stamp
  (gms\cmake\stamp.cmake). It works on scratch copies only: it never writes a checkout's gms\ or gms\build, nor the
  installed host copies (client\run_854_server, client\run_854_server_b).

  Part 1 (always, a few seconds): two scratch checkouts, lane a and lane b (A6: each lane checks the build of its own
  checkout), each with a copy of this checkout's gms\ sources and a scratch build folder holding copies of this
  checkout's gms\build\Release\dgsim.dll and gms_script.exe (dummies when they aren't built) and their stamps, written
  by stamp.cmake as the POST_BUILD step does. File times are set explicitly (minutes apart), so no case depends on
  timing. Cases: a fresh build is current; an unrelated header under gms\core is stale before a build and current
  after a build that didn't relink the DLL (the pre-G5 rule still calls that stale: A6's gotcha); a changed
  gms\engine source is stale until a build succeeds (a failed build leaves it stale); a DLL its stamp doesn't name
  is stale; *.lua, gms\core\control\tests and gms\tests aren't inputs; gms\CMakeLists.txt is; gms_script.exe ignores
  gms\host and gms\engine; a build tree without stamps falls back to the binary's own time; the lanes don't affect each other.
  Part 1b: the callers themselves (run_session.ps1's Assert-HostDll and Assert-ScriptExe, play.ps1's
  Get-StaleGmsSource, taken from the scripts' syntax trees, nothing else of them runs) against the scratch checkouts
  and scratch host copies, per lane: lane b's host copy must hold lane b's own build.

  Part 2 (-Build, about a minute with RakNet): the same with real builds. A scratch copy of gms\ is configured with
  CMake (Visual Studio 17 2022, Win32; RakNet from the main checkout's tools\raknet\RakNet unless -NoRakNet, then only
  the harness) and built with cmake --build --target dgsim: a build writes the stamp; an unrelated header under
  gms\core is stale; the next build doesn't relink dgsim.dll but renews the stamp, so the check passes; a touched
  gms\engine source is stale; a build that fails to compile it leaves it stale; the build after the fix relinks and
  passes.

  Usage:
    powershell -ExecutionPolicy Bypass -File re\scripts\test_g5.ps1 [-Build [-NoRakNet]] [-Keep] [-Scratch <empty dir>]
  -Scratch defaults to a new folder under %TEMP% (removed at the end unless -Keep).
  Exit code: 0 all cases passed, 1 a case failed, 2 setup problem (no cmake, a failed configure).
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [switch]$Build,
    [switch]$NoRakNet,
    [switch]$Keep,
    [string]$Scratch
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'gms_stale.ps1')
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$StampCmake = Join-Path $RepoRoot 'gms\cmake\stamp.cmake'

function Find-CMake {
    # as play.ps1: this checkout's configured cmake, Visual Studio's, or the one on PATH
    $cache = Join-Path $RepoRoot 'gms\build\CMakeCache.txt'
    if (Test-Path $cache) {
        $line = @(Get-Content $cache | Where-Object { $_ -match '^CMAKE_COMMAND:INTERNAL=(.+)$' } | Select-Object -First 1)
        if ($line.Count -and $line[0] -match '^CMAKE_COMMAND:INTERNAL=(.+)$' -and (Test-Path $Matches[1])) { return ($Matches[1] -replace '/', '\') }
    }
    $vs = @(Get-ChildItem 'C:\Program Files\Microsoft Visual Studio\2022\*\Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe' -ErrorAction SilentlyContinue)
    if ($vs.Count) { return $vs[0].FullName }
    $onPath = Get-Command cmake.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    $null
}
function Find-MainRoot {
    # the main checkout (it holds tools\), also when this checkout is a git worktree (as run_session.ps1)
    if (Test-Path (Join-Path $RepoRoot 'tools')) { return $RepoRoot }
    try {
        $common = "$(& git -C $RepoRoot rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
        if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'tools')) { return $cand } }
    } catch { }
    $RepoRoot
}

$CMake = Find-CMake
if (-not $CMake) { Write-Host 'no cmake found (Visual Studio 2022 or PATH); stamp.cmake needs it' -ForegroundColor Red; exit 2 }
if (-not $Scratch) { $Scratch = Join-Path $env:TEMP ('dg_g5_' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
if ((Test-Path $Scratch) -and @(Get-ChildItem $Scratch -Force).Count) { Write-Host "-Scratch $Scratch is not empty" -ForegroundColor Red; exit 2 }
New-Item -ItemType Directory -Force $Scratch | Out-Null
$Scratch = (Resolve-Path $Scratch).Path
$scratchLower = $Scratch.ToLower()
if ($scratchLower.StartsWith($RepoRoot.ToLower() + '\') -or $scratchLower -match '\\client\\') { Write-Host "-Scratch must be outside the checkout and client\" -ForegroundColor Red; exit 2 }
Write-Host "G5 test in $Scratch (cmake: $CMake)" -ForegroundColor Cyan

$script:pass = 0; $script:fail = 0; $script:setupFailed = $false
function Check([string]$case, [string]$what, $verdict, [bool]$wantStale, [string]$wantText) {
    $isStale = [bool]$verdict
    $ok = ($isStale -eq $wantStale) -and (-not $wantText -or "$verdict".Contains($wantText))
    $shown = if ($isStale) { "stale: $verdict" } else { 'current' }
    if ($ok) { $script:pass++; Write-Host "  PASS $case $what -> $shown" -ForegroundColor Green }
    else { $script:fail++; Write-Host "  FAIL $case $what -> $shown (wanted $(if ($wantStale) { 'stale' } else { 'current' })$(if ($wantText) { " naming '$wantText'" }))" -ForegroundColor Red }
}
function Assert-True([string]$case, [string]$what, [bool]$cond) {
    if ($cond) { $script:pass++; Write-Host "  PASS $case $what" -ForegroundColor Green }
    else { $script:fail++; Write-Host "  FAIL $case $what" -ForegroundColor Red }
}
function Get-PreG5Stale([string]$binary, [string]$gmsDir) {
    # the rule before G5 (run_session.ps1's Assert-HostDll up to A6): any file in harness (since G4 host, engine), core, script newer than the DLL
    $t = (Get-Item -LiteralPath $binary).LastWriteTimeUtc
    $newer = @(Get-ChildItem (Join-Path $gmsDir 'host'), (Join-Path $gmsDir 'engine'), (Join-Path $gmsDir 'core'), (Join-Path $gmsDir 'script') -Recurse -File |
        Where-Object { $_.LastWriteTimeUtc -gt $t -and $_.Extension -ne '.lua' -and $_.FullName -notlike '*\gms\core\control\tests\*' })
    if ($newer.Count) { "older than $($newer[0].Name)" } else { $null }
}
function Get-FunctionText([string]$file, [string[]]$names) {
    # the source text of the named functions of a script (parsed, not run)
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($file, [ref]$null, [ref]$null)
    foreach ($n in $names) {
        $fn = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq $n }, $true))
        if (-not $fn.Count) { throw "no function $n in $file" }
        $fn[0].Extent.Text
    }
}
function Copy-GmsSources([string]$checkout) {
    $dst = Join-Path $checkout 'gms'
    New-Item -ItemType Directory -Force $dst | Out-Null
    Get-ChildItem (Join-Path $RepoRoot 'gms') -Force | Where-Object { $_.Name -ne 'build' } |
        ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $dst -Recurse -Force }
    $dst
}
function Set-Time([string]$path, [datetime]$utc) { (Get-Item -LiteralPath $path).LastWriteTimeUtc = $utc }
function Invoke-CMake([string]$log, [string[]]$cmakeArgs) {
    # cmake's exit code, its output in $log (Windows PowerShell 5.1 turns a native program's stderr into errors, which
    # 'Stop' would throw: CMake prints its warnings there)
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $CMake @cmakeArgs *> $log } finally { $ErrorActionPreference = $old }
    $LASTEXITCODE
}
function Write-Stamp([string]$binary) {
    # what the POST_BUILD step runs after a successful build of the target
    $log = Join-Path $Scratch 'stamp.log'
    $code = Invoke-CMake $log @("-DBINARY=$binary", '-P', $StampCmake)
    if ($code -ne 0) { throw "stamp.cmake failed for ${binary}: $(Get-Content $log -Raw)" }
}

try {
    # ================================ part 1: the check on scratch copies of the build outputs ======================
    Write-Host 'part 1: the check (gms_stale.ps1) on scratch checkouts with copies of the build outputs' -ForegroundColor Cyan
    $base = [datetime]::UtcNow.AddHours(-3)
    $base = $base.AddTicks(-($base.Ticks % [TimeSpan]::TicksPerSecond))
    function M([double]$n) { $base.AddMinutes($n) }
    $lanes = @{}
    foreach ($lane in @('a', 'b')) {
        $co = Join-Path $Scratch "lane_$lane"
        $gms = Copy-GmsSources $co
        Get-ChildItem $gms -Recurse -File | ForEach-Object { $_.LastWriteTimeUtc = (M 0) }
        $rel = Join-Path $gms 'build\Release'
        New-Item -ItemType Directory -Force $rel | Out-Null
        foreach ($bin in @('dgsim.dll', 'gms_script.exe')) {
            $src = Join-Path $RepoRoot "gms\build\Release\$bin"
            $dst = Join-Path $rel $bin
            if (Test-Path $src) { Copy-Item -LiteralPath $src -Destination $dst } else { [IO.File]::WriteAllText($dst, "dummy $bin for test_g5") }
            Set-Time $dst (M 10)
            Write-Stamp $dst; Set-Time "$dst.stamp" (M 10.02)
        }
        $lanes[$lane] = @{ Gms = $gms; Dll = (Join-Path $rel 'dgsim.dll'); Exe = (Join-Path $rel 'gms_script.exe') }
    }
    $A = $lanes['a']; $B = $lanes['b']
    $hook = Join-Path $A.Gms 'engine\hook.cpp'
    if (-not (Test-Path $hook)) { throw "no gms\engine\hook.cpp in the copy" }

    Check 'a1' 'fresh build: dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''
    Check 'a1' 'fresh build: gms_script.exe' (Get-GmsBuildStale $A.Exe $A.Gms) $false ''

    $hdr = Join-Path $A.Gms 'core\g5_unrelated\unrelated.h'
    New-Item -ItemType Directory -Force (Split-Path $hdr) | Out-Null
    [IO.File]::WriteAllText($hdr, "#pragma once`r`n// G5 test: a header nothing includes`r`n")
    Set-Time $hdr (M 20)
    Check 'a2' 'unrelated header under gms\core, no build yet' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'core\g5_unrelated\unrelated.h'

    # a build after it: MSBuild doesn't relink dgsim.dll (unchanged, still M 10), the POST_BUILD step renews the stamp
    foreach ($bin in @($A.Dll, $A.Exe)) { Write-Stamp $bin; Set-Time "$bin.stamp" (M 21) }
    Check 'a3' 'unrelated header after a build that did not relink dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''
    Check 'a3' '(the pre-G5 rule on the same files: the A6 gotcha)' (Get-PreG5Stale $A.Dll $A.Gms) $true 'unrelated.h'

    Set-Time $hook (M 30)
    Check 'a4' 'changed gms\engine source, no build yet: dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'engine\hook.cpp'
    Check 'a4' 'changed gms\engine source: gms_script.exe (not its input)' (Get-GmsBuildStale $A.Exe $A.Gms) $false ''
    # a failed build (compile error) writes nothing: still stale
    Check 'a5' 'after a failed build' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'engine\hook.cpp'
    # the link wrote a new DLL but the build didn't finish (no stamp for these bytes)
    [IO.File]::AppendAllText($A.Dll, 'relinked'); Set-Time $A.Dll (M 31)
    Check 'a6' 'DLL newer than every source but not the one its stamp names' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'not the binary its build stamp names'
    Write-Stamp $A.Dll; Set-Time "$($A.Dll).stamp" (M 32)
    Check 'a7' 'after the successful rebuild' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''
    # G4: dgsim's sources are in two folders, host\ (the DLL shell) and engine\
    Set-Time (Join-Path $A.Gms 'host\host.cpp') (M 33)
    Check 'a7' 'changed gms\host source: dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'host\host.cpp'
    Write-Stamp $A.Dll; Set-Time "$($A.Dll).stamp" (M 34)
    Check 'a7' 'after its build' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''

    $lua = @(Get-ChildItem (Join-Path $A.Gms 'engine\lua') -Filter '*.lua' -File | Select-Object -First 1)
    $ctl = @(Get-ChildItem (Join-Path $A.Gms 'core\control\tests') -File -ErrorAction SilentlyContinue | Select-Object -First 1)
    $tst = @(Get-ChildItem (Join-Path $A.Gms 'tests') -File | Select-Object -First 1)
    foreach ($f in @($lua + $ctl + $tst)) { Set-Time $f.FullName (M 40) }
    Check 'a8' "not inputs changed ($(@($lua + $ctl + $tst | ForEach-Object { $_.Name }) -join ', '))" (Get-GmsBuildStale $A.Dll $A.Gms) $false ''

    Set-Time (Join-Path $A.Gms 'CMakeLists.txt') (M 41)
    Check 'a9' 'gms\CMakeLists.txt changed: dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'CMakeLists.txt'
    Check 'a9' 'gms\CMakeLists.txt changed: gms_script.exe' (Get-GmsBuildStale $A.Exe $A.Gms) $true 'CMakeLists.txt'
    foreach ($bin in @($A.Dll, $A.Exe)) { Write-Stamp $bin; Set-Time "$bin.stamp" (M 42) }
    Check 'a9' 'after the build' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''

    $core = @(Get-ChildItem (Join-Path $A.Gms 'core\session') -Filter '*.cpp' -File | Select-Object -First 1)[0].FullName
    Set-Time $core (M 50)
    Check 'a10' 'changed gms\core source: gms_script.exe' (Get-GmsBuildStale $A.Exe $A.Gms) $true 'core\session\'
    Check 'a10' 'changed gms\core source: dgsim.dll' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'core\session\'
    foreach ($bin in @($A.Dll, $A.Exe)) { Write-Stamp $bin; Set-Time "$bin.stamp" (M 51) }

    Remove-Item -LiteralPath "$($A.Dll).stamp"
    Check 'a11' 'no stamp (a build tree from before G5): the DLL time rule' (Get-GmsBuildStale $A.Dll $A.Gms) $true 'no build stamp yet'
    Set-Time $A.Dll (M 52)
    Check 'a11' 'no stamp, DLL newer than every source' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''
    Write-Stamp $A.Dll; Set-Time "$($A.Dll).stamp" (M 53)

    # A6: lane b's checkout (a worktree) has its own gms\build; lane a's changes and builds don't touch its verdict
    Check 'b1' "lane b after lane a's changes and builds" (Get-GmsBuildStale $B.Dll $B.Gms) $false ''
    Set-Time (Join-Path $B.Gms 'engine\hook.cpp') (M 60)
    Check 'b2' 'lane b: changed gms\engine source' (Get-GmsBuildStale $B.Dll $B.Gms) $true 'engine\hook.cpp'
    Check 'b2' 'lane a meanwhile' (Get-GmsBuildStale $A.Dll $A.Gms) $false ''
    Write-Stamp $B.Dll; Set-Time "$($B.Dll).stamp" (M 61)
    Check 'b3' 'lane b after its build' (Get-GmsBuildStale $B.Dll $B.Gms) $false ''

    # the callers themselves: run_session.ps1's Assert-HostDll/Assert-ScriptExe and play.ps1's Get-StaleGmsSource, taken
    # from the scripts' syntax trees and run against the scratch checkouts and scratch host copies (a dummy
    # PhysXLoader_orig.dll beside a copy of the lane's dgsim.dll as PhysXLoader.dll)
    Write-Host 'part 1b: the callers (run_session.ps1, play.ps1) on the scratch checkouts and scratch host copies' -ForegroundColor Cyan
    foreach ($text in @(Get-FunctionText (Join-Path $PSScriptRoot 'run_session.ps1') @('Assert-HostDll', 'Assert-ScriptExe')) +
                      @(Get-FunctionText (Join-Path $PSScriptRoot 'play.ps1') @('Get-StaleGmsSource'))) {
        . ([scriptblock]::Create($text))
    }
    foreach ($lane in @('a', 'b')) {
        $co = Join-Path $Scratch "lane_$lane"
        $hc = Join-Path $co "client\run_854_server$(if ($lane -ne 'a') { "_$lane" })\Dawngate"
        New-Item -ItemType Directory -Force (Join-Path $hc 'game') | Out-Null
        [IO.File]::WriteAllText((Join-Path $hc 'game\PhysXLoader_orig.dll'), 'dummy PhysXLoader for test_g5')
        Copy-Item -LiteralPath $lanes[$lane].Dll -Destination (Join-Path $hc 'game\PhysXLoader.dll') -Force
        $lanes[$lane].Root = $co; $lanes[$lane].HostCopy = $hc
    }
    function Invoke-Caller([string]$lane, [scriptblock]$call) {
        # the callers read $RepoRoot, $Lane, $LaneCfg, $ScriptExe, $SimDll from the calling scope; '' = no error
        $RepoRoot = $lanes[$lane].Root; $Lane = $lane; $LaneCfg = @{ Client = "client\run_854$(if ($lane -ne 'a') { "_$lane" })\Dawngate" }
        $ScriptExe = $lanes[$lane].Exe; $SimDll = $lanes[$lane].Dll
        try { $r = & $call; if ($r) { "returned: $r" } else { '' } } catch { "threw: $($_.Exception.Message)" }
    }
    Check 'r1' 'Assert-HostDll, lane a, current build' (Invoke-Caller 'a' { Assert-HostDll $lanes['a'].HostCopy }) $false ''
    $hdr2 = Join-Path $A.Gms 'core\g5_unrelated\another.h'
    [IO.File]::WriteAllText($hdr2, "#pragma once`r`n"); Set-Time $hdr2 (M 70)
    foreach ($bin in @($A.Dll, $A.Exe)) { Write-Stamp $bin; Set-Time "$bin.stamp" (M 71) }
    Check 'r2' 'Assert-HostDll, lane a, a header added and built without a relink' (Invoke-Caller 'a' { Assert-HostDll $lanes['a'].HostCopy }) $false ''
    Check 'r2' "play.ps1's Get-StaleGmsSource, the same" (Invoke-Caller 'a' { Get-StaleGmsSource }) $false ''
    Set-Time $hook (M 80)
    Check 'r3' 'Assert-HostDll, lane a, changed gms\engine source' (Invoke-Caller 'a' { Assert-HostDll $lanes['a'].HostCopy }) $true 'engine\hook.cpp'
    Check 'r3' "play.ps1's Get-StaleGmsSource, the same" (Invoke-Caller 'a' { Get-StaleGmsSource }) $true 'engine\hook.cpp'
    Check 'r3' 'Assert-ScriptExe, the same (not its input)' (Invoke-Caller 'a' { Assert-ScriptExe }) $false ''
    Check 'r4' 'Assert-HostDll, lane b, its own build and host copy' (Invoke-Caller 'b' { Assert-HostDll $lanes['b'].HostCopy }) $false ''
    Copy-Item -LiteralPath $A.Dll -Destination (Join-Path $lanes['b'].HostCopy 'game\PhysXLoader.dll') -Force
    Check 'r4' "Assert-HostDll, lane b's host copy holding lane a's DLL" (Invoke-Caller 'b' { Assert-HostDll $lanes['b'].HostCopy }) $true 'is not the current'

    # ================================ part 2: real builds (-Build) ==================================================
    if ($Build) {
        Write-Host 'part 2: real builds of a scratch copy of gms\ (cmake --build --target dgsim)' -ForegroundColor Cyan
        $co = Join-Path $Scratch 'build_test'
        $gms = Copy-GmsSources $co
        $bdir = Join-Path $gms 'build'
        $raknet = Join-Path (Find-MainRoot) 'tools\raknet\RakNet'
        if ($NoRakNet -or -not (Test-Path (Join-Path $raknet 'Source\RakNetVersion.h'))) {
            $raknet = Join-Path $Scratch 'no_raknet'; Write-Host "  (no RakNet: only the harness is built)" -ForegroundColor Gray
        }
        $cfgLog = Join-Path $Scratch 'configure.log'
        $code = Invoke-CMake $cfgLog @('-S', $gms, '-B', $bdir, '-G', 'Visual Studio 17 2022', '-A', 'Win32', "-DRAKNET_ROOT=$($raknet -replace '\\', '/')")
        if ($code -ne 0) {
            Get-Content $cfgLog -Tail 15 | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
            $script:setupFailed = $true
            throw "configure failed (exit $code)"
        }
        $script:buildNo = 0
        function Invoke-Build {
            $script:buildNo++
            Invoke-CMake (Join-Path $Scratch "build$($script:buildNo).log") @('--build', $bdir, '--config', 'Release', '--target', 'dgsim')
        }
        $dll = Join-Path $bdir 'Release\dgsim.dll'
        $stamp = "$dll.stamp"

        $code = Invoke-Build
        Assert-True 'p1' "first build succeeds (exit $code) and writes dgsim.dll.stamp" ($code -eq 0 -and (Test-Path $stamp))
        Check 'p1' 'after the first build' (Get-GmsBuildStale $dll $gms) $false ''
        $dllTime = (Get-Item $dll).LastWriteTimeUtc

        Start-Sleep -Milliseconds 1100
        $hdr = Join-Path $gms 'core\g5_unrelated\unrelated.h'
        New-Item -ItemType Directory -Force (Split-Path $hdr) | Out-Null
        [IO.File]::WriteAllText($hdr, "#pragma once`r`n// G5 test: a header nothing includes`r`n")
        Check 'p2' 'unrelated header under gms\core, no build yet' (Get-GmsBuildStale $dll $gms) $true 'core\g5_unrelated\unrelated.h'

        Start-Sleep -Milliseconds 1100
        $code = Invoke-Build
        $relinked = (Get-Item $dll).LastWriteTimeUtc -ne $dllTime
        Assert-True 'p3' "build after the header succeeds (exit $code) without relinking dgsim.dll (relinked: $relinked)" ($code -eq 0 -and -not $relinked)
        Assert-True 'p3' 'the stamp is newer than the header' ((Get-Item $stamp).LastWriteTimeUtc -gt (Get-Item $hdr).LastWriteTimeUtc)
        Check 'p3' 'unrelated header after the build' (Get-GmsBuildStale $dll $gms) $false ''
        Check 'p3' '(the pre-G5 rule: the A6 gotcha)' (Get-PreG5Stale $dll $gms) $true 'unrelated.h'

        Start-Sleep -Milliseconds 1100
        $hook = Join-Path $gms 'engine\hook.cpp'
        Set-Time $hook ([datetime]::UtcNow)
        Check 'p4' 'touched gms\engine\hook.cpp, no build yet' (Get-GmsBuildStale $dll $gms) $true 'engine\hook.cpp'

        $orig = [IO.File]::ReadAllText($hook)
        $stampBefore = (Get-Item $stamp).LastWriteTimeUtc
        [IO.File]::WriteAllText($hook, $orig + "`r`n#error G5 test: a compile error`r`n")
        $code = Invoke-Build
        Assert-True 'p5' "build with a compile error in hook.cpp fails (exit $code) and writes no stamp" ($code -ne 0 -and (Get-Item $stamp).LastWriteTimeUtc -eq $stampBefore)
        Check 'p5' 'after the failed build' (Get-GmsBuildStale $dll $gms) $true 'engine\hook.cpp'

        [IO.File]::WriteAllText($hook, $orig)
        $code = Invoke-Build
        $relinked = (Get-Item $dll).LastWriteTimeUtc -ne $dllTime
        Assert-True 'p6' "build after the fix succeeds (exit $code) and relinks dgsim.dll (relinked: $relinked)" ($code -eq 0 -and $relinked)
        Check 'p6' 'after the fixed build' (Get-GmsBuildStale $dll $gms) $false ''
    }
} catch {
    $script:fail++
    Write-Host "  FAIL setup: $($_.Exception.Message)" -ForegroundColor Red
} finally {
    if (-not $Keep) { Remove-Item -LiteralPath $Scratch -Recurse -Force -ErrorAction SilentlyContinue }
    else { Write-Host "kept $Scratch" -ForegroundColor Gray }
}
Write-Host "G5: $($script:pass) passed, $($script:fail) failed" -ForegroundColor $(if ($script:fail) { 'Red' } else { 'Green' })
exit $(if ($script:setupFailed) { 2 } elseif ($script:fail) { 1 } else { 0 })
