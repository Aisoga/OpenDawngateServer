// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The custom game lobby's match rules: what everyone sees and what the game's admin changes. LobbyReady owns it, draws
// it on its overlay and hands it the server's events:
//     lobby.rulesCatalog {categories: [{id, name, default, options: [{id, name, bots}]}], presets: [{id, name, options}]}
//     lobby.state's rules {options, preset, text, bots, findings: [{category, message}], canEdit, host}
//     lobby.setRules {preset} | {rules: {<category>: <option>, ...}} | {bots}             -> the server
//
// Every player sees a summary above the Spirit team: MATCH RULES, the rules in words (the preset's name and the server's
// text), the bots, and in red, in a short form, that the game can't start like this. The admin also gets CHANGE,
// which opens the editor over the teams: the presets first, then a column per category with its options, the bots
// check box, the findings and a hint line. A click is a call; the server's next lobby.state says how things stand (a
// change resets every ready mark there). The editor greys out nothing on its own authority: an option the catalog says
// the bots can't play is dimmed while the bots are on (the hint line says why), and a choice the server's checks refuse
// shows in red with the server's reason, while LobbyReady holds START (the server refuses it too).
//
// Everything the server sends is untrusted: strings go into .text only (never htmlText), clipped; lists are cut short;
// ids sent back come from the catalog only.
package {
    import flash.display.DisplayObject;
    import flash.display.Graphics;
    import flash.display.Sprite;
    import flash.events.MouseEvent;
    import flash.geom.Rectangle;
    import flash.text.TextField;
    import flash.text.TextFormat;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    import opendawngate.ServerChannel;

    public class RulesPanel {
        private static const CHECK_CLASS:String = "MSCCheckBox001";
        private static const TITLE:String = "MATCH RULES";
        private static const CHANGE:String = "CHANGE";
        private static const CLOSE:String = "DONE";
        private static const PRESET_HEADER:String = "PRESET";
        private static const CUSTOM:String = "Custom rules";
        private static const BOTS_LABEL:String = "Fill open seats with bots"; // the CREATE GAME dialog's words
        private static const BOTS_ON:String = "Bots fill the open seats";
        private static const BOTS_OFF:String = "No bots";
        private static const NO_BOTS_HINT:String = "Dimmed options can't be played with bots yet. ";
        private static const RESET_HINT:String = "A change resets every player's ready mark.";
        private static const INCOMPATIBLE:String = "Incompatible match rules!"; // the user's words
        private static const OLD_HOST:String = "This game's match host plays the standard game only.";
        private static const CANT_START:String = "Can't start: ";

        private static const WHITE:uint = 0xFFFFFF;
        private static const LIGHT:uint = 0xD8D8D8;
        private static const DIM:uint = 0xA9A9A9;      // EA's wins grey
        private static const OFF:uint = 0x6E6E6E;      // an option the bots can't play while they are on
        private static const RED:uint = 0xE0625A;      // a finding (the Spirit team's red, lighter for small text)
        private static const ACCENT:uint = 0xE8A33B;   // the button's label
        private static const FRAME:uint = 0x808080;    // the spectator cells' border

        private static const LINE_H:int = 19;          // the summary's lines
        private static const SUMMARY_LINES:int = 4;    // title, rules, bots, findings
        private static const TEAM_TITLE_H:int = 34;    // the team titles' row above the first cells (the layout's)
        private static const EA_BUTTON:String = "Button001"; // EA's small dark button (the Leaderboard's Refresh)
        private static const CHANGE_GAP:int = 10;      // between CHANGE and the title's text
        private static const BTN_W:int = 76;           // the fallback button's size
        private static const BTN_H:int = 19;
        private static const PAD:int = 12;
        private static const COLUMNS:int = 5;          // the presets and four categories
        private static const HEAD_H:int = 26;          // the editor's title row
        private static const COL_HEAD_H:int = 22;
        private static const ROW_H:int = 28;
        private static const MAX_CATEGORIES:int = 4;   // what the editor has room for (the catalog's four)
        private static const MAX_OPTIONS:int = 10;     // the shaper pool has 8 (all, melee, ranged, five archetypes)
        private static const MAX_PRESETS:int = 6;
        private static const MAX_FINDINGS:int = 4;
        private static const CALL_GAP_MS:int = 300;    // the server takes one change per 250 ms

        private var channel:ServerChannel;
        private var say:Function;

        // the server's catalog (null: none yet; an older server)
        private var cats:Array = null;    // {id, name, def, options: [{id, name, bots}]}
        private var presets:Array = [];   // {id, name, options: {category: option}}

        // the lobby's rules (has false: the game has none, the panel stays hidden)
        private var has:Boolean = false;
        private var options:Object = {};  // category id -> option id
        private var rawOptions:Object = null; // the state's options as they came (the catalog may come after them)
        private var preset:String = "";
        private var rulesText:String = "";
        private var bots:Boolean = false;
        private var findings:Array = [];  // {category, message}
        private var canEdit:Boolean = false;
        private var hostTakes:Boolean = true;

        private var layer:Sprite = null;  // on LobbyReady's overlay
        private var summary:Sprite = null;
        private var titleTf:TextField = null;
        private var textTf:TextField = null;
        private var botsTf:TextField = null;
        private var findTf:TextField = null;
        private var changeBtn:Sprite = null;
        private var doneBtn:Sprite = null;  // the editor's DONE (EA's button), placed at each draw
        private var doneRight:Number = 0;
        private var editor:Sprite = null;
        private var editing:Boolean = false;
        private var dirty:Boolean = true;
        private var hintTf:TextField = null;
        private var dimmed:Boolean = false; // the editor dims an option the bots can't play
        private var lastCall:int = -100000;
        private var sumW:Number = 0;      // the summary's width: the Spirit team's

        public function RulesPanel(channel:ServerChannel, say:Function) {
            this.channel = channel;
            this.say = say;
        }

        // ---- the server's events ----

        public function setCatalog(data:Object):void {
            var cs:Array = [];
            var ps:Array = [];
            try {
                for each (var c:Object in data.categories) {
                    if (cs.length >= MAX_CATEGORIES) {
                        break;
                    }
                    var id:String = str(c.id, 32);
                    if (id == "") {
                        continue;
                    }
                    var opts:Array = [];
                    for each (var o:Object in c.options) {
                        if (opts.length >= MAX_OPTIONS) {
                            break;
                        }
                        var oid:String = str(o.id, 32);
                        if (oid != "") {
                            opts.push({id: oid, name: str(o.name, 40), bots: o.bots === true});
                        }
                    }
                    if (opts.length > 0) {
                        cs.push({id: id, name: str(c.name, 30), def: str(c["default"], 32), options: opts});
                    }
                }
                for each (var p:Object in data.presets) {
                    if (ps.length >= MAX_PRESETS) {
                        break;
                    }
                    var pid:String = str(p.id, 32);
                    if (pid == "") {
                        continue;
                    }
                    var po:Object = {};
                    for each (var k:Object in cs) {
                        po[k.id] = p.options != null && p.options[k.id] != null ? str(p.options[k.id], 32) : k.def;
                    }
                    ps.push({id: pid, name: str(p.name, 30), options: po});
                }
            } catch (err:Error) {
                say("the rules' catalog: " + err.message);
            }
            cats = cs.length > 0 ? cs : null;
            presets = ps;
            readOptions();
            dirty = true;
        }

        // the options of the catalog's categories, from the state's (a missing one is its category's default)
        private function readOptions():void {
            var o:Object = {};
            if (cats != null) {
                for each (var c:Object in cats) {
                    var v:String = rawOptions != null ? str(rawOptions[c.id], 32) : "";
                    o[c.id] = v != "" ? v : c.def;
                }
            }
            options = o;
        }

        // lobby.state's rules part; null or missing: a game without rules
        public function setState(r:Object):void {
            if (r == null) {
                has = false;
                editing = false;
                dirty = true;
                return;
            }
            has = true;
            rawOptions = r.options;
            readOptions();
            preset = str(r.preset, 32);
            rulesText = str(r.text, 120);
            bots = r.bots === true;
            var fs:Array = [];
            for each (var f:Object in r.findings) {
                if (f != null && fs.length < MAX_FINDINGS) {
                    fs.push({category: str(f.category, 32), message: str(f.message, 200)});
                }
            }
            findings = fs;
            canEdit = r.canEdit === true;
            hostTakes = r.host !== false;
            if (!canEdit) {
                editing = false;
            }
            dirty = true;
        }

        // the admin's START was refused for the rules: the findings it names (the state brings them too)
        public function startRefused(fs:Object):void {
            if (fs == null || !has) {
                return;
            }
            var out:Array = [];
            for each (var f:Object in fs) {
                if (f != null && out.length < MAX_FINDINGS) {
                    out.push({category: str(f.category, 32), message: str(f.message, 200)});
                }
            }
            if (out.length > 0) {
                findings = out;
                dirty = true;
            }
        }

        // the server's findings refuse a start: LobbyReady holds START
        public function get blocked():Boolean {
            return has && findings.length > 0;
        }

        // the lobby closed: everything of ours goes
        public function detach():void {
            if (layer != null && layer.parent != null) {
                layer.parent.removeChild(layer);
            }
            clearEditor();
            if (changeBtn != null) {
                changeBtn.removeEventListener(MouseEvent.CLICK, onChange);
            }
            layer = null;
            summary = null;
            rawOptions = null;
            sumW = 0;
            titleTf = textTf = botsTf = findTf = hintTf = null;
            changeBtn = null;
            has = false;
            cats = null;
            presets = [];
            findings = [];
            editing = false;
            dirty = true;
        }

        // ---- drawing: called by LobbyReady's draw, so after every view draw for a few frames ----

        public function draw(overlay:Sprite, view:Object, show:Boolean):void {
            if (!show || !has) {
                if (layer != null) {
                    layer.visible = false;
                }
                return;
            }
            var c0:DisplayObject = view.box0_0 as DisplayObject;
            var c1:DisplayObject = view.box1_0 as DisplayObject;
            if (c0 == null || c1 == null) {
                return;
            }
            var r0:Rectangle = c0.getBounds(overlay);
            var r1:Rectangle = c1.getBounds(overlay);
            if (r1.width < 50 || r1.height < 10) {
                return; // not laid out yet
            }
            if (layer == null) {
                build();
            }
            // above the tiles' marks, which come later, and right below the draft's board (above) when it is there
            if (layer.parent != overlay) {
                overlay.addChild(layer);
            }
            var want:int = overlay.numChildren - 1;
            if (above != null && above != layer && above.parent == overlay) {
                want--;
            }
            if (overlay.getChildIndex(layer) != want) {
                overlay.setChildIndex(layer, want);
            }
            layer.visible = true;
            if (sumW != r1.width) {
                sumW = r1.width;
                dirty = true;
            }
            if (dirty) {
                dirty = false;
                fillSummary();
                if (editing) {
                    fillEditor();
                } else {
                    clearEditor();
                }
            }
            placeChange();
            if (doneBtn != null) {
                var dr:Rectangle = sizeOf(doneBtn);
                doneBtn.x = doneRight - dr.width;
                doneBtn.y = PAD + Math.round((HEAD_H - dr.height) / 2) - 2;
            }
            for each (var k:Object in checks) {
                if (k.ck.selected != k.on) {
                    k.ck.selected = k.on;
                }
            }
            // the summary above the Spirit team, right-aligned with it, its last line just above the team titles
            var titleTop:Number = r1.y - TEAM_TITLE_H;
            summary.x = r1.right;
            summary.y = titleTop - 4 - SUMMARY_LINES * LINE_H;
            // the editor over the two teams, from the team titles down
            if (editor != null) {
                var w:Number = r1.right - r0.x;
                editor.x = r0.x + (w - editor.width) / 2;
                if (editor.x + editor.width > r1.right) {
                    editor.x = r1.right - editor.width;
                }
                editor.y = titleTop;
            }
        }

        private function build():void {
            layer = new Sprite();
            layer.mouseEnabled = false;
            summary = new Sprite();
            summary.mouseEnabled = false;
            layer.addChild(summary);
            titleTf = field("$Header", 15, DIM, "right");
            textTf = field("$Body", 15, WHITE, "right");
            botsTf = field("$Body", 14, DIM, "right");
            findTf = field("$Body", 14, RED, "right");
            changeBtn = button(CHANGE);
            changeBtn.addEventListener(MouseEvent.CLICK, onChange, false, 0, false);
            for each (var t:TextField in [titleTf, textTf, botsTf, findTf]) {
                summary.addChild(t);
            }
            summary.addChild(changeBtn);
        }

        // a button's size: its own once it has one (a CLIK button may draw a frame later), else the fallback's
        private static function sizeOf(b:DisplayObject):Rectangle {
            var w:Number = b.width;
            var h:Number = b.height;
            return new Rectangle(0, 0, w > 20 && w < 300 ? w : BTN_W, h > 10 && h < 60 ? h : BTN_H);
        }

        // the title at the summary's right edge as every line, CHANGE left of the title's text, centred on that line (at
        // each draw: the button's size can come a frame late)
        private function placeChange():void {
            setLine(titleTf, TITLE, 0, 0, sumW);
            var r:Rectangle = sizeOf(changeBtn);
            changeBtn.x = -(titleTf.textWidth + 4) - CHANGE_GAP - r.width;
            changeBtn.y = Math.round((LINE_H - r.height) / 2);
        }

        // the summary's lines, right-aligned at x 0 of the summary sprite (the Spirit team's right edge)
        private function fillSummary():void {
            var w:Number = sumW;
            changeBtn.visible = canEdit && cats != null;
            placeChange();
            var t:String = rulesText;
            var p:Object = presetById(preset);
            if (p != null && p.name != "" && p.name != t) {
                t = p.name + ": " + t;
            } else if (p == null && preset == "" && cats != null && t != "") {
                t = CUSTOM + ": " + t;
            }
            setLine(textTf, t, 0, LINE_H, w);
            fit(textTf, 15, w);
            setLine(botsTf, bots ? BOTS_ON : BOTS_OFF, 0, 2 * LINE_H, w);
            var f:String = "";
            if (findings.length > 0) {
                // a short form that fits, the same for everyone: the reasons are long, and the editor has them in full
                f = INCOMPATIBLE;
            } else if (!hostTakes) {
                f = OLD_HOST;
            }
            setLine(findTf, f, 0, 3 * LINE_H, w);
            findTf.textColor = findings.length > 0 ? RED : DIM;
            fit(findTf, 14, w);
        }

        private static function setLine(tf:TextField, s:String, right:Number, y:Number, w:Number):void {
            if (tf.text != s) {
                tf.text = s;
            }
            tf.width = w;
            tf.x = right - w;
            tf.y = y;
            tf.visible = s != "";
        }

        // a line too long for its width gets a smaller font (down to 11), then is cut by its field
        private static function fit(tf:TextField, size:int, w:Number):void {
            var fmt:TextFormat = tf.defaultTextFormat;
            for (var s:int = size; s >= 11; s--) {
                fmt.size = s;
                tf.setTextFormat(fmt);
                if (tf.textWidth + 6 <= w) {
                    return;
                }
            }
        }

        // ---- the editor ----

        private function onChange(e:MouseEvent):void {
            editing = !editing && canEdit && cats != null;
            dirty = true;
            askRedraw();
        }

        private function onClose(e:MouseEvent):void {
            editing = false;
            dirty = true;
            askRedraw();
        }

        // the owner's redraw: set by LobbyReady
        public var redraw:Function = null;
        // what stays above the panel on the overlay (the draft's board): set by LobbyReady
        public var above:DisplayObject = null;

        private function askRedraw():void {
            if (redraw != null) {
                redraw();
            }
        }

        private function clearEditor():void {
            if (editor == null) {
                return;
            }
            unhook(editor);
            if (editor.parent != null) {
                editor.parent.removeChild(editor);
            }
            editor = null;
            hintTf = null;
            doneBtn = null;
            rowActs = [];
            checks = [];
        }

        // takes our listeners off the editor's rows (our own sprites, but nothing of ours stays hooked)
        private function unhook(s:Sprite):void {
            for (var i:int = 0; i < s.numChildren; i++) {
                var row:Sprite = s.getChildAt(i) as Sprite;
                if (row != null && row.hasEventListener(MouseEvent.CLICK)) {
                    row.removeEventListener(MouseEvent.CLICK, onRowClick);
                    row.removeEventListener(MouseEvent.ROLL_OVER, onRowOver);
                    row.removeEventListener(MouseEvent.ROLL_OUT, onRowOut);
                    row.removeEventListener(MouseEvent.CLICK, onClose);
                }
            }
        }

        // the categories with a finding (their header and chosen option in red)
        private function flagged():Object {
            var o:Object = {};
            for each (var f:Object in findings) {
                o[f.category] = true;
            }
            return o;
        }

        private function fillEditor():void {
            clearEditor();
            editor = new Sprite();
            layer.addChild(editor);
            var bad:Object = flagged();
            dimmed = false;
            var colW:Number = 156;
            var w:Number = PAD * 2 + COLUMNS * colW;
            var y:Number = PAD;
            var title:TextField = field("$Header", 17, WHITE, "left");
            title.text = TITLE;
            title.x = PAD;
            title.y = y;
            title.width = w - 2 * PAD - 90;
            editor.addChild(title);
            doneBtn = button(CLOSE);
            doneRight = w - PAD;
            doneBtn.x = doneRight - sizeOf(doneBtn).width; // and again at each draw, by its size then
            doneBtn.y = y;
            doneBtn.addEventListener(MouseEvent.CLICK, onClose, false, 0, false);
            editor.addChild(doneBtn);
            y += HEAD_H;
            // the columns: the presets, then each category; as many rows as the presets' column can have (with the
            // Custom row), so no row moves when the Custom row comes or goes
            var rows:int = presets.length + 1;
            var col:int = 0;
            var x:Number = PAD;
            header(PRESET_HEADER, x, y, colW, false);
            var r:int = 0;
            for each (var p:Object in presets) {
                row(x, y + COL_HEAD_H + r * ROW_H, colW, p.name, preset == p.id, false, false, {preset: p.id});
                r++;
            }
            if (preset == "") {
                row(x, y + COL_HEAD_H + r * ROW_H, colW, CUSTOM, true, false, false, null);
                r++;
            }
            rows = Math.max(rows, r);
            for each (var c:Object in cats) {
                col++;
                x = PAD + col * colW;
                header(c.name, x, y, colW, bad[c.id] === true);
                r = 0;
                for each (var o:Object in c.options) {
                    var on:Boolean = options[c.id] == o.id;
                    var noBots:Boolean = bots && !o.bots && o.id != c.def;
                    dimmed = dimmed || noBots;
                    row(x, y + COL_HEAD_H + r * ROW_H, colW, o.name, on, on && bad[c.id] === true, noBots,
                        {category: c.id, option: o.id});
                    r++;
                }
                rows = Math.max(rows, r);
            }
            y += COL_HEAD_H + rows * ROW_H + 8;
            // the bots, across the editor
            row(PAD, y, w - 2 * PAD, BOTS_LABEL, bots, bad["bots"] === true, false, {bots: !bots});
            y += ROW_H + 6;
            // the findings, each on its own lines
            if (findings.length > 0) {
                var ft:TextField = field("$Body", 14, RED, "left");
                ft.multiline = true;
                ft.wordWrap = true;
                ft.width = w - 2 * PAD;
                ft.x = PAD;
                ft.y = y;
                var s:String = "";
                for each (var f:Object in findings) {
                    s += (s == "" ? "" : "\n") + f.message;
                }
                ft.text = CANT_START + s;
                ft.height = ft.textHeight + 6;
                editor.addChild(ft);
                y += ft.height + 2;
            }
            hintTf = field("$Body", 13, DIM, "left");
            hintTf.width = w - 2 * PAD;
            hintTf.x = PAD;
            hintTf.y = y;
            editor.addChild(hintTf);
            showHint();
            y += 22 + PAD - 4;
            // the background last (under everything): it takes the clicks that would reach the tiles below
            var g:Graphics = editor.graphics;
            g.lineStyle(1, FRAME, 0.8);
            g.beginFill(0x0A0A0C, 0.94);
            g.drawRect(0, 0, w, y);
            g.endFill();
            g.lineStyle(2, ACCENT, 0.5);
            g.moveTo(0, 0);
            g.lineTo(w, 0);
        }

        private function header(s:String, x:Number, y:Number, w:Number, bad:Boolean):void {
            var t:TextField = field("$Header", 14, bad ? RED : DIM, "left");
            t.text = s.toUpperCase();
            t.x = x + 2;
            t.y = y;
            t.width = w - 6;
            editor.addChild(t);
        }

        // one choice: EA's check box (checked = the active option) and its name; the whole row takes the click
        private function row(x:Number, y:Number, w:Number, label:String, on:Boolean, bad:Boolean, dim:Boolean,
                             act:Object):void {
            var s:Sprite = new Sprite();
            s.x = x;
            s.y = y;
            s.mouseChildren = false;
            s.buttonMode = act != null;
            paintRow(s, w, false);
            try {
                var ck:Object = new (getDefinitionByName(CHECK_CLASS) as Class)();
                ck.mouseEnabled = false;
                ck.mouseChildren = false;
                ck.label = "";
                ck.scrollRect = new Rectangle(0, 0, 26, 26); // the box only (as the ready mark)
                ck.x = 0;
                ck.y = 1;
                s.addChild(ck as DisplayObject);
                ck.selected = on; // and again at each draw (a CLIK control may set itself up a frame later)
                checks.push({ck: ck, on: on});
            } catch (err:Error) {
                say("rules check box: " + err.message);
            }
            var t:TextField = field("$Body", 13, bad ? RED : (dim ? OFF : (on ? WHITE : LIGHT)), "left");
            t.text = label;
            t.x = 30;
            t.y = 4;
            t.width = w - 32;
            fit(t, 13, w - 32);
            s.addChild(t);
            if (act != null) {
                act.w = w;
                s.name = String(rowActs.length);
                rowActs.push(act);
                s.addEventListener(MouseEvent.CLICK, onRowClick, false, 0, false);
                s.addEventListener(MouseEvent.ROLL_OVER, onRowOver, false, 0, false);
                s.addEventListener(MouseEvent.ROLL_OUT, onRowOut, false, 0, false);
            }
            editor.addChild(s);
        }

        private var rowActs:Array = [];   // a row's action, by the row's name (its index here)
        private var checks:Array = [];    // the editor's check boxes, {ck, on}

        private static function paintRow(s:Sprite, w:Number, over:Boolean):void {
            var g:Graphics = s.graphics;
            g.clear();
            g.beginFill(0xFFFFFF, over ? 0.08 : 0.01); // 0.01: invisible, but it takes the pointer
            g.drawRect(-2, 0, w, ROW_H - 2);
            g.endFill();
        }

        private function actOf(e:MouseEvent):Object {
            var s:Sprite = e.currentTarget as Sprite;
            var i:int = s != null ? int(s.name) : -1;
            return i >= 0 && i < rowActs.length ? rowActs[i] : null;
        }

        private function onRowOver(e:MouseEvent):void {
            var a:Object = actOf(e);
            if (a != null) {
                paintRow(e.currentTarget as Sprite, a.w, true);
            }
        }

        private function onRowOut(e:MouseEvent):void {
            var a:Object = actOf(e);
            if (a != null) {
                paintRow(e.currentTarget as Sprite, a.w, false);
            }
        }

        // the hint line: why some options are dimmed (while the bots are on), and what a change does. Always shown,
        // not on hover: the reason then needs no pointer (a posted hover in a test session didn't reach the rows).
        private function showHint():void {
            if (hintTf == null) {
                return;
            }
            var s:String = (dimmed ? NO_BOTS_HINT : "") + RESET_HINT;
            if (hintTf.text != s) {
                hintTf.text = s;
            }
        }

        // a click is a call; it shows at once, and the server's lobby.state confirms it or puts it back
        private function onRowClick(e:MouseEvent):void {
            var a:Object = actOf(e);
            if (a == null || !canEdit || cats == null) {
                return;
            }
            var now:int = getTimer();
            if (now - lastCall < CALL_GAP_MS) {
                return;
            }
            var args:Object = null;
            if (a.preset != null) {
                if (a.preset == preset) {
                    return;
                }
                args = {preset: a.preset};
            } else if (a.category != null) {
                if (options[a.category] == a.option) {
                    return;
                }
                var next:Object = {};
                for each (var c:Object in cats) {
                    next[c.id] = c.id == a.category ? a.option : options[c.id];
                }
                args = {rules: next};
            } else if (a.bots != null) {
                args = {bots: a.bots === true};
            }
            if (args == null || !channel.call("lobby.setRules", args)) {
                return;
            }
            lastCall = now;
            if (args.preset != null) {
                var p:Object = presetById(args.preset);
                if (p != null) {
                    for (var k:String in p.options) {
                        options[k] = p.options[k];
                    }
                    preset = p.id;
                }
            } else if (args.rules != null) {
                options = args.rules;
                var m:Object = presetMatching(options);
                preset = m != null ? m.id : "";
            } else {
                bots = args.bots;
            }
            dirty = true;
            askRedraw();
        }

        private function presetById(id:String):Object {
            for each (var p:Object in presets) {
                if (p.id == id) {
                    return p;
                }
            }
            return null;
        }

        private function presetMatching(o:Object):Object {
            for each (var p:Object in presets) {
                var same:Boolean = true;
                for each (var c:Object in cats) {
                    same = same && p.options[c.id] == o[c.id];
                }
                if (same) {
                    return p;
                }
            }
            return null;
        }

        // ---- helpers ----

        // a server's value as a string we draw: a string only, cut short
        private static function str(v:*, max:int):String {
            if (!(v is String)) {
                return "";
            }
            var s:String = v as String;
            return s.length > max ? s.substr(0, max - 1) + "..." : s;
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

        // EA's small button (Button001: the Leaderboard's Refresh and the Profile's search button, from the always loaded
        // Controls_Source bundle) at its own size (GOTCHAS: resizing a CLIK control scales its skin and cuts its label);
        // a framed button of ours only if the symbol isn't there
        private function button(label:String):Sprite {
            try {
                var ea:Sprite = new (getDefinitionByName(EA_BUTTON) as Class)() as Sprite;
                if (ea != null) {
                    Object(ea).label = label;
                    return ea;
                }
            } catch (err:Error) {
                say("rules button: " + err.message);
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
