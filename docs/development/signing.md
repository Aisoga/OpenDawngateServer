# Code signing

The Windows files of a numbered release, `Start-Server.exe`, `Start-Client.exe` and `host\dgsim.dll`, are signed through [SignPath](https://signpath.org)'s free code signing for open-source projects. The policy players see is in the [README](../../README.md#code-signing-policy). This page is the maintainers' side: what the build does, and what has to be set up once.

## What the build does

1. `re/scripts/make_release.ps1` gives each of the three files a version resource (`server/tools/peversion`): product name `OpenDawngateServer`, product and file version the release's name (such as `v0.7`), a description, the original file name and the copyright. SignPath requires the project's name as the product name and one product version for every file of a build. The script checks all three, in every build, signed or not.
2. For a pushed tag `v*`, once the repository has its SignPath settings (below), `.github/workflows/build.yml` uploads the three files as the workflow artifact `to-sign` and submits it to SignPath with the release's name as the parameter `version`. SignPath verifies that the artifact was built by this repository's workflow on GitHub's runners, checks the files' product name and version, and waits for an approver.
3. An approver approves the request on signpath.io. The workflow waits up to 3 hours; after that the run fails and has to be re-run.
4. The workflow builds the release again with `-SignedFiles`. `make_release.ps1` checks that each signed file is the file this build made with nothing but an Authenticode signature added (`peversion signed`), and that Windows finds the signature valid. Then it writes `BUILD.txt` (the SHA-256 that `Start-Client.exe`'s update checks are the signed files') and the archives. The Linux archive's `host/dgsim.dll` is the same signed file.
5. The release notes say whether the programs are signed and link the code signing policy.

Nightly builds and builds from source stay unsigned.

## Setting it up (once)

1. Apply at <https://signpath.org/apply> with the public repository. The conditions are on <https://signpath.org/terms>: an OSI licence (AGPL-3.0-only), no proprietary components, multi-factor authentication for everyone on the team on GitHub and SignPath, the code signing policy on the home page (the README's section) and on the release pages (the release notes).
2. In the SignPath project:
   - Link the trusted build system *GitHub.com* to the project.
   - Make the artifact configuration from [`.signpath/artifact-configuration.xml`](../../.signpath/artifact-configuration.xml) the project's default. Keep the two equal.
   - The signing policy for releases (SignPath's default slug is `release-signing`) needs manual approval, with the approvers named in the README.
   - Make an API token for a user with the *submitter* role on that policy.
3. In the GitHub repository's *Settings* → *Secrets and variables* → *Actions*:
   - the secret `SIGNPATH_API_TOKEN`: the API token;
   - the variable `SIGNPATH_ORGANIZATION_ID`: the SignPath organization's id. The signing steps run only when it is set;
   - the variables `SIGNPATH_PROJECT_SLUG` and `SIGNPATH_SIGNING_POLICY_SLUG`, only when they differ from `OpenDawngateServer` and `release-signing`.
4. Update the README's *Code signing policy* (drop the sentence that the signing is applied for) and the team roles there when the team changes.

## Checking a signed release

- In Explorer, a file's *Properties* → *Digital Signatures* names *SignPath Foundation*; *Details* shows the product name and version.
- In PowerShell: `Get-AuthenticodeSignature .\Start-Client.exe` says `Valid`, and `(Get-Item .\Start-Client.exe).VersionInfo` shows the version resource.
- `BUILD.txt` has the line `signed   Start-Server.exe, Start-Client.exe, host\dgsim.dll: <the signer>`.
