package blaze

import (
	"fmt"
	"maps"
	"strconv"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
)

// Regions (M27, M28): the server's own machine is the home region, and host machines elsewhere have regions of their
// own (gmshost.Manager.SetRegions). Where a game's match host runs is decided by its players' Preferred Regions
// (preferred.go, PREF1): a custom game's creator's, a solo practice game's player's, the vote of a practice group's
// and a PvP match's humans. Every game starts through startHost (gmshost.Manager.StartIn), so a region that can't take
// it falls back home with a reason (REGION3).
//
// Where the game runs then is the match's (gmshost.Match.RegionPlacement): a custom game's browser Region (PSAS) shows
// it once the host is ready, and its lobby.state's region names it, with the notice when it isn't where it was wanted.

// DefaultRegionHome makes an account that never chose a region see the home region chosen in EA's own region picker
// (getUserSettings answers GameServerRegionId with the home region's id), until our Preferred Region dropdown replaces
// that picker. The client's own default is the asset's first region, North America. Since PREF1 the picker decides
// nothing; this only keeps what it shows close to the truth. false: the client's default.
const DefaultRegionHome = true

// SettingRegionID is the user setting EA's region picker saves (Client_RestoreGameServerRegion 0x00670F70 reads it
// back at login). Stored as sent; since PREF1 nothing reads it for a game's placement.
const SettingRegionID = "GameServerRegionId"

// RegionID is a region alias's GameServerRegionId: the FNV-1 of the alias, in decimal ("eu" -> "1819748219", as the
// client saved it at a click on Europe; "na" -> "1668749442", computed the same way).
func RegionID(alias string) string { return strconv.FormatUint(uint64(content.AssetID(alias)), 10) }

// regionsOn is the home region and every region of this server ("" and nil: regions are off, or no match hosts).
func (s *Server) regionsOn() (string, []string) {
	if s.Hosts == nil {
		return "", nil
	}
	return s.Hosts.HomeRegion(), s.Hosts.Regions()
}

// customWant is the region a custom game should run in, read at its create (the user's decision: fixed then): the
// creator's Preferred Region for a custom game (CREATE GAME), the vote of the creator and the creator's party for a
// practice group's. "" with regions off.
func (s *Server) customWant(r *Request, gid int64, browsable bool) string {
	sess := r.Conn.Session
	creator := regionVoter{persona: sess.Persona.ID, user: sess.Account.UserID, conn: r.Conn}
	what := fmt.Sprintf("game %d", gid)
	if browsable {
		return s.wantedBy(what, []regionVoter{creator})
	}
	return s.wantedBy(what+" (a practice group)", s.partyVoters(creator))
}

// startHost starts the game's match host in the region want (StartIn, with its fallback home), or with regions off
// ("") as before.
func (s *Server) startHost(spec gmshost.CreateGame, ev gmshost.Events, want string) (*gmshost.Match, error) {
	if want != "" {
		return s.Hosts.StartIn(spec, ev, gmshost.Want{Region: want})
	}
	return s.Hosts.Start(spec, ev)
}

// userSettings is what getUserSettings answers: the stored settings without our own (ourSetting), and with
// DefaultRegionHome and regions on the home region's GameServerRegionId for an account that never chose one in EA's
// picker (the stored settings stay as they are).
func (s *Server) userSettings(stored map[string]string) map[string]string {
	home, _ := s.regionsOn()
	ours := false
	for k := range stored {
		ours = ours || ourSetting(k)
	}
	fill := DefaultRegionHome && home != "" && stored[SettingRegionID] == ""
	if !ours && !fill {
		return stored
	}
	out := maps.Clone(stored)
	if out == nil {
		out = map[string]string{}
	}
	maps.DeleteFunc(out, func(k, _ string) bool { return ourSetting(k) })
	if fill {
		out[SettingRegionID] = RegionID(home)
	}
	return out
}

// LobbyRegion is lobby.state's region: the game runs in ID (Name); when that isn't where it was wanted, Want (WantName)
// is where, Got = ID and Why the reason (gmshost.FellBack*). REGION3's class reads Want/Got/Why only and words the
// notice by Why; PREF3's shows Name.
type LobbyRegion struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Want     string `json:"want,omitempty"`
	WantName string `json:"wantName,omitempty"`
	Got      string `json:"got,omitempty"`
	Why      string `json:"why,omitempty"`
}

// regionInfo is lobby.state's region for a game running in region (wanted there or elsewhere, fellBack why).
func (s *Server) regionInfo(region, wanted, fellBack string) *LobbyRegion {
	if region == "" {
		region, _ = s.regionTable()
	}
	r := &LobbyRegion{ID: region, Name: s.regionName(region)}
	if fellBack != "" && wanted != "" && region != wanted {
		r.Want, r.WantName, r.Got, r.Why = wanted, s.regionName(wanted), region, fellBack
	}
	return r
}

// regionReady takes a custom game's region from its match at gameReady (it doesn't change after): the browser's PSAS
// becomes the region its host runs in, and lobby.state's region names it (with the notice when that isn't where it was
// wanted).
func (hg *hostedGame) regionReady(mt *gmshost.Match) {
	if hg.custom == nil {
		return
	}
	region, wanted, fellBack := mt.RegionPlacement()
	info := hg.s.regionInfo(region, wanted, fellBack) // before hg.mu: it asks the host manager for names
	psas := hg.s.browserRegion(info.ID)
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.custom.region = info.ID
	hg.custom.psas = psas
	hg.custom.notice = info
}
