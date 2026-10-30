package tunnel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
)

// TestMapVersionFacts: the info hello's version facts read back, sorted after maps and signup (so a reader of the
// release before them, which reads at most 16 facts, keeps both), and the HTTP page's text.
func TestMapVersionFacts(t *testing.T) {
	ring, grove := sha256.Sum256([]byte("ring")), sha256.Sum256([]byte("grove"))
	versions := map[string][32]byte{"ring": ring, "grove": grove}
	facts := MapVersionFacts(versions)
	if len(facts) != 2 || facts["version.ring"] != hex.EncodeToString(ring[:]) || len(facts["version.grove"]) != 64 {
		t.Fatalf("MapVersionFacts = %v", facts)
	}
	facts[FactMaps], facts[FactSignup] = "ring+grove", "open"
	_, got, err := readInfoReply(bytes.NewReader(infoReply("v1", facts)))
	if err != nil {
		t.Fatal(err)
	}
	if v := FactMapVersions(got); len(v) != 2 || v["ring"] != ring || v["grove"] != grove {
		t.Errorf("the versions read back: %v", v)
	}
	if tags, ok := FactMapTags(got); !ok || len(tags) != 2 {
		t.Errorf("the maps beside them: %v, %v", tags, ok)
	}

	// the most versions a server names, read as a Start-Client before them reads (16 facts at most): maps and
	// signup still come first
	many := map[string][32]byte{}
	for i := 0; i < MaxHelloMaps+5; i++ {
		many[fmt.Sprintf("m%02d", i)] = sha256.Sum256([]byte{byte(i)})
	}
	facts = MapVersionFacts(many)
	if len(facts) != MaxHelloMaps {
		t.Errorf("%d version facts, want at most %d", len(facts), MaxHelloMaps)
	}
	facts[FactMaps], facts[FactSignup] = "m00", "invite"
	reply := infoReply("v1", facts)
	old := readOldInfoReply(t, bytes.NewReader(reply), 16)
	if old[FactMaps] != "m00" || old[FactSignup] != "invite" {
		t.Errorf("a reader of 16 facts got %v", old)
	}
	if _, got, _ = readInfoReply(bytes.NewReader(reply)); len(FactMapVersions(got)) != MaxHelloMaps {
		t.Errorf("this release reads %d versions, want %d", len(FactMapVersions(got)), MaxHelloMaps)
	}

	// the page's text and back; lines that aren't a tag and 64 hex digits are skipped
	text := MapVersionsText(versions)
	if want := "grove " + hex.EncodeToString(grove[:]) + "\nring " + hex.EncodeToString(ring[:]) + "\n"; text != want {
		t.Errorf("MapVersionsText = %q", text)
	}
	if v := ParseMapVersions(text + "bad 1234\nring\n\n"); len(v) != 2 || v["ring"] != ring {
		t.Errorf("ParseMapVersions = %v", v)
	}
	if ParseMapVersions("") != nil || FactMapVersions(map[string]string{FactSignup: "open", "version.": "x"}) != nil {
		t.Error("no versions named: not nil")
	}
	if MapVersionsText(nil) != "" || len(MapVersionFacts(nil)) != 0 {
		t.Error("no versions: some text or facts")
	}
}

// readOldInfoReply reads an info reply's facts as readInfoReply did before MAPED-GUARD-dialog: at most limit.
func readOldInfoReply(t *testing.T, r io.Reader, limit int) map[string]string {
	t.Helper()
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(r, make([]byte, head[1])); err != nil {
		t.Fatal(err)
	}
	var n [1]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		t.Fatal(err)
	}
	facts := map[string]string{}
	for i := 0; i < int(n[0]) && i < limit; i++ {
		var kv [2]string
		for j := range kv {
			var l [1]byte
			if _, err := io.ReadFull(r, l[:]); err != nil {
				t.Fatal(err)
			}
			s := make([]byte, l[0])
			if _, err := io.ReadFull(r, s); err != nil {
				t.Fatal(err)
			}
			kv[j] = string(s)
		}
		facts[kv[0]] = kv[1]
	}
	return facts
}
