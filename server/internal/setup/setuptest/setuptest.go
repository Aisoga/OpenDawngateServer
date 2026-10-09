// Package setuptest finds the repository and the game files for the setup packages' tests.
// Tests that need a game file skip when it isn't there; nothing here writes anything.
package setuptest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is the checkout the test runs in (the folder that holds server/go.mod), or "" when it isn't found.
func Repo() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "server", "go.mod")); err == nil && st.Mode().IsRegular() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// MainCheckout is the main checkout, where the gitignored inputs live (client/, Dawngate.zip, .venv): the checkout
// itself, or for a git worktree the folder its .git file points into (".../.git/worktrees/<name>").
func MainCheckout() string {
	repo := Repo()
	if repo == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(repo, ".git"))
	if err != nil { // a directory (the main checkout itself) or no git at all
		return repo
	}
	line := strings.TrimSpace(string(b))
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return repo
	}
	gitdir = filepath.ToSlash(strings.TrimSpace(gitdir))
	if i := strings.Index(gitdir, "/.git/worktrees/"); i >= 0 {
		return filepath.FromSlash(gitdir[:i])
	}
	return repo
}

// Find returns the first of paths (absolute, or relative to the main checkout) that is a file, or "".
func Find(paths ...string) string {
	main := MainCheckout()
	for _, p := range paths {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			if main == "" {
				continue
			}
			p = filepath.Join(main, filepath.FromSlash(p))
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// GameFile finds a file of the untouched build: the environment variable env (if set), else the main checkout's
// client/ia_Dawngate/Dawngate/<rel> (read only). It skips the test when there is none.
func GameFile(t testing.TB, env, rel string) string {
	t.Helper()
	p := Find(os.Getenv(env), "client/ia_Dawngate/Dawngate/"+rel)
	if p == "" {
		t.Skipf("no %s (set %s, or run in a checkout whose main checkout has client/ia_Dawngate)", rel, env)
	}
	return p
}
