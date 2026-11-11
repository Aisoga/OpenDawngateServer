package localepatch

// The region texts (PREF3). The client names its two built-in regions from AssetData GameServerRegionList, whose
// regionName is ^GameServerRegion!0x1 / 0x2 ("North America", "Europe (alpha)"): the play screen's and the party
// lobby's region buttons and the game browser's Region for a game whose PSAS is "na" or "eu". The server's own names
// replace them where our classes and the browser's PSAS reach; these lines are the fallback for
// every other spot. The new-feature notice's EU item said the European servers were an alpha.

// The tables (FNV-1 of gameserverregion and newfeaturenotify).
const (
	GameServerRegion = 0x1ADEE60A
	NewFeatureNotify = 0x2502B5EA
)

// GameServerRegionLines are GameServerRegion's: Europe without its "(alpha)".
var GameServerRegionLines = map[uint32]string{
	0x00000002: "Europe",
}

// NewFeatureNotifyLines are newFeatureNotify's EU item: its title and text (0x5, its picture, stays).
var NewFeatureNotifyLines = map[uint32]string{
	0x00000003: "Choose your Preferred Region!",
	0x00000004: "On a server with more than one region, pick your Preferred Region on the Play screen: your games " +
		"are hosted there. In a group, every member's choice counts.",
}

// regionTables adds the region tables to Tables' result.
func regionTables(t map[uint32]map[uint32]string) {
	t[GameServerRegion] = GameServerRegionLines
	t[NewFeatureNotify] = NewFeatureNotifyLines
}
