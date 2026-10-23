// Package synthzip makes a stand-in for Dawngate.zip for the tests of the setup and the launchers: a
// zip with one Dawngate/ folder holding exepatch's synthetic exe, a fake PhysX loader, three small config packages
// with a URL each (so configpatch changes them), synthgame's made-up AssetData.snap and Locale.package (so gamecontent
// builds a content file) and a readme, and the manifest that describes it. No game file is involved; the zip is a few
// hundred kilobytes.
package synthzip

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/configpatch"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/setup/testgame/synthgame"
)

// When is the archive's file time.
var When = time.Date(2014, 12, 2, 18, 13, 4, 0, time.UTC)

// Loader is the fake PhysX loader (hostcopy looks for NxCreatePhysicsSDK in it).
var Loader = []byte("MZ fake PhysX loader: NxCreatePhysicsSDK")

func textPackage(props string) []byte {
	return testgame.Package(0, []testgame.PackageEntry{{Type: configpatch.TextType, Instance: 0x8C47952D, Data: []byte(props)}})
}

// Files are the synthetic build's files (copy paths) and contents.
func Files(t testing.TB) map[string][]byte {
	t.Helper()
	game := t.TempDir()
	if err := synthgame.Write(game); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return map[string][]byte{
		"game/Dawngate.exe":    exepatch.SyntheticExe(),
		"game/PhysXLoader.dll": Loader,
		"game/Data/Config.package": textPackage("property NewTelemetryServerConfigURL 0x1 string8 " +
			"http://gos.ea.com/battlegrounds_test/live/builds/manifests/telemetry_rules.xml\r\n"),
		"game/Data/Config_Ship.package":         textPackage("property NewTelemetryServerIP 0x2 string8 159.153.235.32\r\n"),
		"game/Data/Config_Pack.package":         textPackage("property X 0x3 string8 http://cdn.strategy.ea.com/a\r\n"),
		"game/Data/AssetData.snap":              read("game/Data/AssetData.snap"),
		"game/Data/LoginScreen.snap":            read("game/Data/LoginScreen.snap"),
		"game/Data/Battlegrounds.snap":          read("game/Data/Battlegrounds.snap"),
		"game/Data/Global.snap":                 read("game/Data/Global.snap"),
		"game/Data/Locale/en-us/Locale.package": read("game/Data/Locale/en-us/Locale.package"),
		"game/readme.txt":                       []byte("synthetic Dawngate for tests\r\n"),
	}
}

// Zip writes the synthetic Dawngate.zip into dir and returns its path and manifest.
func Zip(t testing.TB, dir string) (string, *archive.Manifest) {
	t.Helper()
	files := Files(t)
	p := filepath.Join(dir, archive.GameZipName)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	var names []string
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	var mf []archive.File
	for _, dirName := range []string{"Dawngate/", "Dawngate/game/", "Dawngate/game/Data/"} {
		h := &zip.FileHeader{Name: dirName, Method: zip.Store, Modified: When}
		if _, err := w.CreateHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range names {
		h := &zip.FileHeader{Name: archive.Top + "/" + rel, Method: zip.Deflate, Modified: When}
		ew, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ew.Write(files[rel]); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(files[rel])
		mf = append(mf, archive.File{Path: archive.Top + "/" + rel, Size: int64(len(files[rel])), SHA256: hex.EncodeToString(sum[:])})
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p, archive.NewManifest(mf)
}
