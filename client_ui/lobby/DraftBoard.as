// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The custom game lobby's draft board, for a game whose match rules pick the shapers by draft. LobbyReady owns it, draws
// it on its overlay and hands it the server's event (the draft's second version):
//     draft.state {phase, reason?, turn, window, left, turnMs, teams, pool: [{id, name, catalogID, portrait?, card?}],
//                  turns: [{kind, team, player, shaper, how, window}], you?: {team, seat}, yourTurn,
//                  own?: {roles, hovers, ready, swaps: [{from, to}], trades: [{from, to}]}}      <- the server
//     draft.role {role}, draft.hover {shaper}, draft.ban {shaper}, draft.pick {shaper}, draft.swap {seat},
//     draft.swapAnswer {accept}, draft.trade {seat}, draft.tradeAnswer {accept}, draft.ready {}   -> the server
//
// The phases: role (every player chooses a role at once), ban and pick (the turns in windows: a double window has two
// open turns, one per seat, and both players choose and lock at once), trade (teammates may trade their shapers until
// every drafter is ready or the time is up), done; or cancelled. The event is the whole board, so the board draws from
// the last one alone: a late joiner, a new connection and a redraw need no history. It covers the spectators and both
// teams from the team titles down to just above START and LEAVE GAME, and right to the teams' boxes or the chat window,
// whichever ends first (the MATCH RULES summary above it and the chat beside it stay free; the lobby isn't scaled, so
// its units are the window's pixels and the board takes the room the window gives):
//   - the head row: whose turn it is (YOUR TURN, larger, on an orange band, the board framed in a glow; else the team
//     and its players on turn), the time left large in the middle with a bar of the step's time (turnMs) under it, the
//     turns as a row of chips (a circle per ban, a square per pick, in the team's colour: done faint, the open ones
//     filled and framed) to its right end;
//   - a column per team (Mortal left, Spirit right): its bans (two; the portrait darkened, a red cross) and a row per
//     pick (its player's name, the portrait and name of the shaper picked, "Picking..." on an open turn's row); the
//     team on turn has a glow of its colour behind its column. On the own team's rows only: each player's role (the
//     game's role icon beside the name; the own one opens the role chooser), a teammate's hover faint in its slot
//     ("Fenmore?"), SWAP (before the picks) or TRADE (in the trade phase) on a teammate's row, and the marks ASKED,
//     ASKS YOU and READY;
//   - the cards between them: every shaper of the pool by name, as the Collection's tall card art with its name below
//     where the grid fits them at least 40 units wide, else as square portraits; a banned one darkened with a red cross,
//     a picked one darkened with a bar in the picking team's colour. In the role phase (and whenever a drafter needs a
//     role or opens the chooser) the four roles cover them instead: the game's role icons with their names;
//   - the bottom row: what to do now; ACCEPT and DECLINE for a teammate's request, READY in the trade phase, and for a
//     player with an open turn BAN or LOCK IN (EA's Button001).
// A card click is the player's hover (draft.hover: its team sees it, and a turn whose time runs out takes it); a player
// with an open turn then clicks BAN or LOCK IN, which is the call; the server's next draft.state says how things stand
// (the turn locked, or the same board again: a refused call). The time left counts down from the event's "left" on this
// client's own clock. When the draft is cancelled (a drafter left) the board closes and a line above the lobby says why
// for a few seconds; when it is done the board stays until the game starts. The enemy's roles, hovers and requests are
// never sent, so never drawn; a spectator sees the public board only.
//
// Everything the server sends is untrusted: texts go into .text only (never htmlText), clipped; lists are cut short; a
// picture is loaded (img://, the game's own texture lookup, as the Collection's IconLoader) only when its path has the
// form of a shaper picture, Heroes/<letters>/default/ui/Textures/<letters and _>.dds, or is one of the four role icons
// named here, so a server can't make the class load anything else; the ids sent back come from the pool, the role list
// and the seats of the own team only. Errors go to LobbyReady's say (a few Assert lines at most); nothing is logged in
// normal use.
package {
    import flash.display.Bitmap;
    import flash.display.DisplayObject;
    import flash.display.GradientType;
    import flash.display.Graphics;
    import flash.display.Loader;
    import flash.display.LoaderInfo;
    import flash.display.Shape;
    import flash.display.Sprite;
    import flash.display.Stage;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.MouseEvent;
    import flash.events.TimerEvent;
    import flash.filters.GlowFilter;
    import flash.geom.ColorTransform;
    import flash.geom.Matrix;
    import flash.geom.Point;
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
        private static const ROLE_LINE:String = "THE PLAYERS CHOOSE THEIR ROLES";
        private static const ROLE_HINT:String = "Every player chooses a role; the bans begin when the time is up.";
        private static const ROLE_CHOOSE:String = "Choose your role. Your team sees it, the enemy doesn't.";
        private static const ROLE_CHANGE:String = "You can change your role until the draft ends.";
        private static const TRADE_LINE:String = "THE TEAMS MAY TRADE";
        private static const TRADE_HINT:String = "Teammates may trade their shapers; the game starts when the time is up.";
        private static const TRADE_OWN:String = "Click TRADE on a teammate's row to offer a trade, then READY.";
        private static const TRADE_READY:String = "You are ready. The game starts once every player is ready or the time is up.";
        private static const NO_ROLE:String = "Choose your role first: the server takes no ban, pick or hover without one.";
        private static const NO_ROLE_CLICK:String = "Choose your role first.";
        private static const WATCHING:String = "You are watching the draft.";
        private static const CANCELLED:String = "The draft was cancelled: ";
        private static const REFUSED:String = "The server didn't take that choice. Choose again.";
        private static const REQ_REFUSED:String = "The server didn't take that request.";
        private static const CHOOSER_TITLE:String = "CHOOSE YOUR ROLE";
        private static const BTN_READY:String = "READY";
        private static const BTN_ACCEPT:String = "ACCEPT";
        private static const BTN_DECLINE:String = "DECLINE";
        private static const RB_SWAP:String = "SWAP";
        private static const RB_TRADE:String = "TRADE";
        private static const RB_ASKED:String = "ASKED";
        private static const RB_ASKS:String = "ASKS YOU";
        private static const RB_READY:String = "READY";
        private static const ROLES:Array = ["gladiator", "tactician", "hunter", "predator"]; // the wire's role ids
        private static const ROLE_NAMES:Array = ["Gladiator", "Tactician", "Hunter", "Predator"];
        private static const ROLE_SHORT:Array = ["GLA", "TAC", "HUN", "PRE"]; // under the icon (shows if it can't load)
        // the game's role icons (RolesTuning's icon of each role, in UI_Icons.snap; its roles Carry, Support, Jungler
        // and Roamer are the Gladiator, Tactician, Hunter and Predator, by their passives modifier_Role_<name>)
        private static const ROLE_ICONS:Array = ["UI/Icons/RolesIcons/textures/Role_Carry.dds",
            "UI/Icons/RolesIcons/textures/Role_Support.dds", "UI/Icons/RolesIcons/textures/Role_Jungler.dds",
            "UI/Icons/RolesIcons/textures/Role_Roamer.dds"];

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

        // the layout (overlay units: the window's pixels; the lobby isn't scaled)
        private static const PAD:int = 12;
        private static const LEFT:int = 16;         // the board's left edge (the spectators' column starts there)
        private static const HEAD_H:int = 46;       // the turn line, the timer and its bar, the chips
        private static const FOOT_H:int = 30;       // the hint and the button
        private static const FALLBACK_H:int = 455;  // the board's height when the stage can't be measured
        private static const MIN_W:int = 560;
        private static const MIN_H:int = 300;
        private static const START_GAP:int = 6;     // the board's bottom above START's and LEAVE GAME's top
        private static const START_ABOVE:int = 81;  // START's top above the stage's bottom (at every size, DRAFT0)
        private static const CHAT_W:Number = 297;   // EA's chat window's width and right margin in 768-high units
        private static const CHAT_GAP:int = 8;      // the board's right edge before the chat window's left
        private static const TEAM_W_MAX:int = 190;  // a team's column
        private static const TEAM_W_MIN:int = 150;
        private static const COL_GAP:int = 16;
        private static const BAN_S:int = 44;
        private static const PICK_H_MAX:int = 56;   // a pick row (its portrait 6 less)
        private static const PICK_H_MIN:int = 36;
        private static const NAME_H:int = 16;       // a card's name line
        private static const CELL_GAP:int = 8;
        private static const CARD_MAX_W:int = 64;   // the card texture's own width: never drawn larger
        private static const TALL_MIN_W:int = 40;   // narrower tall cards give way to square portraits
        private static const SQUARE_MAX:int = 96;
        private static const TEAM_TITLE_H:int = 34; // the lobby's team titles above its first cells (RulesPanel's)
        private static const TIMER_W:int = 110;
        private static const CHIP_W:int = 22;       // a chip's pitch at most
        private static const CHIP_S:int = 16;       // a chip's size at most
        private static const BTN_W:int = 76;        // the fallback button's size
        private static const BTN_H:int = 19;
        private static const BTN_GAP:int = 8;       // between the bottom row's buttons
        private static const TAG_S:int = 18;        // a pick row's role tag, at its name line's inner end
        private static const RB_W:int = 50;         // a pick row's SWAP / TRADE button or mark, at its shaper line's
        private static const RB_H:int = 16;         // inner end
        private static const TILE_MAX:int = 120;    // a role tile of the chooser
        private static const TILE_GAP:int = 12;
        private static const ICON_MAX:int = 64;
        private static const TIMES_UP_MS:int = 10000; // an own turn's last seconds: the hint says what the timeout does

        // what is read of an event
        private static const MAX_POOL:int = 60;
        private static const MAX_TURNS:int = 32;
        private static const MAX_SEATS:int = 5;
        private static const MAX_BANS:int = 3;   // ban slots built a team; as many show as the draft's bans a team (2)
        private static const MAX_LEFT_MS:int = 600000;

        private static const CALL_GAP_MS:int = 300;
        private static const SENT_MS:int = 5000;   // a call with no answer by then is forgotten (the player may retry)
        private static const FLASH_MS:int = 4000;  // a click's answer in the hint line
        private static const NOTE_MS:int = 12000;  // the cancel line
        private static const EA_BUTTON:String = "Button001";

        private var channel:ServerChannel;
        private var say:Function;
        public var redraw:Function = null; // the owner's (LobbyReady's) redraw

        // the board as the last event says (phase "": none)
        private var phase:String = "";
        private var turn:int = -1;         // the current window's first turn
        private var window:int = -1;       // the current window (-1 outside the ban and pick phases)
        private var deadline:int = 0;      // getTimer() when the step's time runs out
        private var turnMs:int = 0;        // the step's full time
        private var teams:Array = [[], []];
        private var pool:Array = [];       // {id, name, portrait, card}, by name
        private var byId:Object = {};
        private var poolKey:String = "";
        private var turns:Array = [];      // {kind, team: 0 or 1, player, shaper, how, window}
        private var you:Object = null;     // {team: 0 or 1, seat}, null for a spectator
        private var yourTurn:Boolean = false;
        private var myTurn:int = -1;       // the index of the receiver's open turn while yourTurn
        private var own:Object = null;     // the own team's part, by seat: {roles, hovers, ready, swaps, trades}
        private var stateKey:String = "";  // the board's content (all but the time): a resend after a refusal is equal

        private var sel:String = "";       // the card the player chose: its hover (a pool id), the server's once answered
        private var sent:Object = null;    // {idx, id, key, at}: the ban or pick call made and not yet answered
        private var hoverReq:Object = null; // {id, key, at}: the hover call not yet answered
        private var req:Object = null;     // {kind, val, key, at}: a role, swap, trade, answer or ready call not answered
        private var noRepeat:Object = {};  // "swap:<name>", "trade:<name>": the own request the teammate declined or let
                                           // expire (the server refuses it again in this draft)
        private var chooserOpen:Boolean = false; // the role chooser opened by the own row's role tag
        private var lastCall:int = -100000;
        private var flashText:String = "";
        private var flashUntil:int = 0;
        private var noteText:String = "";
        private var noteUntil:int = 0;

        // the drawing
        private var layer:Sprite = null;   // the board, on LobbyReady's overlay
        private var rect:Rectangle = null; // where it is, in the overlay's units
        private var coverW:Number = 0;     // the dark strip right of the board, up to the tiles' right edge (measure)
        private var builtCoverW:Number = -1;
        private var noteTf:TextField = null; // the cancel line, on the overlay
        private var dirty:Boolean = true;
        private var boardW:Number = 0;
        private var boardH:Number = 0;
        private var teamW:Number = TEAM_W_MAX;
        private var pickH:Number = PICK_H_MAX;
        private var picksY:Number = 0;     // the first pick row's top
        private var square:Boolean = false; // the cards are square portraits (the window is too short for tall ones)
        private var turnTf:TextField = null;
        private var band:Shape = null;     // the orange band behind YOUR TURN
        private var turnFrame:Shape = null; // the board's glowing frame while it is the player's turn
        private var glows:Array = null;    // per team: the glow behind its column while it is on turn
        private var timerTf:TextField = null;
        private var timeBar:Shape = null;
        private var barLook:String = "";
        private var hintTf:TextField = null;
        private var chips:Sprite = null;    // a Shape per turn
        private var cols:Array = null;     // per team {title, bans: [slot], picks: [{slot, who, what, y}]}
        private var grid:Sprite = null;
        private var cells:Array = [];      // {sp, ld, id, name, frame, mark, nm, aw, ah}
        private var actBtn:Sprite = null;
        private var readyBtn:Sprite = null;
        private var acceptBtn:Sprite = null;
        private var declineBtn:Sprite = null;
        private var hintText:String = "";  // the hint's whole text (fit may cut what shows)
        private var chooser:Sprite = null; // the four roles over the cards
        private var tiles:Array = null;    // per role {sp, frame, nm, mates, ld}
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
            if (ph != "role" && ph != "ban" && ph != "pick" && ph != "trade" && ph != "done") {
                return; // nothing this board knows
            }
            var p:Array = [];
            var ids:Object = {};
            var ts:Array = [];
            var tm:Array = [[], []];
            var y:Object = null;
            var ow:Object = null;
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
                        how: str(t.how, 16), window: t.window is Number ? int(t.window) : ts.length});
                }
                if (d.teams is Array) {
                    for (var i:int = 0; i < 2 && i < d.teams.length; i++) {
                        for each (var n:Object in d.teams[i]) {
                            if (tm[i].length < MAX_SEATS) {
                                tm[i].push(str(n, 30));
                            }
                        }
                    }
                }
                if (d.you != null && (int(d.you.team) == 1 || int(d.you.team) == 2)) {
                    y = {team: int(d.you.team) - 1, seat: Math.max(0, Math.min(MAX_SEATS - 1, int(d.you.seat)))};
                    ow = d.own != null ? ownOf(d.own) : null;
                }
            } catch (err:Error) {
                say("draft state: " + err.message);
                return;
            }
            var key:String = "";
            for each (var q:Object in p) {
                key += q.id + "|" + q.card + "|" + q.portrait + ";";
            }
            var board:Boolean = ph == "ban" || ph == "pick";
            var nextTurn:int = board ? int(d.turn) : -1;
            if (nextTurn < 0 || nextTurn >= ts.length) {
                nextTurn = -1;
            }
            var win:int = nextTurn >= 0 && d.window is Number ? int(d.window) : -1;
            if (win < 0 && nextTurn >= 0) {
                win = ts[nextTurn].window; // a server of the first version: one turn at a time
            }
            var left:int = Math.max(0, Math.min(MAX_LEFT_MS, int(d.left)));
            // the receiver's open turn: its seat's (by name), in the current window, not locked yet
            var me:String = y != null && y.seat < tm[y.team].length ? tm[y.team][y.seat] : "";
            var mt:int = -1;
            var fallback:int = -1;
            for (var j:int = 0; j < ts.length && win >= 0 && y != null; j++) {
                var tj:Object = ts[j];
                if (tj.window == win && tj.how == "" && tj.team == y.team) {
                    if (tj.player == me) {
                        mt = j;
                        break;
                    }
                    if (fallback < 0) {
                        fallback = j;
                    }
                }
            }
            var mine:Boolean = d.yourTurn === true && y != null && win >= 0 && (mt >= 0 || fallback >= 0);
            if (!mine) {
                mt = -1;
            } else if (mt < 0) {
                mt = fallback;
            }
            var sk:String = ph + "/" + win + "/" + tm[0].join(",") + "/" + tm[1].join(",") + "/";
            for each (var tk:Object in ts) {
                sk += tk.kind + tk.team + ":" + tk.player + ":" + tk.shaper + ":" + tk.how + ":" + tk.window + ";";
            }
            if (ow != null) {
                sk += "/" + ow.roles.join(",") + "/" + ow.hovers.join(",") + "/" + ow.ready.join(",") + "/" +
                    pairKey(ow.swaps) + "/" + pairKey(ow.trades);
            }
            if (win != window || ph != phase) {
                flashText = ""; // a click's answer belongs to its window ("Not your turn: rules5a bans now.")
            }
            // the call in flight: answered by the turn locked (or gone), or by the same board again, which is the
            // server's refusal; anything else (the teammate of a double window locked, a hover) leaves it waiting
            if (sent != null) {
                if (!mine || mt != sent.idx) {
                    sent = null;
                } else if (sk == sent.key) {
                    flashText = REFUSED;
                    flashUntil = getTimer() + FLASH_MS;
                    sent = null;
                } else {
                    sent.key = sk;
                }
            }
            // the hover in flight: answered when the team's part names it, refused when the board came back unchanged
            if (hoverReq != null) {
                if (ow == null || y == null || ow.hovers[y.seat] == hoverReq.id || sk == hoverReq.key) {
                    hoverReq = null;
                } else {
                    hoverReq.key = sk;
                }
            }
            // the other requests the same way (each answered by what it changes in the team's part)
            if (req != null) {
                if (sk == req.key) {
                    flashText = REQ_REFUSED;
                    flashUntil = getTimer() + FLASH_MS;
                    req = null;
                } else if (ow == null || y == null || ph != phase || answered(req, ow, y.seat)) {
                    req = null;
                } else {
                    req.key = sk;
                }
            }
            noteEnded(ow, y, ts);
            if (ow != null && y != null) {
                // the choice is the player's hover as the server has it (cleared at its pick, when its shaper is taken,
                // in the trade phase), unless a hover of its own is still on its way
                if (hoverReq == null) {
                    sel = ow.hovers[y.seat];
                }
            } else if (mt != myTurn || !mine) {
                sel = ""; // a server without the team's part (the first version): a choice lasts for its turn only
            }
            if (sel != "" && (ids[sel] == null || stateOf(sel, ts) != 0)) {
                sel = "";
            }
            if (ph != phase && (ph == "role" || ph == "done")) {
                chooserOpen = false;
            }
            phase = ph;
            turn = nextTurn;
            window = win;
            deadline = getTimer() + left;
            turnMs = Math.max(0, Math.min(MAX_LEFT_MS, int(d.turnMs)));
            teams = tm;
            pool = p;
            byId = ids;
            turns = ts;
            you = y;
            yourTurn = mine;
            myTurn = mt;
            own = ow;
            stateKey = sk;
            noteText = "";
            if (key != poolKey) {
                poolKey = key;
                clearGrid();
            }
            dirty = true;
        }

        // the own team's part, each list by seat: a role id of the four or "", a pool id or "", a ready mark, the
        // pending requests {from, to} (seats)
        private static function ownOf(o:Object):Object {
            var r:Object = {roles: [], hovers: [], ready: [], swaps: pairs(o.swaps), trades: pairs(o.trades)};
            for (var i:int = 0; i < MAX_SEATS; i++) {
                var role:String = o.roles is Array && i < o.roles.length ? str(o.roles[i], 16) : "";
                r.roles.push(ROLES.indexOf(role) >= 0 ? role : "");
                r.hovers.push(o.hovers is Array && i < o.hovers.length ? idOf(o.hovers[i]) : "");
                r.ready.push(o.ready is Array && i < o.ready.length && o.ready[i] === true);
            }
            return r;
        }

        private static function pairs(v:*):Array {
            var r:Array = [];
            if (!(v is Array)) {
                return r;
            }
            for each (var p:Object in v) {
                if (r.length >= MAX_SEATS || p == null) {
                    break;
                }
                var f:int = int(p.from);
                var t:int = int(p.to);
                if (f >= 0 && f < MAX_SEATS && t >= 0 && t < MAX_SEATS && f != t) {
                    r.push({from: f, to: t});
                }
            }
            return r;
        }

        private static function pairKey(a:Array):String {
            var s:String = "";
            for each (var p:Object in a) {
                s += p.from + ">" + p.to + ",";
            }
            return s;
        }

        private static function hasPair(a:Array, from:int, to:int):Boolean {
            for each (var p:Object in a) {
                if (p.from == from && p.to == to) {
                    return true;
                }
            }
            return false;
        }

        // the request r (a call of the player at seat) is answered by the team's part ow (a swap or trade request by
        // any change: the teammate may have answered it before this client saw it pending)
        private static function answered(r:Object, ow:Object, seat:int):Boolean {
            switch (r.kind) {
                case "role":
                    return ow.roles[seat] == r.val;
                case "swapAnswer":
                    return !hasPair(ow.swaps, int(r.val), seat);
                case "tradeAnswer":
                    return !hasPair(ow.trades, int(r.val), seat);
                case "ready":
                    return ow.ready[seat] === true;
            }
            return true;
        }

        // The own requests that ended since the last event: one the teammate accepted changed the turns (a swap: the
        // own pick turn is the teammate's now; a trade: the own pick's shaper changed), any other was declined or
        // expired, and the server refuses it again in this draft, so its button stays away (noRepeat).
        private function noteEnded(ow:Object, y:Object, ts:Array):void {
            if (own == null || ow == null || you == null || y == null || you.team != y.team || you.seat != y.seat ||
                you.seat >= teams[you.team].length) {
                return;
            }
            var mates:Array = teams[you.team];
            var mi:int = pickTurnOf(you.team, you.seat);
            for each (var kind:String in ["swap", "trade"]) {
                for each (var p:Object in (kind == "swap" ? own.swaps : own.trades)) {
                    if (p.from != you.seat || p.to >= mates.length ||
                        hasPair(kind == "swap" ? ow.swaps : ow.trades, p.from, p.to)) {
                        continue;
                    }
                    var other:String = mates[p.to];
                    var took:Boolean = mi >= 0 && mi < ts.length && (kind == "swap" ? ts[mi].player == other :
                        ts[mi].shaper != turns[mi].shaper);
                    if (!took) {
                        noRepeat[kind + ":" + other] = true;
                    }
                }
            }
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
            window = -1;
            turnMs = 0;
            teams = [[], []];
            pool = [];
            byId = {};
            poolKey = "";
            turns = [];
            you = null;
            yourTurn = false;
            myTurn = -1;
            own = null;
            stateKey = "";
            sel = "";
            sent = null;
            hoverReq = null;
            req = null;
            noRepeat = {};
            chooserOpen = false;
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
                        unload(r.tag.ld);
                        r.tag.sp.removeEventListener(MouseEvent.CLICK, onTag);
                        r.rb.removeEventListener(MouseEvent.CLICK, onRowButton);
                    }
                }
            }
            cols = null;
            for each (var tl:Object in tiles) {
                unload(tl.ld);
                tl.sp.removeEventListener(MouseEvent.CLICK, onTile);
            }
            tiles = null;
            chooser = null;
            if (actBtn != null) {
                actBtn.removeEventListener(MouseEvent.CLICK, onAct);
                readyBtn.removeEventListener(MouseEvent.CLICK, onReady);
                acceptBtn.removeEventListener(MouseEvent.CLICK, onAnswer);
                declineBtn.removeEventListener(MouseEvent.CLICK, onAnswer);
            }
            actBtn = readyBtn = acceptBtn = declineBtn = null;
            hintText = "";
            if (layer != null && layer.parent != null) {
                layer.parent.removeChild(layer);
            }
            layer = null;
            rect = null;
            grid = null;
            chips = null;
            band = null;
            turnFrame = null;
            glows = null;
            timeBar = null;
            barLook = "";
            turnTf = timerTf = hintTf = null;
            boardW = boardH = 0;
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
            var r:Rectangle = measure(overlay, view);
            if (r == null) {
                return; // not laid out yet
            }
            if (layer != null && (r.width != boardW || r.height != boardH || coverW != builtCoverW)) {
                removeBoard(); // the window's size changed: build again at the new size
            }
            if (layer == null) {
                boardW = r.width;
                boardH = r.height;
                build();
            }
            if (layer.parent != overlay) {
                overlay.addChild(layer);
            } else if (overlay.getChildIndex(layer) != overlay.numChildren - 1) {
                overlay.setChildIndex(layer, overlay.numChildren - 1); // above the tiles' marks and the rules
            }
            layer.x = r.x;
            layer.y = r.y;
            rect = r;
            if (dirty) {
                dirty = false;
                refresh();
            }
            placeButtons();
        }

        // The board's place (DRAFT0's measurements): its top the team titles' (box1_0's top less the titles), its
        // bottom 6 above START and LEAVE GAME (their tops; 81 above the stage's bottom at every size when they can't be
        // measured), its right edge box1_0's right or 8 before EA's chat window, whichever is less. The chat window
        // lies above the lobby, scaled by the stage's height / 768 and placed from the stage's right edge, so its left
        // is computed (CHAT_W); a UI class here has no stage of its own, the overlay's is the lobby's.
        private function measure(overlay:Sprite, view:Object):Rectangle {
            var c1:DisplayObject = view.box1_0 as DisplayObject;
            if (c1 == null) {
                return null;
            }
            var r1:Rectangle = c1.getBounds(overlay);
            if (r1.width < 50 || r1.height < 10) {
                return null;
            }
            var top:Number = Math.round(r1.y - TEAM_TITLE_H);
            var right:Number = r1.right;
            var bottom:Number = top + FALLBACK_H;
            var st:Stage = overlay.stage;
            if (st != null && st.stageWidth > 0 && st.stageHeight > 0) {
                var o:Point = overlay.globalToLocal(new Point(0, 0));
                var e:Point = overlay.globalToLocal(new Point(st.stageWidth, st.stageHeight));
                var k:Number = (e.x - o.x) / st.stageWidth; // overlay units per stage pixel (1: the lobby isn't scaled)
                bottom = e.y - (START_ABOVE + START_GAP) * k;
                for each (var b:Object in [view.startButton, view.cancelButton]) {
                    var bd:DisplayObject = b as DisplayObject;
                    if (bd == null) {
                        continue;
                    }
                    var rb:Rectangle = bd.getBounds(overlay);
                    if (rb.height >= 10 && rb.y > top + MIN_H / 2 && rb.y < e.y) {
                        bottom = Math.min(bottom, rb.y - START_GAP);
                    }
                }
                bottom = Math.min(bottom, e.y - 4);
                right = Math.min(right, e.x - CHAT_W * (st.stageHeight / 768) * k - CHAT_GAP, e.x - 4);
            }
            var r:Rectangle = new Rectangle(LEFT, top, Math.max(MIN_W, Math.round(right - LEFT)),
                Math.max(MIN_H, Math.round(bottom - top)));
            // the teams' tiles reach on under EA's chat window, which lets them show through it (a green READY, the
            // crown at 1024 x 768): a plain dark strip of the board's height covers them up to box1_0's right edge
            coverW = Math.max(0, Math.round(r1.right) + 4 - r.right);
            return r;
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

        // the head row's halves beside the timer: the turn line's width on the left, the chips' room on the right
        private function get headSide():Number {
            return Math.floor(boardW / 2 - TIMER_W / 2) - PAD - 8;
        }

        private function build():void {
            teamW = Math.max(TEAM_W_MIN, Math.min(TEAM_W_MAX, Math.round(boardW * 0.17)));
            picksY = PAD + HEAD_H + 20 + BAN_S + 10;
            pickH = Math.max(PICK_H_MIN, Math.min(PICK_H_MAX, Math.floor((boardH - FOOT_H - 4 - picksY) / MAX_SEATS)));
            layer = new Sprite();
            layer.mouseEnabled = true; // its fill takes the clicks that would reach the lobby's tiles below
            var g:Graphics = layer.graphics;
            g.lineStyle(1, FRAME, 0.9);
            g.beginFill(0x0A0A0C, 1); // opaque: at 0.94 the lobby's titles under the head row showed through
            g.drawRect(0, 0, boardW, boardH);
            g.endFill();
            g.lineStyle(2, ACCENT, 1);
            g.moveTo(1, 1);
            g.lineTo(boardW - 1, 1);
            builtCoverW = coverW;
            if (coverW > 0) {
                // the strip right of the board (under the chat window): a Sprite of its own, which also takes the clicks
                // that would reach the tiles there
                var cover:Sprite = new Sprite();
                cover.x = boardW + 1;
                cover.graphics.beginFill(0x0A0A0C, 1);
                cover.graphics.drawRect(0, 0, coverW, boardH);
                cover.graphics.endFill();
                layer.addChild(cover);
            }

            glows = [teamGlow(0), teamGlow(1)]; // behind the columns
            turnFrame = new Shape();
            turnFrame.graphics.lineStyle(2, ACCENT, 1);
            turnFrame.graphics.drawRect(1, 1, boardW - 2, boardH - 2);
            turnFrame.filters = [new GlowFilter(ACCENT, 0.9, 14, 14, 2, 2)];
            turnFrame.visible = false;
            layer.addChild(turnFrame);
            band = gradient(headSide + PAD + 24, 34, ACCENT, [0.45, 0.25, 0], [0, 150, 255]);
            band.x = 2;
            band.y = PAD - 4;
            band.visible = false;
            layer.addChild(band);

            turnTf = field("$Header", 22, WHITE, "left");
            turnTf.x = PAD;
            turnTf.y = PAD + 1;
            turnTf.width = headSide;
            layer.addChild(turnTf);
            timerTf = field("$Header", 32, ACCENT, "center");
            timerTf.width = TIMER_W;
            timerTf.x = Math.round((boardW - TIMER_W) / 2);
            timerTf.y = PAD - 10;
            layer.addChild(timerTf);
            timeBar = new Shape();
            timeBar.x = timerTf.x + 10;
            timeBar.y = PAD + HEAD_H - 12;
            layer.addChild(timeBar);
            chips = new Sprite();
            chips.mouseEnabled = false;
            chips.mouseChildren = false;
            chips.y = PAD + 10;
            layer.addChild(chips);

            cols = [teamColumn(0, PAD), teamColumn(1, boardW - PAD - teamW)];

            grid = new Sprite();
            grid.x = PAD + teamW + COL_GAP;
            grid.y = PAD + HEAD_H;
            layer.addChild(grid);
            buildChooser();

            hintTf = field("$Body", 14, DIM, "left");
            hintTf.x = PAD;
            hintTf.y = boardH - FOOT_H + 4;
            hintTf.width = boardW - 2 * PAD - 160;
            layer.addChild(hintTf);
            actBtn = button(BTN_BAN);
            actBtn.addEventListener(MouseEvent.CLICK, onAct, false, 0, false);
            readyBtn = button(BTN_READY);
            readyBtn.addEventListener(MouseEvent.CLICK, onReady, false, 0, false);
            acceptBtn = button(BTN_ACCEPT);
            acceptBtn.addEventListener(MouseEvent.CLICK, onAnswer, false, 0, false);
            declineBtn = button(BTN_DECLINE);
            declineBtn.addEventListener(MouseEvent.CLICK, onAnswer, false, 0, false);
            for each (var fb:Sprite in [actBtn, readyBtn, acceptBtn, declineBtn]) {
                fb.visible = false;
                layer.addChild(fb);
            }
            startTick();
            dirty = true;
        }

        // The role chooser over the cards: a title and the four roles side by side, each the game's role icon (its
        // short name under it shows when the icon can't load), the role's name and the teammates who chose it. Shown
        // to a drafter in the role phase, while it has no role in the ban and pick phases, and when it clicks its own
        // row's role tag.
        private function buildChooser():void {
            var W:Number = boardW - 2 * (PAD + teamW + COL_GAP);
            var H:Number = boardH - PAD - HEAD_H - FOOT_H - 4;
            chooser = new Sprite();
            chooser.x = grid.x - 4;
            chooser.y = grid.y;
            chooser.graphics.beginFill(0x0A0A0C, 1); // opaque: the cards under it take no clicks
            chooser.graphics.drawRect(0, 0, W + 8, H);
            chooser.graphics.endFill();
            chooser.visible = false;
            layer.addChild(chooser);
            var tw:Number = Math.min(TILE_MAX, Math.floor((W - 3 * TILE_GAP) / 4));
            var s:Number = Math.max(24, Math.min(ICON_MAX, tw - 16, H - 110));
            var th:Number = s + 70;
            var x0:Number = 4 + Math.round((W - (4 * tw + 3 * TILE_GAP)) / 2);
            var y0:Number = Math.max(30, Math.round((H - th - 30) / 2) + 30);
            var title:TextField = field("$Header", 18, WHITE, "center");
            title.text = CHOOSER_TITLE;
            title.width = W;
            title.x = 4;
            title.y = y0 - 32;
            chooser.addChild(title);
            tiles = [];
            for (var i:int = 0; i < ROLES.length; i++) {
                var sp:Sprite = new Sprite();
                sp.x = x0 + i * (tw + TILE_GAP);
                sp.y = y0;
                sp.name = String(i);
                sp.buttonMode = true;
                sp.mouseChildren = false;
                var g:Graphics = sp.graphics;
                g.lineStyle(1, FRAME, 0.9);
                g.beginFill(SLOT_FILL, 1);
                g.drawRect(0, 0, tw, th);
                g.endFill();
                var abbr:TextField = field("$Header", 13, DARK, "center");
                abbr.text = ROLE_SHORT[i];
                abbr.width = tw;
                abbr.y = 8 + (s - 21) / 2;
                sp.addChild(abbr);
                var ld:Loader = load(ROLE_ICONS[i], s, s);
                ld.x = Math.round((tw - s) / 2);
                ld.y = 8;
                sp.addChild(ld);
                var nm:TextField = field("$Header", 15, WHITE, "center");
                nm.text = ROLE_NAMES[i];
                nm.width = tw;
                nm.y = s + 14;
                fit(nm, 15, tw - 4);
                sp.addChild(nm);
                var mates:TextField = field("$Body", 11, DIM, "center");
                mates.width = tw;
                mates.y = s + 38;
                sp.addChild(mates);
                var frame:Shape = new Shape();
                frame.graphics.lineStyle(2, ACCENT, 1);
                frame.graphics.drawRect(-1, -1, tw + 2, th + 2);
                frame.visible = false;
                sp.addChild(frame);
                sp.addEventListener(MouseEvent.CLICK, onTile, false, 0, false);
                chooser.addChild(sp);
                tiles.push({sp: sp, frame: frame, nm: nm, mates: mates, ld: ld, w: tw, matesText: ""});
            }
        }

        // a horizontal linear gradient of one colour, its alpha ramped (a Shape of its own, its path from its origin)
        private static function gradient(w:Number, h:Number, color:uint, alphas:Array, ratios:Array):Shape {
            var sh:Shape = new Shape();
            var m:Matrix = new Matrix();
            m.createGradientBox(w, h, 0, 0, 0);
            var colors:Array = [];
            for (var i:int = 0; i < alphas.length; i++) {
                colors.push(color);
            }
            sh.graphics.beginGradientFill(GradientType.LINEAR, colors, alphas, ratios, m);
            sh.graphics.drawRect(0, 0, w, h);
            sh.graphics.endFill();
            return sh;
        }

        // the glow behind a team's column while it is on turn: its colour fading from the board's edge towards the
        // cards, and a glowing line along the edge (each figure a Shape of its own)
        private function teamGlow(team:int):Sprite {
            var w:Number = PAD + teamW + COL_GAP / 2;
            var top:Number = PAD + HEAD_H - 6;
            var h:Number = Math.min(boardH - FOOT_H - 2, picksY + MAX_SEATS * pickH + 4) - top;
            var sp:Sprite = new Sprite();
            sp.mouseEnabled = false;
            sp.mouseChildren = false;
            sp.x = team == 0 ? 1 : boardW - 1 - w;
            sp.y = top;
            var left:Boolean = team == 0;
            sp.addChild(gradient(w, h, TEAM_COLORS[team], left ? [0.5, 0.2, 0] : [0, 0.2, 0.5],
                left ? [0, 120, 255] : [0, 135, 255]));
            var edge:Shape = new Shape();
            edge.x = left ? 1 : w - 1;
            edge.graphics.lineStyle(3, TEAM_COLORS[team], 1);
            edge.graphics.lineTo(0, h);
            edge.filters = [new GlowFilter(TEAM_COLORS[team], 0.9, 12, 12, 2, 2)];
            sp.addChild(edge);
            sp.visible = false;
            layer.addChild(sp);
            return sp;
        }

        private function startTick():void {
            if (tick == null) {
                tick = new Timer(250);
                tick.addEventListener(TimerEvent.TIMER, onTick, false, 0, false);
            }
            tick.start();
        }

        // a team's column: its name, three ban slots, a row per pick (pickH high, from picksY; a row's player line has
        // room to its side for more of the seat, e.g. its role)
        private function teamColumn(team:int, x:Number):Object {
            var left:Boolean = team == 0;
            var title:TextField = field("$Header", 16, TEAM_COLORS[team], left ? "left" : "right");
            title.text = TEAM_NAMES[team];
            title.x = x;
            title.y = PAD + HEAD_H - 4;
            title.width = teamW;
            layer.addChild(title);
            var y:Number = PAD + HEAD_H + 20;
            var bans:Array = [];
            for (var b:int = 0; b < MAX_BANS; b++) {
                var bx:Number = left ? x + b * (BAN_S + 8) : x + teamW - (b + 1) * BAN_S - b * 8;
                bans.push(slot(bx, y, BAN_S));
            }
            var ps:Number = pickH - 6;
            var picks:Array = [];
            for (var p:int = 0; p < MAX_SEATS; p++) {
                var py:Number = picksY + p * pickH;
                var sx:Number = left ? x : x + teamW - ps;
                var row:Object = {slot: slot(sx, py, ps), y: py, seat: -1, rbMode: "", left: left};
                row.who = field("$Body", 13, DIM, left ? "left" : "right");
                row.what = field("$Header", 15, WHITE, left ? "left" : "right");
                for each (var t:TextField in [row.who, row.what]) {
                    t.width = teamW - ps - 10;
                    t.x = left ? x + ps + 10 : x;
                    layer.addChild(t);
                }
                row.who.y = py + Math.max(0, Math.floor((ps - 40) / 2));
                row.what.y = row.who.y + 18;
                // the name line leaves room for the role tag at its inner end (towards the cards), the shaper line
                // for the row's button while one shows
                row.who.width = teamW - ps - 10 - TAG_S - 4;
                row.who.x = left ? x + ps + 10 : x + TAG_S + 4;
                row.textX = left ? x + ps + 10 : x;
                row.textW = teamW - ps - 10;
                row.tag = roleTag(left ? x + teamW - TAG_S : x, row.who.y + 1);
                row.rb = rowButton(left ? x + teamW - RB_W : x, row.what.y + 2);
                picks.push(row);
            }
            return {title: title, bans: bans, picks: picks};
        }

        // a pick row's role tag: a framed square with the role's short name, the role's icon over it once it loads; a
        // "?" on the own row without a role; the own row's tag opens the role chooser
        private function roleTag(x:Number, y:Number):Object {
            var sp:Sprite = new Sprite();
            sp.x = x;
            sp.y = y;
            sp.mouseChildren = false;
            var g:Graphics = sp.graphics;
            g.lineStyle(1, FRAME, 0.9);
            g.beginFill(SLOT_FILL, 1);
            g.drawRect(0, 0, TAG_S, TAG_S);
            g.endFill();
            var label:TextField = field("$Header", 9, LIGHT, "center");
            label.width = TAG_S + 4;
            label.x = -2;
            label.y = (TAG_S - 17) / 2;
            sp.addChild(label);
            sp.visible = false;
            sp.addEventListener(MouseEvent.CLICK, onTag, false, 0, false);
            layer.addChild(sp);
            return {sp: sp, label: label, ld: null, role: "*", mine: false}; // role "*": not drawn yet
        }

        // a pick row's button (SWAP, TRADE: framed, orange) or mark (ASKED, ASKS YOU, READY: plain)
        private function rowButton(x:Number, y:Number):Sprite {
            var sp:Sprite = new Sprite();
            sp.x = x;
            sp.y = y;
            sp.mouseChildren = false;
            var t:TextField = field("$Header", 11, ACCENT, "center");
            t.width = RB_W;
            t.y = -2;
            sp.addChild(t);
            sp.visible = false;
            sp.addEventListener(MouseEvent.CLICK, onRowButton, false, 0, false);
            layer.addChild(sp);
            return sp;
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

        // the turns open now: those of the current window not locked yet (none outside the ban and pick phases)
        private function openTurns():Array {
            var r:Array = [];
            if (window < 0 || (phase != "ban" && phase != "pick")) {
                return r;
            }
            for (var i:int = 0; i < turns.length; i++) {
                if (turns[i].window == window && turns[i].how == "") {
                    r.push(i);
                }
            }
            return r;
        }

        // the players of these turns, "a" or "a & b"
        private function playersOf(open:Array):String {
            var s:String = "";
            for each (var i:int in open) {
                s += (s != "" ? " & " : "") + turns[i].player;
            }
            return s;
        }

        private function refresh():void {
            var open:Array = openTurns();
            var first:Object = open.length > 0 ? turns[open[0]] : null;
            var my:Object = myTurn >= 0 ? turns[myTurn] : null;
            // the head row
            var line:String = DONE_LINE;
            var lineColor:uint = WHITE;
            var size:int = 18;
            if (phase == "role") {
                line = ROLE_LINE;
            } else if (phase == "trade") {
                line = TRADE_LINE;
            } else if (yourTurn && my != null) {
                line = my.kind == "ban" ? "YOUR TURN: BAN A SHAPER" : "YOUR TURN: PICK YOUR SHAPER";
                lineColor = ACCENT;
                size = 22;
            } else if (first != null) {
                line = TEAM_NAMES[first.team] + (first.kind == "ban" ? " BANS: " : " PICKS: ") + playersOf(open);
            }
            turnTf.text = line;
            turnTf.textColor = lineColor;
            fit(turnTf, size, headSide);
            turnTf.y = PAD + 1 + (22 - size) / 2;
            band.visible = yourTurn;
            turnFrame.visible = yourTurn;
            for (var gt:int = 0; gt < 2; gt++) {
                glows[gt].visible = first != null && first.team == gt;
            }
            drawChips(open);
            timerTf.visible = timeBar.visible = phase != "done";
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
                    var now:Boolean = open.indexOf(i) >= 0;
                    var seat:int = teams[t].indexOf(tn.player);
                    if (tn.kind == "ban" && b < MAX_BANS) {
                        // the open ban's slot shows its player's hover (the own choice, or the teammate's) faint
                        var bs:Object = col.bans[b++];
                        var bh:String = now && tn.shaper == "" ? hoverOf(t, seat) : "";
                        var bid:String = bh != "" ? bh : tn.shaper;
                        showIn(bs, portraitOf(bid), bh == "" && tn.shaper != "", bh != "", bid != "" ? nameOf(bid) : "");
                        bs.frame.visible = now;
                        bs.dash.visible = !now && tn.how != "" && tn.shaper == "";
                    } else if (tn.kind == "pick" && k < MAX_SEATS) {
                        // a seat's hover shows on its pick row unless its open ban shows it
                        var ph:String = tn.how == "" && !banOpenFor(t, tn.player, open) ? hoverOf(t, seat) : "";
                        drawPickRow(col.picks[k++], tn, now, ph, t, seat);
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
                    er.seat = -1;
                    drawTag(er.tag, null, false);
                    setRowButton(er, "");
                }
            }
            // the cards, or the role chooser over them
            if (cells.length == 0 && pool.length > 0) {
                fillGrid();
            }
            for each (var c:Object in cells) {
                drawCell(c);
            }
            var choosing:Boolean = chooserShown();
            chooser.visible = choosing;
            grid.visible = !choosing;
            if (choosing) {
                drawChooser();
            }
            // the bottom row
            actBtn.visible = yourTurn && my != null;
            if (actBtn.visible) {
                setLabel(actBtn, my.kind == "ban" ? BTN_BAN : BTN_PICK);
                setAlpha(actBtn, sel != "" && sent == null && !noRole() ? 1 : 0.5);
            }
            readyBtn.visible = phase == "trade" && you != null && own != null && !own.ready[you.seat];
            acceptBtn.visible = declineBtn.visible = incoming() != null;
            for each (var fb:Sprite in [readyBtn, acceptBtn, declineBtn]) {
                setAlpha(fb, req == null ? 1 : 0.5);
            }
            placeButtons();
            showHint();
        }

        // the hover to draw for a seat of a team: the own choice for the own seat, a teammate's from the team's part;
        // none for the enemy and for a spectator (the server never sends them)
        private function hoverOf(team:int, seat:int):String {
            if (you == null || team != you.team || seat < 0) {
                return "";
            }
            if (seat == you.seat) {
                return sel;
            }
            return own != null && seat < MAX_SEATS ? own.hovers[seat] : "";
        }

        // the player has an open ban now (its hover is the ban's)
        private function banOpenFor(team:int, player:String, open:Array):Boolean {
            for each (var i:int in open) {
                if (turns[i].kind == "ban" && turns[i].team == team && turns[i].player == player) {
                    return true;
                }
            }
            return false;
        }

        // the index of the pick turn of a team's seat (its player's name in it), -1 when none
        private function pickTurnOf(team:int, seat:int):int {
            var name:String = seat >= 0 && seat < teams[team].length ? teams[team][seat] : null;
            for (var i:int = 0; i < turns.length && name != null; i++) {
                if (turns[i].kind == "pick" && turns[i].team == team && turns[i].player == name) {
                    return i;
                }
            }
            return -1;
        }

        // a drafter whose role the server doesn't have (only with the team's part: a server of the first version has
        // no roles)
        private function noRole():Boolean {
            return you != null && own != null && own.roles[you.seat] == "";
        }

        // the role to show as the player's: the one asked for while that call is on its way
        private function myRole():String {
            if (req != null && req.kind == "role") {
                return req.val;
            }
            return you != null && own != null ? own.roles[you.seat] : "";
        }

        private function chooserShown():Boolean {
            if (you == null || own == null || phase == "done" || phase == "") {
                return false;
            }
            return phase == "role" || chooserOpen || (myRole() == "" && (phase == "ban" || phase == "pick"));
        }

        // the request to the player (a swap before the trade phase, a trade in it): {kind, from}, or null
        private function incoming():Object {
            var p:Object = pendingOf(true);
            return p != null ? {kind: phase == "trade" ? "trade" : "swap", from: p.from} : null;
        }

        // the player's own pending request, or the one to it (asked true), of the phase's kind
        private function pendingOf(asked:Boolean):Object {
            if (you == null || own == null) {
                return null;
            }
            var list:Array = phase == "trade" ? own.trades : (phase == "role" || phase == "ban" || phase == "pick" ?
                own.swaps : []);
            for each (var p:Object in list) {
                if ((asked ? p.to : p.from) == you.seat && (asked ? p.from : p.to) < teams[you.team].length) {
                    return p;
                }
            }
            return null;
        }

        // a seat has a request of the phase's kind pending (asking or asked)
        private function busy(seat:int):Boolean {
            var list:Array = phase == "trade" ? own.trades : own.swaps;
            for each (var p:Object in list) {
                if (p.from == seat || p.to == seat) {
                    return true;
                }
            }
            return false;
        }

        // What a teammate's row offers the player: SWAP before the picks (both still to pick, neither's window on, in
        // the role, ban and pick phases), TRADE in the trade phase (both holding a shaper, neither ready); neither while
        // either has a request pending or after the teammate declined (or let expire) the player's request; else the
        // marks ASKED (the player's request to it), ASKS YOU (its request to the player) and READY (trade phase).
        private function rowMode(team:int, seat:int):String {
            if (you == null || own == null || team != you.team || seat < 0 || seat >= teams[team].length) {
                return "";
            }
            var trade:Boolean = phase == "trade";
            if (trade && own.ready[seat]) {
                return RB_READY;
            }
            if (seat == you.seat || (!trade && phase != "role" && phase != "ban" && phase != "pick")) {
                return "";
            }
            var list:Array = trade ? own.trades : own.swaps;
            if (hasPair(list, you.seat, seat)) {
                return RB_ASKED;
            }
            if (hasPair(list, seat, you.seat)) {
                return RB_ASKS;
            }
            var name:String = teams[team][seat];
            if (busy(you.seat) || busy(seat) || noRepeat[(trade ? "trade:" : "swap:") + name] === true) {
                return "";
            }
            var a:int = pickTurnOf(team, you.seat);
            var b:int = pickTurnOf(team, seat);
            if (a < 0 || b < 0) {
                return "";
            }
            if (trade) {
                return turns[a].shaper != "" && turns[b].shaper != "" && !own.ready[you.seat] ? RB_TRADE : "";
            }
            var ok:Boolean = turns[a].how == "" && turns[b].how == "" && turns[a].window != window &&
                turns[b].window != window;
            return ok ? RB_SWAP : "";
        }

        private function drawPickRow(row:Object, tn:Object, now:Boolean, hov:String, team:int, seat:int):void {
            row.slot.sp.alpha = 1;
            row.seat = seat;
            var ownTeam:Boolean = you != null && team == you.team;
            var mine:Boolean = ownTeam && seat == you.seat;
            var who:String = tn.player + (mine ? " (you)" : "");
            if (row.who.text != who) {
                row.who.text = who;
                fit(row.who, 13, row.who.width); // a long name in a narrow column ("draft2ap1366ca (yo" was cut)
            }
            row.who.textColor = mine ? LIGHT : DIM;
            // the role (the own team's only; the own one from the call on its way)
            var role:String = null;
            if (ownTeam && own != null && seat >= 0) {
                role = mine ? myRole() : own.roles[seat];
            }
            drawTag(row.tag, role, mine && phase != "done");
            if (tn.shaper != "") {
                showIn(row.slot, portraitOf(tn.shaper), false, false);
                row.what.text = nameOf(tn.shaper);
                row.what.textColor = WHITE;
            } else if (hov != "") {
                // the own choice in orange, a teammate's hover faint: "Fenmore?"
                showIn(row.slot, portraitOf(hov), false, true);
                row.what.text = nameOf(hov) + "?";
                row.what.textColor = mine ? ACCENT : LIGHT;
            } else {
                showIn(row.slot, "", false, false);
                row.what.text = now ? PICKING : "-";
                row.what.textColor = now ? ACCENT : DARK;
            }
            setRowButton(row, rowMode(team, seat));
            fit(row.what, 15, row.what.width);
            row.slot.frame.visible = now;
            row.slot.dash.visible = false;
        }

        // a row's role tag: hidden (null: not the own team), the role's icon and short name, or "?" (none yet); the own
        // one is a button (it opens the role chooser)
        private function drawTag(tg:Object, role:String, mine:Boolean):void {
            tg.sp.visible = role != null;
            if (role == null) {
                return;
            }
            var r:int = ROLES.indexOf(role);
            if (tg.role != role) {
                tg.role = role;
                unload(tg.ld);
                if (tg.ld != null && tg.ld.parent != null) {
                    tg.ld.parent.removeChild(tg.ld);
                }
                tg.ld = null;
                if (r >= 0) {
                    tg.ld = load(ROLE_ICONS[r], TAG_S - 2, TAG_S - 2);
                    tg.ld.x = tg.ld.y = 1;
                    tg.sp.addChild(tg.ld);
                }
                tg.label.text = r >= 0 ? ROLE_SHORT[r] : "?";
                tg.label.textColor = r >= 0 ? LIGHT : ACCENT;
            }
            tg.mine = mine;
            tg.sp.buttonMode = mine;
            tg.sp.mouseEnabled = mine;
        }

        // a row's button or mark (mode "": none); the shaper line gives up the button's room while one shows
        private function setRowButton(row:Object, mode:String):void {
            var sp:Sprite = row.rb;
            sp.visible = mode != "";
            row.what.width = mode != "" ? row.textW - RB_W - 4 : row.textW;
            row.what.x = !row.left && mode != "" ? row.textX + RB_W + 4 : row.textX;
            if (row.rbMode == mode) {
                return;
            }
            row.rbMode = mode;
            var button:Boolean = mode == RB_SWAP || mode == RB_TRADE;
            var t:TextField = sp.getChildAt(0) as TextField;
            t.text = mode;
            t.textColor = button ? ACCENT : (mode == RB_READY ? LIGHT : (mode == RB_ASKS ? ACCENT : DIM));
            fit(t, 11, RB_W);
            var g:Graphics = sp.graphics;
            g.clear();
            if (button) {
                g.lineStyle(1, ACCENT, 1);
                g.beginFill(0x000000, 0.6);
                g.drawRect(0, 0, RB_W, RB_H);
                g.endFill();
            }
            sp.buttonMode = button;
            sp.mouseEnabled = button;
        }

        // the chooser's tiles: the player's role framed orange, each role's teammates under its name
        private function drawChooser():void {
            var mineRole:String = myRole();
            for (var i:int = 0; i < tiles.length; i++) {
                var tl:Object = tiles[i];
                var on:Boolean = mineRole == ROLES[i];
                tl.frame.visible = on;
                tl.nm.textColor = on ? ACCENT : WHITE;
                var m:String = "";
                for (var s:int = 0; s < teams[you.team].length && s < MAX_SEATS; s++) {
                    if (s != you.seat && own.roles[s] == ROLES[i]) {
                        m += (m != "" ? ", " : "") + teams[you.team][s];
                    }
                }
                if (tl.matesText != m) {
                    tl.matesText = m;
                    tl.mates.text = m;
                    fit(tl.mates, 11, tl.w - 4);
                }
                setAlpha(tl.sp, req != null ? 0.7 : 1);
            }
        }

        // the turns as chips: a circle per ban, a square per pick, in the team's colour; done faint, the open ones
        // filled with an orange frame, the rest outlined; right of the timer, to the head row's right end
        private function drawChips(open:Array):void {
            while (chips.numChildren > 0) {
                chips.removeChildAt(0);
            }
            var n:int = turns.length;
            var bans:int = 0;
            for each (var t0:Object in turns) {
                bans += t0.kind == "ban" ? 1 : 0;
            }
            var gap:int = bans > 0 && bans < n ? 12 : 0;
            var x0:Number = boardW / 2 + TIMER_W / 2 + 8;
            var x1:Number = boardW - PAD;
            var pitch:Number = Math.min(CHIP_W, (x1 - x0 - gap) / Math.max(1, n));
            var s:Number = Math.max(8, Math.min(CHIP_S, Math.floor(pitch - 4)));
            chips.x = Math.round(x1 - n * pitch - gap + (pitch - s));
            var over:Boolean = phase == "done" || phase == "trade";
            for (var i:int = 0; i < n; i++) {
                var tn:Object = turns[i];
                var color:uint = TEAM_COLORS[tn.team];
                var cx:Number = i * pitch + (tn.kind == "pick" ? gap : 0);
                var cur:Boolean = open.indexOf(i) >= 0;
                var done:Boolean = over || tn.how != "";
                chips.addChild(chip(tn.kind == "ban", cx, s, cur ? 2 : 1, cur ? ACCENT : color, color,
                    cur ? 1 : (done ? 0.35 : 0)));
            }
        }

        // One turn's marker, a Shape of its own whose path begins at its origin: a circle (a ban) or a square (a pick),
        // s units. The game's Flash player joins the paths of one Graphics object, moveTo or not: drawn in a single
        // Shape the circles were tied together by a line through their start points, in the last marker's line style
        // (the "debug" lines across the strip in the first live runs). So no two markers share a Graphics, and none
        // moves its pen.
        private static function chip(ban:Boolean, cx:Number, s:Number, thick:Number, line:uint, fill:uint,
                                     alpha:Number):Shape {
            var sh:Shape = new Shape();
            var g:Graphics = sh.graphics;
            g.lineStyle(thick, line, 1);
            g.beginFill(fill, alpha);
            if (ban) {
                // the circle's centre (cx + s / 2 + 1, s / 2 + 1), radius s / 2 - 1, from its rightmost point (the
                // Shape's origin): eight curves, each control point at r / cos(22.5 degrees), halfway between its ends
                var r:Number = s / 2 - 1;
                sh.x = cx + s / 2 + 1 + r;
                sh.y = s / 2 + 1;
                var rc:Number = r / Math.cos(Math.PI / 8);
                for (var k:int = 1; k <= 8; k++) {
                    var a:Number = k * Math.PI / 4;
                    var m:Number = a - Math.PI / 8;
                    g.curveTo(-r + rc * Math.cos(m), rc * Math.sin(m), -r + r * Math.cos(a), r * Math.sin(a));
                }
            } else {
                sh.x = cx + 1;
                sh.y = 2;
                g.lineTo(s, 0);
                g.lineTo(s, s - 2);
                g.lineTo(0, s - 2);
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
                    }
                    var late:Boolean = false;
                    if (sent != null && getTimer() - sent.at > SENT_MS) {
                        sent = null; // no answer (a call the server dropped unanswered): the player may choose again
                        late = true;
                    }
                    if (hoverReq != null && getTimer() - hoverReq.at > SENT_MS) {
                        hoverReq = null; // the choice falls back to the server's hover at the next event
                        late = true;
                    }
                    if (req != null && getTimer() - req.at > SENT_MS) {
                        req = null;
                        late = true;
                    }
                    if (late) {
                        dirty = true;
                        if (redraw != null) {
                            redraw();
                        }
                    }
                    showHint(); // also the own turn's last seconds ("Time's up: ...")
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

        // the time left in the step, counted down on this client's clock from the event's "left", and under it a bar of
        // the step's full time (turnMs) that shrinks with it
        private function showTime():void {
            if (timerTf == null || !timerTf.visible) {
                return;
            }
            var ms:int = Math.max(0, deadline - getTimer());
            var s:int = Math.ceil(ms / 1000);
            var t:String = int(s / 60) + ":" + (s % 60 < 10 ? "0" : "") + (s % 60);
            var color:uint = s <= 5 ? RED : ACCENT;
            if (timerTf.text != t) {
                timerTf.text = t;
                timerTf.textColor = color;
            }
            var bw:int = turnMs > 0 ? Math.round((TIMER_W - 20) * Math.min(1, ms / turnMs)) : 0;
            var look:String = bw + "|" + color;
            if (look != barLook) {
                barLook = look;
                var g:Graphics = timeBar.graphics;
                g.clear();
                g.beginFill(DARK, 0.6);
                g.drawRect(0, 0, TIMER_W - 20, 3);
                g.endFill();
                if (bw > 0) {
                    g.beginFill(color, 1);
                    g.drawRect(Math.round((TIMER_W - 20 - bw) / 2), 0, bw, 3);
                    g.endFill();
                }
            }
        }

        private function showHint():void {
            if (hintTf == null) {
                return;
            }
            var open:Array = openTurns();
            var first:Object = open.length > 0 ? turns[open[0]] : null;
            var my:Object = myTurn >= 0 ? turns[myTurn] : null;
            var mates:Array = you != null ? teams[you.team] : [];
            var asks:Object = incoming();
            var mine:Object = pendingOf(false);
            var s:String;
            var color:uint = DIM;
            if (flashText != "") {
                s = flashText;
                color = RED;
            } else if (asks != null) {
                // a teammate's request: ACCEPT / DECLINE beside it
                color = ACCENT;
                var who:String = mates[asks.from];
                if (asks.kind == "swap") {
                    s = who + " asks to swap pick turns with you.";
                } else {
                    var a:int = pickTurnOf(you.team, you.seat);
                    var b:int = pickTurnOf(you.team, asks.from);
                    s = a >= 0 && b >= 0 ? who + " offers a trade: you'd play " + nameOf(turns[b].shaper) + ", " + who +
                        " " + nameOf(turns[a].shaper) + "." : who + " offers a trade.";
                }
            } else if (phase == "role") {
                s = you == null || own == null ? ROLE_HINT : (myRole() == "" ? ROLE_CHOOSE : "You play " +
                    ROLE_NAMES[ROLES.indexOf(myRole())] + ". " + ROLE_CHANGE);
                if (mine != null) {
                    s = "Waiting for " + mates[mine.to] + "'s answer to your swap request.";
                }
            } else if (phase == "trade") {
                s = you == null || own == null ? TRADE_HINT : (own.ready[you.seat] ? TRADE_READY : TRADE_OWN);
                if (mine != null) {
                    s = "Waiting for " + mates[mine.to] + "'s answer to your trade offer.";
                }
            } else if (first == null) {
                s = DONE_HINT;
            } else if (yourTurn && my != null) {
                color = LIGHT;
                var left:int = deadline - getTimer();
                var fallback:String = own != null && you != null ? own.hovers[you.seat] : "";
                if (noRole()) {
                    s = NO_ROLE;
                } else if (sent != null) {
                    s = (my.kind == "ban" ? "Banning " : "Picking ") + nameOf(sent.id) + "...";
                    color = DIM;
                } else if (left <= TIMES_UP_MS && own != null) {
                    // what the server does when the time runs out: the hover is banned or picked (DRAFT1b)
                    color = left <= 5000 ? RED : ACCENT;
                    s = fallback != "" ? "Time's up: " + nameOf(fallback) + (my.kind == "ban" ? " will be banned." :
                        " will be picked.") : (my.kind == "ban" ? "Time's up: no ban." :
                        "Time's up: a random shaper will be picked.");
                } else if (sel != "") {
                    s = my.kind == "ban" ? "Ban " + nameOf(sel) + "? Click BAN." : "Pick " + nameOf(sel) + "? Click LOCK IN.";
                } else {
                    s = my.kind == "ban" ? "Your turn: choose a shaper to ban, then BAN." :
                        "Your turn: choose your shaper, then LOCK IN.";
                }
            } else if (you == null) {
                s = WATCHING;
            } else {
                s = "Waiting for " + playersOf(open) + (first.kind == "ban" ? " to ban a shaper." :
                    (open.length > 1 ? " to pick their shapers." : " to pick a shaper."));
                if (mine != null) {
                    s += " " + mates[mine.to] + " has your swap request.";
                } else if (sel != "") {
                    s += " Your hover: " + nameOf(sel) + ".";
                }
            }
            if (hintText != s) {
                hintText = s;
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

        // The bottom row's buttons from its right end, inside the board (which ends before the chat window and above
        // START), so they show at every window size: BAN / LOCK IN (an open turn) or READY (the trade phase), then
        // DECLINE and ACCEPT (a teammate's request); the hint takes the room left of them.
        private function placeButtons():void {
            if (actBtn == null) {
                return;
            }
            var x:Number = boardW - PAD;
            for each (var b:Sprite in [actBtn, readyBtn, declineBtn, acceptBtn]) {
                if (!b.visible) {
                    continue;
                }
                var r:Rectangle = sizeOf(b);
                x -= r.width;
                b.x = x;
                b.y = boardH - PAD - r.height + 2;
                x -= BTN_GAP;
            }
            var w:Number = Math.max(120, Math.min(boardW - 2 * PAD - 160, x - PAD - 4));
            if (hintTf.width != w) {
                hintTf.width = w;
                hintTf.text = hintText;
                fit(hintTf, 14, w);
            }
        }

        // ---- the cards ----

        // The pool's cards: the tall card art (aspect 1 : 2, never wider than the texture's own 64) at the column count
        // whose card comes out largest, when that is at least TALL_MIN_W wide (12 x 3 at 48 x 96 for 35 shapers at
        // 1440 x 810); else the square portraits at the column count whose square comes out largest (the window is too
        // short for tall cards: 1280 x 720, 1024 x 768).
        private function fillGrid():void {
            clearGrid();
            var W:Number = boardW - 2 * (PAD + teamW + COL_GAP);
            var H:Number = boardH - PAD - HEAD_H - FOOT_H - 4;
            var n:int = pool.length;
            var gr:Object = bestGrid(n, W, H, 2, CARD_MAX_W);
            square = gr.s < TALL_MIN_W;
            if (square) {
                gr = bestGrid(n, W, H, 1, SQUARE_MAX);
            }
            var nc:int = gr.c;
            var aw:int = Math.max(8, Math.floor(gr.s));
            var ah:int = square ? aw : aw * 2;
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
                var path:String = square ? e.portrait : e.card;
                var ld:Loader = path != "" ? load(path, aw, ah) : null;
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

        // the column count c whose card (aspect 1 : aspect, at most max wide) of n comes out largest in W x H: {c, s}
        private static function bestGrid(n:int, W:Number, H:Number, aspect:Number, max:Number):Object {
            var best:Number = 0;
            var nc:int = 1;
            for (var c:int = 1; c <= 20; c++) {
                var r:int = Math.max(1, Math.ceil(n / c));
                var s:Number = Math.min(W / c - CELL_GAP, (H / r - NAME_H - CELL_GAP) / aspect, max);
                if (s > best + 0.01) {
                    best = s;
                    nc = c;
                }
            }
            return {c: nc, s: best};
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
        // (the player's choice and hover: an orange frame), under the pointer while it can be chosen (a faint frame)
        private function drawCell(c:Object):void {
            var st:int = stateOf(c.id, turns);
            var chosen:Boolean = sel == c.id && (yourTurn || canHover());
            var canPick:Boolean = st == 0 && ((yourTurn && sent == null) || canHover());
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

        // the player may hover a card now (the server's rule): the ban and pick phases, with a role, before its own pick
        private function canHover():Boolean {
            if (you == null || own == null || (phase != "ban" && phase != "pick") || noRole()) {
                return false;
            }
            var p:int = pickTurnOf(you.team, you.seat);
            return p >= 0 && turns[p].how == "";
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

        // A card's click: the player's choice and hover (draft.hover: its team sees it, and its turn's timeout takes
        // it), on its own turn or before it while it may hover; anyone else hears why nothing happens.
        private function onCell(e:MouseEvent):void {
            var c:Object = cellOf(e);
            var open:Array = openTurns();
            if (c == null || open.length == 0) {
                return;
            }
            var first:Object = turns[open[0]];
            var st:int = stateOf(c.id, turns);
            if (st == 1) {
                flash(c.name + " is banned.");
            } else if (st >= 2) {
                flash(c.name + " is already picked.");
            } else if (you == null) {
                flash(WATCHING);
            } else if (noRole()) {
                chooserOpen = true;
                flash(NO_ROLE_CLICK);
            } else if (yourTurn && sent != null) {
                return;
            } else if (yourTurn || canHover()) {
                choose(c.id);
            } else {
                flash("Not your turn: " + playersOf(open) + (first.kind == "ban" ? " bans now." :
                    (open.length > 1 ? " pick now." : " picks now.")));
            }
            redrawSoon();
        }

        // the player's choice, sent as its hover (not to a server without the team's part, the first version)
        private function choose(id:String):void {
            flashText = "";
            if (sel == id) {
                return;
            }
            sel = id;
            if (own != null && you != null && channel.call("draft.hover", {shaper: id})) {
                hoverReq = {id: id, key: stateKey, at: getTimer()};
            }
        }

        private function redrawSoon():void {
            dirty = true;
            if (redraw != null) {
                redraw();
            }
        }

        // BAN or LOCK IN: the call for the player's own open turn; the server's next draft.state says whether it was
        // taken
        private function onAct(e:MouseEvent):void {
            var my:Object = myTurn >= 0 ? turns[myTurn] : null;
            if (my == null || !yourTurn || sent != null) {
                return;
            }
            if (noRole()) {
                chooserOpen = true;
                flash(NO_ROLE_CLICK);
                redrawSoon();
                return;
            }
            if (sel == "" || byId[sel] == null) {
                flash(my.kind == "ban" ? "Choose a shaper to ban first." : "Choose your shaper first.");
                return;
            }
            var now:int = getTimer();
            if (now - lastCall < CALL_GAP_MS) {
                return;
            }
            if (!channel.call(my.kind == "ban" ? "draft.ban" : "draft.pick", {shaper: sel})) {
                flash("Not connected to the server.");
                return;
            }
            lastCall = now;
            sent = {idx: myTurn, id: sel, key: stateKey, at: now};
            redrawSoon();
        }

        // a request call (role, swap, trade, an answer, ready): one at a time; the next event that shows it done ends
        // the wait, the same board again is the server's refusal
        private function ask(kind:String, call:String, args:Object, val:*):void {
            if (req != null || you == null || own == null) {
                return;
            }
            if (!channel.call(call, args)) {
                flash("Not connected to the server.");
                return;
            }
            req = {kind: kind, val: val, key: stateKey, at: getTimer()};
            redrawSoon();
        }

        // the own row's role tag: the role chooser opens (or closes again)
        private function onTag(e:MouseEvent):void {
            if (you == null || own == null || phase == "done" || phase == "") {
                return;
            }
            chooserOpen = !chooserOpen;
            redrawSoon();
        }

        // a role of the chooser: declared (draft.role), the chooser closes outside the role phase
        private function onTile(e:MouseEvent):void {
            var sp:Sprite = e.currentTarget as Sprite;
            var i:int = sp != null ? int(sp.name) : -1;
            if (i < 0 || i >= ROLES.length || you == null || own == null || phase == "done" || req != null) {
                return;
            }
            chooserOpen = false;
            if (ROLES[i] == myRole()) {
                redrawSoon();
                return;
            }
            ask("role", "draft.role", {role: ROLES[i]}, ROLES[i]);
        }

        // SWAP or TRADE on a teammate's row: the request to that seat
        private function onRowButton(e:MouseEvent):void {
            if (you == null || cols == null) {
                return;
            }
            for each (var row:Object in cols[you.team].picks) {
                if (row.rb == e.currentTarget && (row.rbMode == RB_SWAP || row.rbMode == RB_TRADE)) {
                    var kind:String = row.rbMode == RB_SWAP ? "swap" : "trade";
                    ask(kind, "draft." + kind, {seat: row.seat}, row.seat);
                    return;
                }
            }
        }

        // ACCEPT or DECLINE: the answer to the teammate's request
        private function onAnswer(e:MouseEvent):void {
            var p:Object = incoming();
            if (p == null) {
                return;
            }
            var kind:String = p.kind + "Answer";
            ask(kind, "draft." + kind, {accept: e.currentTarget == acceptBtn}, p.from);
        }

        // READY in the trade phase: final (the server takes no trade of the player's after it)
        private function onReady(e:MouseEvent):void {
            if (phase == "trade" && you != null && own != null && !own.ready[you.seat]) {
                ask("ready", "draft.ready", {}, null);
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
