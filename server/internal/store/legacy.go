package store

import (
	"encoding/json"
	"fmt"
	"os"
)

// The JSON store of the earlier builds (imported at the first start): one file, dgserver's <data>\accounts.json, rewritten whole on every
// change. Its layout is Account's JSON tags.

// legacyFormat names the accounts file's layout.
const legacyFormat = "dawngate-accounts-1"

type legacyFile struct {
	Format        string    `json:"format"`
	NextUserID    int64     `json:"nextUserID"`
	NextPersonaID int64     `json:"nextPersonaID"`
	Accounts      []Account `json:"accounts"`
}

// readLegacy reads and checks a JSON store: it must parse, have the known format and no email twice
// (case-insensitively), as the JSON store's own Open required.
func readLegacy(path string) (*legacyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f legacyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("store: %s doesn't parse (%v); fix or move it away, it won't be overwritten", path, err)
	}
	if f.Format != legacyFormat {
		return nil, fmt.Errorf("store: %s has format %q, want %q", path, f.Format, legacyFormat)
	}
	seen := map[string]bool{}
	for _, a := range f.Accounts {
		key := emailKey(a.Email)
		if seen[key] {
			return nil, fmt.Errorf("store: %s has the email %q twice", path, a.Email)
		}
		seen[key] = true
	}
	return &f, nil
}

// importedName returns the name the imported JSON store at legacy is renamed to: accounts.json.imported, or
// accounts.json.imported.N when that is taken (an earlier import's file is never overwritten).
func importedName(legacy string) string {
	name := legacy + ImportedSuffix
	for i := 2; ; i++ {
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s%s.%d", legacy, ImportedSuffix, i)
	}
}
