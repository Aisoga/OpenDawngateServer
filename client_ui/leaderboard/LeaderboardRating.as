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
//
// The tabs (M30): STANDARD, DRAFT and CUSTOM, each ranking by its own rating (a rating per game type), directly above
// the table like folder tabs (their left edge on the table's, their bottoms on its top). They are the game's own tab,
// Tab000 of the always loaded Controls_Source bundle (the Collection's shaper details' tab row: its art for each state,
// its $Header3 label, the selected tab 6 units taller, 1 apart, 28 high); only where that symbol can't be made, tabs of
// our own in the After Action Review's colours (AfterActionTabs), with one Assert line: opendawngate.GameTabs, which the
// Profile's tabs share, sized as the Profile's. The row lies in the screen's own container above everything else, as
// the Profile's does: the header view's layout draws its panel's background (black, half transparent) over its own
// container, which made the tabs half as bright there. The rule of the shown tab is no line under the title any more
// but the game's tooltip of a small (i) after the title (on hover, or pinned by a click until the next click), and
// the title moves down to the middle of the header. The game always
// asks the server for the same ranking ('Skill'), so which one it gets is the player's choice on the server: while the
// screen shows, the class holds a UI channel connection (opendawngate.ServerChannel), whose leaderboard.state {board,
// boards} says the board the player's requests are answered from and the boards there are (none: a server that ranks
// by wins, or an older one, and no tabs). A tab click calls leaderboard.select {board}; once the state names a board
// other than the one whose ranking the screen last asked for, the class presses the screen's own Refresh
// (nativeBridge.refreshButtonClicked, after any refresh already running); the (i)'s tooltip says the board's rule
// (^FE_Leaderboard!0x5e70d002-4, or the English text where the copy's locale package lacks the lines). Natives are
// called only from the class's frame while the screen shows and its natives are bound (nativeBridgeDestroyedSignal
// ends that before the screen leaves the stage); the connection closes with the screen. The tooltip is the game's
// (maxis.constants.Common.GetContainer().ShowToolTip with ToolTipHelpers.BuildSimpleTooltip, as the game's buttons
// show theirs; ActionScript only), or a small panel of ours where that can't be made.
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Graphics;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.geom.Point;
    import flash.geom.Rectangle;
    import flash.text.TextField;
    import flash.text.TextFieldAutoSize;
    import flash.text.TextFormat;
    import flash.text.TextLineMetrics;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;
    import opendawngate.GameTabs;
    import opendawngate.ServerChannel;

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
        // the tabs: the server's board ids (rating.Modes) in their order, a label and the rule's locale line each
        private static const BOARDS:Array = ["standard", "draft", "custom"];
        private static const TAB_LABELS:Array = ["Standard", "Draft", "Custom"]; // as the game's tabs (its font's caps)
        private static const RULE_KEYS:Array = ["^FE_Leaderboard!0x5e70d002", "^FE_Leaderboard!0x5e70d003",
            "^FE_Leaderboard!0x5e70d004"];
        private static const RULE_TEXTS:Array = [ // without the locale package's lines (localepatch.LeaderboardLines)
            "Standard: skill rating from balanced PvP and standard custom games. Must have at least 10 rated games.",
            "Draft: skill rating from balanced custom games in Draft. Must have at least 10 rated Draft games.",
            "Custom: skill rating from balanced custom games with other rules. Must have at least 10 rated Custom games."];
        // in the header layout's units (its panel is 80 high: the title row 48, the description row 32, whose text
        // box starts 180 from the left; the body's table below it, its header cell 1 in from the body's corner)
        private static const TITLE_ROW:Number = 48;
        private static const HEADER_H:Number = 80;
        private static const DESC_LEFT:Number = 180;
        private static const TABLE_MARGIN:Number = 1;
        // the tabs sized as the Profile's (ProfileModes: as wide as each label needs, at least 70, at most 220; the
        // scaling grid keeps the art)
        private static const TAB_LEAST:Number = 70;
        private static const TAB_MOST:Number = 220;
        // the title's text moves down to the header's middle (at most MAX_DOWN), the (i) after it
        private static const MAX_DOWN:Number = 32;
        private static const INFO_R:Number = 7;              // the circle's radius
        private static const INFO_GAP:Number = 8;            // between the title's text and the circle
        private static const INFO_HIT:Number = 11;           // the radius that takes the mouse
        private static const INFO_OFF:uint = 0xBDBDBD;       // the header's grey; white on hover and while open
        private static const INFO_ON:uint = 0xFFFFFF;
        private static const TIP_NAME:String = "LeaderboardRating.info";
        private static const TIP_ALIGN:uint = 5;             // Common.kToolTipAlign_BottomCenter
        private static const OWN_TIP_W:Number = 300;         // our own panel's text width (the game's tooltip's most)
        private static const REFRESH_EVERY:int = 5;           // frames between looks at a wanted refresh
        private static var noted:Object = {};                 // the one-time Assert lines written

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
        // the tabs
        private var channel:ServerChannel = new ServerChannel();
        private var comp:Object = null;                 // the screen's component and view while attached
        private var view:Object = null;
        private var boards:Array = [];                  // the server's boards (known ids only); none: no tabs
        private var board:String = null;                // the server's board for this player
        private var pending:String = null;              // a clicked tab the server hasn't answered yet
        private var fetched:String = "standard";        // the board of the ranking the screen last asked for
        private var wantRefresh:Boolean = false;
        private var tabs:GameTabs = null;               // the row, in the screen's container while attached
        private var rules:Array = [];                   // the rule per board (BOARDS' order), read at the attach
        // the header: the title moved down, the description emptied (both put back without tabs)
        private var unit:Number = 1;                    // the layout's unit in the screen's container
        private var titleObj:DisplayObject = null;      // the title we moved, its layout place and our place
        private var titleBaseY:Number = NaN;
        private var titleSetY:Number = NaN;
        private var descView:Object = null;             // the view whose description we emptied, and its text
        private var descText:String = null;
        private var checked:Boolean = false;            // the row's look and hit test checked once per attach
        // the (i) and its tooltip
        private var info:Sprite = null;
        private var infoOver:Boolean = false;
        private var infoPinned:Boolean = false;         // opened by a click: open until the next click
        private var tip:Object = null;                  // the game's tooltip while it shows
        private var ownTip:Sprite = null;               // or ours
        private var downOn:Object = null;               // the stage we listen to for the click that closes it

        public function LeaderboardRating() {
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
            channel.on("leaderboard.state", onBoardState);
            tabs = new GameTabs("LeaderboardRating", onTab);
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
                        return;
                    }
                    if (wantRefresh && tick % REFRESH_EVERY == 0) {
                        refresh();
                    }
                    if (tick % SEARCH_EVERY == 5 && tabs.bar.parent == box) {
                        place(); // after the layout moved the screen's parts (a new window size)
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
            // the tabs: the component's end of its natives, the rules' texts, the channel (its state draws the tabs)
            view = v;
            checked = false;
            comp = component();
            if (comp != null) {
                comp.nativeBridgeDestroyedSignal.add(onBridgeGone);
            }
            rules = [];
            for (var i:int = 0; i < BOARDS.length; i++) {
                rules.push(translated(RULE_KEYS[i]) ? ExternalInterface.call("Global.ForceTranslateString", RULE_KEYS[i])
                    : RULE_TEXTS[i]);
            }
            channel.open(localName());
            drawTabs();
        }

        private static function component():Object {
            try {
                return getDefinitionByName(COMPONENT_CLASS).find(COMPONENT_ID);
            } catch (err:Error) {
            }
            return null;
        }

        // the local player's persona name (the channel's hello), "" when unknown (a tunnelled login doesn't need it)
        private static function localName():String {
            try {
                var u:Object = ExternalInterface.call("MaxisChat.GetLocalUser");
                if (u != null && u.m_persona is String) {
                    var s:String = String(u.m_persona);
                    if (s.length > 0 && s.length <= 64) {
                        for (var i:int = 0; i < s.length; i++) {
                            var c:int = s.charCodeAt(i);
                            if (!((c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c == 45)) {
                                return "";
                            }
                        }
                        return s;
                    }
                }
            } catch (err:Error) {
            }
            return "";
        }

        // No listener of ours left on the game's objects, and nothing that reaches C++: when the screen leaves the
        // stage, C++ has already unbound the screen's natives (an item asked for then asserts ASFuncBinder.h 89,
        // nIdx < mBindEntries.size(), and crashes), so the list is left as it is, with our providers and columns, and
        // isn't invalidated (it only draws when invalidated). Our item functions answer nothing until the class is
        // attached again: the same list when the screen shows again, or a new one.
        private function detach():void {
            live = false;
            try {
                if (comp != null) {
                    comp.nativeBridgeDestroyedSignal.remove(onBridgeGone);
                }
            } catch (err:Error) {
            }
            comp = null;
            view = null;
            pending = null;
            dropTabs(); // the row, the (i), its tooltip and the stage's listener (the title stays where it is)
            channel.close();
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

        // C++ takes the screen's natives away (before the screen leaves the stage): nothing of the screen's from now on
        private function onBridgeGone(c:Object = null):void {
            try {
                detach();
                offSince = tick;
            } catch (err:Error) {
            }
        }

        // leaderboard.state {board, boards}: the server's board for this player and the boards there are
        private function onBoardState(data:Object):void {
            var b:String = null;
            var bs:Array = [];
            try {
                b = data.board is String ? String(data.board) : null;
                var list:Object = data.boards;
                if (list is Array) {
                    for each (var id:Object in list) {
                        if (id is String && BOARDS.indexOf(String(id)) >= 0 && bs.indexOf(String(id)) < 0) {
                            bs.push(String(id));
                        }
                    }
                }
            } catch (err:Error) {
                return;
            }
            if (b == null || BOARDS.indexOf(b) < 0) {
                return;
            }
            board = b;
            boards = bs;
            pending = null;
            if (boards.length == 0) {
                dropTabs(); // the wins ranking: the screen as the game made it
                restoreHeader();
                return;
            }
            drawTabs();
            if (board != fetched) {
                wantRefresh = true; // the frame presses Refresh while the screen and its natives are there
            }
        }

        // the screen's own Refresh, once no refresh is running (refreshButtonClicked ignores a click during one)
        private function refresh():void {
            try {
                if (comp == null || view == null || !view.isShown) {
                    return;
                }
                var nb:Object = comp.uiModel.nativeBridge;
                if (nb == null || nb.isRefreshActive()) {
                    return;
                }
                nb.refreshButtonClicked();
                fetched = board;
            } catch (err:Error) {
            }
            wantRefresh = false;
        }

        // the tab row on the table with the board's tab on, the rule line under the title emptied, the title and the (i)
        // placed; an open tooltip says the board's rule
        private function drawTabs():void {
            if (view == null || box == null || boards.length == 0 || board == null) {
                return;
            }
            var hv:Object = view.headerView;
            if (hv == null) {
                return;
            }
            if (tabs.bar.parent != box || tabs.made.join() != boards.join()) {
                dropTabs();
                box.addChild(tabs.bar); // first: the game's buttons draw once on a stage
                var labels:Array = [];
                for each (var b:String in boards) {
                    labels.push(TAB_LABELS[BOARDS.indexOf(b)]);
                }
                tabs.make(boards, labels, TAB_LEAST, false, TAB_MOST);
            }
            emptyDescription(hv);
            place();
            paint();
            if (tip != null || ownTip != null) {
                showTip(); // the shown board's rule
            }
        }

        // The row on the table (its left edge on the table's, the tabs' bottoms on its top) at the top of the screen's
        // container, as the Profile's row (the header view's own container lies under its panel's half transparent
        // background, which halved the tabs' brightness, and under its title's box, which takes the mouse); the title's
        // text moved down to the header's middle; the (i) after it. Again every SEARCH_EVERY frames: the layout places
        // the screen's parts again at a new window size (the title then back at its place: measured and moved again).
        private function place():void {
            try {
                var hv:Object = view.headerView;
                var title:DisplayObject = hv.topPanelTitle as DisplayObject;
                var desc:DisplayObject = hv.topPanelDesc as DisplayObject;
                if (title == null || desc == null || title.stage == null || desc.stage == null) {
                    return;
                }
                if (title !== titleObj) {
                    titleObj = title;
                    titleSetY = NaN;
                }
                var db:Rectangle = desc.getBounds(box);
                if (isNaN(titleSetY) || Math.abs(title.y - titleSetY) > 0.1) {
                    // the title at the layout's place: the unit (the title's and the description's boxes lie TITLE_ROW
                    // apart, else the title's height), then the move
                    titleBaseY = title.y;
                    titleSetY = NaN;
                    var tb:Rectangle = title.getBounds(box);
                    var u:Number = (db.y - tb.y) / TITLE_ROW;
                    if (!(u > 0.3 && u < 4)) {
                        u = tb.height / TITLE_ROW;
                    }
                    if (!(u > 0.3 && u < 4)) {
                        u = 1;
                    }
                    unit = u;
                    var text:Rectangle = titleText(hv);
                    var k:Number = boxPerParent(title);
                    if (text != null && k > 0) {
                        var down:Number = Math.min(MAX_DOWN * u, tb.y + HEADER_H / 2 * u - (text.y + text.height / 2));
                        if (down > 0.5) {
                            title.y = titleBaseY + down / k;
                            titleSetY = title.y;
                        }
                    }
                }
                u = unit;
                // the table: the body's header cell (the header's bottom and the body's left, 1 in), else the layout's
                // numbers from the description's box
                var left:Number = db.x - (DESC_LEFT - TABLE_MARGIN) * u;
                var top:Number = db.y + (HEADER_H - TITLE_ROW + TABLE_MARGIN) * u;
                var cell:DisplayObject = null;
                try {
                    cell = view.bodyView.leaderboardHeaderCell as DisplayObject;
                } catch (err0:Error) {
                }
                if (cell != null && cell.stage != null) {
                    var cb:Rectangle = cell.getBounds(box);
                    if (cb.width > 0 && Math.abs(cb.y - top) < 20 * u && Math.abs(cb.x - left) < 40 * u) {
                        left = cb.x;
                        top = cb.y;
                    }
                }
                var bar:Sprite = tabs.bar;
                bar.scaleX = bar.scaleY = u;
                bar.x = left;
                bar.y = top - tabs.rowHeight * u;
                toTop(bar);
                placeInfo(hv);
                check();
            } catch (err:Error) {
            }
        }

        private function toTop(d:DisplayObject):void {
            if (d != null && d.parent == box && box.getChildIndex(d) != box.numChildren - 1) {
                box.setChildIndex(d, box.numChildren - 1);
            }
        }

        // the title's text (its first line) in the screen's container, null without one
        private function titleText(hv:Object):Rectangle {
            var f:TextField = null;
            try {
                f = hv.topPanelTitleProxy.labelField as TextField;
            } catch (err:Error) {
            }
            if (f == null) {
                f = field(hv.topPanelTitle as DisplayObjectContainer);
            }
            if (f == null || f.stage == null || f.text == "") {
                return null;
            }
            var x0:Number;
            var x1:Number;
            var y1:Number;
            try {
                var lm:TextLineMetrics = f.getLineMetrics(0);
                x0 = lm.x;
                x1 = lm.x + lm.width;
                y1 = 2 + lm.ascent + lm.descent;
            } catch (err2:Error) {
                x0 = (f.width / f.scaleX - f.textWidth) / 2;
                x1 = x0 + f.textWidth;
                y1 = 2 + f.textHeight;
            }
            if (!(x1 > x0) || !(y1 > 2)) {
                return null;
            }
            var a:Point = box.globalToLocal(f.localToGlobal(new Point(x0, 2)));
            var b:Point = box.globalToLocal(f.localToGlobal(new Point(x1, y1)));
            return new Rectangle(a.x, a.y, b.x - a.x, b.y - a.y);
        }

        private static function field(c:DisplayObjectContainer):TextField {
            if (c == null) {
                return null;
            }
            for (var i:int = 0; i < c.numChildren; i++) {
                var d:DisplayObject = c.getChildAt(i);
                if (d is TextField) {
                    return d as TextField;
                }
                if (d is DisplayObjectContainer) {
                    var f:TextField = field(d as DisplayObjectContainer);
                    if (f != null) {
                        return f;
                    }
                }
            }
            return null;
        }

        // the screen's container's units per unit of d's parent (vertical), 0 when unknown
        private function boxPerParent(d:DisplayObject):Number {
            if (d.parent == null) {
                return 0;
            }
            var a:Point = box.globalToLocal(d.parent.localToGlobal(new Point(0, 0)));
            var b:Point = box.globalToLocal(d.parent.localToGlobal(new Point(0, 100)));
            var k:Number = (b.y - a.y) / 100;
            return k > 0.05 && k < 20 ? k : 0;
        }

        // the rule line under the title goes (its text kept, to put back without tabs)
        private function emptyDescription(hv:Object):void {
            try {
                var p:Object = hv.topPanelDescProxy;
                if (p == null) {
                    return;
                }
                if (descView !== view) {
                    descView = view;
                    descText = p.label is String ? String(p.label) : null;
                }
                if (p.label != "") {
                    p.label = "";
                }
            } catch (err:Error) {
            }
        }

        // without tabs (the wins ranking): the title back at its place, the description's text back
        private function restoreHeader():void {
            try {
                if (titleObj != null && !isNaN(titleSetY) && Math.abs(titleObj.y - titleSetY) <= 0.1) {
                    titleObj.y = titleBaseY;
                }
            } catch (err:Error) {
            }
            titleSetY = NaN;
            try {
                if (view != null && descView === view && descText != null) {
                    view.headerView.topPanelDescProxy.label = descText;
                }
            } catch (err2:Error) {
            }
        }

        // our own objects only: the row and the (i) leave the screen's container, the tooltip closes
        private function dropTabs():void {
            tabs.drop();
            dropInfo();
        }

        private function paint():void {
            var on:String = pending != null ? pending : board;
            tabs.paint(on);
            // folder tabs: the bottoms on one line, the selected one 6 taller upward (the game's tabs; ours are as high)
            if (tabs.isGame) {
                var ids:Array = tabs.made;
                for (var i:int = 0; i < ids.length && i < tabs.bar.numChildren; i++) {
                    tabs.bar.getChildAt(i).y = ids[i] == on ? 0 : GameTabs.EA_SELECTED;
                }
            }
        }

        // once per attach: whether the row is drawn at full strength and on top where it lies (a line only when not)
        private function check():void {
            if (checked || tabs.bar.numChildren == 0) {
                return;
            }
            checked = true;
            try {
                var a:Number = tabs.bar.transform.concatenatedColorTransform.alphaMultiplier;
                if (a < 0.99) {
                    note("alpha", "the tab row's alpha " + a);
                }
            } catch (err:Error) {
            }
            try {
                for (var i:int = 0; i < tabs.bar.numChildren; i++) {
                    var r:Rectangle = tabs.bar.getChildAt(i).getBounds(box.stage);
                    var under:Array = box.stage.getObjectsUnderPoint(new Point(r.x + r.width / 2, r.bottom - r.height / 3));
                    var over:DisplayObject = under.length > 0 ? under[under.length - 1] as DisplayObject : null;
                    if (over != null && !tabs.bar.contains(over)) {
                        note("cover", "tab " + i + " under " + getQualifiedClassName(over) + " '" + over.name + "'");
                        break;
                    }
                }
            } catch (err2:Error) {
            }
        }

        private static function note(key:String, text:String):void {
            if (noted[key]) {
                return;
            }
            noted[key] = true;
            try {
                ExternalInterface.call("Assert", "LeaderboardRating: " + text);
            } catch (err:Error) {
            }
        }

        // the (i): after the title's text, on its middle line
        private function placeInfo(hv:Object):void {
            var r:Rectangle = titleText(hv);
            if (r == null) {
                dropInfo();
                return;
            }
            if (info == null) {
                info = new Sprite();
                info.buttonMode = true;
                info.useHandCursor = true;
                info.mouseChildren = false;
                info.addEventListener(MouseEvent.ROLL_OVER, onInfoOver, false, 0, false);
                info.addEventListener(MouseEvent.ROLL_OUT, onInfoOut, false, 0, false);
                info.addEventListener(MouseEvent.CLICK, onInfoClick, false, 0, false);
            }
            if (info.parent != box) {
                box.addChild(info);
                drawInfo();
            }
            toTop(info);
            info.scaleX = info.scaleY = unit;
            info.x = r.right + (INFO_GAP + INFO_R) * unit;
            info.y = r.y + r.height / 2;
            if (ownTip != null) {
                toTop(ownTip);
            }
        }

        // a thin circle with an i, grey (the header's), white while hovered or open; a larger invisible disc takes the
        // mouse
        private function drawInfo():void {
            if (info == null) {
                return;
            }
            var c:uint = infoOver || infoPinned || tip != null || ownTip != null ? INFO_ON : INFO_OFF;
            var g:Graphics = info.graphics;
            g.clear();
            g.beginFill(0, 0);
            g.drawCircle(0, 0, INFO_HIT);
            g.endFill();
            g.lineStyle(1.5, c, 1);
            g.drawCircle(0, 0, INFO_R);
            g.lineStyle();
            g.beginFill(c, 1);
            g.drawCircle(0, -3.4, 1.15);
            g.drawRect(-0.9, -1.4, 1.8, 5.4);
            g.endFill();
        }

        private function dropInfo():void {
            hideTip();
            listenStage(false);
            infoPinned = false;
            infoOver = false;
            if (info != null && info.parent != null) {
                info.parent.removeChild(info);
            }
        }

        // the shown tab's rule
        private function rule():String {
            var i:int = BOARDS.indexOf(pending != null ? pending : board);
            return i >= 0 && i < rules.length ? String(rules[i]) : "";
        }

        // The game's tooltip below the (i) (UIContainer.ShowToolTip with ToolTipHelpers.BuildSimpleTooltip, as a game
        // button shows its tooltipText: ActionScript only, no native); where it can't be made, a panel of ours.
        private function showTip():void {
            hideTip();
            var text:String = rule();
            if (info == null || info.stage == null || text == "") {
                return;
            }
            try {
                var C:Object = getDefinitionByName("maxis.constants.Common");
                var cont:Object = C.GetContainer();
                var data:Object = getDefinitionByName("maxis.ToolTipHelpers").BuildSimpleTooltip(text);
                var al:Object = C.kToolTipAlign_BottomCenter;
                var t:Object = cont.ShowToolTip(info, data, TIP_NAME, "LeaderboardRating", al is Number ? Number(al) : TIP_ALIGN);
                if (t == null) {
                    throw new Error("ShowToolTip gave nothing");
                }
                tip = t;
                try {
                    getDefinitionByName("scaleform.gfx.InteractiveObjectEx").setHitTestDisable(t, true);
                } catch (err0:Error) {
                }
                drawInfo();
                return;
            } catch (err:Error) {
                note("tooltip", "the game's tooltip: " + err.message);
            }
            ownTip = ownPanel(text);
            ownTip.scaleX = ownTip.scaleY = unit;
            box.addChild(ownTip);
            var p:Point = box.globalToLocal(info.localToGlobal(new Point(0, INFO_HIT)));
            ownTip.x = Math.round(p.x - ownTip.width / 2);
            ownTip.y = Math.round(p.y + 4 * unit);
            drawInfo();
        }

        private function hideTip():void {
            if (tip != null) {
                try {
                    getDefinitionByName("maxis.constants.Common").GetContainer().HideToolTipMc(tip);
                } catch (err:Error) {
                }
                tip = null;
            }
            if (ownTip != null) {
                if (ownTip.parent != null) {
                    ownTip.parent.removeChild(ownTip);
                }
                ownTip = null;
            }
            drawInfo();
        }

        // our panel: the game tooltip's look (the $Body 12 text in its light grey, at most 300 wide, on a dark box)
        private static function ownPanel(text:String):Sprite {
            var s:Sprite = new Sprite();
            s.mouseEnabled = false;
            s.mouseChildren = false;
            var tf:TextField = new TextField();
            tf.embedFonts = true;
            tf.selectable = false;
            tf.mouseEnabled = false;
            tf.multiline = true;
            tf.wordWrap = true;
            tf.width = OWN_TIP_W;
            tf.defaultTextFormat = new TextFormat("$Body", 12, 0xD8D8D8);
            tf.autoSize = TextFieldAutoSize.LEFT;
            tf.text = text;
            tf.x = 8;
            tf.y = 6;
            s.addChild(tf);
            var w:Number = Math.ceil(Math.min(OWN_TIP_W, tf.textWidth + 4) + 16);
            var h:Number = Math.ceil(tf.textHeight + 4 + 12);
            var g:Graphics = s.graphics;
            g.beginFill(0x111111, 0.92);
            g.drawRect(0, 0, w, h);
            g.endFill();
            g.beginFill(0x6B6B6B, 1);
            g.drawRect(0, 0, w, 1);
            g.drawRect(0, h - 1, w, 1);
            g.drawRect(0, 1, 1, h - 2);
            g.drawRect(w - 1, 1, 1, h - 2);
            g.endFill();
            return s;
        }

        private function onInfoOver(e:MouseEvent):void {
            try {
                infoOver = true;
                if (tip == null && ownTip == null) {
                    showTip();
                }
                drawInfo();
            } catch (err:Error) {
            }
        }

        private function onInfoOut(e:MouseEvent):void {
            try {
                infoOver = false;
                if (!infoPinned) {
                    hideTip();
                }
                drawInfo();
            } catch (err:Error) {
            }
        }

        // a click opens the tooltip until the next click (on the (i) or anywhere else)
        private function onInfoClick(e:MouseEvent):void {
            try {
                infoPinned = !infoPinned;
                if (infoPinned) {
                    if (tip == null && ownTip == null) {
                        showTip();
                    }
                    listenStage(true);
                } else {
                    listenStage(false);
                    hideTip();
                }
                drawInfo();
            } catch (err:Error) {
            }
        }

        private function onStageDown(e:MouseEvent):void {
            try {
                var t:DisplayObject = e.target as DisplayObject;
                if (info != null && t != null && (t == info || info.contains(t))) {
                    return; // the (i)'s own click
                }
                infoPinned = false;
                listenStage(false);
                if (!infoOver) {
                    hideTip();
                }
                drawInfo();
            } catch (err:Error) {
            }
        }

        // the stage's MOUSE_DOWN while the tooltip is pinned (the click that closes it), else none
        private function listenStage(on:Boolean):void {
            if (downOn != null) {
                try {
                    downOn.removeEventListener(MouseEvent.MOUSE_DOWN, onStageDown);
                } catch (err:Error) {
                }
                downOn = null;
            }
            if (on && info != null && info.stage != null) {
                downOn = info.stage;
                downOn.addEventListener(MouseEvent.MOUSE_DOWN, onStageDown, false, 0, false);
            }
        }

        // a tab (the game's tab's ButtonEvent.CLICK, or a click on ours): the server is asked; its state then turns the
        // tab on and refreshes the list
        private function onTab(id:String):void {
            try {
                if (id == (pending != null ? pending : board)) {
                    return;
                }
                if (channel.call("leaderboard.select", {board: id})) {
                    pending = id;
                    paint();
                }
            } catch (err:Error) {
            }
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
