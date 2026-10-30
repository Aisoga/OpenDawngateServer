// Package depot reads a game copy's files as the client finds them: every game/Data/*.snap archive mounted in the
// directory's order (alphabetical without regard to case), a path two archives share answered by the last one, and
// paths matched without regard to case or slash direction. Only the archives' indexes are read at Open; an entry's
// bytes are read and decompressed when asked for.
package depot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"dawngate/server/internal/setup/snap"
)

// Depot is a copy's mounted archives.
type Depot struct {
	archives []string // the archive files in mount order
	entries  map[string]ref
	all      []ref // every archive's entries in mount order
	mu       sync.Mutex
	files    map[int]*os.File
}

type ref struct {
	archive int
	e       snap.Entry
}

// Key is a path's lookup key: lower case, forward slashes.
func Key(path string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimLeft(path, `/\`), `\`, "/"))
}

// Open mounts the copy's archives (copyDir is the folder holding game/Data).
func Open(copyDir string) (*Depot, error) {
	dir := filepath.Join(copyDir, "game", "Data")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".snap") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("depot: no .snap archives in %s", dir)
	}
	sort.SliceStable(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	d := &Depot{entries: map[string]ref{}, files: map[int]*os.File{}}
	for k, n := range names {
		path := filepath.Join(dir, n)
		list, err := readIndex(path)
		if err != nil {
			return nil, fmt.Errorf("depot: %s: %v", n, err)
		}
		d.archives = append(d.archives, path)
		for _, e := range list {
			d.entries[Key(e.Name)] = ref{k, e}
			d.all = append(d.all, ref{k, e})
		}
	}
	return d, nil
}

// File is one archive's entry; Files lists every one, also those a later archive hides.
type File struct {
	Archive string // the archive's file name
	Name    string // the entry's path as the archive spells it
	Size    int64  // its size unpacked
	r       ref
}

// Files lists every entry of every archive in mount order (an archive's entries in its index order).
func (d *Depot) Files() []File {
	out := make([]File, len(d.all))
	for i, r := range d.all {
		out[i] = File{Archive: filepath.Base(d.archives[r.archive]), Name: r.e.Name, Size: int64(r.e.Size), r: r}
	}
	return out
}

// Head returns at least the first n bytes of a listed entry (all of it when shorter), reading and unpacking only
// as much as that takes: a file's header without its body.
func (d *Depot) Head(f File, n int) ([]byte, error) {
	r := f.r
	raw, err := d.raw(r, min(int64(r.e.StoredSize), int64(n+n/2+1024)))
	if err != nil {
		return nil, fmt.Errorf("depot: %s: %v", f.Name, err)
	}
	if r.e.Stored() {
		return raw, nil
	}
	out, err := snap.DecodePrefix(raw, n)
	if err != nil {
		return nil, fmt.Errorf("depot: %s: %v", f.Name, err)
	}
	return out, nil
}

// raw reads the first size stored bytes of an entry.
func (d *Depot) raw(r ref, size int64) ([]byte, error) {
	d.mu.Lock()
	f := d.files[r.archive]
	if f == nil {
		var err error
		if f, err = os.Open(d.archives[r.archive]); err != nil {
			d.mu.Unlock()
			return nil, err
		}
		d.files[r.archive] = f
	}
	d.mu.Unlock()
	raw := make([]byte, size)
	if _, err := f.ReadAt(raw, int64(r.e.Offset)); err != nil {
		return nil, err
	}
	return raw, nil
}

// readIndex reads an archive's header, index and name table only.
func readIndex(path string) ([]snap.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	head := make([]byte, snap.HeaderSize)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:4]) != snap.Magic {
		return nil, errors.New("not a .snap archive")
	}
	le := binary.LittleEndian
	count, namesSize := int64(le.Uint32(head[8:])), int64(le.Uint32(head[12:]))
	tail := count*snap.EntrySize + namesSize
	if tail > st.Size()-snap.HeaderSize {
		return nil, errors.New("the index exceeds the file")
	}
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, st.Size()-tail); err != nil {
		return nil, err
	}
	names := buf[count*snap.EntrySize:]
	indexOff := uint32(st.Size() - tail)
	out := make([]snap.Entry, count)
	for i := range out {
		at := buf[i*snap.EntrySize:]
		e := snap.Entry{Index: i, NameOffset: le.Uint32(at), Size: le.Uint32(at[4:]), StoredSize: le.Uint32(at[8:]),
			Offset: le.Uint32(at[24:]), Flags: le.Uint32(at[28:])}
		if int64(e.NameOffset) >= int64(len(names)) {
			return nil, fmt.Errorf("entry %d: name outside the table", i)
		}
		end := strings.IndexByte(string(names[e.NameOffset:]), 0)
		if end < 0 {
			return nil, fmt.Errorf("entry %d: name without an end", i)
		}
		e.Name = string(names[e.NameOffset : int(e.NameOffset)+end])
		if e.Offset < snap.HeaderSize || e.Offset+e.StoredSize > indexOff || e.Offset+e.StoredSize < e.Offset {
			return nil, fmt.Errorf("entry %d (%s): data outside the data area", i, e.Name)
		}
		out[i] = e
	}
	return out, nil
}

// Has reports whether a path is in some archive.
func (d *Depot) Has(path string) bool {
	_, ok := d.entries[Key(path)]
	return ok
}

// Archive names the archive file a path is read from ("" when none has it).
func (d *Depot) Archive(path string) string {
	r, ok := d.entries[Key(path)]
	if !ok {
		return ""
	}
	return filepath.Base(d.archives[r.archive])
}

// Read returns a path's bytes from the last archive holding it; fs.ErrNotExist when none does.
func (d *Depot) Read(path string) ([]byte, error) {
	r, ok := d.entries[Key(path)]
	if !ok {
		return nil, fmt.Errorf("depot: %s: %w", path, fs.ErrNotExist)
	}
	return d.readRef(r, path)
}

// ReadFile returns a listed entry's bytes (from its own archive, even when a later one hides it).
func (d *Depot) ReadFile(f File) ([]byte, error) { return d.readRef(f.r, f.Name) }

func (d *Depot) readRef(r ref, path string) ([]byte, error) {
	raw, err := d.raw(r, int64(r.e.StoredSize))
	if err != nil {
		return nil, fmt.Errorf("depot: %s: %v", path, err)
	}
	if r.e.Stored() {
		return raw, nil
	}
	out, err := snap.Decode(raw, int64(r.e.Size))
	if err != nil {
		return nil, fmt.Errorf("depot: %s: %v", path, err)
	}
	return out, nil
}

// Paths lists the mounted paths (lookup keys) with a prefix, sorted.
func (d *Depot) Paths(prefix string) []string {
	prefix = Key(prefix)
	var out []string
	for k := range d.entries {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Close closes the archives opened by Read.
func (d *Depot) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, f := range d.files {
		f.Close()
		delete(d.files, k)
	}
	return nil
}
