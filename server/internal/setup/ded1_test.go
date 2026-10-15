package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/gamecontent"
)

// dedicated are a dedicated server's options (Start-Server's dedicated = yes): no player copy, whatever the tunnel.
var dedicated = ServerOptions{Address: Local, NoClient: true, NoClientNote: "a dedicated server: the match-host copy only"}

// A dedicated server sets up the match-host copy only: no player copy is extracted, patched or checked, and the
// content file (from the host copy's game data) is the same as a normal setup's. A complete folder needs no archive.
func TestDedicatedServer(t *testing.T) {
	s, _ := newSetup(t)
	if err := s.Server(dedicated); err != nil {
		t.Fatalf("Server: %v\n%s", err, s.Out.(*bytes.Buffer))
	}
	expect(t, s, map[string]Status{"certificates": Done, "client copy": Skipped, "host copy": Done, "host exe": Done,
		"host config": Done, "host DLL": Done, "content": Done})
	for _, step := range []string{"client exe", "client config", "client texts"} {
		if _, ok := status(s)[step]; ok {
			t.Errorf("step %q ran for a dedicated server", step)
		}
	}
	if out := s.Out.(*bytes.Buffer).String(); !strings.Contains(out, "a dedicated server: the match-host copy only") {
		t.Errorf("the skipped player copy without its reason:\n%s", out)
	}
	if exists(filepath.Dir(s.ClientCopy())) {
		t.Errorf("%s exists: the player copy was made", filepath.Dir(s.ClientCopy()))
	}
	for _, leftover := range []string{"client.partial", "client.extract", "host.partial", "host.extract"} {
		if exists(filepath.Join(s.Root, leftover)) {
			t.Errorf("%s left behind", leftover)
		}
	}
	got, err := os.ReadFile(s.Content())
	if err != nil {
		t.Fatal(err)
	}

	// the same archive set up the normal way gives the same content file
	n := &Setup{Layout: Layout{Root: filepath.Join(t.TempDir(), "normal")}, Out: &bytes.Buffer{}, Manifest: s.Manifest,
		FindZip: s.FindZip, HostFiles: s.HostFiles}
	if err := n.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatalf("normal Server: %v\n%s", err, n.Out.(*bytes.Buffer))
	}
	if want, _ := os.ReadFile(n.Content()); !bytes.Equal(got, want) {
		t.Error("the content built from the host copy differs from the one built from the player copy")
	}

	// a complete dedicated folder needs neither the archive nor a player copy, and a check is clean
	s2 := again(s)
	s2.FindZip = func() (string, error) { return "", ErrNoZip }
	if err := s2.Server(dedicated); err != nil {
		t.Fatalf("rerun without the archive: %v\n%s", err, s2.Out.(*bytes.Buffer))
	}
	expect(t, s2, map[string]Status{"certificates": OK, "client copy": Skipped, "host copy": OK, "host exe": OK,
		"host config": OK, "host DLL": OK, "content": OK})
	s3 := again(s)
	s3.Check = true
	s3.FindZip = s2.FindZip
	if err := s3.Server(dedicated); err != nil {
		t.Fatalf("check: %v\n%s", err, s3.Out.(*bytes.Buffer))
	}
	if exists(filepath.Dir(s.ClientCopy())) {
		t.Error("the check made the player copy")
	}
	// the same folder for a server that makes the player copy (tunnel = off, not dedicated) is incomplete
	s4 := again(s)
	s4.Check = true
	s4.FindZip = s2.FindZip
	if err := s4.Server(ServerOptions{Address: Local}); err == nil {
		t.Error("a check with the player copy passed without it")
	}
	expect(t, s4, map[string]Status{"client copy": Missing, "host copy": OK})
}

// Without the archive and without a host copy a dedicated server's setup says what is missing: the host copy and
// the content that needs its game data, but not the player copy.
func TestDedicatedNoArchive(t *testing.T) {
	s, _ := newSetup(t)
	s.FindZip = func() (string, error) { return "", ErrNoZip }
	s.Server(dedicated)
	expect(t, s, map[string]Status{"certificates": Done, "client copy": Skipped, "host copy": Missing,
		"host DLL": Missing, "content": Missing})
	if out := s.Out.(*bytes.Buffer).String(); !strings.Contains(out, "needs the match-host copy") {
		t.Errorf("report:\n%s", out)
	}
}

// A dedicated server leaves an existing player copy alone: it is neither checked, patched nor repaired, and the
// content doesn't read its game data (the host copy's, which the setup checked, serve).
func TestDedicatedLeavesPlayerCopyAlone(t *testing.T) {
	s, _ := newSetup(t)
	if err := s.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(s.ClientCopy(), "game", "Dawngate.exe")
	data, err := gamecontent.DataDir(s.ClientCopy())
	if err != nil {
		t.Fatal(err)
	}
	assets := filepath.Join(data, gamecontent.AssetDataSnap)
	for _, f := range []string{exe, assets} {
		if err := os.WriteFile(f, []byte("changed by someone else"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s2 := again(s)
	if err := s2.Server(dedicated); err != nil {
		t.Fatalf("Server: %v\n%s", err, s2.Out.(*bytes.Buffer))
	}
	expect(t, s2, map[string]Status{"client copy": Skipped, "host copy": OK, "content": OK})
	for _, f := range []string{exe, assets} {
		if b, _ := os.ReadFile(f); string(b) != "changed by someone else" {
			t.Errorf("the dedicated setup touched %s", f)
		}
	}
}
