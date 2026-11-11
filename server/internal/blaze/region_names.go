package blaze

import "strings"

// The game browser's Region column (PREF3). The client writes a listed game's PSAS into the column's cell: a value
// that names one of its two built-in regions (the FNV-1 of "na" or "eu", AssetData GameServerRegionList, looked up by
// 0x0066B3C0) becomes that region's localized name, any other value is shown as it is (0x004E40A0), through the cell's
// htmlText (maxis.MatchListViewCell). So the server sends the region's own display name there, and every client,
// with our classes or without, shows the server's names (the user's "the browser shows the server's names too").

// htmlEscaper keeps a name's &, < and > from being read as HTML by the browser's cell.
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// browserRegion is a listed game's PSAS for a game running in region: its display name, escaped for the cell's
// htmlText, on a server with more than one region; "" (an empty column) on a server with one, which has nothing to
// tell apart (as the play screen hides Preferred Region there).
func (s *Server) browserRegion(region string) string {
	if _, ids := s.regionTable(); len(ids) < 2 {
		return ""
	}
	if region == "" {
		region, _ = s.regionTable()
	}
	return htmlEscaper.Replace(s.regionName(region))
}
