// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the Dawngate Restoration contributors.
//
// The server picker on the game's login screen.
//
// Start-Client places this class on the login movie's root timeline with four added tags (the game's own
// bytecode stays as it is) and writes the list of servers into an added archive before every game start, as
// UI/Scaleform/LoginScreen/dgservers.json (a line format, read below). The picker shows "Server: <name>" above
// the login panel; its list has a row per server (name, address, the status Start-Client found at the start)
// and Add and Remove. The pick reaches Start-Client through the login name: at LOGIN the name becomes
//     <name>#<tag>                      log in on the server with that tag
//     <name>#+<address>[=<name>]        add that server (Start-Client checks it, trusts its key on first use,
//                                       stores it) and log in on it
// and Remove sends a login as <name>#-<tag>, which Start-Client answers by removing the server (its own error
// code, so the game shows the login error panel). The first row is This PC, the server on this PC (Start-Client
// starts it at the login to it), which can't be removed. The game's handler reads the field after ours has changed
// it; ours puts the typed name back right after, so the field and a remembered login name show the name only.
//
// The texts come from the game's LoginScreen string table (lines 0x5e70a1xx, which Start-Client's setup adds to the
// copy's locale package) through the game's own Global.ForceTranslateString; the English below is the fallback.
// Start-Client writes the list again while the login screen shows (each server probed again), and the class reads it
// again every few seconds, so a server going offline or coming back shows without a restart.
//
// Build: re/scripts/build_picker.py (the Apache Flex SDK's mxmlc). Without the list file (Start-Client not in
// picker mode) the class shows nothing and changes nothing.
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.InteractiveObject;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.KeyboardEvent;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.geom.Rectangle;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.text.TextFormatAlign;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    public class ServerPicker extends MovieClip {
        // The game's file opener reads URLs below ui/scaleform/ from the mounted archives and keeps only .json
        // (any other extension becomes .gfx).
        private static const LIST_URL:String = "LoginScreen/dgservers.json";
        private static const LIST_MAGIC:String = "dawngate-servers";
        private static const ROWS:int = 6;
        private static const ROW_H:int = 32;
        private static const DETAIL_H:int = 60;     // the selected server's status in words, under the rows
        private static const RELOAD_MS:int = 4000;  // how often the list is read again while the login panel shows

        // The texts: the LoginScreen table's line ids (re/scripts/patch_locale.py, server/internal/setup/localepatch
        // add them) and the English fallback. {1} is the ping, {2} the key the server shows, {3} the trusted key
        // (fingerprints: 16 hex digits, as Start-Server shows them).
        private static const TX_SERVER:Array = [0x5e70a101, "SERVER"];
        private static const TX_SELECT:Array = [0x5e70a102, "SELECT A SERVER"];
        private static const TX_ADD_SERVER:Array = [0x5e70a103, "ADD SERVER"];
        private static const TX_REMOVE:Array = [0x5e70a104, "REMOVE"];
        private static const TX_DONE:Array = [0x5e70a105, "DONE"];
        private static const TX_ADD:Array = [0x5e70a106, "ADD"];
        private static const TX_CANCEL:Array = [0x5e70a107, "CANCEL"];
        private static const TX_ADDRESS:Array = [0x5e70a108, "Server address"];
        private static const TX_NAME:Array = [0x5e70a109, "Name (optional)"];
        private static const TX_ADD_HINT:Array = [0x5e70a10a, "Added at your next login, which checks the server and shows its key: log in again to trust it."];
        private static const TX_BAD_ADDRESS:Array = [0x5e70a10b, "Type the server's address: an IPv4 address such as 203.0.113.5, or a host name."];
        private static const TX_EMPTY:Array = [0x5e70a10c, "No servers yet: add one."];
        private static const TX_NONE:Array = [0x5e70a10d, "none: add a server"];
        private static const TX_PICK:Array = [0x5e70a10e, "pick a server"];
        private static const TX_THIS_PC:Array = [0x5e70a10f, "This PC"];
        private static const TX_ST_OK:Array = [0x5e70a110, "online, {1} ms"];
        private static const TX_ST_NEW:Array = [0x5e70a111, "online, new key"];
        private static const TX_ST_CHANGED:Array = [0x5e70a112, "KEY CHANGED"];
        private static const TX_ST_OFFLINE:Array = [0x5e70a113, "offline"];
        private static const TX_ST_NO_TUNNEL:Array = [0x5e70a114, "no tunnel"];
        private static const TX_ST_PENDING:Array = [0x5e70a115, "added at login"];
        private static const TX_ST_UNKNOWN:Array = [0x5e70a116, "not checked"];
        private static const TX_ST_LOCAL:Array = [0x5e70a117, "starts at login"];
        private static const TX_ST_SETUP:Array = [0x5e70a118, "setting up, {1} %"];
        private static const TX_ST_STARTING:Array = [0x5e70a119, "starting"];
        private static const TX_ST_FAILED:Array = [0x5e70a11a, "didn't start"];
        private static const TX_D_OK:Array = [0x5e70a120, "Online, {1} ms. Its key: {2}."];
        private static const TX_D_NEW:Array = [0x5e70a121, "New server key {2}: compare it with the fingerprint its operator gives you. The first login stops here; log in again to trust it."];
        private static const TX_D_CHANGED:Array = [0x5e70a122, "KEY CHANGED: you trusted {3}, the server now shows {2}. Don't log in until its operator confirms the new key: see the troubleshooting guide."];
        private static const TX_D_OFFLINE:Array = [0x5e70a123, "No answer from this server. It may be offline, or its address may be wrong."];
        private static const TX_D_NO_TUNNEL:Array = [0x5e70a124, "Something answers at this address, but no Dawngate server with the tunnel on."];
        private static const TX_D_UNKNOWN:Array = [0x5e70a125, "Not checked yet."];
        private static const TX_D_LOCAL:Array = [0x5e70a126, "The server on this PC. It starts when you log in; the first time it sets up its match host first (about half a minute)."];
        private static const TX_D_SETUP:Array = [0x5e70a127, "Setting up the server on this PC: {1} %. Your login waits for it, or log in again when it says online."];
        private static const TX_D_STARTING:Array = [0x5e70a128, "The server on this PC is starting."];
        private static const TX_D_FAILED:Array = [0x5e70a129, "The server on this PC didn't start: Start-Client's window says why. Log in again to retry."];

        private static const GOLD:uint = 0xC9A865;
        private static const TEXT:uint = 0xE6DFD0;
        private static const DIM:uint = 0x9C9588;
        private static const GREEN:uint = 0x5DBE4A;
        private static const AMBER:uint = 0xD9A93A;
        private static const RED:uint = 0xD0473B;
        private static const GREY:uint = 0x7A7A7A;

        private var servers:Array = [];   // {tag, name, address, port, status, ping, key, trusted, pending, named, fixed}
        private var sel:int = -1;
        private var texts:Object = {};    // the looked-up texts by line id
        private var loading:Boolean = false;
        private var loadedAt:int = 0;     // getTimer() of the last read of the list
        private var top:int = 0;          // the first row shown
        private var preselect:String = "";
        private var lastUsed:String = "";
        private var listState:int = 0;    // 0 loading, 1 loaded, -1 none
        private var remembered:Object = null; // the server a remembered name#suffix named

        private var frames:int = 0;
        private var hooked:Boolean = false;
        private var lp:DisplayObjectContainer;
        private var acc:Object;
        private var pw:Object;
        private var btn:Object;
        private var saved:String = null;
        private var stripFrames:int = 900; // how long a remembered name#suffix is taken off the field
        private var stripped:String = null;

        private var strip:Sprite;
        private var selector:Object;
        private var selDot:Sprite;
        private var selName:TextField;
        private var panel:Sprite;
        private var rowBox:Sprite;
        private var rows:Array = [];
        private var emptyText:TextField;
        private var listButtons:Sprite;
        private var removeBtn:Object;
        private var addBox:Sprite;
        private var addrInput:Object;
        private var nameInput:Object;
        private var addHint:TextField;
        private var detail:TextField;
        private var title:TextField;
        private var selStatus:TextField;
        private var tabbed:Array = [];    // the panel's controls with a tab index (tabbable only while it shows)
        private var W:Number = 460;

        public function ServerPicker() {
            super();
            mouseEnabled = false;
            visible = false;
            load();
            addEventListener(Event.ENTER_FRAME, onFrame);
        }

        // -- the texts --------------------------------------------------------------------------------------------

        // tr is a text of the game's LoginScreen table, else its English fallback (a line the table lacks gives
        // nothing back: the game's handler sets no return value then).
        private function tr(t:Array):String {
            var id:uint = uint(t[0]);
            if (texts[id] === undefined) {
                var s:String = null;
                try {
                    var hex:String = id.toString(16);
                    while (hex.length < 8) {
                        hex = "0" + hex;
                    }
                    var r:* = ExternalInterface.call("Global.ForceTranslateString", "^LoginScreen!0x" + hex);
                    if (r != null && r !== undefined) {
                        s = String(r);
                    }
                } catch (err:Error) {
                }
                if (s == null || s == "" || s == "undefined" || s == "null" || s.charAt(0) == "^") {
                    s = String(t[1]);
                }
                texts[id] = s;
            }
            return texts[id];
        }

        private static function fill(s:String, a:String = "", b:String = "", c:String = ""):String {
            return s.split("{1}").join(a).split("{2}").join(b).split("{3}").join(c);
        }

        // -- the list file ----------------------------------------------------------------------------------
        // Lines of tab-separated fields (UTF-8):
        //   dawngate-servers  1
        //   preselect  <tag>              the server Start-Client was started for (may be empty)
        //   last       <tag>              the server a login went to last (may be empty)
        //   server     <tag> <name> <address> <gateway port> <status> <ping ms> [<key> <trusted key> [fixed]]
        //   end                           the list is whole (Start-Client may be writing it while it is read)
        // status: ok, new (online, its key not trusted yet), changed (another key than the trusted one),
        // unreachable, no_gateway (no Dawngate tunnel there), unknown; for This PC (fixed: the first line, shown in
        // the game's language, never removed) also local (starts at the login), setup (the ping field: the setup's
        // percentage), starting, failed. key: the fingerprint of the key the server showed, trusted: the one trusted
        // for it (both may be empty).

        private function load():void {
            if (loading) {
                return;
            }
            loading = true;
            loadedAt = getTimer();
            var l:URLLoader = new URLLoader();
            l.addEventListener(Event.COMPLETE, function (e:Event):void {
                loading = false;
                parseList(String(l.data));
            });
            l.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void {
                loading = false;
                if (listState == 0) {
                    listState = -1;
                }
            });
            try {
                l.load(new URLRequest(LIST_URL));
            } catch (err:Error) {
                loading = false;
                if (listState == 0) {
                    listState = -1;
                }
            }
        }

        private function parseList(text:String):void {
            var lines:Array = text.split("\r").join("").split("\n");
            if (lines.length == 0 || String(lines[0]).split("\t")[0] != LIST_MAGIC) {
                if (listState == 0) {
                    listState = -1;
                }
                return;
            }
            var got:Array = [];
            var pre:String = "";
            var last:String = "";
            var whole:Boolean = false;
            for (var i:int = 1; i < lines.length; i++) {
                var f:Array = String(lines[i]).split("\t");
                switch (f[0]) {
                    case "preselect": pre = f.length > 1 ? f[1] : ""; break;
                    case "last": last = f.length > 1 ? f[1] : ""; break;
                    case "end": whole = true; break;
                    case "server":
                        if (f.length >= 7) {
                            var fixed:Boolean = f.length > 9 && f[9] == "fixed";
                            got.push({tag: f[1], name: f[2], address: f[3], port: int(f[4]),
                                      status: f[5], ping: int(f[6]), key: f.length > 7 ? f[7] : "",
                                      trusted: f.length > 8 ? f[8] : "", pending: false, named: true, fixed: fixed});
                        }
                        break;
                }
            }
            if (listState == 1) {
                if (whole) {
                    merge(got);
                }
                return;
            }
            servers = got;
            preselect = pre;
            lastUsed = last;
            listState = 1;
            sel = find(preselect);
            if (sel < 0 && remembered != null) {
                sel = findRemembered(remembered);
            }
            if (sel < 0) {
                sel = find(lastUsed);
            }
            if (sel < 0 && servers.length > 0) {
                sel = 0;
            }
            if (hooked) {
                build();
            }
        }

        // merge takes a list read again: the stored servers' status from it, the servers added in the picker (not
        // stored yet) kept, the selection kept on the same server.
        private function merge(got:Array):void {
            var s:Object = current();
            for (var i:int = 0; i < servers.length; i++) {
                var p:Object = servers[i];
                if (!p.pending) {
                    continue;
                }
                var dup:Boolean = false;
                for (var j:int = 0; j < got.length; j++) {
                    if (String(got[j].address).toLowerCase() == String(p.address).toLowerCase()) {
                        dup = true;
                    }
                }
                if (!dup) {
                    got.push(p);
                }
            }
            servers = got;
            sel = -1;
            if (s != null) {
                sel = s.pending ? findAddress(s.address) : find(s.tag);
            }
            if (sel < 0 && servers.length > 0) {
                sel = 0;
            }
            refresh();
        }

        private function find(tag:String):int {
            if (tag == null || tag == "") {
                return -1;
            }
            for (var i:int = 0; i < servers.length; i++) {
                if (servers[i].tag == tag.toLowerCase()) {
                    return i;
                }
            }
            return -1;
        }

        private function findAddress(address:String):int {
            for (var i:int = 0; i < servers.length; i++) {
                if (String(servers[i].address).toLowerCase() == address.toLowerCase()) {
                    return i;
                }
            }
            return -1;
        }

        private function findRemembered(r:Object):int {
            return r.tag != null ? find(r.tag) : findAddress(r.address);
        }

        private function current():Object {
            return (sel >= 0 && sel < servers.length) ? servers[sel] : null;
        }

        // -- the hook on the login panel ---------------------------------------------------------------------

        private function onFrame(e:Event):void {
            frames++;
            if (!hooked) {
                if (frames % 6 == 1) {
                    tryHook();
                }
                return;
            }
            if (saved != null && frames % 2 == 0) {
                restore(null); // a fold whose restore listener didn't run
            }
            takeRememberedSuffix();
            follow();
            if (listState == 1 && strip != null && strip.visible && getTimer() - loadedAt > RELOAD_MS) {
                load();
            }
        }

        private function tryHook():void {
            var r:Object = root;
            if (r == null) {
                return;
            }
            try {
                lp = r["mcMaster"]["loginPanelMC"] as DisplayObjectContainer;
            } catch (err:Error) {
                lp = null;
            }
            if (lp == null || lp["accountInput"] == null || lp["passwordInput"] == null || lp["btnLogin"] == null) {
                return;
            }
            acc = lp["accountInput"];
            pw = lp["passwordInput"];
            btn = lp["btnLogin"];
            // The game listens on the button and both fields at the target phase with priority 0: a capture-phase
            // listener on the panel runs before it, one at priority -1000 on each target after it.
            lp.addEventListener("buttonClick", foldBefore, true, 1000);
            lp.addEventListener("IF_Submit", foldBefore, true, 1000);
            btn.addEventListener("buttonClick", restore, false, -1000);
            acc.addEventListener("IF_Submit", restore, false, -1000);
            pw.addEventListener("IF_Submit", restore, false, -1000);
            hooked = true;
            if (stage != null) {
                // capture phase: before the focused control (the game's focus moves with the arrow keys too)
                stage.addEventListener(KeyboardEvent.KEY_DOWN, onKey, true, 100);
            }
            if (listState == 1) {
                build();
            }
        }

        private function foldBefore(e:Event):void {
            if (saved != null || listState != 1 || (e.target != btn && e.target != acc && e.target != pw)) {
                return;
            }
            var t:String = String(acc["text"]);
            if (t.length == 0 || t.indexOf("#") >= 0) {
                return; // nothing typed, or a server typed by hand
            }
            var s:Object = current();
            if (s == null) {
                return;
            }
            saved = t;
            acc["text"] = t + "#" + suffix(s);
            if (panel != null && panel.visible) {
                closePanel();
            }
        }

        private function suffix(s:Object):String {
            if (s.pending) {
                return "+" + s.address + (s.named ? "=" + s.name : "");
            }
            return s.tag;
        }

        private function restore(e:Event):void {
            if (saved == null) {
                return;
            }
            acc["text"] = saved;
            saved = null;
        }

        // A remembered login name is saved as the game's handler got it (name#suffix): take the suffix off the
        // field and preselect that server, during the screen's first seconds and while the field has no focus.
        private function takeRememberedSuffix():void {
            if (stripFrames <= 0 || saved != null) {
                return;
            }
            stripFrames--;
            var t:String = String(acc["text"]);
            var i:int = t.indexOf("#");
            if (i < 0 || t == stripped || hasFocus(acc)) {
                return;
            }
            var name:String = t.substring(0, i);
            var rest:String = t.substring(i + 1);
            acc["text"] = name;
            stripped = name;
            var r:Object = null;
            if (rest.charAt(0) == "+") {
                var a:String = rest.substring(1);
                var eq:int = a.indexOf("=");
                r = {tag: null, address: eq >= 0 ? a.substring(0, eq) : a};
            } else if (rest.charAt(0) != "-" && rest.length > 0) {
                r = {tag: rest.toLowerCase(), address: null};
            }
            if (r == null) {
                return;
            }
            remembered = r;
            if (listState == 1 && find(preselect) < 0) {
                var k:int = findRemembered(r);
                if (k >= 0) {
                    select(k);
                }
            }
        }

        private function hasFocus(o:Object):Boolean {
            var f:DisplayObject = stage != null ? stage.focus : null;
            if (f == null) {
                return false;
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            return f == o || (c != null && c.contains(f));
        }

        // -- building the controls ---------------------------------------------------------------------------

        private function text(size:int, color:uint, font:String = "$Body"):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.defaultTextFormat = new TextFormat(font, size, color);
            t.height = size + 10;
            return t;
        }

        private function component(name:String):Object {
            try {
                var c:Class = getDefinitionByName(name) as Class;
                return c != null ? new c() : null;
            } catch (err:Error) {
            }
            return null;
        }

        // A skinned button of the movie (TaskButton2, or FE_Library's Button_1), else a drawn one.
        private function button(label:String, w:Number, h:Number, onClick:Function, skin:String = "TaskButton2"):Object {
            var b:Object = component(skin);
            if (b is DisplayObject) {
                b["label"] = label;
                b["width"] = w;
                b["height"] = h;
                b.addEventListener("buttonClick", function (e:Event):void {
                    onClick();
                });
                return b;
            }
            var s:Sprite = new Sprite();
            s.graphics.lineStyle(1, GOLD);
            s.graphics.beginFill(0x1A1A1A);
            s.graphics.drawRect(0, 0, w, h);
            s.graphics.endFill();
            var t:TextField = text(12, TEXT, "$Header");
            t.width = w;
            t.y = (h - 22) / 2;
            t.text = label;
            t.x = Math.max(0, (w - t.textWidth) / 2 - 2);
            s.addChild(t);
            s.buttonMode = true;
            s.addEventListener(MouseEvent.CLICK, function (e:Event):void {
                onClick();
            });
            return s;
        }

        private function build():void {
            if (strip != null) {
                refresh();
                return;
            }
            visible = true;
            W = lp.getBounds(lp).width > 100 ? lp.width : 460;

            strip = new Sprite();
            addChild(strip);
            var label:TextField = text(14, GOLD, "$Header");
            label.text = tr(TX_SERVER);
            label.width = 80;
            label.y = 6;
            strip.addChild(label);
            selector = button("", W - 84, 30, togglePanel, "Button_1");
            selector.x = 84;
            strip.addChild(DisplayObject(selector));
            if (selector is InteractiveObject) {
                InteractiveObject(selector).tabIndex = 0; // first: the game's own run from 3 (check boxes, LOGIN 4)
            }
            selDot = new Sprite();
            selDot.x = 84 + 14;
            selDot.y = 15;
            selDot.mouseEnabled = false;
            strip.addChild(selDot);
            selName = text(14, TEXT);
            selName.x = 84 + 26;
            selName.y = 4;
            selName.width = W - 84 - 60 - 130;
            strip.addChild(selName);
            selStatus = text(12, DIM);
            selStatus.x = W - 30 - 130;
            selStatus.y = 6;
            selStatus.width = 126;
            var right:TextFormat = selStatus.defaultTextFormat;
            right.align = TextFormatAlign.RIGHT;
            selStatus.defaultTextFormat = right;
            strip.addChild(selStatus);
            var arrow:Sprite = new Sprite();
            arrow.graphics.beginFill(GOLD);
            arrow.graphics.moveTo(0, 0);
            arrow.graphics.lineTo(10, 0);
            arrow.graphics.lineTo(5, 6);
            arrow.graphics.lineTo(0, 0);
            arrow.graphics.endFill();
            arrow.x = W - 22;
            arrow.y = 12;
            arrow.mouseEnabled = false;
            strip.addChild(arrow);

            panel = new Sprite();
            panel.visible = false;
            addChild(panel);
            title = text(14, GOLD, "$Header");
            title.text = tr(TX_SELECT);
            title.x = 14;
            title.y = 8;
            title.width = W - 28;
            panel.addChild(title);
            rowBox = new Sprite();
            rowBox.x = 10;
            rowBox.y = 38;
            panel.addChild(rowBox);
            for (var i:int = 0; i < ROWS; i++) {
                rows.push(makeRow(i));
            }
            emptyText = text(13, DIM);
            emptyText.x = 14;
            emptyText.y = 46;
            emptyText.width = W - 28;
            emptyText.text = tr(TX_EMPTY);
            panel.addChild(emptyText);
            rowBox.addEventListener(MouseEvent.MOUSE_WHEEL, function (e:MouseEvent):void {
                scroll(e.delta > 0 ? -1 : 1);
            });
            detail = text(12, TEXT);
            detail.x = 14;
            detail.y = 38 + ROWS * ROW_H + 2;
            detail.width = W - 28;
            detail.height = DETAIL_H;
            detail.multiline = true;
            detail.wordWrap = true;
            panel.addChild(detail);

            listButtons = new Sprite();
            listButtons.y = 38 + ROWS * ROW_H + DETAIL_H + 10;
            panel.addChild(listButtons);
            var bw:Number = (W - 20 - 2 * 10) / 3;
            var add:Object = button(tr(TX_ADD_SERVER), bw, 28, openAdd);
            add.x = 10;
            listButtons.addChild(DisplayObject(add));
            removeBtn = button(tr(TX_REMOVE), bw, 28, removeSelected);
            removeBtn.x = 10 + bw + 10;
            listButtons.addChild(DisplayObject(removeBtn));
            var done:Object = button(tr(TX_DONE), bw, 28, closePanel);
            done.x = 10 + 2 * (bw + 10);
            listButtons.addChild(DisplayObject(done));
            tabOrder(add, 70);
            tabOrder(removeBtn, 71);
            tabOrder(done, 72);

            addBox = new Sprite();
            addBox.visible = false;
            addBox.y = 38;
            panel.addChild(addBox);
            var l1:TextField = text(13, TEXT, "$Header");
            l1.text = tr(TX_ADDRESS);
            l1.x = 14;
            l1.width = W - 28;
            addBox.addChild(l1);
            addrInput = component("_TextInput");
            if (addrInput is DisplayObject) {
                addrInput.x = 14;
                addrInput.y = 24;
                addrInput["width"] = W - 28;
                addrInput.addEventListener("IF_Submit", function (e:Event):void {
                    confirmAdd();
                });
                addBox.addChild(DisplayObject(addrInput));
            }
            var l2:TextField = text(13, TEXT, "$Header");
            l2.text = tr(TX_NAME);
            l2.x = 14;
            l2.y = 72;
            l2.width = W - 28;
            addBox.addChild(l2);
            nameInput = component("_TextInput");
            if (nameInput is DisplayObject) {
                nameInput.x = 14;
                nameInput.y = 96;
                nameInput["width"] = W - 28;
                nameInput.addEventListener("IF_Submit", function (e:Event):void {
                    confirmAdd();
                });
                addBox.addChild(DisplayObject(nameInput));
            }
            addHint = text(12, DIM);
            addHint.x = 14;
            addHint.y = 140;
            addHint.width = W - 28;
            addHint.height = 40;
            addHint.multiline = true;
            addHint.wordWrap = true;
            addHint.text = tr(TX_ADD_HINT);
            addBox.addChild(addHint);
            var ok:Object = button(tr(TX_ADD), bw, 28, confirmAdd);
            ok.x = 10;
            ok.y = ROWS * ROW_H + DETAIL_H + 10;
            addBox.addChild(DisplayObject(ok));
            var cancel:Object = button(tr(TX_CANCEL), bw, 28, closeAdd);
            cancel.x = 10 + 2 * (bw + 10);
            cancel.y = ROWS * ROW_H + DETAIL_H + 10;
            addBox.addChild(DisplayObject(cancel));
            tabOrder(addrInput, 60);
            tabOrder(nameInput, 61);
            tabOrder(ok, 62);
            tabOrder(cancel, 63);

            // An opaque background (the login panel's text showed through 0.97), drawn on a child below the rest.
            var h:Number = 38 + ROWS * ROW_H + DETAIL_H + 10 + 28 + 12;
            var bg:Sprite = new Sprite();
            bg.graphics.lineStyle(1, GOLD, 1);
            bg.graphics.beginFill(0x0C0B0A, 1);
            bg.graphics.drawRect(0, 0, W, h);
            bg.graphics.endFill();
            bg.mouseEnabled = true; // takes the clicks meant for the login panel below
            panel.addChildAt(bg, 0);
            refresh();
            follow();
            tabbable(false);
        }

        // tabOrder gives one of the panel's controls a tab index; the panel's controls take the focus by Tab only
        // while the panel shows (tabbable), so Tab on the login panel keeps to the game's own order.
        private function tabOrder(o:Object, index:int):void {
            if (o is InteractiveObject) {
                InteractiveObject(o).tabIndex = index;
                tabbed.push(o);
            }
        }

        private function tabbable(on:Boolean):void {
            for (var i:int = 0; i < tabbed.length; i++) {
                var o:InteractiveObject = tabbed[i] as InteractiveObject;
                var shown:Boolean = on && isShown(o);
                o.tabEnabled = shown;
                if (o is DisplayObjectContainer) {
                    DisplayObjectContainer(o).tabChildren = shown;
                }
            }
        }

        private function makeRow(i:int):Object {
            var r:Object = {};
            r.box = new Sprite();
            r.box.y = i * ROW_H;
            rowBox.addChild(r.box);
            r.btn = button("", W - 20, ROW_H - 2, function ():void {
                select(top + i);
            }, "Button_1");
            tabOrder(r.btn, 40 + i);
            r.box.addChild(DisplayObject(r.btn));
            r.mark = new Sprite();
            r.mark.mouseEnabled = false;
            r.box.addChild(r.mark);
            r.dot = new Sprite();
            r.dot.x = 14;
            r.dot.y = (ROW_H - 2) / 2;
            r.dot.mouseEnabled = false;
            r.box.addChild(r.dot);
            r.name = text(13, TEXT);
            r.name.x = 26;
            r.name.y = 5;
            r.name.width = (W - 20) * 0.38;
            r.box.addChild(r.name);
            r.address = text(12, DIM);
            r.address.x = 26 + (W - 20) * 0.38;
            r.address.y = 6;
            r.address.width = (W - 20) * 0.32;
            r.box.addChild(r.address);
            r.status = text(12, DIM);
            r.status.x = 26 + (W - 20) * 0.70;
            r.status.y = 6;
            r.status.width = (W - 20) * 0.30 - 30;
            r.box.addChild(r.status);
            return r;
        }

        // -- showing ------------------------------------------------------------------------------------------

        // nameOf is a server's name as shown: This PC (the list's fixed first entry) in the game's language.
        private function nameOf(s:Object):String {
            return s.fixed ? tr(TX_THIS_PC) : String(s.name);
        }

        // statusOf is a server's status word, its colour and the sentence under the list.
        private function statusOf(s:Object):Array {
            if (s.pending) {
                return [tr(TX_ST_PENDING), AMBER, tr(TX_ADD_HINT)];
            }
            var ping:String = String(s.ping);
            switch (s.status) {
                case "ok": return [fill(tr(TX_ST_OK), ping), GREEN, fill(tr(TX_D_OK), ping, s.key, s.trusted)];
                case "new": return [tr(TX_ST_NEW), GREEN, fill(tr(TX_D_NEW), ping, s.key, s.trusted)];
                case "changed": return [tr(TX_ST_CHANGED), RED, fill(tr(TX_D_CHANGED), ping, s.key, s.trusted)];
                case "unreachable": return [tr(TX_ST_OFFLINE), RED, tr(TX_D_OFFLINE)];
                case "no_gateway": return [tr(TX_ST_NO_TUNNEL), RED, tr(TX_D_NO_TUNNEL)];
                case "local": return [tr(TX_ST_LOCAL), GREEN, tr(TX_D_LOCAL)];
                case "setup": return [fill(tr(TX_ST_SETUP), ping), AMBER, fill(tr(TX_D_SETUP), ping)];
                case "starting": return [tr(TX_ST_STARTING), AMBER, tr(TX_D_STARTING)];
                case "failed": return [tr(TX_ST_FAILED), RED, tr(TX_D_FAILED)];
            }
            return [tr(TX_ST_UNKNOWN), GREY, tr(TX_D_UNKNOWN)];
        }

        private static function dot(sp:Sprite, color:uint):void {
            sp.graphics.clear();
            sp.graphics.beginFill(color);
            sp.graphics.drawCircle(0, 0, 4.5);
            sp.graphics.endFill();
        }

        private function refresh():void {
            if (strip == null) {
                return;
            }
            var s:Object = current();
            if (s != null) {
                var cur:Array = statusOf(s);
                selName.text = nameOf(s);
                dot(selDot, cur[1]);
                selDot.visible = true;
                selStatus.text = cur[0];
                selStatus.textColor = cur[1];
                detail.text = cur[2];
                detail.textColor = cur[1] == RED ? RED : TEXT;
            } else {
                selName.text = servers.length == 0 ? tr(TX_NONE) : tr(TX_PICK);
                selDot.visible = false;
                selStatus.text = "";
                detail.text = "";
            }
            detail.visible = !addBox.visible;
            if (sel >= 0 && sel < top) {
                top = sel;
            }
            if (sel >= top + ROWS) {
                top = sel - ROWS + 1;
            }
            top = Math.max(0, Math.min(top, servers.length - ROWS));
            for (var i:int = 0; i < ROWS; i++) {
                var r:Object = rows[i];
                var k:int = top + i;
                r.box.visible = k < servers.length;
                if (k >= servers.length) {
                    continue;
                }
                var v:Object = servers[k];
                var st:Array = statusOf(v);
                r.name.text = nameOf(v);
                r.address.text = v.address;
                r.status.text = st[0];
                r.status.textColor = st[1];
                dot(r.dot, st[1]);
                r.mark.graphics.clear();
                if (k == sel) {
                    r.mark.graphics.lineStyle(2, GOLD);
                    r.mark.graphics.drawRect(1, 1, W - 22, ROW_H - 4);
                }
            }
            emptyText.visible = servers.length == 0 && !addBox.visible;
            if (removeBtn != null) {
                removeBtn.visible = s != null && !s.fixed; // This PC is always in the list
            }
            tabbable(panel.visible);
        }

        // Places the picker above the login panel (wherever the panel is) and shows it while the panel shows.
        private function follow():void {
            if (strip == null) {
                return;
            }
            var shown:Boolean = isShown(lp);
            strip.visible = shown;
            if (!shown) {
                if (panel.visible) {
                    panel.visible = false;
                    tabbable(false);
                }
                return;
            }
            var b:Rectangle = lp.getBounds(this);
            strip.x = b.x;
            strip.y = b.y - 40;
            panel.x = b.x;
            panel.y = b.y - 4;
        }

        private static function isShown(d:DisplayObject):Boolean {
            for (; d != null; d = d.parent) {
                if (!d.visible || d.alpha < 0.05) {
                    return false;
                }
            }
            return true;
        }

        // -- acting -------------------------------------------------------------------------------------------

        private function select(k:int):void {
            if (k < 0 || k >= servers.length) {
                return;
            }
            sel = k;
            refresh();
        }

        private function scroll(d:int):void {
            top = Math.max(0, Math.min(top + d, servers.length - ROWS));
            refresh();
        }

        private function togglePanel():void {
            if (panel.visible) {
                closePanel();
            } else {
                panel.visible = true;
                closeAdd();
            }
        }

        private function closePanel():void {
            panel.visible = false;
            addBox.visible = false;
            tabbable(false);
        }

        private function openAdd():void {
            addBox.visible = true;
            title.text = tr(TX_ADD_SERVER);
            rowBox.visible = false;
            listButtons.visible = false;
            addHint.textColor = DIM;
            addHint.text = tr(TX_ADD_HINT);
            if (addrInput != null) {
                addrInput["text"] = "";
            }
            if (nameInput != null) {
                nameInput["text"] = "";
            }
            refresh();
            if (stage != null && addrInput is InteractiveObject) {
                try {
                    var tf:InteractiveObject = addrInput.hasOwnProperty("textField") ? addrInput["textField"] as InteractiveObject : null;
                    stage.focus = tf != null ? tf : InteractiveObject(addrInput);
                } catch (err:Error) {
                }
            }
        }

        private function closeAdd():void {
            addBox.visible = false;
            title.text = tr(TX_SELECT);
            rowBox.visible = true;
            listButtons.visible = true;
            refresh();
        }

        private function confirmAdd():void {
            if (!addBox.visible) {
                return;
            }
            var address:String = trim(addrInput != null ? String(addrInput["text"]) : "");
            var name:String = clean(nameInput != null ? String(nameInput["text"]) : "");
            if (!validAddress(address)) {
                addHint.textColor = RED;
                addHint.text = tr(TX_BAD_ADDRESS);
                return;
            }
            var k:int = findAddress(address);
            if (k < 0) {
                servers.push({tag: "", name: name != "" ? name : address, address: address, port: 0, status: "",
                              ping: 0, pending: true, named: name != ""});
                k = servers.length - 1;
            }
            closeAdd();
            select(k);
            closePanel();
        }

        // Remove: a pending server goes from the list at once; a stored one by a login as <name>#-<tag>, which
        // Start-Client answers by removing it (and refusing the login).
        private function removeSelected():void {
            var s:Object = current();
            if (s == null || s.fixed) {
                return;
            }
            if (!s.pending && saved == null) {
                var name:String = String(acc["text"]);
                if (name.indexOf("#") >= 0) {
                    name = name.substring(0, name.indexOf("#"));
                }
                saved = String(acc["text"]);
                acc["text"] = (name != "" ? name : "player") + "#-" + s.tag;
                try {
                    root["OnLoginClicked"](null);
                } catch (err:Error) {
                }
                acc["text"] = saved;
                saved = null;
            }
            servers.splice(sel, 1);
            if (sel >= servers.length) {
                sel = servers.length - 1;
            }
            closePanel();
            refresh();
        }

        private function onKey(e:KeyboardEvent):void {
            if (panel == null || !panel.visible) {
                return;
            }
            if (addBox.visible) {
                if (e.keyCode == 27) {
                    closeAdd();
                    e.stopImmediatePropagation();
                }
                return;
            }
            switch (e.keyCode) {
                case 38: select(Math.max(0, sel - 1)); e.stopImmediatePropagation(); break;   // up
                case 40: select(Math.min(servers.length - 1, sel + 1)); e.stopImmediatePropagation(); break; // down
                case 27: closePanel(); e.stopImmediatePropagation(); break;                    // escape
                case 46: removeSelected(); break;               // delete
                case 13: if (!hasFocus(selector)) { closePanel(); } break; // enter
            }
        }

        private static function trim(s:String):String {
            return s.replace(/^\s+|\s+$/g, "");
        }

        private static function clean(s:String):String {
            s = trim(s.replace(/[#\t\r\n]/g, " "));
            return s.length > 64 ? s.substring(0, 64) : s;
        }

        // An IPv4 address or a host name (letters, digits, '-', '.'), as Start-Client takes them.
        private static function validAddress(a:String):Boolean {
            return a.length > 0 && a.length <= 253 && /^[A-Za-z0-9.\-]+$/.test(a) && a.charAt(0) != "." &&
                   a.charAt(a.length - 1) != "." && a.indexOf("..") < 0 && a.charAt(0) != "-";
        }
    }
}
