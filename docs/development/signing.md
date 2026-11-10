# Code signing

The Windows files of a numbered release, `Start-Server.exe`, `Start-Client.exe` and `host\dgsim.dll`, are signed by the maintainer with a Certum *Open Source Code Signing in the cloud* certificate. Its key stays in Certum's cloud (SimplySign) and every use needs a second factor, so the signing runs on the maintainer's PC, between two runs of the build workflow. The policy players see is in the [README](../../README.md#code-signing-policy). This page is the maintainers' side: what the build does, the steps of each release, and what has to be set up once.

## What the build does

1. `re/scripts/make_release.ps1` gives each of the three files a version resource (`server/tools/peversion`): product name `OpenDawngateServer`, product and file version the release's name (such as `v0.7`), a description, the original file name and the copyright. A file's *Properties* show them beside its signature. The script checks all three, in every build, signed or not.
2. With the repository variable `SIGN_RELEASES` set to `true`, a pushed tag `v*` builds and tests the release in `.github/workflows/build.yml` as always, but publishes nothing. It keeps the three files as the artifact `to-sign` and `dgsim.dll` with its build stamp as the artifact `match-host`, for 14 days. Its summary names the next step.
3. The maintainer runs `re/scripts/sign_release.ps1` (below). It downloads `to-sign`, signs the three files, checks them, uploads them to the tag's draft release and starts the second run.
4. The second run is `workflow_dispatch` on the tag, with the input `unsigned_run` set to the first run's id:
   - The job `signed-files` fetches the three signed files from the draft. Reading a draft release takes write access, so this small job has it and the build job doesn't.
   - The build job checks that `unsigned_run` is a successful push run of this workflow on the same commit. It takes that run's `dgsim.dll` and builds the two programs again: Go builds are reproducible, with the same commit, the same toolchain and `-trimpath`. Then it runs `make_release.ps1 -SignedFiles`.
   - `-SignedFiles` checks that each signed file is this build's file with nothing but an Authenticode signature added (`peversion signed`), that Windows finds the signature valid and timestamped, and that one certificate signed all three. `BUILD.txt` then lists the signed files' SHA-256, which `Start-Client.exe`'s update checks use, and names the certificate by its thumbprint and issuer. The Linux archive's `host/dgsim.dll` is the same signed file.
5. The release job puts the zip and the Linux archive into the draft, removes the three loose files, writes the release notes (signed, with the policy's link) and publishes the release.

Without `SIGN_RELEASES`, a tag's push run publishes the release unsigned. Nightly builds and builds from source stay unsigned.

## Signing a release

1. Push `main`, then the tag, and wait until the tag's run is green. Its release job is skipped.
2. Start SimplySign Desktop and connect: tray icon → *Connect to SimplySign*, with the code from the SimplySign phone app. While it is connected, the certificate is in the user's certificate store.
3. In a checkout of the repository: `powershell -ExecutionPolicy Bypass -File re\scripts\sign_release.ps1 -Tag v0.19`. The script finds the tag's run and waits for it while it still runs. It checks that the run succeeded on the tag's commit, then downloads `to-sign` and signs the files with `signtool` (SHA-256, timestamp from `http://time.certum.pl`). It checks every file: the signature is valid, by the certificate, timestamped, and the file is the downloaded one plus the signature, with the release's version. Then it uploads the files to the draft release (made if missing) and starts the second run.
4. Follow the second run (its address is printed). When it is green, the release is published.

If a step fails, the draft stays a draft. Fix the cause and run `sign_release.ps1` again: it replaces the draft's files. A published release is never changed. After 14 days the first run's artifacts are gone; run the tag's build again first.

To try the certificate and the tools without GitHub, `-Files <folder>` signs and checks the three files of a local build (`make_release.ps1`'s output folder) into `re\out\sign\<tag>\signed`.

## Setting it up (once)

1. The certificate: Certum's *Open Source Code Signing in the cloud* (RSA 4096 or 3072). Certum checks the applicant's identity and issues it for one year. The SimplySign app goes on a phone, SimplySign Desktop on the PC that signs.
2. On that PC: `signtool` from the Windows SDK (its *Signing Tools for Desktop Apps*), Go, and `gh` logged in (`gh auth login`) with write access to the repository.
3. In the GitHub repository's *Settings* → *Secrets and variables* → *Actions* → *Variables*: `SIGN_RELEASES` = `true`. No secret is needed: the key never leaves Certum, and nothing that can sign is stored on GitHub.
4. Renew the certificate before it expires. Signatures made before then stay valid through their timestamp.

## Checking a signed release

- In Explorer, a file's *Properties* → *Digital Signatures* shows a valid signature whose certificate comes from *Certum Code Signing 2021 CA*. *Details* shows the product name and version.
- In PowerShell: `Get-AuthenticodeSignature .\Start-Client.exe` says `Valid` and has a `TimeStamperCertificate`, and `(Get-Item .\Start-Client.exe).VersionInfo` shows the version resource.
- `BUILD.txt` has the line `signed   Start-Server.exe, Start-Client.exe, host\dgsim.dll: certificate <thumbprint> (Certum Code Signing 2021 CA)`.
