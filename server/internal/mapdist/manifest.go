package mapdist

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Entry is a manifest's line: a map the server hands out, its tag and name (map.json's), its bundle's size and
// SHA-256.
type Entry struct {
	Tag, Name string
	Size      int64
	SHA256    [32]byte
}

// Short is the bundle hash's first 8 hex digits, for the log.
func (e Entry) Short() string { return hex.EncodeToString(e.SHA256[:4]) }

// MaxManifest is the most lines a manifest may have (a session hello names at most 64 maps too).
const MaxManifest = 64

var (
	tagRe  = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)
)

// FormatManifest is the manifest's text (web.MapPackagesPath): a line "<tag> <name> <size> <sha256 hex>" per entry, by
// tag.
func FormatManifest(entries []Entry) string {
	es := append([]Entry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Tag < es[j].Tag })
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "%s %s %d %s\n", e.Tag, e.Name, e.Size, hex.EncodeToString(e.SHA256[:]))
	}
	return b.String()
}

// ParseManifest reads a manifest, by tag. Every line must be well formed (a tag of a-z 0-9, a name of A-Z a-z 0-9 _, a
// size of at most 4 GB, 64 hex digits), at most MaxManifest of them, no tag twice; blank lines are skipped.
func ParseManifest(text string) (map[string]Entry, error) {
	out := map[string]Entry{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 || !tagRe.MatchString(f[0]) || !nameRe.MatchString(f[1]) {
			return nil, fmt.Errorf("the server's map list, line %d: not \"<tag> <name> <size> <sha256>\"", i+1)
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < 0 || size > 1<<32 {
			return nil, fmt.Errorf("the server's map list, line %d: size %q", i+1, f[2])
		}
		sum, err := hex.DecodeString(f[3])
		if err != nil || len(sum) != 32 {
			return nil, fmt.Errorf("the server's map list, line %d: not a SHA-256", i+1)
		}
		if _, dup := out[f[0]]; dup {
			return nil, fmt.Errorf("the server's map list names %s twice", f[0])
		}
		if len(out) == MaxManifest {
			return nil, fmt.Errorf("the server's map list has more than %d maps", MaxManifest)
		}
		e := Entry{Tag: f[0], Name: f[1], Size: size}
		copy(e.SHA256[:], sum)
		out[e.Tag] = e
	}
	return out, nil
}
