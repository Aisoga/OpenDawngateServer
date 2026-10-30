package web

import (
	"embed"
	"io/fs"
)

// The web content the client fetches after login (roadmap B6, docs/WEB_CONTENT.md). Once
// BGWeb::getCurrentVersion's DRC is known, the client's WebContentManager (WebContentManager.cpp)
// builds two URL roots from the patched Config.package templates,
//
//	<loc> = http://<host>:<http-port>/battlegrounds_test/live/www/client/<DRC>/en-us
//	<com> = http://<host>:<http-port>/battlegrounds_test/live/www/client/<DRC>/common
//
// and fetches <loc>/dynamic_lore/dynamic_lore.snap at once (DynamicLore_StartDownload 0x0053CA20) and
// <loc>/dynamic_strings/dynamic_strings.xml on its next update (DynamicStrings_Update 0x0053D0B0), both
// through its DownloadManager (DirtySDK ProtoHttp). A 404 (or any non-2xx answer) brings a retry
// after 15 s; after a valid file the strings come again every 5 minutes and the lore every 10. The
// main menu's home page view loads <com>/home.html into an EAWebKit view.
//
// The files in www/ are our own content (the originals came from EA's CDN). The builders below serve
// them when --www has no file of the same path.

//go:embed www/home.html www/history.html www/dynamic_strings.xml www/dynamic_lore.xml
var wwwFiles embed.FS

// DynamicLoreXMLPath is the archive path of the lore XML inside dynamic_lore.snap: Config.package's
// DynamicLoreXmlPath ("dynamic_lore/dynamic_lore.xml"), which CheckWebContentFileVersion (0x0053D690)
// loads from the mounted package.
const DynamicLoreXMLPath = "dynamic_lore/dynamic_lore.xml"

func wwwFile(name string) []byte {
	b, err := fs.ReadFile(wwwFiles, "www/"+name)
	if err != nil {
		panic(err) // embedded at build time
	}
	return b
}

// DynamicStrings is our dynamic_strings.xml (DynamicStringsURL "dynamic_strings/dynamic_strings.xml",
// relative to <loc>). The client parses it with expat (callback 0x0053C6D0): elements named
// DynamicString (any case) at depth 2 with the attributes tableId and instanceId (parsed like wcstoul
// with base 0, so "0x..." is hex; both must be non-zero) set the element's text as the override of
// that Locale string (DynamicStrings_SetString 0x0053BC40). Strings missing from a newer file are
// dropped. Ours has no entries: the client keeps its own strings.
func DynamicStrings() []byte { return wwwFile("dynamic_strings.xml") }

// DynamicLoreXML is the dynamic_lore.xml we pack into the snap: <DynamicLore><Votes> with <Vote name
// cardtexta cardtextb cardimga cardimgb/> entries (DynamicLore_OnStartElement 0x0053D1C0, element names
// compared case-insensitively by FNV-1). Ours has no votes.
func DynamicLoreXML() []byte { return wwwFile("dynamic_lore.xml") }

// DynamicLoreSnap is our dynamic_lore.snap (DynamicLorePackageUrl
// "~webContentRootLoc~/dynamic_lore/dynamic_lore.snap"): a .snap archive with the one entry
// DynamicLoreXMLPath. The client saves it as Dynamic/dl/dl.<DRC>.snap in its user data folder, mounts
// it as a package (Package_MountSnap 0x00AE2A00: "pans" and version 0x0200 are checked) and parses
// the XML; a package that doesn't mount means a retry after 15 s.
func DynamicLoreSnap() []byte {
	return BuildSnap([]SnapEntry{{Name: DynamicLoreXMLPath, Data: DynamicLoreXML()}})
}

// HomePage is our home.html, the page the main menu's home view loads from <com>/home.html (Locale
// FE_HomePage!0x00000002 "~webContentUrlRootCommon~/home.html", expanded by pleaseExpandMoviePath
// 0x004BD8E0). It defines the hooks the client runs in it (onLivingLoreVoteUpdate,
// onRegionKillswitchEuUpdate; onWebVersionUpdate for completeness) and uses the native Client object
// (getLocalPlayerName, requestMainMenuContent, ...). EAWebKit presumably opens it only if the host is
// in Config_Pack.package's WebkitAllowedDomains (no session has requested it so far; patch_config.py
// adds the host since B6, to be confirmed live in B6-live).
func HomePage() []byte { return wwwFile("home.html") }

// HistoryPage is our history.html beside home.html (<com>/history.html): the recent games and a match's stats from
// games.json and game.json (history.go). The home page links to it (history.html, history.html#match=<id>) and
// navigates its own view there; the After Action Review's panel loads history.html?embed=1&latest=1 (a compact
// layout of about 1074 x 597 that opens the player's newest match). The player is Client.getLocalPlayerName(), sent
// as me= with every request.
func HistoryPage() []byte { return wwwFile("history.html") }
