package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Verifier checks a downloaded release before anything in the player's folder is replaced: zip is the download, root
// the unpacked folder whose files matched its BUILD.txt (b). Signed releases plug their signature check in here.
type Verifier func(zip, root string, b Build) error

// ErrServerRunning refuses an update while the folder's Start-Server.exe runs (its file can't be replaced then).
var ErrServerRunning = errors.New("Start-Server.exe runs from this folder")

// Updater installs a release into the player's folder.
type Updater struct {
	Source  Source
	Dir     string    // the player's folder (Start-Client.exe's)
	Work    string    // the download and the unpacked release (Dawngate-Data\update)
	Running string    // the running Start-Client.exe ("": none of the folder's files runs)
	Out     io.Writer // progress lines
	Live    bool      // Out is a console: the download's progress in place
	Verify  Verifier  // nil: none yet (releases aren't signed)
	// Version runs the new Start-Client.exe --version before anything is replaced; it must name the release (nil:
	// not run).
	Version func(exe string) (string, error)
}

// Zip is where Install downloads a release's zip (it stays there after a failed update, for an unpack by hand).
func (u Updater) Zip(r Release) string { return filepath.Join(u.Work, r.Tag+".zip") }

// Install downloads r's zip, unpacks it, checks every file against its BUILD.txt (and Verify), runs the new
// Start-Client.exe --version, and replaces the folder's release files (Replace). It returns what Replace did. On a
// failure the folder keeps the old release; the zip stays in Work unless it was damaged.
func (u Updater) Install(ctx context.Context, r Release) (Result, error) {
	if Running(filepath.Join(u.Dir, "Start-Server.exe")) {
		return Result{}, ErrServerRunning
	}
	if err := os.MkdirAll(u.Work, 0o755); err != nil {
		return Result{}, err
	}
	zip := u.Zip(r)
	if err := u.download(ctx, r, zip); err != nil {
		return Result{}, fmt.Errorf("the download of %s failed: %v", r.Zip.Name, err)
	}
	stage := filepath.Join(u.Work, r.Tag)
	defer os.RemoveAll(stage)
	root, err := Unpack(zip, stage)
	if err == nil {
		var b Build
		if b, err = Check(root, r.Tag); err == nil {
			if u.Verify != nil {
				err = u.Verify(zip, root, b)
			}
			if err == nil && u.Version != nil {
				var v string
				if v, err = u.Version(filepath.Join(root, "Start-Client.exe")); err == nil && v != r.Tag {
					err = fmt.Errorf("its Start-Client.exe says it is %q, not %s", v, r.Tag)
				} else if err != nil {
					err = fmt.Errorf("its Start-Client.exe doesn't start: %v", err)
				}
			}
			if err == nil {
				if Running(filepath.Join(u.Dir, "Start-Server.exe")) { // started meanwhile
					return Result{}, ErrServerRunning
				}
				fmt.Fprintf(u.Out, "  update   %s: %d files match its %s; installing them\n", r.Zip.Name, len(b.Files), BuildFile)
				var res Result
				if res, err = Replace(u.Dir, root, b, u.Running); err == nil {
					os.Remove(zip)
					return res, nil
				}
				return Result{}, fmt.Errorf("the files can't be replaced: %v", err)
			}
		}
	}
	os.Remove(zip) // damaged or not a release: an unpack by hand wouldn't help
	return Result{}, fmt.Errorf("%s is refused: %v", r.Zip.Name, err)
}

func (u Updater) download(ctx context.Context, r Release, dest string) error {
	mb := func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
	size := "size unknown"
	if r.Zip.Size > 0 {
		size = mb(r.Zip.Size)
	}
	fmt.Fprintf(u.Out, "  update   downloading %s (%s) into %s\n", r.Zip.Name, size, u.Work)
	last := int64(-1)
	err := u.Source.Download(ctx, r.Zip, dest, func(done int64) {
		if u.Live && done>>20 != last {
			last = done >> 20
			fmt.Fprintf(u.Out, "\r  update   %s of %s ", mb(done), size)
		}
	})
	if u.Live && last >= 0 {
		fmt.Fprint(u.Out, "\r"+strings.Repeat(" ", 40)+"\r")
	}
	return err
}
