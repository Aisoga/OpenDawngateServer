// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The play screen's Preferred Region: a dropdown of the regions the server can send games to, in place of the game's
// two region buttons (North America, Europe) and its "Match With All Regions" box.
//
// The setup places this class on the play screen movie's root timeline with four added tags (the game's own bytecode
// stays as it is). The class finds the screen (root.mcMatchType, maxis.MatchType) and:
//   - hides the game's two region buttons for good, and the shared check box while the Versus tab shows (there it is
//     "Match With All Regions"; on the Practice tab the same box is "Show Gameplay Tips", which stays);
//   - writes PREFERRED REGION into the game's own region title (the same field, so the same font and colour) and puts a
//     dropdown of its own under it, inside the game's region panel: the chosen region's name and an arrow; a click
//     opens the list below it, a click on an entry picks it, a click anywhere else closes it;
//   - hides the title, the panel and the dropdown while the server offers one region (or hasn't said yet).
//
// The regions and the choice are the server's (the UI channel's `region` area, docs/development/ui-channel.md):
//     server -> class  region.state {regions: [{id, name}], home, selected}   at every connect, and after a change
//     class -> server  region.set {id}                                        the player picked another entry
// The class opens the channel when the screen comes up, keeps the state it gets and closes the channel again; a pick
// opens it once more for the call and its answer. So a socket is open only around those moments: the game loads this
// movie for each visit of the play screen and drops it after, and the socket's thread allocates from the movie's heap,
// so it must not outlive the movie (the class also closes it when the movie leaves the stage).
//
// A game is made in the region the server reads at its creation (PRACTICE DAWNGATE at once), and the call travels on
// the channel while the game's own request travels on its Blaze connection, so a click on one of the screen's action
// buttons right after a pick could be read first. While a pick waits for the server's answer the class holds such a
// click and gives it to the game once the answer is there (or after HOLD_MS without one).
//
// The names are the server's text: drawn with .text only, never htmlText (ui-channel.md *Security*).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.Graphics;
    import flash.display.MovieClip;
    import flash.display.Shape;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    import opendawngate.ServerChannel;

    public class RegionPicker extends MovieClip {
        private static const TITLE:String = "PREFERRED REGION";
        private static const CLICK:String = "buttonClick"; // scaleform.clik.events.ButtonEvent.CLICK
        private static const BUTTON_EVENT:String = "scaleform.clik.events.ButtonEvent";
        private static const MAX_REGIONS:int = 16;
        private static const MAX_NAME:int = 32;
        private static const OPEN_MS:int = 10000; // the channel stays open at most this long for a state or an answer
        private static const HOLD_MS:int = 3000;  // a held click goes on after this without the server's answer
        private static const MAX_ASSERTS:int = 3;

        // where things go, in the screen's coordinates (the movie is 1024 x 576): the game's region panel (its
        // unnamed sprite at depth 54) is x 18-284, y 420-520; its title field is at y 425, its buttons at y 456, 484
        private static const PANEL_X:Number = 18;
        private static const PANEL_Y:Number = 420;
        private static const BOX_X:Number = 44;
        private static const BOX_Y:Number = 455;
        private static const BOX_W:Number = 212;
        private static const BOX_H:Number = 28;
        private static const ROW_H:Number = 26;
        private static const TEXT_SIZE:int = 15;
        private static const PAD:Number = 10;

        private static const TEXT:uint = 0xD8D8D8;
        private static const PICKED:uint = 0xE5483A;   // the game's selected region button's red
        private static const LINE:uint = 0x5C5C5C;
        private static const LINE_OVER:uint = 0x9A9A9A;
        private static const ARROW:uint = 0xA9A9A9;    // the title's grey
        private static const ROW_OVER:uint = 0x3A1A14;

        private var mt:Object = null;          // the screen, maxis.MatchType
        private var layer:Sprite = null;       // ours, the screen's top child
        private var box:Sprite = null;         // the closed dropdown
        private var boxText:TextField = null;
        private var boxOver:Boolean = false;
        private var list:Sprite = null;        // the open list, or null
        private var panel:DisplayObject = null; // the game's region panel
        private var hooked:Array = [];         // [target, type, listener, capture] to take off at the end

        private var channel:ServerChannel = new ServerChannel();
        private var openSince:int = -1;        // the channel is open since (getTimer), -1: closed
        private var regions:Array = null;      // [{id, name}], null: not told yet
        private var selected:String = "";
        private var wanted:String = null;      // a pick that waits for the server's answer
        private var wantedSince:int = 0;
        private var skipStates:int = 0;        // states still to come before the answer (the connect's greeting)
        private var held:Object = null;        // the action button whose click waits for that answer
        private var releasing:Boolean = false;
        private var ended:Boolean = false;
        private var asserts:int = 0;

        public function RegionPicker() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            channel.on("hello", onHello);
            channel.on("region.state", onState);
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
            addEventListener(Event.REMOVED_FROM_STAGE, onGone, false, 0, false);
            openChannel();
        }

        private function say(s:String):void {
            if (asserts >= MAX_ASSERTS) {
                return;
            }
            asserts++;
            try {
                ExternalInterface.call("Assert", "RegionPicker: " + s);
            } catch (e:Error) {
            }
        }

        // ---- the channel ----

        private function openChannel():void {
            if (ended) {
                return;
            }
            if (openSince < 0) {
                openSince = getTimer();
            }
            // through the tunnel (every player's path) the server takes the player from the connection and the name
            // only picks among that connection's logins (two game clients behind one tunnel front share one); a server
            // without the tunnel has one region, so nothing to show there anyway
            channel.open(localName());
        }

        // the local player's persona name, "" when unknown. The play screen has no native that names the player, but
        // the chat's handlers are the game's, not the chat movie's: MaxisChat.GetLocalUser answers any movie's call
        // with a ChatMember-shaped object (m_persona, m_id, ...), the one the chat's own GetLocalPersona reads
        private function localName():String {
            try {
                var u:Object = ExternalInterface.call("MaxisChat.GetLocalUser");
                if (u != null && u.m_persona is String) {
                    return personaOf(String(u.m_persona));
                }
            } catch (err:Error) {
                say("name: " + err.message);
            }
            return "";
        }

        // the name if it is a persona name (1-64 of A-Z a-z 0-9 _ -), else ""; by hand, as the game's String.replace
        // and RegExp gave wrong results in another class
        private static function personaOf(s:String):String {
            if (s.length == 0 || s.length > 64) {
                return "";
            }
            for (var i:int = 0; i < s.length; i++) {
                var c:int = s.charCodeAt(i);
                if (!((c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c == 45)) {
                    return "";
                }
            }
            return s;
        }

        private function closeChannel():void {
            openSince = -1;
            channel.close();
        }

        private function onHello(data:Object):void {
            if (wanted != null) {
                skipStates = 1; // the greeting comes first, then the answer
                channel.call("region.set", {id: wanted});
            } else {
                skipStates = 0;
            }
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
            if (wanted != null) {
                if (skipStates > 0) {
                    skipStates--;
                    redraw();
                    return; // the connect's greeting: the pick stays drawn
                }
                wanted = null; // the answer: taken, or refused (then the server's choice is drawn again)
            }
            selected = sel;
            redraw();
            if (wanted == null) {
                release();
                closeChannel();
            }
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

        private function pick(id:String):void {
            closeList();
            if (id == selected && wanted == null) {
                return;
            }
            selected = id; // drawn at once; the server's answer confirms it or puts it back
            wanted = id;
            wantedSince = getTimer();
            redraw();
            if (channel.isOpen) {
                skipStates = 0;
                channel.call("region.set", {id: id});
            } else {
                openChannel(); // onHello sends it
            }
        }

        // ---- the screen ----

        private function onFrame(e:Event):void {
            if (ended) {
                return;
            }
            if (mt == null && !hook()) {
                return;
            }
            enforce();
            var now:int = getTimer();
            if (wanted != null && now - wantedSince > HOLD_MS) {
                release(); // no answer: the click goes on (the server reads whatever it has)
            }
            if (openSince >= 0 && now - openSince > OPEN_MS) {
                closeChannel(); // the server isn't answering: try again at the next pick or visit
                if (wanted != null) {
                    wanted = null;
                    release();
                }
            }
        }

        private function hook():Boolean {
            var s:Object = null;
            try {
                s = root != null ? root["mcMatchType"] : null;
            } catch (err:Error) {
                s = null;
            }
            if (s == null || !(s is DisplayObjectContainer) || s["mcServerTitle"] == null ||
                    s["mcNorthAmericaServer"] == null || s["mcEuropeServer"] == null || s["mcCheckbox"] == null) {
                return false;
            }
            mt = s;
            var c:DisplayObjectContainer = DisplayObjectContainer(s);
            panel = findPanel(c);
            layer = new Sprite();
            c.addChild(layer);
            box = new Sprite();
            box.x = BOX_X;
            box.y = BOX_Y;
            box.buttonMode = true;
            box.mouseChildren = false;
            boxText = field(TEXT_SIZE, TEXT);
            boxText.x = PAD;
            boxText.y = (BOX_H - (TEXT_SIZE + 8)) / 2;
            boxText.width = BOX_W - PAD - 28;
            box.addChild(boxText);
            var arrow:Shape = new Shape();
            arrow.graphics.beginFill(ARROW);
            arrow.graphics.moveTo(0, 0);
            arrow.graphics.lineTo(10, 0);
            arrow.graphics.lineTo(5, 6);
            arrow.graphics.lineTo(0, 0);
            arrow.graphics.endFill();
            arrow.x = BOX_W - PAD - 10;
            arrow.y = (BOX_H - 6) / 2;
            box.addChild(arrow);
            layer.addChild(box);
            listen(box, MouseEvent.CLICK, onBoxClick, false, 0);
            listen(box, MouseEvent.ROLL_OVER, onBoxOver, false, 0);
            listen(box, MouseEvent.ROLL_OUT, onBoxOut, false, 0);
            // the game's tab buttons set the check box up again at their click: put ours back right after
            for each (var t:String in ["mcPlayTab0", "mcPlayTab1", "mcPlayTab2", "mcTutorialTab"]) {
                if (s[t] != null) {
                    listen(s[t], CLICK, onTabClick, false, -1000);
                }
            }
            // the action buttons' clicks, before the game's (priority 0 on the same target)
            for each (var n:String in ["mcTile0", "mcTile1", "mcTile2"]) {
                var tile:Object = s[n];
                if (tile != null && tile["mcActionButton"] != null) {
                    listen(tile["mcActionButton"], CLICK, onAction, false, 1000);
                }
            }
            redraw();
            return true;
        }

        // the region panel: the screen's unnamed sprite placed at (18, 420)
        private static function findPanel(c:DisplayObjectContainer):DisplayObject {
            for (var i:int = 0; i < c.numChildren; i++) {
                var d:DisplayObject = c.getChildAt(i);
                if (Math.abs(d.x - PANEL_X) < 1 && Math.abs(d.y - PANEL_Y) < 1 && d.name.indexOf("mc") != 0) {
                    return d;
                }
            }
            return null;
        }

        private function listen(target:Object, type:String, f:Function, capture:Boolean, priority:int):void {
            target.addEventListener(type, f, capture, priority, false);
            hooked.push([target, type, f, capture]);
        }

        // the game's parts as this class wants them; cheap, so at every frame (the game sets some of them again)
        private function enforce():void {
            try {
                var shown:Boolean = regions != null && regions.length > 1;
                if (mt.mcNorthAmericaServer.visible) {
                    mt.mcNorthAmericaServer.visible = false;
                }
                if (mt.mcEuropeServer.visible) {
                    mt.mcEuropeServer.visible = false;
                }
                var versus:Boolean = Boolean(mt.mcPlayTab0.selected) && Boolean(mt.mcPlayTab0.visible);
                if (versus && mt.mcCheckbox.visible) {
                    mt.mcCheckbox.visible = false;
                }
                var title:TextField = mt.mcServerTitle as TextField;
                if (title != null) {
                    if (title.text != TITLE) {
                        title.text = TITLE;
                    }
                    if (title.visible != shown) {
                        title.visible = shown;
                    }
                }
                if (panel != null && panel.visible != shown) {
                    panel.visible = shown;
                }
                if (layer.visible != shown) {
                    layer.visible = shown;
                }
                if (!shown) {
                    closeList();
                }
                // as the game's own region buttons: not while queued for a match (the check box follows that state)
                var on:Boolean = Boolean(mt.mcCheckbox.enabled);
                if (box.mouseEnabled != on) {
                    box.mouseEnabled = on;
                    box.alpha = on ? 1 : 0.5;
                    if (!on) {
                        closeList();
                    }
                }
            } catch (err:Error) {
                say("screen: " + err.message);
            }
        }

        private function onTabClick(e:Event):void {
            closeList();
            enforce();
        }

        // ---- the dropdown ----

        private function redraw():void {
            if (box == null) {
                return;
            }
            drawBox();
            var name:String = "";
            if (regions != null) {
                var i:int = indexOf(regions, selected);
                name = i >= 0 ? String(regions[i].name) : "";
            }
            if (boxText.text != name) {
                boxText.text = name;
            }
            if (list != null) {
                openList(); // again, with the new state
            }
        }

        private function drawBox():void {
            var g:Graphics = box.graphics;
            g.clear();
            g.lineStyle(1, boxOver || list != null ? LINE_OVER : LINE);
            g.beginFill(0x000000, 0.55);
            g.drawRect(0, 0, BOX_W, BOX_H);
            g.endFill();
        }

        private function onBoxClick(e:MouseEvent):void {
            if (list != null) {
                closeList();
            } else {
                openList();
            }
        }

        private function onBoxOver(e:MouseEvent):void {
            boxOver = true;
            drawBox();
        }

        private function onBoxOut(e:MouseEvent):void {
            boxOver = false;
            drawBox();
        }

        private function openList():void {
            removeList();
            if (regions == null || layer == null) {
                return;
            }
            list = new Sprite();
            var h:Number = regions.length * ROW_H;
            list.x = BOX_X;
            // below the box, or above it when the screen's bottom bar would cover the list
            list.y = BOX_Y + BOX_H + h <= 548 ? BOX_Y + BOX_H : BOX_Y - h;
            list.graphics.lineStyle(1, LINE_OVER);
            list.graphics.beginFill(0x0E0E0E, 0.96);
            list.graphics.drawRect(0, 0, BOX_W, h);
            list.graphics.endFill();
            for (var i:int = 0; i < regions.length; i++) {
                var row:Sprite = new Sprite();
                row.y = i * ROW_H;
                row.buttonMode = true;
                row.mouseChildren = false;
                paintRow(row, false);
                var t:TextField = field(TEXT_SIZE, regions[i].id == selected ? PICKED : TEXT);
                t.text = String(regions[i].name);
                t.x = PAD;
                t.y = (ROW_H - (TEXT_SIZE + 8)) / 2;
                t.width = BOX_W - 2 * PAD;
                row.addChild(t);
                row.name = "r" + i;
                row.addEventListener(MouseEvent.CLICK, onRowClick, false, 0, false);
                row.addEventListener(MouseEvent.ROLL_OVER, onRowOver, false, 0, false);
                row.addEventListener(MouseEvent.ROLL_OUT, onRowOut, false, 0, false);
                list.addChild(row);
            }
            layer.addChild(list);
            if (stage != null) {
                stage.addEventListener(MouseEvent.MOUSE_DOWN, onAnyDown, true, 0, false);
            }
            drawBox();
        }

        private static function paintRow(row:Sprite, over:Boolean):void {
            row.graphics.clear();
            row.graphics.beginFill(over ? ROW_OVER : 0x000000, over ? 0.9 : 0.01);
            row.graphics.drawRect(1, 0, BOX_W - 1, ROW_H);
            row.graphics.endFill();
        }

        private function removeList():void {
            if (list == null) {
                return;
            }
            for (var i:int = 0; i < list.numChildren; i++) {
                var row:DisplayObject = list.getChildAt(i);
                row.removeEventListener(MouseEvent.CLICK, onRowClick);
                row.removeEventListener(MouseEvent.ROLL_OVER, onRowOver);
                row.removeEventListener(MouseEvent.ROLL_OUT, onRowOut);
            }
            if (list.parent != null) {
                list.parent.removeChild(list);
            }
            list = null;
            if (stage != null) {
                stage.removeEventListener(MouseEvent.MOUSE_DOWN, onAnyDown, true);
            }
            if (box != null) {
                drawBox();
            }
        }

        private function closeList():void {
            if (list != null) {
                removeList();
                redraw();
            }
        }

        private function onRowClick(e:MouseEvent):void {
            var row:Sprite = e.currentTarget as Sprite;
            var i:int = row != null ? int(row.name.substr(1)) : -1;
            if (regions != null && i >= 0 && i < regions.length) {
                pick(String(regions[i].id));
            } else {
                closeList();
            }
        }

        private function onRowOver(e:MouseEvent):void {
            paintRow(e.currentTarget as Sprite, true);
        }

        private function onRowOut(e:MouseEvent):void {
            paintRow(e.currentTarget as Sprite, false);
        }

        // a press outside the box and the list closes the list
        private function onAnyDown(e:MouseEvent):void {
            var d:DisplayObject = e.target as DisplayObject;
            while (d != null) {
                if (d == list || d == box) {
                    return;
                }
                d = d.parent;
            }
            closeList();
        }

        // ---- holding an action while a pick travels ----

        private function onAction(e:Event):void {
            if (releasing || wanted == null) {
                return;
            }
            e.stopImmediatePropagation(); // the game's handler doesn't see it now
            held = e.currentTarget;
        }

        // gives a held click to the game: the same event on the same button, so its own handler runs
        private function release():void {
            var b:Object = held;
            held = null;
            if (b == null || ended) {
                return;
            }
            try {
                var c:Class = getDefinitionByName(BUTTON_EVENT) as Class;
                releasing = true;
                b.dispatchEvent(new c(CLICK));
            } catch (err:Error) {
                say("release: " + err.message);
            } finally {
                releasing = false;
            }
        }

        // ---- the end ----

        // the movie leaves the stage (the game drops it): nothing of ours may run on, the socket least of all
        private function onGone(e:Event):void {
            if (e.target != this) {
                return;
            }
            ended = true;
            held = null;
            closeChannel();
            removeList();
            removeEventListener(Event.ENTER_FRAME, onFrame);
            for each (var h:Array in hooked) {
                try {
                    h[0].removeEventListener(h[1], h[2], h[3]);
                } catch (err:Error) {
                }
            }
            hooked = [];
        }

        private static function field(size:int, color:uint):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = false;
            t.wordWrap = false;
            t.defaultTextFormat = new TextFormat("$Body", size, color);
            t.height = size + 8;
            return t;
        }
    }
}
