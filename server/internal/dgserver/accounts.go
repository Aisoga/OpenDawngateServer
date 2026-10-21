package dgserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/rankedlogs"
	"dawngate/server/internal/store"
)

// accountCommands are dgserver's one-shot account commands: each opens the data folder's account database, does
// its one thing and exits; the server may be running meanwhile (SQLite's WAL lets two processes share the database,
// and the server reads accounts from it at each login). Start-Server.exe passes them on with its own data folder.
type accountCommands struct {
	makeInvite    bool
	inviteUses    int
	inviteDays    float64
	inviteNote    string
	listInvites   bool
	revokeInvite  int64
	createAccount string
	resetPassword string
	accountStatus string // <name>=<status>
	makeAdmin     string // the admin role
	removeAdmin   string
	importLogs    stringList     // --import-ranked-logs: log folders and files
	apply, force  bool           // --import-ranked-logs: write (else a dry run); again after an earlier import
	importLoc     *time.Location // tests: the log times' zone (default the local one)
}

// any reports whether a command was given.
func (c accountCommands) any() bool {
	return c.makeInvite || c.listInvites || c.revokeInvite != 0 || c.createAccount != "" || c.resetPassword != "" ||
		c.accountStatus != "" || c.makeAdmin != "" || c.removeAdmin != "" || len(c.importLogs) > 0 || c.apply || c.force
}

// AccountCommandFlags are the flags of the one-shot account commands, for Start-Server.exe to recognize and pass on.
var AccountCommandFlags = []string{"make-invite", "invite-uses", "invite-days", "invite-note", "list-invites",
	"revoke-invite", "create-account", "reset-password", "account-status", "make-admin", "remove-admin",
	"import-ranked-logs", "apply", "force"}

// run carries out the account commands on the database in dataDir, writing what it did (and the new passwords and
// codes, which are shown only here) to out. It returns the exit code. Every command that changes something writes an
// entry into the audit log (never a password or a code), as the admin page's actions do.
func (c accountCommands) run(dataDir string, out, errOut io.Writer, now time.Time) int {
	if dataDir == "" {
		fmt.Fprintln(errOut, "dgserver: the account commands need a data folder (--data)")
		return 2
	}
	st, err := store.Open(filepath.Join(dataDir, store.FileName))
	if err != nil {
		fmt.Fprintln(errOut, "dgserver:", err)
		return 1
	}
	defer st.Close()
	fail := func(err error) int {
		fmt.Fprintln(errOut, "dgserver:", err)
		return 1
	}
	audit := func(action, target, detail string) {
		if err := accounts.Audit(st, accounts.CommandLine, action, target, detail, now); err != nil {
			fmt.Fprintln(errOut, "dgserver: the audit log:", err)
		}
	}
	if c.createAccount != "" {
		a, pw, err := accounts.CreateAccount(st, c.createAccount, now)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "created account %d %q (persona %q) with the password\n    %s\nGive it to the player; it is shown only now.\n",
			a.UserID, a.Email, a.Personas[0].Name, pw)
		audit("create account", accounts.AccountTarget(a.UserID, a.Email), "a random password")
	}
	if c.resetPassword != "" {
		cr, pw, err := accounts.ResetPassword(st, c.resetPassword)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "account %d %q has a new password:\n    %s\nGive it to the player; it is shown only now, and the old one no longer works.\n",
			cr.UserID, cr.Name, pw)
		audit("reset password", accounts.AccountTarget(cr.UserID, cr.Name), "a new random password")
	}
	if c.accountStatus != "" {
		name, status, ok := strings.Cut(c.accountStatus, "=")
		if !ok {
			return fail(errors.New("--account-status takes <login name>=<active|disabled|banned|deleted>"))
		}
		s, err := store.ParseStatus(status)
		if err != nil {
			return fail(err)
		}
		cr, err := accounts.SetStatus(st, name, s)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "account %d %q: %s (was %s)\n", cr.UserID, cr.Name, s, cr.Status)
		audit("account status", accounts.AccountTarget(cr.UserID, cr.Name), fmt.Sprintf("%s -> %s", cr.Status, s))
	}
	if c.makeInvite {
		if c.inviteUses < 1 {
			return fail(errors.New("--invite-uses must be at least 1"))
		}
		if c.inviteDays < 0 {
			return fail(errors.New("--invite-days must be 0 (never expires) or more"))
		}
		valid := time.Duration(c.inviteDays * float64(24*time.Hour))
		code, inv, err := accounts.NewInvite(st, c.inviteUses, valid, c.inviteNote, now)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "invite code %d for %d account(s), %s:\n    %s\nIt is shown only now (the server keeps only its hash).\n",
			inv.ID, inv.Uses, expiry(inv), code)
		audit("invite create", fmt.Sprintf("invite %d", inv.ID), fmt.Sprintf("%d use(s), %s, note %q", inv.Uses, expiry(inv), inv.Note))
	}
	if c.revokeInvite != 0 {
		if err := st.RevokeInvite(c.revokeInvite); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "invite code %d revoked\n", c.revokeInvite)
		audit("invite revoke", fmt.Sprintf("invite %d", c.revokeInvite), "")
	}
	for _, x := range []struct {
		name  string
		admin bool
	}{{c.makeAdmin, true}, {c.removeAdmin, false}} {
		if x.name == "" {
			continue
		}
		cr, err := accounts.SetAdmin(st, x.name, x.admin)
		if err != nil {
			return fail(err)
		}
		role := store.RolePlayer
		if x.admin {
			role = store.RoleAdmin
		}
		fmt.Fprintf(out, "account %d %q: %s (was %s)\n", cr.UserID, cr.Name, role, cr.Role)
		if x.admin {
			fmt.Fprintln(out, "It logs into the website's admin page (/admin on the website) with its login name and password.")
		}
		audit("account role", accounts.AccountTarget(cr.UserID, cr.Name), fmt.Sprintf("%s -> %s", cr.Role, role))
	}
	if (c.apply || c.force) && len(c.importLogs) == 0 {
		return fail(errors.New("--apply and --force go with --import-ranked-logs <logs folder>"))
	}
	if len(c.importLogs) > 0 {
		sum, err := rankedlogs.Run(st, rankedlogs.Options{Paths: c.importLogs, Loc: c.loc(), Apply: c.apply, Force: c.force,
			Now: now}, out)
		if err != nil {
			return fail(err)
		}
		if sum.Applied {
			audit("import ranked games", "", fmt.Sprintf("%d PvP or custom games (%d wins) of %d accounts from %d log files",
				sum.Games, sum.Wins, sum.Accounts, sum.Imported))
		}
	}
	if c.listInvites {
		all, err := st.Invites()
		if err != nil {
			return fail(err)
		}
		if len(all) == 0 {
			fmt.Fprintln(out, "no invite codes")
		}
		for _, i := range all {
			state := fmt.Sprintf("%d of %d left", i.Left(now), i.Uses)
			if i.Revoked {
				state = "revoked"
			}
			note := ""
			if i.Note != "" {
				note = " (" + i.Note + ")"
			}
			fmt.Fprintf(out, "invite %d: made %s, %s, used %d, %s%s\n", i.ID, i.Created.Format("2006-01-02 15:04 UTC"), expiry(i),
				i.Used, state, note)
		}
	}
	return 0
}

// loc is the zone the import reads the log times in: this machine's (the server that wrote them ran here), or the
// tests' fixed one.
func (c accountCommands) loc() *time.Location {
	if c.importLoc != nil {
		return c.importLoc
	}
	return time.Local
}

// stringList is a flag that may be given more than once.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(v string) error {
	if v == "" {
		return errors.New("an empty path")
	}
	*l = append(*l, v)
	return nil
}

func expiry(i store.Invite) string {
	if i.Expires.IsZero() {
		return "never expires"
	}
	return "valid until " + i.Expires.UTC().Format("2006-01-02 15:04 UTC")
}

// logName is a dgserver log file's name (logging.Open).
var logName = regexp.MustCompile(`^\d{8}-\d{6}\.log$`)

// pruneLogs deletes what is older than days days (log retention, --log-days): dgserver's own log files in logDir
// (by their names' pattern; nothing else there) and the match hosts' folders in hostsDir ("" = none) whose newest file
// is that old. It returns what it removed, for the log. days <= 0 keeps everything.
func pruneLogs(logDir, hostsDir string, days int, now time.Time) (removed []string) {
	if days <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !logName.MatchString(e.Name()) {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
				p := filepath.Join(logDir, e.Name())
				if os.Remove(p) == nil {
					removed = append(removed, p)
				}
			}
		}
	}
	if hostsDir == "" {
		return removed
	}
	entries, err := os.ReadDir(hostsDir)
	if err != nil {
		return removed
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(hostsDir, e.Name())
		if newest(p).Before(cutoff) && os.RemoveAll(p) == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// newest is the latest modification time in a folder tree (the folder's own included).
func newest(dir string) time.Time {
	var t time.Time
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
		return nil
	})
	return t
}

// pruneEvery is how often a running server applies --log-days again.
const pruneEvery = 6 * time.Hour

// describeRemoved is pruneLogs' result for the log.
func describeRemoved(removed []string) string {
	if len(removed) == 0 {
		return "nothing older"
	}
	return strconv.Itoa(len(removed)) + " removed: " + strings.Join(removed, ", ")
}
