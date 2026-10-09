package launcher

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/hostcopy"
)

// ServerConfigName is Start-Server.exe's config file, next to it.
const ServerConfigName = "Start-Server.ini"

// DefaultFolder is the setup folder's default name, next to the programs.
const DefaultFolder = "Dawngate-Data"

// ServerConfigText is the config file Start-Server.exe writes on its first start.
const ServerConfigText = `; Start-Server.ini: the options of Start-Server.exe. Start-Server.exe wrote this file on its first start;
; edit it, then start Start-Server.exe again. Lines that start with ; are comments.

; The address the server listens on and tells the players.
;   127.0.0.1      this PC only: you play here with Start-Client.exe (the default)
;   a LAN address  this PC's address in your home network (e.g. 192.168.1.20): players on other machines
;                  start Start-Client.exe with that address (README.txt, "Playing over your network").
;                  Never an internet address: the server accepts any login and isn't hardened.
address = 127.0.0.1

; Shaper select before every match: on, or off (then everyone plays Fenmore).
shaper_select = on

; The folder for the game copies (about 3 GB), the certificates, the accounts and the logs: relative to this
; program's folder, or a full path such as D:\Games\Dawngate-Data.
folder = Dawngate-Data

; Dawngate.zip from archive.org, only needed for the first setup and for repairs. Empty: next to this program,
; then in your Downloads folder; if it isn't found there, Start-Server.exe asks for it.
zip =

; More server options, separated by blanks, e.g.  --owned starter  or  --mm-team-size 1 --mm-fill-after 5s
; (README.txt lists a few; "dgserver.exe -h" in the developers' build lists all). Empty: the defaults.
server_flags =
`

// manifest is the build the setup expects (nil: build 854); the tests set a synthetic one.
var manifest *archive.Manifest

var serverKeys = []string{"address", "shaper_select", "folder", "zip", "server_flags"}

// IO is where a launcher talks: the console normally, buffers in tests.
type IO struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool // ask questions (a console, not --non-interactive)
	Live        bool // Out is a console (progress in place)
	OwnConsole  bool // the window closes with the program: wait for Enter after an error
	ExeDir      string
}

// StdIO is the console's IO for the program in exeDir.
func StdIO(nonInteractive bool) IO {
	return IO{In: os.Stdin, Out: os.Stdout, Interactive: !nonInteractive && IsConsole(os.Stdin),
		Live: IsConsole(os.Stdout), OwnConsole: OwnConsole(), ExeDir: ExeDir()}
}

func (o IO) fail(format string, a ...any) int {
	fmt.Fprintf(o.Out, "\n"+format+"\n", a...)
	o.pause()
	return 1
}

func (o IO) pause() {
	if o.OwnConsole && o.Interactive {
		fmt.Fprint(o.Out, "\nPress Enter to close this window.")
		bufio.NewReader(o.In).ReadString('\n')
	}
}

// zipFinder is the setup's FindZip: the configured or usual places, then (interactively) the question.
func zipFinder(o IO, in *bufio.Reader, configured string) func() (string, error) {
	return func() (string, error) {
		cands := ZipCandidates(o.ExeDir, configured)
		if p := FirstFile(cands); p != "" {
			return p, nil
		}
		if !o.Interactive {
			return "", fmt.Errorf("%w (looked in %s); put it next to Start-Server.exe or name it in the config file (zip =)",
				setup.ErrNoZip, strings.Join(cands, ", "))
		}
		home, _ := os.UserHomeDir()
		return AskZip(in, o.Out, func() (string, error) { return PickZip(filepath.Join(home, "Downloads")) })
	}
}

// RunServer is Start-Server.exe (cmd/start-server): its first start writes Start-Server.ini, finds Dawngate.zip and
// sets everything up in the folder next to it (setup.Server), every start checks (and repairs) that setup, then runs
// dgserver in this process with the match-host copy until Ctrl+C or the window is closed. It returns the exit code.
func RunServer(args []string, o IO) int {
	fs := flag.NewFlagSet("Start-Server", flag.ContinueOnError)
	fs.SetOutput(o.Out)
	configPath := fs.String("config", "", "the config file (default: "+ServerConfigName+" next to this program)")
	setupOnly := fs.Bool("setup-only", false, "set up or repair everything, then exit without starting the server")
	check := fs.Bool("check", false, "only report what is missing or stale; change nothing")
	fs.Bool("non-interactive", false, "never ask anything (fail when Dawngate.zip isn't found); for scripts and tests")
	seconds := fs.Int("seconds", 0, "stop the server after N seconds (tests)")
	hostDir := fs.String("host-files", "", `the folder with dgsim.dll and lua\ (default: host\ next to this program)`)
	zipFlag := fs.String("zip", "", "Dawngate.zip (instead of the config file's zip)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	in := bufio.NewReader(o.In)
	fmt.Fprintln(o.Out, "Dawngate Restoration: Start-Server")
	path := *configPath
	if path == "" {
		path = filepath.Join(o.ExeDir, ServerConfigName)
	}
	cfg, created, err := LoadOrCreate(path, ServerConfigText)
	if err != nil {
		return o.fail("The config file %s can't be read or written: %v", path, err)
	}
	if created {
		fmt.Fprintf(o.Out, "  wrote %s with the default options (edit it to change them)\n", path)
	}
	for _, k := range cfg.Unknown(serverKeys...) {
		fmt.Fprintf(o.Out, "  note: %s: unknown option %q (ignored)\n", path, k)
	}
	address := cfg["address"]
	if address == "" {
		address = setup.Local
	}
	shaperSelect, err := cfg.Bool("shaper_select", true)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	folder := cfg["folder"]
	if folder == "" {
		folder = DefaultFolder
	}
	root := Resolve(o.ExeDir, folder)
	zipConf := cfg["zip"]
	if *zipFlag != "" {
		zipConf = *zipFlag
	}
	if _, err := setup.ClientOptions(address); err != nil {
		return o.fail("%s: address = %s: %v", path, address, err)
	}
	if !*check {
		if err := thisMachine(address); err != nil {
			return o.fail("%s: address = %s is not an address of this PC (%v): use 127.0.0.1, or this PC's LAN "+
				"address (ipconfig shows it as IPv4 Address)", path, address, err)
		}
	}
	hostFiles := *hostDir
	if hostFiles == "" {
		hostFiles = filepath.Join(o.ExeDir, "host")
	}
	files := hostcopy.Files{FS: os.DirFS(hostFiles), DLL: "dgsim.dll", Lua: "lua"}
	if _, err := os.Stat(filepath.Join(hostFiles, "dgsim.dll")); err != nil {
		return o.fail("The match host's files are missing: %s has no dgsim.dll. Unpack the whole release zip, "+
			"with its host folder, next to Start-Server.exe.", hostFiles)
	}

	if !*check && !*setupOnly { // before the setup: a second Start-Server must not touch the copies of a running one
		held := HeldPorts(address, ServerPorts[:len(ServerPorts)-1])
		held = append(held, HeldPorts(ControlBind(address), ServerPorts[len(ServerPorts)-1:])...)
		if len(held) > 0 {
			return o.fail("Another program holds the server's ports on %s:\n  %s\nIs a server already running (another "+
				"Start-Server window, or dgserver.exe)? Close it, then start Start-Server.exe again.", address,
				strings.Join(held, "\n  "))
		}
	}
	where := "this PC only"
	if !net.ParseIP(address).IsLoopback() {
		where = "players on your network connect to it"
	}
	fmt.Fprintf(o.Out, "  folder   %s\n  address  %s (%s)\n\n", root, address, where)
	if *check {
		fmt.Fprintln(o.Out, "Checking the setup (nothing is changed):")
	} else {
		fmt.Fprintln(o.Out, "Checking the setup (the first start sets everything up: about a minute and 3 GB):")
	}
	l := setup.Layout{Root: root}
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: *check, FindZip: zipFinder(o, in, zipConf), HostFiles: files,
		Manifest: manifest}
	var release func()
	if !*check {
		if release, err = setup.Lock(root); err != nil {
			return o.fail("%v", err)
		}
	}
	err = s.Server(setup.ServerOptions{Address: address})
	if release != nil {
		release()
	}
	if *check {
		if err != nil {
			fmt.Fprintf(o.Out, "\n%d item(s) not current; start Start-Server.exe without --check to set them up.\n", len(s.Bad()))
			return 1
		}
		fmt.Fprintln(o.Out, "\nEverything is set up.")
		return 0
	}
	if err != nil {
		return o.fail("The setup is not complete:\n%s\n\n%s", lines(s.Bad()), setupAdvice(s.Bad(), root))
	}
	fmt.Fprintln(o.Out, "  the setup is complete")
	if *setupOnly {
		return 0
	}

	flags := ServerFlags(l, address, shaperSelect, SplitFlags(cfg["server_flags"]))
	if *seconds > 0 {
		flags = append(flags, fmt.Sprintf("--seconds=%d", *seconds))
	}
	fmt.Fprintln(o.Out, "\nStarting the server ...")
	code := dgserver.Run(flags, dgserver.Hooks{Started: func(info dgserver.Info) {
		fmt.Fprintln(o.Out, "")
		fmt.Fprintf(o.Out, "==== The server runs on %s. ====\n", address)
		if net.ParseIP(address).IsLoopback() {
			fmt.Fprintln(o.Out, "  Now double-click Start-Client.exe to play.")
		} else {
			fmt.Fprintf(o.Out, "  Players start Start-Client.exe %s (you too, on this PC: just Start-Client.exe).\n", address)
			fmt.Fprintf(o.Out, "  The first time, they are shown the server's fingerprint: it must be %s.\n", info.CAFingerprint)
			fmt.Fprintln(o.Out, "  If Windows asks whether the server may use the network, allow it for private networks.")
		}
		fmt.Fprintf(o.Out, "  Log in with any e-mail and password (the first login creates the account).\n")
		fmt.Fprintf(o.Out, "  Logs: %s\n", l.Logs())
		fmt.Fprintln(o.Out, "  Stop the server with Ctrl+C or by closing this window (it ends running matches).")
		fmt.Fprintln(o.Out, "")
	}})
	if code != 0 {
		return o.fail("The server stopped with an error (see above).")
	}
	fmt.Fprintln(o.Out, "Server stopped.")
	return 0
}

// ServerFlags are dgserver's arguments for a setup folder (start_server.ps1's: the host copy, a host per game,
// headless; plus the certificates, data and logs of the folder and --serve-ca for players on other machines).
func ServerFlags(l setup.Layout, address string, shaperSelect bool, extra []string) []string {
	f := []string{"--bind", address, "--public-host", address, "--certs", l.Certs(address), "--data", l.Data(),
		"--log", l.Logs(), "--gms-dir", l.HostCopy(), "--gms-args=-multipleInstances", "--gms-data",
		filepath.Join(l.Logs(), "hosts"), "--gms-control-bind", ControlBind(address), "--serve-ca"}
	if shaperSelect {
		f = append(f, "--shaper-select")
	}
	return append(f, extra...)
}

// ControlBind is where the match hosts' control port binds: the server's own address when it is a loopback one (so a
// server on another loopback address, e.g. a test on 127.0.9.1, keeps off 127.0.0.1:10040), else 127.0.0.1 (a LAN
// address there would expose it, which dgserver allows only with agents, OPERATORS.md).
func ControlBind(address string) string {
	if ip := net.ParseIP(address); ip != nil && ip.IsLoopback() {
		return address
	}
	return setup.Local
}

// thisMachine checks that address can be bound here.
func thisMachine(address string) error {
	ln, err := net.Listen("tcp4", net.JoinHostPort(address, "0"))
	if err != nil {
		return err
	}
	return ln.Close()
}

func lines(ls []setup.Line) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(l.String())
	}
	return b.String()
}

// setupAdvice says what to do about the steps left undone.
func setupAdvice(bad []setup.Line, root string) string {
	for _, l := range bad {
		if strings.Contains(l.Detail, "Dawngate.zip not found") || strings.Contains(l.Detail, "no Dawngate.zip") {
			return "Download Dawngate.zip from https://archive.org/details/Dawngate, put it next to Start-Server.exe " +
				"and start Start-Server.exe again."
		}
		if strings.Contains(l.Detail, "disk space") || l.Step == "disk space" {
			return "Free some disk space (or set folder = to another drive in " + ServerConfigName + "), then start again."
		}
	}
	return "Start Start-Server.exe again: the setup continues where it stopped. If the same step fails again, delete " +
		"the folder named in it (inside " + root + ") so that it is made anew, and report the message if that doesn't help."
}
