<#
  Is a gms/ build current? (roadmap G5) The staleness rule of run_session.ps1 (Assert-HostDll, Assert-ScriptExe) and
  play.ps1 (Get-StaleGmsSource), kept in one place; re\scripts\test_g5.ps1 tests it.

  Dot-source it, then:
    Get-GmsBuildStale -Binary <checkout>\gms\build\Release\dgsim.dll -GmsDir <checkout>\gms [-Inputs host,engine,core,script]
  It returns $null when the binary is current, else a reason, e.g. "dgsim.dll is older than its sources
  (engine\hook.cpp and 2 more newer than its last successful build, 2026-10-08 18:38:29)".

  The rule: no file in the binary's source folders, gms\CMakeLists.txt or gms\cmake is newer than the binary's last
  successful build.
  - That time is the build stamp's: <binary>.stamp, which gms\cmake\stamp.cmake writes after every successful build of
    the target, also when MSBuild didn't relink an unchanged binary. Before G5 the rule used the binary's own time, so
    a header nothing includes (D9-core's core\wells\wells.h) kept dgsim.dll "older than its sources" after any number
    of builds (A6's gotcha). A source that really changed is still newer than the stamp until a build succeeds: a
    failed compile or link writes no stamp.
  - The stamp holds the binary's SHA-256; a stamp for other bytes (a link that wrote the binary but whose build didn't
    finish, a binary copied in by hand) makes the binary stale.
  - Without a stamp (a build tree configured before G5, not built since) the binary's own time is used, the old rule;
    the next build writes the stamp (CMake reconfigures itself because gms\CMakeLists.txt changed).
  - The source folders per binary are $GmsBinaryInputs below; not inputs: *.lua (gms\engine\lua: setup_server_copy.py
    copies them into the host copy) and gms\core\control\tests (dg_control_tests only).
  - Per test lane (A6): the caller passes its own checkout (run_session.ps1: the checkout it runs from, $RepoRoot), so
    lane a's main checkout and lane b's worktree each check their own gms\build and stamp.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>

# the gms\ folders each binary is built from (gms\CMakeLists.txt): dgsim links gms_core, gms_scripted_match (script\)
# and dg_control (core\control); gms_script links gms_core and dg_control
$GmsBinaryInputs = @{
    'dgsim.dll'      = @('host', 'engine', 'core', 'script')
    'gms_script.exe' = @('core', 'script')
}

function Get-GmsBuildTime([string]$Binary) {
    # the time of the binary's last successful build: @{ Time (UTC); What (for messages); Error (a stamp mismatch) }
    $stamp = "$Binary.stamp"
    if (-not (Test-Path -LiteralPath $stamp -PathType Leaf)) {
        $t = (Get-Item -LiteralPath $Binary).LastWriteTimeUtc
        return @{ Time = $t; What = "the binary itself, $($t.ToLocalTime().ToString('yyyy-MM-dd HH:mm:ss')) (no build stamp yet)"; Error = $null }
    }
    $text = [IO.File]::ReadAllText($stamp)
    $hash = (Get-FileHash -LiteralPath $Binary -Algorithm SHA256).Hash
    if ($text -notmatch "(?m)^sha256=$hash\s*$") {
        return @{ Time = $null; What = ''; Error = "$(Split-Path $Binary -Leaf) is not the binary its build stamp names ($(Split-Path $stamp -Leaf)): its last build didn't finish" }
    }
    $t = (Get-Item -LiteralPath $stamp).LastWriteTimeUtc
    @{ Time = $t; What = "its last successful build, $($t.ToLocalTime().ToString('yyyy-MM-dd HH:mm:ss'))"; Error = $null }
}

function Get-GmsBuildStale {
    param(
        [Parameter(Mandatory = $true)][string]$Binary,
        [Parameter(Mandatory = $true)][string]$GmsDir,
        [string[]]$Inputs
    )
    $name = Split-Path $Binary -Leaf
    if (-not $Inputs) { $Inputs = $GmsBinaryInputs[$name] }
    if (-not $Inputs) { throw "Get-GmsBuildStale: no source folders known for $name (pass -Inputs)" }
    if (-not (Test-Path -LiteralPath $Binary -PathType Leaf)) { return "$name is not built" }
    $built = Get-GmsBuildTime $Binary
    if ($built.Error) { return $built.Error }
    $gms = (Resolve-Path -LiteralPath $GmsDir).Path.TrimEnd('\')
    $roots = @(@($Inputs) + @('CMakeLists.txt', 'cmake') | ForEach-Object { Join-Path $gms $_ } | Where-Object { Test-Path -LiteralPath $_ })
    $skip = Join-Path $gms 'core\control\tests\'
    $newer = @(Get-ChildItem -LiteralPath $roots -Recurse -File |
        Where-Object { $_.LastWriteTimeUtc -gt $built.Time -and $_.Extension -ne '.lua' -and -not $_.FullName.StartsWith($skip, [StringComparison]::OrdinalIgnoreCase) } |
        Sort-Object LastWriteTimeUtc -Descending)
    if (-not $newer.Count) { return $null }
    $first = $newer[0].FullName.Substring($gms.Length + 1)
    $more = if ($newer.Count -gt 1) { " and $($newer.Count - 1) more" } else { '' }
    "$name is older than its sources ($first$more newer than $($built.What))"
}
