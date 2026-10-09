// Package launcher holds what Start-Server.exe and Start-Client.exe share: their
// small config files, finding Dawngate.zip (next to the program, in Downloads, or asked for), the console prompts,
// the port check before the server starts, the CA exchange with a server on another machine, and starting the game
// (game_windows.go: the launcher's Global\Dawngate L2G event, the single-client mutex, the test hook that keeps the
// game window off the desktop).
package launcher

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/archive"
)

// ExeDir is the folder of the running program (the release folder).
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return filepath.Dir(exe)
}

// Resolve makes p absolute relative to base (an absolute p stays).
func Resolve(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// --- config files: "key = value" lines, ; or # comments

// bom is UTF-8's byte order mark, which Notepad may put in front of a saved config file.
const bom = string(rune(0xFEFF))

// Config is a parsed config file: keys in lower case.
type Config map[string]string

// ReadConfig reads a config file. Unknown keys are kept (the caller warns about them); a line without "=" is an
// error with its number.
func ReadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseConfig(f, path)
}

// ParseConfig parses a config file's text (name is for messages).
func ParseConfig(r io.Reader, name string) (Config, error) {
	c := Config{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bom))
		if line == "" || line[0] == ';' || line[0] == '#' || line[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s line %d: %q is not \"key = value\"", name, n, line)
		}
		c[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return c, sc.Err()
}

// Unknown lists the keys of c that aren't in known.
func (c Config) Unknown(known ...string) []string {
	ok := map[string]bool{}
	for _, k := range known {
		ok[k] = true
	}
	var out []string
	for k := range c {
		if !ok[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Bool reads an on/off value (on, yes, true, 1 / off, no, false, 0; empty: def).
func (c Config) Bool(key string, def bool) (bool, error) {
	switch strings.ToLower(c[key]) {
	case "":
		return def, nil
	case "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return def, fmt.Errorf("%s = %q: use on or off", key, c[key])
}

// LoadOrCreate reads the config file at path; a missing file is written with text (the defaults with their comments)
// first. created says so.
func LoadOrCreate(path, text string) (c Config, created bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
			return nil, false, err
		}
		created = true
	}
	c, err = ReadConfig(path)
	return c, created, err
}

// SetKey writes key = value into the config file at path: an existing "key =" line is replaced, else the line is
// appended (the file is made when it is missing).
func SetKey(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	done := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if k, _, ok := strings.Cut(t, "="); ok && t[0] != ';' && t[0] != '#' && strings.EqualFold(strings.TrimSpace(k), key) {
			lines[i] = key + " = " + value
			done = true
		}
	}
	if !done {
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, key+" = "+value, "")
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\r\n")), 0o644)
}

// SplitFlags splits extra dgserver flags at blanks; a value in double quotes may contain blanks.
func SplitFlags(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted, have = !quoted, true
		case (r == ' ' || r == '\t') && !quoted:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

// --- Dawngate.zip

// ZipCandidates are the places Dawngate.zip is looked for, in order: configured (when set: only there), else next to
// the program, the folder above it, and the user's Downloads folder.
func ZipCandidates(exeDir, configured string) []string {
	if configured != "" {
		return []string{Resolve(exeDir, configured)}
	}
	c := []string{filepath.Join(exeDir, archive.GameZipName), filepath.Join(filepath.Dir(exeDir), archive.GameZipName)}
	if home, err := os.UserHomeDir(); err == nil {
		c = append(c, filepath.Join(home, "Downloads", archive.GameZipName))
	}
	return c
}

// FirstFile is the first of paths that is a file, or "".
func FirstFile(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// CheckZipFile opens path as a zip and checks that it is one Dawngate/ install folder (archive.CheckZip); the files'
// hashes are checked when it is extracted.
func CheckZipFile(path string) error {
	z, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%s is not a zip archive (%v)", path, err)
	}
	defer z.Close()
	if err := archive.CheckZip(z.File); err != nil {
		return fmt.Errorf("%s is not Dawngate's archive: %v", path, err)
	}
	return nil
}

// Prompt asks one question on out and reads a line from in (trimmed; io.EOF when there is no more input).
func Prompt(in *bufio.Reader, out io.Writer, question string) (string, error) {
	fmt.Fprint(out, question)
	line, err := in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// AskZip asks for Dawngate.zip until a file that looks like it is given: a typed, pasted or dragged path (dragging
// the file onto the console window types its path), or with an empty answer the file dialog (pick, when it isn't
// nil). Up to three tries.
func AskZip(in *bufio.Reader, out io.Writer, pick func() (string, error)) (string, error) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Dawngate.zip was not found. Download it from https://archive.org/details/Dawngate (1.3 GB),")
	fmt.Fprintln(out, "then put it next to this program, or tell me where it is:")
	for try := 0; try < 3; try++ {
		q := "  Drag Dawngate.zip onto this window (or paste its path) and press Enter"
		if pick != nil {
			q += ",\n  or just press Enter to choose it in a window"
		}
		answer, err := Prompt(in, out, q+": ")
		if err != nil {
			return "", err
		}
		answer = strings.Trim(answer, `"' `)
		if answer == "" && pick != nil {
			if answer, err = pick(); err != nil {
				fmt.Fprintf(out, "  (no file chosen: %v)\n", err)
				continue
			}
		}
		if answer == "" {
			continue
		}
		if st, err := os.Stat(answer); err == nil && st.IsDir() {
			answer = filepath.Join(answer, archive.GameZipName)
		}
		if err := CheckZipFile(answer); err != nil {
			fmt.Fprintf(out, "  %v\n", err)
			continue
		}
		return answer, nil
	}
	return "", errors.New("no Dawngate.zip given")
}

// --- ports

// Port is one port the server binds.
type Port struct {
	Number int
	UDP    bool
	What   string
}

// ServerPorts are the ports dgserver binds with match hosts on its default port numbers (OPERATORS.md *Ports and the
// firewall*; the hosts' UDP ports are taken from a pool when a match starts).
var ServerPorts = []Port{{42127, false, "the redirector"}, {10041, false, "Blaze"}, {17502, false, "QoS"},
	{17502, true, "QoS"}, {18080, false, "HTTP"}, {9988, false, "telemetry"}, {10040, false, "the match hosts' control channel"}}

// HeldPorts tries to bind each port on address and lists those another program holds, worded for the player.
func HeldPorts(address string, ports []Port) []string {
	var held []string
	for _, p := range ports {
		at := net.JoinHostPort(address, strconv.Itoa(p.Number))
		if p.UDP {
			c, err := net.ListenPacket("udp4", at)
			if err != nil {
				held = append(held, fmt.Sprintf("UDP %d (%s): %v", p.Number, p.What, err))
				continue
			}
			c.Close()
			continue
		}
		ln, err := net.Listen("tcp4", at)
		if err != nil {
			held = append(held, fmt.Sprintf("TCP %d (%s): %v", p.Number, p.What, err))
			continue
		}
		ln.Close()
	}
	return held
}
