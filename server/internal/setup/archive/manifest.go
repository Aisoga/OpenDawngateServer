package archive

//go:generate go run gen_manifest.go

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// File is one file of a build: its path inside the archive ("Dawngate/game/Dawngate.exe"), size and SHA-256 (hex).
type File struct {
	Path   string
	Size   int64
	SHA256 string
}

// CopyPath is the file's path inside a copy's Dawngate folder ("game/Dawngate.exe"; setup_all.py's in_copy).
func (f File) CopyPath() string {
	_, rest, _ := strings.Cut(f.Path, "/")
	return rest
}

// Manifest is a build's file list.
type Manifest struct {
	files []File          // sorted by Path
	byKey map[string]File // Path -> File
}

// NewManifest makes a manifest from a file list (tests use synthetic ones).
func NewManifest(files []File) *Manifest {
	m := &Manifest{files: append([]File(nil), files...), byKey: map[string]File{}}
	sort.Slice(m.files, func(i, j int) bool { return m.files[i].Path < m.files[j].Path })
	for _, f := range m.files {
		m.byKey[f.Path] = f
	}
	return m
}

// Build854 is build 1.0.903.854's manifest (Dawngate.zip's 167 files), generated from docs/file_manifest.csv.
func Build854() *Manifest { return NewManifest(build854) }

// Files returns the files, sorted by path.
func (m *Manifest) Files() []File { return append([]File(nil), m.files...) }

// Len is the number of files.
func (m *Manifest) Len() int { return len(m.files) }

// Lookup finds a file by its archive path ("Dawngate/game/Dawngate.exe") or its copy path ("game/Dawngate.exe").
func (m *Manifest) Lookup(path string) (File, bool) {
	if f, ok := m.byKey[path]; ok {
		return f, true
	}
	f, ok := m.byKey[Top+"/"+path]
	return f, ok
}

// TotalSize is the sum of the files' sizes.
func (m *Manifest) TotalSize() int64 {
	var n int64
	for _, f := range m.files {
		n += f.Size
	}
	return n
}

// ParseManifestCSV reads docs/file_manifest.csv's build-854 side: the rows with a size_A (setup_all.py's load_manifest).
func ParseManifestCSV(r io.Reader) (*Manifest, error) {
	rows, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty manifest")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, name := range []string{"path", "size_A", "sha256_A"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("manifest has no column %q", name)
		}
	}
	var files []File
	for _, r := range rows[1:] {
		if r[col["size_A"]] == "" {
			continue
		}
		size, err := strconv.ParseInt(r[col["size_A"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: size %q: %v", r[col["path"]], r[col["size_A"]], err)
		}
		files = append(files, File{Path: r[col["path"]], Size: size, SHA256: r[col["sha256_A"]]})
	}
	return NewManifest(files), nil
}
