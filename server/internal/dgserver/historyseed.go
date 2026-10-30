package dgserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
)

// The test aid --test-history-seed N[:<login name>] (MHIST3): an empty match history gets N made-up matches at the
// start, so the history page can be looked at (its Load more, every tab with data) without playing them. Each match
// has ten seats: the given login's persona (its account is made without a password when it doesn't exist, so the
// first login sets one, as for an account of an earlier build) on alternating teams, up to three made-up players
// ("Kestrel", "Marrow", ...; accounts <name>@seed.invalid without a password) and bots. Every fifth match carries
// only the basics (no detailed stats, no sources, no timeline), as a host without match stats leaves it; the others
// carry every key of the stats catalogue, damage and heal sources with the content's item, spell and spirit stone
// ids, and a timeline. The numbers are random (a fixed seed per match), not a game's. A store that already holds a
// match is left alone, so a restart doesn't add them twice.

// historySeedNames are the made-up players' persona names.
var historySeedNames = []string{"Kestrel", "Marrow", "Vesper", "Orrin", "Thistle", "Calder"}

// parseHistorySeed reads N[:<login name>] (N 1..500).
func parseHistorySeed(spec string) (int, string, error) {
	nText, login, _ := strings.Cut(strings.TrimSpace(spec), ":")
	n, err := strconv.Atoi(strings.TrimSpace(nText))
	if err != nil || n < 1 || n > 500 {
		return 0, "", fmt.Errorf("%q: want N[:<login name>] with N from 1 to 500", spec)
	}
	return n, accounts.NormalizeName(login), nil
}

// seedPersona returns the persona of the account with this login name, making the account (no password) when there
// is none.
func seedPersona(st *store.Store, login string, now time.Time) (store.Persona, store.Account, error) {
	a, err := st.Create(store.NewAccount{Name: login}, now)
	if errors.Is(err, store.ErrExists) {
		acc, p, ok := st.PersonaByName(store.PersonaName(login))
		if !ok {
			return store.Persona{}, store.Account{}, fmt.Errorf("account %s exists, but its persona %q wasn't found", login, store.PersonaName(login))
		}
		return p, acc, nil
	}
	if err != nil {
		return store.Persona{}, store.Account{}, err
	}
	if len(a.Personas) == 0 {
		return store.Persona{}, store.Account{}, fmt.Errorf("account %s has no persona", login)
	}
	return a.Personas[0], a, nil
}

// seedHistory inserts the matches and returns the log line's text.
func seedHistory(st *store.Store, c *content.Content, spec string, now time.Time) (string, error) {
	n, login, err := parseHistorySeed(spec)
	if err != nil {
		return "", err
	}
	if have, err := st.RecentMatches(0, 1); err != nil {
		return "", err
	} else if len(have) > 0 {
		return "the match history isn't empty: nothing seeded", nil
	}
	type human struct {
		persona, user int64
		name          string
	}
	var main *human
	if login != "" {
		p, a, err := seedPersona(st, login, now)
		if err != nil {
			return "", err
		}
		main = &human{persona: p.ID, user: a.UserID, name: p.Name}
	}
	var others []human
	for _, name := range historySeedNames {
		p, a, err := seedPersona(st, name+"@seed.invalid", now)
		if err != nil {
			return "", err
		}
		others = append(others, human{persona: p.ID, user: a.UserID, name: p.Name})
	}
	pool := seedPool(c)
	kinds := []struct{ kind, mode string }{{"pvp", "pvp"}, {"practice", "practice"}, {"custom", "custom"},
		{"pvp", "pvp"}, {"practiceGroup", "practice"}, {"pvp", "pvp"}}
	for i := 0; i < n; i++ {
		r := rand.New(rand.NewSource(int64(i + 1)))
		k := kinds[i%len(kinds)]
		minutes := 14 + r.Intn(24)
		if k.kind == "practice" {
			minutes = 9 + r.Intn(10)
		}
		dur := time.Duration(minutes)*time.Minute + time.Duration(r.Intn(60))*time.Second
		ended := now.Add(-time.Duration(n-i) * 47 * time.Minute)
		m := store.Match{GameID: int64(i + 1), Kind: k.kind, Mode: k.mode, Level: blaze.HostedLevel,
			Winner: 1 + r.Intn(2), Reason: []string{"guardian", "surrender", "guardian"}[r.Intn(3)],
			Started: ended.Add(-dur), Ended: ended, DurationMs: dur.Milliseconds()}
		if i%7 == 3 {
			m.Level = "MM_Map_v3"
		}
		basic := i%5 == 4
		// Who sits where: the login's persona at seat 0 or 5, made-up players beside it (fewer in practice).
		humans := map[int]human{}
		if main != nil {
			humans[(i%2)*5] = *main
		}
		extra := 1 + r.Intn(3)
		if k.kind == "practice" {
			extra = 0
		}
		for j, seat := 0, 1; j < extra && seat < 10; seat += 2 {
			if _, taken := humans[seat]; !taken {
				humans[seat] = others[(i+j)%len(others)]
				j++
			}
		}
		perm := r.Perm(len(pool.shapers))
		var seats []seedSeat
		for s := 0; s < 10; s++ {
			team := 1 + s/5
			ss := seedSeat{MatchSeat: store.MatchSeat{Seat: s, Team: team, Won: team == m.Winner, Role: 1 + (s+i)%5%4}}
			if len(pool.shapers) > 0 {
				ss.Noun = pool.shapers[perm[s%len(perm)]]
			}
			if h, ok := humans[s]; ok {
				ss.PersonaID, ss.UserID = h.persona, h.user
			} else {
				ss.Bot = true
			}
			seats = append(seats, ss)
		}
		seedStats(r, seats, minutes, m.Winner, m.DurationMs, basic, pool)
		for _, s := range seats {
			m.Seats = append(m.Seats, s.MatchSeat)
		}
		if !basic {
			m.Timeline = seedTimeline(r, seats, minutes, m.Reason == "guardian")
		}
		if _, err := st.InsertMatch(m); err != nil {
			return "", err
		}
	}
	who := "no player of yours"
	if main != nil {
		who = fmt.Sprintf("%s (%s) in every match", main.name, login)
	}
	return fmt.Sprintf("seeded %d made-up matches, %s, made-up players %s", n, who, strings.Join(historySeedNames, ", ")), nil
}

// seedPool is what the made-up stats name: shaper nouns with a title, item, battle spell and spirit stone ids.
type seedPoolT struct {
	shapers       []uint32
	items, spells []int64
	perks         []int64
}

func seedPool(c *content.Content) seedPoolT {
	var p seedPoolT
	if c == nil {
		return p
	}
	for _, s := range c.Shapers {
		noun := content.AssetID(s.Name + ".Noun")
		if !s.TestingOnly && s.Title != "" && blaze.ShaperTitle(noun, c) != "" { // the released ones (a title)
			p.shapers = append(p.shapers, noun)
		}
	}
	for _, it := range c.MatchItems {
		id := int64(content.AssetID(it.Name + ".Item"))
		if strings.HasPrefix(it.Name, "Spell_") {
			p.spells = append(p.spells, id)
		} else if it.Cost >= 1500 {
			p.items = append(p.items, id)
		}
	}
	for _, pk := range c.Perks {
		if len(pk.Modifiers) > 0 {
			p.perks = append(p.perks, int64(content.AssetID(pk.Modifiers[0])))
		}
	}
	return p
}

type seedSeat struct {
	store.MatchSeat
	goldLine, xpLine, levelLine, csLine []int
}

// seedStats fills each seat's list columns (a bot's as its stats, a human's playerFinish keys) and, unless basic,
// every catalogue key and the sources.
func seedStats(r *rand.Rand, seats []seedSeat, minutes, winner int, durMs int64, basic bool, pool seedPoolT) {
	pct := func(v int64, lo, hi int) int64 { return v * int64(lo+r.Intn(hi-lo+1)) / 100 }
	min := int64(minutes)
	for i := range seats {
		s := &seats[i]
		s.Kills, s.Deaths, s.Assists = int64(r.Intn(12)), int64(r.Intn(10)), int64(r.Intn(15))
		s.CS = min * int64(2+r.Intn(5))
		s.Level = min64(18, 2+min/3+int64(r.Intn(4)))
		s.Gold = 475 + min*int64(260+r.Intn(160))
		s.Stats = map[string]int64{"kills": s.Kills, "deaths": s.Deaths, "assists": s.Assists, "creeps": s.CS,
			"gold": s.Gold, "level": s.Level}
		if !s.Bot { // a human's playerFinish keys (gms/engine/death.cpp LogPlayerFinish), stored as a match stores them
			won := int64(0)
			if s.Won {
				won = 1
			}
			double := min64(s.Kills/2, int64(r.Intn(3)))
			triple := min64(double, int64(r.Intn(4)/3))
			for k, v := range map[string]int64{"won": won, "team": int64(s.Team), "durationMs": durMs,
				"noun": int64(s.Noun), "role": int64(s.Role), "bestStreak": min64(s.Kills, int64(r.Intn(6))),
				"doubleKills": double, "tripleKills": triple, "tetraKills": 0, "omniKills": 0} {
				s.Stats[k] = v
			}
		}
		if basic {
			continue
		}
		st := map[string]int64{}
		for k, v := range s.Stats {
			st[k] = v
		}
		dealt := min * int64(450+r.Intn(500))
		phys := pct(dealt, 35, 70)
		tru := pct(dealt, 2, 8)
		st["damageDealt"], st["damageDealtPhysical"], st["damageDealtTrue"] = dealt, phys, tru
		st["damageDealtMagical"] = dealt - phys - tru
		st["damageOverload"] = pct(dealt, 0, 4)
		st["largestHit"] = 250 + int64(r.Intn(900))
		st["overkill"] = pct(dealt, 1, 5)
		shares := []struct {
			key string
			lo  int
			hi  int
		}{{"damageToShapers", 30, 45}, {"damageToMinions", 20, 35}, {"damageToJungle", 4, 12}, {"damageToObjectives", 2, 6},
			{"damageToStructures", 4, 12}, {"damageToGuardian", 0, 3}, {"damageToWells", 0, 2}, {"damageToWorkers", 0, 2}}
		rest := dealt
		for j, sh := range shares {
			v := pct(dealt, sh.lo, sh.hi)
			if j == len(shares)-1 || v > rest {
				v = min64(v, rest)
			}
			st[sh.key] = v
			rest -= v
		}
		st["damageToMinions"] += rest
		taken := min * int64(350+r.Intn(450))
		tp := pct(taken, 40, 60)
		tt := pct(taken, 2, 6)
		st["damageTaken"], st["damageTakenPhysical"], st["damageTakenTrue"], st["damageTakenMagical"] = taken, tp, tt, taken-tp-tt
		fromShapers := pct(taken, 40, 70)
		st["damageTakenFromShapers"], st["damageTakenFromNpcs"] = fromShapers, taken-fromShapers
		st["damageAbsorbed"] = pct(taken, 1, 8)
		heal := min * int64(40+r.Intn(160))
		if s.Role == 2 {
			heal *= 3
		}
		self := pct(heal, 40, 70)
		allies := pct(heal, 10, 40)
		ld := pct(heal, 0, 20)
		st["healingDone"], st["healingSelf"], st["healingAllies"], st["healingOther"] = heal, self, allies, heal-self-allies
		st["lifeDrain"], st["overheal"] = ld, pct(heal, 5, 25)
		st["healingReceived"] = self + min*int64(r.Intn(60))
		st["shieldingDone"] = pct(heal, 0, 30)
		st["killsMinions"], st["killsJungle"] = s.CS*8/10, s.CS*2/10
		st["killsObjectives"], st["killsStructures"] = int64(r.Intn(3)), int64(r.Intn(4))
		st["killsWorkers"] = int64(r.Intn(6)) // no ward keys: a ward can't be hit or killed (the host sends none)
		if s.Team == winner && i%5 == 0 {
			st["killsGuardian"] = 1
		}
		st["timeDeadMs"] = s.Deaths * int64(12000+r.Intn(30000))
		st["wellsCaptured"], st["wardsPlaced"] = int64(r.Intn(5)), int64(r.Intn(12))
		// Gold: the start, the passive income and the sources, summing to the total (the list's gold).
		start, passive := int64(475), min*int64(60+r.Intn(30))
		st["goldStart"], st["goldPassive"] = start, passive
		left := max64(0, s.Gold-start-passive)
		gs := []string{"goldFromMinion", "goldFromJungle", "goldFromTower", "goldFromPlayerKill", "goldFromAssist",
			"goldFromObjective", "goldFromWorkers", "goldFromWellPassive", "goldFromWellBounty", "goldRole"}
		weights := []int{40, 10, 10, 12, 6, 4, 3, 5, 2, 8}
		for j, k := range gs {
			v := left * int64(weights[j]) / 100
			st[k] = v
		}
		sum := int64(0)
		for _, k := range gs {
			sum += st[k]
		}
		st["goldFromMinion"] += left - sum
		st["goldTotal"] = s.Gold
		xp := min * int64(500+r.Intn(250))
		st["xpTotal"], st["xpPassive"] = xp, pct(xp, 10, 15)
		xs := []string{"xpFromMinion", "xpFromJungle", "xpFromTower", "xpFromPlayerKill", "xpFromAssist", "xpFromObjective", "xpRole"}
		xw := []int{45, 12, 6, 12, 8, 4, 3}
		xsum := st["xpPassive"]
		for j, k := range xs {
			st[k] = xp * int64(xw[j]) / 100
			xsum += st[k]
		}
		st["xpFromMinion"] += xp - xsum
		switch s.Role {
		case 1:
			st["goldRoleGladiatorLastHit"], st["goldRoleGladiatorStored"] = st["goldRole"]*6/10, st["goldRole"]*4/10
		case 2:
			st["goldRoleTacticianProximity"], st["goldRoleTacticianHarass"] = st["goldRole"]*7/10, st["goldRole"]*3/10
		case 3:
			st["goldRoleHunterProc"], st["damageRoleHunterBonus"], st["healRoleHunter"] = st["goldRole"], pct(dealt, 1, 4), pct(heal, 2, 10)
		case 4:
			st["goldRolePredatorKill"], st["goldRolePredatorAssist"] = st["goldRole"]*5/10, st["goldRole"]*3/10
			st["goldRolePredatorWorkers"], st["xpRolePredator"] = st["goldRole"]*2/10, st["xpRole"]
		}
		caused, endured := int64(0), int64(0)
		for _, kind := range []string{"Stun", "Root", "Slow", "Fear", "Silence", "Knockback"} {
			if r.Intn(3) > 0 {
				v := min * int64(100+r.Intn(900))
				st["ccCaused"+kind+"Ms"] = v
				caused += v
			}
			if r.Intn(3) > 0 {
				v := min * int64(100+r.Intn(700))
				st["ccEndured"+kind+"Ms"] = v
				endured += v
			}
		}
		st["ccCausedMs"], st["ccEnduredMs"] = caused, endured
		s.Stats = st
		s.Sources = seedSources(r, s.Role, dealt, heal, pool)
	}
}

// seedSources splits the damage dealt (and the healing) over sources, the largest first.
func seedSources(r *rand.Rand, role int, dealt, heal int64, pool seedPoolT) json.RawMessage {
	slot := func(n int) *int { return &n }
	pick := func(ids []int64) int64 {
		if len(ids) == 0 {
			return 0
		}
		return ids[r.Intn(len(ids))]
	}
	xs := []gmshost.StatSource{
		{Kind: "basic"},
		{Kind: "ability", Slot: slot(0)}, {Kind: "ability", Slot: slot(1)}, {Kind: "ability", Slot: slot(2)},
		{Kind: "ability", Slot: slot(3)}, {Kind: "ability", Slot: slot(4)},
		{Kind: "item", ID: pick(pool.items)}, {Kind: "item", ID: pick(pool.items)},
		{Kind: "spell", ID: pick(pool.spells)}, {Kind: "perk", ID: pick(pool.perks)}, {Kind: "role"},
	}
	weights := []int{28, 16, 14, 10, 18, 4, 4, 2, 1, 2, 1}
	left := dealt
	for i := range xs {
		xs[i].Damage = dealt * int64(weights[i]) / 100
		left -= xs[i].Damage
	}
	xs[0].Damage += left
	hw := map[int]int{1: 40, 4: 25, 9: 15, 10: 20}
	hleft := heal
	for i, w := range hw {
		xs[i].Heal = heal * int64(w) / 100
		hleft -= xs[i].Heal
	}
	xs[1].Heal += hleft
	if role != 3 { // only the Hunter's role heals
		xs[1].Heal += xs[10].Heal
		xs[10].Heal = 0
	}
	for i := 1; i < len(xs); i++ { // largest first, as the host sends them
		for j := i; j > 0 && xs[j].Damage+xs[j].Heal > xs[j-1].Damage+xs[j-1].Heal; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
	b, _ := json.Marshal(xs)
	return b
}

// seedTimeline makes the per-minute lines (each ending at the seat's final values) and the events.
func seedTimeline(r *rand.Rand, seats []seedSeat, minutes int, guardian bool) json.RawMessage {
	tl := gmshost.MatchTimeline{StepSec: 60}
	for t := 1; t <= minutes; t++ {
		tl.Sec = append(tl.Sec, int64(t*60))
	}
	teamGold := [3][]int64{nil, make([]int64, minutes), make([]int64, minutes)}
	teamKills := [3][]int64{nil, make([]int64, minutes), make([]int64, minutes)}
	for _, s := range seats {
		ts := gmshost.TimelineSeat{Index: s.Seat}
		for t := 1; t <= minutes; t++ {
			f := float64(t) / float64(minutes)
			wobble := 0.9 + 0.2*r.Float64()
			g := int64(float64(s.Gold) * f * wobble)
			if t == minutes {
				g = s.Gold
			}
			ts.Gold = append(ts.Gold, g)
			ts.XP = append(ts.XP, int64(float64(s.Stats["xpTotal"])*f))
			ts.Level = append(ts.Level, 1+int64(float64(s.Level-1)*f))
			ts.CS = append(ts.CS, int64(float64(s.CS)*f))
			teamGold[s.Team][t-1] += g
		}
		tl.Seats = append(tl.Seats, ts)
	}
	var events []gmshost.TimelineEvent
	for _, s := range seats {
		for k := int64(0); k < s.Kills; k++ {
			victim := r.Intn(5)
			if s.Team == 1 {
				victim += 5
			}
			ms := 60000 + r.Intn((minutes-1)*60000+1)
			events = append(events, gmshost.TimelineEvent{Ms: int64(ms), Kind: "kill", A: s.Seat, B: victim})
			for t := (ms+59999)/60000 - 1; t < minutes; t++ { // the samples at or after the kill
				teamKills[s.Team][t]++
			}
		}
	}
	winner := 1
	if !seats[0].Won {
		winner = 2
	}
	for k := 0; k < 6; k++ {
		events = append(events, gmshost.TimelineEvent{Ms: int64((minutes*60000)*(k+3)/10 + r.Intn(20000)), Kind: "structure", A: winner, B: 9})
	}
	for k := 0; k < 3; k++ {
		events = append(events, gmshost.TimelineEvent{Ms: int64(120000 + r.Intn(minutes*50000)), Kind: "well", A: 1 + r.Intn(2), B: k})
	}
	if guardian {
		events = append(events, gmshost.TimelineEvent{Ms: int64(minutes*60000 - 5000), Kind: "guardian", A: winner})
	}
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].Ms < events[j-1].Ms; j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
	tl.Events = events
	for team := 1; team <= 2; team++ {
		tl.Teams = append(tl.Teams, gmshost.TimelineTeam{Team: team, Gold: teamGold[team], Kills: teamKills[team]})
	}
	b, _ := json.Marshal(tl)
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
