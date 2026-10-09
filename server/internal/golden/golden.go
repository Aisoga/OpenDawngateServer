// Package golden loads server/testdata for tests: tdf_golden.json (TDF cases and Fire frames from the
// Python codec, written by re/scripts/tdf_golden.py) and other reference files.
package golden

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Case is one TDF payload with the text the server logs for it (tdf.py's pretty(decode(payload), 2)).
type Case struct {
	Name   string `json:"name"`
	Hex    string `json:"hex"`
	Pretty string `json:"pretty"`
}

// Frame is one whole Fire frame.
type Frame struct {
	Name      string  `json:"name"`
	Component uint16  `json:"component"`
	Command   uint16  `json:"command"`
	Type      uint8   `json:"type"`
	ID        uint32  `json:"id"`
	Error     uint16  `json:"error"`
	Payload   string  `json:"payload"`
	Pretty    *string `json:"pretty"`
	Hex       string  `json:"hex"`
}

// ErrorCase is a malformed payload and the Python codec's error message.
type ErrorCase struct {
	Name  string `json:"name"`
	Hex   string `json:"hex"`
	Error string `json:"error"`
}

// Data is tdf_golden.json.
type Data struct {
	Writer []Case      `json:"writer"`
	Decode []Case      `json:"decode"`
	Errors []ErrorCase `json:"errors"`
	Frames []Frame     `json:"frames"`
}

// Dir returns server/testdata.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

// Load reads tdf_golden.json.
func Load(t testing.TB) *Data {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(Dir(), "tdf_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// File reads another file from server/testdata.
func File(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Dir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// WriterCase returns a writer case by name.
func (d *Data) WriterCase(t testing.TB, name string) Case {
	t.Helper()
	for _, c := range d.Writer {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no writer case %q in tdf_golden.json", name)
	return Case{}
}

// Frame returns a frame by name.
func (d *Data) Frame(t testing.TB, name string) Frame {
	t.Helper()
	for _, f := range d.Frames {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no frame %q in tdf_golden.json", name)
	return Frame{}
}

// Bytes decodes a hex string from the golden file.
func Bytes(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
