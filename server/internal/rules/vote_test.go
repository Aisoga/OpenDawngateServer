package rules

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// DrawVote: 10,000 draws land in proportion to the votes (3 : 2 : 1), a vote outside the pool or "" never counts, no
// vote draws uniformly from the pool, and the same seed draws the same.
func TestDrawVote(t *testing.T) {
	pool := []string{"PC_Alpha", "PC_Artificer", "PC_Gunman", "PC_Warden"}
	const n = 10000
	check := func(name string, votes []string, want map[string]float64, fromVote bool) {
		t.Helper()
		rng := rand.New(rand.NewPCG(7, 11))
		got := map[string]int{}
		for range n {
			s, v := DrawVote(votes, pool, rng)
			if v != fromVote {
				t.Fatalf("%s: fromVote %v, want %v", name, v, fromVote)
			}
			got[s]++
		}
		for s, c := range got {
			if _, ok := want[s]; !ok {
				t.Errorf("%s: drew %s %d times, which it never should", name, s, c)
			}
		}
		for s, share := range want {
			// a binomial's standard deviation at n = 10,000 is at most 50 draws; allow five of them
			if d := math.Abs(float64(got[s]) - share*n); d > 250 {
				t.Errorf("%s: %s drawn %d times, want about %.0f", name, s, got[s], share*n)
			}
		}
	}
	check("3:2:1", []string{"PC_Alpha", "PC_Gunman", "PC_Alpha", "PC_Warden", "PC_Gunman", "PC_Alpha"},
		map[string]float64{"PC_Alpha": 0.5, "PC_Gunman": 1.0 / 3, "PC_Warden": 1.0 / 6}, true)
	check("one vote", []string{"PC_Gunman"}, map[string]float64{"PC_Gunman": 1}, true)
	check("votes outside the pool and empty ones don't count", []string{"", "PC_Nobody", "PC_Warden", ""},
		map[string]float64{"PC_Warden": 1}, true)
	check("no votes: uniform from the pool", nil,
		map[string]float64{"PC_Alpha": .25, "PC_Artificer": .25, "PC_Gunman": .25, "PC_Warden": .25}, false)
	check("only empty votes", []string{"", ""},
		map[string]float64{"PC_Alpha": .25, "PC_Artificer": .25, "PC_Gunman": .25, "PC_Warden": .25}, false)
	if s, v := DrawVote([]string{"PC_Alpha"}, nil, rand.New(rand.NewPCG(1, 2))); s != "" || v {
		t.Errorf("an empty pool drew %q (%v)", s, v)
	}
	if s, v := DrawVote(nil, []string{"PC_Old"}, rand.New(rand.NewPCG(1, 2))); s != "PC_Old" || v {
		t.Errorf("a pool of one drew %q (%v)", s, v)
	}
	// the draw is the generator's: the same seed, the same shapers
	a, b := rand.New(rand.NewPCG(3, 4)), rand.New(rand.NewPCG(3, 4))
	votes := []string{"PC_Alpha", "PC_Gunman", "PC_Warden"}
	for i := range 50 {
		x, _ := DrawVote(votes, pool, a)
		y, _ := DrawVote(votes, pool, b)
		if x != y {
			t.Fatalf("draw %d: %s and %s from one seed", i, x, y)
		}
	}
}

// Army and Clone Pick in Check: a rules host with rules2, shaper select, no bots; a pool of one shaper is enough.
func TestCheckArmyClone(t *testing.T) {
	rules2 := Context{HostRules: true, HostFeatures: []string{"rules", HostRules2}}
	for _, pick := range []string{PickArmy, PickClone} {
		name := map[string]string{PickArmy: "Army Pick", PickClone: "Clone Pick"}[pick]
		c := with(Standard(), Pick, pick)
		if o, ok := option(Pick, pick); !ok || o.Name != name || o.Bots || o.Host != HostRules2 {
			t.Fatalf("%s: the option %+v (%v)", pick, o, ok)
		}
		if !IsVotePick(pick) {
			t.Errorf("%s isn't a vote", pick)
		}
		if Describe(c) != name || Prefix(c) != CustomPrefix {
			t.Errorf("%s: described %q, prefix %q", pick, Describe(c), Prefix(c))
		}
		if _, ok := PresetOf(c); ok {
			t.Errorf("%s is a preset", pick)
		}
		cases := []struct {
			name string
			ctx  Context
			c    Combination
			want string
		}{
			{"rules2 host", rules2, c, ""},
			{"rules host without rules2", Context{HostRules: true, HostFeatures: []string{"rules"}}, c, Pick},
			{"rules host naming no features", Context{HostRules: true}, c, Pick},
			{"old host", Context{}, c, FindingHost},
			{"with bots", Context{HostRules: true, HostFeatures: []string{HostRules2}, Bots: true, BotSeats: 4}, c, Pick},
			{"without shaper select", Context{HostRules: true, HostFeatures: []string{HostRules2}, NoShaperSelect: true}, c,
				Pick},
			{"a pool of one", Context{HostRules: true, HostFeatures: []string{HostRules2},
				Catalog: fakeCatalog{"all": {24, 10}, "melee": {1, 1}}}, with(c, ShaperPool, "melee"), ""},
			{"an empty pool", Context{HostRules: true, HostFeatures: []string{HostRules2},
				Catalog: fakeCatalog{"all": {24, 10}, "melee": {0, 0}}}, with(c, ShaperPool, "melee"), ShaperPool},
			{"with lightning", rules2, with(c, ItemPool, "lightning"), ""},
			{"with shop on death", rules2, with(c, Shop, "onDeath"), ""},
		}
		for _, k := range cases {
			got := Check(k.c, k.ctx)
			if categories(got) != k.want {
				t.Errorf("%s, %s: findings %q, want %q (%s)", pick, k.name, categories(got), k.want, DescribeFindings(got))
			}
		}
		fs := Check(c, Context{HostRules: true, HostFeatures: []string{HostRules2}, NoShaperSelect: true})
		if len(fs) != 1 || fs[0].Message != name+" needs shaper select, which this server doesn't run." {
			t.Errorf("%s without shaper select: %v", pick, fs)
		}
		fs = Check(c, Context{HostRules: true})
		if len(fs) != 1 || !strings.HasPrefix(fs[0].Message, name+" needs a newer match host") {
			t.Errorf("%s without rules2: %v", pick, fs)
		}
		fs = Check(c, Context{HostRules: true, HostFeatures: []string{HostRules2}, Bots: true, BotSeats: 1})
		if len(fs) != 1 || !strings.HasPrefix(fs[0].Message, name+" can't be played with bots yet") {
			t.Errorf("%s with bots: %v", pick, fs)
		}
		if p, err := Parse("pick=" + pick); err != nil || p.Pick != pick {
			t.Errorf("Parse pick=%s: %+v %v", pick, p, err)
		}
	}
	for _, p := range []string{"blind", "random", "draft", "", "Army"} {
		if IsVotePick(p) {
			t.Errorf("%q is a vote", p)
		}
	}
}
