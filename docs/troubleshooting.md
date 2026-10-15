# Troubleshooting

- [Starting up](#starting-up)
- [Logging in](#logging-in)
- [Starting a match](#starting-a-match)
- [During a match](#during-a-match)
- [Playing over a network](#playing-over-a-network)
- [Reporting a problem](#reporting-a-problem)

## Starting up

**"is not the Dawngate.zip of archive.org"**
The setup checks every file against build 1.0.903.854. Download the zip again from <https://archive.org/details/Dawngate>; the download may be incomplete.

**Start-Server can't find Dawngate.zip**
Put the zip next to `Start-Server.exe`, or in your Downloads folder. You can also drag it onto the program's window when it asks, or set `zip = <path>` in `Start-Server.ini`.

**The setup stopped halfway**
Start the program again: it continues where it stopped. If the same step fails again, delete the folder the message names inside `Dawngate-Data`, and it will be made anew.

**"Another program holds the server's ports"**
A server is already running, probably another `Start-Server` window. Close it first. If no other server is running, some other program uses one of the [server's ports](reference.md#ports).

**"No server is running"**
`Start-Client.exe` couldn't find a server on this PC. Start `Start-Server.exe` first and wait until it says the server runs. To play on another PC's server, start `Start-Client.exe <address>`.

**The game closes right away**
Only one Dawngate client can run at a time. Close the other one, including any that's still open from an earlier session.

**SmartScreen or antivirus warnings**
The programs aren't signed. Click *More info*, then *Run anyway*.

## Logging in

**"The login name or password you entered is incorrect"**
Check the password. On a server with open sign-up, a new login name creates a new account, so a typo in the name logs you into a different, new account.

**Forgot your password**
The server's operator can print a new one with `Start-Server.exe --reset-password <name>`, or reset it on the admin page. If the server is your own, you are the operator.

**"Your account is disabled" or "banned"**
Contact the server's operator.

**A LOGIN ERROR with only a number**
Too many login attempts from your address in a short time. Wait a minute or two and try again.

**"The server's key has changed since you last played", or KEY CHANGED in the server list**
Every server has a tunnel key, and `Start-Client.exe` remembers the key of each server you have logged in to. A different key means that the operator made new keys or moved the server, or that someone else answers at its address. Open the server list on the login screen: it shows the key you trusted and the one the server shows now. Ask the operator which fingerprint their server window shows. Only if it matches the new one, delete the file `Dawngate-Data\servers\<server address>\gateway.pin` and log in again. That login trusts the new key. If it doesn't match, don't log in to that server.

**"The server you picked doesn't answer", or offline in the server list**
The server is offline, or its address in your list is wrong. The list checks every server again every few seconds while the login screen is open, so the server turns green by itself when it is back. If it stays offline, check the address with its operator. An operator of a server on the internet forwards TCP and UDP 42128 to it.

**"Something answers at that address, but no Dawngate server with the tunnel on"**
The server picker needs the server's tunnel. Ask the operator to set `tunnel = on` in `Start-Server.ini`.

**"That server isn't in your list"**
The login name had a `#` with a server that isn't in your list. Type only your login name and pick the server with SERVER above the login panel.

**"The server you picked answered, but couldn't take the login"**
The server's tunnel works, but its login service didn't answer in time, for example while it is starting. Try again in a moment.

**"That isn't a server address"**
The address typed in ADD SERVER (or after `#+` in the login name) is no IPv4 address or host name. Type it as `203.0.113.5` or `play.example.org`, with `:<port>` only if the operator's tunnel port isn't 42128.

**"The server was removed from your list"**
That is REMOVE's confirmation, not an error. Click OK and pick another server.

**There is no SERVER above the login panel**
The login screen shows the server list only for servers reached through their tunnel. A server on a home network without the tunnel is played on directly; `Start-Client.exe`'s window says so (*picker not used*). If the server has its tunnel on, start `Start-Client.exe <address> --tunnel on`. If `Start-Client.ini` has `server = <a home address>`, empty it to get the list. `picker = off` turns the list off.

**Shift+Tab or Escape on the login screen does something else**
The server list is used with the mouse. Shift+Tab from the login name goes to the game's EXIT button, and Enter there asks whether to quit the game. Escape doesn't close the list; click DONE.

**The login screen can't connect, or stays at "Initializing"**
The game copy and the server don't match. This happens, for example, when the server's address or certificates changed. Start `Start-Client.exe` again: it checks the copy and patches it for the server.

## Starting a match

**Nothing happens after PRACTICE DAWNGATE, and the envelope at the bottom right shows "1"**
The server couldn't start a match. Click the envelope, then the notification: it says "Game creation failed - no servers available". Check the server window and its log in `Dawngate-Data\logs`. Ten matches at once is the default maximum.

**"Failed to connect to game server" about 20 seconds after the click**
The match host ended before it was ready. Its log is in `Dawngate-Data\logs\hosts\<match>\dgsim.log`.

## During a match

**The world freezes: nothing moves, and you can't attack or cast**
The server notices a stuck match after about 10 seconds and ends it, and you are back in the main menu. Start a new match. The match's folder under `Dawngate-Data\logs\hosts\` then has `watchdog.txt` and `watchdog.dmp`. Please include them in a report.

**You were logged out in the middle of a match**
The game logs you out after five minutes without any input. Log in again.

**The game crashed**
A crash window may appear. Its report stays on your PC and is never sent. It's under `%LOCALAPPDATA%\CrashRpt\UnsentCrashReports\` and includes screenshots of your screens; delete it if you like. Start the game again, log in and click **Rejoin** to return to the match.

**The window's close button does nothing**
In a match the game ignores it. Leave the match from the gear menu above the minimap, then close the game.

**Messages in the game's log**
`%APPDATA%\DawngateData\Application.log` contains many asserts. Most are harmless and come from the game itself, for example `GameSimulator.cpp` 7412 in every match.

## Playing over a network

**Other PCs can't reach your server**

- `address` in `Start-Server.ini` must be the server PC's LAN address, not `127.0.0.1`.
- When Windows asks whether `Start-Server.exe` and the game copy in `Dawngate-Data\host` may use the network, allow private networks. If you cancelled the question, allow them in the Windows Firewall settings.

**"The server ... has another certificate authority" or another tunnel key**
The server's fingerprint changed since you last played. That happens when its operator changed the address or made new keys. Ask the operator which fingerprint their window shows, and answer `y` only if it matches. For a server in the login screen's list, see *The server's key has changed* under [Logging in](#logging-in).

**"gateway unavailable"**
The server's tunnel refused the connection, usually because too many sessions come from your address. Close other game sessions and try again.

**A public server isn't reachable from outside**
Forward TCP and UDP 42128 and TCP 443 to the server PC, and check that `address` is your current internet address. Many home connections get a new address now and then.

## Reporting a problem

Please include:

- what you did and what happened, with the time;
- the game's log, `%APPDATA%\DawngateData\Application.log`;
- for a problem in a match, the match's folder from `Dawngate-Data\logs\hosts\`, if you run the server.

Logs contain login names and addresses. Remove what you don't want to share.
