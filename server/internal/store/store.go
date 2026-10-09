// Package store keeps the server's accounts, personas and player content (roadmap B5, B7a).
//
// Login accepts any credentials: the first login with an email address creates the account and one
// persona on the fly. Ids are handed out in order from fixed bases, so a fresh store gives the first
// account the same ids every time.
//
// Persistence (B7a, ADR-1: stdlib only): a store opened with Open keeps everything in one JSON file
// (dgserver: server\data\accounts.json). Every change rewrites the whole file under the store's lock:
// the new state goes to a temporary file in the same folder, which is synced and then renamed over the
// old one, so a crash leaves either the old or the new file, never half of one. Requests are served
// concurrently, but the lock serializes all reads and writes of the state, and every method returns
// copies. A file that doesn't parse stops Open instead of being overwritten. At the expected scale
// (dozens of players, a handful of changes per session) rewriting the file is cheap; a database would
// be the next step if that changes.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	c := History{Shapers: maps.Clone(h.Shapers)}
	if h.Days != nil {
		c.Days = make([]HistoryDay, len(h.Days))
		for i, d := range h.Days {
			c.Days[i] = HistoryDay{Day: d.Day, Shapers: maps.Clone(d.Shapers), Roles: maps.Clone(d.Roles)}
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

// Store holds the accounts.
type Store struct {
	mu          sync.Mutex
	path        string // the JSON file; "" = memory only
	byEmail     map[string]*Account
	nextUser    int64
	nextPersona int64
}

// fileFormat names the accounts file's layout.
const fileFormat = "dawngate-accounts-1"

type file struct {
	Format        string    `json:"format"`
	NextUserID    int64     `json:"nextUserID"`
	NextPersonaID int64     `json:"nextPersonaID"`
	Accounts      []Account `json:"accounts"`
}

// New returns an empty store that lives in memory only.
func New() *Store {
	return &Store{byEmail: map[string]*Account{}, nextUser: FirstUserID, nextPersona: FirstPersonaID}
}

// Open returns the store kept in the JSON file path, loading it if it exists. The file is written on
// every change; its folder is created then.
func Open(path string) (*Store, error) {
	s := New()
	s.path = path
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("store: %s doesn't parse (%v); fix or move it away, it won't be overwritten", path, err)
	}
	if f.Format != fileFormat {
		return nil, fmt.Errorf("store: %s has format %q, want %q", path, f.Format, fileFormat)
	}
	s.nextUser, s.nextPersona = max(f.NextUserID, FirstUserID), max(f.NextPersonaID, FirstPersonaID)
	for i := range f.Accounts {
		a := f.Accounts[i]
		key := emailKey(a.Email)
		if _, dup := s.byEmail[key]; dup {
			return nil, fmt.Errorf("store: %s has the email %q twice", path, a.Email)
		}
		s.byEmail[key] = &a
		s.nextUser = max(s.nextUser, a.UserID+1)
		for _, p := range a.Personas {
			s.nextPersona = max(s.nextPersona, p.ID+1)
		}
	}
	return s, nil
}

// Path returns the file the store is kept in ("" = memory only).
func (s *Store) Path() string { return s.path }

// save writes the whole store to its file (the caller holds s.mu).
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	f := file{Format: fileFormat, NextUserID: s.nextUser, NextPersonaID: s.nextPersona, Accounts: []Account{}}
	for _, a := range s.byEmail {
		f.Accounts = append(f.Accounts, *a)
	}
	sort.Slice(f.Accounts, func(i, j int) bool { return f.Accounts[i].UserID < f.Accounts[j].UserID })
	raw, err := json.MarshalIndent(&f, "", " ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(raw, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path) // replaces the old file, on Windows too
	}
	if err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("store: saving %s: %v", s.path, err)
	}
	return nil
}

func emailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Login returns the account for email (compared case-insensitively), creating it with one persona if
// it doesn't exist. The password is not checked. first reports whether the account was just created.
// The returned Account is a copy, as it was before this login (LastLogin = the previous login). An
// error means the change couldn't be saved; the account is valid in memory anyway.
func (s *Store) Login(email string, now time.Time) (acct Account, first bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := emailKey(email)
	a, ok := s.byEmail[key]
	if !ok {
		a = &Account{UserID: s.nextUser, Email: strings.TrimSpace(email), Created: now, Level: 1}
		s.nextUser++
		a.Personas = []Persona{{ID: s.nextPersona, Name: s.uniqueName(PersonaName(email))}}
		s.nextPersona++
		s.byEmail[key] = a
		first = true
	}
	acct = copyAccount(a)
	a.LastLogin = now
	return acct, first, s.save()
}

// Persona returns the account's persona with this name (case-insensitive).
func (a Account) Persona(name string) (Persona, bool) {
	for _, p := range a.Personas {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Persona{}, false
}

// PersonaByID returns the persona with this id and its account.
func (s *Store) PersonaByID(id int64) (Account, Persona, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.byEmail {
		for _, p := range a.Personas {
			if p.ID == id {
				return copyAccount(a), p, true
			}
		}
	}
	return Account{}, Persona{}, false
}

// PersonaByName returns the persona with this display name (compared case-insensitively; names are unique,
// see uniqueName) and its account (A5b: UserSessions::lookupUser, the PvP lobby's invite by name).
func (s *Store) PersonaByName(name string) (Account, Persona, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	for _, a := range s.byEmail {
		for _, p := range a.Personas {
			if strings.EqualFold(p.Name, name) {
				return copyAccount(a), p, true
			}
		}
	}
	return Account{}, Persona{}, false
}

// Account returns the account with this user id.
func (s *Store) Account(userID int64) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.byUser(userID); a != nil {
		return copyAccount(a), true
	}
	return Account{}, false
}

// ErrNoAccount is returned by Update for an unknown user id.
var ErrNoAccount = errors.New("store: no such account")

// Update changes the account with this user id through fn and saves the store. fn gets the stored
// account (maps allocated) and must not keep it. It returns a copy of the changed account. An error
// other than ErrNoAccount means the change couldn't be saved (it is kept in memory).
func (s *Store) Update(userID int64, fn func(a *Account)) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.byUser(userID)
	if a == nil {
		return Account{}, ErrNoAccount
	}
	if a.Items == nil {
		a.Items = map[uint32]uint32{}
	}
	if a.Settings == nil {
		a.Settings = map[string]string{}
	}
	if a.Stats == nil {
		a.Stats = map[string]map[string]string{}
	}
	fn(a)
	return copyAccount(a), s.save()
}

// UpdatePair changes two accounts through fn under one lock and one save (B13b: a friendship is added to or
// removed from both accounts at once). fn gets the stored accounts (maps allocated, as Update's; the same pointer
// twice if the ids are equal) and must not keep them. It returns copies of both; ErrNoAccount if either is unknown.
func (s *Store) UpdatePair(userA, userB int64, fn func(a, b *Account)) (Account, Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, b := s.byUser(userA), s.byUser(userB)
	if a == nil || b == nil {
		return Account{}, Account{}, ErrNoAccount
	}
	for _, x := range []*Account{a, b} {
		if x.Items == nil {
			x.Items = map[uint32]uint32{}
		}
		if x.Settings == nil {
			x.Settings = map[string]string{}
		}
		if x.Stats == nil {
			x.Stats = map[string]map[string]string{}
		}
	}
	fn(a, b)
	return copyAccount(a), copyAccount(b), s.save()
}

func (s *Store) byUser(userID int64) *Account {
	for _, a := range s.byEmail {
		if a.UserID == userID {
			return a
		}
	}
	return nil
}

func copyAccount(a *Account) Account {
	c := *a
	c.Personas = append([]Persona(nil), a.Personas...)
	c.Items = maps.Clone(a.Items)
	c.Settings = maps.Clone(a.Settings)
	c.PerkPages = ClonePages(a.PerkPages)
	c.History = a.History.Clone()
	c.Lists = a.Lists.Clone()
	if a.Stats != nil {
		c.Stats = make(map[string]map[string]string, len(a.Stats))
		for g, m := range a.Stats {
			c.Stats[g] = maps.Clone(m)
		}
	}
	return c
}

// uniqueName appends a number to name while another account's persona already has it.
func (s *Store) uniqueName(name string) string {
	taken := map[string]bool{}
	for _, a := range s.byEmail {
		for _, p := range a.Personas {
			taken[strings.ToLower(p.Name)] = true
		}
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; ; i++ {
		suffix := strconv.Itoa(i)
		base := name
		if len(base)+len(suffix) > MaxPersonaName {
			base = base[:MaxPersonaName-len(suffix)]
		}
		if c := base + suffix; !taken[strings.ToLower(c)] {
			return c
		}
	}
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
