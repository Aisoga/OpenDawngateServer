// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The draft's sounds and the drafters' team chat, beside the board (DraftBoard.as), from the same draft.state events:
//   - the music: the game's own shaper-select music (Play_Music_Shaper_Select) from the draft's begin, stopped
//     (Stop_Music_Shaper_Select: every instance, a 5 s fade) when the draft is done or cancelled and when the lobby hides;
//     posted once per draft, so a resent draft.state (a late entry, a refusal, a teammate's hover) never starts it again;
//   - a speaker button in the board's head row, top right: the player's own choice of the draft's music (only the
//     music: the turn sounds stay), off stops it at once and keeps it off for later drafts, on starts it again while a
//     draft runs. The choice goes to the server (draft.music {on}), which keeps it in the account's settings and sends
//     it back in the player's own draft.state (musicOff: true when off; absent when on, or from a server without it);
//     a choice made in this client run counts over the server's word until the client closes;
//   - the turn sounds, for a drafter only and only for its own turns: the notification (Play_UI_notification, the
//     menus' interact event 74) when a ban or pick window with the player's open turn begins, and the countdown tick
//     (Play_UI_Countdown_tick, 149) at 5, 4, 3, 2 and 1 s left in it while the turn is still open; once per window
//     (keyed to the phase and the window's number);
//   - the chat: a drafter's custom lobby chat window switches to the team channel at the draft's begin and back to the
//     lobby channel at its end, cancel or the lobby's hide (the window's own "Switched to ..." line is the cue);
//     spectators keep the lobby channel.
//
// The game's mute setting for shaper-select music can't be read from the lobby (only the Options screen reads it), so
// the music plays even for a player who muted shaper select's, until the game re-applies that setting at the next
// shaper select; the speaker button is the draft's own switch. Every call goes by name through ExternalInterface
// (Global.PlaySound: its first argument is ignored, the second is the Wwise event's name; Global.OnInteractEvent: the
// menus' sound table) or through EA's chat classes, by name, and only while the lobby is shown (LobbyReady hands the
// events over only then, and calls detach() at the hide).
package {
    import flash.display.DisplayObject;
    import flash.display.Graphics;
    import flash.display.Sprite;
    import flash.events.MouseEvent;
    import flash.events.TimerEvent;
    import flash.external.ExternalInterface;
    import flash.geom.Rectangle;
    import flash.utils.Timer;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

    import opendawngate.ServerChannel;

    public class DraftSound {
        private static const SOUND_TAG:String = "draft"; // Global.PlaySound's first argument (the game ignores it)
        private static const MUSIC_PLAY:String = "Play_Music_Shaper_Select";
        private static const MUSIC_STOP:String = "Stop_Music_Shaper_Select";
        private static const IE_NOTIFY:int = 74; // CHAT_NOTIFY_SYS_MESSAGE: Play_UI_notification
        private static const IE_TICK:int = 149;  // Play_UI_Countdown_tick
        private static const TICKS:int = 5;      // the ticks at 5, 4, 3, 2 and 1 s left
        private static const TICK_MS:int = 100;
        private static const MAX_LEFT_MS:int = 600000;
        private static const CHAT_CLASS:String = "maxis.chat.manager.ChatManager";
        private static const CALL_MUSIC:String = "draft.music";

        // the button: the board's head row, at its right end (DraftBoard's PAD, HEAD_H and SPEAKER_W: the board's
        // rectangle, DraftBoard.boardRect(), and the place it keeps free there)
        private static const PAD:int = 12;
        private static const HEAD_H:int = 46;
        private static const SPEAKER_W:int = 32;
        private static const BTN_S:int = 24;
        private static const ON_COLOR:uint = 0xD8D8D8;
        private static const OFF_COLOR:uint = 0x6E6E6E;
        private static const OVER_COLOR:uint = 0xE8A33B; // the board's accent

        private var channel:ServerChannel;
        private var say:Function;

        private var running:Boolean = false;  // a draft runs (role, ban, pick or trade)
        private var playing:Boolean = false;  // our Play was posted and not stopped
        private var chatTeam:Boolean = false; // the window was switched to the team channel by us
        private var turnKey:String = "";      // the last window whose notification played: "<phase>:<window>"
        private var mine:Boolean = false;     // the player's turn in it is open
        private var deadline:int = 0;         // getTimer() at its end
        private var nextTick:int = 0;         // the next tick's second (0: none left)
        private var timer:Timer = null;

        private var musicOn:Boolean = true;
        private var musicChosen:Boolean = false; // the player clicked in this client run: that counts over the server's

        private var btn:Sprite = null;
        private var over:Boolean = false;

        public function DraftSound(channel:ServerChannel, say:Function) {
            this.channel = channel;
            this.say = say;
        }

        // ---- the server's event ----

        public function setState(d:Object):void {
            if (d == null) {
                return;
            }
            var ph:String = d.phase is String ? String(d.phase) : "";
            if (!musicChosen) {
                musicOn = d.musicOff !== true;
            }
            if (ph != "role" && ph != "ban" && ph != "pick" && ph != "trade") {
                end(); // done, cancelled, or nothing we know
                return;
            }
            running = true;
            if (musicOn && !playing) {
                playing = true;
                post(MUSIC_PLAY);
            } else if (!musicOn && playing) {
                playing = false;
                post(MUSIC_STOP);
            }
            var drafter:Boolean = d.you != null && (int(d.you.team) == 1 || int(d.you.team) == 2);
            if (drafter && !chatTeam) {
                chatTeam = chat("team"); // again at the next event if the window wasn't there
            }
            var open:Boolean = drafter && d.yourTurn === true && (ph == "ban" || ph == "pick");
            if (!open) {
                mine = false;
                stopTicks();
                return;
            }
            var left:int = d.left is Number ? Math.max(0, Math.min(MAX_LEFT_MS, int(d.left))) : 0;
            deadline = getTimer() + left;
            var key:String = ph + ":" + int(d.window);
            if (key != turnKey) {
                turnKey = key;
                mine = true;
                ie(IE_NOTIFY);
                nextTick = Math.min(TICKS, Math.ceil(left / 1000) - 1);
            } else if (!mine) {
                return; // the turn was closed (a lock) and the window is the same: no ticks again
            }
            startTicks();
        }

        // the lobby hides (left, or the game starts): everything stops, the chat goes back to the lobby channel
        public function detach():void {
            end();
            if (btn != null) {
                btn.removeEventListener(MouseEvent.CLICK, onClick);
                btn.removeEventListener(MouseEvent.ROLL_OVER, onOver);
                btn.removeEventListener(MouseEvent.ROLL_OUT, onOut);
                if (btn.parent != null) {
                    btn.parent.removeChild(btn);
                }
                btn = null;
            }
            over = false;
        }

        private function end():void {
            stopTicks();
            if (playing) {
                playing = false;
                post(MUSIC_STOP);
            }
            if (chatTeam) {
                chat("lobby");
            }
            running = false;
            chatTeam = false;
            turnKey = "";
            mine = false;
        }

        // ---- the countdown ticks ----

        private function startTicks():void {
            if (nextTick < 1) {
                stopTicks();
                return;
            }
            if (timer == null) {
                timer = new Timer(TICK_MS);
                timer.addEventListener(TimerEvent.TIMER, onTimer, false, 0, false);
            }
            if (!timer.running) {
                timer.start();
            }
        }

        private function stopTicks():void {
            if (timer != null && timer.running) {
                timer.stop();
            }
        }

        private function onTimer(e:TimerEvent):void {
            var rem:int = deadline - getTimer();
            if (rem <= 0 || nextTick < 1) {
                stopTicks();
                return;
            }
            if (rem <= nextTick * 1000) {
                ie(IE_TICK);
                nextTick = Math.min(nextTick - 1, Math.ceil(rem / 1000) - 1); // a late timer skips, never doubles
                if (nextTick < 1) {
                    stopTicks();
                }
            }
        }

        // ---- the speaker button: drawn on the board's layer by LobbyReady's draw, after the board's own ----

        public function draw(board:DisplayObject, rect:Rectangle):void {
            var layer:Sprite = board as Sprite;
            if (layer == null || rect == null || layer.parent == null) {
                if (btn != null && btn.parent != null) {
                    btn.parent.removeChild(btn);
                }
                return;
            }
            if (btn == null) {
                btn = new Sprite();
                btn.buttonMode = true;
                btn.mouseChildren = false;
                btn.addEventListener(MouseEvent.CLICK, onClick, false, 0, false);
                btn.addEventListener(MouseEvent.ROLL_OVER, onOver, false, 0, false);
                btn.addEventListener(MouseEvent.ROLL_OUT, onOut, false, 0, false);
                paint();
            }
            if (btn.parent != layer) {
                layer.addChild(btn);
            } else if (layer.getChildIndex(btn) != layer.numChildren - 1) {
                layer.setChildIndex(btn, layer.numChildren - 1);
            }
            // in the board's own units: the head row's right end, SPEAKER_W wide before the padding (DraftBoard keeps
            // it free)
            btn.x = Math.round(rect.width - PAD - SPEAKER_W + (SPEAKER_W - BTN_S) / 2);
            btn.y = PAD + Math.round((HEAD_H - BTN_S) / 2);
        }

        // a speaker: a box and a cone; on, two waves; off, a cross
        private function paint():void {
            if (btn == null) {
                return;
            }
            var c:uint = over ? OVER_COLOR : (musicOn ? ON_COLOR : OFF_COLOR);
            var g:Graphics = btn.graphics;
            g.clear();
            g.lineStyle();
            g.beginFill(0x000000, 0.01); // the whole square takes the click
            g.drawRect(0, 0, BTN_S, BTN_S);
            g.endFill();
            g.beginFill(c, 1);
            g.drawRect(3, 9, 4, 6);
            g.moveTo(7, 9);
            g.lineTo(12, 4);
            g.lineTo(12, 20);
            g.lineTo(7, 15);
            g.lineTo(7, 9);
            g.endFill();
            g.lineStyle(2, c, 1);
            if (musicOn) {
                g.moveTo(15, 9);
                g.curveTo(17, 12, 15, 15);
                g.moveTo(18, 6);
                g.curveTo(22, 12, 18, 18);
            } else {
                g.moveTo(15, 9);
                g.lineTo(21, 15);
                g.moveTo(21, 9);
                g.lineTo(15, 15);
            }
        }

        private function onOver(e:MouseEvent):void {
            over = true;
            paint();
        }

        private function onOut(e:MouseEvent):void {
            over = false;
            paint();
        }

        private function onClick(e:MouseEvent):void {
            musicOn = !musicOn;
            musicChosen = true;
            channel.call(CALL_MUSIC, {on: musicOn});
            if (running && musicOn && !playing) {
                playing = true;
                post(MUSIC_PLAY);
            } else if (!musicOn && playing) {
                playing = false;
                post(MUSIC_STOP);
            }
            paint();
        }

        // ---- the game ----

        private function post(event:String):void {
            try {
                ExternalInterface.call("Global.PlaySound", SOUND_TAG, event);
            } catch (err:Error) {
                say("draft sound " + event + ": " + err.message);
            }
        }

        private function ie(id:int):void {
            try {
                ExternalInterface.call("Global.OnInteractEvent", id);
            } catch (err:Error) {
                say("draft sound " + id + ": " + err.message);
            }
        }

        // the custom lobby's chat window to a channel ("team" or "lobby"); false when it couldn't
        private function chat(name:String):Boolean {
            try {
                var cm:Object = getDefinitionByName(CHAT_CLASS)["GetChatManager"]();
                var w:Object = cm == null ? null : cm.customGameChatWindow;
                if (w == null) {
                    return false;
                }
                w.SwitchToChannel(name, false);
                return true;
            } catch (err:Error) {
                say("draft chat " + name + ": " + err.message);
            }
            return false;
        }
    }
}
