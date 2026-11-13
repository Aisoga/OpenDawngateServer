// Package rules is the match rules of a custom game: a fixed catalog of categories, each with exactly one active
// option (its default is the standard game), the named presets, the checks a combination must pass before a match
// host gets it, and the readable text for the game browser, the lobby and the line at the match start.
//
// The categories combine freely; a combination that makes no sense, or that the bots can't play while the lobby's
// bots are on, is refused by Check's findings. The server decides: a client only ever proposes a combination, and the
// host only ever gets one that passed. Bots are a setup option, not a rule: each option says whether it supports
// bots, and a combination allows bots only if every option in it does.
package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The categories' ids, in the order the catalog, the wire and the texts use.
const (
	Pick       = "pick"       // how a player gets a shaper
	ShaperPool = "shaperPool" // which shapers may be played
	Shop       = "shop"       // when a player may shop
	ItemPool   = "itemPool"   // which items may be bought
)

// Option is one choice of a category.
type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"` // shown in the lobby, the browser's prefix and the line at the start
	// Bots: the bots can play a match with this option. Every default option can; the others until the bots are taught
	// them.
	Bots bool `json:"bots"`
	// Host is the match host feature the option needs beyond the rules every rules host plays ("" = none): a host
	// whose hello doesn't name it would misplay the option, so Check refuses it there. Not on the wire.
	Host string `json:"-"`
}

// HostRules2 is the host feature of the second set of options (gmshost.FeatureRules2): the item pool "lightning" and
// the picks "army" and "clone". An older host takes an unknown item pool as "all items" and keeps a shaper once per
// team.
const HostRules2 = "rules2"

// Category is one group of options, exactly one of them active.
type Category struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Default string   `json:"default"` // the standard game's option
	Options []Option `json:"options"`
}

// catalog is the fixed catalog. The pools are presets; their contents are the game data's (pools.go). A pool's Bots is
// true: the bots draw from the pool, and Check counts whether it holds enough shapers they play.
var catalog = []Category{
	{ID: Pick, Name: "Shaper pick", Default: "blind", Options: []Option{
		{ID: "blind", Name: "Blind pick", Bots: true},
		{ID: "random", Name: "All Random", Bots: true}, // the bots already draw shuffled after the humans
		{ID: "draft", Name: "Draft"},
		// ARMY1: a vote in the lobby (vote.go); a team holds one shaper several times, which needs a newer host
		{ID: PickArmy, Name: "Army Pick", Host: HostRules2},
		{ID: PickClone, Name: "Clone Pick", Host: HostRules2},
	}},
	{ID: ShaperPool, Name: "Shaper pool", Default: "all", Options: []Option{
		{ID: "all", Name: "All shapers", Bots: true},
		{ID: "melee", Name: "Melee shapers", Bots: true},
		{ID: "ranged", Name: "Ranged shapers", Bots: true},
		{ID: "tank", Name: "Tanks", Bots: true}, // RULES3: the archetypes (pools.go)
		{ID: "bruiser", Name: "Bruisers", Bots: true},
		{ID: "carry", Name: "Carries", Bots: true},
		{ID: "mage", Name: "Mages", Bots: true},
		{ID: "support", Name: "Supports", Bots: true},
	}},
	{ID: Shop, Name: "Shop access", Default: "normal", Options: []Option{
		{ID: "normal", Name: "Normal shop", Bots: true},
		{ID: "onDeath", Name: "Shop on Death"},
	}},
	// RULES4a: the match host applies Shop access and the item pools (gms/core/rules/shop.h, which must know every id
	// here): noLegendary = Basic and Advanced stat items, basics = the six Basic ones, lightning (LIGHTNING1) = Time,
	// Energy, Chaos, Potency and Resonance; potions, flasks and battle spells are always allowed.
	{ID: ItemPool, Name: "Item pool", Default: "all", Options: []Option{
		{ID: "all", Name: "All items", Bots: true},
		{ID: "noLegendary", Name: "No legendary items"},
		{ID: "basics", Name: "Basic items only"},
		{ID: "lightning", Name: "Lightning items only", Host: HostRules2},
	}},
}

// Categories returns the catalog (a copy).
func Categories() []Category {
	out := make([]Category, len(catalog))
	for i, c := range catalog {
		c.Options = append([]Option(nil), c.Options...)
		out[i] = c
	}
	return out
}

// category returns the category id.
func category(id string) (Category, bool) {
	for _, c := range catalog {
		if c.ID == id {
			return c, true
		}
	}
	return Category{}, false
}

// option returns category cat's option id.
func option(cat, id string) (Option, bool) {
	c, ok := category(cat)
	if !ok {
		return Option{}, false
	}
	for _, o := range c.Options {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// Combination is one option per category, by id. Its JSON is the wire's (the lobby, the match host's startGame, the
// match history): the four categories in catalog order.
type Combination struct {
	Pick       string `json:"pick"`
	ShaperPool string `json:"shaperPool"`
	Shop       string `json:"shop"`
	ItemPool   string `json:"itemPool"`
}

// Standard is the standard game: every category's default.
func Standard() Combination {
	var c Combination
	for _, cat := range catalog {
		c.set(cat.ID, cat.Default)
	}
	return c
}

// Get returns category cat's option ("" for an unknown category).
func (c Combination) Get(cat string) string {
	switch cat {
	case Pick:
		return c.Pick
	case ShaperPool:
		return c.ShaperPool
	case Shop:
		return c.Shop
	case ItemPool:
		return c.ItemPool
	}
	return ""
}

func (c *Combination) set(cat, v string) {
	switch cat {
	case Pick:
		c.Pick = v
	case ShaperPool:
		c.ShaperPool = v
	case Shop:
		c.Shop = v
	case ItemPool:
		c.ItemPool = v
	}
}

// IsDefault reports whether every category has its default option (an empty category counts as its default).
func (c Combination) IsDefault() bool {
	for _, cat := range catalog {
		if v := c.Get(cat.ID); v != "" && v != cat.Default {
			return false
		}
	}
	return true
}

// Normal returns c with every empty category set to its default.
func (c Combination) Normal() Combination {
	for _, cat := range catalog {
		if c.Get(cat.ID) == "" {
			c.set(cat.ID, cat.Default)
		}
	}
	return c
}

// Validate refuses an option id the catalog doesn't have (an empty one is the default).
func (c Combination) Validate() error {
	for _, cat := range catalog {
		if v := c.Get(cat.ID); v != "" {
			if _, ok := option(cat.ID, v); !ok {
				return fmt.Errorf("rules: %s has no option %q", cat.ID, clip(v))
			}
		}
	}
	return nil
}

// Preset is a named combination. Choosing one fills the categories, which the lobby's admin can still change.
type Preset struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Options Combination `json:"options"`
}

var presets = []Preset{
	{ID: "standard", Name: "Standard", Options: Standard()},
	{ID: "aram", Name: "ARAM", Options: with(Standard(), Pick, "random", Shop, "onDeath")},
	{ID: "allRandom", Name: "All Random", Options: with(Standard(), Pick, "random")},
	{ID: "draft", Name: "Draft", Options: with(Standard(), Pick, "draft")}, // M30: the Draft rating's type (internal/rating ModeOf)
}

func with(c Combination, kv ...string) Combination {
	for i := 0; i+1 < len(kv); i += 2 {
		c.set(kv[i], kv[i+1])
	}
	return c
}

// Presets returns the presets (a copy).
func Presets() []Preset { return append([]Preset(nil), presets...) }

// PresetOf returns the preset whose combination c is exactly (ok false: none).
func PresetOf(c Combination) (Preset, bool) {
	c = c.Normal()
	for _, p := range presets {
		if p.Options == c {
			return p, true
		}
	}
	return Preset{}, false
}

// FindPreset returns preset id (case-insensitive).
func FindPreset(id string) (Preset, bool) {
	for _, p := range presets {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return Preset{}, false
}

// MaxJSON is the longest combination Parse reads (bytes): four ids are far below it.
const MaxJSON = 1024

// ErrUnknown is wrapped by every error about an unknown category or option.
var ErrUnknown = errors.New("unknown")

// ParseJSON reads a combination as the wire carries it: an object of category ids to option ids. A missing category
// is its default; an unknown category, an unknown option, a value that isn't a string, a second object or anything
// over MaxJSON is refused (a hostile client's call).
func ParseJSON(data []byte) (Combination, error) {
	if len(data) > MaxJSON {
		return Combination{}, fmt.Errorf("rules: %d bytes, more than %d", len(data), MaxJSON)
	}
	var raw map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&raw); err != nil {
		return Combination{}, fmt.Errorf("rules: %v", err)
	}
	if raw == nil {
		return Combination{}, errors.New("rules: not an object")
	}
	if d.More() {
		return Combination{}, errors.New("rules: more than one value")
	}
	var c Combination
	for k, v := range raw {
		if _, ok := category(k); !ok {
			return Combination{}, fmt.Errorf("rules: %w category %q", ErrUnknown, clip(k))
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return Combination{}, fmt.Errorf("rules: %s is not a string", k)
		}
		if _, ok := option(k, s); !ok {
			return Combination{}, fmt.Errorf("rules: %w option %q for %s", ErrUnknown, clip(s), k)
		}
		c.set(k, s)
	}
	return c.Normal(), nil
}

// Parse reads a preset's id, a combination's JSON or its short form "category=option[,category=option]" (dgserver
// --test-rules; the short form needs no quotes on a command line: "pick=random,shaperPool=melee").
func Parse(spec string) (Combination, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "{") {
		return ParseJSON([]byte(spec))
	}
	if strings.Contains(spec, "=") {
		obj := map[string]string{}
		for _, kv := range strings.Split(spec, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok {
				return Combination{}, fmt.Errorf("rules: %q is no category=option", clip(kv))
			}
			obj[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return Combination{}, err
		}
		return ParseJSON(b)
	}
	if p, ok := FindPreset(spec); ok {
		return p.Options, nil
	}
	var ids []string
	for _, p := range presets {
		ids = append(ids, p.ID)
	}
	return Combination{}, fmt.Errorf("rules: %w preset %q (presets: %s, or a JSON object)", ErrUnknown, clip(spec),
		strings.Join(ids, ", "))
}

// clip shortens a value from outside for an error text.
func clip(s string) string {
	const n = 32
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Describe is the combination's readable text: its options away from the default, in catalog order ("All Random, Shop
// on Death"), or "Standard".
func Describe(c Combination) string {
	var parts []string
	for _, cat := range catalog {
		v := c.Get(cat.ID)
		if v == "" || v == cat.Default {
			continue
		}
		if o, ok := option(cat.ID, v); ok {
			parts = append(parts, o.Name)
		} else {
			parts = append(parts, cat.Name+" "+clip(v))
		}
	}
	if len(parts) == 0 {
		return "Standard"
	}
	return strings.Join(parts, ", ")
}

// CustomPrefix is the game browser's prefix of a combination that is no preset.
const CustomPrefix = "[Custom rules] "

// Prefix is the game browser's prefix for c: "" for the standard game, "[<preset>] " for a preset, else CustomPrefix.
func Prefix(c Combination) string {
	if c.IsDefault() {
		return ""
	}
	if p, ok := PresetOf(c); ok {
		return "[" + p.Name + "] "
	}
	return CustomPrefix
}

// Finding is one reason a combination can't be played as it is (as mapc check's findings): Category is a category's id,
// or "bots" or "host" for the setup around it.
type Finding struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// The findings' categories besides the rule categories.
const (
	FindingBots = "bots"
	FindingHost = "host"
)

// Catalog tells Check how big a shaper pool is: its shapers, and those of them the bots can play.
type Catalog interface {
	PoolSize(pool string) (shapers, botShapers int, ok bool)
}

// Context is what the checks need to know about the game beside the combination.
type Context struct {
	Bots      bool // the lobby's bots are on (they fill the open seats)
	BotSeats  int  // the most seats one team leaves to bots (with Bots)
	TeamSize  int  // the seats of a team (the game's team capacity); 0 = BotSeats counts alone
	HostRules bool // the game's match host takes rules (it said so in its hello)
	// HostFeatures are the features the game's match host named in its hello: an option that needs one (Option.Host)
	// is refused without it.
	HostFeatures []string
	Catalog      Catalog // the pools' sizes; nil = Pools{} (every shaper of the table)
	// NoShaperSelect: the server runs no shaper select (dgserver without --shaper-select), so it can give nobody a
	// shaper list: All Random and the pools can't be played.
	NoShaperSelect bool
}

// The shapers a pick needs in the pool: shapers are unique within a team, so five; a draft takes ten picks and two
// bans a team, none twice in the whole game (draft.go); a vote one (PoolForVote, vote.go).
const (
	PoolForPick  = 5
	PoolForDraft = 2*DraftPicksPerTeam + 2*DraftBansPerTeam
)

// Check returns why the combination can't be played in this context; none = it passes. The checks: unknown options;
// the host must take rules for anything away from the standard game; All Random and a pool need the server's shaper
// select; the pool must hold the shapers the pick needs; with bots on every option must support bots, and the pool must
// hold a bot shaper for every seat of a team with bots. The item pool and the shop access don't interact (a purchase
// needs both).
func Check(c Combination, ctx Context) []Finding {
	var out []Finding
	if err := c.Validate(); err != nil {
		for _, cat := range catalog {
			if v := c.Get(cat.ID); v != "" {
				if _, ok := option(cat.ID, v); !ok {
					out = append(out, Finding{cat.ID, fmt.Sprintf("%s has no option %q", cat.Name, clip(v))})
				}
			}
		}
		return out
	}
	c = c.Normal()
	if !c.IsDefault() && !ctx.HostRules {
		out = append(out, Finding{FindingHost, "This game's match host is an older build that can't play rules: only " +
			"the standard game is possible."})
	} else {
		for _, k := range catalog { // M30: an option a rules host plays only with a newer feature
			if o, _ := option(k.ID, c.Get(k.ID)); o.Host != "" && !slices.Contains(ctx.HostFeatures, o.Host) {
				out = append(out, Finding{k.ID, o.Name + " needs a newer match host than this game's: choose another " +
					"option."})
			}
		}
	}
	if ctx.NoShaperSelect { // RULES3: the rolls and the pools are the shaper lists of shaper select
		if c.Pick == "random" {
			out = append(out, Finding{Pick, "All Random needs shaper select, which this server doesn't run."})
		}
		if c.Pick == "draft" || IsVotePick(c.Pick) { // RULES5b, ARMY1: the drafted or drawn shaper is the human's list
			p, _ := option(Pick, c.Pick)
			out = append(out, Finding{Pick, p.Name + " needs shaper select, which this server doesn't run."})
		}
		if c.ShaperPool != "all" {
			out = append(out, Finding{ShaperPool, "A shaper pool needs shaper select, which this server doesn't run."})
		}
	}
	cat := ctx.Catalog
	if cat == nil {
		cat = Pools{}
	}
	shapers, botShapers, ok := cat.PoolSize(c.ShaperPool)
	if !ok {
		out = append(out, Finding{ShaperPool, "This server has no shapers for the pool " + clip(c.ShaperPool) + "."})
	} else if c.Pick != "blind" || c.ShaperPool != "all" { // the standard pick is the server's as it is (RULES3)
		need := PoolForPick
		switch {
		case c.Pick == "draft":
			need = PoolForDraft
		case IsVotePick(c.Pick):
			need = PoolForVote
		}
		if shapers < need {
			p, _ := option(Pick, c.Pick)
			out = append(out, Finding{ShaperPool, fmt.Sprintf("%s needs at least %d shapers in the pool; it has %d.",
				p.Name, need, shapers)})
		}
	}
	if ctx.Bots {
		for _, k := range catalog {
			v := c.Get(k.ID)
			if v == k.Default {
				continue
			}
			if o, _ := option(k.ID, v); !o.Bots {
				out = append(out, Finding{k.ID, o.Name + " can't be played with bots yet: turn the bots off or choose " +
					"another option."})
			}
		}
		// RULES3: a team with bots needs a bot shaper for every seat of the team, not only the bots' seats: its humans may
		// hold some of them (they pick, or roll, before the bots draw, and the bots never take a teammate's shaper)
		need := ctx.BotSeats
		if need > 0 && ctx.TeamSize > need {
			need = ctx.TeamSize
		}
		if ok && c.ShaperPool != "all" && need > botShapers { // without a pool the bots draw as they always did
			out = append(out, Finding{FindingBots, fmt.Sprintf("The bots play %d of the pool's shapers; a team with "+
				"bots needs %d, one for each of its seats.", botShapers, need)})
		}
	}
	return out
}

// DescribeFindings joins findings for a log line.
func DescribeFindings(fs []Finding) string {
	var parts []string
	for _, f := range fs {
		parts = append(parts, f.Category+": "+f.Message)
	}
	return strings.Join(parts, "; ")
}

// CatalogView is the whole catalog for a lobby's panel: the categories with their options and the presets.
type CatalogView struct {
	Categories []Category `json:"categories"`
	Presets    []Preset   `json:"presets"`
}

// View returns the catalog for the lobby.
func View() CatalogView { return CatalogView{Categories: Categories(), Presets: Presets()} }
