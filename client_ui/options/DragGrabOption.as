// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The Options menu's "Grab-style drag scroll (middle mouse)" check box, in the gameplay tab.
//
// The setup places this class on the tab movie's root timeline (UI/Scaleform/Options/Options_GameSettings.gfx) with
// four added tags; the game's own bytecode stays as it is. The class makes one more of the tab's own check boxes (the
// library symbol Options_GameSetCheckBox, the skin and label of the twelve shipped ones), names it OptionDragGrab and
// puts it straight on the tab's root, under the Screen Edge Scroll Speed slider. That is all it does: the game's own
// Options code handles it like the others, by name.
//   - Load: Options.as reads Options.GetGameSettings and sets tab[key].selected for every key the tab has, when the
//     tab movie has loaded, at every opening and at RESET TO DEFAULTS. The box must exist (and be the root's property
//     OptionDragGrab) before the first of those, which runs at the loader's INIT, right after this movie's first
//     frame is built: so the class makes the box in its constructor (the timeline has made the tab's own children
//     and put this one on the root by then). Should the root not be there yet, it tries again when it is added and at
//     each frame, and then reads the value once itself (the same call), since the game's first load has passed.
//   - Save: SAVE walks the root's children and stores each visible one whose String() contains "CheckBox" by its
//     name (Options.SaveSettings' gameCheckboxProperties), so the box is saved with the others, as OptionDragGrab, in
//     the player's Preferences.prop. This class's own name must therefore not contain that word.
// The game reads the key where its options table names it; a game without that entry neither returns nor keeps it,
// and the box then stays unticked.
//
// The label is the locale line ^Options!0x5e70f001 (the setup's locale package); where the copy lacks it, the English
// text below.
//
// Build: re/scripts/build_picker.py build options (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.events.Event;
    import flash.external.ExternalInterface;
    import flash.utils.getDefinitionByName;

    public class DragGrabOption extends MovieClip {
        private static const NAME:String = "OptionDragGrab";        // the Preferences.prop key the game stores
        private static const SKIN:String = "Options_GameSetCheckBox"; // the tab's own check box (library symbol 36)
        private static const KEY:String = "^Options!0x5e70f001";
        private static const LABEL:String = "Grab-style drag scroll (middle mouse)";
        // Under the Screen Edge Scroll Speed slider (the slider at 404, 332; the panel ends at y 400), in tab
        // coordinates (930 x 500), aligned with the slider's left end.
        private static const X:Number = 404;
        private static const Y:Number = 370;
        private static const TRIES:int = 120;                        // frames to wait for the root at most

        private var box:Object;
        private var tries:int = TRIES;

        public function DragGrabOption() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            if (!make(false)) {
                addEventListener(Event.ADDED, onLater, false, 0, false);
                addEventListener(Event.ENTER_FRAME, onLater, false, 0, false);
            }
        }

        private function onLater(e:Event):void {
            if (e.type == Event.ADDED && e.target != this) {
                return;
            }
            if (make(true) || --tries <= 0) {
                removeEventListener(Event.ADDED, onLater);
                removeEventListener(Event.ENTER_FRAME, onLater);
            }
        }

        // Makes the box on the tab's root; late = after the game's first load, so the value is read here.
        private function make(late:Boolean):Boolean {
            var root:DisplayObjectContainer = parent;
            if (box != null) {
                return true;
            }
            if (root == null) {
                return false;
            }
            try {
                if (root.getChildByName(NAME) != null) {
                    return true; // the movie has one already
                }
                var c:Class = getDefinitionByName(SKIN) as Class;
                var b:Object = new c();
                b.name = NAME;
                // as the shipped boxes are set up (the tab's __setProp_* functions)
                b.autoSize = "none";
                b.data = "";
                b.enabled = true;
                b.focusable = true;
                b.label = label();
                b.selected = false;
                b.visible = true;
                b.x = X;
                b.y = Y;
                Object(root)[NAME] = b; // the load finds it as tab[key]
                root.addChild(b as MovieClip);
                box = b;
            } catch (err:Error) {
                return true; // no such symbol (another movie): nothing to do
            }
            if (late) {
                readValue();
            }
            return true;
        }

        private function label():String {
            try {
                var t:Object = ExternalInterface.call("Global.ForceTranslateString", KEY);
                if (t != null && String(t) != "" && String(t).charAt(0) != "^") {
                    return KEY; // the line is there: the label's own translation shows it
                }
            } catch (err:Error) {
            }
            return LABEL;
        }

        private function readValue():void {
            try {
                var s:Object = ExternalInterface.call("Options.GetGameSettings", false);
                if (s != null && typeof s[NAME] == "boolean") {
                    box.selected = s[NAME];
                }
            } catch (err:Error) {
            }
        }
    }
}
