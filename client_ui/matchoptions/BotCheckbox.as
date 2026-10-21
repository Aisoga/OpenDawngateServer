// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The "Fill open seats with bots" check box in the game's CREATE GAME dialog (Custom Game Setup).
//
// The setup places this class on the dialog movie's root timeline with four added tags (the game's own bytecode stays
// as it is). It adds a check box from the movie's own library (CheckboxLabeled_new, the skin of Show Password) to the
// dialog, right-aligned on the line of "Map: The Dawngate". The box starts unticked: the game loads the movie anew for
// every CREATE GAME, and the class also unticks it whenever the dialog becomes the modal clip again.
//
// Ticked, the class tells the server through the game's name: when the player confirms (the Create button, or Enter in
// the name or password field) the name gets the marker ~#bots at its end just before the game's own handler reads it
// (the game's C++ reads the field at once, inside that handler), and the typed name is put back right after. The
// server takes the marker off, shows the game as "[Bots] <name>" and fills the open seats with bots at the start. A
// name too long for the marker is cut so that name and marker fit the dialog's limit (the field's maxChars, which the
// game sets from its own maximum). Unticked, nothing changes.
//
// Build: re/scripts/build_picker.py build matchoptions (the Apache Flex SDK's mxmlc).
package {
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.MovieClip;
    import flash.events.Event;
    import flash.utils.getDefinitionByName;

    public class BotCheckbox extends MovieClip {
        // The marker the server reads at the end of a custom game's name (the server's blaze.BotFillMarker).
        private static const MARKER:String = "~#bots";
        private static const LABEL:String = "Fill open seats with bots";
        // The game's GetMaxGameNameLength, should the field not tell.
        private static const MAX_NAME:int = 31;
        private static const PLACE_FRAMES:int = 12; // the box is placed again while its skin settles its size

        private var dlg:DisplayObjectContainer;
        private var nameIn:Object;
        private var pwIn:Object;
        private var okBtn:Object;
        private var box:Object;
        private var placeFrames:int = PLACE_FRAMES;
        private var wasModal:Boolean = false;
        private var saved:String = null; // the typed name while the field holds the marked one

        public function BotCheckbox() {
            super();
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame);
        }

        private function onFrame(e:Event):void {
            if (dlg == null && !hook()) {
                return;
            }
            if (saved != null) {
                restore(null); // a fold whose restore didn't run (the game's handler stopped the event)
            }
            if (box == null) {
                return;
            }
            if (placeFrames > 0) {
                placeFrames--;
                place();
            }
            var modal:Boolean = isModal();
            if (modal && !wasModal) {
                box["selected"] = false;
            }
            wasModal = modal;
            var on:Boolean = Boolean(nameIn["enabled"]);
            if (Boolean(box["enabled"]) != on) {
                box["enabled"] = on;
            }
        }

        // hook finds the dialog's fields and buttons (root.mcMatchOptionsDialog) and adds the box and the listeners.
        private function hook():Boolean {
            var d:Object = null;
            try {
                d = root != null ? root["mcMatchOptionsDialog"] : null;
            } catch (err:Error) {
                d = null;
            }
            if (d == null || !(d is DisplayObjectContainer) || d["mcGameNameInput"] == null ||
                    d["mcPasswordInput"] == null || d["mcConfirmBtn"] == null) {
                return false;
            }
            dlg = DisplayObjectContainer(d);
            nameIn = d["mcGameNameInput"];
            pwIn = d["mcPasswordInput"];
            okBtn = d["mcConfirmBtn"];
            try {
                var c:Class = getDefinitionByName("CheckboxLabeled_new") as Class;
                var b:Object = new c();
                b["label"] = LABEL;
                b["autoSize"] = "left";
                b["selected"] = false;
                dlg.addChild(DisplayObject(b));
                box = b;
                place();
            } catch (err:Error) {
                box = null; // no box: nothing is folded
                return true;
            }
            // The game listens on the button and both fields at the target phase with priority 0: a capture-phase
            // listener on the dialog runs before it, one at priority -1000 on each target after it.
            dlg.addEventListener("buttonClick", foldBefore, true, 1000);
            dlg.addEventListener("IF_Submit", foldBefore, true, 1000);
            okBtn.addEventListener("buttonClick", restore, false, -1000);
            nameIn.addEventListener("IF_Submit", restore, false, -1000);
            pwIn.addEventListener("IF_Submit", restore, false, -1000);
            return true;
        }

        // place puts the box on the map line, its right end under the password field's.
        private function place():void {
            try {
                var map:Object = dlg["textFieldMap"];
                box["x"] = Number(pwIn["x"]) + Number(pwIn["width"]) - Number(box["width"]);
                box["y"] = map != null ? Number(map["y"]) : Number(pwIn["y"]) + Number(pwIn["height"]) + 8;
            } catch (err:Error) {
            }
        }

        private function isModal():Boolean {
            try {
                var fm:Object = getDefinitionByName("scaleform.gfx.FocusManager");
                return fm.getModalClip() == dlg;
            } catch (err:Error) {
            }
            return false;
        }

        private function foldBefore(e:Event):void {
            if (saved != null || box == null || !box["selected"]) {
                return;
            }
            if (e.target != okBtn && e.target != nameIn && e.target != pwIn) {
                return;
            }
            var t:String = String(nameIn["text"]);
            if (t.length == 0 || t.substr(t.length - MARKER.length) == MARKER) {
                return; // nothing typed (the game's handler does nothing then), or the marker typed by hand
            }
            var max:int = int(nameIn["maxChars"]);
            if (max <= MARKER.length) {
                max = MAX_NAME;
            }
            var keep:String = t;
            if (keep.length + MARKER.length > max) {
                keep = keep.substr(0, max - MARKER.length);
            }
            saved = t;
            nameIn["text"] = keep + MARKER;
        }

        private function restore(e:Event):void {
            if (saved == null) {
                return;
            }
            try {
                nameIn["text"] = saved;
            } catch (err:Error) {
            }
            saved = null;
        }
    }
}
