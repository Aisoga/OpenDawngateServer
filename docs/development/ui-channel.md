# The UI channel

How our additions to the game's screens talk to the server. A class of ours in one of the game's UI movies opens its own TCP connection, calls the server and hears back over it. Nothing goes through the chat or any other part of the game: a button click is a call, and a change on the server is an event.

The custom lobby's ready check is the first feature on it, and the example below. Use the channel for any new UI feature that needs the server. The reasons, and the alternatives that lost: [decision 5](decisions/0005-ui-channel.md).

## The whole feature, in both languages

The ready check in short. On the server, a handler per call:

```go
ui.Handle("lobby.setReady", func(user int64, args json.RawMessage) error {
    var a struct {
        Ready *bool `json:"ready"`
    }
    if err := json.Unmarshal(args, &a); err != nil || a.Ready == nil {
        return fmt.Errorf("arguments %s: no ready", args)
    }
    return s.lobbySetReady(user, *a.Ready) // changes the state, then ui.Send(...) to whoever cares
})
```

In the class, a call and a handler per event:

```actionscript
channel.on("lobby.state", onState);                // onState(data:Object)
channel.call("lobby.setReady", {ready: true});
```

That is all a feature needs from the channel. `user` is always the player the connection belongs to; a handler never takes it from the arguments.

## Adding a feature

1. **On the server,** write the state as an ordinary Go object and register its calls with `ui.Handle` (see `AttachUI` in `server/internal/blaze/ready.go`). When the state changes, tell the players concerned with `ui.Send(user, "<event>", data)`; `data` is anything `encoding/json` can marshal.
2. **In the class,** create a `ServerChannel`, register `on` handlers and call `open(playerName)`. Say your state in the `"hello"` handler: it runs at every (re)connect, so a lost connection recovers by itself.
3. **Build:** `python -I re/scripts/build_picker.py build <spec>`; `client_ui/common` is on every class's source path.

Calls have no replies of their own. A call changes something on the server, and the server's event says how things now stand. Send the whole state of a thing rather than differences where it is small, as `lobby.state` does: the class then only draws what it is told, and there is nothing on the client to get out of step.

Name calls and events `<area>.<name>`. A new call on an older server is logged and ignored, so a class can be ahead of the server.

## The wire

One JSON object per line, at most 64 KiB, on TCP port **10039** (`dgserver --ui-port`). Calls are UTF-8. Events are pure ASCII, with anything else escaped as `\uXXXX`, so a byte is a character when the class reads them.

| Direction | Line |
|---|---|
| Client → server | `{"call":"lobby.setReady","args":{"ready":true}}` (`args` may be left out) |
| Server → client | `{"event":"lobby.state","data":{"players":[{"name":"alpha","rating":1612},{"name":"bravo","ready":false}]}}` (`data` may be left out) |

A connection starts with a handshake: `{"call":"hello","args":{"protocol":1,"name":"<the player's name>"}}`, answered by `{"event":"hello","data":{"protocol":1}}` or by `{"event":"refused","data":{"reason":"..."}}` and the connection's end. Right after the hello every connection gets the events of the server's `Greet` hook, before any other event: today `region.state` (the regions and the player's Preferred Region), `leaderboard.state` (the Leaderboard's tab) and `profile.state` (the Profile's game type), so a class that needs one has it at every connect and reconnect without a call. A class ignores events it has no handler for.

A connection is ended for a malformed line, a line over the limit, a player's calls going over 10 a second (bursts of 20, counted over all that player's connections and across reconnects), no hello within 10 s, a name in the hello over 64 bytes, or not reading its events (64 queued). A player may hold up to 8 connections at once, so classes in different movies don't have to share one; every connection gets the player's events.

The other way, `ServerChannel` ends the channel until its next `open()` for a line over 64 KiB, nesting deeper than 32 levels, a `/` outside a string (the game's JSON library skips comments, which could hide nesting), more than 200 lines or 256 KiB between two of its once-a-second checks, or a `refused`. A feature has to stay under those: one event of a few KB is fine, a stream of them isn't.

## Who a connection belongs to

- **Through the tunnel** (every player's path), the tunnel session says it. One Start-Client has one session and one login, and the server takes the player from the Blaze connection in the same session. The `name` in the hello is ignored, so a client can't speak for someone else. (A session with several logins exists only in the test lanes, with two clients behind one tunnel. There the name picks among that session's logins, and only those.)
- **Directly** (`tunnel = off`, the developers' path), the name must be logged in without the tunnel from the same address, every loopback address counting as one. This path has no encryption or real identity to build on: its Blaze connection carries logins in plain text too.

A connection ends when the login it came by ends, also while the same player has another login elsewhere (`uichannel.Recheck`). A player whose channel is gone counts as a client without our classes, so a lost connection can't block a lobby.

## Security

This is an open-source project, so assume that everyone has read this code, that clients can be modified, and that anyone can host a server. What that means for a feature:

- **A class can't enforce anything.** A greyed-out button is a convenience: a modified client calls what it likes, as often as the limits let it. The server decides who may do what (the ready check's start is refused on the server too). A handler takes the player from its `user` argument, never from its arguments.
- **A call's arguments are hostile.** Check their types and ranges, and don't put them in a log line or an error as they came: `uichannel` cuts what it logs short, but only the handler knows what a value should be.
- **What a call makes the server do must cost little.** The budget is 10 calls a second per player. A handler that reaches the store, or tells other players, shouldn't do it at every call: answer the same state with nothing, keep a value that can't change, and limit how often a broadcast goes out (`publishLobby`). Other players' clients redraw at each event.
- **An event is hostile to the class.** A player can connect to any server. Draw a server's text with `.text`, never `htmlText`, turn numbers into numbers, and don't pass its text to a native (`ServerChannel.say` cleans what goes to `Assert`). Handlers run inside a `try`, but a wrong value can still draw something wrong.
- **What goes on this channel is game state, and not secrets.** Through the tunnel, the port on the player's PC is loopback only and has no password of its own. With the server picker (the default) only the player copy's programs may connect (`launcher.gameGuard`: the connection's owner by the system's table, its file by folder identity), which keeps out a browser, another Windows user and a program that simply connects. A program of the same user that is willing to inject itself into the game gets in anyway; it could read the game's memory too, so nothing on this PC would hold against it. A web page can't: its first line is an HTTP request, which ends the connection. So nothing that changes an account, spends anything or hands out a credential belongs on it. A movie has nowhere safe to keep a secret from such a program anyway.
- **An action that can't be undone doesn't rest on two connections arriving in order.** The channel and the game's Blaze connection are separate, so nothing says which is read first. The login's server pick stays inside the login itself for that reason: a lost pick would send a password to another server.

## How the connection gets there

- **Where to connect:** the setup writes `UI/Scaleform/Channel/dgchannel.json` into the `zz_CollectionItems.snap` archive for the server the copy is set up for (`moviepatch.ChannelFile`). Any movie can read it, as `Channel/dgchannel.json`.
- **On a tunnelled server** `Start-Client` listens on the port, one of the game's fixed ones, and carries it over TLS 1.3 like the game's own connections. The server's own `--ui-port` has to stay 10039 here: the number isn't mapped. The port doesn't need forwarding in a router. The picker's front passes it to the server of the current login; before a login it refuses it.
- **With `tunnel = off`** the server listens on the port itself, so a firewall in between must let it through. Without it the game plays as before, without the ready check.

## What the game's Flash player does

Measured in the running client (build 854). `ServerChannel` handles all of this, so a feature doesn't need to care:

- **`flash.net.Socket` needs an exe patch to be safe.** Its thread allocates from the movie's memory heap, which the game creates without locks, so an open connection corrupts the heap within a second or so, and the client crashes at a random place. The `heaplocks` patch (`exepatch`, `patch_client.py`) turns the heaps' locks on, in every player copy. It is the only part of the channel that touches the exe.
- `connected` is `true` immediately after `connect()`, and the connection attempt runs on a thread of the player for about 6 s.
- `CONNECT` and `SOCKET_DATA` fire. `CLOSE` doesn't fire when the connection is lost, and nothing fires when it is refused. `connected` turning false is the only sign, so the class checks it once a second with a `flash.utils.Timer`.
- `close()` throws on a socket that isn't connected. A socket isn't reused after its end.
- `bytesAvailable` can promise more than there is. `readUTFBytes(1)` then gives an empty string, while `readUnsignedByte` logs `Scaleform: Error: null`, so read one character at a time. That is also why events are pure ASCII.
- The movies have no global `JSON`. `com.adobe.serialization.json.JSON` is in the game's code and does both directions (`GravityJSON.encode` returns null here).
- A clean quit with a socket open is fine: the server sees the connection end. A movie unloaded with a socket open while the game goes on has not been tried, and the socket's thread allocates from that movie's heap: check it before using the channel in a movie that doesn't stay loaded. The play screen's movie is one (the game loads it anew at each visit), so its Preferred Region class (`client_ui/matchtype/RegionPicker.as`) keeps the channel open only for the moments it needs it: the state at the movie's start and the answer to a pick, a few tens of milliseconds each, then it closes it again (also when the movie leaves the stage). Do the same in any movie that doesn't stay loaded.

## Waiting for a screen without polling

The game's own Gravity framework has events for the things classes used to look for every frame:

- `GComponent.componentTreeChangeHandlers`: a public list of functions called whenever a component comes or goes anywhere. Push yours and look with `GComponent.find(id)`. (`waitForComponentAdd` looks similar but goes by a component's `name`, not its `id`, and serves only once.) This one is in the front end's code movie (`UIRuntime.gfx`) only: the other movies' copies of the framework don't have it.
- `component.signalViewCreated`: its view exists.
- `view.showComplete`, `view.hideComplete`, `view.drawSignal`: the view showed, hid, or has just drawn. React to a draw on the next frame: an invalidation made inside the view's draw is dropped at its end.
- `component.nativeBridgeDestroyedSignal`: C++ took the screen's natives away, which comes before the view hides. Stop here: a call into them now crashes the client.

- A view's own parts have them too: the custom lobby's `playerTileAdded` hands out each player tile as it is built, and a tile has `drawSignal`, `showComplete`, `dragStart` and `dragComplete`; a layout `Box` has `drawSignal`. Go by the tile, not by the data behind it: the natives know a player seconds before the tile is drawn, and a dragged tile leaves its cell for the stage. At `dragComplete` a tile still says `isDragging`, so look on the next frame.

`client_ui/lobby/LobbyReady.as` runs on these and has no frame loop of its own, except for the few frames after a draw while the tiles' layouts settle.

## Testing

- `go test ./internal/uichannel` holds the wire, the limits and the handshake; `./internal/blaze` (`TestReadyCheck`) a whole feature with real Blaze and channel clients; `./internal/dgserver` (`TestUIChannelThroughTunnel`) the identity through the real tunnel.
- `blaze/ready_test.go`'s `uiConnect` and `attachUI` are what a new feature's test needs.
- Live, `run_session.ps1 -SecondClient` covers the direct path and `-Tunnel` the player's; the server's log lines carry the tag `[uichannel]`.
