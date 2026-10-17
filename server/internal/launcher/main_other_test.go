//go:build !windows

package launcher

import (
	"os"
	"testing"
)

// The launcher's tests check the Windows behaviour on every OS; the Linux server's path (hosts under Wine, wine.go)
// has its own tests, which turn it on (wine_test.go).
func TestMain(m *testing.M) {
	hostsUnderWine = false
	os.Exit(m.Run())
}
