package moviepatch

import (
	_ "embed"
)

// OptionsTags are the Options menu's drag scroll check box's four tags (client_ui/options, DragGrabOption: one more of
// the gameplay tab's own check boxes, OptionDragGrab, which the game's Options code loads and saves by name):
// DefineSprite 2000, DoABC (the class), SymbolClass 2000 -> DragGrabOption, PlaceObject3 of 2000 at depth 2000 without
// an instance name.
var OptionsTags = mustHex(optionsTagsHex)

//go:embed options_tags.hex
var optionsTagsHex string

// Options is the Options menu's "Grab-style drag scroll (middle mouse)" check box in the gameplay tab: the tab's movie
// UI/Scaleform/Options/Options_GameSettings.gfx from game\Data\Options.snap into game\Data\zz_Options.snap (every
// player copy; re/scripts/patch_login_movie.py --spec options). The tab's character ids end at 80 and its root depths
// at 99, so 2000 is free. Its label is localepatch.OptionsLines' line.
var Options = Spec{What: "the Options menu's drag scroll check box", Source: "game/Data/Options.snap",
	Rel: "game/Data/zz_Options.snap", MoviePath: "UI/Scaleform/Options/Options_GameSettings.gfx",
	Class: "DragGrabOption", CharID: 2000, Depth: 2000, Tags: OptionsTags}
