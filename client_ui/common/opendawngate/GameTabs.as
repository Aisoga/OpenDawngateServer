// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// A row of tabs in the game's own look, for a class of ours that adds tabs to a front-end screen (the Leaderboard's
// STANDARD, DRAFT, CUSTOM; the Profile's game types):
//
//     var tabs:GameTabs = new GameTabs("ProfileModes", onPick);   // onPick(id:String) at a click
//     container.addChild(tabs.bar);                               // first: the game's buttons draw once on a stage
//     tabs.make(["practice", "all"], ["Practice", "All Modes"], 87, true, 146);
//     tabs.bar.x = ...; tabs.bar.scaleX = tabs.bar.scaleY = unit;
//     tabs.paint("all");                                          // the selected one; again at every change
//     tabs.drop();                                                // the row out of its container, the listeners off
//
// The tabs are the game's tab, Tab000 of the always loaded Controls_Source bundle (the Collection's shaper details' tab
// row: a maxis.controls.Button with its art for each state, its $Header3 label in small caps, grey, white on hover,
// orange-red when selected; SFTabsView's row: 28 high, the selected one 6 taller, 1 apart, sized by setSize, whose
// scaling grid keeps the art). Only where that symbol can't be made, tabs of our own in the After Action Review's
// colours (flat rectangles, $Body 13 in upper case, 26 high, 3 apart), and one Assert line per client run and class.
// Hover and press of the game's tabs are the button's own frames. Nothing here calls a native of the screen's; the
// buttons call Global.OnInteractEvent (the game's UI sounds), as every button of the game does.
package opendawngate {
    import flash.display.Graphics;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.text.TextFormatAlign;
    import flash.utils.getDefinitionByName;

    public class GameTabs {
        public static const EA_TAB:String = "Tab000";
        public static const EA_H:Number = 28;         // a tab's height (SFTabsView)
        public static const EA_SELECTED:Number = 6;   // the selected one's more
        public static const EA_GAP:Number = 1;
        public static const EA_PAD:Number = 22;       // the label field's margins (18) and a little air
        // our own tabs where Tab000 can't be made (MMR2's)
        public static const OWN_W:Number = 84;        // at least
        public static const OWN_H:Number = 26;
        public static const OWN_GAP:Number = 3;
        private static const OWN_PAD:Number = 20;
        private static const FONT:String = "$Body";
        private static const FONT_SIZE:int = 13;
        private static const BRONZE:uint = 0xB3814E;
        // per state: fill, fill alpha, border alpha, text colour (AfterActionTabs')
        private static const OFF:Array = [0x282828, 0.8, 0.2, 0x9A9D8D];
        private static const OVER:Array = [0x282828, 0.8, 0.35, 0xCFD4B3];
        private static const ON:Array = [0x6E4C2C, 0.9, 0.7, 0xFBEEDD];
        private static var told:Object = {};          // the classes whose fallback Assert line was written

        public var bar:Sprite = new Sprite();         // the row; its owner places and scales it
        private var who:String;
        private var onPick:Function;
        private var tabs:Array = [];                  // Tab000s (or our Sprites); ids[i] is tab i's id
        private var ids:Array = [];
        private var widths:Array = [];
        private var ea:Boolean = false;
        private var eaClick:String = "buttonClick";   // scaleform.clik.events.ButtonEvent.CLICK
        private var hover:int = -1;                   // our own tabs' hover (Tab000 draws its own)
        private var on:String = null;

        public function GameTabs(who:String, onPick:Function) {
            this.who = who;
            this.onPick = onPick;
        }

        // whether the tabs are the game's Tab000
        public function get isGame():Boolean {
            return ea;
        }

        // the ids of the tabs made
        public function get made():Array {
            return ids.concat();
        }

        // the row's width and height (the selected tab's) in the bar's units
        public function get rowWidth():Number {
            var w:Number = 0;
            for each (var x:Number in widths) {
                w += x;
            }
            return w + Math.max(0, widths.length - 1) * (ea ? EA_GAP : OWN_GAP);
        }

        public function get rowHeight():Number {
            return ea ? EA_H + EA_SELECTED : OWN_H;
        }

        // Makes the tabs (the bar already in a container on the stage, so the buttons draw at once). The game's: as
        // wide as least (or wider for a label that needs more: its text width and EA_PAD), at most most; all of the
        // widest one's width when equal. Ours where the game's can't be made. Returns whether they are the game's.
        public function make(newIds:Array, labels:Array, least:Number, equal:Boolean, most:Number):Boolean {
            clear();
            ids = newIds.concat();
            if (makeGame(labels, least, equal, most)) {
                return true;
            }
            makeOwn(labels);
            return false;
        }

        private function makeGame(labels:Array, least:Number, equal:Boolean, most:Number):Boolean {
            var made:Array = [];
            try {
                try {
                    var be:Object = getDefinitionByName("scaleform.clik.events.ButtonEvent");
                    if (be != null && be.CLICK is String) {
                        eaClick = String(be.CLICK);
                    }
                } catch (err0:Error) {
                }
                var C:Class = getDefinitionByName(EA_TAB) as Class;
                var need:Array = [];
                for (var i:int = 0; i < ids.length; i++) {
                    var t:Object = new C();
                    // a button's members (a sealed class without one throws a ReferenceError here)
                    if (!(t is Sprite) || !(t.selected is Boolean) || !(t.setSize is Function) ||
                        !(t.validateNow is Function)) {
                        throw new Error("not a button");
                    }
                    made.push(t);
                    bar.addChild(t as Sprite);
                    t.toggle = false;          // the owner sets selected itself (the server's choice)
                    t.focusable = false;
                    t.label = String(labels[i]);
                    t.validateNow();
                    need.push(t.textField != null ? Math.ceil(Number(t.textField.textWidth) + EA_PAD) : least);
                }
                widths = [];
                var widest:Number = 0;
                for each (var n:Number in need) {
                    widest = Math.max(widest, n);
                }
                var x:Number = 0;
                for (i = 0; i < made.length; i++) {
                    var w:Number = Math.min(most, Math.max(least, equal ? widest : need[i]));
                    widths.push(w);
                    made[i].x = x;
                    x += w + EA_GAP;
                    made[i].addEventListener(eaClick, onTab, false, 0, false);
                }
                tabs = made;
                ea = true;
                return true;
            } catch (err:Error) {
                for each (var m:Object in made) {
                    if (m.parent != null) {
                        m.parent.removeChild(m);
                    }
                }
                if (!told[who]) {
                    told[who] = true;
                    try {
                        ExternalInterface.call("Assert", who + ": tab symbol " + EA_TAB + ": " + err.message);
                    } catch (err2:Error) {
                    }
                }
            }
            ea = false;
            widths = [];
            return false;
        }

        private function makeOwn(labels:Array):void {
            var x:Number = 0;
            for (var i:int = 0; i < ids.length; i++) {
                var t:Sprite = new Sprite();
                t.x = x;
                t.buttonMode = true;
                t.mouseChildren = false;
                var tf:TextField = new TextField();
                tf.embedFonts = true;
                tf.selectable = false;
                tf.mouseEnabled = false;
                var fmt:TextFormat = new TextFormat(FONT, FONT_SIZE, OFF[3]);
                fmt.align = TextFormatAlign.CENTER;
                fmt.letterSpacing = 1;
                tf.defaultTextFormat = fmt;
                tf.text = String(labels[i]).toUpperCase();
                var w:Number = Math.max(OWN_W, Math.ceil(tf.textWidth + OWN_PAD));
                tf.width = w;
                tf.height = OWN_H;
                tf.y = Math.round((OWN_H - tf.textHeight) / 2) - 2;
                t.addChild(tf);
                t.addEventListener(MouseEvent.CLICK, onTab, false, 0, false);
                t.addEventListener(MouseEvent.ROLL_OVER, onOver, false, 0, false);
                t.addEventListener(MouseEvent.ROLL_OUT, onOut, false, 0, false);
                bar.addChild(t);
                tabs.push(t);
                widths.push(w);
                x += w + OWN_GAP;
            }
        }

        // The row out of its container and every listener off (our own objects only).
        public function drop():void {
            clear();
            if (bar.parent != null) {
                bar.parent.removeChild(bar);
            }
        }

        // the tabs out of the row, their listeners off
        private function clear():void {
            for each (var t:Object in tabs) {
                t.removeEventListener(eaClick, onTab);
                t.removeEventListener(MouseEvent.CLICK, onTab);
                t.removeEventListener(MouseEvent.ROLL_OVER, onOver);
                t.removeEventListener(MouseEvent.ROLL_OUT, onOut);
                if (t.parent != null) {
                    t.parent.removeChild(t);
                }
            }
            tabs = [];
            ids = [];
            widths = [];
            hover = -1;
        }

        // The selected tab: the game's selected and 6 taller (SFTabsView's updateSelectionDisplay), the others not; ours
        // each in its state's colours.
        public function paint(selected:String):void {
            on = selected;
            if (ea) {
                for (var k:int = 0; k < tabs.length; k++) {
                    var b:Object = tabs[k];
                    var sel:Boolean = ids[k] == on;
                    b.setSize(widths[k], sel ? EA_H + EA_SELECTED : EA_H);
                    if (b.selected != sel) {
                        b.selected = sel;
                    }
                    b.validateNow();
                }
                return;
            }
            for (var i:int = 0; i < tabs.length; i++) {
                var st:Array = ids[i] == on ? ON : (i == hover ? OVER : OFF);
                var t:Sprite = tabs[i];
                var w:Number = widths[i];
                var g:Graphics = t.graphics;
                g.clear();
                g.beginFill(st[0], st[1]);
                g.drawRect(1, 1, w - 2, OWN_H - 1);
                g.endFill();
                g.beginFill(BRONZE, st[2]);
                g.drawRect(0, 0, w, 1);
                g.drawRect(0, 1, 1, OWN_H - 1);
                g.drawRect(w - 1, 1, 1, OWN_H - 1);
                g.endFill();
                (t.getChildAt(0) as TextField).textColor = st[3];
            }
        }

        private function onOver(e:MouseEvent):void {
            try {
                hover = tabs.indexOf(e.currentTarget);
                paint(on);
            } catch (err:Error) {
            }
        }

        private function onOut(e:MouseEvent):void {
            try {
                if (tabs.indexOf(e.currentTarget) == hover) {
                    hover = -1;
                    paint(on);
                }
            } catch (err:Error) {
            }
        }

        // a click: the owner hears the tab's id (not for the selected one)
        private function onTab(e:Event):void {
            try {
                var i:int = tabs.indexOf(e.currentTarget);
                if (i >= 0 && ids[i] != on) {
                    onPick(String(ids[i]));
                }
            } catch (err:Error) {
            }
        }
    }
}
