// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The draft's sounds and the drafters' team chat, beside the board (DraftBoard.as), from the same draft.state events:
//   - the music: the game's own shaper-select music (Play_Music_Shaper_Select) from the draft's begin, stopped
//     (Stop_Music_Shaper_Select: every instance, a 5 s fade) when the draft is done or cancelled and when the lobby hides;
//     posted once per draft, so a resent draft.state (a late entry, a refusal, a teammate's hover) never starts it again;
//   - the turn sounds, for a drafter only and only for its own turns: the notification (Play_UI_notification, the
//     menus' interact event 74) when a ban or pick window with the player's open turn begins, and the countdown tick
//     (Play_UI_Countdown_tick, 149) at 5, 4, 3, 2 and 1 s left in it while the turn is still open; once per window
//     (keyed to the phase and the window's number); a vote (Army Pick, Clone Pick) is one such window, open until the
//     player's lock;
//   - the chat: a drafter's custom lobby chat window switches to the team channel at the draft's begin and back to the
//     lobby channel at its end, cancel or the lobby's hide (the window's own "Switched to ..." line is the cue);
//     spectators keep the lobby channel, and so does everyone in a Clone Pick vote (all ten vote for one shaper).
//
// The game's mute setting for shaper-select music can't be read from the lobby (only the Options screen reads it), so
// the music plays even for a player who muted shaper select's, until the game re-applies that setting at the next
// shaper select (accepted: the draft's own music switch was taken out at the user's word, it looked out of place and
// didn't work well; the music stays). Every call goes by name through ExternalInterface
// (Global.PlaySound: its first argument is ignored, the second is the Wwise event's name; Global.OnInteractEvent: the
// menus' sound table) or through EA's chat classes, by name, and only while the lobby is shown (LobbyReady hands the
// events over only then, and calls detach() at the hide).
package {
    import flash.events.TimerEvent;
    import flash.external.ExternalInterface;
    import flash.utils.Timer;
    import flash.utils.getDefinitionByName;
    import flash.utils.getTimer;

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

        private var say:Function;

        private var playing:Boolean = false;  // our Play was posted and not stopped
        private var chatTeam:Boolean = false; // the window was switched to the team channel by us
        private var turnKey:String = "";      // the last window whose notification played: "<phase>:<window>"
        private var mine:Boolean = false;     // the player's turn in it is open
        private var deadline:int = 0;         // getTimer() at its end
        private var nextTick:int = 0;         // the next tick's second (0: none left)
        private var timer:Timer = null;

        public function DraftSound(say:Function) {
            this.say = say;
        }

        // ---- the server's event ----

        public function setState(d:Object):void {
            if (d == null) {
                return;
            }
            var ph:String = d.phase is String ? String(d.phase) : "";
            if (ph != "role" && ph != "ban" && ph != "pick" && ph != "trade" && ph != "vote") {
                end(); // done, cancelled, or nothing we know
                return;
            }
            if (!playing) {
                playing = true;
                post(MUSIC_PLAY);
            }
            var drafter:Boolean = d.you != null && (int(d.you.team) == 1 || int(d.you.team) == 2);
            var clone:Boolean = d.vote != null && d.vote.kind == "clone"; // everyone votes together: the lobby's chat
            if (drafter && !chatTeam && !clone) {
                chatTeam = chat("team"); // again at the next event if the window wasn't there
            }
            var open:Boolean = drafter && d.yourTurn === true && (ph == "ban" || ph == "pick" || ph == "vote");
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
