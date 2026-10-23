// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// Opens the chat rooms the server marks as "open by default", once per login, as if the player had double-clicked
// them in the Social panel's Rooms tab.
//
// The setup places this class on the chat movie's root timeline with four added tags (the game's own bytecode stays
// as it is). The server marks a room by putting it in a category whose id is AUTO_CATEGORY_MIN or more; the game
// shows no categories, so the mark is invisible. The class reads the Rooms tab's list (the game fills it at the login,
// whether the Social panel was ever opened or not) and, for each marked room, calls the game's own
// ChatManager.JoinRoom(category, room) once: the same request and the same window as the player's double-click.
//
// The game opens a room window it joined from the UI expanded and with the input focus. The class turns that around
// on the next frame: it minimises the new window to its tab in the bottom bar, takes the focus off its text field and
// gives it back to what had it before, so a joined room never takes the typing. A window the player opens later
// works as before. The game also marks the tab of such a window as having news at once (its lit "notifySolid" look,
// before any line), so a later line couldn't change it; the class clears that mark while the window has no line yet,
// and the tab lights up (four blinks, then lit until opened) when the first real line arrives.
//
// Once per login: a room the player closes (the game leaves it) stays closed until the next login, and the Rooms
// tab's refresh doesn't join it again. A room the player opened first is left alone. Without marked rooms (a server
// that marks none, or doesn't know the mark) the class does nothing.
//
// It also keeps every chat window's lines when the window changes its layout. The game's ChatWindow switches between
// its frames "open_no_group" and "open_with_group" (the member list shown from two members on) with gotoAndStop,
// and each frame has its own text field, so the switch shows an empty window: the lines that came while the window
// was minimised, or all of them when a member list opens or closes. The class notices the new text field on the next
// frame and puts the old one's lines into it, scrolled to the end.
//
// A new line in a marked room plays the game's own notification sound (the one of its chat pop-ups, through
// Global.OnInteractEvent), so a player waiting in the queue hears the queue's news. Not for the player's own lines,
// nor for the lines that come with the join (the first QUIET_MS of a window), and at most once every RING_GAP_MS.
// The game's sound options (Mute All, the volumes) apply to it as to every other sound of the menus.
//
// Build: re/scripts/build_picker.py build chatrooms (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObject;
    import flash.display.InteractiveObject;
    import flash.display.MovieClip;
    import flash.events.Event;
    import flash.external.ExternalInterface;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    public class AutoRooms extends MovieClip {
        // A room in a category with this id or more opens by default (the server's blaze.AutoOpenCategoryMin).
        private static const AUTO_CATEGORY_MIN:Number = 1000;
        // The bottom bar's user id while nobody is logged in (BottomBarUI.reset; SetLocalUserData sets the real one).
        private static const LOGGED_OUT:String = "LOCALUSERID";
        private static const CHAT_TYPE_ROOM:int = 3; // ChatManager.CHAT_TYPE_ROOM
        private static const POLL_FRAMES:int = 10; // how often the Rooms tab's list is read
        private static const SETTLE_MS:int = 2000; // a room is joined once it has been listed this long
        private static const PENDING_MS:int = 30000; // a join whose window hasn't opened by then is forgotten

        private var user:String = LOGGED_OUT;
        private var asked:Object = {}; // room id -> true: joined by this class or opened by the player, this login
        private var seen:Object = {}; // room id -> getTimer() when it was first listed, this login
        private var pending:Object = {}; // room id -> getTimer() of this class's join, until its window opens
        private var settle:Array = []; // {w: the new window, focus: what had the focus before it opened}
        private var bar:Object = null; // the bottom bar's window row (root.bottomBar)
        private var frames:int = 0;
        private var kept:Array = []; // {w: a chat window on the bottom bar, text: its text field as last seen}
        private static const SCROLL_SPEED:Number = 2; // BaseChatWindow.SCROLL_SPEED (SetScrollExtents)
        private static const QUIET_MS:int = 3000; // how long after its join a room's tab mark is cleared (quietTab)
        // The sound of a new line: the game's interact event CHAT_NOTIFY_SYS_MESSAGE (maxis.constants.InteractEvents),
        // which the game plays as its Wwise event Play_UI_notification.
        private static const SOUND_EVENT:uint = 74;
        private static const RING_GAP_MS:int = 2000; // at most one sound in this time
        private var marked:Object = {}; // room id -> true: a marked room of the Rooms tab's list, this login
        private var lastRing:int = -RING_GAP_MS;

        public function AutoRooms() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame);
        }

        private function onFrame(e:Event):void {
            if (settle.length > 0) {
                settleWindows();
            }
            if (kept.length > 0) {
                keepLines();
            }
            var u:String = localUser();
            if (u != user) { // a login, a logout or another account: start over
                user = u;
                asked = {};
                seen = {};
                pending = {};
                marked = {};
            }
            if (user == LOGGED_OUT || ++frames < POLL_FRAMES) {
                return;
            }
            frames = 0;
            hookBar();
            joinMarked();
        }

        private function localUser():String {
            try {
                var id:Object = root != null ? root["GetLocalUserID"]() : null;
                if (id != null) {
                    return String(id);
                }
            } catch (err:Error) {
            }
            return LOGGED_OUT;
        }

        // hookBar listens for the windows the game adds to the bottom bar (BottomBarUI.CreateChatWindow).
        private function hookBar():void {
            if (bar != null) {
                return;
            }
            try {
                var b:Object = root["bottomBar"];
                if (b != null) {
                    b.addEventListener(Event.ADDED, onAdded);
                    bar = b;
                    var n:int = int(b["numChildren"]);
                    for (var i:int = 0; i < n; i++) {
                        keep(b.getChildAt(i)); // the windows opened before the hook
                    }
                }
            } catch (err:Error) {
                bar = null;
            }
        }

        // keep starts watching w's text field when w is a chat window (ChatWindowContainer) not watched yet.
        private function keep(w:Object):void {
            try {
                for each (var k:Object in kept) {
                    if (k.w == w) {
                        return;
                    }
                }
                var t:Object = w.GetChatWindow()["m_chatText"];
                if (t != null) {
                    kept.push({w: w, text: t, quiet: 0, since: getTimer(), len: int(t["length"])});
                }
            } catch (err:Error) {
            }
        }

        // keepLines puts a window's lines back when the game replaced its text field (a frame change, see above), and
        // forgets the windows that were closed.
        private function keepLines():void {
            var still:Array = [];
            for each (var k:Object in kept) {
                try {
                    if (k.w["parent"] == null) {
                        continue; // closed
                    }
                    still.push(k);
                    if (k.quiet != 0) {
                        unmark(k);
                    }
                    var cw:Object = k.w.GetChatWindow();
                    var t:Object = cw["m_chatText"];
                    if (t == null) {
                        continue;
                    }
                    if (t == k.text) {
                        newLines(k, t);
                        continue;
                    }
                    var old:Object = k.text;
                    k.text = t;
                    k.len = 0; // what the game put into the new field since the switch is new
                    newLines(k, t);
                    if (old == null || int(old["length"]) == 0) {
                        continue;
                    }
                    // a line the game appended to the new field in the same frame stays after the old ones
                    var html:String = String(old["htmlText"]);
                    if (int(t["length"]) > 0) {
                        html += String(t["htmlText"]);
                    }
                    t["htmlText"] = html;
                    t["scrollV"] = t["maxScrollV"];
                    var sb:Object = cw["m_scrollBar"];
                    if (sb != null) {
                        sb.setScrollProperties(t["maxScrollV"], 0, t["maxScrollV"], SCROLL_SPEED);
                        sb["position"] = t["scrollV"];
                    }
                    k.len = int(t["length"]); // the carried-over lines are not new
                } catch (err:Error) {
                }
            }
            kept = still;
        }

        // newLines looks at the text the game appended to k's field since the last frame.
        private function newLines(k:Object, t:Object):void {
            var n:int = int(t["length"]);
            if (n == int(k.len)) {
                return;
            }
            var added:String = n > int(k.len) ? String(t["text"]).substr(int(k.len)) : "";
            k.len = n;
            if (added.length > 0) {
                heard(k, added);
            }
        }

        // heard plays the sound for new lines in a marked room's window unless they are all the player's own (the game
        // writes a member's line as "[persona]: text", after "[channel] " where it shows the channel) or the window is
        // younger than QUIET_MS (the lines of the join).
        private function heard(k:Object, added:String):void {
            try {
                if (getTimer() - int(k.since) < QUIET_MS || !marked[String(k.w["m_id"])] ||
                        int(k.w.GetChatWindow().GetChatType()) != CHAT_TYPE_ROOM) {
                    return;
                }
            } catch (err:Error) {
                return;
            }
            var own:String = "[" + localPersona() + "]";
            for each (var line:String in added.split(/[\r\n]+/)) {
                if (line.length == 0) {
                    continue;
                }
                var cut:int = line.indexOf("] ");
                if (line.indexOf(own) == 0 || (cut > 0 && line.indexOf(own, cut + 2) == cut + 2)) {
                    continue; // the player's own line
                }
                ring();
                return;
            }
        }

        private function ring():void {
            var now:int = getTimer();
            if (now - lastRing < RING_GAP_MS) {
                return;
            }
            lastRing = now;
            try {
                ExternalInterface.call("Global.OnInteractEvent", SOUND_EVENT);
            } catch (err:Error) {
            }
        }

        private function localPersona():String {
            try {
                var p:Object = root != null ? root["GetLocalUserPersona"]() : null;
                if (p != null) {
                    return String(p);
                }
            } catch (err:Error) {
            }
            return "";
        }

        // joinMarked joins each marked room of the Rooms tab's list that this login hasn't joined yet.
        private function joinMarked():void {
            var rooms:Object = null;
            var cm:Object = null;
            try {
                var sm:Object = getDefinitionByName("maxis.social.manager.SocialManager");
                rooms = sm["GetSocialManager"]()["m_mainslider"]["m_roomPanel"].GetInnerElements();
                cm = getDefinitionByName("maxis.chat.manager.ChatManager")["GetChatManager"]();
            } catch (err:Error) {
                return;
            }
            if (rooms == null || cm == null) {
                return;
            }
            var now:int = getTimer();
            var n:int = int(rooms["length"]);
            for (var i:int = 0; i < n; i++) {
                var d:Object = null;
                try {
                    d = rooms[i].GetData();
                } catch (err:Error) {
                    d = null;
                }
                if (d == null) {
                    continue;
                }
                var id:String = String(d["m_roomID"]);
                var cat:String = String(d["m_categoryID"]);
                if (isMarked(cat) && id.length > 0) {
                    marked[id] = true; // its new lines ring (heard), also in a window the player opened
                }
                if (!isMarked(cat) || asked[id] || id.length == 0) {
                    continue;
                }
                if (hasWindow(id)) {
                    asked[id] = true; // the player opened it first
                    continue;
                }
                if (seen[id] == undefined) {
                    seen[id] = now;
                    continue;
                }
                if (now - int(seen[id]) < SETTLE_MS) {
                    continue; // the login sends the list twice; join once it has settled
                }
                asked[id] = true;
                pending[id] = now;
                try {
                    cm.JoinRoom(cat, id);
                } catch (err:Error) {
                    delete pending[id];
                }
            }
            var old:Array = [];
            for (var p:String in pending) {
                if (now - int(pending[p]) > PENDING_MS) {
                    old.push(p); // the server refused the join: a later window is the player's own
                }
            }
            for each (var q:String in old) {
                delete pending[q];
            }
        }

        private static function isMarked(cat:String):Boolean {
            var v:Number = Number(cat);
            return !isNaN(v) && v >= AUTO_CATEGORY_MIN;
        }

        private function hasWindow(id:String):Boolean {
            try {
                return root["GetWindowByID"](id) != null;
            } catch (err:Error) {
            }
            return false;
        }

        // onAdded runs inside the game's CreateChatWindow, after the window is added and before it takes the focus.
        private function onAdded(e:Event):void {
            var w:Object = e.target;
            if (w == null || bar == null || w["parent"] != bar) {
                return;
            }
            keep(w);
            try {
                var id:String = String(w["m_id"]);
                if (pending[id] == undefined || int(w.GetChatWindow().GetChatType()) != CHAT_TYPE_ROOM) {
                    return;
                }
                delete pending[id];
                settle.push({w: w, focus: stage != null ? stage.focus : null});
            } catch (err:Error) {
            }
        }

        // settleWindows minimises the windows this class's joins opened and gives the focus back.
        private function settleWindows():void {
            var list:Array = settle;
            settle = [];
            for each (var s:Object in list) {
                try {
                    var w:Object = s.w;
                    if (w["parent"] == null) {
                        continue; // closed already
                    }
                    if (!w.IsMinimized()) {
                        w.ToggleMinimize();
                    }
                    w.GetChatWindow().unfocus();
                    quietTab(w, getTimer() + QUIET_MS);
                } catch (err:Error) {
                }
                restoreFocus(s.focus);
            }
        }

        // quietTab marks w's tab "nothing new" until `until` (getTimer): the game's CreateChatWindow marks the tab of a
        // room window it opens without the input focus as notified (ChatWindowTab.SetNotify), so the tab would sit in
        // its lit "notifySolid" state from the login on and a real line couldn't change it. While the window has no
        // line yet, keepLines resets that mark (ChatWindowContainer.OnContext, as the game's Expand does).
        private function quietTab(w:Object, until:int):void {
            for each (var k:Object in kept) {
                if (k.w == w) {
                    k.quiet = until;
                    unmark(k);
                    return;
                }
            }
        }

        // unmark puts a quiet window's tab back to idle while its text field is still empty.
        private function unmark(k:Object):void {
            try {
                if (getTimer() > int(k.quiet)) {
                    k.quiet = 0;
                    return;
                }
                var t:Object = k.w.GetChatWindow()["m_chatText"];
                if (t != null && int(t["length"]) > 0) {
                    k.quiet = 0; // a real line: the game's notify stays
                    return;
                }
                var top:Object = k.w["m_chatTab"]["m_topBar"];
                if (top != null && int(top["currentFrame"]) != 1) {
                    k.w.OnContext();
                }
            } catch (err:Error) {
                k.quiet = 0;
            }
        }

        private function restoreFocus(f:Object):void {
            if (f == null || !(f is DisplayObject) || DisplayObject(f).stage == null) {
                return;
            }
            try {
                getDefinitionByName("scaleform.clik.managers.FocusHandler")["getInstance"]().setFocus(f);
            } catch (err:Error) {
                try {
                    stage.focus = f as InteractiveObject;
                } catch (err2:Error) {
                }
            }
        }
    }
}
