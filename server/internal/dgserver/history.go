package dgserver

// MHIST2: the match history's JSON, games.json (a page of matches, newest first) and game.json (one match with its
// statistics, sources and timeline), for the history page in the client's web view (web/history.go answers them).
// Every match is read from the store (MHIST1); player names are the personas' as they are now. The operator's
// --match-history-names off blanks every human's name except the asking player's own (the page's me=, which is
// Client.getLocalPlayerName(): not an authentication, the page has no session) and refuses other players' lists.
// Shapers, items, battle spells and spirit stones are named from the server's content; a bot is "Bot".

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
	"dawngate/server/internal/web"
)

// historyVersion is the format of games.json and game.json: a page checks it, and a change that breaks a page raises
// it.
const historyVersion = 1

// historyPage is games.json's page size without n; historyPageMax the most n asks for (more is clamped).
const (
	historyPage    = 20
	historyPageMax = 50
)

// historyAll is games.json's player= for everyone's games (also an empty or missing player=).
const historyAll = "all"

// historyBot is a bot seat's name.
const historyBot = "Bot"

// historyStore is what the history reads (*store.Store; the tests' too).
type historyStore interface {
	RecentMatches(before int64, limit int) ([]store.Match, error)
	MatchesOf(personaID int64, before int64, limit int) ([]store.Match, error)
	Match(id int64) (store.Match, bool, error)
	PersonaByName(name string) (store.Account, store.Persona, bool)
}

// historyGames is games.json.
type historyGames struct {
	Version    int   `json:"version"`
	ServerTime int64 `json:"serverTime"` // Unix seconds when it was made
	Names      bool  `json:"names"`      // the server shows player names (--match-history-names)
	// Player is the list's player (player=<name>, by the persona's name as it is now); null for everyone's games
	Player     *historyPlayer `json:"player"`
	Matches    []historyMatch `json:"matches"` // newest first
	More       bool           `json:"more"`    // older matches follow
	NextBefore int64          `json:"nextBefore"`
}

type historyPlayer struct {
	Name string `json:"name"`
}

// historyGame is game.json.
type historyGame struct {
	Version    int                `json:"version"`
	ServerTime int64              `json:"serverTime"`
	Names      bool               `json:"names"`
	Match      historyMatchDetail `json:"match"`
}

// historyHead is what a match says in the list and in game.json.
type historyHead struct {
	ID          int64  `json:"id"`       // the store's match id (home.json's matchId)
	Mode        string `json:"mode"`     // createGame's: "pvp", "custom", "practice"
	Kind        string `json:"kind"`     // "pvp", "custom", "practiceGroup", "practice"
	KindName    string `json:"kindName"` // "PvP match", "Custom game", "Practice group", "Practice match"
	Map         string `json:"map"`      // display name
	Level       string `json:"level"`
	Winner      int    `json:"winner"` // 1 or 2, 0 = no winner
	Reason      string `json:"reason"` // finishGame's: "guardian", "surrender", ...
	Started     int64  `json:"started"`
	Ended       int64  `json:"ended"` // Unix seconds
	DurationSec int64  `json:"durationSec"`
}

type historyMatch struct {
	historyHead
	Seats []historySeat `json:"seats"`
}

type historyMatchDetail struct {
	historyHead
	Seats []historySeatDetail `json:"seats"`
	// Timeline is the host's matchTimeline without "t", null when the host sent none
	Timeline json.RawMessage `json:"timeline"`
}

// historySeat is one seat in the list.
type historySeat struct {
	Seat     int    `json:"seat"` // the gameplay index (0-9)
	Team     int    `json:"team"` // 1 or 2
	Name     string `json:"name"` // the persona's name, "Bot", or "" (names off, or the persona is gone)
	Bot      bool   `json:"bot"`
	You      bool   `json:"you"`    // the asking player's seat (me=)
	Shaper   string `json:"shaper"` // the content's title, "" unknown
	Noun     uint32 `json:"noun"`
	Role     int    `json:"role"`     // 1 gladiator, 2 tactician, 3 hunter, 4 predator, 0 none
	RoleName string `json:"roleName"` // "Gladiator", ..., "" for none
	Won      bool   `json:"won"`
	Left     bool   `json:"left"`
	Kills    int64  `json:"kills"`
	Deaths   int64  `json:"deaths"`
	Assists  int64  `json:"assists"`
	CS       int64  `json:"cs"`
	Gold     int64  `json:"gold"`
	Level    int64  `json:"level"`
}

// historySeatDetail is one seat in game.json.
type historySeatDetail struct {
	historySeat
	Stats   map[string]int64 `json:"stats"`   // every stat the host reported ({} none)
	Sources []historySource  `json:"sources"` // damage and heal by source, the host's order ([] none)
}

// historySource is one of seatStats.sources with its name.
type historySource struct {
	Kind   string `json:"kind"` // basic, ability, item, spell, perk, role, other
	Slot   *int   `json:"slot,omitempty"`
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Damage int64  `json:"damage"`
	Heal   int64  `json:"heal"`
}

// historyKinds are store.Match.Kind's display words (blaze's resultKind).
var historyKinds = map[string]string{"pvp": "PvP match", "custom": "Custom game", "practiceGroup": "Practice group",
	"practice": "Practice match"}

// historyRoles are store.MatchSeat.Role's names (1 + the RolesTuning role).
var historyRoles = []string{"", "Gladiator", "Tactician", "Hunter", "Predator"}

// historyAbilities name an ability source by its slot (the contract: 0-3 Q/W/E/R, 4 the passive).
var historyAbilities = []string{"Q", "W", "E", "R", "Passive"}

// history answers games.json and game.json.
type history struct {
	st    historyStore
	c     *content.Content
	names bool
	now   func() time.Time
	logf  func(who, format string, a ...any)
	items map[int64]string // a match item's or battle spell's asset id (AssetID of "<name>.Item") -> its title
	perks map[int64]string // a spirit stone modifier's SPID (AssetID of its name) -> the stone's title
}

// newHistory reads the content's names once (it doesn't change while the server runs).
func newHistory(st historyStore, c *content.Content, names bool, now func() time.Time, logf func(who, format string, a ...any)) *history {
	h := &history{st: st, c: c, names: names, now: now, logf: logf, items: map[int64]string{}, perks: map[int64]string{}}
	if c == nil {
		return h
	}
	for _, it := range c.MatchItems {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			title = it.Name
		}
		h.items[int64(content.AssetID(it.Name+".Item"))] = title
	}
	for _, p := range c.Perks {
		title := perkTitle(p, c)
		for _, m := range p.Modifiers {
			id := int64(content.AssetID(m))
			if _, dup := h.perks[id]; !dup { // no modifier is two stones' in build 854's content; the first wins
				h.perks[id] = title
			}
		}
	}
	return h
}

// onOff is a setting's word in the log.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// perkTitle is a spirit stone's title ("Spiritstone of the Adventurer"), else its asset name.
func perkTitle(p content.Perk, c *content.Content) string {
	if it, ok := c.Item(p.CatalogID); ok && strings.TrimSpace(it.Title) != "" {
		return strings.TrimSpace(it.Title)
	}
	return p.Name
}

// Serve is web.HTTP.History.
func (h *history) Serve(kind string, q url.Values) (int, []byte) {
	var v any
	code, errCode, msg := http.StatusOK, "", ""
	switch kind {
	case web.HistoryGames:
		v, code, errCode, msg = h.games(q)
	case web.HistoryGame:
		v, code, errCode, msg = h.game(q)
	default:
		code, errCode, msg = http.StatusNotFound, "notFound", "no such data"
	}
	if code != http.StatusOK {
		return code, web.HistoryError(errCode, msg)
	}
	b, err := json.Marshal(v)
	if err != nil { // only plain types: can't happen
		return http.StatusInternalServerError, web.HistoryError("storeError", "the match history can't be read")
	}
	return code, b
}

// me is the asking player's persona id (me=), 0 when there's none or the name is unknown.
func (h *history) me(q url.Values) int64 {
	name := strings.TrimSpace(q.Get("me"))
	if name == "" {
		return 0
	}
	_, p, ok := h.st.PersonaByName(name)
	if !ok {
		return 0
	}
	return p.ID
}

// number reads a non-negative integer parameter (def when missing or empty); ok false for anything else.
func number(q url.Values, name string, def int64) (int64, bool) {
	s := strings.TrimSpace(q.Get(name))
	if s == "" {
		return def, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0
}

func (h *history) games(q url.Values) (any, int, string, string) {
	before, ok := number(q, "before", 0)
	if !ok {
		return nil, http.StatusBadRequest, "badRequest", "before must be a match id"
	}
	n, ok := number(q, "n", historyPage)
	if !ok || n < 1 {
		return nil, http.StatusBadRequest, "badRequest", "n must be a number from 1"
	}
	n = min(n, historyPageMax)
	me := h.me(q)
	d := historyGames{Version: historyVersion, ServerTime: h.now().Unix(), Names: h.names, Matches: []historyMatch{}}
	var ms []store.Match
	var err error
	if who := strings.TrimSpace(q.Get("player")); who == "" || who == historyAll {
		ms, err = h.st.RecentMatches(before, int(n)+1)
	} else {
		_, p, found := h.st.PersonaByName(who)
		if !found {
			return nil, http.StatusNotFound, "unknownPlayer", "no player of that name"
		}
		if !h.names && p.ID != me {
			return nil, http.StatusForbidden, "namesOff", "this server shows only your own games by name"
		}
		d.Player = &historyPlayer{Name: p.Name}
		ms, err = h.st.MatchesOf(p.ID, before, int(n)+1)
	}
	if err != nil {
		h.logf("http", "match history: %v", err)
		return nil, http.StatusInternalServerError, "storeError", "the match history can't be read"
	}
	if len(ms) > int(n) {
		ms, d.More = ms[:n], true
		d.NextBefore = ms[n-1].ID
	}
	for _, m := range ms {
		hm := historyMatch{historyHead: h.head(m), Seats: []historySeat{}}
		for _, s := range m.Seats {
			hm.Seats = append(hm.Seats, h.seat(s, me))
		}
		d.Matches = append(d.Matches, hm)
	}
	return d, http.StatusOK, "", ""
}

func (h *history) game(q url.Values) (any, int, string, string) {
	id, ok := number(q, "id", 0)
	if !ok || id < 1 {
		return nil, http.StatusBadRequest, "badRequest", "id must be a match id"
	}
	m, found, err := h.st.Match(id)
	if err != nil {
		h.logf("http", "match history: match %d: %v", id, err)
		return nil, http.StatusInternalServerError, "storeError", "the match history can't be read"
	}
	if !found {
		return nil, http.StatusNotFound, "notFound", "no such match"
	}
	me := h.me(q)
	d := historyGame{Version: historyVersion, ServerTime: h.now().Unix(), Names: h.names,
		Match: historyMatchDetail{historyHead: h.head(m), Seats: []historySeatDetail{}, Timeline: m.Timeline}}
	if len(d.Match.Timeline) == 0 || !json.Valid(d.Match.Timeline) {
		d.Match.Timeline = json.RawMessage("null")
	}
	for _, s := range m.Seats {
		sd := historySeatDetail{historySeat: h.seat(s, me), Stats: map[string]int64{}, Sources: []historySource{}}
		for k, v := range s.Stats {
			sd.Stats[k] = v
		}
		if len(s.Sources) > 0 {
			var src []gmshost.StatSource
			if err := json.Unmarshal(s.Sources, &src); err != nil {
				h.logf("http", "match history: match %d seat %d: sources: %v", id, s.Seat, err)
			}
			for _, x := range src {
				sd.Sources = append(sd.Sources, historySource{Kind: x.Kind, Slot: x.Slot, ID: x.ID,
					Name: h.sourceName(x, s.Role), Damage: x.Damage, Heal: x.Heal})
			}
		}
		d.Match.Seats = append(d.Match.Seats, sd)
	}
	return d, http.StatusOK, "", ""
}

func (h *history) head(m store.Match) historyHead {
	kindName := historyKinds[m.Kind]
	if kindName == "" {
		kindName = m.Kind
	}
	hd := historyHead{ID: m.ID, Mode: m.Mode, Kind: m.Kind, KindName: kindName, Map: blaze.LevelDisplay(m.Level),
		Level: m.Level, Reason: m.Reason, Started: m.Started.Unix(), Ended: m.Ended.Unix(), DurationSec: m.DurationMs / 1000}
	if m.Winner == 1 || m.Winner == 2 {
		hd.Winner = m.Winner
	}
	return hd
}

// seat is a seat with the names rule applied: a bot is "Bot"; a human's name shows when the server shows names or
// the seat is the asking player's (me, 0 = none).
func (h *history) seat(s store.MatchSeat, me int64) historySeat {
	hs := historySeat{Seat: s.Seat, Team: s.Team, Bot: s.Bot, You: !s.Bot && me != 0 && s.PersonaID == me,
		Shaper: blaze.ShaperTitle(s.Noun, h.c), Noun: s.Noun, Role: s.Role, Won: s.Won, Left: s.Left,
		Kills: s.Kills, Deaths: s.Deaths, Assists: s.Assists, CS: s.CS, Gold: s.Gold, Level: s.Level}
	if s.Role > 0 && s.Role < len(historyRoles) {
		hs.RoleName = historyRoles[s.Role]
	}
	switch {
	case s.Bot:
		hs.Name = historyBot
	case h.names || hs.You:
		hs.Name = s.Name
	}
	return hs
}

// sourceName names a source: an ability by its key, an item or battle spell by its asset id (an account catalog id
// too), a spirit stone by its modifier's SPID (its catalog id too), the role by the seat's role.
func (h *history) sourceName(x gmshost.StatSource, role int) string {
	switch x.Kind {
	case "basic":
		return "Basic attack"
	case "ability":
		if x.Slot != nil && *x.Slot >= 0 && *x.Slot < len(historyAbilities) {
			return historyAbilities[*x.Slot]
		}
		return "Ability"
	case "item", "spell":
		if t, ok := h.items[x.ID]; ok {
			return t
		}
		if it, ok := h.c.Item(uint32(x.ID)); ok && x.ID > 0 && x.ID <= 0xFFFFFFFF && strings.TrimSpace(it.Title) != "" {
			return strings.TrimSpace(it.Title)
		}
		if x.Kind == "spell" {
			return "Battle spell"
		}
		return "Item"
	case "perk":
		if t, ok := h.perks[x.ID]; ok {
			return t
		}
		if p, ok := h.c.Perk(uint32(x.ID)); ok && x.ID > 0 && x.ID <= 0xFFFFFFFF {
			return perkTitle(p, h.c)
		}
		return "Spirit stone"
	case "role":
		if role > 0 && role < len(historyRoles) {
			return historyRoles[role]
		}
		return "Role"
	}
	return "Other"
}
