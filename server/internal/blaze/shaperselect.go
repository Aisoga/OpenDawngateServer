package blaze

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
)

// Shaper select on the match host (roadmap D3-live step 4; docs/GMS_PROTOCOL.md *Shaper select on the match
// host (D3-live)*, docs/HOST_CONTROL.md). With dgserver --shaper-select a hosted game's createGame asks the host
// for shaper select (CreateGame.ShaperSelect) and gives every human roster entry its content: the shaper list
// the client's C8 02 shows, its skins (C8 0B), its saved skins (the account's USET SKIN_0x* keys), and its wards
// and announcer packs (C8 0E), all from the account's inventory (B7a's store) and the content file.
//
// The list entry's flags (GMS_PROTOCOL.md *Shaper select*, the 10-byte entry): an owned shaper is ShaperOwned
// (selectable), one named by --free-rotation is ShaperFreeRotation (selectable, with the rotation icon), every
// other shaper that isn't testing-only is 0 (shown only under the client's "Any" filter, disabled). The offer id
// is 0: the store has no PUII yet (B7b/B7c).
//
// Shapers the client can't show are left out (SHAPERPOOL): a shaper item without a title (content.Listed: no
// longName string, so no collection entry and no art) is offered to nobody, owned or not, nor are its skins. In
// build 854 that is, besides the five testing-only shapers, Weaver ("Rezen") and WeaponMaster: listed, the client
// showed no card for them, but the host's Random and its phase-1 timer draw from this list, which made a ghost
// (no model, no animations) of a human who chose Random.

// ShaperSelect is dgserver's shaper select setup (--shaper-select, --free-rotation, --shaper-timers).
type ShaperSelect struct {
	On bool
	// FreeRotation names the shapers in the free rotation: shaper item names ("alpha"), their Nouns
	// ("alpha.Noun"), PlayerClasses ("PC_Alpha") or titles ("Fenmore"), case-insensitive.
	FreeRotation []string
	// Timers are the CreateGame.Timers keys gmshost.TimerShaperSelection ... TimerReveal (ms); empty = the
	// host's (practice: 300 s, 300 s, 15.5 s, 16 ms).
	Timers map[string]int64
}

// ParseShaperTimers reads --shaper-timers "<pick>,<role/perk>,<skin>,<reveal>" (ms; gms_script's syntax).
func ParseShaperTimers(s string) (map[string]int64, error) {
	f := strings.Split(s, ",")
	if len(f) != 4 {
		return nil, fmt.Errorf("--shaper-timers %q: want <pick ms>,<role/perk ms>,<skin ms>,<reveal ms>", s)
	}
	keys := []string{gmshost.TimerShaperSelection, gmshost.TimerRolePerkSelect, gmshost.TimerSkinSelect, gmshost.TimerReveal}
	out := map[string]int64{}
	for i, v := range f {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("--shaper-timers %q: %q is no duration in ms", s, v)
		}
		out[keys[i]] = n
	}
	return out, nil
}

// Describe is the setup for the startup log line.
func (ss ShaperSelect) Describe() string {
	if !ss.On {
		return "off (the host's [host] shaper_select decides)"
	}
	timers := "the host's"
	if len(ss.Timers) > 0 {
		timers = fmt.Sprintf("%d/%d/%d/%d ms", ss.Timers[gmshost.TimerShaperSelection], ss.Timers[gmshost.TimerRolePerkSelect],
			ss.Timers[gmshost.TimerSkinSelect], ss.Timers[gmshost.TimerReveal])
	}
	free := "none"
	if len(ss.FreeRotation) > 0 {
		free = strings.Join(ss.FreeRotation, ",")
	}
	return fmt.Sprintf("on: the roster's content from the accounts; free rotation %s; timers %s", free, timers)
}

// owned reports whether the account owns the item: its inventory, or an item everyone has (alwaysOwned, e.g.
// the default ward and announcer pack).
func owned(a store.Account, it content.Item) bool {
	return a.Items[it.CatalogID] > 0 || it.Status == "alwaysOwned"
}

// ShaperContent fills e with the account's shaper select content (the fields gmshost.RosterEntry carries for a
// human). Without content nothing is filled (the host then gives every human its default list).
func ShaperContent(c *content.Content, a store.Account, freeRotation []string, e *gmshost.RosterEntry) {
	if c.Empty() {
		return
	}
	free := map[string]bool{}
	for _, n := range freeRotation {
		free[strings.ToLower(strings.TrimSpace(n))] = true
	}
	inRotation := func(it content.Item) bool {
		return free[strings.ToLower(it.Name)] || free[strings.ToLower(it.Asset)] || free[strings.ToLower(it.Shaper)] ||
			free[strings.ToLower(it.Title)]
	}
	listed := c.Listed()
	byClass := map[string]content.Item{} // PlayerClass -> its shaper item (the default skin is its asset)
	for _, it := range c.Items {
		if it.ItemType != content.ItemShaper || it.TestingOnly || it.Asset == "" || !listed(it) {
			continue
		}
		byClass[it.Shaper] = it
		o := gmshost.ShaperOffer{Noun: it.Asset}
		if owned(a, it) {
			o.Flags |= gmshost.ShaperOwned
		}
		if inRotation(it) {
			o.Flags |= gmshost.ShaperFreeRotation
		}
		e.Shapers = append(e.Shapers, o)
	}
	for _, it := range c.Items {
		if it.TestingOnly || it.Asset == "" {
			continue
		}
		// WARDSKIN: every ward skin and announcer pack, owned or not. Build 854's client counts every collection type
		// but perk shapes and gems as owned (Collection_IsOwned 0x00878840), so its
		// ward carousel and announcer list offer them all and a click sends C8 0E for any of them
		// (ShaperSelect_OnWardClicked 0x00515260, _OnSelectAnouncerPack 0x00512DE0); the host refuses a C8 0E outside
		// this list, which with --owned starter left the shown pick without effect in the match.
		if it.ItemType != content.ItemWardSkin && it.ItemType != content.ItemAnnouncerPack && !owned(a, it) {
			continue
		}
		switch it.ItemType {
		case content.ItemShaperSkin:
			if sh, ok := byClass[it.Shaper]; ok && sh.Asset != it.Asset {
				if e.Skins == nil {
					e.Skins = map[string][]string{}
				}
				e.Skins[sh.Asset] = append(e.Skins[sh.Asset], it.Asset)
			}
		case content.ItemWardSkin:
			e.Wards = append(e.Wards, it.Asset)
		case content.ItemAnnouncerPack:
			e.Announcers = append(e.Announcers, it.Asset)
		}
	}
	// USET SKIN_0x<catalogID of the shaper's default skin, hex> = <the skin's catalogID, decimal>
	// (ClientAccountSettings_SaveShaperSelectSettings 0x00411E30, written at EnterGame when the skin isn't the
	// default); a saved skin counts only while it is owned.
	keys := make([]string, 0, len(a.Settings))
	for k := range a.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(strings.ToUpper(k), "SKIN_0X") {
			continue
		}
		def, err1 := strconv.ParseUint(k[len("SKIN_0x"):], 16, 32)
		skin, err2 := strconv.ParseUint(strings.TrimSpace(a.Settings[k]), 10, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		sh, ok1 := c.Item(uint32(def))
		sk, ok2 := c.Item(uint32(skin))
		if !ok1 || !ok2 || sh.ItemType != content.ItemShaper || sk.ItemType != content.ItemShaperSkin ||
			sk.Shaper != sh.Shaper || !owned(a, sk) {
			continue
		}
		if _, offered := byClass[sh.Shaper]; !offered { // a shaper the list leaves out (SHAPERPOOL)
			continue
		}
		if e.SavedSkins == nil {
			e.SavedSkins = map[string]string{}
		}
		e.SavedSkins[sh.Asset] = sk.Asset
	}
	// D3-perks: the account's perk pages with their stats (C8 09, C8 0A, C0 02)
	for _, p := range a.PerkPages {
		e.PerkPages = append(e.PerkPages, PageStats(c, p))
	}
}

// PageStats is a perk page as the match host gets it (D3-perks; docs/GMS_PROTOCOL.md *Perk pages in shaper select
// (D3-perks)*): its id and version, and the stats it gives, added up as the client's
// LoadoutManager_ComputePageStats (0x0088C890) does for the role/perk page's tooltip:
//   - each complete shape adds its attributes and modifiers (LoadoutManager_IsPerkComplete 0x00888AE0: exactly one
//     gem per socket, each a known gem whose colour is the socket's, or the socket takes any);
//   - every gem adds its own, also in an incomplete shape;
//   - attributes are summed per attribute and op, the sum rounded to 0.001 after each addition
//     (floor((sum + value) x 1000 + 0.5) / 1000: LoadoutStats_AddPerk 0x008870B0, LoadoutStats_AddGem 0x00887240);
//     modifiers are a set.
//
// Shapes and gems the content doesn't know, and attribute or op names outside the enums, give nothing.
func PageStats(c *content.Content, p store.PerkPage) gmshost.PerkPage {
	out := gmshost.PerkPage{ID: p.ID, Version: p.Version}
	type key struct{ attr, op int }
	sums := map[key]int64{}
	mods := map[string]bool{}
	add := func(perk content.Perk) {
		for _, at := range perk.Attributes {
			ai, ok1 := content.AttributeIndex(at.ID)
			oi, ok2 := content.OpIndex(at.Op)
			if !ok1 || !ok2 {
				continue
			}
			sums[key{ai, oi}] += int64(math.Floor(float64(at.Value)*1000 + 0.5))
		}
		for _, m := range perk.Modifiers {
			mods[m] = true
		}
	}
	for _, s := range p.Shapes {
		if shape, ok := c.Perk(s.ID); ok && shape.Kind == content.KindShape && perkComplete(c, shape, s.Gems) {
			add(shape)
		}
		for _, g := range s.Gems {
			if gem, ok := c.Perk(g.ID); ok && gem.Kind == content.KindGem {
				add(gem)
			}
		}
	}
	for k, v := range sums {
		out.Attributes = append(out.Attributes, gmshost.PerkAttribute{Attribute: k.attr, Op: k.op, Milli: v})
	}
	sort.Slice(out.Attributes, func(i, j int) bool {
		a, b := out.Attributes[i], out.Attributes[j]
		return a.Attribute < b.Attribute || (a.Attribute == b.Attribute && a.Op < b.Op)
	})
	for m := range mods {
		out.Modifiers = append(out.Modifiers, m)
	}
	sort.Strings(out.Modifiers)
	return out
}

// perkComplete is LoadoutManager_IsPerkComplete (0x00888AE0): as many gems as the shape has sockets, every socket
// filled, each gem known and of the socket's colour (or the socket is "any").
func perkComplete(c *content.Content, shape content.Perk, gems []store.PerkGem) bool {
	if len(gems) != len(shape.Sockets) {
		return false
	}
	filled := make([]*content.Perk, len(shape.Sockets))
	for _, g := range gems {
		if int(g.Socket) >= len(shape.Sockets) {
			return false
		}
		gem, ok := c.Perk(g.ID)
		if !ok || gem.Kind != content.KindGem {
			return false
		}
		filled[g.Socket] = &gem
	}
	for i, gem := range filled {
		if gem == nil || (shape.Sockets[i] != content.SocketAny && shape.Sockets[i] != gem.Category) {
			return false
		}
	}
	return true
}

// shaperSelectSpec puts dgserver's shaper select into a hosted game's createGame: the flag, the timers and every
// human's content from its account.
func (s *Server) shaperSelectSpec(spec *gmshost.CreateGame, players []*hostedPlayer) {
	if !s.ShaperSelect.On {
		return
	}
	spec.ShaperSelect = true
	if len(s.ShaperSelect.Timers) > 0 {
		if spec.Timers == nil {
			spec.Timers = map[string]int64{}
		}
		for k, v := range s.ShaperSelect.Timers {
			spec.Timers[k] = v
		}
	}
	for i := range spec.Roster {
		if spec.Roster[i].Bot || i >= len(players) || s.Store == nil {
			continue
		}
		if a, ok := s.Store.Account(players[i].userID); ok {
			ShaperContent(s.Content, a, s.ShaperSelect.FreeRotation, &spec.Roster[i])
		}
	}
}
