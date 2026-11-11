package dgserver

import (
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/store"
)

func TestParseTestPreferred(t *testing.T) {
	ps, err := parseTestPreferred(" p3a@x.io|dawngate1=NA, p3b@x.io = eu ")
	if err != nil || len(ps) != 2 || ps[0] != (testPreferred{"p3a@x.io", "dawngate1", "na"}) || ps[1] != (testPreferred{"p3b@x.io", "", "eu"}) {
		t.Errorf("parsed %+v, %v", ps, err)
	}
	for _, bad := range []string{"", "p3a@x.io", "=na", "p3a@x.io=n", "p3a@x.io=northamerica", "p3a@x.io=n4", "a=na,b"} {
		if _, err := parseTestPreferred(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// PREF3's test aid: the named accounts (p3a made with its password) get their Preferred Region at the start; a bad
// value is only logged.
func TestTestPreferredAtStart(t *testing.T) {
	data := t.TempDir()
	log := runBriefly(t, data, "--test-preferred-region", "p3a@x.io|dawngate1=na,p3b@x.io=eu")
	if !strings.Contains(log, "TEST: Preferred Regions set: p3a@x.io (account ") ||
		!strings.Contains(log, ") eu (--test-preferred-region, a test aid)") {
		t.Fatalf("start:\n%s", log)
	}
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"p3a": "na", "p3b": "eu"} {
		if a, _, ok := st.PersonaByName(name); !ok || a.Settings[blaze.SettingPreferredRegion] != want {
			t.Errorf("%s: %v %v, want %s", name, ok, a.Settings, want)
		}
	}
	if c, r := accounts.Authenticate(st, "p3a@x.io", "dawngate1"); r.Code != 0 || c.UserID == 0 {
		t.Errorf("p3a's login with the given password: %+v, %+v", c, r)
	}
	st.Close()
	if log := runBriefly(t, data, "--test-preferred-region", "p3a@x.io=x"); !strings.Contains(log, "TEST: --test-preferred-region p3a@x.io=x: ") {
		t.Errorf("a bad value:\n%s", log)
	}
}
