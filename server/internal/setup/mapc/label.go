package mapc

// Label is what offers a map to players besides its archive, as map.json names it: the server's playable level (its
// name, tag, the game browser's prefix and the display name), the create dialog's line in the client copy's map list
// (dgmaps.json: "map<TAB><tag><TAB><display>") and the map's line in the custom lobby's table FE_CustomGameLobby, by
// which the client (its map-name method patched by the setup) names the map in the custom lobby and the game browser:
// the line of the map's LevelId. The services and the setup take it from here once they read the installed packages.
type Label struct {
	Name    string `json:"name"`    // the Level's name (createGame's level without ".Level")
	Tag     string `json:"tag"`     // a custom game's name tag: "<game name>~#<tag>"
	Prefix  string `json:"prefix"`  // put in front of a game's name in the game browser
	Display string `json:"display"` // the dialog's and the lobby's name
	Archive string `json:"archive"` // zz_<name>.snap in game\Data
	// MapListLine is the client copy's dgmaps.json line, with its line end.
	MapListLine string `json:"mapListLine"`
	// LocaleTable and LocaleID name the map's line (FE_CustomGameLobby, the line of the map's LevelId: FNV-1 of the
	// lower-cased "<name>.Level"); LocaleText is its text, the display name.
	LocaleTable string `json:"localeTable"`
	LocaleID    uint32 `json:"localeId"`
	LocaleText  string `json:"localeText"`
}

// LabelOf is the map's label.
func LabelOf(m *Map) Label {
	return Label{Name: m.Name, Tag: m.Tag, Prefix: m.Prefix, Display: m.Display, Archive: File(m.Name),
		MapListLine: "map\t" + m.Tag + "\t" + m.Display + "\n", LocaleTable: "FE_CustomGameLobby",
		LocaleID: fnv1Lower(m.Name + ".Level"), LocaleText: m.Display}
}
