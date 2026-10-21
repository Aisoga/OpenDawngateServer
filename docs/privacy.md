# Privacy

OpenDawngateServer collects nothing for its developers: no analytics, no account with the project, no reports sent to us. The programs transfer information only to the systems listed here: the servers you choose to play on, GitHub for the update check, and Discord through the Discord app on your PC. Each one that isn't the point of playing can be switched off.

## Start-Client.exe

| What | Where it goes | When | To switch it off |
|---|---|---|---|
| Your login, and everything the game sends while you play (your actions, chat, the game's usage telemetry with an anonymous token) | The server you pick in the server list, through its encrypted tunnel | When you log in to it | Play only on **This PC**, the server on your own PC |
| A connection to each server in your server list, to show whether it is online | Those servers | Every 15 seconds while the login screen shows | Remove a server from the list |
| The update check: one HTTPS request for the newest release, with the program's version in its user agent | `api.github.com` (GitHub's privacy statement applies) | At each start of a numbered release; the download only after you say yes | `update_check = off` in `Start-Client.ini` |
| Your Discord status: what you do in the game (*In the menu*, *In match · PvP*, ...), a timer and your party's size, never your name or your shaper | The Discord app on your PC, which shows it to your Discord friends (Discord's privacy policy applies) | While the game and the Discord app run | `discord = off` in `Start-Client.ini` |
| Discord's *Join*: the address and the tunnel key of the server you play on and a code for your party or lobby | The same, and to anyone who sees your Discord profile or a Discord *Invite to join* you post; they can use it to join while your party or lobby is open | While you are in a party or lobby with a free seat | `discord_join = off` in `Start-Client.ini` |

At each start, `Start-Client.exe` prints one line on whether the Discord status is on and one on whether Join is on, with the setting that switches it off.

What stays on your PC:

- **Crash and error reports.** The game's crash reporter writes a report with a memory dump, the game's log and settings, and pictures of every screen. `Start-Client.exe` answers the game's attempts to send one itself and passes nothing on. The reports are saved under `%LOCALAPPDATA%\CrashRpt\UnsentCrashReports\`; delete them if you like.
- **The game itself.** Its copy is patched to contact only `Start-Client.exe` on your own PC (127.0.0.1) instead of Electronic Arts' addresses, and its router port forwarding (UPnP) is switched off.
- **This PC's server.** It listens only on your own PC's loopback addresses.

## Start-Server.exe

A server keeps what it needs to run: the accounts (login name, display name and a hash of the password), what each account owns, the match histories, and logs with players' login names and internet addresses. All of it stays on the server's PC, in `Dawngate-Data`; logs are deleted after `log_days` days. The server's operator is responsible for that data, not this project ([hosting.md](hosting.md#security-what-is-protected-and-what-isnt) *Personal data*).

`Start-Server.exe` makes no update check and contacts no one on its own, with one exception: a public server with a domain name (`web_domain`) gets the website's certificate from Let's Encrypt, which then knows that domain and, if you set `web_email`, that address for its expiry mails.

## The match host

`host\dgsim.dll` runs each match inside the game's code on the server's PC. It blocks the game's own network connections and talks only to its server on the same PC (127.0.0.1).

## Questions

Ask in the project repository's *Issues* on GitHub.
