<#
.SYNOPSIS
  Signs a numbered release's Windows programs on the maintainer's PC with the Certum code signing certificate
  (SimplySign) and hands them back to the build workflow, which packs and publishes the release.

.DESCRIPTION
  With the repository variable SIGN_RELEASES = true, the push run of a version tag (.github/workflows/build.yml)
  builds and tests the release but does not publish it: it keeps Start-Server.exe, Start-Client.exe and
  host\dgsim.dll as its artifact to-sign (and dgsim.dll with its build stamp as match-host). This script:
  1. finds that run (the tag's newest push run of build.yml), waits while it runs, and checks that it succeeded on
     the tag's commit;
  2. downloads to-sign (gh run download) into <Work>\unsigned;
  3. signs copies of the three in <Work>\signed with signtool: SHA-256, an RFC 3161 timestamp (-TimestampUrl), the
     certificate -Thumbprint or else the one valid Certum code signing certificate with its key in the user's store
     (SimplySign Desktop must be connected: it puts the cloud certificate there);
  4. checks each signed file: Windows finds the signature valid, by that certificate, timestamped, and
     server\tools\peversion finds it the downloaded file with nothing but a signature added, with the release's
     version resource;
  5. uploads the three to the tag's draft release (made here; a draft from an earlier try gets them replaced; a
     published release is refused);
  6. starts the workflow's second run on the tag (gh workflow run build.yml --ref <tag> -f unsigned_run=<run id>).
     That run takes the first run's dgsim.dll, builds the programs again, checks that each signed file is its own
     build's file plus a valid signature (make_release.ps1 -SignedFiles), packs the archives, puts them into the
     draft, removes the three loose files and publishes the release.
  -Files <folder>: only steps 3 and 4, on the three files found in <folder> (such as a local make_release.ps1
  build), without GitHub: to try the certificate and the tools. -NoDispatch: steps 1 to 5, not 6.
  Needs gh (logged in, with write access to -Repo), Go (peversion) and signtool (the Windows SDK). Writes only
  under <Work> (default re\out\sign\<Tag>, gitignored).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File re\scripts\sign_release.ps1 -Tag v0.19
  powershell -ExecutionPolicy Bypass -File re\scripts\sign_release.ps1 -Tag v0.19 -Files re\out\release\v0.19
#>
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$Repo = 'Aisoga/OpenDawngateServer',
    [string]$Thumbprint,
    [string]$Files,
    [string]$Work,
    [string]$TimestampUrl = 'http://time.certum.pl',
    [switch]$NoDispatch
)
$ErrorActionPreference = 'Stop'
function Fail([string]$m) { Write-Host $m -ForegroundColor Red; exit 1 }
function Step([string]$m) { Write-Host $m -ForegroundColor Cyan }
# a native command whose stderr is dropped (Windows PowerShell turns redirected stderr into errors, which 'Stop'
# makes fatal); its output, $LASTEXITCODE as it left it
function Quiet([scriptblock]$b) { $old = $ErrorActionPreference; $ErrorActionPreference = 'Continue'; try { & $b 2>$null } finally { $ErrorActionPreference = $old } }
# gh's --json output as a list of objects (Windows PowerShell's ConvertFrom-Json hands an array on as one object).
# The comma keeps the list a list: a function's output is unrolled, and a single object in Windows PowerShell has no
# .Count (v0.19's first try: its one run counted as none).
function FromJson($lines) { $o = ($lines -join "`n") | ConvertFrom-Json; return , @($o | Where-Object { $_ }) }
if ($Tag -cnotmatch '^v[0-9]+\.[0-9]+(\.[0-9]+)?$') { Fail "-Tag $Tag`: a release tag is v<major>.<minor> or v<major>.<minor>.<patch>" }
$Root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if (-not $Work) { $Work = Join-Path $Root "re\out\sign\$Tag" }
$Targets = @('Start-Server.exe', 'Start-Client.exe', 'dgsim.dll') # host\dgsim.dll in the release

# the tools
$signtool = (Get-Command signtool.exe -ErrorAction SilentlyContinue).Source
if (-not $signtool) {
    $signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
        Sort-Object { [version]$_.Directory.Parent.Name } -Descending | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $signtool) { Fail 'signtool.exe not found: install the Windows SDK (its "Windows SDK Signing Tools for Desktop Apps")' }
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go -and (Test-Path 'C:\Program Files\Go\bin\go.exe')) { $go = 'C:\Program Files\Go\bin\go.exe' }
if (-not $go) { Fail 'Go is needed for the check of the signed files (server\tools\peversion)' }
if (-not $Files) {
    # gh on the PATH, else the main checkout's tools\gh (also from a worktree)
    $gh = (Get-Command gh -ErrorAction SilentlyContinue).Source
    if (-not $gh) {
        $common = "$(Quiet { & git -C $Root rev-parse --path-format=absolute --git-common-dir })".Trim()
        foreach ($r in @($Root) + @(if ($common) { Split-Path ($common -replace '/', '\') -Parent })) {
            $cand = Join-Path $r 'tools\gh\bin\gh.exe'
            if (-not $gh -and (Test-Path $cand)) { $gh = $cand }
        }
    }
    if (-not $gh) { Fail 'gh (GitHub CLI) is needed: https://cli.github.com (or tools\gh\bin\gh.exe), then gh auth login' }
    Quiet { & $gh auth status --hostname github.com } | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail 'gh is not logged in to github.com: gh auth login' }
}

# the certificate (SimplySign Desktop connected: the cloud certificate is in CurrentUser\My, its key on Certum's side)
$now = Get-Date
if ($Thumbprint) {
    $cert = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object { $_.Thumbprint -eq $Thumbprint.ToUpper() }
    if (-not $cert) { Fail "no code signing certificate $Thumbprint in the user's store (is SimplySign Desktop connected?)" }
} else {
    $certs = @(Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object {
            $_.HasPrivateKey -and $_.Issuer -match 'CN=Certum Code Signing' -and $_.NotBefore -le $now -and $_.NotAfter -gt $now })
    if ($certs.Count -eq 0) { Fail 'no valid Certum code signing certificate in the user''s store: connect SimplySign Desktop (tray icon: Connect to SimplySign), or pass -Thumbprint' }
    if ($certs.Count -gt 1) { Fail "$($certs.Count) valid Certum code signing certificates: pass -Thumbprint ($(($certs | ForEach-Object { $_.Thumbprint }) -join ', '))" }
    $cert = $certs[0]
}
if (-not $cert.HasPrivateKey) { Fail "certificate $($cert.Thumbprint) has no private key here (SimplySign Desktop not connected?)" }
if ($cert.NotAfter -le $now) { Fail "certificate $($cert.Thumbprint) expired on $($cert.NotAfter)" }
$days = [int]($cert.NotAfter - $now).TotalDays
Write-Host "certificate $($cert.Thumbprint) from $($cert.GetNameInfo('SimpleName', $true)), valid for $days more days" -ForegroundColor Green
if ($days -lt 30) { Write-Host "  renew it soon (Certum account): it expires on $($cert.NotAfter.ToString('yyyy-MM-dd'))" -ForegroundColor Yellow }

foreach ($d in @('unsigned', 'signed')) { $p = Join-Path $Work $d; if (Test-Path $p) { Remove-Item -Recurse -Force $p }; New-Item -ItemType Directory -Force $p | Out-Null }
$Work = (Resolve-Path $Work).Path # absolute: go build -C and the & operator take it as given
$Unsigned = Join-Path $Work 'unsigned'
$Signed = Join-Path $Work 'signed'

# 1-2. the tag's push run and its files (or the local folder)
$runId = $null
if ($Files) {
    if (-not (Test-Path $Files)) { Fail "-Files $Files`: no such folder" }
    foreach ($leaf in $Targets) {
        $found = @(Get-ChildItem $Files -Recurse -File -Filter $leaf)
        if ($found.Count -ne 1) { Fail "-Files: $($found.Count) files named $leaf in $Files, not one" }
        Copy-Item $found[0].FullName (Join-Path $Unsigned $leaf)
    }
} else {
    Step "the push run of $Tag in $Repo ..."
    $tagSha = "$(Quiet { & $gh api "repos/$Repo/commits/$Tag" --jq .sha })".Trim()
    if ($LASTEXITCODE -ne 0 -or -not $tagSha) { Fail "tag $Tag not found in $Repo (push the tag first)" }
    $runs = & $gh run list --repo $Repo --workflow build.yml --event push --branch $Tag --limit 1 --json 'databaseId,status,conclusion,headSha,url'
    if ($LASTEXITCODE -ne 0) { Fail 'gh run list failed' }
    $run = FromJson $runs
    if (-not $run.Count) { Fail "no push run of build.yml for $Tag yet (it starts when the tag is pushed)" }
    $run = $run[0]
    $runId = $run.databaseId
    if ($run.headSha -ne $tagSha) { Fail "run $runId built $($run.headSha), the tag is $tagSha" }
    if ($run.status -ne 'completed') {
        Write-Host "run $runId is $($run.status), waiting: $($run.url)"
        & $gh run watch $runId --repo $Repo --interval 30 --exit-status | Out-Null
        $run = (FromJson (& $gh run view $runId --repo $Repo --json 'status,conclusion'))[0]
    }
    if ($run.conclusion -ne 'success') { Fail "run $runId ended with '$($run.conclusion)': fix it and run the tag's build again first" }
    Step "downloading the artifact to-sign of run $runId ..."
    & $gh run download $runId --repo $Repo --name to-sign --dir $Unsigned
    if ($LASTEXITCODE -ne 0) { Fail "run $runId has no artifact to-sign (SIGN_RELEASES not set on the repository when it ran, or kept past its 14 days: re-run the tag's build)" }
    foreach ($leaf in $Targets) { if (-not (Test-Path (Join-Path $Unsigned $leaf))) { Fail "to-sign lacks $leaf" } }
}

# 3. sign
foreach ($leaf in $Targets) { Copy-Item (Join-Path $Unsigned $leaf) (Join-Path $Signed $leaf) }
$paths = @($Targets | ForEach-Object { Join-Path $Signed $_ })
Step "signing $($Targets -join ', ') ..."
& $signtool sign /sha1 $cert.Thumbprint /fd sha256 /tr $TimestampUrl /td sha256 @paths
if ($LASTEXITCODE -ne 0) { Fail "signtool sign failed (exit $LASTEXITCODE): is SimplySign Desktop still connected?" }

# 4. check
$peversion = Join-Path $Work "peversion-$PID.exe"
& $go build -C (Join-Path $Root 'server') -o $peversion ./tools/peversion
if ($LASTEXITCODE -ne 0) { Fail 'go build ./tools/peversion failed' }
try {
    foreach ($leaf in $Targets) {
        $s = Join-Path $Signed $leaf
        $sig = Get-AuthenticodeSignature $s
        if ($sig.Status -ne 'Valid') { Fail "$leaf`: the signature is $($sig.Status) ($($sig.StatusMessage))" }
        if ($sig.SignerCertificate.Thumbprint -ne $cert.Thumbprint) { Fail "$leaf`: signed by $($sig.SignerCertificate.Thumbprint), not $($cert.Thumbprint)" }
        if (-not $sig.TimeStamperCertificate) { Fail "$leaf`: no timestamp (the signature would end with the certificate)" }
        & $peversion signed -version $Tag (Join-Path $Unsigned $leaf) $s
        if ($LASTEXITCODE -ne 0) { Fail "$leaf`: not the built file with only a signature added, or not version $Tag" }
    }
} finally { Remove-Item $peversion -ErrorAction SilentlyContinue }
Write-Host "signed and checked: $Signed" -ForegroundColor Green
if ($Files) { exit 0 }

# 5. the draft release with the signed files
Step "the draft release $Tag ..."
$view = Quiet { & $gh release view $Tag --repo $Repo --json 'isDraft' }
if ($LASTEXITCODE -eq 0) {
    if (-not (FromJson $view)[0].isDraft) { Fail "release $Tag is already published: a published release is not changed" }
    & $gh release upload $Tag --repo $Repo --clobber @paths
    if ($LASTEXITCODE -ne 0) { Fail 'gh release upload failed' }
} else {
    & $gh release create $Tag --repo $Repo --draft --verify-tag --title "OpenDawngateServer $Tag" `
        --notes 'Being signed: the build workflow puts the archives here and publishes this release.' @paths
    if ($LASTEXITCODE -ne 0) { Fail 'gh release create --draft failed' }
}
if ($NoDispatch) { Write-Host "the signed files are in the draft $Tag; the second run not started (-NoDispatch)" -ForegroundColor Yellow; exit 0 }

# 6. the second run
Step "starting the signed build of $Tag ..."
$before = Get-Date
& $gh workflow run build.yml --repo $Repo --ref $Tag -f "unsigned_run=$runId"
if ($LASTEXITCODE -ne 0) { Fail 'gh workflow run failed' }
$url = $null
for ($i = 0; $i -lt 10 -and -not $url; $i++) {
    Start-Sleep -Seconds 3
    $r = FromJson (& $gh run list --repo $Repo --workflow build.yml --event workflow_dispatch --branch $Tag --limit 1 --json 'url,createdAt')
    if ($r.Count -and ([datetime]$r[0].createdAt).ToUniversalTime() -ge $before.ToUniversalTime().AddSeconds(-30)) { $url = $r[0].url }
}
if ($url) { Write-Host "the signed build runs: $url (gh run watch to follow it); it publishes $Tag when it passes" -ForegroundColor Green }
else { Write-Host "the signed build was started; find it under the repository's Actions (build, workflow_dispatch on $Tag)" -ForegroundColor Yellow }
