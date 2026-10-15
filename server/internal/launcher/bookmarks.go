package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// The server picker's bookmarks: the servers a player can pick on the login screen, kept by Start-Client in
// Dawngate-Data\servers\bookmarks.json. Each server's trusted tunnel key stays in its own folder
// (Dawngate-Data\servers\<address>\gateway.pin, TrustedGateway), as without the picker.

// BookmarksFile is the bookmarks' file in Dawngate-Data\servers\.
const BookmarksFile = "bookmarks.json"

// BookmarksPath is the bookmarks' file of a layout.
func BookmarksPath(l setup.Layout) string { return filepath.Join(l.Root, "servers", BookmarksFile) }

// Bookmark is one server in the picker.
type Bookmark struct {
	// Tag names the server in a login name typed as <name>#<tag>: 1-16 of a-z, 0-9, _ and -, lower case, not starting
	// with -.
	Tag string `json:"tag"`
	// Name is the player's name for it (shown in the picker; the address when none was given).
	Name string `json:"name"`
	// Address is the server's IPv4 address or host name; GatewayPort its tunnel port (TCP and UDP).
	Address     string `json:"address"`
	GatewayPort int    `json:"gateway_port"`
	// LastUsed is when a login last went to it (RFC 3339, UTC; empty: never).
	LastUsed string `json:"last_used,omitempty"`
}

// Gateway is the bookmark's tunnel gateway (host:port).
func (b Bookmark) Gateway() string { return net.JoinHostPort(b.Address, strconv.Itoa(b.GatewayPort)) }

// ThisPCTag and ThisPCName are the list's fixed first entry, "This PC": the server on this PC (Start-Client runs
// it itself, or this setup folder's Start-Server while that runs; thispc.go). Its address follows which of them
// serves, so it isn't kept in the file's servers, only its last use (this_pc_last_used). A login name picks it as
// <name>#pc; it can't be removed.
const (
	ThisPCTag  = "pc"
	ThisPCName = "This PC"
)

// bookmarksJSON is the file's layout.
type bookmarksJSON struct {
	Servers        []Bookmark `json:"servers"`
	ThisPCLastUsed string     `json:"this_pc_last_used,omitempty"`
}

// Bookmarks is the list in memory and its file; safe for concurrent use. Every change is written at once.
type Bookmarks struct {
	path string
	mu   sync.Mutex
	list []Bookmark
	// thisPC is the fixed entry (nil: none; Start-Client's picker sets it, SetThisPC), thisPCUsed its last use
	// (kept in the file also while there is no entry)
	thisPC     *Bookmark
	thisPCUsed string
}

// Errors of the bookmark list.
var (
	ErrNoBookmark = errors.New("no such server in the list")
	ErrBookmarked = errors.New("that server is in the list already")
	ErrFixed      = errors.New("This PC is always in the list")
)

// LoadBookmarks reads the bookmarks' file (a missing file is an empty list; it is written at the first change).
func LoadBookmarks(path string) (*Bookmarks, error) {
	b := &Bookmarks{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return nil, err
	}
	var f bookmarksJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %v (fix or delete the file)", path, err)
	}
	if f.ThisPCLastUsed != "" {
		if _, err := time.Parse(time.RFC3339, f.ThisPCLastUsed); err != nil {
			return nil, fmt.Errorf("%s: this_pc_last_used %q: not an RFC 3339 time (fix or delete the file)", path, f.ThisPCLastUsed)
		}
		b.thisPCUsed = f.ThisPCLastUsed
	}
	for _, bm := range f.Servers {
		if bm, err = checkBookmark(bm); err != nil {
			return nil, fmt.Errorf("%s: %v (fix or delete the file)", path, err)
		}
		if bm.Tag == ThisPCTag { // This PC's tag, given by hand: the server gets a free one (written at the next change)
			bm.Tag = ""
		}
		b.list = append(b.list, bm)
	}
	for i := range b.list {
		if b.list[i].Tag == "" {
			b.list[i].Tag = b.freeTag()
		}
	}
	return b, nil
}

// SetThisPC puts the fixed entry This PC first in the list (or moves it): the server on this PC, reached at address
// and gatewayPort (0: the default). It isn't written into the file's servers.
func (b *Bookmarks) SetThisPC(address string, gatewayPort int) {
	if gatewayPort == 0 {
		gatewayPort = tunnel.DefaultPort
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.thisPC = &Bookmark{Tag: ThisPCTag, Name: ThisPCName, Address: address, GatewayPort: gatewayPort, LastUsed: b.thisPCUsed}
}

// ThisPC is the fixed entry (false: the list has none).
func (b *Bookmarks) ThisPC() (Bookmark, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.thisPC == nil {
		return Bookmark{}, false
	}
	return *b.thisPC, true
}

// all is the list with This PC first (b.mu held).
func (b *Bookmarks) all() []Bookmark {
	out := make([]Bookmark, 0, len(b.list)+1)
	if b.thisPC != nil {
		out = append(out, *b.thisPC)
	}
	return append(out, b.list...)
}

// checkBookmark checks and normalizes a bookmark (the tag lower case, the default port and name).
func checkBookmark(bm Bookmark) (Bookmark, error) {
	bm.Tag = strings.ToLower(strings.TrimSpace(bm.Tag))
	bm.Address = strings.TrimSpace(bm.Address)
	bm.Name = strings.TrimSpace(bm.Name)
	if bm.Tag != "" && !ValidTag(bm.Tag) {
		return bm, fmt.Errorf("server tag %q: use 1-16 of a-z, 0-9, _ and -, not starting with -", bm.Tag)
	}
	if err := ValidServerAddress(bm.Address); err != nil {
		return bm, err
	}
	if bm.GatewayPort == 0 {
		bm.GatewayPort = tunnel.DefaultPort
	}
	if bm.GatewayPort < 1 || bm.GatewayPort > 65535 {
		return bm, fmt.Errorf("gateway port %d: a port number 1-65535", bm.GatewayPort)
	}
	if bm.Name == "" {
		bm.Name = bm.Address
	}
	if len(bm.Name) > 64 {
		bm.Name = bm.Name[:64]
	}
	if bm.LastUsed != "" {
		if _, err := time.Parse(time.RFC3339, bm.LastUsed); err != nil {
			return bm, fmt.Errorf("last_used %q: not an RFC 3339 time", bm.LastUsed)
		}
	}
	return bm, nil
}

// ValidTag says whether s is a server tag: 1-16 of a-z, 0-9, _ and -, not starting with '-' (a login's <name>#-<tag>
// removes a server, front_list.go).
func ValidTag(s string) bool {
	if len(s) < 1 || len(s) > 16 || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// ValidServerAddress checks a bookmark's address: an IPv4 address or a host name (letters, digits, '-' and '.').
// It names a folder too (the server's trusted key), so nothing else is allowed.
func ValidServerAddress(a string) error {
	if ip := net.ParseIP(a); ip != nil {
		if ip.To4() == nil || strings.Count(a, ".") != 3 {
			return fmt.Errorf("the server address %q: an IPv4 address such as 203.0.113.5, or a host name", a)
		}
		return nil
	}
	if a == "" || len(a) > 253 || strings.HasPrefix(a, ".") || strings.HasSuffix(a, ".") || strings.Contains(a, "..") {
		return fmt.Errorf("the server address %q: an IPv4 address such as 203.0.113.5, or a host name", a)
	}
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return fmt.Errorf("the server address %q: an IPv4 address such as 203.0.113.5, or a host name", a)
		}
	}
	return nil
}

// List returns a copy of the bookmarks in their order, This PC first when the list has it.
func (b *Bookmarks) List() []Bookmark {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.all()
}

// Find looks a server up by its tag (any case), else by its address, else by its name (any case).
func (b *Bookmarks) Find(key string) (Bookmark, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.find(key)
}

func (b *Bookmarks) find(key string) (Bookmark, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Bookmark{}, false
	}
	all := b.all()
	for _, pass := range []func(Bookmark) bool{
		func(bm Bookmark) bool { return bm.Tag == strings.ToLower(key) },
		func(bm Bookmark) bool { return strings.EqualFold(bm.Address, key) },
		func(bm Bookmark) bool { return strings.EqualFold(bm.Name, key) },
	} {
		for _, bm := range all {
			if pass(bm) {
				return bm, true
			}
		}
	}
	return Bookmark{}, false
}

// Last is the server a login went to last (the first in the list, This PC when it has it, when none was used yet);
// false for an empty list.
func (b *Bookmarks) Last() (Bookmark, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	all := b.all()
	if len(all) == 0 {
		return Bookmark{}, false
	}
	best := all[0]
	for _, bm := range all[1:] {
		if bm.LastUsed > best.LastUsed { // RFC 3339 in UTC sorts as text
			best = bm
		}
	}
	return best, true
}

// Add adds a server: a free tag is given when it has none (a, b, ..., z, then s27, s28, ...). A server whose
// address is in the list already, or a tag that is taken (This PC's too), is refused (ErrBookmarked); the existing
// one is returned.
func (b *Bookmarks) Add(bm Bookmark) (Bookmark, error) {
	bm, err := checkBookmark(bm)
	if err != nil {
		return bm, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if bm.Tag == ThisPCTag && b.thisPC == nil {
		return bm, fmt.Errorf("%w: the tag %s is This PC's", ErrBookmarked, ThisPCTag)
	}
	for _, old := range b.all() {
		if strings.EqualFold(old.Address, bm.Address) {
			return old, fmt.Errorf("%w (%s, tag %s)", ErrBookmarked, old.Address, old.Tag)
		}
		if bm.Tag != "" && old.Tag == bm.Tag {
			return old, fmt.Errorf("%w: the tag %s is %s's", ErrBookmarked, old.Tag, old.Address)
		}
	}
	if bm.Tag == "" {
		bm.Tag = b.freeTag()
	}
	b.list = append(b.list, bm)
	if err := b.save(); err != nil {
		b.list = b.list[:len(b.list)-1]
		return bm, err
	}
	return bm, nil
}

func (b *Bookmarks) freeTag() string {
	taken := map[string]bool{ThisPCTag: true}
	for _, bm := range b.list {
		taken[bm.Tag] = true
	}
	for c := 'a'; c <= 'z'; c++ {
		if !taken[string(c)] {
			return string(c)
		}
	}
	for n := 27; ; n++ {
		if t := "s" + strconv.Itoa(n); !taken[t] {
			return t
		}
	}
}

// Remove removes a server (by tag, address or name, as Find). Its trusted key stays in its folder: a server added
// again keeps it, so a changed key is still noticed. This PC can't be removed (ErrFixed).
func (b *Bookmarks) Remove(key string) (Bookmark, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bm, ok := b.find(key)
	if !ok {
		return bm, ErrNoBookmark
	}
	if b.thisPC != nil && bm.Tag == ThisPCTag {
		return bm, ErrFixed
	}
	old := b.list
	b.list = nil
	for _, x := range old {
		if x.Tag != bm.Tag {
			b.list = append(b.list, x)
		}
	}
	if err := b.save(); err != nil {
		b.list = old
		return bm, err
	}
	return bm, nil
}

// Used records a login to the server with this tag at t.
func (b *Bookmarks) Used(tag string, t time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	when := t.UTC().Format(time.RFC3339)
	if b.thisPC != nil && tag == ThisPCTag {
		b.thisPCUsed, b.thisPC.LastUsed = when, when
		return b.save()
	}
	for i := range b.list {
		if b.list[i].Tag == tag {
			b.list[i].LastUsed = when
			return b.save()
		}
	}
	return ErrNoBookmark
}

// save writes the file (b.mu held): a temporary file renamed over the old one, so a crash leaves one or the other.
func (b *Bookmarks) save() error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	list := b.list
	if list == nil {
		list = []Bookmark{}
	}
	raw, err := json.MarshalIndent(bookmarksJSON{Servers: list, ThisPCLastUsed: b.thisPCUsed}, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// sortedTags lists the tags (for messages).
func (b *Bookmarks) sortedTags() []string {
	var tags []string
	for _, bm := range b.List() {
		tags = append(tags, bm.Tag)
	}
	sort.Strings(tags)
	return tags
}
