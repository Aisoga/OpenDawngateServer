// Package issues holds the issue tracker's rules: the list of tracked guesses about the original game that the
// server carries (assumptions.json, kept in step with the store by Sync), the YouTube links a claim may give
// (ParseLink), a claim's weight and an issue's status (score.go), the check of a claim's note and the API's tokens.
// The data is internal/store's (schema version 8), the pages and the API internal/site's.
package issues

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "embed"

	"dawngate/server/internal/store"
)

// Entry is one tracked guess as the list carries it: only the player-facing fields (the list is published with the
// server's code; the notes' private fields never come here).
type Entry struct {
	ID      string `json:"id"`
	Area    string `json:"area"`    // the list's section
	Title   string `json:"title"`   // the guess's heading
	Assumed string `json:"assumed"` // what we assumed, one plain sentence
	LookFor string `json:"lookFor"` // what to look for in a video of the original
	Top     bool   `json:"top"`     // one of the most important
	Order   int    `json:"order"`   // the list's order
}

// the list of tracked guesses, written by ./gen from the notes
//
//go:embed assumptions.json
var assumptionsJSON []byte

// Entries returns the tracked guesses the server carries, in the list's order.
func Entries() ([]Entry, error) {
	return ParseEntries(assumptionsJSON)
}

// ParseEntries reads a list in assumptions.json's format and checks it: every entry has an id and a title, no id
// twice.
func ParseEntries(b []byte) ([]Entry, error) {
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, fmt.Errorf("issues: the list: %v", err)
	}
	seen := make(map[string]bool, len(es))
	for i, e := range es {
		switch {
		case strings.TrimSpace(e.ID) == "":
			return nil, fmt.Errorf("issues: entry %d has no id", i)
		case seen[e.ID]:
			return nil, fmt.Errorf("issues: %q twice", e.ID)
		case strings.TrimSpace(e.Title) == "":
			return nil, fmt.Errorf("issues: %q has no title", e.ID)
		}
		seen[e.ID] = true
	}
	return es, nil
}

// Sync makes the store's issues these entries (store.SyncIssues): a new id is added (open), a listed one gets the
// entry's text, an id no longer listed is withdrawn (InDoc false). States, verdicts and claims are never touched.
func Sync(st *store.Store, entries []Entry, now time.Time) (store.SyncResult, error) {
	list := make([]store.IssueText, len(entries))
	for i, e := range entries {
		list[i] = store.IssueText{ID: e.ID, Area: e.Area, Title: e.Title, Assumed: e.Assumed, LookFor: e.LookFor,
			Top: e.Top, Order: e.Order}
	}
	return st.SyncIssues(list, now)
}

// MaxNote is how many characters a claim's note may have.
const MaxNote = store.MaxClaimNote

// Errors of CheckNote (their texts are shown to the player).
var (
	ErrNoteLong    = fmt.Errorf("the note is longer than %d characters", MaxNote)
	ErrNoteControl = errors.New("the note holds characters that aren't text")
)

// CheckNote returns the note as it is stored: line breaks as \n, spaces at both ends trimmed. It refuses a note of
// more than MaxNote characters, one that isn't UTF-8, and control or direction-changing characters (only line breaks
// and tabs are kept). The note is plain text: the pages show it escaped and never turn it into links.
func CheckNote(note string) (string, error) {
	return checkText(note, MaxNote, ErrNoteLong, ErrNoteControl)
}

// MaxVerdictNote is how many characters a verdict's note may have.
const MaxVerdictNote = store.MaxVerdictNote

// Errors of CheckVerdictNote.
var (
	ErrVerdictNoteLong    = fmt.Errorf("the verdict's note is longer than %d characters", MaxVerdictNote)
	ErrVerdictNoteControl = errors.New("the verdict's note holds characters that aren't text")
)

// CheckVerdictNote is CheckNote for a verdict's note (an admin's or the API's; MaxVerdictNote characters).
func CheckVerdictNote(note string) (string, error) {
	return checkText(note, MaxVerdictNote, ErrVerdictNoteLong, ErrVerdictNoteControl)
}

func checkText(note string, max int, errLong, errControl error) (string, error) {
	if !utf8.ValidString(note) {
		return "", errControl
	}
	note = strings.ReplaceAll(note, "\r\n", "\n")
	note = strings.ReplaceAll(note, "\r", "\n")
	note = strings.TrimSpace(note)
	for _, r := range note {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.Is(unicode.Cc, r) || bidiOrSeparator(r) {
			return "", errControl
		}
	}
	if utf8.RuneCountInString(note) > max {
		return "", errLong
	}
	return note, nil
}

// bidiOrSeparator reports the characters that would let a note change how the text around it reads: the
// bidirectional marks, embeddings, overrides and isolates, and the line and paragraph separators.
func bidiOrSeparator(r rune) bool {
	switch {
	case r == 0x061C, r == 0x200E, r == 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	}
	return false
}

// NewToken makes a token for the API: 32 random bytes in base64url (43 characters), and its SHA-256 in lowercase
// hex, which is all the store keeps (store.CreateAPIToken). The token is shown once.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, TokenHash(token), nil
}

// TokenHash is the SHA-256 of a token in lowercase hex (store.APITokenByHash looks a token up by it).
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
