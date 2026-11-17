package dgserver

// MHIST2: the match history's JSON, games.json (a page of matches, newest first) and game.json (one match with its
// statistics, sources and timeline), for the history page in the client's web view (web/history.go answers them).
// Every match is read from the store (MHIST1); player names are the personas' as they are now. The operator's
// --match-history-names off blanks every human's name except the asking player's own (the page's me=, which is
// Client.getLocalPlayerName(): not an authentication, the page has no session) and refuses other players' lists.
// Shapers, items, battle spells and spirit stones are named from the server's content; a bot is "Bot".

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/rating"
	"dawngate/server/internal/rules"
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
	MatchesWhere(personaID int64, before int64, limit int, keep func(store.Match) bool) ([]store.Match, error)
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
	// Watched is SPECSTATS': the match the asking player (me=) last watched as a custom game's spectator since the
	// server started (a spectator has no seat, so it isn't in the player's list); missing for none. The page's
	// ?latest=1 (the After Action Review's MATCH STATS) opens it when it is newer than the player's own newest match.
	Watched int64 `json:"watched,omitempty"`
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
	// Rules is a custom game's match rules as text ("All Random, Shop on Death"), left out for the standard game
	Rules string `json:"rules,omitempty"`
	// Type is the game's type for the list's filter (PROFILE1, historyType): "practice", "standard", "draft", "custom"
	Type string `json:"type"`
	// Unrated: a custom game its admin made unrated (M30); such a game is in no list unless unrated=1 asks for it
	Unrated bool `json:"unrated,omitempty"`
	// MapRules are the map's own rules the match played in a few words ("Race 20 · Lv 6 · Tuned"; MAPRULES5), left out
	// for none; MapUnrated: the map's victory (a kill race or a time limit) kept a game unrated that would otherwise
	// rate (a PvP or custom game its admin left Rated)
	MapRules   string `json:"mapRules,omitempty"`
	MapUnrated bool   `json:"mapUnrated,omitempty"`
	// Bindings are each team's destructions of enemy Bindings when the match played a Binding race (WINBIND-B: the
	// record's destroyed), the score of "won the Binding race 3-1"; left out otherwise
	Bindings *[2]int `json:"bindings,omitempty"`
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
	// M32 (MITEMS1): the final inventory, its non-empty slots in slot order ([] none), and the purchases in match
	// order ([] none)
	Items     []historyItem     `json:"items"`
	Purchases []historyPurchase `json:"purchases"`
}

// historyItem is one non-empty slot of a seat's final inventory (slots 0-5 items, 6-8 battle spells, 9-10
// consumables). ID is the item's asset id; Name its title ("Item", "Battle spell" when the content doesn't know it);
// Icon the item's file name for the page's itemicons/<Icon>.png ("" when unknown; the server doesn't check the file).
type historyItem struct {
	Slot int    `json:"slot"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// historyPurchase is one of seatStats.purchases with its item's name and icon: Kind buy, sell or undo.
type historyPurchase struct {
	Ms   int64  `json:"ms"`
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// matchItem is what the history knows of a match item by its asset id: its file name (MatchItem.Name, the icon's) and
// its title.
type matchItem struct {
	name, title string
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

// The game types of games.json's type= and of a match's "type" (PROFILE1, the user's request of 2026-11-12): practice
// (a practice match or a practice group's), and for the others MMR1's rating type (rating.ModeOf): standard (PvP, or
// a custom game with the default rules), draft (exactly the draft preset), custom (any other rules). historyTypeAll
// (also an empty or missing type=) is every type.
const (
	historyTypeAll      = "all"
	historyTypePractice = "practice"
)

// historyTypes are type='s known values.
var historyTypes = map[string]bool{historyTypeAll: true, historyTypePractice: true, rating.Standard: true,
	rating.Draft: true, rating.Custom: true}

// historyType is a match's game type (historyTypes without "all").
func historyType(m store.Match) string {
	return blaze.GameType(m) // PROFILE2: the Profile's tabs take the same types
}

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
	items map[int64]matchItem // a match item's or battle spell's asset id (AssetID of "<name>.Item") -> its name and title
	perks map[int64]string    // a spirit stone modifier's SPID (AssetID of its name) -> the stone's title
	// watched is the match a persona last watched as a spectator, 0 none (SPECSTATS: blaze.Server.WatchedMatch; nil: none)
	watched func(persona int64) int64
}

// newHistory reads the content's names once (it doesn't change while the server runs).
func newHistory(st historyStore, c *content.Content, names bool, now func() time.Time, logf func(who, format string, a ...any)) *history {
	h := &history{st: st, c: c, names: names, now: now, logf: logf, items: map[int64]matchItem{}, perks: map[int64]string{}}
	if c == nil {
		return h
	}
	for _, it := range c.MatchItems {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			title = it.Name
		}
		h.items[int64(content.AssetID(it.Name+".Item"))] = matchItem{name: it.Name, title: title}
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

// leaderboardNote and leaderboardGames are the start's Leaderboard line (RATING1): what it ranks by and what it counts.
func leaderboardNote(rating bool) string {
	if rating {
		return "ranked by skill rating"
	}
	return "ranked by wins"
}

func leaderboardGames(rating bool) string {
	if rating {
		return "rated games (balanced PvP and custom games)"
	}
	return "PvP and custom games"
}

// profileRatingNote is the start's profile box line (RATINGSHOW1): whether players see their rating, and from when.
func profileRatingNote(show bool, minGames int) string {
	if show {
		return fmt.Sprintf("each player's skill rating shown from %d rated games", minGames)
	}
	return "no rating shown"
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
	typ := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if typ == "" {
		typ = historyTypeAll
	}
	if !historyTypes[typ] {
		return nil, http.StatusBadRequest, "badRequest", "type must be all, practice, standard, draft or custom"
	}
	unrated, ok := number(q, "unrated", 0)
	if !ok || unrated > 1 {
		return nil, http.StatusBadRequest, "badRequest", "unrated must be 0 or 1"
	}
	// PROFILE1 (the user's decision of 2026-11-12): a custom game its admin made Unrated is in no list (a game that
	// simply wasn't rated, as with uneven teams or bots, is); unrated=1 keeps it (the page's ?latest=1: the review of
	// an unrated game opens that game). The filter runs in the store's scan, so every page is full and more is right.
	keep := func(m store.Match) bool {
		return (unrated == 1 || !m.Unrated) && (typ == historyTypeAll || historyType(m) == typ)
	}
	me := h.me(q)
	d := historyGames{Version: historyVersion, ServerTime: h.now().Unix(), Names: h.names, Matches: []historyMatch{}}
	if me != 0 && h.watched != nil {
		d.Watched = h.watched(me)
	}
	var ms []store.Match
	var err error
	if who := strings.TrimSpace(q.Get("player")); who == "" || who == historyAll {
		ms, err = h.st.MatchesWhere(0, before, int(n)+1, keep)
	} else {
		_, p, found := h.st.PersonaByName(who)
		if !found {
			return nil, http.StatusNotFound, "unknownPlayer", "no player of that name"
		}
		if !h.names && p.ID != me {
			return nil, http.StatusForbidden, "namesOff", "this server shows only your own games by name"
		}
		d.Player = &historyPlayer{Name: p.Name}
		ms, err = h.st.MatchesWhere(p.ID, before, int(n)+1, keep)
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
		sd := historySeatDetail{historySeat: h.seat(s, me), Stats: map[string]int64{}, Sources: []historySource{},
			Items: []historyItem{}, Purchases: []historyPurchase{}}
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
		h.seatItems(&sd, s, id)
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
		Level: m.Level, Reason: m.Reason, Started: m.Started.Unix(), Ended: m.Ended.Unix(), DurationSec: m.DurationMs / 1000,
		Type: historyType(m), Unrated: m.Unrated}
	if m.Winner == 1 || m.Winner == 2 {
		hd.Winner = m.Winner
	}
	if m.MapRules != "" { // MAPRULES5
		if r, err := store.ParseMapRules(m.MapRules); err == nil {
			hd.MapRules = blaze.PlayedRulesWords(r)
			hd.Bindings = r.Destroyed // WINBIND-B
		} else {
			hd.MapRules = "Map rules"
		}
		hd.MapUnrated = m.MapVictory() && !m.Unrated && (m.Kind == "pvp" || m.Kind == "custom")
	}
	if m.Rules != "" { // RULES1
		if c, err := rules.ParseJSON([]byte(m.Rules)); err == nil {
			hd.Rules = rules.Describe(c)
		} else {
			hd.Rules = "Custom rules"
		}
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

// seatItems fills a seat's items and purchases from the stored inventory and purchases (M32); a list that doesn't
// decode leaves its field [] with a log line, as sources.
func (h *history) seatItems(sd *historySeatDetail, s store.MatchSeat, match int64) {
	if len(s.Inventory) > 0 {
		var inv []int64
		if err := json.Unmarshal(s.Inventory, &inv); err != nil {
			h.logf("http", "match history: match %d seat %d: inventory: %v", match, s.Seat, err)
			inv = nil
		}
		for slot, id := range inv {
			if id != 0 {
				name, icon := h.itemName(id, slot >= 6 && slot <= 8)
				sd.Items = append(sd.Items, historyItem{Slot: slot, ID: id, Name: name, Icon: icon})
			}
		}
	}
	if len(s.Purchases) > 0 {
		var buys []gmshost.ItemEvent
		if err := json.Unmarshal(s.Purchases, &buys); err != nil {
			h.logf("http", "match history: match %d seat %d: purchases: %v", match, s.Seat, err)
			buys = nil
		}
		for _, b := range buys {
			name, icon := h.itemName(b.ID, false)
			sd.Purchases = append(sd.Purchases, historyPurchase{Ms: b.Ms, Kind: b.Kind, ID: b.ID, Name: name, Icon: icon})
		}
	}
}

// iconName is the pattern of an icon's file name (the page puts it into a path: itemicons/<icon>.png).
var iconName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// itemName is a match item's title and icon name by its asset id; an item the content doesn't know is "Item" (in a
// battle spell's slot "Battle spell") without an icon.
func (h *history) itemName(id int64, spell bool) (name, icon string) {
	if it, ok := h.items[id]; ok {
		if iconName.MatchString(it.name) {
			icon = it.name
		}
		return it.title, icon
	}
	if spell {
		return "Battle spell", ""
	}
	return "Item", ""
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
		if it, ok := h.items[x.ID]; ok {
			return it.title
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
