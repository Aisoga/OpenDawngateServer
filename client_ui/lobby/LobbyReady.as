// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The custom game lobby's ready check. Every human on the two teams except the host gets a READY toggle at START's
// place; every tile of a player whose client has this class shows whether that player is ready; the host's START stays
// disabled, with "n/m ready" beside it, until every such player is ready. Spectators never need to be, the host is ready
// by starting, and a lobby with only the host starts at once.
//
// The state lives on the server (server/internal/blaze/ready.go), and this class only shows it. It talks to the server
// over the UI channel (opendawngate.ServerChannel): the lobby's entry and the button are calls, and every change comes
// back as the whole lobby in one event, which is simply drawn:
//     lobby.enter, lobby.setReady {ready}                  -> the server
//     lobby.state {players: [{name, ready?, rating?}]},
//     lobby.startRefused                                   <- the server
//
// The class sits on the root timeline of the front end's movie (UIRuntime.gfx), placed there by four added tags; the
// game's bytecode stays as it is, and the class uses only public members of the game's classes, by name. Its clip is on
// no stage, so it keeps a static strong reference to itself. It runs on the game's own events: the framework says when
// the lobby's component comes and goes (GComponent.componentTreeChangeHandlers: its screen is created on demand), when its
// view exists (signalViewCreated), when the view shows and hides (showComplete, hideComplete), when it has drawn
// (drawSignal) and when C++ takes its natives away (nativeBridgeDestroyedSignal). After a draw the tiles' own layouts follow in the next frames, so a redraw is repeated for a few frames
// (SETTLE_FRAMES); then nothing of this class runs until the next event.
//
// What it draws on a tile belongs to that tile. The lobby builds its 15 tiles one by one and fills them only when all are
// ready, seconds after the natives and the server know the players, and a tile being dragged leaves its cell for the
// stage. So a mark and a rating go by the tile (a "card" here: the view's playerTileAdded hands them out), not by the
// natives' roster: they show on a cell while its card sits there, shown, drawn with its player's name and with its avatar
// loaded, and they name the player that card shows. The card's own signals say when to look again (its draw, its show,
// the drag's start and end, its avatar), as does the cell's draw. The guest's button and the host's count wait for the
// local player's own card the first time. Marks stay on the overlay, not in the tile: they hide during a drag rather than
// travel with it, and the game's tiles get no children of ours.
//
// The roster comes from the lobby's own natives (comp.viewController.nativeBridge), whose arguments must be Scaleform
// UInts (u()). The host's START uses the view's own startButtonEnabled, which C++ sets too ("a player on team 0 or 1"):
// C++ setting it invalidates the view, whose draw brings the class back here to set it again.
//
// A tile of a player whose rating players see (the server's show_rating and the Leaderboard's rated games decide) shows
// "Rating <n>" after its wins, in the wins label's own format (EA's Label009: its font, size and grey), on the same
// line; spectators' tiles have no wins row (EA's spectator layout), so no rating either.
//
// Without the server's answer (no channel) the class shows and holds nothing.
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.MouseEvent;
    import flash.events.TimerEvent;
    import flash.external.ExternalInterface;
    import flash.geom.Rectangle;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.Timer;
    import flash.utils.getDefinitionByName;

    import opendawngate.ServerChannel;

    public class LobbyReady extends MovieClip {
        private static const COMPONENT_CLASS:String = "com.gravity.framework.component.GComponent";
        private static const COMPONENT_ID:String = "customMatchLobby";
        private static const CHECK_CLASS:String = "MSCCheckBox001";
        // the texts (English: the game ships en-us only, and the class needs no locale line of its own)
        private static const BTN_READY:String = "ready";          // the button's own field draws small capitals
        private static const BTN_NOT_READY:String = "not ready";
        private static const MARK_READY:String = "READY";
        private static const MARK_NOT_READY:String = "NOT READY";
        private static const WAIT_TEXT:String = "Not all ready";
        private static const RATING_LABEL:String = "Rating "; // as the profile box's "Rating 1532"
        private static const RATING_GAP:int = 14;              // after the wins, in the wins label's own units
        // the colours: the lobby's own (a ready mark green as the client's online state, the rest the tile's wins grey)
        private static const READY_COLOR:uint = 0x7ED957;
        private static const DIM_COLOR:uint = 0xA9A9A9;
        private static const WAIT_COLOR:uint = 0xC24E4E;   // the Spirit team's title red
        private static const WAIT_MS:int = 4000;
        // the count's field: right-aligned, ending this far left of START (the footer's own invite and balance icons
        // lie further left; above START is the other team's Join button)
        private static const COUNT_WIDTH:int = 120;
        private static const COUNT_GAP:int = 10;
        private static const SETTLE_FRAMES:int = 12;
        private static const AVATAR_WAIT_MS:int = 3000; // an avatar that never loads doesn't keep a mark away for good
        private static const MAX_ASSERTS:int = 3;
        private static var self:LobbyReady = null; // a strong reference besides the timeline's

        private var channel:ServerChannel = new ServerChannel();
        private var comp:Object = null;
        private var view:Object = null;
        private var nb:Object = null;
        private var box:DisplayObjectContainer = null; // the view's displayContainer, while it shows
        private var overlay:Sprite = null;
        private var settle:int = 0;
        private var waitTimer:Timer = null;
        private var channelOpened:Boolean = false;

        private var slots:Array = [];    // {t, s, name, owner, local}
        private var myName:String = "";
        private var myTeam:int = -1;
        private var isAdmin:Boolean = false;
        private var players:Object = {}; // a player's name -> {ready: Boolean or undefined, rating: int or undefined}
        private var held:Boolean = false;
        private var waiting:Boolean = false; // the host's hint after a refused start

        private var readyBtn:Object = null;
        private var tiles:Object = {};   // "<team>_<slot>" -> Tile parts {check, text, rating, wins, shown}
        private var cards:Array = [];    // the lobby's tiles, as {view, drawn: the name it last drew, hooked, icon}
        private var cardsOff:Boolean = true; // no listeners on the game's objects: the lobby isn't there, or it went
        private var cardsUp:Boolean = false; // the local player's card has shown since the lobby opened
        private var hookedCells:Array = [];  // the team cells whose draw this class hears
        private var avatarTimer:Timer = null;
        private var avatarsOverdue:Boolean = false;
        private var countTf:TextField = null;
        private var asserts:int = 0;
        private var said:Object = {};

        public function LobbyReady() {
            self = this;
            mouseEnabled = false;
            mouseChildren = false;
            channel.on("hello", onChannelOpen);
            channel.on("lobby.state", onState);
            channel.on("lobby.startRefused", onStartRefused);
            addEventListener(Event.ENTER_FRAME, onFirstFrame, false, 0, false);
        }

        // an error once per message, as an Assert line (a handful at most: the log thins repeated ones anyway)
        private function say(s:String):void {
            if (said[s] || asserts >= MAX_ASSERTS) {
                return;
            }
            said[s] = true;
            asserts++;
            try {
                ExternalInterface.call("Assert", "LobbyReady: " + s);
            } catch (e:Error) {
            }
        }

        // ---- when the lobby exists, shows and hides: the framework's events ----

        // the framework's classes are there from the movie's first frame on. (Not waitForComponentAdd: it goes by a
        // component's name, here "Custom Game Match Lobby UI Screen", and serves only once, while this screen comes and
        // goes.)
        private function onFirstFrame(e:Event):void {
            removeEventListener(Event.ENTER_FRAME, onFirstFrame);
            try {
                getDefinitionByName(COMPONENT_CLASS).componentTreeChangeHandlers.push(onTreeChange);
            } catch (err:Error) {
                say("componentTreeChangeHandlers: " + err.message);
            }
            onTreeChange();
        }

        // a component came or went somewhere: is it ours? (This runs inside the framework's own work: nothing may escape.)
        private function onTreeChange():void {
            try {
                var c:Object = getDefinitionByName(COMPONENT_CLASS).find(COMPONENT_ID);
                if (c == comp) {
                    return;
                }
                if (comp != null) {
                    if (view != null) {
                        onHidden(view);
                        forgetCards(view);
                        view = null;
                    }
                    comp.signalViewCreated.remove(onViewCreated);
                    comp.nativeBridgeDestroyedSignal.remove(onBridgeGone);
                }
                comp = c;
                if (c != null) {
                    c.signalViewCreated.add(onViewCreated);
                    c.nativeBridgeDestroyedSignal.add(onBridgeGone);
                    if (c.view != null) {
                        onViewCreated(c, c.view);
                    }
                }
            } catch (err:Error) {
                say("the component tree: " + err.message);
            }
        }

        // C++ unbinds the screen's natives first when it goes, before the view hides, and a call into them then crashes the
        // client, so this is where the class stops
        private function onBridgeGone(c:Object):void {
            if (view != null) {
                onHidden(view);
            }
        }

        private function onViewCreated(c:Object, v:Object):void {
            if (view != null && view != v) {
                onHidden(view);
                forgetCards(view);
            }
            view = v;
            cardsOff = false;
            try { // before the lobby builds them: they come a frame or more after the view
                v.playerTileAdded.add(onCardAdded);
                v.playerTileRemoved.add(onCardRemoved);
            } catch (err:Error) {
                say("the cards: " + err.message);
            }
            v.showComplete.add(onShown);
            v.hideComplete.add(onHidden);
            if (v.isShown) {
                onShown(v);
            }
        }

        private function onShown(v:Object):void {
            if (box != null) {
                return;
            }
            box = v.displayContainer as DisplayObjectContainer;
            if (box == null) {
                return;
            }
            nb = null;
            try {
                nb = comp.viewController.nativeBridge;
            } catch (e1:Error) {
            }
            if (nb == null) {
                nb = comp.nativeBridge;
            }
            overlay = new Sprite();
            overlay.mouseEnabled = false;
            overlay.mouseChildren = true;
            countTf = text(16, DIM_COLOR);
            countTf.defaultTextFormat = new TextFormat("$Body", 16, DIM_COLOR, null, null, null, null, null, "right");
            countTf.width = COUNT_WIDTH;
            countTf.height = 24;
            countTf.visible = false;
            overlay.addChild(countTf);
            box.addChild(overlay);
            box.addEventListener(Event.REMOVED_FROM_STAGE, onBoxGone, false, 0, false);
            v.drawSignal.add(onDraw);
            hookCards();
            redraw();
        }

        private function onBoxGone(e:Event):void {
            if (e.target == box) {
                onHidden(view);
            }
        }

        // the lobby closed (left, or the match started): everything of ours goes, the state with it
        private function onHidden(v:Object):void {
            if (box == null) {
                return;
            }
            channel.close();
            channelOpened = false;
            removeEventListener(Event.ENTER_FRAME, onSettle);
            settle = 0;
            if (waitTimer != null) {
                waitTimer.stop();
            }
            try {
                v.drawSignal.remove(onDraw);
            } catch (err:Error) {
            }
            box.removeEventListener(Event.REMOVED_FROM_STAGE, onBoxGone);
            unhookCards();
            if (readyBtn != null) {
                readyBtn.removeEventListener(MouseEvent.CLICK, onReadyClick);
                readyBtn.removeEventListener(Event.ENTER_FRAME, onButtonFrame);
                readyBtn.removeEventListener(Event.EXIT_FRAME, onButtonFrame);
            }
            if (overlay.parent != null) {
                overlay.parent.removeChild(overlay);
            }
            overlay = null;
            box = null;
            readyBtn = null;
            countTf = null;
            tiles = {};
            nb = null;
            slots = [];
            myName = "";
            players = {};
            held = false;
            waiting = false;
            cardsUp = false;
        }

        // Not drawn here but from the next frame on: this runs inside the view's draw, whose end drops an invalidation made
        // meanwhile, so START held here would stay lit (C++ enables it at a team change, and taking it back here is in vain).
        private function onDraw(v:Object):void {
            settleNext();
        }

        // draws now and for the next frames, while the tiles' own layouts follow the view's
        private function redraw():void {
            draw();
            settleNext();
        }

        private function settleNext():void {
            if (settle == 0) {
                addEventListener(Event.ENTER_FRAME, onSettle, false, 0, false);
            }
            settle = SETTLE_FRAMES;
        }

        private function onSettle(e:Event):void {
            draw();
            if (--settle <= 0) {
                settle = 0;
                removeEventListener(Event.ENTER_FRAME, onSettle);
            }
        }

        private function draw():void {
            if (box == null) {
                return;
            }
            roster();
            if (myName != "" && !channelOpened) {
                channelOpened = true; // once per lobby entry: it stays up (and reconnects) until the lobby closes
                channel.open(myName);
            }
            if (overlay.parent != box || box.getChildIndex(overlay) != box.numChildren - 1) {
                box.addChild(overlay);
            }
            part("cards", checkCardsUp);
            part("button", drawButton);
            part("tiles", drawTiles);
            part("start", drawStart);
        }

        private function part(name:String, f:Function):void {
            try {
                f();
            } catch (err:Error) {
                say(name + ": " + err.message);
            }
        }

        // ---- the lobby's tiles ("cards"), and when they are there ----

        private function onCardAdded(tv:Object):void {
            var c:Object = {view: tv, drawn: "", hooked: false, icon: null};
            cards.push(c);
            if (!cardsOff) {
                try { // inside the lobby's own work: nothing may escape
                    hookCard(c);
                } catch (err:Error) {
                    say("a card: " + err.message);
                }
            }
        }

        private function onCardRemoved(tv:Object):void {
            for (var i:int = 0; i < cards.length; i++) {
                if (cards[i].view == tv) {
                    unhookCard(cards[i]);
                    cards.splice(i, 1);
                    return;
                }
            }
        }

        private function hookCard(c:Object):void {
            if (c.hooked) {
                return;
            }
            c.hooked = true;
            c.view.drawSignal.add(onCardDraw);
            c.view.showComplete.add(onCardChange);
            c.view.hideComplete.add(onCardChange);
            c.view.dragStart.add(onCardDragStart);
            c.view.dragComplete.add(onCardChange);
        }

        private function unhookCard(c:Object):void {
            if (!c.hooked) {
                return;
            }
            c.hooked = false;
            c.drawn = "";
            try {
                c.view.drawSignal.remove(onCardDraw);
                c.view.showComplete.remove(onCardChange);
                c.view.hideComplete.remove(onCardChange);
                c.view.dragStart.remove(onCardDragStart);
                c.view.dragComplete.remove(onCardChange);
                if (c.icon != null) {
                    c.icon.loadCompleteSignal.remove(onCardChange);
                    c.icon = null;
                }
            } catch (err:Error) {
            }
        }

        // with the lobby's cells: they exist once the view shows, the cards may be older
        private function hookCards():void {
            cardsOff = false;
            for each (var c:Object in cards) {
                hookCard(c);
            }
            for (var t:int = 0; t < 2; t++) {
                for (var s:int = 0; s < 5; s++) {
                    var cell:Object = view["box" + t + "_" + s];
                    if (cell != null && cell.drawSignal != null) {
                        cell.drawSignal.add(onCellDraw);
                        hookedCells.push(cell);
                    }
                }
            }
        }

        private function unhookCards():void {
            cardsOff = true;
            for each (var c:Object in cards) {
                unhookCard(c);
            }
            for each (var cell:Object in hookedCells) {
                try {
                    cell.drawSignal.remove(onCellDraw);
                } catch (err:Error) {
                }
            }
            hookedCells = [];
            if (avatarTimer != null) {
                avatarTimer.stop();
            }
            avatarsOverdue = false;
        }

        private function forgetCards(v:Object):void {
            unhookCards();
            cards = [];
            try {
                v.playerTileAdded.remove(onCardAdded);
                v.playerTileRemoved.remove(onCardRemoved);
            } catch (err:Error) {
            }
        }

        // a card has drawn: its data, and with it its name, is on the screen now (the view fills a card's data and its
        // draw follows a frame later, so the data alone says too early)
        private function onCardDraw(tv:Object):void {
            try { // inside the card's own draw: nothing may escape
                for each (var c:Object in cards) {
                    if (c.view == tv) {
                        c.drawn = tv.playerViewData != null ? String(tv.playerViewData.playerName) : "";
                        if (c.icon == null && tv.playerAvatarIcon != null) {
                            c.icon = tv.playerAvatarIcon;
                            c.icon.loadCompleteSignal.add(onCardChange);
                        }
                    }
                }
            } catch (err:Error) {
                say("a card's draw: " + err.message);
            }
            onCardChange(tv);
        }

        private function onCardChange(o:Object):void {
            if (box != null) {
                settleNext();
            }
        }

        private function onCellDraw(cell:Object):void {
            onCardChange(cell);
        }

        // The drag has begun: the card has left its cell for the stage, and what we drew there must go with this very
        // event. (At its end, dragComplete, the card still calls itself dragging: that is looked at from the next frame.)
        private function onCardDragStart(tv:Object):void {
            if (box != null) {
                redraw();
            }
        }

        private function onAvatarsOverdue(e:TimerEvent):void {
            avatarsOverdue = true;
            if (box != null) {
                redraw();
            }
        }

        // the card that sits in this cell and looks finished, or null
        private function cardIn(cell:DisplayObject):Object {
            for each (var c:Object in cards) {
                if (c.view.parentContainer == cell) {
                    return cardFinished(c, cell) ? c.view : null;
                }
            }
            return null;
        }

        private function cardFinished(c:Object, cell:DisplayObject):Boolean {
            var tv:Object = c.view;
            var d:Object = tv.playerViewData;
            if (d == null || !tv.isShown || tv.isDragging) {
                return false;
            }
            var name:String = String(d.playerName);
            if (name == "" || c.drawn != name || !showsText(cell)) {
                return false;
            }
            var icon:Object = tv.playerAvatarIcon; // the card hides it while it loads
            if (icon != null && !icon.visible && !avatarsOverdue && icon.url != null && icon.url != "") {
                if (avatarTimer == null) {
                    avatarTimer = new Timer(AVATAR_WAIT_MS, 1);
                    avatarTimer.addEventListener(TimerEvent.TIMER_COMPLETE, onAvatarsOverdue);
                }
                if (!avatarTimer.running) {
                    avatarTimer.reset();
                    avatarTimer.start();
                }
                return false;
            }
            return true;
        }

        // is any text of this display object on the screen (visible all the way down from it)?
        private static function showsText(d:DisplayObject):Boolean {
            if (d == null || !d.visible) {
                return false;
            }
            var tf:TextField = d as TextField;
            if (tf != null) {
                return tf.text != "";
            }
            var c:DisplayObjectContainer = d as DisplayObjectContainer;
            if (c != null) {
                for (var i:int = 0; i < c.numChildren; i++) {
                    if (showsText(c.getChildAt(i))) {
                        return true;
                    }
                }
            }
            return false;
        }

        // the guest's button and the host's count wait for the first sight of the local player's card
        private function checkCardsUp():void {
            if (cardsUp) {
                return;
            }
            for each (var c:Object in cards) {
                var d:Object = c.view.playerViewData;
                var cell:DisplayObject = c.view.parentContainer as DisplayObject;
                if (cell != null && d != null && d.isLocalPlayer && cardFinished(c, cell)) {
                    cardsUp = true;
                    return;
                }
            }
        }

        // ---- the server ----

        private function onChannelOpen(data:Object):void {
            channel.call("lobby.enter");
        }

        private function onState(data:Object):void {
            var next:Object = {};
            for each (var p:Object in data.players) {
                next[String(p.name)] = p;
            }
            players = next;
            if (box != null) {
                redraw();
            }
        }

        // a refused start: the server unlocked the roster (RosterLockedKey "0": the roster manager tells the view's
        // rosterLocked) and sends the admin its extended data (the lobby's roster redraw: the join buttons and the kick
        // X come back only with drawRoster); the same here, in case that redraw came before the unlock reached the view
        private function onStartRefused(data:Object):void {
            if (box == null) {
                return;
            }
            waiting = true;
            if (waitTimer == null) {
                waitTimer = new Timer(WAIT_MS, 1);
                waitTimer.addEventListener(TimerEvent.TIMER_COMPLETE, onWaitOver);
            }
            waitTimer.reset();
            waitTimer.start();
            try {
                view.invalidateIsRosterLocked();
                view.invalidateRoster();
            } catch (err:Error) {
            }
            redraw();
        }

        private function onWaitOver(e:TimerEvent):void {
            waiting = false;
            if (box != null) {
                redraw();
            }
        }

        // a player's ready state as the server said it: true, false, or undefined when it doesn't count
        private function readyOf(name:String):* {
            var p:Object = players[name];
            return p == null ? undefined : p.ready;
        }

        // ---- the roster (the lobby's natives) ----

        // a native's arguments must be Scaleform UInts (CustomMatchLobbyUI.cpp asserts "params.pArgs[0].IsUInt()"): a
        // literal or an incremented loop variable arrives as an int and the call answers false
        private static function u(i:uint):uint {
            var x:uint = i;
            return x;
        }

        private function roster():void {
            var out:Array = [];
            var lt:int = -1;
            var ls:int = -1;
            try {
                lt = int(nb.GetLocalPlayerTeam());
                ls = int(nb.GetLocalPlayerTeamSlot());
                for (var t:uint = 0; t < 3; t++) {
                    for (var s:uint = 0; s < 5; s++) {
                        if (nb.IsSlotOccupied(u(t), u(s))) {
                            out.push({t: t, s: s, name: String(nb.GetSlotPlayerName(u(t), u(s))),
                                owner: Boolean(nb.IsSlotOwner(u(t), u(s))), local: t == lt && s == ls});
                        }
                    }
                }
            } catch (err:Error) {
                say("natives: " + err.message);
            }
            slots = out;
            myTeam = lt;
            myName = "";
            isAdmin = false;
            for each (var o:Object in out) {
                if (o.local) {
                    myName = o.name;
                    isAdmin = o.owner;
                }
            }
        }

        // the tiles that count: a player on a team, not the host, whose state the server tells
        private function counted():Array {
            var a:Array = [];
            for each (var o:Object in slots) {
                if (o.t < 2 && !o.owner && readyOf(o.name) !== undefined) {
                    a.push(o);
                }
            }
            return a;
        }

        private function readyCount(c:Array):int {
            var n:int = 0;
            for each (var o:Object in c) {
                if (readyOf(o.name) === true) {
                    n++;
                }
            }
            return n;
        }

        private static function digits(s:String):Boolean {
            if (s == null || s.length == 0 || s.length > 9) {
                return false;
            }
            for (var i:int = 0; i < s.length; i++) {
                if (s.charAt(i) < "0" || s.charAt(i) > "9") {
                    return false;
                }
            }
            return true;
        }

        private static function text(size:int, color:uint):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = false;
            t.wordWrap = false;
            t.defaultTextFormat = new TextFormat("$Body", size, color);
            return t;
        }

        private static function firstText(d:DisplayObject):TextField {
            if (d is TextField) {
                return d as TextField;
            }
            var c:DisplayObjectContainer = d as DisplayObjectContainer;
            if (c != null) {
                for (var i:int = 0; i < c.numChildren; i++) {
                    var t:TextField = firstText(c.getChildAt(i));
                    if (t != null) {
                        return t;
                    }
                }
            }
            return null;
        }

        // ---- the guest's READY button: START's own class at START's place ----

        private function wantButton():Boolean {
            return !isAdmin && myName != "" && (myTeam == 0 || myTeam == 1) && readyOf(myName) !== undefined;
        }

        private function drawButton():void {
            if (!wantButton()) {
                if (readyBtn != null) {
                    readyBtn.visible = false;
                }
                return;
            }
            var sb:DisplayObject = view.startButton as DisplayObject;
            if (sb == null) {
                return;
            }
            if (sb.visible) {
                sb.visible = false; // EA's START (a guest's is never enabled) under ours
            }
            if (!cardsUp) {
                return;
            }
            if (readyBtn == null) {
                var cls:Class = Object(sb).constructor as Class;
                readyBtn = new cls();
                overlay.addChild(readyBtn as DisplayObject);
                readyBtn.addEventListener(MouseEvent.CLICK, onReadyClick, false, 0, false);
                // the button's own text field (the symbol draws its text there, not in CLIK's textField: label changes
                // nothing); its timeline puts the symbol's text back at a state change (a hover, a press), so the text
                // is set again each frame while the button exists
                readyBtn.addEventListener(Event.ENTER_FRAME, onButtonFrame, false, 0, false);
                readyBtn.addEventListener(Event.EXIT_FRAME, onButtonFrame, false, 0, false);
            }
            readyBtn.visible = true;
            var r:Rectangle = sb.getBounds(overlay);
            if (r.width > 0) {
                readyBtn.x = r.x;
                readyBtn.y = r.y;
                try {
                    readyBtn.setSize(sb.width, sb.height);
                } catch (err:Error) {
                    readyBtn.width = r.width;
                    readyBtn.height = r.height;
                }
            }
            label();
        }

        private function onButtonFrame(e:Event):void {
            label();
        }

        private function label():void {
            if (readyBtn == null || !readyBtn.visible) {
                return;
            }
            var want:String = readyOf(myName) === true ? BTN_NOT_READY : BTN_READY;
            var tf:TextField = firstText(readyBtn as DisplayObject);
            if (tf != null && tf.text != want) {
                tf.text = want;
            }
        }

        // The click just sets the ready state. It shows at once; the server's lobby.state then confirms it (or, had the
        // server refused, puts it back).
        private function onReadyClick(e:MouseEvent):void {
            if (!wantButton()) {
                return;
            }
            var ready:Boolean = readyOf(myName) !== true;
            if (channel.call("lobby.setReady", {ready: ready})) {
                players[myName].ready = ready;
                redraw();
            }
        }

        // ---- the tiles: a check box and READY / NOT READY on each counted tile, the rating after the wins ----

        // Tile parts for the tile in cell key: the ready mark (check, text) and the rating (rating; wins: EA's wins field
        // it follows)
        private function tile(key:String):Object {
            var tl:Object = tiles[key];
            if (tl != null) {
                return tl;
            }
            tl = {check: null, text: null, rating: null, shown: ""};
            try {
                var ck:Object = new (getDefinitionByName(CHECK_CLASS) as Class)();
                ck.mouseEnabled = false;
                ck.mouseChildren = false;
                ck.label = "";
                ck.scrollRect = new Rectangle(0, 0, 26, 26); // the box only: unclipped, its menu bar covers the text
                overlay.addChild(ck as DisplayObject);
                tl.check = ck;
            } catch (err:Error) {
                say("check box: " + err.message);
            }
            tl.text = text(14, DIM_COLOR);
            tl.text.width = 110;
            tl.text.height = 22;
            overlay.addChild(tl.text);
            tiles[key] = tl;
            return tl;
        }

        private function drawTiles():void {
            var marked:Object = {};
            var rated:Object = {};
            for (var t:int = 0; t < 2; t++) { // the spectators: no mark, no rating
                for (var i:int = 0; i < 5; i++) {
                    var key:String = t + "_" + i;
                    var cell:DisplayObject = view["box" + key] as DisplayObject;
                    var card:Object = cell != null ? cardIn(cell) : null;
                    if (card == null) {
                        continue;
                    }
                    var name:String = String(card.playerViewData.playerName);
                    var state:* = readyOf(name);
                    if (!card.playerViewData.isOwner && state !== undefined) { // as counted()
                        marked[key] = true;
                        drawTile(tile(key), cell.getBounds(overlay), state === true);
                    }
                    var p:Object = players[name];
                    if (p != null && int(p.rating) > 0 && drawRating(tile(key), cell, String(int(p.rating)))) {
                        rated[key] = true;
                    }
                }
            }
            for (var k:String in tiles) {
                var tl:Object = tiles[k];
                if (!marked[k]) {
                    if (tl.check != null) {
                        tl.check.visible = false;
                    }
                    tl.text.visible = false;
                }
                if (!rated[k] && tl.rating != null) {
                    tl.rating.visible = false;
                }
            }
        }

        // the tile's wins field (EA's Label009 in the tile's lower row): the lowest visible text field in the cell whose
        // text is a number (the name is above it, the ready mark and the rating are ours, on the overlay)
        private static function winsField(cell:DisplayObject):TextField {
            var best:TextField = null;
            var bestY:Number = 0;
            var todo:Array = [cell];
            while (todo.length > 0) {
                var d:DisplayObject = todo.pop() as DisplayObject;
                if (d == null || !d.visible) {
                    continue;
                }
                var tf:TextField = d as TextField;
                if (tf != null) {
                    if (digits(tf.text)) {
                        var y:Number = tf.getBounds(cell).y;
                        if (best == null || y > bestY) {
                            best = tf;
                            bestY = y;
                        }
                    }
                    continue;
                }
                var c:DisplayObjectContainer = d as DisplayObjectContainer;
                if (c != null) {
                    for (var i:int = 0; i < c.numChildren; i++) {
                        todo.push(c.getChildAt(i));
                    }
                }
            }
            return best;
        }

        // "Rating <n>" after the wins, in the wins field's own format and scale, on its line; false when the tile shows
        // no wins (then no rating either)
        private function drawRating(tl:Object, cell:DisplayObject, value:String):Boolean {
            var w:TextField = winsField(cell);
            if (w == null || w.stage == null) {
                return false;
            }
            var rt:TextField = tl.rating as TextField;
            if (rt == null) {
                rt = new TextField();
                rt.embedFonts = true;
                rt.selectable = false;
                rt.mouseEnabled = false;
                rt.multiline = false;
                rt.wordWrap = false;
                overlay.addChild(rt);
                tl.rating = rt;
            }
            var want:String = RATING_LABEL + value;
            var fmt:TextFormat = w.getTextFormat();
            fmt.color = w.textColor;
            var look:String = want + "|" + fmt.font + "|" + fmt.size + "|" + w.textColor;
            if (tl.shown != look) {
                tl.shown = look;
                rt.defaultTextFormat = fmt;
                rt.text = want;
                rt.setTextFormat(fmt);
            }
            // the wins field's scale from its own units to the overlay's (the layout scales the label to its row)
            var r:Rectangle = w.getBounds(overlay);
            var localH:Number = w.scaleY != 0 ? w.height / w.scaleY : w.height;
            var sc:Number = localH > 0 ? r.height / localH : 1;
            rt.scaleX = 1;
            rt.scaleY = 1;
            rt.width = rt.textWidth + 8;
            rt.height = localH;
            rt.scaleX = sc;
            rt.scaleY = sc;
            rt.x = r.x + (w.textWidth + 2 + RATING_GAP) * sc;
            rt.y = r.y;
            rt.visible = true;
            return true;
        }

        // at the tile's right, before its kick button (32 wide)
        private function drawTile(tl:Object, r:Rectangle, on:Boolean):void {
            var tf:TextField = tl.text as TextField;
            var want:String = on ? MARK_READY : MARK_NOT_READY;
            if (tf.text != want) {
                tf.text = want;
            }
            tf.textColor = on ? READY_COLOR : DIM_COLOR;
            tf.x = r.right - 36 - 100;
            tf.y = r.y + r.height / 2 - 10;
            tf.visible = true;
            if (tl.check != null) {
                if (tl.check.selected != on) {
                    tl.check.selected = on;
                }
                tl.check.x = r.right - 36 - 130;
                tl.check.y = r.y + r.height / 2 - 12;
                tl.check.visible = true;
            }
        }

        // ---- the host's START: held disabled until every counted player is ready, the count beside it ----

        private function drawStart():void {
            if (!isAdmin) {
                countTf.visible = false;
                return;
            }
            var c:Array = counted();
            var n:int = readyCount(c);
            var sb:DisplayObject = view.startButton as DisplayObject;
            var showWait:Boolean = waiting && n < c.length;
            countTf.visible = cardsUp && c.length > 0 && sb != null && sb.visible;
            if (countTf.visible) {
                var want:String = showWait ? WAIT_TEXT : n + "/" + c.length + " ready";
                if (countTf.text != want) {
                    countTf.text = want;
                }
                countTf.textColor = showWait ? WAIT_COLOR : DIM_COLOR;
                var r:Rectangle = sb.getBounds(overlay);
                countTf.x = r.x - COUNT_WIDTH - COUNT_GAP; // left of START, before the footer's icons
                countTf.y = r.y + (r.height - 22) / 2;
            }
            // the view's own switch: its setter does nothing when the value is the same, and its draw enables the button
            if (n < c.length) {
                view.startButtonEnabled = false;
                held = true;
            } else if (held) {
                held = false;
                var anyOnTeam:Boolean = false;
                for each (var o:Object in slots) {
                    anyOnTeam = anyOnTeam || o.t < 2;
                }
                view.startButtonEnabled = anyOnTeam; // C++'s own rule
            }
        }
    }
}
