# Decision 5: Our UI classes talk to the server over their own connection

- **Status:** accepted (2026-11-04, the maintainer's request after the first ready check). The pattern for every later UI feature that needs the server.
- **In short:** a class of ours in a game movie opens a plain TCP connection with `flash.net.Socket` to a small service of `dgserver` (`server/internal/uichannel`). Calls and events are lines of JSON. On a tunnelled server the connection crosses the tunnel, and the tunnel session says who the player is.

## Context

- **Our classes run inside the game's Flash movies,** and the game gives them no way to reach the server: the natives are read-only, and C++ hands the movies only what its own screens need.
- **The first ready check used the only thing that reached both ways, the lobby's chat.** The class sent hidden chat lines, the server answered in chat lines, and the class scanned the chat box every frame and cut them out before they showed. It worked, but a button's click travelled as a chat message, the chat's scroll position jumped, a line typed in the chat could pass for a server line, and the button only appeared after a few seconds. The maintainer's verdict: a clean solution, and the proof of concept for later features.
- **What the client can do was measured, not assumed** ([the guide](../ui-channel.md#what-the-games-flash-player-does)): this client's Scaleform player implements `flash.net.Socket` with real TCP, UTF-8 and a working `flash.utils.Timer`, and its movies carry a JSON library and framework events for screens. One thing has to be fixed for it: the socket's thread allocates from the movie's memory heap, which is created without locks, and that crashes the client within a second or so.
- **Every player connection already crosses the encrypted tunnel,** and each of its streams carries the session it belongs to.

## Decision

1. **One small exe patch, once.** `heaplocks` turns the Scaleform heaps' locks on (5 bytes at each of 2 places), which fixes the race at its cause. It is the only exe change the channel needs, and no feature needs another.
2. **A service of its own.** `server/internal/uichannel` listens on TCP 10039 (`--ui-port`), knows nothing about features, and offers `Handle(call, handler)` and `Send(user, event, data)`. Features register their calls; `blaze/ready.go` is the first.
3. **Lines of JSON, one direction each.** A call goes to the server, an event comes back; a call has no reply of its own. Plain text keeps the protocol readable in a log and testable with any TCP client.
4. **Identity from the connection, never from the message.** Through the tunnel the session names the player, and a handler gets that player's id. Only the developers' direct path (`tunnel = off`) trusts a name, checked against the address of that name's login; it has no encryption or real identity to build on anywhere.
5. **The state lives on the server, and the class draws what it is told.** A feature's state is an ordinary Go object, and the event carries it whole where it is small.
6. **No client without the channel is blocked.** A player whose class never connected, or whose connection ended, doesn't take part. A lost connection can't hold a lobby up.
7. **Classes wait on the game's own events,** not on a frame loop: `GComponent.componentTreeChangeHandlers`, `signalViewCreated`, `showComplete`, `hideComplete`, `drawSignal`.
8. **Both ends treat the other as hostile.** The server limits what a client can make it do and what it writes to its log, and a connection lasts as long as the login it came by. The class limits what a server can make it read and shows none of a server's text to a native as it came. The rules for a feature: [the guide](../ui-channel.md#security).

## The alternatives

- **Keep the chat, and catch its lines earlier** by patching `ChatManager.OnGameMessage` in the movie's bytecode. That removes the scanning and the side effects, but a button's click still travels as a chat message, and our setup would start rewriting EA's code.
- **Blaze player attributes** (`setPlayerAttributes`, `NotifyPlayerAttribChange`), which is how EA's demo mode kept its own ready state. It is the protocol's natural place, but the movies can't reach it: every call would need a new native, written as machine code in an exe patch, and every later feature another one. The channel's one patch is a fixed cost instead.
- **A DLL of ours in the client,** adding natives in C++. The most capable way, but a second native component to ship and keep working, for players who today need only an exe patch and a data archive.
- **Authenticating the connection with a token.** A movie has nowhere to get a secret from without yet another channel, and a program on the player's PC could read whatever file it came from. The tunnel session settles who the player is against the server; on the player's own PC the port stays open to any local program, so the channel carries only game state.

## Consequences

- **A new UI feature needs no exe patch of its own, no bytecode patch and no chat:** a Go handler and an ActionScript class ([the guide](../ui-channel.md)).
- **Every player copy carries `heaplocks`.** Each allocation of the UI takes an uncontended lock. A copy without the patch must not open a socket; the setup installs the exe and the classes together.
- **One more TCP port.** Through the tunnel it costs nothing, since the gateway lists it and only the gateway's port is forwarded. A server with `tunnel = off` must also let 10039 through, or the channel's features are simply absent.
- **Older ways move over one at a time.** Other features still pass data in their own ways (the create dialog's bot box as a marker in the game's name, the picker's server tag in the login name). The login's tag stays: it comes before there is a session on any server, and inside the login it can't get lost or arrive late, while a pick on a second connection could send the player's password to another server than the one on screen.
- **The client's socket quirks are confined to `ServerChannel`:** no event at a refused or lost connection, so it watches `connected` once a second.
