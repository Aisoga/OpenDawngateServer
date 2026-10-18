package update

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The suffixes of Replace's temporary files beside the release's files: the new file before it takes the old one's
// place, and the old one until it is deleted (both removed by CleanOld when a start was cut short).
const (
	newSuffix = ".upd-new"
	oldSuffix = ".upd-old"
)

// Result is what Replace did.
type Result struct {
	Installed []string // the release's files written (slash paths), BUILD.txt last
	Removed   []string // files of the old release (its BUILD.txt) that the new one doesn't have
	OldExe    string   // the running program's new name (Start-Client.old.exe), "" when it wasn't replaced
}

// Replace puts the checked release in root (its build b) into the player's folder dir: every file of b and its
// BUILD.txt, and it removes the files the old release (dir's BUILD.txt) listed that the new one doesn't. running is the
// running Start-Client.exe: Windows lets a running program be renamed but not replaced or deleted, so it becomes
// Start-Client.old.exe (CleanOld deletes it at the next start). The player's files (Protected) are never touched.
//
// It works in two passes so that a failure leaves the old release in place: first every new file is copied in beside
// its place (name.upd-new, its SHA-256 checked again), then each old file is renamed away and the new one renamed in;
// a failed rename undoes the ones before it.
func Replace(dir, root string, b Build, running string) (Result, error) {
	type step struct {
		rel, dest, staged, backup string
		remove, running           bool
		moved, placed             bool
	}
	var steps []*step
	files := append(b.Paths(), BuildFile)
	for _, rel := range files {
		if Protected(rel) {
			return Result{}, fmt.Errorf("the release would replace %s, which is the player's", rel)
		}
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		steps = append(steps, &step{rel: rel, dest: dest, staged: dest + newSuffix, running: sameFile(dest, running)})
	}
	if old, err := ReadBuild(filepath.Join(dir, BuildFile)); err == nil {
		for _, rel := range old.Paths() {
			dest := filepath.Join(dir, filepath.FromSlash(rel))
			if _, keep := b.Files[rel]; keep || Protected(rel) || !isFile(dest) {
				continue
			}
			steps = append(steps, &step{rel: rel, dest: dest, remove: true, running: sameFile(dest, running)})
		}
	}
	unstage := func() {
		for _, s := range steps {
			if !s.remove {
				os.Remove(s.staged)
			}
		}
	}

	// 1. the new files beside their places
	for _, s := range steps {
		if s.remove {
			continue
		}
		want := b.Files[s.rel] // "" for BUILD.txt
		if err := copyChecked(filepath.Join(root, filepath.FromSlash(s.rel)), s.staged, want); err != nil {
			unstage()
			return Result{}, fmt.Errorf("%s: %v", s.rel, err)
		}
	}

	// 2. the old files away, the new ones in
	undo := func() {
		for i := len(steps) - 1; i >= 0; i-- {
			s := steps[i]
			if s.placed {
				os.Remove(s.dest)
			}
			if s.moved {
				os.Rename(s.backup, s.dest)
			}
		}
		unstage()
	}
	var res Result
	for _, s := range steps {
		if isFile(s.dest) {
			s.backup = s.dest + oldSuffix
			if s.running {
				s.backup = oldExeName(s.dest)
			}
			os.Remove(s.backup)
			if err := os.Rename(s.dest, s.backup); err != nil {
				undo()
				return Result{}, fmt.Errorf("%s can't be moved aside (is it in use?): %v", s.rel, err)
			}
			s.moved = true
		}
		if s.remove {
			continue
		}
		if err := os.Rename(s.staged, s.dest); err != nil {
			undo()
			return Result{}, fmt.Errorf("%s can't be put in place: %v", s.rel, err)
		}
		s.placed = true
	}

	// 3. the old files deleted (the running program's at the next start)
	for _, s := range steps {
		switch {
		case s.remove:
			res.Removed = append(res.Removed, s.rel)
		default:
			res.Installed = append(res.Installed, s.rel)
		}
		if !s.moved {
			continue
		}
		if s.running {
			res.OldExe = filepath.Base(s.backup)
			continue
		}
		os.Remove(s.backup) // a leftover is CleanOld's
	}
	return res, nil
}

// oldExeName is the name the running program is renamed to: Start-Client.old.exe, or Start-Client.<n>.old.exe while
// an earlier one still runs (it can't be deleted then).
func oldExeName(exe string) string {
	base := strings.TrimSuffix(exe, filepath.Ext(exe))
	for i := 0; i < 10; i++ {
		name := base + ".old.exe"
		if i > 0 {
			name = fmt.Sprintf("%s.%d.old.exe", base, i)
		}
		if err := os.Remove(name); err == nil || errors.Is(err, fs.ErrNotExist) {
			return name
		}
	}
	return fmt.Sprintf("%s.%d.old.exe", base, os.Getpid())
}

// CleanOld deletes what an update left in the player's folder dir: the old programs (Start-*.old.exe; one that still
// runs stays until a later start) and Replace's temporary files of a start that was cut short. It returns the names it
// deleted.
func CleanOld(dir string) []string {
	var gone []string
	olds, _ := filepath.Glob(filepath.Join(dir, "Start-*.old.exe"))
	for _, p := range olds {
		if os.Remove(p) == nil {
			gone = append(gone, filepath.Base(p))
		}
	}
	clean := func(p string) {
		if (strings.HasSuffix(p, newSuffix) || strings.HasSuffix(p, oldSuffix)) && os.Remove(p) == nil {
			rel, _ := filepath.Rel(dir, p)
			gone = append(gone, filepath.ToSlash(rel))
		}
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				clean(filepath.Join(dir, e.Name()))
			}
		}
	}
	filepath.WalkDir(filepath.Join(dir, "host"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			clean(p)
		}
		return nil
	})
	return gone
}

// copyChecked copies src to dest (made anew), and checks the copy's SHA-256 when want isn't "".
func copyChecked(src, dest, want string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && want != "" {
		var got string
		if got, err = fileSHA256(dest); err == nil && got != want {
			err = fmt.Errorf("the copy's SHA-256 is %s, not %s", got, want)
		}
	}
	if err != nil {
		os.Remove(dest)
	}
	return err
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func sameFile(a, b string) bool {
	if b == "" {
		return false
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err1 == nil && err2 == nil {
		return os.SameFile(fa, fb)
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
