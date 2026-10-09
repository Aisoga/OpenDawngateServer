package certs

import (
	"strings"
	"testing"
	"time"
)

func TestVerifyServerCert(t *testing.T) {
	a, err := Generate("192.168.1.20", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("192.168.1.20", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := parseCert(a.ServerCert)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyServerCert(srv, a.CAModulus, "192.168.1.20"); err != nil {
		t.Errorf("its own CA: %v", err)
	}
	if err := VerifyServerCert(srv, a.CAModulus, ""); err != nil {
		t.Errorf("no CN: %v", err)
	}
	if err := VerifyServerCert(srv, b.CAModulus, "192.168.1.20"); err == nil || !strings.Contains(err.Error(), "isn't signed by this CA") {
		t.Errorf("another CA: %v", err)
	}
	if err := VerifyServerCert(srv, a.CAModulus, "192.168.1.21"); err == nil || !strings.Contains(err.Error(), "is for 192.168.1.20") {
		t.Errorf("another CN: %v", err)
	}
	if err := VerifyServerCert(srv, a.CAModulus[:64], ""); err == nil {
		t.Error("a short modulus passed")
	}
	if got := Fingerprint(a.CAModulus); len(got) != 16 || got != sha256Prefix(a.CAModulus) {
		t.Errorf("fingerprint %q", got)
	}
}
