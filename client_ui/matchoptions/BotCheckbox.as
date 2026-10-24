// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The CREATE GAME dialog's (Custom Game Setup) additions: the "Fill open seats with bots" check box and the map
// dropdown.
//
// The setup places this class on the dialog movie's root timeline with four added tags (the game's own bytecode stays
// as it is). It adds a check box from the movie's own library (CheckboxLabeled_new, the skin of Show Password) to the
// dialog, right-aligned on the line of "Map: The Dawngate". The box starts unticked: the game loads the movie anew for
// every CREATE GAME, and the class also unticks it whenever the dialog becomes the modal clip again.
//
// The map dropdown is the dialog's own (mcMapDropDown, which the game hides because it knows one map). The setup
// writes the maps this copy of the game has besides The Dawngate into the same added archive, as
// UI/Scaleform/Battlegrounds/dgmaps.json (a line format, read below). When the list names one, the class shows the
// dropdown on the map line with The Dawngate first and the listed maps after it; with none the dialog stays as the
// game made it (a click that leaves the list closed steps to the next map). The game's own code reads only the dropdown's first entry (it knows one map), so the class sets the
// dropdown back to that entry while the game reads the dialog and puts the pick back right after.
//
// Both tell the server through the game's name: when the player confirms (the Create button, or Enter in the name or
// password field) the name gets its markers at its end just before the game's own handler reads it (the game's C++
// reads the field at once, inside that handler), and the typed name is put back right after: the map's tag (~#sm for
// The Dawngate, ~#mm for Anshavar Pass; only while the dropdown shows) and ~#bots when the box is ticked. The server
// takes the markers off, shows the game as "[Pass] [Bots] <name>", plays it on that map (one it doesn't offer: its own)
// and fills the open seats with bots at the start. A name too long for the markers is cut so that name and markers
// fit the dialog's limit (the field's maxChars, which the game sets from its own maximum).
//
// Build: re/scripts/build_picker.py build matchoptions (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.geom.Matrix;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.Dictionary;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;

    public class BotCheckbox extends MovieClip {
        // The marker the server reads at the end of a custom game's name (the server's blaze.BotFillMarker).
        private static const MARKER:String = "~#bots";
        private static const LABEL:String = "Fill open seats with bots";
        // The game's GetMaxGameNameLength, should the field not tell.
        private static const MAX_NAME:int = 31;
        private static const PLACE_FRAMES:int = 12; // the box is placed again while its skin settles its size

        // The map list (the setup writes it into this movie's archive; the game's file opener keeps only .json):
        //     dawngate-maps	1
        //     map	<tag>	<label>       a map besides The Dawngate: its name tag (mm: ~#mm) and the dropdown's text
        //     end
        private static const MAPS_URL:String = "Battlegrounds/dgmaps.json";
        private static const MAPS_MAGIC:String = "dawngate-maps";
        // The Dawngate's tag, the dropdown's first entry.
        private static const FIRST_TAG:String = "sm";
        // The dialog's "Map:" (the game's own line, which it shows before the map's name).
        private static const MAP_KEY:String = "^FE_MatchOptions!0x0f31b981";
        private static const DROP_MIN_WIDTH:Number = 140;
        // The dropdown's own height (MOpt_DDMenu is 426 x 36): its label's text field is made for it, and the
        // component's constraints give the field what a lower box loses, which cut the label's lower part off.
        private static const DROP_HEIGHT:Number = 36;
        private static const DROP_GAP:Number = 8;
        private static const ROW_TEXT_SIZE:int = 13; // the list's rows (the renderer's own size is 12, the label's 14)
        private static const ARROW_FROM:Number = 0.5; // the dropdown's parts right of its middle are its arrow
        private static const BOX_WIDTH:Number = 165; // the bot box with its label, as it settles

        private var dlg:DisplayObjectContainer;
        private var nameIn:Object;
        private var pwIn:Object;
        private var okBtn:Object;
        private var box:Object;
        private var placeFrames:int = PLACE_FRAMES;
        private var wasModal:Boolean = false;
        private var saved:String = null; // the typed name while the field holds the marked one

        private var drop:Object;          // the dialog's mcMapDropDown, once the list names a map
        private var mapText:Object;       // the dialog's textFieldMap
        private var mapLabels:Array = null; // the dropdown's texts (null: the list not read yet, or no map in it)
        private var mapTags:Array = null;   // their tags, the same order (the list's maps)
        private var dropTags:Array = null;  // the dropdown's tags: The Dawngate's, then mapTags
        private var mapData:Object = null;  // the data provider the class gave the dropdown
        private var savedIndex:int = -1;    // the pick while the dropdown shows the first entry for the game
        private var dropHooked:Boolean = false; // the click listeners are on the dropdown
        private var openAtPress:Boolean = false; // the list was open when the dropdown was pressed
        private var list:DisplayObjectContainer = null;            // the dropdown's open list (the game's popup)
        private var listHooked:Dictionary = new Dictionary(true);   // lists with our ADDED listener
        private var fixedTexts:Dictionary = new Dictionary(true);   // row text fields given the dialog's font
        private var arrowParts:Dictionary = new Dictionary(true);   // the arrow's parts: their own matrices
        private var dropRight:Number = NaN; // the dropdown's right edge in its own coordinates (426)

        public function BotCheckbox() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame);
            loadMaps();
        }

        private function onFrame(e:Event):void {
            if (dlg == null && !hook()) {
                return;
            }
            if (saved != null || savedIndex >= 0) {
                restore(null); // a fold whose restore didn't run (the game's handler stopped the event)
            }
            feedMaps();
            if (box == null) {
                return;
            }
            if (placeFrames > 0) {
                placeFrames--;
                place();
            }
            var modal:Boolean = isModal();
            if (modal && !wasModal) {
                box["selected"] = false;
            }
            wasModal = modal;
            var on:Boolean = Boolean(nameIn["enabled"]);
            if (Boolean(box["enabled"]) != on) {
                box["enabled"] = on;
            }
        }

        // hook finds the dialog's fields and buttons (root.mcMatchOptionsDialog) and adds the box and the listeners.
        private function hook():Boolean {
            var d:Object = null;
            try {
                d = root != null ? root["mcMatchOptionsDialog"] : null;
            } catch (err:Error) {
                d = null;
            }
            if (d == null || !(d is DisplayObjectContainer) || d["mcGameNameInput"] == null ||
                    d["mcPasswordInput"] == null || d["mcConfirmBtn"] == null) {
                return false;
            }
            dlg = DisplayObjectContainer(d);
            nameIn = d["mcGameNameInput"];
            pwIn = d["mcPasswordInput"];
            okBtn = d["mcConfirmBtn"];
            try {
                mapText = d["textFieldMap"];
            } catch (err:Error) {
                mapText = null;
            }
            try {
                var c:Class = getDefinitionByName("CheckboxLabeled_new") as Class;
                var b:Object = new c();
                b["label"] = LABEL;
                b["autoSize"] = "left";
                b["selected"] = false;
                dlg.addChild(DisplayObject(b));
                box = b;
                place();
            } catch (err:Error) {
                box = null; // no box: no bot marker (the map's tag still goes in)
            }
            // The game listens on the button and both fields at the target phase with priority 0: a capture-phase
            // listener on the dialog runs before it, one at priority -1000 on each target after it.
            dlg.addEventListener("buttonClick", foldBefore, true, 1000);
            dlg.addEventListener("IF_Submit", foldBefore, true, 1000);
            okBtn.addEventListener("buttonClick", restore, false, -1000);
            nameIn.addEventListener("IF_Submit", restore, false, -1000);
            pwIn.addEventListener("IF_Submit", restore, false, -1000);
            return true;
        }

        // place puts the box on the map line, its right end under the password field's.
        private function place():void {
            try {
                box["x"] = Number(pwIn["x"]) + Number(pwIn["width"]) - Number(box["width"]);
                box["y"] = mapText != null ? Number(mapText["y"]) : Number(pwIn["y"]) + Number(pwIn["height"]) + 8;
            } catch (err:Error) {
            }
        }

        private function isModal():Boolean {
            try {
                var fm:Object = getDefinitionByName("scaleform.gfx.FocusManager");
                return fm.getModalClip() == dlg;
            } catch (err:Error) {
            }
            return false;
        }

        // loadMaps reads the map list once (the setup writes it beside this movie, so it is always there).
        private function loadMaps():void {
            var l:URLLoader = new URLLoader();
            l.addEventListener(Event.COMPLETE, function (e:Event):void {
                parseMaps(String(l.data));
            });
            l.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void {
            });
            try {
                l.load(new URLRequest(MAPS_URL));
            } catch (err:Error) {
            }
        }

        private function parseMaps(text:String):void {
            var lines:Array = text.split("\r").join("").split("\n");
            if (lines.length == 0 || String(lines[0]).split("\t")[0] != MAPS_MAGIC) {
                return;
            }
            var labels:Array = [];
            var tags:Array = [];
            var whole:Boolean = false;
            for (var i:int = 1; i < lines.length; i++) {
                var f:Array = String(lines[i]).split("\t");
                if (f[0] == "end") {
                    whole = true;
                    break;
                }
                if (f[0] == "map" && f.length >= 3 && String(f[1]).length > 0 && String(f[2]).length > 0) {
                    tags.push(String(f[1]));
                    labels.push(String(f[2]));
                }
            }
            if (whole && tags.length > 0) {
                mapTags = tags;
                mapLabels = labels;
            }
        }

        // feedMaps shows the dialog's dropdown with The Dawngate and the listed maps, once the game has filled it
        // (its OnShow gives it the game's own list and selects the first entry).
        private function feedMaps():void {
            if (mapLabels == null || dlg == null) {
                return;
            }
            if (drop == null) {
                try {
                    drop = dlg["mcMapDropDown"];
                } catch (err:Error) {
                    drop = null;
                }
                if (drop == null) {
                    mapLabels = null; // no dropdown: no map pick
                    return;
                }
            }
            try {
                var dp:Object = drop["dataProvider"];
                if (dp == null || dp == mapData) {
                    if (dp != null && Boolean(drop["enabled"]) != Boolean(nameIn["enabled"])) {
                        drop["enabled"] = Boolean(nameIn["enabled"]);
                    }
                    return; // not filled by the game yet, or ours already
                }
                var first:Object = dp.length > 0 ? dp[0] : null;
                if (first != null && !(first is String)) {
                    first = first.hasOwnProperty("label") ? first["label"] : null;
                }
                // objects, not strings: the game's list item renderer asks `"tooltip" in data`, which a string
                // fails ("Scaleform: Error: null")
                var items:Array = [{label: translate(first != null ? String(first) : "^labs_global!0x30000001")}];
                var tags:Array = [FIRST_TAG];
                for (var i:int = 0; i < mapLabels.length; i++) {
                    items.push({label: String(mapLabels[i])});
                    tags.push(String(mapTags[i]));
                }
                var dpc:Class = getDefinitionByName("scaleform.clik.data.DataProvider") as Class;
                var data:Object = new dpc(items);
                // the inspector's list linkages aren't in this movie; its own list and renderer are (no scroll bar:
                // every map shows)
                drop["dropdown"] = "MOpt_SList";
                drop["itemRenderer"] = "MOpt_ListItemSkinned";
                drop["scrollBar"] = "";
                drop["labelField"] = "label";
                drop["menuRowCount"] = items.length;
                drop["dataProvider"] = data;
                drop["selectedIndex"] = 0;
                mapData = data;
                dropTags = tags;
                if (mapText != null) {
                    mapText["text"] = translate(MAP_KEY);
                    // the map line's text field lies above the dropdown (the game placed it higher) and would take
                    // its clicks
                    mapText["mouseEnabled"] = false;
                }
                placeDrop();
                // above the dialog's other parts (its open list is the game's popup)
                dlg.setChildIndex(DisplayObject(drop), dlg.numChildren - 1);
                hookDrop();
                drop["visible"] = true;
            } catch (err:Error) {
                hideDrop();
            }
        }

        // placeDrop puts the dropdown on the map line after "Map:", up to the bot box (the game made it as wide as the
        // fields, 426 x 36: it is narrowed and keeps its height, centred on the line, which puts its label level with
        // "Map:"; keepArrow keeps the arrow at its own size).
        private function placeDrop():void {
            var x:Number = Number(pwIn["x"]) + 10;
            var mid:Number = Number(pwIn["y"]) + Number(pwIn["height"]) + 8 + DROP_HEIGHT / 2;
            if (mapText != null) {
                x = Number(mapText["x"]) + Number(mapText["textWidth"]) + DROP_GAP;
                mid = Number(mapText["y"]) + Number(mapText["height"]) / 2;
            }
            var right:Number = Number(pwIn["x"]) + Number(pwIn["width"]);
            if (box != null) {
                // the box's right end is the password field's; its width settles over a few frames (165 at the end)
                right -= Math.max(Number(box["width"]), BOX_WIDTH) + DROP_GAP;
            }
            drop["width"] = Math.max(right - x, DROP_MIN_WIDTH);
            drop["height"] = DROP_HEIGHT;
            drop["x"] = Math.round(x);
            drop["y"] = Math.round(mid - DROP_HEIGHT / 2);
        }

        // keepArrow keeps the dropdown's arrow (its button, the separator and the triangle at the right end) at its own
        // size: narrowing the component scales all of it in x, and the game's constraints counter-scale only the
        // label, which left the arrow at half its width. Each part right of the middle gets its own matrix scaled back
        // by 1 / scaleX about the component's right edge, so it looks as in the full-width dropdown. Run every frame:
        // the component applies its scale when it redraws, and a state change of its timeline may place a part anew.
        private function keepArrow():void {
            if (!dropShown()) {
                return;
            }
            try {
                var c:DisplayObjectContainer = DisplayObjectContainer(drop);
                var sx:Number = Number(drop["scaleX"]);
                if (!(sx > 0) || c.numChildren == 0) {
                    return;
                }
                if (isNaN(dropRight)) {
                    var bg:DisplayObject = c.getChildAt(0); // the background, as wide as the component (426)
                    dropRight = bg.getBounds(c).right;
                }
                for (var i:int = 1; i < c.numChildren; i++) {
                    var p:DisplayObject = c.getChildAt(i);
                    if (p is TextField) {
                        continue;
                    }
                    var own:Matrix = arrowParts[p] as Matrix;
                    if (own == null) {
                        own = p.transform.matrix; // a copy, as the timeline placed it
                        if (own.tx < dropRight * ARROW_FROM) {
                            continue;
                        }
                        arrowParts[p] = own;
                    }
                    var m:Matrix = own.clone();
                    m.a = own.a / sx;
                    m.c = own.c / sx;
                    m.tx = dropRight - (dropRight - own.tx) / sx;
                    var now:Matrix = p.transform.matrix;
                    if (Math.abs(now.a - m.a) > 0.0001 || Math.abs(now.c - m.c) > 0.0001 ||
                            Math.abs(now.tx - m.tx) > 0.01) {
                        p.transform.matrix = m;
                    }
                }
            } catch (err:Error) {
            }
        }

        // hookDrop adds the dropdown's click listeners once: should a click leave the list closed (the game couldn't
        // open it), the click steps to the next map instead, so the pick works without the list too.
        private function hookDrop():void {
            if (dropHooked) {
                return;
            }
            dropHooked = true;
            drop.addEventListener("mouseDown", onDropPress, false, 1000);
            drop.addEventListener("buttonClick", onDropClick, false, -1000);
            try {
                drop["showDropdownSignal"].add(onListShown); // the game's DropdownMenu, at the end of its showDropdown
            } catch (err:Error) {
            }
            addEventListener(Event.EXIT_FRAME, onExitFrame);
        }

        // The game's list item renderer (MOpt_ListItemSkinned) draws its rows in an "Arial" that this movie embeds
        // only for the glyphs of "textField": each row's text field gets the closed dropdown's own font instead (the
        // dialog's $Body2), before it draws. The renderer has a text field per state (up, over, down, ...), so every
        // text field added to the list is fixed as it comes (Event.ADDED), and once a frame while the list is open.
        private function onListShown(d:Object):void {
            list = findList();
            if (list == null) {
                return;
            }
            if (!listHooked[list]) {
                listHooked[list] = true;
                list.addEventListener(Event.ADDED, onListAdded);
            }
            try {
                list["validateNow"]();
            } catch (err:Error) {
            }
            fixTexts(DisplayObject(list));
        }

        private function onListAdded(e:Event):void {
            if (e.target is TextField) {
                fixText(TextField(e.target));
            }
        }

        private function onExitFrame(e:Event):void {
            keepArrow();
            if (list != null && dropOpen()) {
                fixTexts(DisplayObject(list));
            }
        }

        // findList finds the open list: the game's popup of class MOpt_SList, below the stage.
        private function findList():DisplayObjectContainer {
            return stage != null ? findClass(stage, "MOpt_SList", 0) : null;
        }

        private function findClass(o:DisplayObjectContainer, name:String, depth:int):DisplayObjectContainer {
            if (depth > 12) {
                return null;
            }
            for (var i:int = o.numChildren - 1; i >= 0; i--) {
                var c:DisplayObject = null;
                try {
                    c = o.getChildAt(i);
                } catch (err:Error) {
                    c = null;
                }
                var cc:DisplayObjectContainer = c as DisplayObjectContainer;
                if (cc == null) {
                    continue;
                }
                if (cc.visible && getQualifiedClassName(cc) == name) {
                    return cc;
                }
                var hit:DisplayObjectContainer = findClass(cc, name, depth + 1);
                if (hit != null) {
                    return hit;
                }
            }
            return null;
        }

        private function fixTexts(o:DisplayObject):void {
            if (o is TextField) {
                fixText(TextField(o));
                return;
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            if (c == null) {
                return;
            }
            for (var i:int = 0; i < c.numChildren; i++) {
                try {
                    fixTexts(c.getChildAt(i));
                } catch (err:Error) {
                }
            }
        }

        private function fixText(tf:TextField):void {
            if (fixedTexts[tf]) {
                return;
            }
            try {
                var fmt:TextFormat = TextField(drop["textField"]).getTextFormat();
                fmt.size = ROW_TEXT_SIZE;
                tf.embedFonts = true;
                tf.defaultTextFormat = fmt;
                tf.setTextFormat(fmt);
                fixedTexts[tf] = true;
            } catch (err:Error) {
            }
        }

        private function dropOpen():Boolean {
            try {
                return Boolean(drop.isOpen());
            } catch (err:Error) {
            }
            return false;
        }

        private function onDropPress(e:Event):void {
            openAtPress = dropShown() && dropOpen();
        }

        private function onDropClick(e:Event):void {
            if (!dropShown() || openAtPress || dropOpen() || !Boolean(drop["enabled"])) {
                return; // the list opened, or this click closed it
            }
            try {
                drop["selectedIndex"] = (int(drop["selectedIndex"]) + 1) % dropTags.length;
            } catch (err:Error) {
            }
        }

        private function hideDrop():void {
            try {
                if (drop != null) {
                    drop["visible"] = false;
                    drop["selectedIndex"] = 0;
                }
            } catch (err:Error) {
            }
            drop = null;
            mapLabels = null;
            mapData = null;
        }

        private function translate(key:String):String {
            if (key == null || key.charAt(0) != "^") {
                return key;
            }
            try {
                var common:Object = getDefinitionByName("maxis.constants.Common");
                var s:Object = common.LoadString(key, false);
                if (s != null && String(s).length > 0) {
                    return String(s);
                }
            } catch (err:Error) {
            }
            return key;
        }

        // dropShown is true while the dropdown shows our list.
        private function dropShown():Boolean {
            return drop != null && mapData != null && drop["dataProvider"] == mapData;
        }

        private function foldBefore(e:Event):void {
            if (saved != null || savedIndex >= 0) {
                return;
            }
            if (e.target != okBtn && e.target != nameIn && e.target != pwIn) {
                return;
            }
            var tag:String = "";
            if (dropShown()) {
                var i:int = int(drop["selectedIndex"]);
                if (i < 0 || i >= dropTags.length) {
                    i = 0;
                }
                tag = "~#" + String(dropTags[i]);
                if (i != 0) {
                    savedIndex = i;
                    drop["selectedIndex"] = 0; // the game knows one map: its index must be 0
                }
            }
            var t:String = String(nameIn["text"]);
            if (t.length == 0) {
                return; // nothing typed (the game's handler does nothing then)
            }
            var bots:String = "";
            if (box != null && box["selected"] && t.substr(t.length - MARKER.length) != MARKER) {
                bots = MARKER; // not when the marker was typed by hand
            }
            var add:String = tag + bots;
            if (add.length == 0) {
                return;
            }
            var max:int = int(nameIn["maxChars"]);
            if (max <= add.length) {
                max = MAX_NAME;
            }
            var keep:String = t;
            if (keep.length + add.length > max) {
                keep = keep.substr(0, max - add.length);
            }
            saved = t;
            nameIn["text"] = keep + add;
        }

        private function restore(e:Event):void {
            if (saved != null) {
                try {
                    nameIn["text"] = saved;
                } catch (err:Error) {
                }
                saved = null;
            }
            if (savedIndex >= 0) {
                try {
                    if (dropShown()) {
                        drop["selectedIndex"] = savedIndex;
                    }
                } catch (err:Error) {
                }
                savedIndex = -1;
            }
        }
    }
}
