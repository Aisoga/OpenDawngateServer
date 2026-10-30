package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The map check per version (MAPED-GUARD): a map of a maps folder is compiled into every player copy and the host
// copy at setup, and the compiler is deterministic, so a copy has the server's version of a map exactly when its
// archive's SHA-256 is the host copy's. A player copies a map package in by hand, so its copy may lack a map or have
// another version of it; the session hello names the copy's map archives with their SHA-256 (sessionVersion 3), and
// the server lets the player into a game on such a map only with the same hash (blaze.Server.MapVersions).

// MaxHelloMaps bounds the maps a session hello names.
const MaxHelloMaps = 64

// InstalledMap is a map archive installed in a player copy's game\Data ("zz_CM_Ring.snap") and its SHA-256.
type InstalledMap struct {
	Archive string
	SHA256  [32]byte
}

// Hex is the hash in lower-case hex.
func (m InstalledMap) Hex() string { return hex.EncodeToString(m.SHA256[:]) }

// Short is the hash's first 8 hex digits, for log lines.
func (m InstalledMap) Short() string { return m.Hex()[:8] }

// CleanArchive keeps an archive's file name to letters, digits, '.', '_', '+' and '-' (anything else becomes '-'),
// at most 255 bytes, no blanks around it.
func CleanArchive(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 255 {
		s = s[:255]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '+', r == '-':
			return r
		}
		return '-'
	}, s)
}

// ReadInstalledMaps hashes those of archives (file names, once each in any case) that are regular files in the copy's
// game\Data folder dataDir, in archives' order; a missing or unreadable one is left out (the copy doesn't have it).
// The list is never nil (Client.Maps: a copy without maps names an empty list).
func ReadInstalledMaps(dataDir string, archives ...string) []InstalledMap {
	out := []InstalledMap{}
	seen := map[string]bool{}
	for _, a := range archives {
		k := strings.ToLower(a)
		if a == "" || seen[k] || a != filepath.Base(a) {
			continue
		}
		seen[k] = true
		if sum, ok := hashFile(filepath.Join(dataDir, a)); ok {
			out = append(out, InstalledMap{Archive: a, SHA256: sum})
		}
	}
	if len(out) > MaxHelloMaps {
		out = out[:MaxHelloMaps]
	}
	return out
}

func hashFile(p string) (sum [32]byte, ok bool) {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return sum, false
	}
	f, err := os.Open(p)
	if err != nil {
		return sum, false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, false
	}
	copy(sum[:], h.Sum(nil))
	return sum, true
}

// MapsWords lists maps for a log line: "zz_CM_Ring.snap 85d808c2, zz_MM_Map_v3.snap eed1e14c" ("none" when empty).
func MapsWords(maps []InstalledMap) string {
	if len(maps) == 0 {
		return "none"
	}
	var b []string
	for _, m := range maps {
		b = append(b, m.Archive+" "+m.Short())
	}
	return strings.Join(b, ", ")
}
