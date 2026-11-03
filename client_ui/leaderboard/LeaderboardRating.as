// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The Leaderboard's Rating column: a fifth column "Rating" at the right of the main menu's Leaderboard (Rank, Name,
// Games Played, Win Ratio, Rating) with each player's skill rating, drawn by the screen's own list with its own header
// and cell renderers, so it has the screen's header style, font, row colours, hover and selection.
//
// Where the number comes from: the server appends a tag <dgr r='<rating>'/> to the name of each row whose rating
// players see (the Leaderboard's reply, ENAM). The game's C++ puts the name into the name cell's HTML unescaped, and
// Scaleform's htmlText drops the unknown tag, so without this class the name shows as it is. This class takes the tag
// out of the name and shows the number in its column; a row without one shows nothing there, and while no row has
// one (a server that doesn't show ratings, or an older one) the screen keeps its four columns.
//
// How: the screen (GComponent.find("leaderboard").view, a LeaderboardView) has a list (bodyView.leaderboard_List.
// mcLeaderboardList, a maxis.controls.ScrollingListView) whose columns are ListViewColumnDefs {dataSource, widthFactor,
// headerClickable} and whose header and cell rows come from two providers C++ made (maxis.DelegateDataProvider over
// the natives leaderboardHeaderItem and leaderboardCellItem: an object per row with a {text, flags} per column). The
// class gives the list two new providers of the same class whose item functions read C++'s and add the column
// "rating" (the header: the locale key ^FE_Leaderboard!0x5e70d001, which the header cell translates as it does
// EA's own keys, or the word Rating where the copy's locale package lacks the line; the cell: the number in the Win Ratio column's format), passes C++'s CHANGE events on, and appends
// a column definition to a copy of the list's. The new column isn't clickable: the list hands C++ the column index of
// a header click, and C++ sorts by its own four columns (Rank sorts by rating anyway).
//
// While the screen is off the stage the class keeps no listener on the game's objects (FlashUI's shutdown runs one more
// frame after the code movie's classes are gone, and a listener of ours left on a game object would be called then:
// the Collection's class crashed the quit that way) and calls nothing of the screen's (C++ has unbound its natives by
// then); the list keeps our providers, which answer nothing until the screen shows again (detach).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.events.Event;
    import flash.external.ExternalInterface;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;

    public class LeaderboardRating extends MovieClip {
        private static const COMPONENT_CLASS:String = "com.gravity.framework.component.GComponent";
        private static const COMPONENT_ID:String = "leaderboard";
        private static const KEY:String = "rating";           // the column's dataSource
        private static const HEADER:String = "^FE_Leaderboard!0x5e70d001"; // "Rating" (the setup's locale package)
        private static const LABEL:String = "Rating";        // the header in a copy whose locale package lacks the line
        private static const WIDTH:Number = 1;                // as Rank, Games Played and Win Ratio (Name: 2)
        private static const MARK_OPEN:String = "<dgr r='";
        private static const MARK_CLOSE:String = "'/>";
        private static const CELL_OPEN:String = "<p align='center'><font size='16'>"; // the Win Ratio cell's format
        private static const CELL_CLOSE:String = "</font></p>";
        private static const SEARCH_EVERY:int = 10;           // frames between searches for the screen
        private static const OFF_WAIT:int = 30;               // frames of nothing after the screen left the stage

        private var tick:int = 0;
        private var offSince:int = -1;
        private var box:DisplayObjectContainer = null;  // the screen's displayContainer while the class is attached
        private var list:Object = null;                 // the screen's list
        private var eaCell:Object = null;               // C++'s providers and columns
        private var eaHead:Object = null;
        private var eaDefs:Object = null;
        private var ourCell:Object = null;
        private var ourHead:Object = null;
        private var shown:Boolean = false;              // the list has the Rating column
        private var live:Boolean = false;               // attached: our item functions read C++'s providers
        private var header:String = HEADER;             // the header cell's text: the key, or LABEL without its line

        public function LeaderboardRating() {
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
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
                    if (tick % SEARCH_EVERY == 0 && list != currentList()) {
                        detach(); // a new list (the screen built again)
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

        // the Leaderboard screen while it is shown, else null
        private static function screen():Object {
            try {
                var comp:Object = getDefinitionByName(COMPONENT_CLASS).find(COMPONENT_ID);
                var v:Object = comp != null ? comp.view : null;
                if (v == null || getQualifiedClassName(v).indexOf("LeaderboardView") < 0 || !v.isShown) {
                    return null;
                }
                var dc:DisplayObjectContainer = v.displayContainer as DisplayObjectContainer;
                return dc != null && dc.stage != null ? v : null;
            } catch (err:Error) {
            }
            return null;
        }

        private function currentList():Object {
            try {
                var v:Object = screen();
                return v != null ? v.bodyView.leaderboard_List.mcLeaderboardList : null;
            } catch (err:Error) {
            }
            return null;
        }

        private function attach(v:Object):void {
            var l:Object = null;
            try {
                l = v.bodyView.leaderboard_List.mcLeaderboardList;
            } catch (err:Error) {
            }
            if (l == null || l.cellRowDataProvider == null || l.headerDataProvider == null || l.columnDefs == null ||
                l.columnDefs.length == 0) {
                return;
            }
            if (l !== list || l.cellRowDataProvider !== ourCell || l.headerDataProvider !== ourHead) {
                // a list of C++'s own: our providers in front of its
                if (getQualifiedClassName(l.cellRowDataProvider).indexOf("DelegateDataProvider") < 0) {
                    return;
                }
                list = l;
                eaCell = l.cellRowDataProvider;
                eaHead = l.headerDataProvider;
                eaDefs = l.columnDefs;
                var DP:Class = Object(eaCell).constructor as Class; // maxis.DelegateDataProvider
                ourCell = new DP(cellSize, cellItem, cellIndex);
                ourHead = new DP(headSize, headItem, headIndex);
                shown = false;
                l.headerDataProvider = ourHead;
                l.cellRowDataProvider = ourCell;
            }
            // else the list the class left (the screen shown again): it still has our providers
            box = v.displayContainer as DisplayObjectContainer;
            live = true;
            header = translated(HEADER) ? HEADER : LABEL;
            eaCell.addEventListener(Event.CHANGE, onCellChange, false, 0, false);
            eaHead.addEventListener(Event.CHANGE, onHeadChange, false, 0, false);
            box.addEventListener(Event.REMOVED_FROM_STAGE, onBoxGone, false, 0, false);
            columns(marked());
            ourCell.invalidate();
            ourHead.invalidate();
        }

        // No listener of ours left on the game's objects, and nothing that reaches C++: when the screen leaves the
        // stage, C++ has already unbound the screen's natives (an item asked for then asserts ASFuncBinder.h 89,
        // nIdx < mBindEntries.size(), and crashes), so the list is left as it is, with our providers and columns, and
        // isn't invalidated (it only draws when invalidated). Our item functions answer nothing until the class is
        // attached again: the same list when the screen shows again, or a new one.
        private function detach():void {
            live = false;
            if (box != null) {
                box.removeEventListener(Event.REMOVED_FROM_STAGE, onBoxGone);
            }
            if (eaCell != null) {
                eaCell.removeEventListener(Event.CHANGE, onCellChange);
            }
            if (eaHead != null) {
                eaHead.removeEventListener(Event.CHANGE, onHeadChange);
            }
            box = null;
        }

        private function onBoxGone(e:Event):void {
            detach();
            offSince = tick;
        }

        // whether any row carries a rating
        private function marked():Boolean {
            var n:uint = live && eaCell != null ? eaCell.length : 0;
            for (var i:uint = 0; i < n; i++) {
                var o:Object = eaCell.requestItemAt(i);
                if (o != null && o.name != null && String(o.name.text).indexOf(MARK_OPEN) >= 0) {
                    return true;
                }
            }
            return false;
        }

        // the list with or without the Rating column
        private function columns(on:Boolean):void {
            if (list == null || eaDefs == null || on == shown) {
                return;
            }
            // ListView's draw makes new renderers for new column definitions but leaves the old ones in its
            // container (the old headers would stay, faint, at their old places): take them out first
            var old:Object = list.columnDefs;
            var n:int = old != null ? old.length : 0;
            for (var i:int = 0; i < n; i++) {
                drop(list.getHeaderRendererAt(i) as DisplayObject);
            }
            for (var j:int = 0; j < n * (list.cellRows + 1); j++) {
                drop(list.getCellRendererAt(j) as DisplayObject);
            }
            var defs:Object = eaDefs;
            if (on) {
                defs = eaDefs.concat();
                var Def:Class = Object(eaDefs[0]).constructor as Class; // maxis.data.ListViewColumnDef
                var d:Object = new Def();
                d.dataSource = KEY;
                d.widthFactor = WIDTH;
                d.headerClickable = false;
                defs.push(d);
            }
            shown = on;
            list.columnDefs = defs;
            if (box != null && box.stage != null) {
                list.validateNow();
            }
        }

        private static function drop(d:DisplayObject):void {
            if (d != null && d.parent != null) {
                d.parent.removeChild(d);
            }
        }

        private function onCellChange(e:Event):void {
            try {
                columns(marked());
                if (ourCell != null) {
                    ourCell.invalidate();
                }
            } catch (err:Error) {
            }
        }

        private function onHeadChange(e:Event):void {
            try {
                if (ourHead != null) {
                    ourHead.invalidate();
                }
            } catch (err:Error) {
            }
        }

        private function cellSize():uint {
            return live && eaCell != null ? eaCell.length : 0;
        }

        private function cellIndex(o:Object):int {
            return live && eaCell != null ? eaCell.indexOf(o) : -1;
        }

        private function headSize():uint {
            return live && eaHead != null ? eaHead.length : 0;
        }

        private function headIndex(o:Object):int {
            return live && eaHead != null ? eaHead.indexOf(o) : -1;
        }

        private static function copy(o:Object):Object {
            var r:Object = {};
            for (var k:String in o) {
                r[k] = o[k];
            }
            return r;
        }

        // a row: C++'s, with the tag taken out of the name and the number in the Rating column
        private function cellItem(i:uint):Object {
            var o:Object = live && eaCell != null ? eaCell.requestItemAt(i) : null;
            if (o == null || !shown) {
                return o;
            }
            var r:Object = copy(o);
            var value:String = "";
            var flags:Object = o.winRatio != null ? o.winRatio.flags : 0;
            var t:String = o.name != null ? o.name.text as String : null;
            var a:int = t != null ? t.indexOf(MARK_OPEN) : -1;
            if (a >= 0) {
                var b:int = t.indexOf(MARK_CLOSE, a);
                if (b > a && digits(t.substring(a + MARK_OPEN.length, b))) {
                    value = t.substring(a + MARK_OPEN.length, b);
                    r.name = {text: t.substring(0, a) + t.substring(b + MARK_CLOSE.length), flags: o.name.flags};
                }
            }
            r[KEY] = {text: value != "" ? CELL_OPEN + value + CELL_CLOSE : "", flags: flags};
            return r;
        }

        // whether the locale has a line for key (a key without one shows nothing in the header cell)
        private static function translated(key:String):Boolean {
            try {
                var t:Object = ExternalInterface.call("Global.ForceTranslateString", key);
                return t != null && String(t) != "" && String(t) != key;
            } catch (err:Error) {
            }
            return false;
        }

        private static function digits(s:String):Boolean {
            if (s.length == 0 || s.length > 9) {
                return false;
            }
            for (var i:int = 0; i < s.length; i++) {
                var c:Number = s.charCodeAt(i);
                if (c < 48 || c > 57) {
                    return false;
                }
            }
            return true;
        }

        // the header row: C++'s, with the Rating column's key
        private function headItem(i:uint):Object {
            var o:Object = live && eaHead != null ? eaHead.requestItemAt(i) : null;
            if (o == null || !shown) {
                return o;
            }
            var r:Object = copy(o);
            r[KEY] = {text: header, flags: 0};
            return r;
        }
    }
}
