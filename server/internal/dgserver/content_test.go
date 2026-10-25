package dgserver

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/setup/testgame/synthgame"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// B7a: --make-content builds the content file from an AssetData dump (here the synthetic fixture), and
// a server with a data folder grants it to a new account and keeps the account across a restart.
func TestServerContentAndPersistence(t *testing.T) {
	data := t.TempDir()
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: data, policy: blaze.DefaultPolicy()}
	if err := makeContent(filepath.Join(golden.Dir(), "assetdata"), cfg.contentFile()); err != nil {
		t.Fatal(err)
	}
	if err := makeContent(t.TempDir(), cfg.contentFile()); err == nil {
		t.Error("--make-content accepted a folder without a dump")
	}
	if err := makeContent(filepath.Join(golden.Dir(), "assetdata"), ""); err == nil {
		t.Error("--make-content without a content file")
	}

	session := func() {
		in, err := start(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer in.stop()
		bc, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", in.ports[1]))
		if err != nil {
			t.Fatal(err)
		}
		defer bc.Close()
		call(t, bc, 1, 0x28, 0, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).Bytes())
		bal := call(t, bc, 0x820, 0x08, 1, nil)
		if ec, _ := bal.Int("ECBL"); ec != blaze.DefaultStartEC {
			t.Errorf("ECBL %d, want %d", ec, blaze.DefaultStartEC)
		}
		complete := call(t, bc, 0x81F, 0x76, 2, nil)
		list, _ := complete.Get("LIST")
		acct := list.Value.(*tdf.List).Items[0].(*tdf.Struct)
		if items, ok := acct.Get("ITEM"); !ok || len(items.Value.(*tdf.List).Items) != 9 {
			t.Errorf("ITEM: %+v", items)
		}
	}
	session()
	session() // the same account, not granted twice (ECBL unchanged)
	// the account is in the database, once
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Count(); n != 1 {
		t.Errorf("%d accounts in the database", n)
	}
	if a, _, ok := st.PersonaByName("player"); !ok || a.Email != "player@example.com" || !a.Granted {
		t.Errorf("stored account: %+v %v", a, ok)
	}
	st.Close()

	// a broken database stops the server and stays as it is
	fresh := t.TempDir()
	cfg.dataDir, cfg.contentPath = fresh, filepath.Join(data, "content.json")
	broken := strings.Repeat("{", 2000)
	os.WriteFile(filepath.Join(fresh, store.FileName), []byte(broken), 0o644)
	if in, err := start(cfg); err == nil {
		in.stop()
		t.Error("started with a broken accounts.db")
	}
	if raw, _ := os.ReadFile(filepath.Join(fresh, store.FileName)); string(raw) != broken {
		t.Errorf("accounts.db changed")
	}
	// and so does a broken accounts.json of an earlier build, which isn't imported then
	os.Remove(filepath.Join(fresh, store.FileName))
	os.WriteFile(filepath.Join(fresh, store.LegacyFile), []byte("{"), 0o644)
	if in, err := start(cfg); err == nil {
		in.stop()
		t.Error("started with a broken accounts.json")
	}
	if raw, _ := os.ReadFile(filepath.Join(fresh, store.LegacyFile)); string(raw) != "{" {
		t.Errorf("accounts.json changed: %s", raw)
	}
}

// --make-content also takes a game folder (game\Data\AssetData.snap) and writes what
// gamecontent.JSON makes from it.
func TestMakeContentFromGameFolder(t *testing.T) {
	game := t.TempDir()
	if err := synthgame.Write(game); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "content.json")
	if err := makeContent(game, file); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	want, err := gamecontent.JSON(game)
	if err != nil || string(got) != string(want) {
		t.Errorf("content from the game folder: %v\n%s", err, got)
	}
	c, err := content.Load(file)
	if err != nil || len(c.Items) != 3 || len(c.Shapers) != 2 {
		t.Errorf("load: %v %+v", err, c)
	}
}

// Without a content file the server runs as before B7a and says how to make one; without a data folder
// it keeps accounts in memory only.
func TestServerWithoutContent(t *testing.T) {
	for _, c := range []struct {
		data string
		want string
	}{
		{t.TempDir(), "content.json (new accounts get no items; build it with --make-content); new accounts: all owned, EC 10000, PC 1000; FTUE done for all: true; perk pages: 10\n"},
		{"", "[stub] accounts in memory only; no content; new accounts: all owned, EC 10000, PC 1000; FTUE done for all: true; perk pages: 10\n"},
	} {
		logDir := t.TempDir()
		in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, dataDir: c.data, policy: blaze.DefaultPolicy(),
			mm: blaze.DefaultMatchmaking()})
		if err != nil {
			t.Fatal(err)
		}
		in.stop()
		files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
		raw, _ := os.ReadFile(files[0])
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		for _, want := range []string{c.want, "[stub] PvP matchmaking: 5v5, start with 1+ players after 1m30s (at least 30s after the last session joins)\n"} { // B10a, MMWAIT
			if !strings.Contains(text, want) {
				t.Errorf("log lacks %q:\n%s", want, text)
			}
		}
	}
}
