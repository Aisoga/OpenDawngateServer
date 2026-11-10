package blaze

import (
	"maps"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
)

// Regions for custom games (M27). The play screen's region choice (North America, Europe) decides where a custom game's
// match host runs: the server's own machine is the home region, and host machines elsewhere have regions of their own
// (gmshost.Manager.SetRegions). Only a browsable custom game (CREATE GAME) looks at it; PvP, practice and a practice
// group's game stay on the home region's machines.
//
// The wanted region at the create (WantedRegion): the client sends the region's ping sites (updateNetworkInfo's NLMP:
// the chosen region 50, "<region>-any" 100, or "<region>-any" 50 with Match With All Regions; Client_SetGameServerRegion
// 0x00670B70) and saveUserSettings {GameServerRegionId} at the same click, both on the Blaze connection before CREATE
// GAME's resetDedicatedServer. So the lowest-latency alias of NLMP, its "-any" cut, is the current choice; a connection
// without one (or with an alias that is no region of this server) has the stored GameServerRegionId; neither gives the
// home region. A region this server has no machines for counts as no wish.
//
// Where the game runs then is the match's (gmshost.Match.RegionPlacement): the browser's Region (PSAS) shows it once the
// host is ready, and lobby.state's region tells the lobby when it isn't where its creator wanted (regionNotice).

// DefaultRegionHome makes an account that never chose a region see the home region chosen (getUserSettings answers
// GameServerRegionId with the home region's id). The client's own default is the asset's first region, North America,
// which on a European main server would put every new player's custom games on the North American machine. false: the
// client's default.
const DefaultRegionHome = true

// SettingRegionID is the user setting the play screen's region choice saves (Client_RestoreGameServerRegion 0x00670F70
// reads it back at login).
const SettingRegionID = "GameServerRegionId"

// RegionID is a region alias's GameServerRegionId: the FNV-1 of the alias, in decimal ("eu" -> "1819748219", as the
// client saved it at a click on Europe; "na" -> "1668749442", computed the same way).
func RegionID(alias string) string { return strconv.FormatUint(uint64(content.AssetID(alias)), 10) }

// pingRegion is the lowest-latency alias of a connection's NLMP with "-any" cut ("" without one; ties to the smaller
// alias, as lobbyRegion).
func pingRegion(sites map[string]int64) string {
	if len(sites) == 0 {
		return ""
	}
	return gmshost.NormRegion(strings.TrimSuffix(lobbyRegion(sites), "-any"))
}

// WantedRegion is the region a creator wants a custom game in: NLMP's choice if it names one of regions (the server's,
// gmshost.Manager.Regions), else the stored GameServerRegionId if it is one of them, else home.
func WantedRegion(sites map[string]int64, settings map[string]string, home string, regions []string) string {
	if r := pingRegion(sites); r != "" {
		for _, x := range regions {
			if x == r {
				return r
			}
		}
	}
	if id := settings[SettingRegionID]; id != "" {
		for _, x := range regions {
			if RegionID(x) == id {
				return x
			}
		}
	}
	return home
}

// regionsOn is the home region and every region of this server ("" and nil: regions are off, or no match hosts).
func (s *Server) regionsOn() (string, []string) {
	if s.Hosts == nil {
		return "", nil
	}
	return s.Hosts.HomeRegion(), s.Hosts.Regions()
}

// customWant is the region a custom game's creator wants it in ("" for a practice group's game, or with regions off).
func (s *Server) customWant(r *Request, browsable bool) string {
	home, regions := s.regionsOn()
	if !browsable || home == "" {
		return ""
	}
	var settings map[string]string
	if a, ok := r.account(); ok {
		settings = a.Settings
	}
	return s.reachableWant(r, WantedRegion(r.Conn.Net.PingSites, settings, home, regions), home) // REGION5
}

// startHost starts the game's match host: a custom game with a wish in its region (StartIn), every other at home.
func (s *Server) startHost(spec gmshost.CreateGame, ev gmshost.Events, custom *customLobby) (*gmshost.Match, error) {
	if custom != nil && custom.want != "" {
		return s.Hosts.StartIn(spec, ev, gmshost.Want{Region: custom.want})
	}
	return s.Hosts.Start(spec, ev)
}

// userSettings is what getUserSettings answers: the stored settings, and with DefaultRegionHome and regions on the
// home region's GameServerRegionId for an account that never chose one (the stored settings stay as they are).
func (s *Server) userSettings(stored map[string]string) map[string]string {
	home, _ := s.regionsOn()
	if !DefaultRegionHome || home == "" || stored[SettingRegionID] != "" {
		return stored
	}
	out := maps.Clone(stored)
	if out == nil {
		out = map[string]string{}
	}
	out[SettingRegionID] = RegionID(home)
	return out
}

// LobbyRegion is lobby.state's region: the game runs in Got, not in Want where its creator wanted it, for Why
// (gmshost.FellBack*). The class words it by Why and draws nothing for a region it doesn't know.
type LobbyRegion struct {
	Want string `json:"want"`
	Got  string `json:"got"`
	Why  string `json:"why"`
}

// regionNotice is lobby.state's region for a match placed so (nil when the game runs where it was wanted).
func regionNotice(region, wanted, fellBack string) *LobbyRegion {
	if fellBack == "" || wanted == "" || region == wanted {
		return nil
	}
	return &LobbyRegion{Want: wanted, Got: region, Why: fellBack}
}

// regionReady takes a custom game's region from its match at gameReady (it doesn't change after): the browser's PSAS
// becomes the region its host runs in, and lobby.state gets the notice when that isn't where its creator wanted.
func (hg *hostedGame) regionReady(mt *gmshost.Match) {
	if hg.custom == nil {
		return
	}
	region, wanted, fellBack := mt.RegionPlacement()
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if region != "" {
		hg.custom.region = region
	}
	hg.custom.notice = regionNotice(region, wanted, fellBack)
}
