package winres

import (
	"encoding/binary"
	"testing"
)

func TestNumbersOf(t *testing.T) {
	for name, want := range map[string][4]uint16{
		"v0.6":                                   {0, 6, 0, 0},
		"v1.0.1":                                 {1, 0, 1, 0},
		"2.3.4":                                  {2, 3, 4, 0},
		"v1":                                     {},
		"v1.2.3.4":                               {},
		"v70000.1":                               {},
		"OpenDawngateServer-20261017-1412db3630": {},
		"SRVVER-test-1":                          {},
	} {
		if got := NumbersOf(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

func TestVersionRoundTrip(t *testing.T) {
	v := Version{ProductName: "OpenDawngateServer", ProductVersion: "v1.2.3", FileVersion: "v1.2.3",
		FileDescription: "OpenDawngateServer: the match host (ü)", InternalName: "dgsim",
		OriginalFilename: "dgsim.dll", LegalCopyright: "Copyright (C) 2026", Numbers: NumbersOf("v1.2.3"), DLL: true}
	b := v.Resource()
	le := binary.LittleEndian
	if int(le.Uint16(b)) != len(b) || len(b)%4 != 0 {
		t.Fatalf("wLength %d of %d bytes", le.Uint16(b), len(b))
	}
	if le.Uint32(b[40:]) != 0xFEEF04BD { // after the header (6), the key (32) and the padding (2)
		t.Fatalf("no VS_FIXEDFILEINFO signature at 40: % x", b[:48])
	}
	if le.Uint32(b[40+36:]) != 2 {
		t.Errorf("file type %d, want VFT_DLL", le.Uint32(b[40+36:]))
	}
	strs, nums, err := ParseVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	if nums != [4]uint16{1, 2, 3, 0} {
		t.Errorf("numbers %v", nums)
	}
	for k, want := range map[string]string{"ProductName": v.ProductName, "ProductVersion": v.ProductVersion,
		"FileVersion": v.FileVersion, "FileDescription": v.FileDescription, "InternalName": v.InternalName,
		"OriginalFilename": v.OriginalFilename, "LegalCopyright": v.LegalCopyright} {
		if strs[k] != want {
			t.Errorf("%s: %q, want %q", k, strs[k], want)
		}
	}
	if len(strs) != 7 {
		t.Errorf("%d strings: %v", len(strs), strs)
	}
	// an empty value is left out
	v.LegalCopyright = ""
	if strs, _, _ := ParseVersion(v.Resource()); len(strs) != 6 {
		t.Errorf("without the copyright: %v", strs)
	}
	for name, bad := range map[string][]byte{"empty": nil, "cut": b[:len(b)/2], "icon": GroupIcon(nil)} {
		if _, _, err := ParseVersion(bad); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
