// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// Three tabs on the After Action Review, the screen after a match: SCOREBOARD (the game's own scoreboard), MATCH STATS
// (the server's page of the match just played) and RECENT GAMES (the server's list of the player's matches).
//
// The setup places this class on the review movie's root timeline with four added tags (the game's own bytecode stays
// as it is). The movie already has the code of a tab system its makers never finished: the class
// maxis.AfterActionReview has the hidden button mcScoreBoardBtn wired to _OnTabButtonClicked, which fades the shown
// panel out and the button's `data` panel in (_SwitchTabFocus). The class draws its three tabs as the first row inside
// the review's panel: a tab bar at the panel's top left, three tabs of equal width and a thin bronze line under them,
// in the colours of the history page's own tab row (the active tab dark bronze with a bronze border and light text,
// the others near-black with a dim border and grey text; the movie's own font). The scoreboard moves below the bar,
// scaled down to the height left (its aspect kept, centred), and the web panel takes the same area. A tab switches
// panels through that hidden button: it sets the button's data to the panel and dispatches the button's click, so the
// game's own fade runs.
//
// The two web tabs share one panel with one web view, the game's own maxis.WebView (the class the main menu's home page
// uses, found by name in the shared application domain; our own small view on the WebUI functions if a build lacks
// it). The view is made at the first click on a web tab and loads that tab's page at each switch (a click on the
// shown web tab loads it again: back to the list). Its texture has the panel's size in window pixels, under a holder
// that undoes the movie's scale, so the page is drawn 1:1 and sharp (about 1074 x 562 in a 1440 x 810 window); a change
// of the scale (a window resize) resizes the texture. The view is destroyed when the movie leaves the stage. The game
// loads the movie anew for every review and defines this class anew with it, so the class keeps nothing in statics.
//
// The pages come from the server the copy is set up for: the setup writes the web content's root into the same
// archive (Battlegrounds/dgweb.json; the root the client config's web content URLs use):
//     dawngate-web<TAB>1
//     common<TAB>http://<host>:<port>/battlegrounds_test/live/www/client/1/common
//     end
// MATCH STATS is <common>/history.html?embed=1&latest=1, RECENT GAMES <common>/history.html?embed=1; the page finds
// the player itself (Client.getLocalPlayerName). Without the file, or without the scoreboard, the class does nothing
// and the review stays as the game made it.
//
// Build: re/scripts/build_picker.py build aftaction (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObject;
    import flash.display.Graphics;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.MouseEvent;
    import flash.geom.Rectangle;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.text.TextFormatAlign;
    import flash.utils.getDefinitionByName;

    public class AfterActionTabs extends MovieClip {
        private static const CONFIG:String = "Battlegrounds/dgweb.json";
        private static const MAGIC:String = "dawngate-web\t1";
        private static const PAGE:String = "/history.html?embed=1";
        private static const LABELS:Array = ["SCOREBOARD", "MATCH STATS", "RECENT GAMES"];
        private static const QUERIES:Array = ["", "&latest=1", ""];
        private static const TEXTURE:String = "AfterActionTabs_Web";
        // the tab bar, in the movie's units (about 1.05 window pixels each in a 1440 x 810 window)
        private static const TAB_W:Number = 176;
        private static const TAB_H:Number = 30;
        private static const TAB_GAP:Number = 2;
        private static const BAR_H:Number = 34; // the tabs, the line under them and a gap: the panels start here
        private static const FONT:String = "$Body"; // the review's own text font
        private static const FONT_SIZE:int = 14;
        private static const BRONZE:uint = 0xB3814E;
        private static const LINE_ALPHA:Number = 0.45;
        // per state: fill, fill alpha, border alpha, text colour (the history page's tab row)
        private static const OFF:Array = [0x282828, 0.8, 0.2, 0x9A9D8D];
        private static const OVER:Array = [0x282828, 0.8, 0.35, 0xCFD4B3];
        private static const ON:Array = [0x6E4C2C, 0.9, 0.7, 0xFBEEDD];
        private static const HOOK_FRAME:int = 12; // the scoreboard is placed and filled by then
        private static const GIVE_UP_FRAME:int = 600;
        private static const SCALE_CHECK_FRAMES:int = 15;

        private var common:String = null; // the web content's root (dgweb.json), null without it
        private var configDone:Boolean = false;
        private var frames:int = 0;
        private var hooked:Boolean = false;
        private var gone:Boolean = false;
        private var tabs:Array = []; // Sprites, each with a TextField
        private var hover:int = -1;
        private var current:int = 0;
        private var loaded:int = -1; // the tab whose page the view shows
        private var bounds:Rectangle; // the panels' area below the bar, in this sprite's (= the root's) units
        private var webPanel:MovieClip;
        private var holder:Sprite;
        private var view:Object = null; // maxis.WebView, or OwnView
        private var scale:Number = 0;
        private var texW:int = 0;
        private var texH:int = 0;

        public function AfterActionTabs() {
            super();
            mouseEnabled = false;
            addEventListener(Event.ENTER_FRAME, onFrame);
            addEventListener(Event.REMOVED_FROM_STAGE, onGone);
            var ld:URLLoader = new URLLoader();
            ld.addEventListener(Event.COMPLETE, function (e:Event):void {
                configDone = true;
                common = parse(String(ld.data));
            });
            ld.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void { configDone = true; });
            try {
                ld.load(new URLRequest(CONFIG));
            } catch (err:Error) {
                configDone = true;
            }
        }

        // the web root from dgweb.json's text, null when the file isn't ours or names none
        private static function parse(text:String):String {
            var lines:Array = text.replace(/\r/g, "").split("\n");
            if (lines.length < 2 || lines[0] != MAGIC) {
                return null;
            }
            for each (var line:String in lines) {
                var f:Array = line.split("\t");
                if (f.length == 2 && f[0] == "common") {
                    var u:String = String(f[1]).replace(/\/+$/, "");
                    if (/^https?:\/\/[^\s"'<>]+$/.test(u)) {
                        return u;
                    }
                }
            }
            return null;
        }

        private function onFrame(e:Event):void {
            frames++;
            if (gone) {
                return;
            }
            if (!hooked) {
                if (frames >= GIVE_UP_FRAME || (configDone && common == null)) {
                    removeEventListener(Event.ENTER_FRAME, onFrame);
                } else if (frames >= HOOK_FRAME && common != null && root != null && root["mcScoreboard"] != null) {
                    hooked = true;
                    try {
                        hook();
                    } catch (err:Error) {
                        // the review stays the game's own
                    }
                }
                return;
            }
            if (frames % SCALE_CHECK_FRAMES == 0) {
                resize();
            }
        }

        private function hook():void {
            var sb:DisplayObject = root["mcScoreboard"] as DisplayObject;
            var whole:Rectangle = sb.getBounds(this);
            bounds = new Rectangle(whole.x, whole.y + BAR_H, whole.width, whole.height - BAR_H);
            // the bar: the tabs from the panel's left edge, the line under them across the panel
            var bar:Sprite = new Sprite();
            bar.x = whole.x;
            bar.y = whole.y;
            bar.graphics.beginFill(BRONZE, LINE_ALPHA);
            bar.graphics.drawRect(0, TAB_H, whole.width, 1);
            bar.graphics.endFill();
            addChild(bar);
            for (var i:int = 0; i < LABELS.length; i++) {
                var t:Sprite = new Sprite();
                t.x = i * (TAB_W + TAB_GAP);
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
                tf.width = TAB_W;
                tf.height = TAB_H;
                tf.text = LABELS[i];
                tf.y = Math.round((TAB_H - tf.textHeight) / 2) - 2;
                t.addChild(tf);
                t.addEventListener(MouseEvent.CLICK, onTab);
                t.addEventListener(MouseEvent.ROLL_OVER, onOver);
                t.addEventListener(MouseEvent.ROLL_OUT, onOut);
                bar.addChild(t);
                tabs.push(t);
            }
            paint();
            // the scoreboard below the bar: scaled to the height left, its aspect kept, centred
            var k:Number = Math.min(bounds.width / whole.width, bounds.height / whole.height);
            sb.scaleX *= k;
            sb.scaleY *= k;
            var b:Rectangle = sb.getBounds(this);
            sb.x += bounds.x + (bounds.width - b.width) / 2 - b.x;
            sb.y += bounds.y - b.y;
            webPanel = new MovieClip();
            webPanel.x = bounds.x;
            webPanel.y = bounds.y;
            webPanel.visible = false;
            webPanel.alpha = 0;
            webPanel.graphics.beginFill(0x000000, 0.35);
            webPanel.graphics.drawRect(0, 0, bounds.width, bounds.height);
            webPanel.graphics.endFill();
            addChildAt(webPanel, 0);
            holder = new Sprite();
            webPanel.addChild(holder);
            resize();
        }

        // each tab in its state's colours: a fill, a border on the top and the sides, the label
        private function paint():void {
            for (var i:int = 0; i < tabs.length; i++) {
                var st:Array = i == current ? ON : (i == hover ? OVER : OFF);
                var t:Sprite = tabs[i];
                var g:Graphics = t.graphics;
                g.clear();
                g.beginFill(st[0], st[1]);
                g.drawRect(1, 1, TAB_W - 2, TAB_H - 1);
                g.endFill();
                g.beginFill(BRONZE, st[2]);
                g.drawRect(0, 0, TAB_W, 1);
                g.drawRect(0, 1, 1, TAB_H - 1);
                g.drawRect(TAB_W - 1, 1, 1, TAB_H - 1);
                g.endFill();
                var tf:TextField = t.getChildAt(0) as TextField;
                tf.textColor = st[3];
            }
        }

        private function onOver(e:MouseEvent):void {
            hover = tabs.indexOf(e.currentTarget);
            paint();
        }

        private function onOut(e:MouseEvent):void {
            if (tabs.indexOf(e.currentTarget) == hover) {
                hover = -1;
                paint();
            }
        }

        // the texture at the panel's size in window pixels, the holder at 1/scale (the page drawn 1:1)
        private function resize():void {
            var s:Number = webPanel.transform.concatenatedMatrix.a;
            if (!(s > 0) || Math.abs(s - scale) < 0.0001) {
                return;
            }
            scale = s;
            holder.scaleX = holder.scaleY = 1 / s;
            texW = Math.round(bounds.width * s);
            texH = Math.round(bounds.height * s);
            if (view != null && !(view is OwnView)) {
                view.textureWidth = texW;
                view.textureHeight = texH;
            }
        }

        private function onTab(e:Event):void {
            var i:int = tabs.indexOf(e.currentTarget);
            if (i >= 0) {
                try {
                    select(i);
                } catch (err:Error) {
                }
            }
        }

        private function select(i:int):void {
            var again:Boolean = i == current;
            current = i;
            paint();
            if (i > 0 && makeView() && (loaded != i || again)) {
                loaded = i;
                view.loadURL(common + PAGE + QUERIES[i]);
            }
            if (again) {
                return;
            }
            var r:Object = root;
            var panel:MovieClip = i == 0 ? r.mcScoreboard as MovieClip : webPanel;
            try {
                // the game's own switch: the hidden tab button's handler fades the shown panel out and this one in
                var be:Class = getDefinitionByName("scaleform.clik.events.ButtonEvent") as Class;
                var btn:Object = r.mcScoreBoardBtn;
                btn.data = panel;
                btn.dispatchEvent(new be("buttonClick"));
            } catch (err:Error) {
                r.mcScoreboard.visible = i == 0;
                r.mcScoreboard.alpha = 1;
                webPanel.visible = i != 0;
                webPanel.alpha = 1;
            }
        }

        private function makeView():Boolean {
            if (view != null) {
                return true;
            }
            try {
                var wv:Class = getDefinitionByName("maxis.WebView") as Class;
                var v:Object = new wv();
                holder.addChild(v as DisplayObject);
                v.textureWidth = texW;
                v.textureHeight = texH;
                v.textureName = TEXTURE;
                view = v;
            } catch (err:Error) {
                view = null;
            }
            if (view == null) {
                var o:OwnView = new OwnView(TEXTURE, texW, texH);
                holder.addChild(o);
                if (o.create() >= 0) {
                    view = o;
                }
            }
            return view != null;
        }

        private function onGone(e:Event):void {
            if (gone) {
                return;
            }
            gone = true;
            removeEventListener(Event.ENTER_FRAME, onFrame);
            if (view != null) {
                try {
                    view.clear();
                } catch (err:Error) {
                }
            }
            view = null;
        }
    }
}

import flash.display.Loader;
import flash.display.Sprite;
import flash.events.MouseEvent;
import flash.external.ExternalInterface;
import flash.geom.Point;
import flash.net.URLRequest;

// The fallback view: the engine's WebUI functions as maxis.WebView calls them (a texture img://generated/<name> that
// the engine draws the page into, and the mouse forwarded from a transparent scrim). Its size stays the first one.
class OwnView extends Sprite {
    private var id:int = -1;
    private var tex:String;
    private var w:int;
    private var h:int;
    private var loader:Loader = new Loader();
    private var scrim:Sprite = new Sprite();

    public function OwnView(tex:String, w:int, h:int) {
        this.tex = tex;
        this.w = w;
        this.h = h;
        addChild(loader);
        loader.mouseEnabled = false;
        scrim.graphics.beginFill(0xFFFFFF, 0);
        scrim.graphics.drawRect(0, 0, w, h);
        scrim.graphics.endFill();
        addChild(scrim);
    }

    public function create():int {
        var r:Object = ExternalInterface.call("WebUI.ViewCreate", tex, w, h, this);
        id = r == null ? -1 : int(r);
        if (id >= 0) {
            loader.load(new URLRequest("img://generated/" + tex));
            scrim.addEventListener(MouseEvent.MOUSE_DOWN, down);
            scrim.addEventListener(MouseEvent.MOUSE_MOVE, move);
            scrim.addEventListener(MouseEvent.MOUSE_WHEEL, wheel);
        }
        return id;
    }

    public function loadURL(u:String):void {
        if (id >= 0) {
            ExternalInterface.call("WebUI.ViewLoadURL", id, u);
        }
    }

    public function clear():void {
        if (id >= 0) {
            loader.unload();
            ExternalInterface.call("WebUI.ViewDestroy", id);
            id = -1;
        }
    }

    // called by the engine
    public function OnLoadStateChanged(s:uint):void {
    }

    public function OnURLUpdated(u:String, secure:Boolean):void {
    }

    private function down(e:MouseEvent):void {
        ExternalInterface.call("WebUI.ViewOnFocusChange", id, true);
        ExternalInterface.call("WebUI.ViewOnMouseDown", id, 0, e.localX, e.localY, e.ctrlKey, e.altKey, e.shiftKey);
        if (stage) {
            stage.addEventListener(MouseEvent.MOUSE_UP, up);
        }
    }

    private function up(e:MouseEvent):void {
        if (stage) {
            stage.removeEventListener(MouseEvent.MOUSE_UP, up);
        }
        var p:Point = scrim.globalToLocal(new Point(e.stageX, e.stageY));
        ExternalInterface.call("WebUI.ViewOnMouseUp", id, 0, p.x, p.y, e.ctrlKey, e.altKey, e.shiftKey);
    }

    private function move(e:MouseEvent):void {
        ExternalInterface.call("WebUI.ViewOnMouseMove", id, e.localX, e.localY, e.ctrlKey, e.altKey, e.shiftKey);
    }

    private function wheel(e:MouseEvent):void {
        ExternalInterface.call("WebUI.ViewOnMouseWheel", id, e.delta, e.localX, e.localY, e.ctrlKey, e.altKey, e.shiftKey);
    }
}
