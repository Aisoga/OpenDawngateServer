# Dawngate Restoration

[![build](https://github.com/Aisoga/OpenDawngateServer/actions/workflows/build.yml/badge.svg)](https://github.com/Aisoga/OpenDawngateServer/actions/workflows/build.yml)

Play **Dawngate** again. Dawngate was a MOBA by Waystone Games and Electronic Arts. It was in beta in 2014 and shut down in 2015. This project is a replacement server: it lets the original 2014 game client log in, open its menus and play full matches again, on your own PC, on your home network or on a server on the internet.

> **Unofficial fan project.** It is not affiliated with, endorsed by or sponsored by Electronic Arts or Waystone Games. Dawngate is a trademark of its respective owners. This repository and its releases contain **no game files**. Every player downloads the game client from archive.org.

## What works

- Logging in, the main menu, the collection, perk pages (loadouts), the store, your profile and match history.
- **Practice matches** against bots on the full map: minion waves, towers, jungle camps, spirit wells, the parasite and the Guardians.
- **Shaper select** with every shaper, role, skin and perk page.
- **Matches between players:** the solo queue, parties with invites and lobby chat, and in-match chat. Bots fill the empty seats.
- The end of a match: surrender votes, karma votes, rewards, and returning to a match after a crash.
- Hosting for friends on your home network, or on the internet with accounts, an encrypted connection and a small website for sign-up.

See [What you can do in a match](docs/playing.md#whats-in-a-match) for details and [Known limitations](docs/playing.md#known-limitations) for what's missing.

## Quick start

You need Windows 10 or 11 (64-bit), a graphics card that runs DirectX 9 games and about 5 GB of free disk space.

1. Download **`Dawngate.zip`** (1.3 GB, build 1.0.903.854) from <https://archive.org/details/Dawngate>.
2. Download the latest **release zip** from this repository's *Releases* page and unpack it into a folder of its own, for example `C:\Games\Dawngate`. (The `nightly` pre-release there is an automatic, untested build of the latest code; take a numbered release to play.)
3. Put `Dawngate.zip` into that folder, next to `Start-Client.exe`.
4. Double-click **`Start-Client.exe`** (its first start makes your copy of the game, about half a minute). The login screen opens with **This PC** selected: the server on your own PC, which `Start-Client.exe` runs by itself. Log in with any login name and a password of 8 or more characters. Your first login creates the account; the first time, it also sets up the server (about half a minute).
5. Click **PLAY**, then **PRACTICE**, then **PRACTICE DAWNGATE**. Pick a shaper, a role and a skin, and the match begins.

`Start-Server.exe` is only needed to run a server that other people play on ([docs/hosting.md](docs/hosting.md)).

Windows may warn that the programs are unrecognized while a release isn't signed yet ([Code signing policy](#code-signing-policy)). Click *More info*, then *Run anyway*.

To remove everything again, see [Uninstalling](docs/playing.md#uninstalling).

## Documentation

| I want to… | Read |
|---|---|
| play: install, the first match, controls, playing with friends | [docs/playing.md](docs/playing.md) |
| host a server for friends or for the internet | [docs/hosting.md](docs/hosting.md) |
| look up a setting, a command or a port | [docs/reference.md](docs/reference.md) |
| fix a problem | [docs/troubleshooting.md](docs/troubleshooting.md) |
| know what the programs send where | [docs/privacy.md](docs/privacy.md) |
| understand how the restoration works | [docs/how-it-works.md](docs/how-it-works.md) |
| build it from source, or work on it | [docs/development/](docs/development/README.md) |

## Safety in brief

The game client is code from 2014 and trusts the server it connects to.

- Play only on servers whose operator you trust.
- Use a password you don't use anywhere else.

On your own PC and home network the game and the server send nothing to the internet; `Start-Client.exe` itself asks GitHub for updates and, with the Discord app running, shows your status on Discord ([Privacy](docs/privacy.md), both can be switched off). A server on the internet runs at its operator's own risk. Its protections, and what they leave open, are in [Hosting on the internet](docs/hosting.md#hosting-on-the-internet).

## Code signing policy

The Windows programs of the numbered releases (`Start-Client.exe`, `Start-Server.exe` and `host\dgsim.dll`) get signed by the release build: free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org). The project is applying for it; until it is granted, the releases are unsigned.

- Only files built by this repository's own build (`.github/workflows/build.yml`, from a tagged commit, on GitHub's runners) are signed, never a file built elsewhere or another project's.
- Every signing request is approved by hand.
- Committers and reviewers: [Aisoga](https://github.com/Aisoga). Approvers: [Aisoga](https://github.com/Aisoga).
- Privacy policy: [docs/privacy.md](docs/privacy.md) lists everything the programs send and where (the servers you play on, the update check on GitHub, the Discord status) and how to switch each off. Nothing goes to this project's developers.

## License

This repository's own code and documentation are licensed under the **GNU Affero General Public License, version 3 only** (AGPL-3.0-only); see [`LICENSE`](LICENSE). Copyright (C) 2026 the Dawngate Restoration contributors.

The license covers only this project's code and docs, not the game, its files or its content, which belong to their owners. Third-party components keep their own licenses:

- **RakNet 4.081** (BSD, with its patent grant) is downloaded separately and patched at build time with this project's fixes.
- The Go modules `modernc.org/sqlite` (BSD-3-Clause and MIT; SQLite itself is public domain) and `golang.org/x/crypto` (BSD-3-Clause) are fetched by the Go toolchain.

Each release includes all of their license texts in `THIRD-PARTY-NOTICES.txt`.
