// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The custom game lobby's ready check. In a custom game's lobby every human on the two teams except the host gets a
// READY toggle at START's place (a second instance of START's own button class, its own label field written); every
// tile of a player whose client has this class shows whether that player is ready (the Collection's check box,
// MSCCheckBox001 from the shared library, clipped to its box, and "READY" / "NOT READY" before the kick button); the
// host's START stays disabled with "n/m ready" above it until every such player is ready. Spectators never need to be,
// the host is ready by starting, and a lobby with only the host starts at once.
//
// The class sits on the root timeline of the front end's movie (UIRuntime.gfx) beside the Collection's class, placed
// there by four added tags; the game's bytecode stays as it is, and the class uses only public members of the game's
// classes, by name. Its clip is on no stage, so it keeps a static strong reference to itself and a strong ENTER_FRAME
// listener. The lobby is the view of the component "customMatchLobby" (GComponent.find, the id in the game's screen
// manifest) while it is shown; the roster comes from the lobby's own natives (comp.viewController.nativeBridge), whose
// arguments must be Scaleform UInts (u()).
//
// The channel is the lobby's game chat. The class sends "~#rdy hi~" once per lobby entry and "~#rdy 1~" / "~#rdy 0~"
// from the toggle (ChatManager.SendMessageToGame, the lobby input's own call); the server never relays them and answers
// only clients that said hi, with lines in the name of the player whose state it is ("~#rdy 1~", "~#rdy 0~"; "~#rdy
// wait~" to the host after a refused start). The chat writes them into the lobby window's text field
// (customGameChatWindow.m_chatText) as a paragraph with the sender's link "event:p,<persona id>" and "[<name>]:"; the
// class reads every such paragraph, takes it out of the field and scrolls back to the end, every frame and again right
// after the view's drawing. A lobby line is "~#<kind> <args>~": onLine dispatches by kind (KINDS), so another kind
// is one more case there and one more part per tile (tile()). Without the server's answer (an older server) the class
// shows and holds nothing.
//
// The tiles' ratings: the server also sends "~#rat <n>~" in the name of each player whose rating players see (the
// server's show_rating switch and the Leaderboard's rated games decide; nothing with the switch off), at the hi and
// after every roster change. Each team tile of such a player shows "Rating <n>" (the profile box's word) after its
// wins, in the wins label's own format (EA's Label009: its font, size and grey), on the same line; spectators' tiles
// have no wins row (EA's spectator layout), so no rating either.
//
// Listeners on the game's objects (the late pass on the view's displayContainer) exist only while the lobby is on the
// stage: a listener left behind its screen crashes the client's quit.
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.geom.Rectangle;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;
    import flash.utils.getTimer;

    public class LobbyReady extends MovieClip {
        private static const COMPONENT_CLASS:String = "com.gravity.framework.component.GComponent";
        private static const CHAT_CLASS:String = "maxis.chat.manager.ChatManager";
        private static const COMPONENT_ID:String = "customMatchLobby";
        private static const VIEW_CLASS:String = "CustomGameLobbyView";
        private static const CHECK_CLASS:String = "MSCCheckBox001";
        // the lobby line kinds the class reads (each "~#<kind> ...~" paragraph is taken out of the chat)
        private static const KINDS:Array = ["rdy", "rat"];
        private static const HELLO_DELAY_MS:int = 3000;
        // the texts (English: the game ships en-us only, and the class needs no locale line of its own)
        private static const BTN_READY:String = "ready";          // the button's own field draws small capitals
        private static const BTN_NOT_READY:String = "not ready";
        private static const MARK_READY:String = "READY";
        private static const MARK_NOT_READY:String = "NOT READY";
        private static const WAIT_TEXT:String = "Not all ready";
        private static const RATING_LABEL:String = "Rating "; // as the profile box's "Rating 1532"
        private static const RATING_GAP:int = 14;              // after the wins, in the wins label's own units
        // the colours: the lobby's own (a ready mark green as the client's online state, the rest the tile's wins grey)
        private static const READY_COLOR:uint = 0x7ED957;
        private static const DIM_COLOR:uint = 0xA9A9A9;
        private static const WAIT_COLOR:uint = 0xC24E4E;   // the Spirit team's title red
        private static const WAIT_MS:int = 4000;
        // the count's field: right-aligned, ending this far left of START (the footer's own invite and balance icons
        // lie further left; above START is the other team's Join button)
        private static const COUNT_WIDTH:int = 120;
        private static const COUNT_GAP:int = 10;
        private static const MAX_ASSERTS:int = 3;
        private static var self:LobbyReady = null; // a strong reference besides the timeline's

        private var tick:int = 0;
        private var asserts:int = 0;
        private var view:Object = null;
        private var nb:Object = null;
        private var box:DisplayObjectContainer = null; // the view's displayContainer
        private var lateBox:DisplayObjectContainer = null;
        private var overlay:Sprite = null;
        private var enteredAt:int = 0;
        private var helloSent:Boolean = false;

        private var slots:Array = [];    // {t, s, name, owner, local}
        private var myName:String = "";
        private var myTeam:int = -1;
        private var isAdmin:Boolean = false;
        private var state:Object = {};   // a player's name -> "1" / "0", as the server said
        private var ratings:Object = {}; // a player's name -> the rating to show ("1532"), as the server said
        private var waitUntil:int = 0;   // the host's hint after a refused start shows until then

        private var readyBtn:Object = null;
        private var tiles:Object = {};   // "<team>_<slot>" -> Tile parts {check, text}
        private var countTf:TextField = null;
        private var held:Boolean = false;

        public function LobbyReady() {
            self = this;
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
        }

        // an error once per message, as an Assert line (a handful at most: the log thins repeated ones anyway)
        private var said:Object = {};

        private function say(s:String):void {
            if (said[s] || asserts >= MAX_ASSERTS) {
                return;
            }
            said[s] = true;
            asserts++;
            try {
                ExternalInterface.call("Assert", "LobbyReady: " + s);
            } catch (e:Error) {
            }
        }

        private function onFrame(e:Event):void {
            tick++;
            try {
                step();
            } catch (err:Error) {
                say("step: " + err.message);
            }
        }

        // a native's arguments must be Scaleform UInts (CustomMatchLobbyUI.cpp asserts "params.pArgs[0].IsUInt()"): a
        // literal or an incremented loop variable arrives as an int and the call answers false
        private static function u(i:uint):uint {
            var x:uint = i;
            return x;
        }

        private function lobbyView():Object {
            try {
                var comp:Object = getDefinitionByName(COMPONENT_CLASS).find(COMPONENT_ID);
                if (comp == null) {
                    return null;
                }
                var v:Object = comp.view;
                if (v == null || getQualifiedClassName(v).indexOf(VIEW_CLASS) < 0) {
                    return null;
                }
                nb = null;
                try {
                    nb = comp.viewController.nativeBridge;
                } catch (e1:Error) {
                }
                if (nb == null) {
                    nb = comp.nativeBridge;
                }
                return v;
            } catch (err:Error) {
            }
            return null;
        }

        private function step():void {
            var v:Object = (view != null || tick % 5 == 0) ? lobbyView() : null;
            var shown:Boolean = false;
            try {
                shown = v != null && v.isShown && v.displayContainer != null && v.displayContainer.stage != null;
            } catch (err:Error) {
            }
            if (!shown) {
                // the chat manager only while the lobby shows: GetChatManager at the front end's start (before the
                // chat is set up) logged "Scaleform: Error: null" and crashed a second client at its boot
                if (view != null) {
                    leave();
                }
                return;
            }
            if (v != view) {
                if (view != null) {
                    leave();
                }
                enter(v);
            }
            strip();
            if (tick % 10 == 0 || slots.length == 0) {
                roster();
            }
            if (!helloSent && myName != "" && getTimer() - enteredAt > HELLO_DELAY_MS) {
                helloSent = true;
                send("hi");
            }
            if (overlay.parent != box || box.getChildIndex(overlay) != box.numChildren - 1) {
                box.addChild(overlay);
            }
            part("button", drawButton);
            part("tiles", drawTiles);
            part("start", drawStart);
        }

        private function part(name:String, f:Function):void {
            try {
                f();
            } catch (err:Error) {
                say(name + ": " + err.message);
            }
        }

        private function enter(v:Object):void {
            view = v;
            box = v.displayContainer as DisplayObjectContainer;
            enteredAt = getTimer();
            helloSent = false;
            state = {};
            ratings = {};
            waitUntil = 0;
            slots = [];
            readyBtn = null;
            tiles = {};
            held = false;
            overlay = new Sprite();
            overlay.mouseEnabled = false;
            overlay.mouseChildren = true;
            countTf = text(16, DIM_COLOR);
            countTf.defaultTextFormat = new TextFormat("$Body", 16, DIM_COLOR, null, null, null, null, null, "right");
            countTf.width = COUNT_WIDTH;
            countTf.height = 24;
            countTf.visible = false;
            overlay.addChild(countTf);
            box.addChild(overlay);
            // the late pass: right after the view's own drawing (BaseView's ENTER_FRAME on the same container); off
            // again when the container leaves the stage
            lateBox = box;
            box.addEventListener(Event.ENTER_FRAME, onLate, false, -1000, false);
            box.addEventListener(Event.REMOVED_FROM_STAGE, onBoxGone, false, 0, false);
        }

        private function dropLate():void {
            if (lateBox != null) {
                lateBox.removeEventListener(Event.ENTER_FRAME, onLate);
                lateBox.removeEventListener(Event.REMOVED_FROM_STAGE, onBoxGone);
                lateBox = null;
            }
        }

        private function onBoxGone(e:Event):void {
            if (e.target == lateBox) {
                leave();
            }
        }

        // the lobby closed (left, or the match started): everything of ours goes, the state resets
        private function leave():void {
            dropLate();
            if (readyBtn != null) {
                readyBtn.removeEventListener(MouseEvent.CLICK, onReadyClick);
            }
            if (overlay != null && overlay.parent != null) {
                overlay.parent.removeChild(overlay);
            }
            overlay = null;
            readyBtn = null;
            tiles = {};
            view = null;
            box = null;
            nb = null;
            slots = [];
            state = {};
            ratings = {};
            held = false;
        }

        private function onLate(e:Event):void {
            try {
                if (view == null || box == null || box.stage == null) {
                    return;
                }
                strip();
                holdStart();
                label();
            } catch (err:Error) {
            }
        }

        private static function text(size:int, color:uint):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = false;
            t.wordWrap = false;
            t.defaultTextFormat = new TextFormat("$Body", size, color);
            return t;
        }

        private static function firstText(d:DisplayObject):TextField {
            if (d is TextField) {
                return d as TextField;
            }
            var c:DisplayObjectContainer = d as DisplayObjectContainer;
            if (c != null) {
                for (var i:int = 0; i < c.numChildren; i++) {
                    var t:TextField = firstText(c.getChildAt(i));
                    if (t != null) {
                        return t;
                    }
                }
            }
            return null;
        }

        // ---- the channel ----

        private function send(arg:String):void {
            try {
                getDefinitionByName(CHAT_CLASS).GetChatManager().SendMessageToGame("~#rdy " + arg + "~");
            } catch (err:Error) {
                say("send: " + err.message);
            }
        }

        private function chatField():TextField {
            try {
                var w:Object = getDefinitionByName(CHAT_CLASS).GetChatManager().customGameChatWindow;
                return w == null ? null : w.m_chatText as TextField;
            } catch (err:Error) {
            }
            return null;
        }

        // the paragraph's lobby line kind, or "" (no RegExp: Scaleform's split by one cuts at the wrong places)
        private static function kindOf(p:String):String {
            for each (var k:String in KINDS) {
                if (p.indexOf("~#" + k + " ") >= 0 || p.indexOf("~#" + k + "~") >= 0) {
                    return k;
                }
            }
            return "";
        }

        private static function unescapeHtml(s:String):String {
            return s.split("&lt;").join("<").split("&gt;").join(">").split("&quot;").join("\"").split("&apos;").join("'")
                .split("&#39;").join("'").split("&amp;").join("&");
        }

        // the paragraphs of an htmlText ("<P ...>...</P>", as Scaleform writes them; lower case too)
        private static function paragraphs(html:String):Array {
            var out:Array = [];
            var pos:int = 0;
            while (pos < html.length) {
                var a:int = html.indexOf("<P", pos);
                var a2:int = html.indexOf("<p", pos);
                if (a < 0 || (a2 >= 0 && a2 < a)) {
                    a = a2;
                }
                if (a < 0) {
                    break;
                }
                var e:int = html.indexOf("</P>", a);
                var e2:int = html.indexOf("</p>", a);
                if (e < 0 || (e2 >= 0 && e2 < e)) {
                    e = e2;
                }
                if (e < 0) {
                    break;
                }
                if (a > pos) {
                    out.push(html.substring(pos, a)); // anything between paragraphs, kept as it is
                }
                out.push(html.substring(a, e + 4));
                pos = e + 4;
            }
            if (pos < html.length) {
                out.push(html.substring(pos));
            }
            return out;
        }

        // reads and removes the lobby lines from the chat field
        private function strip():void {
            var tf:TextField = chatField();
            if (tf == null || tf.text.indexOf("~#") < 0) {
                return;
            }
            var kept:Array = [];
            var found:Boolean = false;
            for each (var p:String in paragraphs(tf.htmlText)) {
                var k:String = kindOf(p);
                if (k == "") {
                    kept.push(p);
                    continue;
                }
                found = true;
                read(k, p);
            }
            if (!found) {
                return;
            }
            tf.htmlText = kept.join("");
            tf.scrollV = tf.maxScrollV;
            try {
                var w:Object = getDefinitionByName(CHAT_CLASS).GetChatManager().customGameChatWindow;
                if (w.m_scrollBar != null) {
                    w.m_scrollBar.setScrollProperties(tf.maxScrollV, 0, tf.maxScrollV, 2);
                    w.m_scrollBar.position = tf.scrollV;
                }
            } catch (err:Error) {
            }
        }

        // one lobby line's paragraph: <A HREF="event:p,<persona id>">[<name>]:</A> ~#<kind> <args>~
        private function read(kind:String, p:String):void {
            var id:String = "";
            var name:String = "";
            var at:int = p.indexOf("event:p,");
            if (at >= 0) {
                var i:int = at + 8;
                while (i < p.length && p.charAt(i) >= "0" && p.charAt(i) <= "9") {
                    i++;
                }
                id = p.substring(at + 8, i);
                var open:int = p.indexOf(">[", i);
                var close:int = open < 0 ? -1 : p.indexOf("]:", open);
                if (close > open) {
                    name = unescapeHtml(p.substring(open + 2, close));
                }
            }
            var m:int = p.indexOf("~#" + kind);
            var end:int = p.indexOf("~", m + 2);
            if (m < 0 || end < 0) {
                return;
            }
            var args:Array = [];
            for each (var a:String in unescapeHtml(p.substring(m + 2 + kind.length, end)).split(" ")) {
                if (a != "") {
                    args.push(a);
                }
            }
            if (name != "") {
                onLine(kind, args, id, name);
            }
        }

        // a lobby line from the server, in the name of the player it is about
        private function onLine(kind:String, args:Array, id:String, name:String):void {
            var arg:String = args.length > 0 ? String(args[0]) : "";
            switch (kind) {
                case "rdy":
                    if (arg == "1" || arg == "0") {
                        state[name] = arg;
                    } else if (arg == "wait" && name == myName) {
                        // a refused start: the server unlocked the roster (RosterLockedKey "0": the roster manager
                        // tells the view's rosterLocked) and sends the admin its extended data (the lobby's roster
                        // redraw: the join buttons and the kick X come back only with drawRoster); the same here,
                        // in case that redraw came before the unlock reached the view
                        waitUntil = getTimer() + WAIT_MS;
                        try {
                            view.invalidateIsRosterLocked();
                            view.invalidateRoster();
                        } catch (err:Error) {
                        }
                    }
                    break;
                case "rat":
                    if (digits(arg) && int(arg) > 0) {
                        ratings[name] = String(int(arg));
                    } else {
                        delete ratings[name];
                    }
                    break;
            }
        }

        private static function digits(s:String):Boolean {
            if (s == null || s.length == 0 || s.length > 9) {
                return false;
            }
            for (var i:int = 0; i < s.length; i++) {
                if (s.charAt(i) < "0" || s.charAt(i) > "9") {
                    return false;
                }
            }
            return true;
        }

        // ---- the roster (the lobby's natives) ----

        private function roster():void {
            var out:Array = [];
            var lt:int = -1;
            var ls:int = -1;
            try {
                lt = int(nb.GetLocalPlayerTeam());
                ls = int(nb.GetLocalPlayerTeamSlot());
                for (var t:uint = 0; t < 3; t++) {
                    for (var s:uint = 0; s < 5; s++) {
                        if (nb.IsSlotOccupied(u(t), u(s))) {
                            out.push({t: t, s: s, name: String(nb.GetSlotPlayerName(u(t), u(s))),
                                owner: Boolean(nb.IsSlotOwner(u(t), u(s))), local: t == lt && s == ls});
                        }
                    }
                }
            } catch (err:Error) {
                say("natives: " + err.message);
            }
            slots = out;
            myTeam = lt;
            myName = "";
            isAdmin = false;
            for each (var o:Object in out) {
                if (o.local) {
                    myName = o.name;
                    isAdmin = o.owner;
                }
            }
        }

        // the tiles that count: a player on a team, not the host, whose client has the class (the server said its state)
        private function counted():Array {
            var a:Array = [];
            for each (var o:Object in slots) {
                if (o.t < 2 && !o.owner && state[o.name] != null) {
                    a.push(o);
                }
            }
            return a;
        }

        private function readyCount(c:Array):int {
            var n:int = 0;
            for each (var o:Object in c) {
                if (state[o.name] == "1") {
                    n++;
                }
            }
            return n;
        }

        // ---- the guest's READY button: START's own class at START's place ----

        private function wantButton():Boolean {
            return !isAdmin && myName != "" && (myTeam == 0 || myTeam == 1) && state[myName] != null;
        }

        private function drawButton():void {
            if (!wantButton()) {
                if (readyBtn != null) {
                    readyBtn.visible = false;
                }
                return;
            }
            var sb:DisplayObject = view.startButton as DisplayObject;
            if (sb == null) {
                return;
            }
            if (readyBtn == null) {
                var cls:Class = Object(sb).constructor as Class;
                readyBtn = new cls();
                overlay.addChild(readyBtn as DisplayObject);
                readyBtn.addEventListener(MouseEvent.CLICK, onReadyClick, false, 0, false);
            }
            readyBtn.visible = true;
            var r:Rectangle = sb.getBounds(overlay);
            if (r.width > 0) {
                readyBtn.x = r.x;
                readyBtn.y = r.y;
                try {
                    readyBtn.setSize(sb.width, sb.height);
                } catch (err:Error) {
                    readyBtn.width = r.width;
                    readyBtn.height = r.height;
                }
            }
            if (sb.visible) {
                sb.visible = false; // EA's START (a guest's is never enabled) under ours
            }
            label();
        }

        // the button's own text field (the symbol draws its text there, not in CLIK's textField: label changes
        // nothing); its timeline puts the symbol's text back at a state change, so this runs every frame and late
        private function label():void {
            if (readyBtn == null || !readyBtn.visible) {
                return;
            }
            var want:String = state[myName] == "1" ? BTN_NOT_READY : BTN_READY;
            var tf:TextField = firstText(readyBtn as DisplayObject);
            if (tf != null && tf.text != want) {
                tf.text = want;
            }
        }

        private function onReadyClick(e:MouseEvent):void {
            try {
                if (wantButton()) {
                    send(state[myName] == "1" ? "0" : "1"); // the server's own copy sets the state
                }
            } catch (err:Error) {
                say("click: " + err.message);
            }
        }

        // ---- the tiles: a check box and READY / NOT READY on each counted tile, the rating after the wins ----

        // Tile parts for the tile in cell key: the ready mark (check, text) and the rating (rating; wins: EA's wins
        // field it follows, found again every 30 frames)
        private function tile(key:String):Object {
            var tl:Object = tiles[key];
            if (tl != null) {
                return tl;
            }
            tl = {check: null, text: null, rating: null, wins: null, winsAt: -1000, shown: ""};
            try {
                var ck:Object = new (getDefinitionByName(CHECK_CLASS) as Class)();
                ck.mouseEnabled = false;
                ck.mouseChildren = false;
                ck.label = "";
                ck.scrollRect = new Rectangle(0, 0, 26, 26); // the box only: unclipped, its menu bar covers the text
                overlay.addChild(ck as DisplayObject);
                tl.check = ck;
            } catch (err:Error) {
                say("check box: " + err.message);
            }
            tl.text = text(14, DIM_COLOR);
            tl.text.width = 110;
            tl.text.height = 22;
            overlay.addChild(tl.text);
            tiles[key] = tl;
            return tl;
        }

        private function drawTiles():void {
            var marked:Object = {};
            var rated:Object = {};
            for each (var o:Object in slots) {
                if (o.t >= 2) {
                    continue; // the spectators: no mark, no rating
                }
                var key:String = o.t + "_" + o.s;
                var cell:DisplayObject = view["box" + key] as DisplayObject;
                if (cell == null) {
                    continue;
                }
                if (!o.owner && state[o.name] != null) { // counted()
                    marked[key] = true;
                    drawTile(tile(key), cell.getBounds(overlay), state[o.name] == "1");
                }
                if (ratings[o.name] != null && drawRating(tile(key), cell, String(ratings[o.name]))) {
                    rated[key] = true;
                }
            }
            for (var k:String in tiles) {
                var tl:Object = tiles[k];
                if (!marked[k]) {
                    if (tl.check != null) {
                        tl.check.visible = false;
                    }
                    tl.text.visible = false;
                }
                if (!rated[k] && tl.rating != null) {
                    tl.rating.visible = false;
                }
            }
        }

        // the tile's wins field (EA's Label009 in the tile's lower row): the lowest visible text field in the cell
        // whose text is a number (the name is above it, the ready mark and the rating are ours, on the overlay)
        private static function winsField(cell:DisplayObject):TextField {
            var best:TextField = null;
            var bestY:Number = 0;
            var todo:Array = [cell];
            while (todo.length > 0) {
                var d:DisplayObject = todo.pop() as DisplayObject;
                if (d == null || !d.visible) {
                    continue;
                }
                var tf:TextField = d as TextField;
                if (tf != null) {
                    if (digits(tf.text)) {
                        var y:Number = tf.getBounds(cell).y;
                        if (best == null || y > bestY) {
                            best = tf;
                            bestY = y;
                        }
                    }
                    continue;
                }
                var c:DisplayObjectContainer = d as DisplayObjectContainer;
                if (c != null) {
                    for (var i:int = 0; i < c.numChildren; i++) {
                        todo.push(c.getChildAt(i));
                    }
                }
            }
            return best;
        }

        // "Rating <n>" after the wins, in the wins field's own format and scale, on its line; false when the tile shows
        // no wins (then no rating either)
        private function drawRating(tl:Object, cell:DisplayObject, value:String):Boolean {
            var w:TextField = tl.wins as TextField;
            if (w == null || w.stage == null || tick - tl.winsAt >= 30) {
                w = winsField(cell);
                tl.wins = w;
                tl.winsAt = tick;
            }
            if (w == null || w.stage == null) {
                return false;
            }
            var rt:TextField = tl.rating as TextField;
            if (rt == null) {
                rt = new TextField();
                rt.embedFonts = true;
                rt.selectable = false;
                rt.mouseEnabled = false;
                rt.multiline = false;
                rt.wordWrap = false;
                overlay.addChild(rt);
                tl.rating = rt;
            }
            var want:String = RATING_LABEL + value;
            var fmt:TextFormat = w.getTextFormat();
            fmt.color = w.textColor;
            var look:String = want + "|" + fmt.font + "|" + fmt.size + "|" + w.textColor;
            if (tl.shown != look) {
                tl.shown = look;
                rt.defaultTextFormat = fmt;
                rt.text = want;
                rt.setTextFormat(fmt);
            }
            // the wins field's scale from its own units to the overlay's (the layout scales the label to its row)
            var r:Rectangle = w.getBounds(overlay);
            var localH:Number = w.scaleY != 0 ? w.height / w.scaleY : w.height;
            var sc:Number = localH > 0 ? r.height / localH : 1;
            rt.scaleX = 1;
            rt.scaleY = 1;
            rt.width = rt.textWidth + 8;
            rt.height = localH;
            rt.scaleX = sc;
            rt.scaleY = sc;
            rt.x = r.x + (w.textWidth + 2 + RATING_GAP) * sc;
            rt.y = r.y;
            rt.visible = true;
            return true;
        }

        // at the tile's right, before its kick button (32 wide)
        private function drawTile(tl:Object, r:Rectangle, on:Boolean):void {
            var tf:TextField = tl.text as TextField;
            var want:String = on ? MARK_READY : MARK_NOT_READY;
            if (tf.text != want) {
                tf.text = want;
            }
            tf.textColor = on ? READY_COLOR : DIM_COLOR;
            tf.x = r.right - 36 - 100;
            tf.y = r.y + r.height / 2 - 10;
            tf.visible = true;
            if (tl.check != null) {
                if (tl.check.selected != on) {
                    tl.check.selected = on;
                }
                tl.check.x = r.right - 36 - 130;
                tl.check.y = r.y + r.height / 2 - 12;
                tl.check.visible = true;
            }
        }

        // ---- the host's START: held disabled until every counted player is ready, the count above it ----

        private function drawStart():void {
            if (!isAdmin) {
                countTf.visible = false;
                return;
            }
            var c:Array = counted();
            var n:int = readyCount(c);
            var sb:DisplayObject = view.startButton as DisplayObject;
            var waiting:Boolean = getTimer() < waitUntil && n < c.length;
            countTf.visible = c.length > 0 && sb != null && sb.visible;
            if (countTf.visible) {
                var want:String = waiting ? WAIT_TEXT : n + "/" + c.length + " ready";
                if (countTf.text != want) {
                    countTf.text = want;
                }
                countTf.textColor = waiting ? WAIT_COLOR : DIM_COLOR;
                var r:Rectangle = sb.getBounds(overlay);
                countTf.x = r.x - COUNT_GAP - COUNT_WIDTH; // left of START, before the footer's icons
                countTf.y = r.y + (r.height - 22) / 2;
            }
            holdStart();
        }

        // C++ sets startButtonEnabled ("a player on team 0 or 1") whenever the roster changes and the view redraws its
        // buttons, so the hold runs every frame and after the view's drawing; a disabled CLIK button sends no click.
        // EA's own state comes back through invalidateIsRosterLocked (its updateButtonState).
        private function holdStart():void {
            if (!isAdmin || view == null) {
                return;
            }
            var c:Array = counted();
            if (readyCount(c) < c.length) {
                if (view.startButton.enabled) {
                    view.startButton.enabled = false;
                }
                try {
                    if (view.startButtonProxy.enabled) {
                        view.startButtonProxy.enabled = false;
                    }
                } catch (err:Error) {
                }
                held = true;
            } else if (held) {
                held = false;
                view.invalidateIsRosterLocked();
            }
        }
    }
}
