package rules

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/content"
)

// The pools' sizes in build 854 (35 offered shapers, 13 the bots play), and on a server that offers fewer.
func TestPoolSizes(t *testing.T) {
	want := map[string][2]int{"all": {35, 13}, "melee": {17, 8}, "ranged": {19, 6}, "tank": {5, 2}, "bruiser": {7, 3},
		"carry": {7, 3}, "mage": {7, 1}, "support": {5, 2}}
	for _, o := range Categories()[1].Options {
		w, ok := want[o.ID]
		if !ok {
			t.Errorf("pool %q has no expected size", o.ID)
			continue
		}
		n, b, ok := Pools{}.PoolSize(o.ID)
		if !ok || n != w[0] || b != w[1] {
			t.Errorf("pool %s: %d shapers, %d for bots (%v), want %v", o.ID, n, b, ok, w)
		}
		if o.ID != "all" && n < PoolForPick {
			t.Errorf("pool %s can never be played (%d shapers)", o.ID, n)
		}
	}
	if _, _, ok := (Pools{}).PoolSize("assassin"); ok {
		t.Error("an id outside the catalog has a size")
	}
	few := Pools{Offered: map[string]bool{"PC_Guardian": true, "PC_Alpha": true, "PC_Weaver": true}}
	if n, b, _ := few.PoolSize("all"); n != 2 || b != 1 {
		t.Errorf("two offered shapers: %d, %d", n, b)
	}
	if got := few.Members("melee"); !reflect.DeepEqual(got, []string{"PC_Guardian"}) {
		t.Errorf("melee members %v", got)
	}
	if got := (Pools{}).Members("all"); got != nil {
		t.Errorf("the pool all has members %v", got)
	}
	if got := (Pools{}).Members("support"); strings.Join(got, ",") != "PC_Artist,PC_BloodMage,PC_Doll,PC_Dreamer,PC_Sculptor" {
		t.Errorf("support members %v", got)
	}
	// a hybrid attacker is in both
	for _, p := range []string{"melee", "ranged"} {
		found := false
		for _, m := range (Pools{}).Members(p) {
			found = found || m == "PC_Golem"
		}
		if !found {
			t.Errorf("Faris (hybrid) not in %s", p)
		}
	}
}

// All Random's rolls: one shaper of the pool per seat, never twice within a team, the same for a seed; "" when the pool
// runs out.
func TestRoll(t *testing.T) {
	pool := (Pools{}).Members("tank")
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 200; seed++ {
		got := Roll(pool, map[int]int{1: 5, 2: 3}, seed)
		if len(got[1]) != 5 || len(got[2]) != 3 {
			t.Fatalf("seed %d: %v", seed, got)
		}
		for team, picks := range got {
			dup := map[string]bool{}
			for _, p := range picks {
				if p == "" || dup[p] {
					t.Fatalf("seed %d team %d: %v (empty or twice)", seed, team, picks)
				}
				dup[p] = true
				found := false
				for _, m := range pool {
					found = found || m == p
				}
				if !found {
					t.Fatalf("seed %d: %s outside the pool", seed, p)
				}
			}
		}
		seen[got[2][0]] = true
		if again := Roll(pool, map[int]int{1: 5, 2: 3}, seed); !reflect.DeepEqual(again, got) {
			t.Fatalf("seed %d rolled %v, then %v", seed, got, again)
		}
	}
	if len(seen) != len(pool) {
		t.Errorf("200 seeds gave team 2's first seat only %d of the pool's %d shapers", len(seen), len(pool))
	}
	if got := Roll([]string{"PC_Alpha", "PC_Guardian"}, map[int]int{1: 3}, 7); got[1][2] != "" || got[1][0] == got[1][1] {
		t.Errorf("a pool of two for three seats: %v", got)
	}
}

// The table against a content file made from build 854 (DG_CONTENT_FILE, e.g. server\data\content.json): every shaper
// the content offers (titled, not testing-only) has a row with the content's archetype, and every row is offered.
func TestPoolsAgreeWithContent(t *testing.T) {
	path := os.Getenv("DG_CONTENT_FILE")
	if path == "" {
		t.Skip("DG_CONTENT_FILE not set")
	}
	c, err := content.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	listed := c.Listed()
	offered := map[string]bool{}
	for _, it := range c.Items {
		if it.ItemType == content.ItemShaper && !it.TestingOnly && it.Asset != "" && listed(it) {
			offered[it.Shaper] = true
		}
	}
	arch := map[string]string{}
	for _, s := range c.Shapers {
		arch[s.Name] = s.Archetype
	}
	rows := map[string]bool{}
	for _, f := range Facts() {
		rows[f.Class] = true
		if !offered[f.Class] {
			t.Errorf("%s: in the table, not offered by the content", f.Class)
		}
		if arch[f.Class] != f.Archetype {
			t.Errorf("%s: archetype %q, the content's %q", f.Class, f.Archetype, arch[f.Class])
		}
	}
	for cls := range offered {
		if !rows[cls] {
			t.Errorf("%s: offered by the content, not in the table", cls)
		}
	}
}

// RULES3's checks: shaper select needed, the pool's real sizes, a team with bots needs a bot shaper per seat.
func TestCheckPools(t *testing.T) {
	cases := []struct {
		name string
		c    Combination
		ctx  Context
		want string
	}{
		{"all random without shaper select", with(Standard(), Pick, "random"), Context{HostRules: true, NoShaperSelect: true}, Pick},
		{"a pool without shaper select", with(Standard(), ShaperPool, "melee"), Context{HostRules: true, NoShaperSelect: true}, ShaperPool},
		{"the standard game without shaper select", Standard(), Context{HostRules: true, NoShaperSelect: true}, ""},
		{"tanks, humans only", with(Standard(), ShaperPool, "tank"), Context{HostRules: true}, ""},
		{"tanks in a draft", with(Standard(), Pick, "draft", ShaperPool, "tank"), Context{HostRules: true}, ShaperPool},
		{"tanks with one bot seat", with(Standard(), ShaperPool, "tank"), Context{HostRules: true, Bots: true, BotSeats: 1, TeamSize: 5}, FindingBots},
		{"melee with bots", with(Standard(), Pick, "random", ShaperPool, "melee"), Context{HostRules: true, Bots: true, BotSeats: 4, TeamSize: 5}, ""},
		{"ranged with bots (6 bot shapers)", with(Standard(), ShaperPool, "ranged"), Context{HostRules: true, Bots: true, BotSeats: 5, TeamSize: 5}, ""},
		{"ranged, bots on, no open seat", with(Standard(), ShaperPool, "mage"), Context{HostRules: true, Bots: true, BotSeats: 0, TeamSize: 5}, ""},
		{"mages with bots", with(Standard(), ShaperPool, "mage"), Context{HostRules: true, Bots: true, BotSeats: 2, TeamSize: 5}, FindingBots},
		{"a pool the server offers too little of", with(Standard(), ShaperPool, "support"),
			Context{HostRules: true, Catalog: Pools{Offered: map[string]bool{"PC_Artist": true}}}, ShaperPool},
	}
	for _, c := range cases {
		got := Check(c.c, c.ctx)
		if categories(got) != c.want {
			t.Errorf("%s: findings %q, want %q (%s)", c.name, categories(got), c.want, DescribeFindings(got))
		}
	}
}
