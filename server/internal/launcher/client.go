package launcher

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
)

// ClientConfigName is Start-Client.exe's config file, next to it (written only when a player saves a server address).
const ClientConfigName = "Start-Client.ini"

// ClientConfigText is the head of Start-Client.ini when Start-Client.exe writes it.
const ClientConfigText = `; Start-Client.ini: the options of Start-Client.exe. Lines that start with ; are comments.

; The server to play on: its address, e.g. 192.168.1.20. Empty: the server of Start-Server.exe on this PC
; (its Start-Server.ini), else 127.0.0.1. "Start-Client.exe <address>" overrides it.
server =

; The folder for the game copy (1.5 GB): relative to this program's folder, or a full path. Empty: Start-Server.ini's
; folder, else Dawngate-Data.
folder =
`

var clientKeys = []string{"server", "folder", "zip"}

// TestParkEnv set to 1 is Start-Client's --test-park (the release's test hook; never needed by a player).
const TestParkEnv = "DAWNGATE_TEST_PARK"

// RunClient is Start-Client.exe (cmd/start-client): it finds the server to play on, makes sure the player copy is set
// up and patched for it (setup.Client; for a server on another machine with that server's CA, trusted on first use),
// checks that the server answers, then starts the game and waits until it exits. It returns the exit code.
func RunClient(args []string, o IO) int {
	fs := flag.NewFlagSet("Start-Client", flag.ContinueOnError)
	fs.SetOutput(o.Out)
	serverFlag := fs.String("server", "", "the server's address (also the first argument; default: Start-Client.ini, then this PC's Start-Server)")
	caFile := fs.String("ca", "", "the server's ca_modulus.bin, when its operator handed it out (instead of fetching it)")
	trust := fs.Bool("trust", false, "trust a new server CA without asking (scripts and tests; players confirm the fingerprint)")
	setupOnly := fs.Bool("setup-only", false, "set up the player copy for the server, then exit without starting the game")
	check := fs.Bool("check", false, "only report what is missing or stale; change nothing")
	fs.Bool("non-interactive", false, "never ask anything; for scripts and tests")
	park := fs.Bool("test-park", os.Getenv(TestParkEnv) == "1", "test hook: keep the game window off the desktop and never activate it, as run_session.ps1 -Background does; also "+TestParkEnv+"=1")
	zipFlag := fs.String("zip", "", "Dawngate.zip, when the player copy must be made or repaired")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	in := bufio.NewReader(o.In)
	fmt.Fprintln(o.Out, "Dawngate Restoration: Start-Client")

	clientIni := filepath.Join(o.ExeDir, ClientConfigName)
	ccfg, err := ReadConfig(clientIni)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return o.fail("%v", err)
	}
	for _, k := range ccfg.Unknown(clientKeys...) {
		fmt.Fprintf(o.Out, "  note: %s: unknown option %q (ignored)\n", clientIni, k)
	}
	scfg, _ := ReadConfig(filepath.Join(o.ExeDir, ServerConfigName)) // this PC's server, when there is one
	server := *serverFlag
	if server == "" && fs.NArg() > 0 {
		server = fs.Arg(0)
	}
	if server == "" {
		server = ccfg["server"]
	}
	if server == "" && scfg != nil {
		server = scfg["address"]
	}
	if server == "" && o.Interactive {
		answer, err := Prompt(in, o.Out, "\nWhich server do you want to play on? Type its address (e.g. 192.168.1.20),\n"+
			"or just press Enter for a server on this PC: ")
		if err != nil {
			return o.fail("%v", err)
		}
		server = answer
		if answer != "" {
			if err := setClientKey(clientIni, "server", answer); err == nil {
				fmt.Fprintf(o.Out, "  saved in %s (edit or delete it to play on another server)\n", clientIni)
			}
		}
	}
	if server == "" {
		server = setup.Local
	}
	if _, err := setup.ClientOptions(server); err != nil {
		return o.fail("The server address %q: %v", server, err)
	}
	folder := ccfg["folder"]
	if folder == "" && scfg != nil {
		folder = scfg["folder"]
	}
	if folder == "" {
		folder = DefaultFolder
	}
	l := setup.Layout{Root: Resolve(o.ExeDir, folder)}
	if IsPublicAddress(server) {
		ClientRiskNotice(o.Out, server)
	}
	local := !remoteServer(server)
	fmt.Fprintf(o.Out, "  server   %s\n  folder   %s\n", server, l.Root)

	// the server's CA: this PC's own server's, else fetched (or handed out) and trusted on first use
	var ca []byte
	ownCA := filepath.Join(l.Certs(server), certs.CAModulusFile)
	cert, answering := ServerCert(server, RedirectorPort, 5*time.Second) // the redirector, as the game reaches it
	if b, err := os.ReadFile(ownCA); err == nil {
		ca = b
	} else if local {
		return o.fail("No server is set up on this PC for %s: start Start-Server.exe first (its first start sets "+
			"everything up), then Start-Client.exe.", server)
	}
	if answering != nil && !*setupOnly && !*check {
		if local || ca != nil {
			return o.fail("No server is running on %s: double-click Start-Server.exe first and wait until it says "+
				"\"The server runs\", then start Start-Client.exe again.", server)
		}
		return o.fail("No server answers at %s (port %d: %v).\nIs Start-Server.exe running on that machine with "+
			"address = %s in its Start-Server.ini, and does its firewall let the network in (README.txt)?", server,
			RedirectorPort, answering, server)
	}
	if ca == nil {
		if ca, err = remoteCA(o, in, l, server, *caFile, *trust, answering == nil); err != nil {
			return o.fail("%v", err)
		}
	}
	if answering == nil {
		if err := certs.VerifyServerCert(cert, ca, server); err != nil {
			return o.fail("The server at %s doesn't present a certificate the game would accept: %v\n"+
				"The game would fail with \"Unable to establish connection\". If the server was set up anew, delete "+
				"%s and start Start-Client.exe again to confirm its new fingerprint.", server, err, l.TrustedCA(server))
		}
	}

	fmt.Fprintln(o.Out, "\nChecking the game copy (the first start makes it: about half a minute and 1.5 GB):")
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: *check, FindZip: zipFinder(o, in, firstNonEmpty(*zipFlag, ccfg["zip"], scfg["zip"])),
		Manifest: manifest}
	var release func()
	if !*check {
		if release, err = setup.Lock(l.Root); err != nil {
			return o.fail("%v", err)
		}
	}
	if ClientRunning() && !*check {
		if release != nil {
			release()
		}
		return o.fail("A Dawngate client is already running (only one can run at a time): close it, then start " +
			"Start-Client.exe again.")
	}
	err = s.Client(server, ca)
	if release != nil {
		release()
	}
	if *check {
		if err != nil {
			fmt.Fprintf(o.Out, "\n%d item(s) not current.\n", len(s.Bad()))
			return 1
		}
		fmt.Fprintln(o.Out, "\nThe game copy is ready.")
		return 0
	}
	if err != nil {
		return o.fail("The game copy is not ready:\n%s\n\n%s", lines(s.Bad()), setupAdvice(s.Bad(), l.Root))
	}
	if *setupOnly {
		return 0
	}
	return play(o, l, *park)
}

// remoteServer says whether a server address is another machine's (its CA is fetched and trusted on first use) rather
// than this PC's own (a loopback address: the CA is this PC's setup's). The test of the remote path sets it to use a
// server on a loopback address.
var remoteServer = func(address string) bool { return !net.ParseIP(address).IsLoopback() }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func setClientKey(path, key, value string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(ClientConfigText, "\n", "\r\n")), 0o644); err != nil {
			return err
		}
	}
	return SetKey(path, key, value)
}

// remoteCA is the CA of a server on another machine: the handed-out file (--ca) or the one it serves, trusted on
// first use (TrustCA; the player compares the fingerprint with the operator's Start-Server window).
func remoteCA(o IO, in *bufio.Reader, l setup.Layout, server, caFile string, trust, answering bool) ([]byte, error) {
	var served []byte
	var err error
	switch {
	case caFile != "":
		if served, err = os.ReadFile(caFile); err != nil {
			return nil, err
		}
		if len(served) != 128 {
			return nil, fmt.Errorf("%s has %d bytes: a ca_modulus.bin has 128", caFile, len(served))
		}
		trust = true // the operator handed it out
	case answering:
		if served, err = FetchCA(server, HTTPPort, 5*time.Second); err != nil {
			if known, kerr := os.ReadFile(l.TrustedCA(server)); kerr == nil {
				fmt.Fprintf(o.Out, "  note: %v; using the CA trusted before (%s)\n", err, certs.Fingerprint(known))
				return known, nil
			}
			return nil, fmt.Errorf("the server's certificate authority can't be fetched: %v\nAsk its operator for "+
				"the file ca_modulus.bin and start Start-Client.exe %s --ca <that file>", err, server)
		}
	default:
		if known, err := os.ReadFile(l.TrustedCA(server)); err == nil {
			return known, nil
		}
		return nil, fmt.Errorf("no server answers at %s and its CA isn't known yet: start Start-Client.exe while "+
			"the server runs, or pass --ca <its ca_modulus.bin>", server)
	}
	var ask func(string) (bool, error)
	if o.Interactive {
		ask = func(q string) (bool, error) {
			a, err := Prompt(in, o.Out, "\n  "+q)
			return strings.EqualFold(a, "y") || strings.EqualFold(a, "yes"), err
		}
	}
	t, err := TrustCA(server, served, TrustOptions{Store: l.TrustedCA(server), Accept: trust, Ask: ask})
	if err != nil {
		return nil, err
	}
	switch t {
	case TrustNew:
		fmt.Fprintf(o.Out, "  trusting the server's CA %s from now on (%s)\n", certs.Fingerprint(served), l.TrustedCA(server))
	case TrustChanged:
		fmt.Fprintf(o.Out, "  trusting the server's new CA %s\n", certs.Fingerprint(served))
	}
	return served, nil
}

// play starts the game and waits for it; Ctrl+C or closing the window closes the game.
func play(o IO, l setup.Layout, park bool) int {
	prev := ForegroundWindow() // the test hook gives the foreground back to it (Park)
	g, err := StartGame(l.ClientCopy(), StartOptions{NoActivate: park})
	if err != nil {
		return o.fail("The game didn't start: %v", err)
	}
	defer g.Release()
	fmt.Fprintf(o.Out, "\nThe game starts (game pid %d; its window opens in a few seconds).\n", g.Pid)
	fmt.Fprintln(o.Out, "  Log in with any e-mail and password, then PLAY and PRACTICE DAWNGATE (README.txt).")
	fmt.Fprintln(o.Out, "  This window closes when the game ends; Ctrl+C here closes the game.")
	stop := make(chan struct{})
	defer close(stop)
	if park {
		fmt.Fprintln(o.Out, "  test hook: the game window is kept off the desktop, without a taskbar button, and out of the foreground (--test-park)")
		go g.Park(stop, prev, ParkEvents{ForegroundBack: func(n int, ok bool) {
			if n <= 20 {
				res := "given back to the window that had it"
				if !ok {
					res = "Windows refused to give it back"
				}
				fmt.Fprintf(o.Out, "  test hook: the game took the foreground (%d); %s (%s)\n", n, res, time.Now().Format("15:04:05.000"))
			}
		}, Lock: func(locked bool) {
			what := "the user is idle past ForegroundLockTimeout: foreground locked"
			if !locked {
				what = "the user is back: foreground lock released"
			}
			fmt.Fprintf(o.Out, "  test hook: %s (%s)\n", what, time.Now().Format("15:04:05.000"))
		}})
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	done := make(chan uint32, 1)
	go func() {
		code, _ := g.Wait()
		done <- code
	}()
	select {
	case code := <-done:
		fmt.Fprintf(o.Out, "The game exited (code %d).\n", int32(code))
		return 0
	case <-interrupt:
		fmt.Fprintln(o.Out, "Closing the game ...")
		g.Close()
		if !g.Exited(4 * time.Second) { // in a match the game ignores WM_CLOSE; a closed console allows about 5 s
			g.Kill()
		}
		return 0
	}
}
