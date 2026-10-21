// Package store keeps the server's accounts, personas and player content (roadmap B5, B7a).
//
// Login accepts any credentials: the first login with an email address creates the account and one
// persona on the fly. Ids are handed out in order from fixed bases, so a fresh store gives the first
// account the same ids every time.
//
// Persistence (ADR-4): a store opened with Open keeps everything in an SQLite database (dgserver:
// <data>\accounts.db; modernc.org/sqlite, pure Go) with a versioned schema (schema.go: a schema_version
// table and migrations applied at Open), WAL journal, synchronous writes and one connection, the one
// writer. Every method runs under the store's lock and in one transaction, reads the account from the
// database and returns copies; Update and UpdatePair write only the parts of an account that changed.
// The first Open of a folder that still has the JSON store of B7a-B13b (accounts.json) imports it and
// renames it to accounts.json.imported (legacy.go). New keeps a store in an in-memory database.
package store

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Id bases. Blaze ids are int64; the persona id doubles as the Blaze user id (BUID), as in other
// BlazeSDK 3.x emulators.
const (
	FirstUserID    int64 = 1000000001
	FirstPersonaID int64 = 2000000001
)

// MaxPersonaName is the longest persona name we make up from an email address.
const MaxPersonaName = 16

// FlagFTUEDone is bit 0 of an account's Flags (BGABasicData.FLAG): the player has completed the
// first-time-user experience, so PLAY opens the normal play screen instead of the Practice-only one
// (B7a: cGameEntryManager vtable +0xF0, 0x006816B0, returns account data +0xD10 & 1, and
// SetClientBasicInfo 0x00ACE210 stores FLAG there).
const FlagFTUEDone uint64 = 1

// Persona is one persona (display name) of an account.
type Persona struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Account is one user account and its player content.
type Account struct {
	UserID    int64     `json:"userID"`
	Email     string    `json:"email"`
	Personas  []Persona `json:"personas"`
	Created   time.Time `json:"created"`
	LastLogin time.Time `json:"lastLogin"` // the previous login (zero on the first one)

	// Player content (B7a). Granted is set once the new-account content (inventory, avatar,
	// currencies) has been given; an account created while the server had no content file gets it at
	// its next login with content.
	Granted bool   `json:"granted"`
	Level   uint32 `json:"level"`  // BGABasicData.ALEV (accounts start at 1)
	XP      uint32 `json:"xp"`     // BGABasicData.ALXP
	Flags   uint64 `json:"flags"`  // BGABasicData.FLAG; FlagFTUEDone
	Avatar  uint32 `json:"avatar"` // BGABasicData.AVTR: the avatar item's catalogID
	// Currencies: EC earned ("Destiny"), PC paid ("Waypoints"), CC (unknown; GetBalanceResponse CCBL).
	EC uint32 `json:"ec"`
	PC uint32 `json:"pc"`
	CC uint32 `json:"cc"`
	// Items is the inventory: catalogID -> count.
	Items map[uint32]uint32 `json:"items"`
	// Settings is the client's user settings (getUserSettings/saveUserSettings USET).
	Settings map[string]string `json:"settings"`
	// UserInfo is the user info attribute (UserSessions::setUserInfoAttribute; UserSessionExtendedData
	// UATT). The client sets bit 62 when the player dismisses the main menu's news overlay (B8).
	UserInfo uint64 `json:"userInfo"`
	// Stats holds stat values by group and name (Stats getStatsByGroupAsync); missing ones are "0".
	Stats map[string]map[string]string `json:"stats,omitempty"`

	// B7c: perk pages (BattlegroundsAccounts getPerkPageData/updatePerkPage, getCompleteData's PERK),
	// in page id order.
	PerkPages []PerkPage `json:"perkPages,omitempty"`
	// Commerce is the commerce profile the purchases add to (BGEcommerce getCommerceProfile).
	Commerce Commerce `json:"commerce,omitzero"`
	// FirstWinDay is the UTC day ("2006-01-02") of the last first-win-of-the-day bonus; "" = never.
	FirstWinDay string `json:"firstWinDay,omitempty"`

	// B7d: the profile's shaper and role history (Stats ShaperStats, ShaperHistoryStats, RoleHistoryStats).
	History History `json:"history,omitzero"`

	// B13b: the account's association lists (AssociationLists getLists, addUsersToList, removeUsersFromList).
	Lists Lists `json:"lists,omitzero"`
}

// ListMember is one member of an association list (B13b): a persona by its id (the Blaze user id, ListMemberId
// BLID), the name it had when it was added (PNAM) and when that was (ListMemberInfo TIME).
type ListMember struct {
	ID    int64     `json:"id"`
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
}

// Lists are an account's association lists (B13b; server/internal/blaze/associationlists.go keeps them): its
// friends (mutual: each friend has this account's persona in its own Friends), the recent players (the other
// humans of its last games) and the users it blocked. Each is in the order the members were added, oldest first.
type Lists struct {
	Friends []ListMember `json:"friends,omitempty"`
	Recent  []ListMember `json:"recentPlayers,omitempty"`
	Blocked []ListMember `json:"blocked,omitempty"`
}

// Clone returns a deep copy of l.
func (l Lists) Clone() Lists {
	return Lists{Friends: slices.Clone(l.Friends), Recent: slices.Clone(l.Recent), Blocked: slices.Clone(l.Blocked)}
}

// IndexOf returns the index of the member with persona id in members, or -1.
func IndexOf(members []ListMember, id int64) int {
	return slices.IndexFunc(members, func(m ListMember) bool { return m.ID == id })
}

// History is an account's game history by shaper and by role (B7d; server/internal/progress fills it after
// each hosted game, server/internal/blaze/stats.go answers the profile's three keyed stat groups from it).
type History struct {
	// Shapers holds every game ever played, by the shaper's catalogID (Stats group ShaperStats, which the
	// client asks for all time: STAT_PERIOD_ALL_TIME, one period).
	Shapers map[uint32]GamesWon `json:"shapers,omitempty"`
	// Days holds the last days' games, one entry per UTC day with games, oldest first (ShaperHistoryStats
	// and RoleHistoryStats, which the client asks for by day: STAT_PERIOD_DAILY, 7 periods).
	Days []HistoryDay `json:"days,omitempty"`
	// Ranked counts the games the main menu's Leaderboard ranks by (LBMIN): PvP (matchmade) and custom games, never
	// practice games (solo or a practice group). Counted from the build that added it; earlier games aren't in it.
	Ranked GamesWon `json:"ranked,omitzero"`
}

// GamesWon is a count of games and of the wins among them.
type GamesWon struct {
	Games uint32 `json:"games"`
	Wins  uint32 `json:"wins"`
}

// HistoryDay is one UTC day of games.
type HistoryDay struct {
	Day     string               `json:"day"`               // "2006-01-02"
	Shapers map[uint32]ShaperDay `json:"shapers,omitempty"` // by the shaper's catalogID
	Roles   map[uint8]GamesWon   `json:"roles,omitempty"`   // by role: 1 gladiator, 2 tactician, 3 hunter, 4 predator
	Ranked  GamesWon             `json:"ranked,omitzero"`   // the day's part of History.Ranked (the daily ranking)
}

// ShaperDay is one shaper's games of one day: the eight values of a ShaperHistoryStats row, as totals (the
// client averages them over Games).
type ShaperDay struct {
	Games   uint32 `json:"games"`
	Wins    uint32 `json:"wins"`
	Kills   uint32 `json:"kills"`
	Deaths  uint32 `json:"deaths"`
	Assists uint32 `json:"assists"`
	Creeps  uint32 `json:"creeps"`  // creep score ("CS")
	Gold    uint32 `json:"gold"`    // "Vim", Dawngate's gold
	Seconds uint32 `json:"seconds"` // match time
}

// Clone returns a deep copy of h.
func (h History) Clone() History {
	c := History{Shapers: maps.Clone(h.Shapers), Ranked: h.Ranked}
	if h.Days != nil {
		c.Days = make([]HistoryDay, len(h.Days))
		for i, d := range h.Days {
			c.Days[i] = HistoryDay{Day: d.Day, Shapers: maps.Clone(d.Shapers), Roles: maps.Clone(d.Roles), Ranked: d.Ranked}
		}
	}
	return c
}

// Commerce is a Blaze::BGEcommerce::CommerceProfile's stored part: what the account spent and how many
// purchases it made, per currency (ASEC/ASPC, CTEC/CTPC; R11: the client keeps these four).
type Commerce struct {
	SpentEC uint32 `json:"spentEC,omitempty"`
	SpentPC uint32 `json:"spentPC,omitempty"`
	CountEC uint32 `json:"countEC,omitempty"`
	CountPC uint32 `json:"countPC,omitempty"`
}

// PerkPage is one stored perk page (Blaze::BattlegroundsAccounts::BGACompletePerkPage; R11's reading in
// docs/ACCOUNT_CONTENT.md §7).
type PerkPage struct {
	ID      uint32      `json:"id"`      // SUMM.PID: the page id, unique per account
	Name    string      `json:"name"`    // SUMM.NAME
	Version uint32      `json:"version"` // SUMM.VERS: the page's save version, >= 1
	Status  uint8       `json:"status"`  // SUMM.STAT: echoed by the client (1 for a new page; meaning open)
	Shapes  []PerkShape `json:"shapes,omitempty"`
}

// PerkShape is one placed perk shape (BGAPerkPageShape).
type PerkShape struct {
	ID       uint32    `json:"id"`       // PID: the PerkShape item's catalogID
	Cell     uint8     `json:"cell"`     // GIDX: row * 4 + column on the 4 x 4 board
	Rotation uint8     `json:"rotation"` // ROT: quarter turns, 0-3
	Gems     []PerkGem `json:"gems,omitempty"`
}

// PerkGem is one gem in a shape's socket (BGAPerkPageGem).
type PerkGem struct {
	ID     uint32 `json:"id"`     // GID: the Gem item's catalogID
	Socket uint8  `json:"socket"` // SIDX: index into PerkShape.socket
}

// Page returns the account's perk page with this id.
func (a *Account) Page(id uint32) *PerkPage {
	for i := range a.PerkPages {
		if a.PerkPages[i].ID == id {
			return &a.PerkPages[i]
		}
	}
	return nil
}

// AddPage appends a new empty perk page ("Page <n>", the next id after the highest, version 1, status 1)
// and returns it.
func (a *Account) AddPage() PerkPage {
	var id uint32
	for _, p := range a.PerkPages {
		id = max(id, p.ID)
	}
	p := PerkPage{ID: id + 1, Name: "Page " + strconv.Itoa(len(a.PerkPages)+1), Version: 1, Status: 1}
	a.PerkPages = append(a.PerkPages, p)
	return p
}

// ClonePages returns a deep copy of pages.
func ClonePages(pages []PerkPage) []PerkPage {
	if pages == nil {
		return nil
	}
	out := make([]PerkPage, len(pages))
	for i, p := range pages {
		out[i] = p
		if p.Shapes != nil {
			out[i].Shapes = make([]PerkShape, len(p.Shapes))
			for j, s := range p.Shapes {
				out[i].Shapes[j] = s
				out[i].Shapes[j].Gems = append([]PerkGem(nil), s.Gems...)
			}
		}
	}
	return out
}

// PersonaName makes a persona name from an email address: the part before '@', letters, digits, '_'
// and '-' only, at most MaxPersonaName characters; "Player" if nothing is left.
func PersonaName(email string) string {
	local, _, _ := strings.Cut(strings.TrimSpace(email), "@")
	var b strings.Builder
	for _, r := range local {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-') {
			b.WriteRune(r)
			if b.Len() == MaxPersonaName {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "Player"
	}
	return b.String()
}
