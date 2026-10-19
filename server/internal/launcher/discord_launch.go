package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Discord starts a game that isn't running when a friend clicks Join, through the URL scheme discord-<application
// id>:// that the game registers for itself: HKEY_CURRENT_USER\Software\Classes\discord-<id> with "URL Protocol",
// DefaultIcon and shell\open\command (the program to start). It is per user, so no administrator is needed. Start-
// Client writes it at each start with Join on (registerDiscordLaunch), only when the program's path changed. Under the
// test hook it goes to a test key of its own instead (discordLaunchRoot), never the player's.

// urlRegistry is the part of the registry the scheme needs: string values under HKEY_CURRENT_USER (path, value name;
// "" is the key's default value). The tests replace it (discordRegistry).
type urlRegistry interface {
	Get(path, name string) (string, bool)
	Set(path, name, value string) error
}

// discordRegistry is the registry the scheme is written to: the user's on Windows, none elsewhere; the tests set a
// memRegistry (TestMain), so no test writes the real one.
var discordRegistry urlRegistry = userRegistry{}

// discordClassesRoot is where a player's start registers the scheme; discordTestRoot (plus the test pipe's name) is
// a test hook run's own key, which nothing reads and the test script removes.
const (
	discordClassesRoot = `Software\Classes`
	discordTestRoot    = `Software\DawngateRestorationTest`
)

// discordLaunchRoot is the registry path the scheme goes under: the user's classes, or with the test hook (testPipe
// set) a key of that pipe's own.
func discordLaunchRoot(testPipe string) string {
	if testPipe == "" {
		return discordClassesRoot
	}
	return discordTestRoot + `\` + strings.TrimPrefix(testPipe, `\\.\pipe\`)
}

// registerDiscordLaunch makes discord-<appID> start exe: it writes the values that differ and says whether it wrote
// any.
func registerDiscordLaunch(reg urlRegistry, root, appID, exe string) (bool, error) {
	if appID == "" || strings.Trim(appID, "0123456789") != "" || exe == "" {
		return false, fmt.Errorf("no application id or program to register")
	}
	key := root + `\discord-` + appID
	want := []struct{ path, name, value string }{
		{key, "", "URL:Run game " + appID + " protocol"},
		{key, "URL Protocol", ""},
		{key + `\DefaultIcon`, "", exe},
		{key + `\shell\open\command`, "", `"` + exe + `"`},
	}
	changed := false
	for _, w := range want {
		if v, ok := reg.Get(w.path, w.name); ok && v == w.value {
			continue
		}
		if err := reg.Set(w.path, w.name, w.value); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// memRegistry is a registry in memory (the tests').
type memRegistry struct {
	mu sync.Mutex
	v  map[string]string
}

func (m *memRegistry) Get(path, name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.v[strings.ToLower(path)+"|"+name]
	return v, ok
}

func (m *memRegistry) Set(path, name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v == nil {
		m.v = map[string]string{}
	}
	m.v[strings.ToLower(path)+"|"+name] = value
	return nil
}

// registerLaunch registers this program for Discord's launch of the game (a player's start: the user's classes;
// the test hook: its own key) and says so when something was written.
func registerLaunch(o IO, dc discordConfig) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.Abs(exe)
	}
	if err != nil {
		fmt.Fprintf(o.Out, "  discord  Discord can't start Start-Client for a Join: %v\n", err)
		return
	}
	root := discordLaunchRoot(dc.hook)
	changed, err := registerDiscordLaunch(discordRegistry, root, dc.appID, exe)
	switch {
	case err != nil:
		fmt.Fprintf(o.Out, "  discord  Discord can't start Start-Client for a Join (HKEY_CURRENT_USER\\%s\\discord-%s: %v)\n", root, dc.appID, err)
	case changed:
		fmt.Fprintf(o.Out, "  discord  registered for Discord's Join: HKEY_CURRENT_USER\\%s\\discord-%s starts %s\n", root, dc.appID, exe)
	}
}
