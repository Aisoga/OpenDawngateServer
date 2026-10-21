package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dawngate/server/internal/update"
	"dawngate/server/internal/version"
)

// UpdateTestEnv is the release test's hook: a fake of GitHub's API and downloads on a loopback address
// (http://127.x.y.z:port; any other address is refused). With it the update check also runs with --test-lane and
// without a console (the answer is read from the input). A player never sets it.
const UpdateTestEnv = "DAWNGATE_TEST_UPDATE_URL"

// UpdatedFromEnv is set for the Start-Client that an update started: the version it replaced. That start asks no
// update question.
const UpdatedFromEnv = "DAWNGATE_UPDATED_FROM"

// UpdateDir is the folder in Dawngate-Data where an update's zip is downloaded and unpacked.
const UpdateDir = "update"

// The update's seams for the tests: this build's version, the running program, how the new Start-Client is asked for
// its version and started.
var (
	updateOwnVersion = version.String
	updateRunning    = os.Executable
	updateExeVersion = exeVersion
	updateStart      = startUpdated
	updateSource     = update.GitHub
)

// updateCheckSetting reads update_check = (Start-Client.ini): on (the default, also "") or off.
func updateCheckSetting(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("update_check = %s: use on or off", v)
}

// updateOptions are the start's facts that decide whether Start-Client asks for a newer release.
type updateOptions struct {
	args                             []string // the arguments, for the new Start-Client
	check, setupOnly, nonInteractive bool
	lane                             string
	setting                          string // Start-Client.ini update_check
	root                             string // Dawngate-Data
}

// offerUpdate is the update at Start-Client's start: only a numbered release (v0.6) asks, and only in a console
// without --check, --setup-only, --non-interactive or --test-lane and with update_check on. It asks GitHub once for the
// latest release (a short timeout; no answer: it goes on), and when that one is newer asks the player. Yes: the release
// is downloaded into Dawngate-Data\update, checked and installed (update.Updater), and the new Start-Client is started
// with the same arguments; ran is true then and code its exit code. Anything else goes on with this version.
func offerUpdate(o IO, in *bufio.Reader, u updateOptions) (code int, ran bool, err error) {
	on, err := updateCheckSetting(u.setting)
	if err != nil {
		return 0, false, err
	}
	if from := os.Getenv(UpdatedFromEnv); from != "" {
		fmt.Fprintf(o.Out, "  update   updated from %s to %s\n", from, updateOwnVersion())
		return 0, false, nil
	}
	if u.check || u.setupOnly || u.nonInteractive || !on {
		return 0, false, nil
	}
	ua := "OpenDawngateServer-Start-Client/" + updateOwnVersion()
	src := updateSource(ua)
	if base := os.Getenv(UpdateTestEnv); base != "" {
		if src, err = update.Loopback(base, ua); err != nil {
			fmt.Fprintf(o.Out, "  update   %s: %v (no update check)\n", UpdateTestEnv, err)
			return 0, false, nil
		}
	} else if u.lane != "" || !o.Interactive {
		return 0, false, nil
	}
	own := updateOwnVersion()
	if !update.IsRelease(own) {
		return 0, false, nil // a developer build or a nightly never asks
	}
	ctx, cancel := context.WithTimeout(context.Background(), src.AskTime)
	r, lerr := src.Latest(ctx)
	cancel()
	switch {
	case errors.Is(lerr, update.ErrNoZip):
		return 0, false, nil
	case lerr != nil:
		fmt.Fprintf(o.Out, "  update   no answer about a newer release (%v); going on\n", shortErr(lerr))
		return 0, false, nil
	case !update.Newer(own, r.Tag):
		return 0, false, nil
	}
	a, perr := Prompt(in, o.Out, fmt.Sprintf("\nVersion %s is available (you have %s). Update now? [Y/n]: ", r.Tag, own))
	if !o.Interactive {
		fmt.Fprintln(o.Out, a) // the answer came from a file (the test hook): end the question's line
	}
	if perr != nil || !(a == "" || strings.EqualFold(a, "y") || strings.EqualFold(a, "yes")) {
		fmt.Fprintf(o.Out, "  update   not now; Start-Client goes on with %s (update_check = off in %s stops the question)\n",
			own, ClientConfigName)
		return 0, false, nil
	}
	running, _ := updateRunning()
	up := update.Updater{Source: src, Dir: o.ExeDir, Work: filepath.Join(u.root, UpdateDir), Running: running,
		Out: o.Out, Live: o.Live, Version: updateExeVersion}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Minute)
	res, ierr := up.Install(ctx, r)
	cancel()
	if errors.Is(ierr, update.ErrServerRunning) {
		fmt.Fprintf(o.Out, "\nStart-Server.exe runs from this folder: close it, then start Start-Client.exe again to update.\n"+
			"Start-Client goes on with %s.\n\n", own)
		return 0, false, nil
	}
	if ierr != nil {
		fmt.Fprintf(o.Out, "\nThe update to %s failed: %v\nStart-Client goes on with %s.", r.Tag, ierr, own)
		if _, err := os.Stat(up.Zip(r)); err == nil {
			fmt.Fprintf(o.Out, " To update by hand, close Start-Client and unpack\n%s over this folder.", up.Zip(r))
		} else {
			fmt.Fprintf(o.Out, " To update by hand, download %s.zip from\n%s and unpack it over this folder.", r.Tag, update.ReleasesPage)
		}
		fmt.Fprint(o.Out, "\n\n")
		return 0, false, nil
	}
	exe := filepath.Join(o.ExeDir, "Start-Client.exe")
	note := ""
	if res.OldExe != "" {
		note = fmt.Sprintf("; the old program is %s until the next start", res.OldExe)
	}
	fmt.Fprintf(o.Out, "  update   %s installed (%d files%s); starting it\n\n", r.Tag, len(res.Installed), note)
	code, serr := updateStart(exe, u.args, own)
	if serr != nil {
		return o.fail("%s %s doesn't start: %v\nStart Start-Client.exe again.", exe, r.Tag, serr), true, nil
	}
	if code != 0 {
		o.pause() // the new program shares this window and doesn't wait for Enter itself
	}
	return code, true, nil
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// exeVersion runs a Start-Client.exe with --version and returns what it prints.
func exeVersion(exe string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	cmd.Dir = filepath.Dir(exe)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// startUpdated starts the new Start-Client with this start's arguments in this console and waits for it (so the
// window stays and whoever started this one gets its exit code); Ctrl+C reaches both, and this one leaves it to the
// new one.
func startUpdated(exe string, args []string, from string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), UpdatedFromEnv+"="+from)
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}
