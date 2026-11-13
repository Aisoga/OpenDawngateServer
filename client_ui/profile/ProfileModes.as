// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The Profile's game types: a row of tabs on the main menu's Profile screen, Practice | Standard | Draft | Custom |
// All Modes (All Modes at first), which switches every number the screen shows (Career, Recent History,
// Statistics) to the shown player's games of that type. Custom games their admin made Unrated count in none.
//
// The game asks the server for the same four stat groups whatever the screen shows, so which games they count is the
// player's choice on the server: while the screen shows, the class holds a UI channel connection
// (opendawngate.ServerChannel), whose profile.state {mode, modes} says the mode the player's profile requests are
// answered in and the modes there are, in the tabs' order (none: an older server, and no tabs). A tab click calls
// profile.select {mode}; once the state names a mode other than the one the screen's numbers were loaded in, the class
// makes the screen load its player again: the screen's own native search(name) with the shown player's name, which
// loads the same player again since the exe patch profilereload (as shipped it loads only another player). C++ then
// asks for the four groups and draws what comes back, as when the screen opens. A load the screen didn't take (one
// was running) is asked for again, three times at most.
//
// The screen's Statistics panel (MyOverallStatsView) makes its five boxes anew at every load and numbers each box's
// place in its reveal sequence from a counter it never resets, so each load of the same screen showed them 0.375 s
// later than the one before (0.05 s + 0.075 s per place: after ten tab clicks about 4 s). The class gives each new box
// of that panel its place in the panel instead (0 to 4, as at the screen's first load), so every load reveals them as
// the first did.
//
// The tabs are the game's own (opendawngate.GameTabs: Tab000, as the Leaderboard's), in the header's lower half: the
// row starts above the right-hand panels (Recent History, Statistics) and ends before the search field, moved left
// where it doesn't fit (not over the player's title), and drawn smaller only where even that is too narrow. Natives
// are called only from the class's frame while the screen shows and its natives are bound
// (nativeBridgeDestroyedSignal ends that before the screen leaves the stage); the connection closes with the screen.
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.external.ExternalInterface;
    import flash.geom.Rectangle;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;
    import opendawngate.GameTabs;
    import opendawngate.ServerChannel;

    public class ProfileModes extends MovieClip {
        private static const COMPONENT_CLASS:String = "com.gravity.framework.component.GComponent";
        private static const COMPONENT_ID:String = "playerProfile";   // screen_manager_manifest.json
        private static const SEARCH_EVERY:int = 10;           // frames between searches for the screen
        private static const OFF_WAIT:int = 30;               // frames of nothing after the screen left the stage
        private static const RELOAD_EVERY:int = 1;            // frames between looks at a wanted reload
        private static const RELOAD_WAIT:int = 90;            // frames for the screen's new numbers before asking again
        private static const RELOAD_TRIES:int = 3;
        // the server's modes in the tabs' order (blaze.ProfileModes), the labels (the user's words)
        private static const MODES:Array = ["practice", "standard", "draft", "custom", "all"];
        private static const LABELS:Array = ["Practice", "Standard", "Draft", "Custom", "All Modes"];
        // in the profile layout's units (playerprofile_manifest.json): the header (profileContainer) 88 high, 6 above
        // the panels
        private static const HEADER_TO_PANELS:Number = 94;
        private static const TAB_LEAST:Number = 70;           // a tab's least width
        private static const TAB_MOST:Number = 220;           // and most (the scaling grid keeps the art)
        private static const GAP:Number = 12;                 // to the search field and the title
        // where the header's parts have no size (the first opening of a run: profile2_tabs2d): the title's block ends
        // about 260 from the header's left (avatar 88, the title's icon 32, the text), the search box (390 and its 15
        // border) at the header's right, which is the right-hand panels' right
        private static const TITLE_END:Number = 260;
        private static const SEARCH_BOX:Number = 405;
        private static const ABOVE_PANELS:Number = 2;         // the selected tab's bottom above the panels
        private static const STATS_ROOT:String = "myStatsRoot"; // the Statistics panel's box of boxes (its layout)
        private static var self:ProfileModes = null;          // a strong reference (the code movie is on no stage)

        private var tick:int = 0;
        private var offSince:int = -1;
        private var channel:ServerChannel = new ServerChannel();
        private var tabs:GameTabs = null;
        private var comp:Object = null;                 // the screen's component and view while attached
        private var view:Object = null;
        private var box:DisplayObjectContainer = null;  // the screen's displayContainer while attached
        private var modes:Array = [];                   // the server's modes (known ids only); none: no tabs
        private var mode:String = null;                 // the server's mode for this player
        private var pending:String = null;              // a clicked tab the server hasn't answered yet
        private var fetched:String = null;              // the mode the screen's numbers were loaded in
        private var wantReload:Boolean = false;
        private var reloadAt:int = -1;                  // the frame of the last reload asked for, -1 none waiting
        private var reloadData:Object = null;           // the screen's numbers then
        private var tries:int = 0;
        private var placed:Boolean = false;             // the row is in the screen, placed by its panels
        private var placedAt:String = "";               // where the panels were then (where())
        private var attachedAt:int = 0;
        private var why:String = "";                    // why the row isn't placed (place())
        private var statsRoot:Object = null;            // the Statistics panel's box of boxes, hooked while attached

        public function ProfileModes() {
            self = this;
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
            channel.on("profile.state", onState);
            tabs = new GameTabs("ProfileModes", onTab);
        }

        private function onFrame(e:Event):void {
            tick++;
            try {
                step();
            } catch (err:Error) {
                detach();
            }
        }

        private function step():void {
            if (box != null) {
                if (box.stage != null) {
                    if (tick % SEARCH_EVERY == 0 && screen() !== view) {
                        detach(); // a new screen
                        return;
                    }
                    if (tick % SEARCH_EVERY == 0 && (!placed || where() != placedAt)) {
                        place(); // first, or the panels moved (the window's size)
                        if (!placed && tick - attachedAt > 300) {
                            note("place", "no tabs after 300 frames: " + why);
                        }
                    }
                    if (reloadAt >= 0 && tick - reloadAt >= RELOAD_WAIT) {
                        reloadAt = -1;
                        if (data() === reloadData && tries < RELOAD_TRIES) {
                            wantReload = true; // the screen didn't take it (a load was running): again
                        } else if (data() === reloadData) {
                            note("reload", "the screen took none of " + tries + " reloads (search) for " + fetched);
                        }
                    }
                    if (reloadAt >= 0 && data() !== reloadData) {
                        reloadAt = -1;          // the new numbers are there
                    }
                    if (statsRoot == null && tick % SEARCH_EVERY == 0) {
                        hookStats();
                    }
                    if (wantReload && reloadAt < 0 && tick % RELOAD_EVERY == 0) {
                        reload();
                    }
                    return;
                }
                detach();
                offSince = tick;
            }
            if (offSince >= 0) {
                if (tick - offSince < OFF_WAIT) {
                    return;
                }
                offSince = -1;
            }
            if (tick % SEARCH_EVERY == 0) {
                var v:Object = screen();
                if (v != null) {
                    attach(v);
                }
            }
        }

        // The Profile screen while it is shown, else null: of every component named playerProfile (GComponent.find
        // returns the first of its unordered root table's, and the live run's first opening drew no tabs), the one whose
        // view is shown, preferring one with numbers. found is its component.
        private var found:Object = null;
        private var candidates:int = 0;

        private function screen():Object {
            var best:Object = null;
            var bestComp:Object = null;
            var all:Array = components();
            candidates = 0;
            for each (var c:Object in all) {
                try {
                    var v:Object = c.view;
                    if (v == null || getQualifiedClassName(v).indexOf("PlayerProfileView") < 0 || !v.isShown) {
                        continue;
                    }
                    var dc:DisplayObjectContainer = v.displayContainer as DisplayObjectContainer;
                    if (dc == null || dc.stage == null) {
                        continue;
                    }
                    candidates++;
                    if (best == null || (best.playerProfileData == null && v.playerProfileData != null)) {
                        best = v;
                        bestComp = c;
                    }
                } catch (err:Error) {
                }
            }
            found = bestComp;
            return best;
        }

        // every component named playerProfile in the tree of the one GComponent.find knows
        private static function components():Array {
            var out:Array = [];
            try {
                var c:Object = getDefinitionByName(COMPONENT_CLASS).find(COMPONENT_ID);
                if (c == null) {
                    return out;
                }
                var root:Object = c;
                for (var n:int = 0; n < 32 && root.parent != null; n++) {
                    root = root.parent;
                }
                walk(root, out, 0);
                if (out.indexOf(c) < 0) {
                    out.push(c);
                }
            } catch (err:Error) {
            }
            return out;
        }

        private static function walk(c:Object, out:Array, depth:int):void {
            if (c == null || depth > 16) {
                return;
            }
            try {
                if (c.name == COMPONENT_ID && out.indexOf(c) < 0) {
                    out.push(c);
                }
                var ch:Object = c.children;
                var n:int = ch != null ? int(ch.length) : 0;
                for (var i:int = 0; i < n; i++) {
                    walk(ch[i], out, depth + 1);
                }
            } catch (err:Error) {
            }
        }

        // one Assert line per attach and reason, only when something doesn't work (the live check reads them)
        private var noted:Object = {};

        private function note(key:String, text:String):void {
            if (noted[key]) {
                return;
            }
            noted[key] = true;
            try {
                ExternalInterface.call("Assert", "ProfileModes: " + text);
            } catch (err:Error) {
            }
        }

        // the numbers the screen shows (its PlayerProfileData), null before the first
        private function data():Object {
            try {
                return view != null ? view.playerProfileData : null;
            } catch (err:Error) {
            }
            return null;
        }

        private function attach(v:Object):void {
            view = v;
            box = v.displayContainer as DisplayObjectContainer;
            comp = found;
            noted = {};
            attachedAt = tick;
            if (candidates > 1) {
                note("many", candidates + " profile screens shown; numbers on the chosen one: " + (data() != null));
            }
            if (comp != null) {
                comp.nativeBridgeDestroyedSignal.add(onBridgeGone);
            }
            box.addEventListener(Event.REMOVED_FROM_STAGE, onBoxGone, false, 0, false);
            fetched = null;     // the screen loaded in the server's mode: the first state says which
            wantReload = false;
            reloadAt = -1;
            tries = 0;
            placed = false;
            channel.open(localName());
            place();
        }

        // the local player's persona name (the channel's hello), "" when unknown (a tunnelled login doesn't need it)
        private static function localName():String {
            try {
                var u:Object = ExternalInterface.call("MaxisChat.GetLocalUser");
                if (u != null && u.m_persona is String) {
                    var s:String = String(u.m_persona);
                    if (s.length > 0 && s.length <= 64 && plain(s)) {
                        return s;
                    }
                }
            } catch (err:Error) {
            }
            return "";
        }

        // digits, letters, '_' and '-' only (a persona name; anything else isn't handed on)
        private static function plain(s:String):Boolean {
            for (var i:int = 0; i < s.length; i++) {
                var c:int = s.charCodeAt(i);
                if (!((c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c == 45)) {
                    return false;
                }
            }
            return true;
        }

        // Nothing of the screen's from now on (its natives are unbound once it leaves): our row out of its container,
        // our listeners off, the channel closed. The mode and modes are kept, so the tabs show at once at the next attach.
        private function detach():void {
            try {
                if (comp != null) {
                    comp.nativeBridgeDestroyedSignal.remove(onBridgeGone);
                }
            } catch (err:Error) {
            }
            unhookStats();
            comp = null;
            view = null;
            pending = null;
            wantReload = false;
            reloadAt = -1;
            placed = false;
            tabs.drop();
            channel.close();
            if (box != null) {
                box.removeEventListener(Event.REMOVED_FROM_STAGE, onBoxGone);
            }
            box = null;
        }

        private function onBoxGone(e:Event):void {
            detach();
            offSince = tick;
        }

        // C++ takes the screen's natives away (before the screen leaves the stage)
        private function onBridgeGone(c:Object = null):void {
            try {
                detach();
                offSince = tick;
            } catch (err:Error) {
            }
        }

        // profile.state {mode, modes}: the server's mode for this player and the modes there are
        private function onState(d:Object):void {
            var m:String = null;
            var ms:Array = [];
            try {
                m = d.mode is String ? String(d.mode) : null;
                var list:Object = d.modes;
                if (list is Array) {
                    for each (var id:Object in list) {
                        if (id is String && MODES.indexOf(String(id)) >= 0 && ms.indexOf(String(id)) < 0) {
                            ms.push(String(id));
                        }
                    }
                }
            } catch (err:Error) {
                return;
            }
            if (m == null || MODES.indexOf(m) < 0) {
                return;
            }
            mode = m;
            pending = null;
            if (ms.join() != modes.join()) {
                modes = ms;
                placed = false;
            }
            if (fetched == null) {
                fetched = mode; // the screen opened in it
            }
            if (modes.length == 0) {
                tabs.drop();
                return;
            }
            if (!placed) {
                place();
            } else {
                tabs.paint(mode);
            }
            if (mode != fetched) {
                wantReload = true;
                tries = 0;
            }
        }

        // The row in the screen's container, placed by the header and the panels (once they are drawn: the panels show
        // with the first numbers).
        private function place():void {
            if (view == null || box == null || modes.length == 0 || mode == null) {
                why = "no state from the server yet";
                return;
            }
            var header:DisplayObject = null;
            var panel:DisplayObject = null;
            var search:DisplayObject = null;
            var title:DisplayObject = null;
            try {
                header = view.playerStatsView.displayContainer as DisplayObject;
                panel = view.historyByShaperView.displayContainer as DisplayObject;
                search = view.playerStatsView.searchInputField as DisplayObject;
                title = view.playerStatsView.playerTitle as DisplayObject;
            } catch (err:Error) {
            }
            if (header == null || panel == null || header.stage == null || panel.stage == null) {
                why = "header " + (header != null) + "/" + (header != null && header.stage != null) + ", panel " +
                    (panel != null) + "/" + (panel != null && panel.stage != null);
                return;
            }
            var hb:Rectangle = header.getBounds(box);
            var pb:Rectangle = panel.getBounds(box);
            if (pb.width <= 0 || pb.height <= 0 || !(hb.y < pb.y)) {
                why = "bounds header " + hb + ", panel " + pb;
                return; // the panels aren't drawn yet
            }
            var u:Number = (pb.y - hb.y) / HEADER_TO_PANELS;
            if (!(u > 0.3 && u < 4)) {
                u = 1;
            }
            var bar:Sprite = tabs.bar;
            if (bar.parent != box || tabs.made.join() != modes.join()) {
                // the row once per screen (and when the modes change); else only placed again
                box.addChild(bar);
                var labels:Array = [];
                for each (var m:String in modes) {
                    labels.push(LABELS[MODES.indexOf(m)]);
                }
                tabs.make(modes, labels, TAB_LEAST, false, TAB_MOST);
            } else {
                box.setChildIndex(bar, box.numChildren - 1);
            }
            var w:Number = tabs.rowWidth;
            // where it may go: from the title's end to the search field; where those have no size (the header's parts
            // at the first opening), from the layout's sizes: the header's top is right (its bounds have a place, not a size)
            var right:Number = hb.width > 0 ? hb.x + hb.width : pb.x + pb.width;
            var lo:Number = hb.x + TITLE_END * u;
            if (title != null && title.stage != null) {
                var tb:Rectangle = title.getBounds(box);
                if (tb.width > 0) {
                    lo = tb.x + tb.width + GAP * u;
                }
            }
            var hi:Number = right - (SEARCH_BOX + GAP) * u;
            if (search != null && search.stage != null) {
                var sb:Rectangle = search.getBounds(box);
                if (sb.width > 0 && sb.x > lo) {
                    hi = sb.x - GAP * u;
                }
            }
            var s:Number = u;
            if (w * s > hi - lo) {
                s = Math.max(0.5 * u, (hi - lo) / w); // too narrow even from the title on: smaller
            }
            bar.scaleX = bar.scaleY = s;
            bar.x = Math.max(lo, Math.min(pb.x, hi - w * s));
            bar.y = pb.y - (tabs.rowHeight + ABOVE_PANELS) * s;
            tabs.paint(pending != null ? pending : mode);
            placed = true;
            placedAt = where();
        }

        // the header's and the right-hand panels' bounds in the screen's container, as text ("" when not there)
        private function where():String {
            try {
                var h:DisplayObject = view.playerStatsView.displayContainer as DisplayObject;
                var p:DisplayObject = view.historyByShaperView.displayContainer as DisplayObject;
                if (h != null && p != null && h.stage != null && p.stage != null) {
                    return h.getBounds(box).toString() + p.getBounds(box).toString();
                }
            } catch (err:Error) {
            }
            return "";
        }

        // The Statistics panel's box of boxes (MyOverallStatsView's myStatsRoot, there once the panel is bound): every
        // box added to it from now on gets its place in the panel as its reveal place.
        private function hookStats():void {
            if (statsRoot != null || view == null) {
                return;
            }
            try {
                var panel:Object = view.myOverallStatsView;
                var layout:Object = panel != null ? panel.viewLayout : null;
                var root:Object = layout != null ? layout.find(STATS_ROOT) : null;
                if (root != null && root.layoutElementAdded != null) {
                    root.layoutElementAdded.add(onStatBox);
                    statsRoot = root;
                }
            } catch (err:Error) {
                note("stats", "no hook on the Statistics boxes: " + err.message);
            }
        }

        private function unhookStats():void {
            try {
                if (statsRoot != null) {
                    statsRoot.layoutElementAdded.remove(onStatBox);
                }
            } catch (err:Error) {
            }
            statsRoot = null;
        }

        // A box added to the Statistics panel (MyOverallStatsView.addStatBox has set its reveal place from its
        // never-reset counter just before): its place among the panel's boxes instead, never a later one.
        private function onStatBox(parent:Object, box:Object):void {
            try {
                if (box == null || parent !== statsRoot) {
                    return;
                }
                var all:Array = statsRoot.layoutElements as Array;
                var at:int = all != null ? all.indexOf(box) : -1;
                if (at < 0 && all != null) {
                    at = all.length - 1;
                }
                if (at >= 0 && int(box.contentAppearanceIndex) > at && !box.contentShown && !box.contentDisplayInProgress) {
                    box.contentAppearanceIndex = at;
                }
            } catch (err:Error) {
                note("statbox", "a Statistics box kept its reveal place: " + err.message);
            }
        }

        // a tab: the server is asked; its state then turns the tab on and the screen loads again
        private function onTab(id:String):void {
            try {
                if (id == (pending != null ? pending : mode)) {
                    return;
                }
                if (channel.call("profile.select", {mode: id})) {
                    pending = id;
                    tabs.paint(pending);
                }
            } catch (err:Error) {
            }
        }

        // The screen loads its player again (its native search with the shown name; the same player loads again since
        // the exe patch profilereload): only with numbers shown (none: the first load is still running) and a name that
        // is a persona name.
        private function reload():void {
            wantReload = false;
            try {
                if (comp == null || view == null || !view.isShown) {
                    return;
                }
                var d:Object = data();
                // the screen's natives (search, changeAvatar, ...) are bound to its controller's bridge, as
                // PlayerProfileController's own _nativeBridge.search(newName); the model's bridge has none (Error #1006)
                var nb:Object = comp.viewController != null ? comp.viewController.nativeBridge : null;
                var name:String = d != null && d.playerName is String ? String(d.playerName) : "";
                if (nb == null || !(nb.search is Function) || d == null || name.length == 0 || name.length > 64 ||
                    !plain(name)) {
                    wantReload = d == null; // the first numbers aren't there yet: later
                    if (d == null && tick - attachedAt > 300) {
                        note("data", "no numbers on the screen after 300 frames");
                    } else if (d != null) {
                        note("name", "no reload: bridge " + (nb != null) + ", search " + (nb != null && nb.search is Function) +
                            ", a name of " + name.length + " characters");
                    }
                    return;
                }
                hookStats(); // before the screen makes the new boxes
                tries++;
                reloadData = d;
                reloadAt = tick;
                fetched = mode;
                nb.search(name);
            } catch (err:Error) {
                note("search", "search failed: " + err.message);
            }
        }
    }
}
