// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The item shop's inline refund: a purchase made during this visit to the base can be taken back for its full price,
// right where an item is sold.
//
// The server takes every sale of an item that was bought during the current visit (and not used, sold or moved since)
// as an undo of that purchase: the item before it comes back (an upgrade's component, one less in a stack, or an empty
// slot) and the full price is paid back. Leaving the shop's reach while alive ends the visit. This class only shows
// that rule in the shop; the server stays the authority. It changes nothing of the game's code and draws nothing of
// its own: everything comes from the shop movie's own symbols, font and colours.
//
//  - A slot whose newest purchase can be undone wears a small badge at the icon's bottom left: the shop's own vim
//    symbol (the flame above the sell strip's price; ShopVim). One badge per slot, for its newest purchase; after a
//    refund it stands for the step before (Time, then Motion over it: the first refund gives Time back, the second
//    empties the slot). A stack of potions has one badge and gives back one potion a click.
//  - Selecting such a slot puts the shop in its own sell layout (the game's click); the class changes only the texts:
//    the button says Refund, the line above it "Refund for 380 vim?" and the price under the coin "380". The click
//    is the game's own Sell button with its sounds; the server turns the sale into the refund.
//  - The slot's tooltip says "Refunds for: 380" in place of "Sells for: 190".
//  - An upgrade's refund also gives the item before it back (Motion's gives Time back): then the line says "Refund for
//    Time and 480 vim?", the tooltip "Refunds for: Time and 480", and the strip is the shop's purchase strip of that
//    upgrade mirrored: Motion left, a fork in the game's own line style splitting from it into two arrows, the upper
//    to Time's icon (ours, as the strip's), the lower to the game's coin and "480" under it, the game's button where
//    Purchase is. The class moves the game's strip parts there and draws only the fork (the movie has a fork merging
//    two into one, none splitting one into two); the game's sell layout comes back through its own SetInteraction.
//
// It mirrors conservatively, since a badge the server doesn't honour would turn the click into a plain sale at half
// price: a purchase counts only when the class saw it while the shop was shown (one slot and the gold changing in the
// same frame); any other change of a slot (a use, a sale, a swap) drops that slot's history; everything is dropped when
// the out-of-reach banner shows, also while the shop is closed (the game doesn't update the shop's movie then, so the
// class asks for the banner itself through the shop's own tab call, REFUND5), and when the shop opens after being
// closed for KEEP_CLOSED_MS or more without the class having seen an answer to that question. After a refund the slot is selected again, so the shop shows the item that came back.
// A purchase's price is the one the purchase layout showed for the item (every purchase of the shop's own UI), else
// the gold drop, which income in the same frames can make a little short.
//
// Texts: three lines of the HUD string table that the server's setup adds (LINE_*; English fallbacks without them).
// Settings (for tests): an optional HUD/dgrefund.json in the same archive, "dawngate-refund <TAB> 1" and then
// "<key> <TAB> <value>" lines: gate <s> (show nothing before s seconds after the shop first showed; the history is kept
// from the start), debug 1 (a text box right of the shop with the class's state and events).
//
// Build: re/scripts/build_picker.py build itemshop (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.display.Bitmap;
    import flash.display.BitmapData;
    import flash.geom.Matrix;
    import flash.geom.Rectangle;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    public class ShopRefund extends MovieClip {
        // the inventory slots (ItemShopUI's public clips); C++ registers MaxisItemShop.OnInvClicked on each with the
        // slot's number as data (0-5, 9, 10)
        private static const SLOTS:Array = ["mcInv0", "mcInv1", "mcInv2", "mcInv3", "mcInv4", "mcInv5", "mcCons0", "mcCons1"];
        private static const ROLL_OVER_ITEM:int = 2; // Common.kRollOverTypeItem (a spell slot is 7: never undoable)
        private static const SELL_X:Number = 634; // SetInteraction(kInteractSell) puts the Purchase/Sell button here
        private static const KEEP_CLOSED_MS:int = 3000; // a shop closed this long, unwatched, drops every badge
        private static const POLL_FRAMES:int = 10; // while closed, the banner is asked for this often (REFUND5)
        private static const TAB_ITEMS:uint = 1; // ItemShopUI's kTabItems, kTabConsumables, kTabSearch
        private static const TAB_CONSUMABLES:uint = 2;
        private static const TAB_SEARCH:uint = 3;
        private static const SETTLE_FRAMES:int = 10; // frames after the opening whose changes are taken as they are
        private static const WAIT_FRAMES:int = 8; // a slot change waits this long for its gold change
        private static const UNDO_SLACK:Number = 15; // vim a refund's gold rise may exceed its price (income)
        private static const MAX_COST:Number = 10000; // a larger gold drop is no purchase (the movie's placeholders)
        private static const VIM_SCALE:Number = 0.85; // the badge's height, of the sell strip's coin (mcCoins)
        private static const CONFIG:String = "HUD/dgrefund.json";
        private static const MAGIC:String = "dawngate-refund\t1";
        // the HUD string table's lines the server's setup adds (patch_locale.py, localepatch), and their fallbacks
        private static const LINE_LABEL:String = "^HUD!0x5e70c001";
        private static const LINE_WARN:String = "^HUD!0x5e70c002";
        private static const LINE_TIP:String = "^HUD!0x5e70c003";
        private static const PRICE:String = "~price~";
        private static const WARN_EN:String = "Refund for ~price~ vim?";
        private static const TIP_EN:String = "Refunds for: ~price~";
        // an upgrade's refund also gives the item before it back: the lines name it (~item~, the game's own name)
        private static const LINE_WARN_BACK:String = "^HUD!0x5e70c004";
        private static const LINE_TIP_BACK:String = "^HUD!0x5e70c005";
        private static const ITEM:String = "~item~";
        private static const WARN_BACK_EN:String = "Refund for ~item~ and ~price~ vim?";
        private static const TIP_BACK_EN:String = "Refunds for: ~item~ and ~price~";
        // an upgrade's refund is drawn as the shop's purchase strip of an upgrade, mirrored: SetInteraction's upgrade
        // positions (the shop's units) with the held item and the returned one swapped, and the fork split the other way
        private static const HELD_X:Number = 627; // mcPurchaseTarget (the held item): the component's column
        private static const HELD_Y:Number = 692; // at the target's height
        private static const BACK_X:Number = 774; // the returned item (our icon): the target's column
        private static const BACK_Y:Number = 670; // at the component's height
        private static const COIN_X:Number = 786; // mcCoins and mcCostTxt under the returned item (639 and 625 + 147)
        private static const COIN_Y:Number = 719;
        private static const COST_X:Number = 772;
        private static const COST_Y:Number = 740;
        private static const LINK_X:Number = 680; // the fork, where mcUpgradeLink sits
        private static const LINK_Y:Number = 692;
        private static const BTN_X:Number = 859; // the button where Purchase is
        private static const BTN_Y:Number = 705;
        private static const INTERACT_SELL:uint = 1; // SetInteraction(kInteractSell, ITEMSHOP_SELL): the sell layout back
        private static const IE_SELL:uint = 58;
        // the fork in mcUpgradeLink's own units: its "upgrade" shape (245: a black edge, #FFFF99 inside) mirrored, the
        // stem's head moved to both branches (the head of its "purchase" arrow, 246)
        private static const FORK_EDGE:Array = [0, 17.5, 15, 17.5, 16.55, 15.95, 16.6, 15.95, 32.55, 0, 55, 0, 56.25, -1.25,
            60, 2.5, 60, 2.55, 56.3, 6.25, 55.05, 5, 34.95, 5, 20, 20, 35, 35, 55, 35, 56.25, 33.75, 60, 37.5, 60, 37.55,
            56.3, 41.25, 55.05, 40, 32.45, 40, 14.95, 22.5, 0, 22.5];
        private static const FORK_LINE:Array = [1, 18.5, 15.65, 18.5, 33.1, 1.05, 33.1, 1, 56.35, 1, 55.55, 0.2, 56.25, -0.5,
            59.25, 2.5, 56.2, 5.55, 55.5, 4.85, 56.35, 4, 34.45, 4, 18.45, 20, 34.45, 36, 56.35, 36, 55.55, 35.2, 56.25, 34.5,
            59.25, 37.5, 56.2, 40.55, 55.5, 39.85, 56.35, 39, 33, 39, 33, 38.85, 15.65, 21.5, 1, 21.5];
        private static const FORK_EDGE_RGB:uint = 0x000000;
        private static const FORK_LINE_RGB:uint = 0xFFFF99;
        // the button's labels as SetInteraction writes them (sell; purchase and upgrade) and the shop's own sell line:
        // C++ calls SetInteraction only when its state or event changes, so selecting a slot again in the sell state
        // rewrites the price but not the label (REFUND3); the class puts the game's label back itself
        private static const LABEL_SELL:String = "^HUD!0x0ad42795";
        private static const LABEL_PURCHASE:String = "^HUD!0x0ad42796";
        private static const LINE_SELL_WARN:String = "^HUD!0x0b5a6f63";
        private static const SELL_WARN_EN:String = "Sell for ~price~ vim?";
        private var priceCache:Object = {};

        private var gateS:Number = 0;
        private var debug:Boolean = false;
        private var configDone:Boolean = false;

        private var frames:int = 0;
        private var hooked:Boolean = false;
        private var shop:Object = null;
        private var slots:Array = []; // the slot clips by index (SLOTS)
        private var last:Array = []; // each slot as last seen: {res, id, n, type}
        private var gold:Number = NaN;
        private var hist:Array = []; // per slot: [{before: key, after: key, cost}], newest last
        private var badges:Array = []; // per slot: our badge Sprite inside the slot clip, or null
        private var vimRoute:String = ""; // how the badge's symbol was made (the debug box)
        private var shownPrev:Boolean = false;
        private var hiddenAt:int = -1;
        private var hiddenFrames:int = 0; // frames since the shop was closed
        private var polls:int = 0; // OnTabChanged calls while closed
        private var hideGold:String = ""; // the gold text at the close
        private var pollSeen:Boolean = false; // the gold text changed while closed: the polls are answered
        private var firstShown:int = -1;
        private var settle:int = 0;
        private var oorPrev:Boolean = false;
        private var pend:Object = null; // {slots: {index: key before}, dg, age}
        private var shownCost:Number = NaN; // the price the purchase layout showed last frame
        private var shownTarget:String = "";
        private var sel:int = -1; // the slot the player selected last (our own click listener)
        private var refunding:Boolean = false;
        private var refundSlot:int = -1;
        private var restoreSlot:int = -1; // a slot whose texts are still ours after the refund state ended
        private var reselectAt:int = -1;
        private var tipApplied:Array = []; // per slot: the tooltip line we put in
        private var texts:Object = null; // {label, warn, tip}
        private var warnShown:String = ""; // the warning line the refund state wrote last
        private var backIcon:Object = null; // the returned item's icon (an IconLoader, as the strip's), made once
        private var backRes:String = "";
        private var fork:Sprite = null; // the split fork, made once
        private var mirrored:Boolean = false; // the strip is ours (an upgrade's refund state)
        private var names:Object = {}; // asset id -> the game's name for it
        private var textRoute:String = ""; // how the two-token lines were filled (the debug box)

        private var info:TextField = null;
        private var facts:Array = [];
        private var t0:int;

        public function ShopRefund() {
            super();
            t0 = getTimer();
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame);
            var ld:URLLoader = new URLLoader();
            ld.addEventListener(Event.COMPLETE, function (e:Event):void {
                configure(String(ld.data));
                configDone = true;
            });
            ld.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void { configDone = true; });
            try {
                ld.load(new URLRequest(CONFIG));
            } catch (err:Error) {
                configDone = true;
            }
        }

        private function configure(text:String):void {
            var lines:Array = text.replace(/\r/g, "").split("\n");
            if (lines.length < 2 || lines[0] != MAGIC) {
                return;
            }
            for each (var line:String in lines) {
                var f:Array = line.split("\t");
                if (f.length != 2) {
                    continue;
                }
                var v:String = String(f[1]);
                if (f[0] == "gate") {
                    gateS = Math.max(0, Number(v) || 0);
                } else if (f[0] == "debug") {
                    debug = v == "1";
                }
            }
        }

        private function onFrame(e:Event):void {
            frames++;
            if (frames < 12) {
                return;
            }
            if (!hooked) {
                if (!configDone && frames < 120) {
                    return;
                }
                hooked = true;
                try {
                    hook();
                } catch (err:Error) {
                    say("hook threw " + err.message);
                    hooked = false;
                    removeEventListener(Event.ENTER_FRAME, onFrame);
                    return;
                }
            }
            try {
                tick();
            } catch (err2:Error) {
                if (frames % 120 == 0) {
                    say("tick threw " + err2.message);
                }
            }
            if (debug && frames % 10 == 0) {
                showInfo();
            }
        }

        private function hook():void {
            shop = root;
            for (var i:int = 0; i < SLOTS.length; i++) {
                var s:Object = shop[SLOTS[i]];
                slots.push(s);
                last.push(null);
                hist.push([]);
                badges.push(null);
                tipApplied.push("");
                if (s != null) {
                    (s as DisplayObject).addEventListener(MouseEvent.CLICK, slotClicker(i), false, 0, false); // strong: a closure
                    (s as DisplayObject).addEventListener(MouseEvent.ROLL_OVER, slotHover(i), false, -1, false);
                }
            }
            texts = {label: line(LINE_LABEL, "Refund"), warn: priced(LINE_WARN, WARN_EN, "380"),
                     tip: priced(LINE_TIP, TIP_EN, "380")};
            if (debug) {
                info = new TextField();
                info.embedFonts = true;
                info.selectable = false;
                info.mouseEnabled = false;
                info.background = true;
                info.backgroundColor = 0x000000;
                info.multiline = true;
                info.wordWrap = true;
                info.defaultTextFormat = new TextFormat("$Body", 11, 0x80FF80);
                info.x = 1032; // right of the shop: the inventory tooltips open to the left
                info.y = 0;
                info.width = 400;
                info.height = 760;
                addChild(info);
            }
            say("hooked:" + (gateS > 0 ? " gate " + gateS : "") +
                "; texts '" + texts.label + "' '" + texts.warn + "' '" + texts.tip + "'");
        }

        // a line of the game's string tables, or the fallback when the table lacks it
        private static function line(key:String, fallback:String):String {
            try {
                var t:* = ExternalInterface.call("Global.ForceTranslateString", key);
                if (found(t)) {
                    return String(t);
                }
            } catch (err:Error) {
            }
            return fallback;
        }

        private static function found(t:*):Boolean {
            return t != null && String(t) != "" && String(t).charAt(0) != "^" && String(t).indexOf("!0x") < 0 &&
                   String(t).indexOf("*UNKNOWN") < 0;
        }

        // a line with its ~price~ filled by the game's own token replacement (as the chat fills ~user~; the plain
        // translation would turn the token into *UNKNOWN<price>* and drop the rest of the line), cached
        private function priced(key:String, fallback:String, price:String):String {
            var k:String = key + "|" + price;
            if (priceCache.hasOwnProperty(k)) {
                return priceCache[k];
            }
            var out:String = fallback.split(PRICE).join(price);
            try {
                var t:* = ExternalInterface.call("Global.ForceTranslateStringReplace", key, "price", price);
                if (found(t)) {
                    out = String(t);
                }
            } catch (err:Error) {
            }
            priceCache[k] = out;
            return out;
        }

        // a line with ~item~ and ~price~ filled by the game's token replacement in one call (two pairs), else the
        // English fallback filled here; cached
        private function itemPriced(key:String, fallback:String, item:String, price:String):String {
            var k:String = key + "|" + item + "|" + price;
            if (priceCache.hasOwnProperty(k)) {
                return priceCache[k];
            }
            var out:String = fallback.split(ITEM).join(item).split(PRICE).join(price);
            var route:String = "fallback";
            try {
                var t:* = ExternalInterface.call("Global.ForceTranslateStringReplace", key, "item", item, "price", price);
                if (found(t) && String(t).indexOf(ITEM) < 0 && String(t).indexOf(PRICE) < 0) {
                    out = String(t);
                    route = "two tokens";
                }
            } catch (err:Error) {
            }
            if (textRoute != route) {
                textRoute = route;
                say("lines with ~item~: " + route);
            }
            priceCache[k] = out;
            return out;
        }

        // the game's own name of an item (as its tooltip shows it), by asset id
        private function itemName(id:String):String {
            if (names.hasOwnProperty(id)) {
                return names[id];
            }
            var n:String = "";
            try {
                var nc:Object = ExternalInterface.call("Global.GetItemNameColor", id);
                n = nc != null && nc.name != null ? String(nc.name) : "";
            } catch (err:Error) {
            }
            if (n == "") {
                n = id.replace(/\.Item$/, "").replace(/^[A-Za-z]+_/, "");
            }
            names[id] = n;
            return n;
        }

        // the entry's returned item: an upgrade's component, null for a new slot or one more in a stack
        private static function back(e:Object):Object {
            return e != null && e.backId != null && e.backId != "" ? {id: e.backId, res: e.backRes} : null;
        }

        // the strip is still ours: the held item where we put it (every SetInteraction of the game moves or hides it)
        private function ours():Boolean {
            try {
                var t:Object = shop.mcPurchaseTarget;
                return mirrored && t.visible && t.x == HELD_X && t.y == HELD_Y && shop.mcPurchaseBtn.x == BTN_X;
            } catch (err:Error) {
            }
            return false;
        }

        // an upgrade's refund state as the purchase strip of that upgrade, mirrored: the held item (the game's strip
        // item) left, our fork splitting from it, its upper head to the returned item (our icon, as the strip's), the
        // lower to the vim (the game's coin and price), the game's button where Purchase is; nothing of the game's is
        // drawn by us but the fork, whose split with two heads the movie has no art for
        private function mirror(b:Object):void {
            try {
                var t:Object = shop.mcPurchaseTarget;
                var link:Object = shop.mcUpgradeLink;
                if (!mirrored) {
                    say("strip mirrored: " + b.id);
                }
                mirrored = true;
                t.x = HELD_X;
                t.y = HELD_Y;
                link.visible = false;
                shop.mcCoins.x = COIN_X;
                shop.mcCoins.y = COIN_Y;
                shop.mcCostTxt.x = COST_X;
                shop.mcCostTxt.y = COST_Y;
                shop.mcPurchaseBtn.x = BTN_X;
                shop.mcPurchaseBtn.y = BTN_Y;
                if (fork == null) {
                    fork = new Sprite();
                    poly(fork, FORK_EDGE, FORK_EDGE_RGB);
                    poly(fork, FORK_LINE, FORK_LINE_RGB);
                    addChild(fork);
                }
                fork.scaleX = link.scaleX; // the link's own placement (1.47 x 1.2)
                fork.scaleY = link.scaleY;
                fork.x = LINK_X;
                fork.y = LINK_Y;
                fork.visible = true;
                if (backIcon == null) {
                    var c:Class = getDefinitionByName("maxis.controls.IconLoader") as Class;
                    backIcon = new c();
                    addChild(backIcon as DisplayObject);
                }
                if (backRes != b.res) {
                    backRes = b.res;
                    backIcon.LoadIcon(b.res, 3); // Common.kIconSizeStretch
                }
                // the strip item's icon inside its button clip: the same size and offset for ours
                var r:Rectangle = (t.mcIcon as DisplayObject).getBounds(t as DisplayObject);
                if (!(r.width > 4) || !(r.height > 4)) {
                    r = new Rectangle(2, 2, 44, 44);
                }
                backIcon.width = r.width;
                backIcon.height = r.height;
                backIcon.x = BACK_X + r.x;
                backIcon.y = BACK_Y + r.y;
                backIcon.visible = true;
            } catch (err:Error) {
                say("mirror threw " + err.message);
            }
        }

        // the game's own sell layout again, if the strip is still ours (else the game has laid it out since)
        private function unmirror():void {
            if (fork != null) {
                fork.visible = false;
            }
            if (backIcon != null) {
                backIcon.visible = false;
            }
            if (!mirrored) {
                return;
            }
            var still:Boolean = ours();
            mirrored = false;
            if (still) {
                try {
                    shop.SetInteraction(INTERACT_SELL, IE_SELL); // the movie's own layout code
                } catch (err:Error) {
                    say("SetInteraction threw " + err.message);
                }
            }
            say("strip back" + (still ? " (sell layout)" : " (the game's own since)"));
        }

        // a closed polygon (x, y pairs in the fork's units) filled in a colour
        private static function poly(s:Sprite, p:Array, rgb:uint):void {
            s.graphics.beginFill(rgb, 1);
            s.graphics.moveTo(p[0], p[1]);
            for (var i:int = 2; i < p.length; i += 2) {
                s.graphics.lineTo(p[i], p[i + 1]);
            }
            s.graphics.lineTo(p[0], p[1]);
            s.graphics.endFill();
        }

        private function slotClicker(i:int):Function {
            return function (e:MouseEvent):void {
                sel = i;
                try {
                    show(getTimer(), oorPrev); // the game's handler ran first: no frame with the sale's texts
                } catch (err:Error) {
                }
            };
        }

        private function slotHover(i:int):Function {
            return function (e:MouseEvent):void {
                tipApplied[i] = "";
                try {
                    tooltip(i);
                } catch (err:Error) {
                }
            };
        }

        private function secs():String {
            return ((getTimer() - t0) / 1000).toFixed(1);
        }

        private function say(msg:String):void {
            if (!debug) {
                return;
            }
            facts.push(secs() + " " + msg);
            if (facts.length > 40) {
                facts.shift();
            }
        }

        // ---- reading the shop ----

        private static function num(t:String):Number {
            var d:String = String(t).replace(/[^0-9]/g, "");
            return d == "" ? NaN : Number(d);
        }

        private function readSlot(i:int):Object {
            var s:Object = slots[i];
            if (s == null) {
                return {res: "", id: "", n: 0, type: 0};
            }
            var res:String = "";
            try {
                res = s.mcIcon.Resource == null ? "" : String(s.mcIcon.Resource);
            } catch (err:Error) {
            }
            var id:String = s.mRollOverId is String ? String(s.mRollOverId) : "";
            var n:int = 0;
            if (res != "") {
                n = 1;
                try {
                    if (s.mcStacks.visible && num(s.mcStacks.text) > 0) {
                        n = int(num(s.mcStacks.text));
                    }
                } catch (err2:Error) {
                }
            }
            return {res: res, id: id, n: n, type: int(s.mRollOverType)};
        }

        private static function key(o:Object):String {
            return o.res == "" ? "" : o.res + "#" + o.n;
        }

        private function newest(i:int):Object {
            var h:Array = hist[i];
            return h.length > 0 ? h[h.length - 1] : null;
        }

        // the slot's newest entry if the slot is still what that purchase left
        private function honoured(i:int):Object {
            var e:Object = newest(i);
            return e != null && last[i] != null && key(last[i]) == e.after ? e : null;
        }

        private function clearAll(why:String):void {
            var n:int = 0;
            for (var i:int = 0; i < hist.length; i++) {
                n += hist[i].length;
                hist[i] = [];
            }
            pend = null;
            if (n > 0) {
                say("cleared " + n + " (" + why + ")");
            }
        }

        // ---- each frame ----

        private function tick():void {
            var now:int = getTimer();
            if (!shop.visible) {
                if (shownPrev) {
                    hiddenAt = now;
                    shownPrev = false;
                    hiddenFrames = 0;
                    polls = 0;
                    hideGold = String(shop.mcAvailGold.text);
                    pollSeen = false;
                    say("hidden");
                    if (refunding) {
                        restoreSlot = refundSlot; // the texts are ours until the slot is selected again
                    }
                    unmirror(); // the game's sell layout before it shows again
                    refunding = false;
                }
                if (hiddenAt >= 0) {
                    watchClosed();
                }
                return;
            }
            if (!shownPrev) {
                shownPrev = true;
                settle = SETTLE_FRAMES;
                if (firstShown < 0) {
                    firstShown = now;
                    say("shown");
                } else {
                    var closedS:String = hiddenAt >= 0 ? ((now - hiddenAt) / 1000).toFixed(1) : "?";
                    say("shown (closed " + closedS + " s; " + polls + " polls, " +
                        (pollSeen ? "answered" : "no answer") + ")");
                    if (hiddenAt >= 0 && now - hiddenAt >= KEEP_CLOSED_MS && !pollSeen) {
                        clearAll("closed " + closedS + " s, unwatched");
                    }
                }
            }
            var oor:Boolean = shop.mcOOR != null && shop.mcOOR.visible;
            if (oor && !oorPrev) {
                say("banner on");
                clearAll("out of reach");
            } else if (!oor && oorPrev) {
                say("banner off");
            }
            oorPrev = oor;

            var g:Number = num(shop.mcAvailGold.text);
            var dg:Number = isNaN(gold) || isNaN(g) ? 0 : g - gold;
            var now8:Array = [];
            var changed:Array = [];
            for (var i:int = 0; i < slots.length; i++) {
                now8.push(readSlot(i));
                if (last[i] != null && key(now8[i]) != key(last[i])) {
                    changed.push(i);
                }
            }
            if (settle > 0 || oor) {
                if (settle > 0) {
                    settle--;
                }
                pend = null; // the opening's values arrive in these frames; out of reach nothing is bought
                for each (var c:int in changed) {
                    say("slot " + slotNo(c) + " changed " + (oor ? "under the banner" : "while opening") + " (gold " +
                        (dg >= 0 ? "+" : "") + dg + "): not counted" + (hist[c].length > 0 ? ", " + hist[c].length +
                        " dropped" : ""));
                    hist[c] = [];
                }
            } else if (changed.length > 0 || pend != null) {
                if (pend == null) {
                    // the purchase layout as it was before this change (REFUND4): the layout may change in the
                    // frames a slow gold change takes
                    pend = {slots: {}, dg: 0, age: 0, shown: shownCost, target: shownTarget};
                }
                for each (var j:int in changed) {
                    if (!pend.slots.hasOwnProperty(String(j))) {
                        pend.slots[String(j)] = last[j];
                    }
                }
                pend.dg += dg;
                pend.age++;
            }
            for (var k:int = 0; k < slots.length; k++) {
                last[k] = now8[k];
            }
            if (!isNaN(g)) {
                gold = g;
            }
            if (pend != null) {
                decide(pend.age >= WAIT_FRAMES);
            }
            readPurchaseLayout();
            if (reselectAt >= 0 && !oor) {
                var r:int = reselectAt;
                reselectAt = -1;
                reselect(r);
            }
            show(now, oor);
        }

        // while the shop is closed (REFUND5): C++ updates the movie only while it is shown, but the shop's own
        // MaxisItemShop.OnTabChanged (FUN_004cb080) runs ItemShopUI_Update (0x004CA680) at once, which writes the banner
        // (mcOOR = not LabsPlayer_CanUseItemShop: out of reach and alive) and the gold whatever the shop's visibility.
        // So every POLL_FRAMES frames the class asks with the tab the movie shows (no selection changes), and drops every
        // badge when the banner comes on, as the host drops its history then. The poll is taken as answered once the
        // gold text changed while closed (income); a long close without an answer drops everything, as before.
        private function watchClosed():void {
            hiddenFrames++;
            if (polls > 0) {
                var g:String = String(shop.mcAvailGold.text);
                if (g != hideGold) {
                    pollSeen = true;
                }
                var oorH:Boolean = shop.mcOOR != null && shop.mcOOR.visible;
                if (oorH && !oorPrev) {
                    say("banner on while closed");
                    clearAll("out of reach while closed");
                } else if (!oorH && oorPrev) {
                    say("banner off while closed");
                }
                oorPrev = oorH;
            }
            if (hiddenFrames % POLL_FRAMES != 1) {
                return;
            }
            var tab:uint = TAB_ITEMS;
            try {
                if (shop.mcSearchTab.visible) {
                    tab = TAB_SEARCH;
                } else if (shop.mcConsumablesTab.visible) {
                    tab = TAB_CONSUMABLES;
                }
            } catch (err:Error) {
            }
            try {
                ExternalInterface.call("MaxisItemShop.OnTabChanged", tab);
                polls++;
            } catch (err2:Error) {
                if (polls == 0 && hiddenFrames == 1) {
                    say("poll threw " + err2.message);
                }
            }
        }

        // the pending change: one slot and a gold drop is a purchase, one slot back to its newest entry's state before
        // with the gold up by its price is a refund; anything else, or nothing decided within WAIT_FRAMES, drops the
        // changed slots' history
        private function decide(timeUp:Boolean):void {
            var idx:Array = [];
            for (var s:String in pend.slots) {
                idx.push(int(s));
            }
            if (idx.length == 1) {
                var i:int = idx[0];
                var before:Object = pend.slots[String(i)];
                var after:Object = last[i];
                var e:Object = newest(i);
                if (e != null && key(after) == e.before && pend.dg >= e.cost - 1 && pend.dg <= e.cost + UNDO_SLACK) {
                    hist[i].pop();
                    say("refund seen: slot " + slotNo(i) + " +" + pend.dg + " (" + e.cost + "), back to " +
                        (e.before == "" ? "empty" : e.before) + "; left " + hist[i].length);
                    if (sel == i) {
                        reselectAt = i;
                    }
                    pend = null;
                    return;
                }
                if (pend.dg < 0 && -pend.dg <= MAX_COST && after.type == ROLL_OVER_ITEM && key(after) != "") {
                    // the drop can include the income of the same frames, so it can be short of the price the host
                    // took and gives back: take the price the shop showed for this item (every purchase of the
                    // shop's own UI has one, REFUND4); only a purchase made without the shop's UI keeps the drop
                    var cost:Number = -pend.dg;
                    var route:String = "drop";
                    if (!isNaN(pend.shown) && pend.target == after.res && Math.abs(pend.shown - cost) <= UNDO_SLACK) {
                        cost = pend.shown;
                        route = "shown";
                    }
                    if (e != null && key(before) != e.after) {
                        hist[i] = []; // the slot had changed since (the server dropped that history then)
                    }
                    var upgrade:Boolean = before != null && key(before) != "" && before.id != after.id &&
                                          before.type == ROLL_OVER_ITEM;
                    hist[i].push({before: key(before), after: key(after), cost: cost,
                                  backId: upgrade ? before.id : "", backRes: upgrade ? before.res : ""});
                    say("purchase seen: slot " + slotNo(i) + " " + after.id + " x" + after.n + " for " + cost +
                        " (" + route + ", drop " + (-pend.dg) + "); history " + hist[i].length);
                    pend = null;
                    return;
                }
            }
            if (!timeUp && pend.dg == 0) {
                return; // a slot change may still get its gold change
            }
            for each (var k:int in idx) {
                if (hist[k].length > 0) {
                    say("slot " + slotNo(k) + " changed (gold " + (pend.dg >= 0 ? "+" : "") + pend.dg + "): " +
                        hist[k].length + " dropped");
                    hist[k] = [];
                }
            }
            pend = null;
        }

        private function slotNo(i:int):String {
            try {
                return String(slots[i].mOnClickData[0]);
            } catch (err:Error) {
            }
            return String(i);
        }

        // the purchase layout's price and item, for the next frame's purchase (its exact price)
        private function readPurchaseLayout():void {
            shownCost = NaN;
            shownTarget = "";
            try {
                var b:Object = shop.mcPurchaseBtn;
                if (b.visible && b.enabled && b.x != SELL_X && !ours() && shop.mcCostTxt.visible) {
                    shownCost = num(shop.mcCostTxt.text);
                    shownTarget = String(shop.mcPurchaseTarget.mcIcon.Resource);
                }
            } catch (err:Error) {
            }
        }

        // EA's own selection of a slot (what a click on it does): the shop shows the item now in the slot
        private function reselect(i:int):void {
            try {
                var s:Object = slots[i];
                ExternalInterface.call(s.mOnClickCallbacks[0], s.mOnClickData[0]);
                say("slot " + slotNo(i) + " selected again");
            } catch (err:Error) {
            }
        }

        private function gateOpen(now:int):Boolean {
            return firstShown >= 0 && now - firstShown >= gateS * 1000;
        }

        // ---- showing it ----

        private function show(now:int, oor:Boolean):void {
            var open:Boolean = gateOpen(now) && !oor;
            for (var i:int = 0; i < slots.length; i++) {
                var on:Boolean = open && pend == null && honoured(i) != null;
                if (on && badges[i] == null) {
                    badges[i] = makeBadge(i);
                }
                if (badges[i] != null) {
                    badges[i].visible = on;
                }
                if (on) {
                    tooltip(i);
                } else if (tipApplied[i] != "") {
                    tipApplied[i] = "";
                    try {
                        slots[i].UpdateTooltip(false); // the game's own tooltip back (its "Sells for" line)
                    } catch (err0:Error) {
                    }
                }
            }
            var e:Object = null;
            var inSell:Boolean = false;
            try {
                var btn:Object = shop.mcPurchaseBtn;
                inSell = btn.visible && (btn.x == SELL_X || ours()); // the game's sell layout, or ours mirrored
                if (open && inSell && sel >= 0 && pend == null) {
                    e = honoured(sel);
                    if (e != null && String(shop.mcPurchaseTarget.mcIcon.Resource) != last[sel].res) {
                        e = null;
                    }
                }
                if (e != null) {
                    var price:String = String(e.cost);
                    var b:Object = back(e);
                    if (b != null) {
                        mirror(b); // an upgrade's refund: the purchase strip mirrored (REFUND6)
                    } else {
                        unmirror(); // a new slot or one more in a stack: the sell layout (its label: below)
                    }
                    if (btn.label != texts.label) {
                        btn.label = texts.label;
                    }
                    warnShown = b != null ? itemPriced(LINE_WARN_BACK, WARN_BACK_EN, itemName(b.id), price) :
                                priced(LINE_WARN, WARN_EN, price);
                    setText(shop.mcWarningText, warnShown);
                    setText(shop.mcCostTxt, price); // the plain amount, as the game's own sale
                    if (!refunding || refundSlot != sel) {
                        say("refund state: slot " + slotNo(sel) + " +" + price);
                    }
                    refunding = true;
                    refundSlot = sel;
                } else if (refunding) {
                    unmirror();
                    refunding = false;
                    restoreSlot = refundSlot;
                    say("refund state ends");
                }
                // the game's own texts for the slot again (it rewrites them only when its selection changes)
                if (restoreSlot >= 0 && !refunding && !oor && settle == 0) {
                    if (inSell && sel == restoreSlot) {
                        reselect(sel);
                        sellLine();
                    }
                    restoreSlot = -1;
                }
                // our label outside the refund state is stale (REFUND3: after a walk out of the base and back, a
                // close, a refund that leaves an item of an earlier visit, or a click on an unbadged item, the shop
                // stays in its sell state and C++ doesn't rewrite the label): the game's own label for the layout
                if (!refunding && btn.label == texts.label) {
                    btn.label = btn.x == SELL_X ? LABEL_SELL : LABEL_PURCHASE;
                    say("label back: " + btn.label);
                }
            } catch (err:Error) {
            }
        }

        // after the slot was selected again: the game's sell line if the warning still shows ours (the price under the
        // coin is the game's again: the selection writes it)
        private function sellLine():void {
            var f:TextField = shop.mcWarningText as TextField;
            if (f == null || warnShown == "" || f.text != warnShown) {
                return;
            }
            var price:Number = num(shop.mcCostTxt.text);
            if (!isNaN(price)) {
                setText(f, priced(LINE_SELL_WARN, SELL_WARN_EN, String(price)));
                say("sell line back: " + f.text);
            }
        }

        // a text field's text replaced, its formatting kept (the game writes them as HTML)
        private static function setText(f:TextField, want:String):void {
            if (f == null || f.text == want) {
                return;
            }
            var cur:String = f.text;
            var h:String = f.htmlText;
            if (cur != "" && h.indexOf(cur) >= 0) {
                f.htmlText = h.split(cur).join(want);
            }
            if (f.text != want) {
                f.text = want;
            }
        }

        // the slot's tooltip with the refund line in place of the sell line (the game's own tooltip, rebuilt by its
        // own helper); again whenever the game puts its own data back
        private function tooltip(i:int):void {
            var s:Object = slots[i];
            var tip:Object = s.Tooltip;
            var e:Object = honoured(i);
            if (tip == null || e == null || !gateOpen(getTimer())) {
                return;
            }
            var tb:Object = back(e);
            var want:String = tb != null ? itemPriced(LINE_TIP_BACK, TIP_BACK_EN, itemName(tb.id), String(e.cost)) :
                              priced(LINE_TIP, TIP_EN, String(e.cost));
            if (tipApplied[i] == want && tip.data != null && tip.data.rightLines is Array &&
                (tip.data.rightLines as Array).indexOf(want) >= 0) {
                return;
            }
            var id:String = s.mRollOverId is String ? String(s.mRollOverId) : "";
            if (id == "") {
                return;
            }
            var helpers:Object = getDefinitionByName("maxis.ToolTipHelpers");
            var nc:Object = ExternalInterface.call("Global.GetItemNameColor", id);
            var stats:Array = ExternalInterface.call("Global.GetItemStats", id) as Array;
            var passives:Array = ExternalInterface.call("Global.GetItemPassiveModifiers", id, s.mRolloverObjectId) as Array;
            var actives:Array = ExternalInterface.call("Global.GetItemActiveAbilities", id, s.mRolloverObjectId) as Array;
            var data:Object = helpers.BuildTooltipForInventoryItem(String(nc.name), uint(nc.nameColor), want, stats,
                                                                   passives, actives);
            tip.applyData(data);
            tipApplied[i] = want;
        }

        private function makeBadge(i:int):Sprite {
            var s:DisplayObjectContainer = slots[i] as DisplayObjectContainer;
            var r:Rectangle = null;
            try {
                r = (slots[i].mcIcon as DisplayObject).getBounds(s);
            } catch (err:Error) {
            }
            if (r == null || r.width < 4) {
                r = s.getBounds(s);
            }
            var holder:Sprite = new Sprite();
            holder.mouseEnabled = false;
            holder.mouseChildren = false;
            var art:DisplayObject = vim();
            if (art == null) {
                return holder;
            }
            // the coin's size in the slot's units, VIM_SCALE of it
            var coinH:Number = 20;
            try {
                coinH = (shop.mcCoins as DisplayObject).getBounds(shop as DisplayObject).height;
            } catch (err1:Error) {
            }
            var a:Rectangle = art.getBounds(art);
            var slotScale:Number = (s as DisplayObject).scaleY;
            if (!(slotScale > 0)) {
                slotScale = 1;
            }
            var scale:Number = coinH * VIM_SCALE / slotScale / Math.max(1, a.height);
            art.scaleX = scale;
            art.scaleY = scale;
            // its bottom left at the icon's bottom left, inset a little (nothing of the game's sits in that corner)
            var inset:Number = r.width * 0.04;
            art.x = r.left + inset - a.x * scale;
            art.y = r.bottom - inset - (a.y + a.height) * scale;
            holder.addChild(art);
            // under the stack count and the cooldown's parts, above the icon
            var under:int = -1;
            try {
                var st:DisplayObject = slots[i].mcStacks as DisplayObject;
                if (st != null && st.parent == s) {
                    under = s.getChildIndex(st);
                }
            } catch (err2:Error) {
            }
            if (under >= 0) {
                s.addChildAt(holder, under);
            } else {
                s.addChild(holder);
            }
            return holder;
        }

        // the shop's own vim symbol: the movie's character 250 (mcCoins' clip), bound to ShopVim by our SymbolClass tag;
        // if that binding didn't take (an empty clip), a bitmap copy of the shop's mcCoins
        private function vim():DisplayObject {
            try {
                var v:ShopVim = new ShopVim();
                if (v.width > 0 && v.height > 0) {
                    vimRoute = "ShopVim (character 250)";
                    return v;
                }
            } catch (err:Error) {
            }
            try {
                var c:DisplayObject = shop.mcCoins as DisplayObject;
                var b:Rectangle = c.getBounds(c);
                var bd:BitmapData = new BitmapData(Math.max(1, Math.ceil(b.width)), Math.max(1, Math.ceil(b.height)), true, 0);
                bd.draw(c, new Matrix(1, 0, 0, 1, -b.x, -b.y), null, null, null, true);
                var bm:Bitmap = new Bitmap(bd, "auto", true);
                vimRoute = "a bitmap of mcCoins";
                return bm;
            } catch (err2:Error) {
                vimRoute = "none (" + err2.message + ")";
            }
            return null;
        }

        private function showInfo():void {
            if (info == null) {
                return;
            }
            var lines:Array = ["ShopRefund  vim " + vimRoute + "  ~item~ " + textRoute + "  gate " + gateS + "  sel " + (sel >= 0 ? slotNo(sel) : "-") +
                               (refunding ? "  REFUND" : "") + (mirrored ? " MIRROR" : "") + (pend != null ? "  pending" : "")];
            try {
                lines.push("shown " + shop.visible + "  OOR " + shop.mcOOR.visible + "  gold " + shop.mcAvailGold.text +
                           "  btn x " + shop.mcPurchaseBtn.x + " '" + shop.mcPurchaseBtn.label + "'");
                lines.push("warn '" + shop.mcWarningText.text + "'  cost '" + shop.mcCostTxt.text + "'");
                for (var i:int = 0; i < slots.length; i++) {
                    var h:Array = hist[i];
                    var parts:Array = [];
                    for each (var e:Object in h) {
                        parts.push(e.cost);
                    }
                    lines.push(SLOTS[i] + " " + (last[i] != null ? last[i].id + " x" + last[i].n + " t" + last[i].type : "?") +
                               (h.length > 0 ? "  [" + parts.join(",") + "]" + (honoured(i) != null ? " badge" : "") : ""));
                }
            } catch (err:Error) {
                lines.push("info threw " + err.message);
            }
            lines.push("--");
            info.text = lines.concat(facts).join("\n");
        }
    }
}
