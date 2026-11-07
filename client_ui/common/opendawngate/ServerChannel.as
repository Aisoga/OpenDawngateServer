// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The UI channel, the movie's side: a class of ours in one of the game's movies talks to the server over its own TCP
// connection (the server's side and the wire: server/internal/uichannel; how to add a feature:
// docs/development/ui-channel.md).
//
//     var channel:ServerChannel = new ServerChannel();
//     channel.on("hello", onOpen);                     // connected, and again after every reconnect
//     channel.on("lobby.state", onState);              // onState(data:Object)
//     channel.open(playerName);
//     channel.call("lobby.setReady", {ready: true});
//     channel.close();
//
// Where to connect comes from the file the setup writes for the copy's server (Channel/dgchannel.json in the added
// archive). JSON is the game's own library (the movies have no global JSON class).
//
// What this client's flash.net.Socket does, measured (docs/development/ui-channel.md), and why the class
// watches `connected` once a second: connect() returns at once with connected already true and tries for about 6 s on a
// thread of the player; it fires CONNECT when it gets through, but neither CLOSE at a lost connection nor any event at
// a refused one. connected turns false within a second of either, so that is what ends a connection here. A Socket
// isn't used again after its end: each attempt has a new one.
//
// The socket's thread allocates from the movie's memory heap, which the game creates without locks. Our copy of the exe
// has them on (exepatch's heaplocks); without that patch a connection corrupts the heap within a second.
//
// Anyone can run a server, so what comes over this connection is hostile until read: a line has a length and a depth, a
// server past what any feature sends in a second is cut off (reading slower instead would pile its data up in the movie's
// heap, where the socket's thread puts it), and none of a server's text reaches the game's natives as it came (say).
package opendawngate {
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.ProgressEvent;
    import flash.events.SecurityErrorEvent;
    import flash.events.TimerEvent;
    import flash.external.ExternalInterface;
    import flash.net.Socket;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.utils.Timer;
    import flash.utils.getDefinitionByName;

    public class ServerChannel {
        private static const CONFIG:String = "Channel/dgchannel.json";
        private static const CONFIG_MAGIC:String = "dawngate-channel\t1";
        private static const JSON_CLASS:String = "com.adobe.serialization.json.JSON";
        private static const PROTOCOL:int = 1;
        private static const CHECK_MS:int = 1000;   // how often `connected` is looked at
        private static const RETRY_MAX:int = 16;    // seconds between attempts at most, doubling from 1
        private static const MAX_ASSERTS:int = 3;
        private static const MAX_LINE:int = 65536;  // the server's own limit (uichannel.MaxLine); a server past it is cut off
        private static const MAX_DEPTH:int = 32;    // a line's nesting: the JSON decoder recurses, and no event comes near this
        private static const MAX_CHARS:int = 262144; // characters between two checks: past what any feature sends
        private static const MAX_LINES:int = 200;    // lines between two checks, likewise
        private static const MAX_SAID:int = 160;     // characters of an Assert line

        private var host:String = null;
        private var port:int = 0;
        private var configLoading:Boolean = false;
        private var name:String = null;     // the player's, for the hello; null: closed
        private var sock:Socket = null;
        private var inbox:String = "";
        private var check:Timer = null;
        private var retryIn:int = 0;
        private var retryStep:int = 1;
        private var handlers:Object = {};   // event -> Function(data:Object)
        private var ready:Boolean = false;  // the server said hello
        private var charsRead:int = 0;      // since the last check
        private var linesRead:int = 0;
        private var said:int = 0;

        // handler(data:Object) gets every event of that name; "hello" is told each time the channel (re)opens, so a
        // feature says what its state is there
        public function on(event:String, handler:Function):void {
            handlers[event] = handler;
        }

        public function get isOpen():Boolean {
            return ready;
        }

        // Starts connecting, as the player `playerName`, and keeps the channel up until close().
        public function open(playerName:String):void {
            if (name != null) {
                return;
            }
            name = playerName;
            retryStep = 1;
            retryIn = 0;
            if (check == null) {
                check = new Timer(CHECK_MS);
                check.addEventListener(TimerEvent.TIMER, onCheck);
            }
            check.start();
            if (host == null) {
                loadConfig();
            } else {
                connect();
            }
        }

        public function close():void {
            name = null;
            if (check != null) {
                check.stop();
            }
            drop();
        }

        // A call to the server; false when the channel isn't open (the caller's state goes up again at the next "hello").
        public function call(method:String, args:Object = null):Boolean {
            if (!ready) {
                return false;
            }
            var m:Object = {call: method};
            if (args != null) {
                m.args = args;
            }
            return writeLine(json().encode(m));
        }

        private function say(s:String):void {
            if (said < MAX_ASSERTS) {
                said++;
                try {
                    ExternalInterface.call("Assert", "ServerChannel: " + plain(s));
                } catch (e:Error) {
                }
            }
        }

        // A text that may hold a server's words as far as a native may see it: short, and only harmless characters (the
        // native's handling of long strings and format characters is unknown).
        private static function plain(s:String):String {
            var out:String = "";
            for (var i:int = 0; i < s.length && i < MAX_SAID; i++) {
                var c:int = s.charCodeAt(i);
                var ok:Boolean = c >= 48 && c <= 57 || c >= 65 && c <= 90 || c >= 97 && c <= 122 ||
                    c == 32 || c == 46 || c == 58 || c == 95 || c == 45 || c == 34;
                out += ok ? s.charAt(i) : "?";
            }
            return out;
        }

        private function json():Object {
            return getDefinitionByName(JSON_CLASS);
        }

        // ---- where the server is ----

        private function loadConfig():void {
            if (configLoading) {
                return;
            }
            configLoading = true;
            var l:URLLoader = new URLLoader();
            l.addEventListener(Event.COMPLETE, function(e:Event):void {
                configLoading = false;
                readConfig(String(l.data));
                if (host != null && name != null) {
                    connect();
                }
            });
            l.addEventListener(IOErrorEvent.IO_ERROR, function(e:IOErrorEvent):void {
                configLoading = false;
                say("no " + CONFIG);
            });
            l.load(new URLRequest(CONFIG));
        }

        // the setup's lines: dawngate-channel<TAB>1, host<TAB><address>, port<TAB><n>, end
        private function readConfig(text:String):void {
            var lines:Array = text.split("\n");
            if (lines.length < 3 || lines[0] != CONFIG_MAGIC) {
                say("not a channel file: " + CONFIG);
                return;
            }
            for each (var line:String in lines) {
                var tab:int = line.indexOf("\t");
                if (tab > 0) {
                    var key:String = line.substring(0, tab);
                    if (key == "host") {
                        host = line.substring(tab + 1);
                    } else if (key == "port") {
                        port = int(line.substring(tab + 1));
                    }
                }
            }
        }

        // ---- the connection ----

        private function connect():void {
            drop();
            sock = new Socket();
            sock.addEventListener(Event.CONNECT, onConnect);
            sock.addEventListener(ProgressEvent.SOCKET_DATA, onData);
            sock.addEventListener(IOErrorEvent.IO_ERROR, onSocketError);
            sock.addEventListener(SecurityErrorEvent.SECURITY_ERROR, onSocketError);
            try {
                sock.connect(host, port);
            } catch (err:Error) {
                say("connect: " + err.message);
            }
        }

        // the socket's end, as ours: nothing of it runs any more
        private function drop():void {
            ready = false;
            inbox = "";
            if (sock == null) {
                return;
            }
            sock.removeEventListener(Event.CONNECT, onConnect);
            sock.removeEventListener(ProgressEvent.SOCKET_DATA, onData);
            sock.removeEventListener(IOErrorEvent.IO_ERROR, onSocketError);
            sock.removeEventListener(SecurityErrorEvent.SECURITY_ERROR, onSocketError);
            if (sock.connected) {
                try {
                    sock.close();
                } catch (err:Error) {
                    // it ended in the meantime: close() throws on a socket that isn't connected
                }
            }
            sock = null;
        }

        private function onCheck(e:TimerEvent):void {
            charsRead = 0;
            linesRead = 0;
            if (name == null || host == null) {
                return;
            }
            if (sock != null && sock.connected) {
                return;
            }
            if (sock != null) {
                drop(); // lost, or never got through
                retryIn = retryStep;
                retryStep = Math.min(retryStep * 2, RETRY_MAX);
            }
            if (--retryIn <= 0) {
                connect();
            }
        }

        private function onSocketError(e:Event):void {
            // nothing to do here: `connected` tells it within a second
        }

        private function onConnect(e:Event):void {
            retryStep = 1;
            writeLine(json().encode({call: "hello", args: {protocol: PROTOCOL, name: name}}));
        }

        private function writeLine(line:String):Boolean {
            if (sock == null || !sock.connected) {
                return false;
            }
            try {
                sock.writeUTFBytes(line + "\n");
                sock.flush();
                return true;
            } catch (err:Error) {
                say("write: " + err.message);
            }
            return false;
        }

        // One character at a time: bytesAvailable can promise more than there is, and readUTFBytes then simply gives an
        // empty string, where readUnsignedByte logs "Scaleform: Error: null". The server's lines are pure ASCII (anything
        // else as \uXXXX), so a byte is a character.
        private function onData(e:ProgressEvent):void {
            while (sock != null && sock.bytesAvailable > 0) {
                if (++charsRead > MAX_CHARS) {
                    cutOff("too much data");
                    return;
                }
                var c:String = sock.readUTFBytes(1);
                if (c.length == 0) {
                    return;
                }
                if (c != "\n") {
                    inbox += c;
                    if (inbox.length >= MAX_LINE) {
                        cutOff("a line over the limit");
                        return;
                    }
                    continue;
                }
                if (++linesRead > MAX_LINES) {
                    cutOff("too many lines");
                    return;
                }
                var line:String = inbox;
                inbox = "";
                dispatch(line); // a handler may close the channel: the loop looks at sock again
            }
        }

        // Not what our server does: the channel stays closed until the next open().
        private function cutOff(why:String):void {
            say(why);
            close();
        }

        // how deep a line nests, outside its strings, is more than MAX_DEPTH. A / outside a string counts too: the game's
        // JSON tokenizer skips comments, and a quote inside one would make what follows look like a string here. JSON has
        // none, and our server never sends one.
        private static function tooDeep(text:String):Boolean {
            var depth:int = 0;
            var inString:Boolean = false;
            for (var i:int = 0; i < text.length; i++) {
                var c:int = text.charCodeAt(i);
                if (inString) {
                    if (c == 92) { // a backslash: the next character is escaped
                        i++;
                    } else if (c == 34) {
                        inString = false;
                    }
                } else if (c == 34) {
                    inString = true;
                } else if (c == 47) {
                    return true;
                } else if (c == 91 || c == 123) {
                    if (++depth > MAX_DEPTH) {
                        return true;
                    }
                } else if (c == 93 || c == 125) {
                    depth--;
                }
            }
            return false;
        }

        private function dispatch(text:String):void {
            if (tooDeep(text)) {
                cutOff("a line nested too deep");
                return;
            }
            try {
                var m:Object = json().decode(text);
                var event:String = String(m.event);
                if (event == "hello") {
                    ready = true;
                } else if (event == "refused") {
                    say("refused: " + (m.data != null ? String(m.data.reason) : ""));
                    close(); // nothing changes by trying again; the next open() does
                    return;
                }
                if (handlers.hasOwnProperty(event)) {
                    var h:Function = handlers[event] as Function;
                    h(m.data);
                }
            } catch (err:Error) {
                say("a line: " + err.message);
            }
        }
    }
}
