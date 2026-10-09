package assetdata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// DumpEnv names a folder that `assetdata.py dump <bin> <dir> --locale <Locale.package>` wrote from the
// same game files; TestDumpMatchesAssetdataPy compares every class with it.
const DumpEnv = "DAWNGATE_ASSETDATA_DUMP"

func gameSet(t *testing.T) (*Set, *Types, *Locale) {
	t.Helper()
	arch, err := snap.Open(testgame.Data(t, "AssetData.snap"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := FromSnap(arch)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := dbpf.Open(testgame.Data(t, "Locale", "en-us", "Locale.package"))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := LoadLocale(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return set, ts, loc
}

// Build 854: every file of every class parses exactly, with no bad reference, and the
// catalog's typeCrcs equal the type tables' (the client's load check).
func TestGameFilesParse(t *testing.T) {
	set, ts, loc := gameSet(t)
	files, classes := 0, 0
	for _, c := range set.Classes() {
		inst, errs, _, err := set.Instances(ts, c, loc)
		if IsNoClass(err) {
			t.Errorf("class %s: no type table", c)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if errs.Len() > 0 {
			t.Errorf("class %s: %d files don't parse, e.g. %v", c, errs.Len(), errs.vals[errs.keys[0]])
		}
		for _, k := range inst.Keys() {
			o, _ := inst.Get(k)
			if _, bad := o.(*Object).Get("_badref"); bad {
				t.Errorf("%s.%s: bad references", k, c)
			}
		}
		files += inst.Len()
		classes++
	}
	if files != 6970 || classes != 57 {
		t.Errorf("%d files of %d classes, want 6970 of 57", files, classes)
	}
	cat, _, _, err := set.Instances(ts, "Catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cat.Get("catalog_153")
	entries, _ := c.(*Object).Get("entries")
	n := 0
	for _, e := range entries.([]any) {
		eo := e.(*Object)
		name, _ := eo.Get("assetNameType")
		crc, _ := eo.Get("typeCrc")
		_, class, _ := strings.Cut(name.(string), ".")
		typ := ts.Type(class)
		if typ == nil || !typ.HasCRC || uint64(typ.CRC) != crc.(uint64) {
			t.Errorf("%s: typeCrc %v, type table %v", name, crc, typ)
		}
		n++
	}
	if n != 6969 {
		t.Errorf("%d catalog entries, want 6969", n)
	}
}

// Each class's JSON reads exactly as assetdata.py dump's: the same keys in the same order, the same
// strings, ints as ints with the same digits, floats as floats with the same float64 and float32 value.
func TestDumpMatchesAssetdataPy(t *testing.T) {
	dir := os.Getenv(DumpEnv)
	if dir == "" {
		t.Skipf("set %s to an assetdata.py dump of the same game files", DumpEnv)
	}
	set, ts, loc := gameSet(t)
	py, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range py {
		class := strings.TrimSuffix(filepath.Base(f), ".json")
		if class == "catalog_ids" || class == "_summary" {
			continue
		}
		seen[strings.ToLower(class)] = true
		want, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := set.Dump(ts, class, loc)
		if err != nil {
			t.Errorf("%s: %v", class, err)
			continue
		}
		if diffs := compareJSON(want, got, 5); len(diffs) > 0 {
			t.Errorf("%s differs from assetdata.py's:\n  %s", class, strings.Join(diffs, "\n  "))
		}
	}
	for _, c := range set.Classes() {
		if !seen[c] {
			t.Errorf("class %s: assetdata.py wrote no file for it", c)
		}
	}
	if len(seen) != 57 {
		t.Errorf("%d classes in %s, want 57", len(seen), dir)
	}
}

// The decoded payloads of AssetData.snap are snap.py's: SHA-256 over every entry's name, NUL, size
// (u32 LE) and bytes, in index order (the same digest over snap.py's Snap.read).
func TestSnapPayloadsMatchSnapPy(t *testing.T) {
	arch, err := snap.Open(testgame.Data(t, "AssetData.snap"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, e := range arch.Entries {
		b, err := arch.Read(e)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(h, e.Name+"\x00")
		h.Write([]byte{byte(len(b)), byte(len(b) >> 8), byte(len(b) >> 16), byte(len(b) >> 24)})
		h.Write(b)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != snapDigest {
		t.Errorf("AssetData.snap payload digest %s, snap.py's %s", got, snapDigest)
	}
	if notes := arch.LayoutNotes(); len(notes) > 0 {
		t.Errorf("layout notes: %v", notes)
	}
}

// snapDigest is the digest TestSnapPayloadsMatchSnapPy describes, computed with snap.py (build 854).
const snapDigest = "bd057faf6f06b48a07efa67542f3d3448be0de6ac325e2cf4c4ce4c743abbf36"

// jnode is a JSON value with its object keys in order.
type jnode struct {
	kind byte // o object, a array, s string, n number, b bool, z null
	keys []string
	kids []*jnode
	text string
}

func decodeOrdered(raw []byte) (*jnode, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() (*jnode, error)
	walk = func() (*jnode, error) {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch v := tok.(type) {
		case json.Delim:
			n := &jnode{kind: 'a'}
			if v == '{' {
				n.kind = 'o'
			}
			for dec.More() {
				if n.kind == 'o' {
					k, err := dec.Token()
					if err != nil {
						return nil, err
					}
					n.keys = append(n.keys, k.(string))
				}
				kid, err := walk()
				if err != nil {
					return nil, err
				}
				n.kids = append(n.kids, kid)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		case string:
			return &jnode{kind: 's', text: v}, nil
		case json.Number:
			return &jnode{kind: 'n', text: string(v)}, nil
		case bool:
			return &jnode{kind: 'b', text: strconv.FormatBool(v)}, nil
		case nil:
			return &jnode{kind: 'z'}, nil
		}
		return nil, fmt.Errorf("unexpected token %v", tok)
	}
	return walk()
}

// compareJSON lists up to max differences between two JSON documents.
func compareJSON(want, got []byte, max int) []string {
	w, err := decodeOrdered(want)
	if err != nil {
		return []string{"want: " + err.Error()}
	}
	g, err := decodeOrdered(got)
	if err != nil {
		return []string{"got: " + err.Error()}
	}
	var diffs []string
	var cmp func(path string, a, b *jnode)
	cmp = func(path string, a, b *jnode) {
		if len(diffs) >= max {
			return
		}
		if a.kind != b.kind {
			diffs = append(diffs, fmt.Sprintf("%s: kind %c, got %c", path, a.kind, b.kind))
			return
		}
		switch a.kind {
		case 'o', 'a':
			if len(a.kids) != len(b.kids) || strings.Join(a.keys, "\x00") != strings.Join(b.keys, "\x00") {
				diffs = append(diffs, fmt.Sprintf("%s: keys %v (%d), got %v (%d)", path, a.keys, len(a.kids), b.keys, len(b.kids)))
				return
			}
			for i := range a.kids {
				p := fmt.Sprintf("%s[%d]", path, i)
				if a.kind == 'o' {
					p = path + "." + a.keys[i]
				}
				cmp(p, a.kids[i], b.kids[i])
			}
		case 'n':
			af, bf := strings.ContainsAny(a.text, ".eE"), strings.ContainsAny(b.text, ".eE")
			if af != bf {
				diffs = append(diffs, fmt.Sprintf("%s: number %s, got %s", path, a.text, b.text))
				return
			}
			if !af {
				if a.text != b.text {
					diffs = append(diffs, fmt.Sprintf("%s: %s, got %s", path, a.text, b.text))
				}
				return
			}
			a64, _ := strconv.ParseFloat(a.text, 64)
			b64, _ := strconv.ParseFloat(b.text, 64)
			a32, _ := strconv.ParseFloat(a.text, 32)
			b32, _ := strconv.ParseFloat(b.text, 32)
			if a64 != b64 || a32 != b32 || math.Signbit(a64) != math.Signbit(b64) {
				diffs = append(diffs, fmt.Sprintf("%s: %s, got %s", path, a.text, b.text))
			}
		default:
			if a.text != b.text {
				diffs = append(diffs, fmt.Sprintf("%s: %q, got %q", path, a.text, b.text))
			}
		}
	}
	cmp("", w, g)
	return diffs
}
