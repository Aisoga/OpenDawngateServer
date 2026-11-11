// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The party lobby's Preferred Region: the player's own Preferred Region by the server's name, in place of the game's
// region choice (two region buttons, "Match With All Regions" and their busy indicator).
//
// The setup places this class on the party lobby movie's root timeline with four added tags (the game's own bytecode
// stays as it is). The class finds the lobby (root.mcPVPLobby, maxis.PVPLobby) and:
//   - hides the game's two region buttons, its "Match With All Regions" box and the region busy indicator for good:
//     the server no longer reads a party's region choice, each member's own Preferred Region votes where the party's
//     match runs, so a click there would change nothing;
//   - writes PREFERRED REGION into the game's own header of that panel (the same field, so the same font and colour)
//     and puts the player's Preferred Region under it, read-only, with a line saying where it is changed (the play
//     screen); the panel is cut to fit;
//   - hides the header, the panel and its lines while the server offers one region (or hasn't said yet).
//
// The regions and the player's choice are the server's (the UI channel's `region` area, docs/development/ui-channel.md):
//     server -> class  region.state {regions: [{id, name}], home, selected}   at every connect
// The class only reads: it opens the channel when the lobby comes up (the game's OnShow, which gives the region buttons
// a new group named "regions" each time, or the first member tile turning "occupied"), keeps the state it gets and closes the channel again, so a socket is open
// only for those few milliseconds (the socket's thread allocates from the movie's heap, so it must not outlive the
// movie; the class also closes it when the movie leaves the stage). Should the game show the lobby again without
// loading the movie anew, the class asks again, so a choice changed on the play screen in between is what it shows.
// The hello names the local member (the lobby's own GetMemberName of the member IsMemberLocal says is this player):
// the server ignores the name when the connection's tunnel session has one login, and picks by it among that
// session's logins when it has several (two clients behind one tunnel front).
//
// Nothing of the game waits on this class: it calls only those getters of the lobby's natives, never one that
// changes the party, so the game's party code (its queue, its Ready) runs as without it.
//
// The names are the server's text: drawn with .text only, never htmlText (ui-channel.md *Security*).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.external.ExternalInterface;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.text.TextFormatAlign;
    import flash.utils.getTimer;

    import opendawngate.ServerChannel;

    public class PartyRegion extends MovieClip {
        private static const TITLE:String = "PREFERRED REGION";
        private static const HINT:String = "Change it on the play screen";
        private static const MAX_REGIONS:int = 16;
        private static const MAX_NAME:int = 32;
        private static const OPEN_MS:int = 10000; // the channel stays open at most this long for the state
        private static const ASK_GAP_MS:int = 1000; // at most one fetch a second
        private static const NAME_WAIT_MS:int = 5000; // no local member named after this long: say hello without
        private static const SHOW_GROUP:String = "regions"; // the group the lobby's OnShow gives its region buttons
        private static const OCCUPIED:String = "occupied"; // a member tile's frame label while it holds a member
        private static const MAX_ASSERTS:int = 3;

        // where things go, in the lobby's coordinates: the game's region panel (its unnamed sprite at depth 114) is
        // placed at (18, 365), its header (an unnamed text field at depth 116) at (25, 370.7); the region buttons
        // were at y 403 and 425, the check box at y 472
        private static const PANEL_X:Number = 18;
        private static const PANEL_Y:Number = 365;
        private static const HEADER_X:Number = 25;
        private static const HEADER_Y:Number = 370.7;
        private static const NAME_Y:Number = 399;
        private static const NAME_SIZE:int = 16;
        private static const HINT_GAP:Number = 4;
        private static const HINT_SIZE:int = 12;
        private static const BOTTOM_PAD:Number = 12;

        private static const PICKED:uint = 0xE5483A; // the game's selected region button's red
        private static const HINT_COLOR:uint = 0x8A8A8A;

        private var lobby:Object = null;            // maxis.PVPLobby
        private var layer:Sprite = null;            // ours, the lobby's top child
        private var nameText:TextField = null;
        private var hintText:TextField = null;
        private var header:TextField = null;        // the game's panel header
        private var headerFormat:TextFormat = null;
        private var panel:DisplayObject = null;     // the game's region panel
        private var panelHeight:Number = 0;         // its height as the game drew it

        private var channel:ServerChannel = new ServerChannel();
        private var openSince:int = -1;             // the channel is open since (getTimer), -1: closed
        private var askedAt:int = -100000;
        private var shownGroup:Object = null;       // the region buttons' group at the last OnShow seen
        private var wantSince:int = -1;             // a fetch waits for the local member's name since, -1: none
        private var wasOccupied:Boolean = false;    // the first member tile was "occupied" at the last frame
        private var regions:Array = null;           // [{id, name}], null: not told yet
        private var selected:String = "";
        private var ended:Boolean = false;
        private var asserts:int = 0;

        public function PartyRegion() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            channel.on("region.state", onState);
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
            addEventListener(Event.REMOVED_FROM_STAGE, onGone, false, 0, false);
        }

        private function say(s:String):void {
            if (asserts >= MAX_ASSERTS) {
                return;
            }
            asserts++;
            try {
                ExternalInterface.call("Assert", "PartyRegion: " + s);
            } catch (e:Error) {
            }
        }

        // ---- the channel ----

        // a fetch is due: opens the channel once the local member's name is known (or NAME_WAIT_MS passed)
        private function tryAsk(now:int):void {
            if (ended || wantSince < 0 || openSince >= 0 || now - askedAt < ASK_GAP_MS) {
                return;
            }
            var me:String = localName();
            if (me == "" && now - wantSince < NAME_WAIT_MS) {
                return; // the members come with the lobby's next draw
            }
            wantSince = -1;
            askedAt = now;
            openSince = now;
            // through the tunnel (every player's path) the server takes the player from the connection and the name
            // only picks among that connection's logins; a server without the tunnel has one region, so nothing to
            // show there anyway
            channel.open(me);
        }

        // the local member's name from the lobby's getters (the ones its own drawMembers reads), "" while unknown
        private function localName():String {
            try {
                var ext:Object = lobby.ext;
                if (ext == null) {
                    return "";
                }
                var n:int = int(ext.GetMemberCount());
                for (var i:int = 0; i < n && i < 5; i++) {
                    if (Boolean(ext.IsMemberLocal(i))) {
                        // the getter gives the tile's HTML (<font color="#EFEFEF">name</font>): the tags off, and a
                        // persona name has only letters, digits, _ and -
                        var s:Object = ext.GetMemberName(i);
                        return s is String ? personaOf(String(s)) : "";
                    }
                }
            } catch (err:Error) {
                say("name: " + err.message);
            }
            return "";
        }

        // the text outside the HTML tags, if it is a persona name (1-64 of A-Z a-z 0-9 _ -), else ""; by hand, as the
        // game's String.replace with a RegExp left the name out when tried
        private static function personaOf(html:String):String {
            var out:String = "";
            var inTag:Boolean = false;
            for (var i:int = 0; i < html.length; i++) {
                var c:int = html.charCodeAt(i);
                if (inTag) {
                    inTag = c != 62; // '>'
                } else if (c == 60) { // '<'
                    inTag = true;
                } else if ((c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c == 45) {
                    out += html.charAt(i);
                } else if (c != 32) {
                    return ""; // not a persona name
                }
            }
            return out.length <= 64 ? out : "";
        }

        private function closeChannel():void {
            openSince = -1;
            channel.close();
        }

        private function onState(data:Object):void {
            var rs:Array = readRegions(data);
            if (rs == null) {
                return;
            }
            var sel:String = data.selected is String ? String(data.selected) : "";
            if (indexOf(rs, sel) < 0) {
                sel = String(rs[0].id);
            }
            regions = rs;
            selected = sel;
            closeChannel(); // the greeting is all this class needs
            redraw();
        }

        private static function readRegions(data:Object):Array {
            if (data == null || !(data.regions is Array)) {
                return null;
            }
            var out:Array = [];
            for each (var r:Object in data.regions) {
                if (out.length >= MAX_REGIONS) {
                    break;
                }
                if (r == null || !(r.id is String) || !(r.name is String)) {
                    continue;
                }
                var id:String = String(r.id);
                var name:String = String(r.name);
                if (id.length == 0 || id.length > MAX_NAME || name.length == 0) {
                    continue;
                }
                if (name.length > MAX_NAME) {
                    name = name.substr(0, MAX_NAME);
                }
                out.push({id: id, name: name});
            }
            return out.length > 0 ? out : null;
        }

        private static function indexOf(rs:Array, id:String):int {
            for (var i:int = 0; i < rs.length; i++) {
                if (rs[i].id == id) {
                    return i;
                }
            }
            return -1;
        }

        // ---- the lobby ----

        private function onFrame(e:Event):void {
            if (ended) {
                return;
            }
            if (lobby == null && !hook()) {
                return;
            }
            var now:int = getTimer();
            // the lobby's OnShow gives the region buttons a new group named "regions": the lobby came up (again)
            var g:Object = null;
            var gName:String = "";
            try {
                g = lobby.mcRegion0.group;
                gName = g != null ? String(g.name) : "";
            } catch (err:Error) {
                g = null;
            }
            if (g != shownGroup) {
                shownGroup = g;
                if (gName == SHOW_GROUP) {
                    wantSince = now; // the choice may have changed on the play screen meanwhile
                }
            }
            // and the first member's tile turns "occupied" when the lobby draws its members after OnShow (the local
            // player is always one); a movie loaded before its first OnShow has it "empty"
            var occupied:Boolean = false;
            try {
                occupied = String(lobby.mcMember0.currentLabel) == OCCUPIED;
            } catch (err2:Error) {
                occupied = false;
            }
            if (occupied && !wasOccupied) {
                wantSince = now;
            }
            wasOccupied = occupied;
            tryAsk(now);
            if (openSince >= 0 && now - openSince > OPEN_MS) {
                closeChannel(); // the server isn't answering: try again at the next visit
            }
            enforce();
        }

        private function hook():Boolean {
            var s:Object = null;
            try {
                s = root != null ? root["mcPVPLobby"] : null;
            } catch (err:Error) {
                s = null;
            }
            if (s == null || !(s is DisplayObjectContainer) || s["mcRegion0"] == null || s["mcRegion1"] == null ||
                    s["mcMatchAllRegionsCb"] == null) {
                return false;
            }
            lobby = s;
            var c:DisplayObjectContainer = DisplayObjectContainer(s);
            findParts(c);
            layer = new Sprite();
            layer.mouseEnabled = false;
            layer.mouseChildren = false;
            c.addChild(layer);
            var left:Number = header != null ? header.x : HEADER_X;
            var width:Number = header != null ? header.width : 250;
            nameText = field(NAME_SIZE, PICKED, "$Body"); // in the red of the game's chosen region
            nameText.x = left;
            nameText.y = NAME_Y;
            nameText.width = width;
            layer.addChild(nameText);
            hintText = field(HINT_SIZE, HINT_COLOR, "$Body");
            hintText.x = left;
            hintText.width = width;
            hintText.text = HINT;
            layer.addChild(hintText);
            redraw();
            return true;
        }

        // the panel: the lobby's unnamed sprite placed at (18, 365); its header: the unnamed text field right above it
        // (depth 116, the panel 114), placed at (25, 370.7)
        private function findParts(c:DisplayObjectContainer):void {
            for (var i:int = 0; i < c.numChildren && panel == null; i++) {
                var d:DisplayObject = c.getChildAt(i);
                if (d.name.indexOf("mc") != 0 && !(d is TextField) && Math.abs(d.x - PANEL_X) < 1 &&
                        Math.abs(d.y - PANEL_Y) < 1) {
                    panel = d;
                    panelHeight = d.height;
                    var next:DisplayObject = i + 1 < c.numChildren ? c.getChildAt(i + 1) : null;
                    if (next is TextField && next.name.indexOf("mc") != 0 && Math.abs(next.y - HEADER_Y) < 12) {
                        header = TextField(next);
                    }
                }
            }
            if (header == null) { // by its place alone
                for (var j:int = 0; j < c.numChildren; j++) {
                    var t:DisplayObject = c.getChildAt(j);
                    if (t is TextField && t.name.indexOf("mc") != 0 && Math.abs(t.x - HEADER_X) < 12 &&
                            Math.abs(t.y - HEADER_Y) < 12) {
                        header = TextField(t);
                        break;
                    }
                }
            }
            if (header != null) {
                headerFormat = header.getTextFormat();
            } else {
                say("no header of the region panel" + (panel == null ? " and no panel" : ""));
            }
        }

        // the game's parts as this class wants them; cheap, so at every frame (the game sets some of them again)
        private function enforce():void {
            try {
                var shown:Boolean = regions != null && regions.length > 1;
                for each (var n:String in ["mcRegion0", "mcRegion1", "mcMatchAllRegionsCb", "mcRegionActivityIndicator"]) {
                    var o:Object = lobby[n];
                    if (o != null && o.visible) {
                        o.visible = false;
                    }
                }
                if (header != null) {
                    if (header.text != TITLE) {
                        header.text = TITLE;
                        if (headerFormat != null) {
                            header.setTextFormat(headerFormat);
                        }
                    }
                    if (header.visible != shown) {
                        header.visible = shown;
                    }
                }
                if (panel != null) {
                    if (panel.visible != shown) {
                        panel.visible = shown;
                    }
                    var h:Number = Math.min(panelHeight, hintText.y + hintText.height + BOTTOM_PAD - PANEL_Y);
                    if (h > 0 && Math.abs(panel.height - h) > 0.5) {
                        panel.height = h;
                    }
                }
                if (layer.visible != shown) {
                    layer.visible = shown;
                }
            } catch (err:Error) {
                say("lobby: " + err.message);
            }
        }

        private function redraw():void {
            if (nameText == null) {
                return;
            }
            var name:String = "";
            if (regions != null) {
                var i:int = indexOf(regions, selected);
                name = i >= 0 ? String(regions[i].name) : "";
            }
            if (nameText.text != name) {
                nameText.text = name;
            }
            hintText.y = nameText.y + nameText.height + HINT_GAP;
        }

        // ---- the end ----

        // the movie leaves the stage (the game drops it): nothing of ours may run on, the socket least of all
        private function onGone(e:Event):void {
            if (e.target != this) {
                return;
            }
            ended = true;
            closeChannel();
            removeEventListener(Event.ENTER_FRAME, onFrame);
        }

        private static function field(size:int, color:uint, font:String):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = false;
            t.wordWrap = false;
            var f:TextFormat = new TextFormat(font, size, color);
            f.align = TextFormatAlign.CENTER;
            t.defaultTextFormat = f;
            t.height = size + 8;
            return t;
        }
    }
}
