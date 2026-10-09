package dbpf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

// Build 854's packages read as dbpf.py reads them. The digests are SHA-256 over every entry's key,
// NUL, size (u32 LE) and contents, in index order, computed with dbpf.py's Package.read;
// Locale.package exercises RefPack (198 of its 200 entries). Rebuild without replacements gives the file
// back unchanged, as dbpf.py's rebuild does for all four.
func TestGamePackagesMatchDbpfPy(t *testing.T) {
	for _, c := range []struct {
		rel     string
		entries int
		digest  string
	}{
		{"Config.package", 18, "795e6f91934ae9c0eb213bc723c1ed4a1b9e368669434539720de0c87f4b7c9c"},
		{"Config_Ship.package", 1389, "eeaa16b85f37a82d2863859f0b7f68c77175b7e3ff4e77912284b5e340be4299"},
		{"Config_Pack.package", 7, "b7380a06d1cc681261226e8124402c8aacb82a645153e9e3bc6b4ba75f95223c"},
		{filepath.Join("Locale", "en-us", "Locale.package"), 200, "1f9edba600ab4814ddefac2a7c9ac9d3f21602f5a18c1f37774e5f556f1eb2a4"},
	} {
		p, err := Open(testgame.Data(t, c.rel))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Entries) != c.entries {
			t.Errorf("%s: %d entries, want %d", c.rel, len(p.Entries), c.entries)
		}
		h := sha256.New()
		for _, e := range p.Entries {
			b, err := p.Read(e)
			if err != nil {
				t.Fatalf("%s: %v", c.rel, err)
			}
			io.WriteString(h, e.Key()+"\x00")
			h.Write([]byte{byte(len(b)), byte(len(b) >> 8), byte(len(b) >> 16), byte(len(b) >> 24)})
			h.Write(b)
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != c.digest {
			t.Errorf("%s: digest %s, dbpf.py's %s", c.rel, got, c.digest)
		}
		same, err := p.Rebuild(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(same, p.Data) {
			t.Errorf("%s: Rebuild without replacements changed the file", c.rel)
		}
	}
}
