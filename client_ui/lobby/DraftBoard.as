// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The custom game lobby's draft board, for a game whose match rules pick the shapers by draft. LobbyReady owns it, draws
// it on its overlay and hands it the server's event:
//     draft.state {phase, reason?, turn, left, turnMs, teams, pool: [{id, name, catalogID, portrait?, card?}],
//                  turns: [{kind, team, player, shaper, how}], you?: {team, seat}, yourTurn}   <- the server
//     draft.ban {shaper}, draft.pick {shaper}                                                    -> the server
//
// The event is the whole board, so the board draws from the last one alone: a late joiner, a new connection and a
// redraw need no history. It covers the spectators and both teams from the team titles down to just above LEAVE GAME and
// START (the MATCH RULES summary above it and the chat beside it stay free):
//   - the head row: whose turn it is (the team and the player), the turns as a row of chips (a circle per ban, a square
//     per pick, in the team's colour: done faint, the current one filled and framed), the time left in the turn;
//   - a column per team (Mortal left, Spirit right): its bans (two; the portrait darkened, a red cross) and a row per
//     seat (its player's name, the portrait and name of the shaper picked, "Picking..." on the turn's row);
//   - the cards between them: every shaper of the pool as the Collection's tall card art with its name below, by name;
//     a banned one darkened with a red cross, a picked one darkened with a bar in the picking team's colour;
//   - the bottom row: what to do now, and for the player on turn BAN or LOCK IN (EA's Button001).
// The player on turn clicks a free card (it is framed and shown, faint, in the slot of the turn), then BAN or LOCK IN,
// which is the call; the server's next draft.state says how things stand (the turn moved on, or the same turn again: a
// refused call). Everyone else sees the same board; a click of theirs only says why it does nothing. The time left counts
// down from the event's "left" on this client's own clock. When the draft is cancelled (a drafter left) the board closes
// and a line above the lobby says why for a few seconds; when it is done the board stays until the game starts.
//
// Everything the server sends is untrusted: texts go into .text only (never htmlText), clipped; lists are cut short; a
// picture is loaded (img://, the game's own texture lookup, as the Collection's IconLoader) only when its path has the
// form of a shaper picture, Heroes/<letters>/default/ui/Textures/<letters and _>.dds, so a server can't make the class
// load anything else; the ids sent back come from the pool only. Errors go to LobbyReady's say (a few Assert lines at
// most); nothing is logged in normal use.
package {
    import flash.display.Bitmap;
    import flash.display.DisplayObject;
    import flash.display.Graphics;
    import flash.display.Loader;
    import flash.display.LoaderInfo;
    import flash.display.Shape;
    import flash.display.Sprite;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.MouseEvent;
    import flash.events.TimerEvent;
    import flash.geom.ColorTransform;
    import flash.geom.Rectangle;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.Dictionary;
    import flash.utils.Timer;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    import opendawngate.ServerChannel;

    public class DraftBoard {
        // the texts (English: the game ships en-us only)
        private static const TEAM_NAMES:Array = ["MORTAL", "SPIRIT"];
        private static const BTN_BAN:String = "BAN";
        private static const BTN_PICK:String = "LOCK IN";
        private static const PICKING:String = "Picking...";
        private static const DONE_LINE:String = "THE DRAFT IS DONE";
        private static const DONE_HINT:String = "The game starts with these shapers.";
        private static const WATCHING:String = "You are watching the draft.";
        private static const CANCELLED:String = "The draft was cancelled: ";
        private static const REFUSED:String = "The server didn't take that choice. Choose again.";

        // the colours: the teams' (Mortal's blue [assumed], the Spirit team's title red), the lobby's greys, our accent
        private static const TEAM_COLORS:Array = [0x5B9BD5, 0xC24E4E];
        private static const WHITE:uint = 0xFFFFFF;
        private static const LIGHT:uint = 0xD8D8D8;
        private static const DIM:uint = 0xA9A9A9;
        private static const DARK:uint = 0x6E6E6E;
        private static const RED:uint = 0xE0625A;
        private static const ACCENT:uint = 0xE8A33B;
        private static const FRAME:uint = 0x808080;
        private static const SLOT_FILL:uint = 0x15151A;

        // the layout (overlay units: the 1440 x 810 window's pixels), as the first sketch had it
        private static const PAD:int = 12;
        private static const LEFT:int = 16;       // the board's left edge (the spectators' column starts there)
        private static const HEAD_H:int = 40;     // the turn line, the chips, the timer
        private static const FOOT_H:int = 30;     // the hint and the button
        private static const BOARD_H:int = 455;   // from the team titles down to just above LEAVE GAME and START
        private static const TEAM_W:int = 190;    // a team's column
        private static const COL_GAP:int = 16;
        private static const BAN_S:int = 44;
        private static const PICK_H:int = 56;
        private static const PICK_S:int = 50;
        private static const NAME_H:int = 16;     // a card's name line
        private static const CELL_GAP:int = 8;
        private static const CARD_MAX_W:int = 64; // the card texture's own width: never drawn larger
        private static const TEAM_TITLE_H:int = 34; // the lobby's team titles above its first cells (RulesPanel's)
        private static const CHIP_W:int = 22;
        private static const BTN_W:int = 76;      // the fallback button's size
        private static const BTN_H:int = 19;

        // what is read of an event
        private static const MAX_POOL:int = 60;
        private static const MAX_TURNS:int = 32;
        private static const MAX_SEATS:int = 5;
        private static const MAX_BANS:int = 3;   // ban slots built a team; as many show as the draft's bans a team (2)
        private static const MAX_LEFT_MS:int = 600000;

        private static const CALL_GAP_MS:int = 300;
        private static const FLASH_MS:int = 4000;  // a click's answer in the hint line
        private static const NOTE_MS:int = 12000;  // the cancel line
        private static const EA_BUTTON:String = "Button001";

        private var channel:ServerChannel;
        private var say:Function;
        public var redraw:Function = null; // the owner's (LobbyReady's) redraw

        // the board as the last event says (phase "": none)
        private var phase:String = "";
        private var turn:int = -1;
        private var deadline:int = 0;      // getTimer() when the turn's time runs out
        private var teams:Array = [[], []];
        private var pool:Array = [];       // {id, name, portrait, card}, by name
        private var byId:Object = {};
        private var poolKey:String = "";
        private var turns:Array = [];      // {kind, team: 0 or 1, player, shaper, how}
        private var you:Object = null;     // {team: 0 or 1, seat}, null for a spectator
        private var yourTurn:Boolean = false;

        private var sel:String = "";       // the card the player on turn chose (a pool id)
        private var sent:Object = null;    // {turn, id}: the call made and not yet answered
        private var lastCall:int = -100000;
        private var flashText:String = "";
        private var flashUntil:int = 0;
        private var noteText:String = "";
        private var noteUntil:int = 0;

        // the drawing
        private var layer:Sprite = null;   // the board, on LobbyReady's overlay
        private var noteTf:TextField = null; // the cancel line, on the overlay
        private var dirty:Boolean = true;
        private var boardW:Number = 0;
        private var turnTf:TextField = null;
        private var timerTf:TextField = null;
        private var hintTf:TextField = null;
        private var chips:Sprite = null;    // a Shape per turn
        private var cols:Array = null;     // per team {title, bans: [slot], picks: [{slot, who, what, frame}]}
        private var grid:Sprite = null;
        private var cells:Array = [];      // {sp, ld, id, name, frame, mark, nm, aw, ah}
        private var actBtn:Sprite = null;
        private var tick:Timer = null;
        private var loaders:Dictionary = new Dictionary(); // a LoaderInfo -> {ld, w, h}

        public function DraftBoard(channel:ServerChannel, say:Function) {
            this.channel = channel;
            this.say = say;
        }

        // the board is up (a draft runs or has just ended): the guest's READY button stays away meanwhile
        public function get running():Boolean {
            return phase != "";
        }

        // the board's sprite (LobbyReady keeps it above the rules' layer)
        public function get top():DisplayObject {
            return layer;
        }

        // ---- the server's event ----

        public function setState(d:Object):void {
            if (d == null) {
                return;
            }
            var ph:String = str(d.phase, 16);
            if (ph == "cancelled") {
                var why:String = str(d.reason, 120);
                clearState();
                noteText = CANCELLED + (why != "" ? why : "a player left.");
                noteUntil = getTimer() + NOTE_MS;
                dirty = true;
                return;
            }
            if (ph != "ban" && ph != "pick" && ph != "done") {
                return; // nothing this board knows
            }
            var p:Array = [];
            var ids:Object = {};
            try {
                for each (var o:Object in d.pool) {
                    if (p.length >= MAX_POOL) {
                        break;
                    }
                    var id:String = idOf(o != null ? o.id : null);
                    if (id == "" || ids[id] != null) {
                        continue;
                    }
                    var e:Object = {id: id, name: str(o.name, 40), portrait: picture(o.portrait), card: picture(o.card)};
                    if (e.name == "") {
                        e.name = id;
                    }
                    ids[id] = e;
                    p.push(e);
                }
                p.sort(byName);
                var ts:Array = [];
                for each (var t:Object in d.turns) {
                    if (ts.length >= MAX_TURNS || t == null) {
                        break;
                    }
                    var kind:String = str(t.kind, 8);
                    var team:int = int(t.team) - 1;
                    if ((kind != "ban" && kind != "pick") || team < 0 || team > 1) {
                        continue;
                    }
                    var sh:String = idOf(t.shaper);
                    ts.push({kind: kind, team: team, player: str(t.player, 30), shaper: ids[sh] != null ? sh : "",
                        how: str(t.how, 16)});
                }
                var tm:Array = [[], []];
                if (d.teams is Array) {
                    for (var i:int = 0; i < 2 && i < d.teams.length; i++) {
                        for each (var n:Object in d.teams[i]) {
                            if (tm[i].length < MAX_SEATS) {
                                tm[i].push(str(n, 30));
                            }
                        }
                    }
                }
            } catch (err:Error) {
                say("draft state: " + err.message);
                return;
            }
            var key:String = "";
            for each (var q:Object in p) {
                key += q.id + "|" + q.card + "|" + q.portrait + ";";
            }
            var nextTurn:int = ph == "done" ? -1 : int(d.turn);
            if (nextTurn < 0 || nextTurn >= ts.length) {
                nextTurn = -1;
            }
            var left:int = Math.max(0, Math.min(MAX_LEFT_MS, int(d.left)));
            var y:Object = null;
            if (d.you != null && (int(d.you.team) == 1 || int(d.you.team) == 2)) {
                y = {team: int(d.you.team) - 1, seat: Math.max(0, Math.min(MAX_SEATS - 1, int(d.you.seat)))};
            }
            var mine:Boolean = d.yourTurn === true && y != null && nextTurn >= 0;
            if (nextTurn != turn) {
                flashText = ""; // a click's answer belongs to its turn ("Not your turn: rules5a bans now.")
            }
            // a call answered by the same turn again: the server refused it (an accepted one ends the turn)
            if (sent != null && mine && nextTurn == sent.turn) {
                flash(REFUSED);
            }
            sent = null;
            if (nextTurn != turn || !mine || (sel != "" && stateOf(sel, ts) != 0)) {
                sel = "";
            }
            phase = ph;
            turn = nextTurn;
            deadline = getTimer() + left;
            teams = tm;
            pool = p;
            byId = ids;
            turns = ts;
            you = y;
            yourTurn = mine;
            noteText = "";
            if (key != poolKey) {
                poolKey = key;
                clearGrid();
            }
            dirty = true;
        }

        // the lobby closed: everything of ours goes (the textures unloaded, the timer stopped, the listeners off)
        public function detach():void {
            removeBoard();
            if (noteTf != null && noteTf.parent != null) {
                noteTf.parent.removeChild(noteTf);
            }
            noteTf = null;
            clearState();
            noteText = "";
        }

        private function clearState():void {
            phase = "";
            turn = -1;
            teams = [[], []];
            pool = [];
            byId = {};
            poolKey = "";
            turns = [];
            you = null;
            yourTurn = false;
            sel = "";
            sent = null;
            flashText = "";
            removeBoard();
        }

        private function removeBoard():void {
            if (tick != null) {
                tick.stop();
                tick.removeEventListener(TimerEvent.TIMER, onTick);
                tick = null;
            }
            clearGrid();
            if (cols != null) {
                for each (var c:Object in cols) {
                    for each (var b:Object in c.bans) {
                        unload(b.ld);
                    }
                    for each (var r:Object in c.picks) {
                        unload(r.slot.ld);
                    }
                }
            }
            cols = null;
            if (actBtn != null) {
                actBtn.removeEventListener(MouseEvent.CLICK, onAct);
            }
            actBtn = null;
            if (layer != null && layer.parent != null) {
                layer.parent.removeChild(layer);
            }
            layer = null;
            grid = null;
            chips = null;
            turnTf = timerTf = hintTf = null;
            boardW = 0;
            loaders = new Dictionary();
            dirty = true;
        }

        // ---- drawing: called by LobbyReady's draw (the view's draws and the frames after them) and after each event ----

        public function draw(overlay:Sprite, view:Object):void {
            var now:int = getTimer();
            drawNote(overlay, view, now);
            if (phase == "") {
                return;
            }
            var c1:DisplayObject = view.box1_0 as DisplayObject;
            if (c1 == null) {
                return;
            }
            var r1:Rectangle = c1.getBounds(overlay);
            if (r1.width < 50 || r1.height < 10) {
                return; // not laid out yet
            }
            var w:Number = Math.round(r1.right - LEFT);
            if (layer != null && w != boardW) {
                removeBoard(); // the lobby's layout changed: build again at the new width
            }
            if (layer == null) {
                boardW = w;
                build();
            }
            if (layer.parent != overlay) {
                overlay.addChild(layer);
            } else if (overlay.getChildIndex(layer) != overlay.numChildren - 1) {
                overlay.setChildIndex(layer, overlay.numChildren - 1); // above the tiles' marks and the rules
            }
            layer.x = LEFT;
            layer.y = Math.round(r1.y - TEAM_TITLE_H);
            if (dirty) {
                dirty = false;
                refresh();
            }
            placeButton();
        }

        // the cancel line, for a few seconds, in the spectators' column below its title (left of the teams, where the
        // board began), wrapped to the column, on a dark ground over a spectator's tile
        private function drawNote(overlay:Sprite, view:Object, now:int):void {
            var show:Boolean = noteText != "" && now < noteUntil;
            if (!show) {
                if (noteTf != null) {
                    noteTf.visible = false;
                }
                return;
            }
            var c0:DisplayObject = view.box0_0 as DisplayObject;
            if (c0 == null) {
                return;
            }
            var r0:Rectangle = c0.getBounds(overlay);
            var w:Number = Math.max(120, Math.round(r0.x - LEFT - 12));
            if (noteTf == null) {
                noteTf = field("$Body", 15, RED, "left");
                noteTf.multiline = true;
                noteTf.wordWrap = true;
                noteTf.background = true;
                noteTf.backgroundColor = 0x0A0A0C;
            }
            if (noteTf.parent != overlay) {
                overlay.addChild(noteTf);
            } else if (overlay.getChildIndex(noteTf) != overlay.numChildren - 1) {
                overlay.setChildIndex(noteTf, overlay.numChildren - 1);
            }
            noteTf.width = w;
            if (noteTf.text != noteText) {
                noteTf.text = noteText;
            }
            noteTf.height = Math.min(noteTf.textHeight + 6, 66);
            noteTf.x = LEFT;
            noteTf.y = Math.round(r0.y + 6);
            noteTf.visible = true;
            startTick(); // a timer of its own hides it again
        }

        private function build():void {
            layer = new Sprite();
            layer.mouseEnabled = true; // its fill takes the clicks that would reach the lobby's tiles below
            var g:Graphics = layer.graphics;
            g.lineStyle(1, FRAME, 0.9);
            g.beginFill(0x0A0A0C, 1); // opaque: at 0.94 the lobby's titles under the head row showed through
            g.drawRect(0, 0, boardW, BOARD_H);
            g.endFill();
            g.lineStyle(2, ACCENT, 1);
            g.moveTo(1, 1);
            g.lineTo(boardW - 1, 1);

            turnTf = field("$Header", 18, WHITE, "left");
            turnTf.x = PAD;
            turnTf.y = PAD + 4;
            turnTf.width = 360;
            layer.addChild(turnTf);
            chips = new Sprite();
            chips.mouseEnabled = false;
            chips.mouseChildren = false;
            chips.y = PAD + 8;
            layer.addChild(chips);
            timerTf = field("$Header", 26, ACCENT, "right");
            timerTf.width = 90;
            timerTf.x = boardW - PAD - 90;
            timerTf.y = PAD - 2;
            layer.addChild(timerTf);

            cols = [teamColumn(0, PAD), teamColumn(1, boardW - PAD - TEAM_W)];

            grid = new Sprite();
            grid.x = PAD + TEAM_W + COL_GAP;
            grid.y = PAD + HEAD_H;
            layer.addChild(grid);

            hintTf = field("$Body", 14, DIM, "left");
            hintTf.x = PAD;
            hintTf.y = BOARD_H - FOOT_H + 4;
            hintTf.width = boardW - 2 * PAD - 160;
            layer.addChild(hintTf);
            actBtn = button(BTN_BAN);
            actBtn.addEventListener(MouseEvent.CLICK, onAct, false, 0, false);
            actBtn.visible = false;
            layer.addChild(actBtn);
            startTick();
            dirty = true;
        }

        private function startTick():void {
            if (tick == null) {
                tick = new Timer(250);
                tick.addEventListener(TimerEvent.TIMER, onTick, false, 0, false);
            }
            tick.start();
        }

        // a team's column: its name, three ban slots, a row per seat
        private function teamColumn(team:int, x:Number):Object {
            var left:Boolean = team == 0;
            var title:TextField = field("$Header", 16, TEAM_COLORS[team], left ? "left" : "right");
            title.text = TEAM_NAMES[team];
            title.x = x;
            title.y = PAD + HEAD_H - 4;
            title.width = TEAM_W;
            layer.addChild(title);
            var y:Number = PAD + HEAD_H + 20;
            var bans:Array = [];
            for (var b:int = 0; b < MAX_BANS; b++) {
                var bx:Number = left ? x + b * (BAN_S + 8) : x + TEAM_W - (b + 1) * BAN_S - b * 8;
                bans.push(slot(bx, y, BAN_S));
            }
            y += BAN_S + 10;
            var picks:Array = [];
            for (var p:int = 0; p < MAX_SEATS; p++) {
                var py:Number = y + p * PICK_H;
                var sx:Number = left ? x : x + TEAM_W - PICK_S;
                var row:Object = {slot: slot(sx, py, PICK_S)};
                row.who = field("$Body", 13, DIM, left ? "left" : "right");
                row.what = field("$Header", 15, WHITE, left ? "left" : "right");
                for each (var t:TextField in [row.who, row.what]) {
                    t.width = TEAM_W - PICK_S - 10;
                    t.x = left ? x + PICK_S + 10 : x;
                    layer.addChild(t);
                }
                row.who.y = py + 4;
                row.what.y = py + 22;
                picks.push(row);
            }
            return {title: title, bans: bans, picks: picks};
        }

        // a ban or pick slot: a dark square, the portrait loaded into it when there is one (else, on a ban slot, the
        // shaper's name: a server whose content has no pictures), a red cross for a ban, a frame for the turn's slot, a
        // dash for a skipped ban
        private function slot(x:Number, y:Number, s:Number):Object {
            var sp:Sprite = new Sprite();
            sp.x = x;
            sp.y = y;
            sp.mouseEnabled = false;
            sp.mouseChildren = false;
            var g:Graphics = sp.graphics;
            g.lineStyle(1, FRAME, 0.9);
            g.beginFill(SLOT_FILL, 1);
            g.drawRect(0, 0, s, s);
            g.endFill();
            var cross:Shape = new Shape();
            cross.graphics.lineStyle(3, RED, 1);
            cross.graphics.moveTo(4, 4);
            cross.graphics.lineTo(s - 4, s - 4);
            cross.graphics.moveTo(s - 4, 4);
            cross.graphics.lineTo(4, s - 4);
            cross.visible = false;
            var frame:Shape = new Shape();
            frame.graphics.lineStyle(2, ACCENT, 1);
            frame.graphics.drawRect(-1, -1, s + 2, s + 2);
            frame.visible = false;
            var dash:TextField = field("$Header", 16, DARK, "center");
            dash.text = "-";
            dash.width = s;
            dash.y = (s - 24) / 2;
            dash.visible = false;
            var label:TextField = field("$Body", 11, LIGHT, "center");
            label.width = s + 8; // into the 8-unit gaps beside the slot: "Amarynth" fits at 9
            label.x = -4;
            label.y = (s - 19) / 2;
            label.visible = false;
            sp.addChild(label);
            sp.addChild(cross);
            sp.addChild(dash);
            sp.addChild(frame);
            layer.addChild(sp);
            return {sp: sp, s: s, ld: null, path: "", cross: cross, frame: frame, dash: dash, label: label};
        }

        // a slot shows the shaper's portrait (path "": none); faint for a choice not yet made, darkened for a ban; without
        // a portrait the slot shows name (if given) instead, and a ban its cross all the same
        private function showIn(sl:Object, path:String, ban:Boolean, faint:Boolean, name:String = ""):void {
            if (sl.path != path) {
                unload(sl.ld);
                if (sl.ld != null && sl.ld.parent != null) {
                    sl.ld.parent.removeChild(sl.ld);
                }
                sl.ld = null;
                sl.path = path;
                if (path != "") {
                    sl.ld = load(path, sl.s, sl.s);
                    sl.sp.addChildAt(sl.ld, 0);
                }
            }
            if (sl.ld != null) {
                sl.ld.alpha = faint ? 0.6 : 1;
                sl.ld.transform.colorTransform = ban ? new ColorTransform(0.4, 0.4, 0.4, 1) : new ColorTransform();
            }
            var plain:Boolean = path == "" && name != "";
            if (plain && sl.label.text != name) {
                sl.label.text = name;
                fit(sl.label, 11, sl.s + 8);
            }
            sl.label.visible = plain;
            sl.label.alpha = faint ? 0.6 : 1;
            sl.cross.visible = ban && (path != "" || plain) && !faint;
        }

        // ---- the board's state on the screen ----

        private function refresh():void {
            var cur:Object = turn >= 0 ? turns[turn] : null;
            // the head row
            var line:String = DONE_LINE;
            var lineColor:uint = WHITE;
            if (cur != null) {
                if (yourTurn) {
                    line = cur.kind == "ban" ? "YOUR TURN: BAN A SHAPER" : "YOUR TURN: PICK YOUR SHAPER";
                    lineColor = ACCENT;
                } else {
                    line = TEAM_NAMES[cur.team] + (cur.kind == "ban" ? " BANS: " : " PICKS: ") + cur.player;
                }
            }
            turnTf.text = line;
            turnTf.textColor = lineColor;
            fit(turnTf, 18, 360);
            drawChips();
            timerTf.visible = cur != null;
            showTime();
            // the teams' columns: as many ban slots as the team with the most bans makes (the server's count, two)
            var nb:Array = [0, 0];
            for each (var tb:Object in turns) {
                nb[tb.team] += tb.kind == "ban" ? 1 : 0;
            }
            var slots:int = Math.min(MAX_BANS, Math.max(nb[0], nb[1]));
            for (var t:int = 0; t < 2; t++) {
                var col:Object = cols[t];
                var b:int = 0;
                var k:int = 0;
                for (var i:int = 0; i < turns.length; i++) {
                    var tn:Object = turns[i];
                    if (tn.team != t) {
                        continue;
                    }
                    var now:Boolean = i == turn;
                    var preview:Boolean = now && yourTurn && sel != "";
                    if (tn.kind == "ban" && b < MAX_BANS) {
                        var bs:Object = col.bans[b++];
                        var bid:String = preview ? sel : tn.shaper;
                        showIn(bs, portraitOf(bid), !preview && tn.shaper != "", preview, bid != "" ? nameOf(bid) : "");
                        bs.frame.visible = now;
                        bs.dash.visible = !now && tn.how != "" && tn.shaper == "";
                    } else if (tn.kind == "pick" && k < MAX_SEATS) {
                        var row:Object = col.picks[k++];
                        drawPickRow(row, tn, now, preview);
                    }
                }
                for (var bv:int = 0; bv < MAX_BANS; bv++) {
                    col.bans[bv].sp.visible = bv < slots;
                }
                for (; b < MAX_BANS; b++) { // bans this team doesn't make (a team without humans)
                    showIn(col.bans[b], "", false, false);
                    col.bans[b].frame.visible = false;
                    col.bans[b].dash.visible = false;
                }
                for (; k < MAX_SEATS; k++) { // seats nobody drafts for
                    var er:Object = col.picks[k];
                    showIn(er.slot, "", false, false);
                    er.slot.frame.visible = false;
                    er.slot.dash.visible = false;
                    er.slot.sp.alpha = 0.35;
                    er.who.text = k < teams[t].length ? teams[t][k] : "";
                    er.what.text = "";
                }
            }
            // the cards
            if (cells.length == 0 && pool.length > 0) {
                fillGrid();
            }
            for each (var c:Object in cells) {
                drawCell(c);
            }
            // the bottom row
            actBtn.visible = yourTurn;
            if (yourTurn) {
                setLabel(actBtn, cur.kind == "ban" ? BTN_BAN : BTN_PICK);
                setAlpha(actBtn, sel != "" && sent == null ? 1 : 0.5);
            }
            showHint();
        }

        private function drawPickRow(row:Object, tn:Object, now:Boolean, preview:Boolean):void {
            row.slot.sp.alpha = 1;
            var mine:Boolean = you != null && tn.team == you.team && tn.player == teams[you.team][you.seat];
            row.who.text = tn.player + (mine ? " (you)" : "");
            row.who.textColor = mine ? LIGHT : DIM;
            if (tn.shaper != "") {
                showIn(row.slot, portraitOf(tn.shaper), false, false);
                row.what.text = nameOf(tn.shaper);
                row.what.textColor = WHITE;
            } else if (preview) {
                showIn(row.slot, portraitOf(sel), false, true);
                row.what.text = nameOf(sel) + "?";
                row.what.textColor = ACCENT;
            } else {
                showIn(row.slot, "", false, false);
                row.what.text = now ? PICKING : "-";
                row.what.textColor = now ? ACCENT : DARK;
            }
            fit(row.what, 15, TEAM_W - PICK_S - 10);
            row.slot.frame.visible = now;
            row.slot.dash.visible = false;
        }

        // the turns as chips: a circle per ban, a square per pick, in the team's colour; done faint, the current one
        // filled with an orange frame, the rest outlined
        private function drawChips():void {
            while (chips.numChildren > 0) {
                chips.removeChildAt(0);
            }
            var bans:int = 0;
            for each (var t0:Object in turns) {
                bans += t0.kind == "ban" ? 1 : 0;
            }
            var gap:int = bans > 0 && bans < turns.length ? 12 : 0;
            var w:Number = turns.length * CHIP_W + gap;
            chips.x = Math.round((boardW - w) / 2) + 40;
            for (var i:int = 0; i < turns.length; i++) {
                var tn:Object = turns[i];
                var color:uint = TEAM_COLORS[tn.team];
                var cx:Number = i * CHIP_W + (tn.kind == "pick" ? gap : 0);
                var done:Boolean = turn < 0 || i < turn;
                chips.addChild(chip(tn.kind == "ban", cx, i == turn ? 2 : 1, i == turn ? ACCENT : color, color,
                    i == turn ? 1 : (done ? 0.35 : 0)));
            }
        }

        // One turn's marker, a Shape of its own whose path begins at its origin: a circle (a ban) or a square (a pick).
        // The game's Flash player joins the paths of one Graphics object, moveTo or not: drawn in a single Shape the
        // circles were tied together by a line through their start points, in the last marker's line style (the "debug"
        // lines across the strip in the first live runs). So no two markers share a Graphics, and none moves its pen.
        private static function chip(ban:Boolean, cx:Number, thick:Number, line:uint, fill:uint, alpha:Number):Shape {
            var sh:Shape = new Shape();
            var g:Graphics = sh.graphics;
            g.lineStyle(thick, line, 1);
            g.beginFill(fill, alpha);
            if (ban) {
                // the circle's centre (cx + 9, 9), radius 7, from its rightmost point (the Shape's origin): eight curves,
                // each control point at r / cos(22.5 degrees), halfway between its two ends
                var r:Number = 7;
                sh.x = cx + 9 + r;
                sh.y = 9;
                var rc:Number = r / Math.cos(Math.PI / 8);
                for (var k:int = 1; k <= 8; k++) {
                    var a:Number = k * Math.PI / 4;
                    var m:Number = a - Math.PI / 8;
                    g.curveTo(-r + rc * Math.cos(m), rc * Math.sin(m), -r + r * Math.cos(a), r * Math.sin(a));
                }
            } else {
                sh.x = cx + 1;
                sh.y = 2;
                g.lineTo(16, 0);
                g.lineTo(16, 14);
                g.lineTo(0, 14);
                g.lineTo(0, 0);
            }
            g.endFill();
            return sh;
        }

        private function onTick(e:TimerEvent):void {
            try {
                if (layer != null) {
                    showTime();
                    if (flashText != "" && getTimer() >= flashUntil) {
                        flashText = "";
                        showHint();
                    }
                }
                if (noteTf != null && noteTf.visible && getTimer() >= noteUntil) {
                    noteTf.visible = false;
                    noteText = "";
                }
                if (layer == null && (noteTf == null || !noteTf.visible) && tick != null) {
                    tick.stop();
                }
            } catch (err:Error) {
                say("draft timer: " + err.message);
            }
        }

        // the time left in the turn, counted down on this client's clock from the event's "left"
        private function showTime():void {
            if (timerTf == null || !timerTf.visible) {
                return;
            }
            var s:int = Math.max(0, Math.ceil((deadline - getTimer()) / 1000));
            var t:String = int(s / 60) + ":" + (s % 60 < 10 ? "0" : "") + (s % 60);
            if (timerTf.text != t) {
                timerTf.text = t;
                timerTf.textColor = s <= 5 ? RED : ACCENT;
            }
        }

        private function showHint():void {
            if (hintTf == null) {
                return;
            }
            var cur:Object = turn >= 0 ? turns[turn] : null;
            var s:String;
            var color:uint = DIM;
            if (flashText != "") {
                s = flashText;
                color = RED;
            } else if (cur == null) {
                s = DONE_HINT;
            } else if (yourTurn && sent != null) {
                s = (cur.kind == "ban" ? "Banning " : "Picking ") + nameOf(sent.id) + "...";
            } else if (yourTurn && sel != "") {
                s = cur.kind == "ban" ? "Ban " + nameOf(sel) + "? Click BAN." : "Pick " + nameOf(sel) + "? Click LOCK IN.";
                color = LIGHT;
            } else if (yourTurn) {
                s = cur.kind == "ban" ? "Your turn: choose a shaper to ban, then BAN." :
                    "Your turn: choose your shaper, then LOCK IN.";
                color = LIGHT;
            } else if (you == null) {
                s = WATCHING;
            } else {
                s = "Waiting for " + cur.player + (cur.kind == "ban" ? " to ban a shaper." : " to pick a shaper.");
            }
            if (hintTf.text != s) {
                hintTf.text = s;
                fit(hintTf, 14, hintTf.width);
            }
            hintTf.textColor = color;
        }

        private function flash(s:String):void {
            flashText = s;
            flashUntil = getTimer() + FLASH_MS;
            showHint();
        }

        private function placeButton():void {
            if (actBtn == null) {
                return;
            }
            var r:Rectangle = sizeOf(actBtn);
            actBtn.x = boardW - PAD - r.width;
            actBtn.y = BOARD_H - PAD - r.height + 2;
        }

        // ---- the cards ----

        // the pool's cards: the column count whose card comes out largest (12 x 3 at 47 x 94 for 35 shapers on a
        // 1440-wide window), never wider than the texture's own 64
        private function fillGrid():void {
            clearGrid();
            var W:Number = boardW - 2 * (PAD + TEAM_W + COL_GAP);
            var H:Number = BOARD_H - PAD - HEAD_H - FOOT_H - 4;
            var n:int = pool.length;
            var best:Number = 0;
            var nc:int = 1;
            for (var c:int = 1; c <= 16; c++) {
                var r:int = Math.ceil(n / c);
                var s:Number = Math.min(W / c - CELL_GAP, (H / r - NAME_H - CELL_GAP) / 2, CARD_MAX_W);
                if (s > best + 0.01) {
                    best = s;
                    nc = c;
                }
            }
            var aw:int = Math.max(8, Math.floor(best));
            var ah:int = aw * 2;
            var cw:Number = W / Math.min(nc, Math.max(n, 1));
            var rows:int = Math.ceil(n / nc);
            var used:Number = cw * Math.min(nc, n);
            var x0:Number = (W - used) / 2;
            var ch:Number = ah + NAME_H + CELL_GAP;
            for (var i:int = 0; i < n; i++) {
                var e:Object = pool[i];
                var sp:Sprite = new Sprite();
                sp.x = Math.round(x0 + (i % nc) * cw + (cw - aw) / 2);
                sp.y = Math.round(Math.floor(i / nc) * ch);
                sp.mouseChildren = false;
                sp.name = String(i);
                var g:Graphics = sp.graphics;
                g.beginFill(SLOT_FILL, 1);
                g.drawRect(0, 0, aw, ah);
                g.endFill();
                var ld:Loader = e.card != "" ? load(e.card, aw, ah) : null;
                if (ld != null) {
                    sp.addChild(ld);
                }
                var mark:Shape = new Shape();
                sp.addChild(mark);
                var frame:Shape = new Shape();
                sp.addChild(frame);
                var nm:TextField = field("$Body", 12, WHITE, "center");
                nm.width = cw - 2;
                nm.x = (aw - nm.width) / 2;
                nm.y = ah + 1;
                nm.text = e.name;
                fit(nm, 12, cw - 4);
                sp.addChild(nm);
                sp.addEventListener(MouseEvent.CLICK, onCell, false, 0, false);
                sp.addEventListener(MouseEvent.ROLL_OVER, onCellOver, false, 0, false);
                sp.addEventListener(MouseEvent.ROLL_OUT, onCellOut, false, 0, false);
                grid.addChild(sp);
                cells.push({sp: sp, ld: ld, id: e.id, name: e.name, mark: mark, frame: frame, nm: nm, aw: aw, ah: ah,
                    over: false, look: ""});
            }
            if (rows > 0 && grid != null) {
                grid.y = PAD + HEAD_H + Math.max(0, Math.floor((H - rows * ch) / 2));
            }
        }

        private function clearGrid():void {
            for each (var c:Object in cells) {
                c.sp.removeEventListener(MouseEvent.CLICK, onCell);
                c.sp.removeEventListener(MouseEvent.ROLL_OVER, onCellOver);
                c.sp.removeEventListener(MouseEvent.ROLL_OUT, onCellOut);
                unload(c.ld);
                if (c.sp.parent != null) {
                    c.sp.parent.removeChild(c.sp);
                }
            }
            cells = [];
        }

        // a card's look: free, banned (darkened, a red cross), picked (darkened, a bar in the team's colour), chosen
        // (an orange frame), under the pointer while it can be chosen (a faint frame)
        private function drawCell(c:Object):void {
            var st:int = stateOf(c.id, turns);
            var chosen:Boolean = yourTurn && sel == c.id;
            var canPick:Boolean = yourTurn && st == 0 && sent == null;
            var look:String = st + "|" + chosen + "|" + (c.over && canPick);
            c.sp.buttonMode = canPick;
            if (c.look == look) {
                return;
            }
            c.look = look;
            if (c.ld != null) {
                c.ld.transform.colorTransform = st == 1 ? new ColorTransform(0.3, 0.3, 0.3, 1) :
                    (st >= 2 ? new ColorTransform(0.45, 0.45, 0.45, 1) : new ColorTransform());
            }
            var g:Graphics = c.mark.graphics;
            g.clear();
            if (st == 1) {
                g.lineStyle(2, RED, 0.9);
                g.moveTo(3, 3);
                g.lineTo(c.aw - 3, c.ah - 3);
                g.moveTo(c.aw - 3, 3);
                g.lineTo(3, c.ah - 3);
            } else if (st >= 2) {
                g.lineStyle(3, TEAM_COLORS[st - 2], 1);
                g.moveTo(0, c.ah - 1);
                g.lineTo(c.aw, c.ah - 1);
            }
            var f:Graphics = c.frame.graphics;
            f.clear();
            if (chosen) {
                f.lineStyle(2, ACCENT, 1);
                f.drawRect(-1, -1, c.aw + 2, c.ah + 2);
            } else if (c.over && canPick) {
                f.lineStyle(1, WHITE, 0.7);
                f.drawRect(-1, -1, c.aw + 2, c.ah + 2);
            }
            c.nm.textColor = st == 0 ? WHITE : DARK;
        }

        private function cellOf(e:MouseEvent):Object {
            var sp:Sprite = e.currentTarget as Sprite;
            var i:int = sp != null ? int(sp.name) : -1;
            return i >= 0 && i < cells.length && cells[i].sp == sp ? cells[i] : null;
        }

        private function onCellOver(e:MouseEvent):void {
            var c:Object = cellOf(e);
            if (c != null) {
                c.over = true;
                drawCell(c);
            }
        }

        private function onCellOut(e:MouseEvent):void {
            var c:Object = cellOf(e);
            if (c != null) {
                c.over = false;
                drawCell(c);
            }
        }

        // a card's click: the player on turn chooses it (shown at once); anyone else hears why nothing happens
        private function onCell(e:MouseEvent):void {
            var c:Object = cellOf(e);
            var cur:Object = turn >= 0 ? turns[turn] : null;
            if (c == null || cur == null) {
                return;
            }
            var st:int = stateOf(c.id, turns);
            if (st == 1) {
                flash(c.name + " is banned.");
            } else if (st >= 2) {
                flash(c.name + " is already picked.");
            } else if (you == null) {
                flash(WATCHING);
            } else if (!yourTurn) {
                flash("Not your turn: " + cur.player + (cur.kind == "ban" ? " bans now." : " picks now."));
            } else if (sent != null) {
                return;
            } else {
                sel = c.id;
                flashText = "";
            }
            dirty = true;
            if (redraw != null) {
                redraw();
            }
        }

        // BAN or LOCK IN: the call; the server's next draft.state says whether it was taken
        private function onAct(e:MouseEvent):void {
            var cur:Object = turn >= 0 ? turns[turn] : null;
            if (cur == null || !yourTurn || sent != null) {
                return;
            }
            if (sel == "" || byId[sel] == null) {
                flash(cur.kind == "ban" ? "Choose a shaper to ban first." : "Choose your shaper first.");
                return;
            }
            var now:int = getTimer();
            if (now - lastCall < CALL_GAP_MS) {
                return;
            }
            if (!channel.call(cur.kind == "ban" ? "draft.ban" : "draft.pick", {shaper: sel})) {
                flash("Not connected to the server.");
                return;
            }
            lastCall = now;
            sent = {turn: turn, id: sel};
            dirty = true;
            if (redraw != null) {
                redraw();
            }
        }

        // ---- the board's data ----

        // 0 free, 1 banned, 2 picked by Mortal, 3 by Spirit
        private static function stateOf(id:String, ts:Array):int {
            for each (var t:Object in ts) {
                if (t.shaper == id) {
                    return t.kind == "ban" ? 1 : 2 + t.team;
                }
            }
            return 0;
        }

        private function nameOf(id:String):String {
            var e:Object = byId[id];
            return e != null ? e.name : id;
        }

        private function portraitOf(id:String):String {
            var e:Object = byId[id];
            return e != null ? e.portrait : "";
        }

        private static function byName(a:Object, b:Object):int {
            var x:String = String(a.name).toLowerCase();
            var y:String = String(b.name).toLowerCase();
            return x < y ? -1 : (x > y ? 1 : 0);
        }

        // ---- the pictures ----

        // a server's picture path, if it has a shaper picture's form: Heroes/<letters>/default/ui/Textures/<letters, _>.dds
        // (any case: the game's lookup ignores it); "" for anything else
        private static function picture(v:*):String {
            if (!(v is String)) {
                return "";
            }
            var s:String = v as String;
            if (s.length > 120) {
                return "";
            }
            var parts:Array = s.split("/");
            if (parts.length != 6) {
                return "";
            }
            var fixed:Array = ["heroes", null, "default", "ui", "textures", null];
            for (var i:int = 0; i < 6; i++) {
                if (fixed[i] != null && String(parts[i]).toLowerCase() != fixed[i]) {
                    return "";
                }
            }
            var file:String = parts[5];
            if (file.length < 5 || file.substr(file.length - 4).toLowerCase() != ".dds") {
                return "";
            }
            return letters(parts[1], false) && letters(file.substr(0, file.length - 4), true) ? s : "";
        }

        private static function letters(s:String, underscore:Boolean):Boolean {
            if (s == null || s.length == 0 || s.length > 48) {
                return false;
            }
            for (var i:int = 0; i < s.length; i++) {
                var c:String = s.charAt(i);
                if (!((c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || (underscore && c == "_"))) {
                    return false;
                }
            }
            return true;
        }

        // a pool id (a PlayerClass: letters, digits and _), "" for anything else
        private static function idOf(v:*):String {
            if (!(v is String)) {
                return "";
            }
            var s:String = v as String;
            if (s.length == 0 || s.length > 40) {
                return "";
            }
            for (var i:int = 0; i < s.length; i++) {
                var c:String = s.charAt(i);
                if (!((c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || (c >= "0" && c <= "9") || c == "_")) {
                    return "";
                }
            }
            return s;
        }

        // a texture of the game's by the game's own loader (img://, as the Collection's IconLoader), scaled to w x h when
        // it arrives; a texture that doesn't come leaves the slot's dark square
        private function load(path:String, w:Number, h:Number):Loader {
            var ld:Loader = new Loader();
            loaders[ld.contentLoaderInfo] = {ld: ld, w: w, h: h};
            ld.contentLoaderInfo.addEventListener(Event.COMPLETE, onLoaded, false, 0, false);
            ld.contentLoaderInfo.addEventListener(IOErrorEvent.IO_ERROR, onLoadError, false, 0, false);
            try {
                ld.load(new URLRequest("img://" + path));
            } catch (err:Error) {
                say("draft picture: " + err.message);
            }
            return ld;
        }

        private function onLoaded(e:Event):void {
            var li:LoaderInfo = e.target as LoaderInfo;
            var k:Object = li != null ? loaders[li] : null;
            if (k == null) {
                return;
            }
            var bmp:Bitmap = k.ld.content as Bitmap;
            if (bmp != null) {
                bmp.smoothing = true;
            }
            k.ld.width = k.w;
            k.ld.height = k.h;
        }

        private function onLoadError(e:Event):void {
            say("a draft picture didn't load");
        }

        private function unload(ld:Loader):void {
            if (ld == null) {
                return;
            }
            try {
                ld.contentLoaderInfo.removeEventListener(Event.COMPLETE, onLoaded);
                ld.contentLoaderInfo.removeEventListener(IOErrorEvent.IO_ERROR, onLoadError);
                delete loaders[ld.contentLoaderInfo];
                ld.unload();
            } catch (err:Error) {
            }
        }

        // ---- helpers (as RulesPanel's) ----

        // a server's value as a string we draw: a string only, cut short
        private static function str(v:*, max:int):String {
            if (!(v is String)) {
                return "";
            }
            var s:String = v as String;
            return s.length > max ? s.substr(0, max - 1) + "..." : s;
        }

        private static function sizeOf(b:DisplayObject):Rectangle {
            var w:Number = b.width;
            var h:Number = b.height;
            return new Rectangle(0, 0, w > 20 && w < 300 ? w : BTN_W, h > 10 && h < 60 ? h : BTN_H);
        }

        // a button's label: EA's by its label (set only when it changes: CLIK redraws at each set), ours by its text field
        private static function setLabel(b:Sprite, s:String):void {
            try {
                if (Object(b).label != s) {
                    Object(b).label = s;
                }
                return;
            } catch (err:Error) { // our framed Sprite has no label
            }
            var t:TextField = b.numChildren > 0 ? b.getChildAt(0) as TextField : null;
            if (t != null && t.text != s) {
                t.text = s;
            }
        }

        private static function setAlpha(d:DisplayObject, a:Number):void {
            if (d.alpha != a) {
                d.alpha = a;
            }
        }

        // a line too long for its width gets a smaller font (down to 9), then is cut with "..."
        private static function fit(tf:TextField, size:int, w:Number):void {
            var f:TextFormat = tf.defaultTextFormat;
            f.size = size;
            tf.setTextFormat(f);
            while (size > 9 && tf.textWidth + 4 > w) {
                size--;
                f.size = size;
                tf.setTextFormat(f);
            }
            if (tf.textWidth + 4 > w) {
                var s:String = tf.text;
                while (s.length > 3 && tf.textWidth + 4 > w) {
                    s = s.substr(0, s.length - 1);
                    tf.text = s + "...";
                }
            }
        }

        private static function field(font:String, size:int, color:uint, align:String):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = false;
            t.wordWrap = false;
            t.defaultTextFormat = new TextFormat(font, size, color, null, null, null, null, null, align);
            t.height = size + 8;
            return t;
        }

        // EA's small button (Button001, as RulesPanel's CHANGE) at its own size; a framed button of ours only if the
        // symbol isn't there
        private function button(label:String):Sprite {
            try {
                var ea:Sprite = new (getDefinitionByName(EA_BUTTON) as Class)() as Sprite;
                if (ea != null) {
                    Object(ea).label = label;
                    return ea;
                }
            } catch (err:Error) {
                say("draft button: " + err.message);
            }
            var b:Sprite = new Sprite();
            b.buttonMode = true;
            b.mouseChildren = false;
            var g:Graphics = b.graphics;
            g.lineStyle(1, FRAME, 0.9);
            g.beginFill(0x000000, 0.6);
            g.drawRect(0, 0, BTN_W, BTN_H);
            g.endFill();
            var t:TextField = field("$Header", 13, ACCENT, "center");
            t.text = label;
            t.width = BTN_W;
            t.y = -1;
            b.addChild(t);
            return b;
        }
    }
}
