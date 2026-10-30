package tunnel

import (
	"encoding/hex"
	"sort"
	"strings"
)

// The server's versions of the maps it offers (MAPED-GUARD-dialog): Start-Client's CREATE GAME dialog leaves out a
// map whose archive the player's copy has in another version than the server's, which the map check would refuse
// (blaze.Server.MapVersions) and which would play The Dawngate instead. The info hello names each offered map's
// version as a fact of its own, FactMapVersion + the map's tag ("version.ring") → the archive's SHA-256 in lower-case
// hex (64 characters, a fact value's most: version.MaxLen). Its keys sort after FactMaps and FactSignup, so a
// Start-Client of a release before them, which reads at most 16 facts in key order, still reads those two; it
// ignores the rest. Without the tunnel the same versions are dgserver's page web.MapVersionsPath, one line per map
// (MapVersionsText). A server of a release before them names none, and the dialog lists the map as before.

// FactMapVersion is the key prefix of the facts naming the server's version of an offered map: FactMapVersion + tag.
const FactMapVersion = "version."

// MapVersionFacts are the info hello's facts for versions (a map's tag → its archive's SHA-256): at most
// MaxHelloMaps, by tag.
func MapVersionFacts(versions map[string][32]byte) map[string]string {
	facts := map[string]string{}
	for _, tag := range versionTags(versions) {
		sum := versions[tag]
		facts[FactMapVersion+strings.ToLower(tag)] = hex.EncodeToString(sum[:])
	}
	return facts
}

// FactMapVersions reads the server's map versions from a probe's facts (tag, lower case → SHA-256); a fact whose
// value isn't 64 hex digits is left out. nil when the server named none.
func FactMapVersions(facts map[string]string) map[string][32]byte {
	var out map[string][32]byte
	for k, v := range facts {
		tag, ok := strings.CutPrefix(strings.ToLower(k), FactMapVersion)
		if !ok || tag == "" {
			continue
		}
		if sum, ok := parseSHA256(v); ok {
			if out == nil {
				out = map[string][32]byte{}
			}
			out[tag] = sum
		}
	}
	return out
}

// MapVersionsText is web.MapVersionsPath's body for versions: a line "<tag> <sha256 hex>" per map, by tag (at most
// MaxHelloMaps; empty: none).
func MapVersionsText(versions map[string][32]byte) string {
	var b strings.Builder
	for _, tag := range versionTags(versions) {
		sum := versions[tag]
		b.WriteString(strings.ToLower(tag) + " " + hex.EncodeToString(sum[:]) + "\n")
	}
	return b.String()
}

// ParseMapVersions reads MapVersionsText back (a line that isn't a tag and 64 hex digits is skipped); nil when it
// names none.
func ParseMapVersions(text string) map[string][32]byte {
	facts := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			facts[FactMapVersion+f[0]] = f[1]
		}
	}
	return FactMapVersions(facts)
}

// versionTags are versions' tags, sorted, at most MaxHelloMaps, each one a fact key can carry.
func versionTags(versions map[string][32]byte) []string {
	var tags []string
	for tag := range versions {
		if tag != "" && CleanArchive(tag) == tag && len(FactMapVersion+tag) <= 64 {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	if len(tags) > MaxHelloMaps {
		tags = tags[:MaxHelloMaps]
	}
	return tags
}

func parseSHA256(s string) (sum [32]byte, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return sum, false
	}
	if _, err := hex.Decode(sum[:], []byte(s)); err != nil {
		return sum, false
	}
	return sum, true
}
