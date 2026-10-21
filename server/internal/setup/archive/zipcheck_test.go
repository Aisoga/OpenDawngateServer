package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckZipFiles(t *testing.T) {
	entries := sample()
	m := manifestOf(entries)
	zp := makeZip(t, entries)
	var last int64
	err := CheckZipFiles(zp, m, func(phase string, done, total int64) {
		if phase != PhaseCheckZip || done > total || done < last {
			t.Errorf("%s: %d of %d (after %d)", phase, done, total, last)
		}
		last = done
	})
	if err != nil {
		t.Fatalf("the matching archive refused: %v", err)
	}
	if last != m.TotalSize() {
		t.Errorf("progress ended at %d of %d", last, m.TotalSize())
	}
	// an extra entry is allowed
	if err := CheckZipFiles(makeZip(t, append(sample(), entry{"Dawngate/extra.txt", "x", when})), m, nil); err != nil {
		t.Errorf("an extra entry refused: %v", err)
	}
	for name, c := range map[string]struct {
		entries []entry
		want    string
	}{
		"missing":   {sample()[:3], "Dawngate/game/Data/Config.package is missing (+1 more)"},
		"changed":   {[]entry{sample()[0], sample()[1], {"Dawngate/game/Dawngate.exe", "MZ fake exf", when}, sample()[3], sample()[4]}, "Dawngate/game/Dawngate.exe differs (SHA-256)"},
		"resized":   {[]entry{sample()[0], sample()[1], {"Dawngate/game/Dawngate.exe", "MZ fake exe!", when}, sample()[3], sample()[4]}, "Dawngate/game/Dawngate.exe has 12 bytes, want 11"},
		"outside":   {append(sample(), entry{"Other/a.txt", "x", when}), "everything must be under Dawngate/"},
		"not a zip": {nil, "not a zip archive"},
	} {
		zp := filepath.Join(t.TempDir(), "Dawngate.zip")
		if c.entries == nil {
			os.WriteFile(zp, []byte("<html>not here</html>"), 0o644)
		} else {
			zp = makeZip(t, c.entries)
		}
		if err := CheckZipFiles(zp, m, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
