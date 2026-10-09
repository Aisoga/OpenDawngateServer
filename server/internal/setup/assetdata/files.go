package assetdata

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"dawngate/server/internal/setup/snap"
)

// File is one AssetData .bin file in an archive.
type File struct {
	FileName string // "alpha.CollectionItem.bin"
	Name     string // the instance name, "alpha"
	Class    string // the class, lower case: "collectionitem" ("catalog" for catalog_153.bin)
	Entry    snap.Entry
}

// Set is AssetData.snap's AssetData/Binary/*.bin files by class, in the order assetdata.py reads the
// folder snap.py extracts them to (names sorted).
type Set struct {
	arch    *snap.Archive
	byClass map[string][]File
}

var catalogName = regexp.MustCompile(`(?i)\Acatalog_\p{Nd}+\z`) // \d is Unicode in Python

// classOf splits a file name into instance name and class as assetdata.py's class_of does: the class is
// everything after the first dot, as the client takes it (strstr, 0x008CCFB0); catalog_<n>.bin is the
// class Catalog.
func classOf(fn string) (name, class string) {
	base := fn
	if strings.HasSuffix(strings.ToLower(fn), ".bin") {
		base = fn[:len(fn)-4]
	}
	if catalogName.MatchString(base) {
		return base, "catalog"
	}
	name, class, _ = strings.Cut(base, ".")
	return name, strings.ToLower(class)
}

// FromSnap lists an archive's AssetData files (AssetData.snap). As snap.py extract would, it refuses an
// archive with an unsafe entry path (absolute, "..", a drive), and two .bin names that differ only in
// case (Windows would have stored one file for both).
func FromSnap(a *snap.Archive) (*Set, error) {
	s := &Set{arch: a, byClass: map[string][]File{}}
	var files []File
	seen := map[string]string{}
	for _, e := range a.Entries {
		var parts []string
		for _, p := range strings.FieldsFunc(e.Name, func(r rune) bool { return r == '/' || r == '\\' }) {
			if p != "." {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("assetdata: unsafe entry path %q", e.Name)
		}
		for _, p := range parts {
			if p == ".." || strings.Contains(p, ":") {
				return nil, fmt.Errorf("assetdata: unsafe entry path %q", e.Name)
			}
		}
		if len(parts) != 3 || !strings.EqualFold(parts[0], "AssetData") || !strings.EqualFold(parts[1], "Binary") ||
			!strings.HasSuffix(strings.ToLower(parts[2]), ".bin") {
			continue
		}
		fn := parts[2]
		if strings.ContainsFunc(fn, func(r rune) bool { return r < 0x20 || strings.ContainsRune(`<>"|?*`, r) }) {
			return nil, fmt.Errorf("assetdata: %q: a name Windows can't store", e.Name)
		}
		if prev, dup := seen[strings.ToLower(fn)]; dup {
			return nil, fmt.Errorf("assetdata: %q and %q differ only in case", prev, e.Name)
		}
		seen[strings.ToLower(fn)] = e.Name
		name, class := classOf(fn)
		files = append(files, File{FileName: fn, Name: name, Class: class, Entry: e})
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].FileName < files[j].FileName })
	for _, f := range files {
		s.byClass[f.Class] = append(s.byClass[f.Class], f)
	}
	return s, nil
}

// Classes are the classes with files (lower case), sorted.
func (s *Set) Classes() []string {
	var out []string
	for c := range s.byClass {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Files are a class's files (any case), in order.
func (s *Set) Files(class string) []File { return s.byClass[strings.ToLower(class)] }

// ErrNoClass is wrapped (with fs.ErrNotExist) when a class has no files or no type table.
var ErrNoClass = fmt.Errorf("no such class: %w", fs.ErrNotExist)

// Instances parses every file of a class, as assetdata.py dump does: the objects by instance name in
// file order (an object with bad references carries "_badref": [[path, text, why], ...]), and the files
// that didn't parse with their messages. The error wraps ErrNoClass when the class has no files or no
// type table (dump writes no <Class>.json then); any other error stops the run, as an exception stopped
// assetdata.py (a payload that doesn't decode stopped snap.py extract).
func (s *Set) Instances(ts *Types, class string, loc *Locale) (instances, errs *Object, t *Type, err error) {
	files := s.Files(class)
	t = ts.Type(class)
	if len(files) == 0 || t == nil {
		return nil, nil, nil, fmt.Errorf("assetdata: %s: %w", class, ErrNoClass)
	}
	instances, errs = NewObject(), NewObject()
	for _, f := range files {
		data, err := s.arch.Read(f.Entry)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("assetdata: %w", err)
		}
		obj, refs, err := ts.Parse(t, data, loc)
		if err != nil {
			if errFatal(err) {
				return nil, nil, nil, fmt.Errorf("assetdata: %s: %w", f.FileName, err)
			}
			errs.Set(f.Name, err.Error())
			continue
		}
		if len(refs) > 0 {
			bad := make([]any, len(refs))
			for i, r := range refs {
				bad[i] = []any{r[0], r[1], r[2]}
			}
			obj.Set("_badref", bad)
		}
		instances.Set(f.Name, obj)
	}
	return instances, errs, t, nil
}

// Dump is a class's <Class>.json as assetdata.py dump writes it ({"class", "count", "errors",
// "instances"}; compact, where dump indents).
func (s *Set) Dump(ts *Types, class string, loc *Locale) ([]byte, error) {
	inst, errs, t, err := s.Instances(ts, class, loc)
	if err != nil {
		return nil, err
	}
	doc := NewObject()
	doc.Set("class", t.Name)
	doc.Set("count", int64(inst.Len()))
	doc.Set("errors", errs)
	doc.Set("instances", inst)
	return AppendJSON(nil, doc), nil
}

// IsNoClass reports whether err says a class has no files or no type table.
func IsNoClass(err error) bool { return errors.Is(err, ErrNoClass) }
