package configpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

// The Go patch is byte for byte what patch_config.py makes from the untouched packages of build 854.
// The hashes are SHA-256 of patch_config.py's output on copies of client\ia_Dawngate's packages (copy
// each, run patch_config.py --host on the copy, hash it); the untouched packages' hashes are docs/file_manifest.csv's.
var pythonHashes = []struct {
	name string
	o    Options
	want map[string]string
}{
	{"local (setup_all.py's client copy)", Defaults(), map[string]string{
		"Config.package":      "e154e0940bf417f15115826e89269aad124b02a80a1b639a507946b883042209",
		"Config_Ship.package": "d1d189ffb1424183af95f54860e005113e22a2daee3c53da40031f117b8c087c",
		"Config_Pack.package": "b163d83b11abe3d0163f54698aff9094a6410cab73e9083916a66a12d1d0322f",
	}},
	{"lan --host 192.168.1.20", Options{Host: "192.168.1.20", HTTPPort: 18080, TelemetryPort: 9988}, map[string]string{
		"Config.package":      "3d707074e165489c9050eb3dafd0ce004812694625f92d33cd063b460f7a6f86",
		"Config_Ship.package": "c13eb0721358a0d5159e07eec56c6ec96240fa52664c81589a0a6bb707e3ea0b",
		"Config_Pack.package": "e2b8318ed2ae5d0760931d283f19b15924207a490b4944a04d8e20ec289bf46b",
	}},
	{"--host 10.0.0.5 --http-port 8080 --telemetry-port 9000 --afk-minutes 30 --afk-disconnect-minutes 5",
		Options{Host: "10.0.0.5", HTTPPort: 8080, TelemetryPort: 9000, AFKMinutes: 30, AFKDisconnectMinutes: 5}, map[string]string{
			"Config.package":      "ced0b49e056dff0330bf6f7b0ad5fc0f8b43c70d38d585995b770a7e14ea40fb",
			"Config_Ship.package": "4d6533295fe5fdeb6be0188498fe6bfa3853450e780f1d54ad9eca4522c3c527",
			"Config_Pack.package": "c5336241f6d6759367e5f1f4de58f12ccccde1e73c0128223b3f6c4c630cc48d",
		}},
}

// The untouched packages (docs/file_manifest.csv, build 854).
var untouched = map[string]string{
	"Config.package":      "3d24beca4c8807ab4409c2496f60a2748b5c06e0e25e0e1e73bc18bc8fbae418",
	"Config_Ship.package": "945c10d797a764a40e1602a30fcbe89da78a77163885cab504be3577bf1041fc",
	"Config_Pack.package": "5bbe4c872f20021fcc6d462037a9817d8ec0c0a4de619876695061be643cfee8",
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestPackageMatchesPatchConfigPy(t *testing.T) {
	orig := map[string][]byte{}
	for _, name := range Packages {
		orig[name] = testgame.Read(t, name)
		if got := sha(orig[name]); got != untouched[name] {
			t.Fatalf("%s: SHA-256 %s, not build 854's untouched package", name, got)
		}
	}
	for _, c := range pythonHashes {
		hits := 0
		for _, name := range Packages {
			res, err := Package(orig[name], name, c.o)
			if err != nil {
				t.Fatalf("%s: %s: %v", c.name, name, err)
			}
			if res.Data == nil {
				t.Fatalf("%s: %s: nothing changed", c.name, name)
			}
			if got := sha(res.Data); got != c.want[name] {
				t.Errorf("%s: %s: SHA-256 %s, patch_config.py's %s", c.name, name, got, c.want[name])
			}
			hits += len(res.AFKHits)
		}
		if want := map[bool]int{true: 4, false: 0}[c.o.AFKMinutes != 0]; hits != want {
			t.Errorf("%s: %d AFK hits, want %d", c.name, hits, want)
		}
	}
}

// Patch on a scratch copy: the .orig files, the patched packages, a second run that changes nothing, and
// Restore.
func TestPatchDirOnACopy(t *testing.T) {
	dir := t.TempDir()
	for _, name := range Packages {
		if err := os.WriteFile(filepath.Join(dir, name), testgame.Read(t, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for run := 0; run < 2; run++ {
		if _, err := Patch(dir, Defaults()); err != nil {
			t.Fatal(err)
		}
		for _, name := range Packages {
			got, _ := os.ReadFile(filepath.Join(dir, name))
			if sha(got) != pythonHashes[0].want[name] {
				t.Errorf("run %d: %s not patch_config.py's", run, name)
			}
			o, _ := os.ReadFile(filepath.Join(dir, name+".orig"))
			if sha(o) != untouched[name] {
				t.Errorf("run %d: %s.orig is not the untouched package", run, name)
			}
		}
	}
	if _, err := Restore(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range Packages {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if sha(got) != untouched[name] {
			t.Errorf("%s not restored", name)
		}
	}
}
