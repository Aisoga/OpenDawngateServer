// Command mapedit runs the map editor's backend (internal/mapedit) without the game, for the developers: the page,
// the checks, Save, Build into the output folder and the 3-D view, but no Test (that is Start-Client.exe --editor's,
// MAPED-REL3). It is its own mapc, as Start-Client is (`mapedit --mapc <args>` runs internal/mapctool), so nothing
// is built at the start.
//
// Usage (from server\):
//
//	go run ./tools/mapedit -pages <page folder> -maps <dir> [-copy <copy>] [-out <dir>] [-cache <dir>]
//	                       [-addr 127.0.0.1:17890] [-mapc <mapc.exe>] [-open]
//
//	-pages  the page: a checkout of the page's repository (its root, with web\index.html,
//	        templates\ and editor.json) or a release's editor\ folder
//	-maps   the folder of map packages (a scratch folder: the checkout's maps\ holds the user's own packages)
//	-copy   a game copy to read (the folder with game\Data), used by Build, the catalog checks, the props and the
//	        3-D view; never written (a pristine extract is fine). Without it those are off
//	-out    Build's output folder (default: out beside the maps folder)
//	-cache  the 3-D view's exports (default: <out>\3d); converted game art, never in git
//	-mapc   another mapc program (default: this program with --mapc)
//	-open   open the page in the default browser
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"dawngate/server/internal/mapctool"
	"dawngate/server/internal/mapedit"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--mapc" || os.Args[1] == "-mapc") {
		os.Exit(mapctool.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	var c mapedit.Config
	addr := flag.String("addr", "127.0.0.1:17890", "the address to serve on (loopback only)")
	flag.StringVar(&c.Pages, "pages", "", "the page's folder (the page repository's checkout or a release's editor\\)")
	flag.StringVar(&c.Maps, "maps", "", "the folder of map packages")
	flag.StringVar(&c.Copy, "copy", "", "the game copy to read")
	flag.StringVar(&c.Out, "out", "", "Build's output folder (default: out beside -maps)")
	flag.StringVar(&c.Cache, "cache", "", "the 3-D view's exports (default: <out>\\3d)")
	flag.StringVar(&c.Mapc, "mapc", "", "another mapc program (default: this one with --mapc)")
	open := flag.Bool("open", false, "open the page in the browser")
	flag.Parse()
	if err := run(*addr, c, *open); err != nil {
		log.Fatal("mapedit: ", err)
	}
}

func run(addr string, c mapedit.Config, open bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("-addr %s: only a loopback address is allowed", addr)
	}
	if c.Maps == "" {
		return errors.New("no -maps folder given")
	}
	if c.Maps, err = filepath.Abs(c.Maps); err != nil {
		return err
	}
	if c.Mapc == "" {
		if c.Mapc, err = os.Executable(); err != nil {
			return err
		}
		c.MapcArgs = []string{"--mapc"}
	}
	if c.Out == "" {
		c.Out = filepath.Join(filepath.Dir(c.Maps), "out")
	}
	if c.Cache == "" {
		c.Cache = filepath.Join(c.Out, "3d")
	}
	c.Out, _ = filepath.Abs(c.Out)
	c.Cache, _ = filepath.Abs(c.Cache)
	c.TestOff = "Test plays the map in the game: tools/mapedit has no game (Start-Client.exe --editor has)"
	s, err := mapedit.NewServer(c)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.PageError(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String() + "/"
	log.Printf("maps %s, copy %q, mapc %s %v", c.Maps, c.Copy, c.Mapc, c.MapcArgs)
	log.Printf("serving %s (Ctrl+C ends)", url)
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	if open {
		openBrowser(url)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sh)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("opening the browser: %v", err)
	}
}
