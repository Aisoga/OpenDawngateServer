package limits

import (
	"strings"
	"testing"
	"time"
)

// the admin login's stricter limits: 3 free failures per login name, then a lockout from 1 min doubling; 5 at
// once per address, then one every 30 s.
func TestAdminConfig(t *testing.T) {
	l, c := testLimiter(Admin())
	for i := range 4 {
		if wait, why := l.Login("10.0.0.1", "boss"); wait != 0 {
			t.Fatalf("attempt %d refused: %s", i+1, why)
		}
		l.LoginDone("10.0.0.1", "boss", false)
		c.add(31 * time.Second)
	}
	if wait, _ := l.Login("10.0.0.2", "boss"); wait <= 0 || wait > time.Minute {
		t.Errorf("after 4 failures: wait %v, want up to 1 min", wait)
	}
	c.add(time.Minute)
	if wait, _ := l.Login("10.0.0.2", "boss"); wait != 0 {
		t.Errorf("after the lockout: wait %v", wait)
	}
	l.LoginDone("10.0.0.2", "boss", false)
	if wait, _ := l.Login("10.0.0.2", "boss"); wait <= time.Minute {
		t.Errorf("the 5th failure: wait %v, want 2 min", wait)
	}
	// the address's rate: 5 at once (other names), then refused until 30 s passed
	for i := range 5 {
		if wait, _ := l.Login("10.0.0.9", "n"+string(rune('a'+i))); wait != 0 {
			t.Fatalf("burst %d refused", i)
		}
	}
	if wait, _ := l.Login("10.0.0.9", "nz"); wait <= 0 || wait > 30*time.Second {
		t.Errorf("over the rate: wait %v", wait)
	}
	if !strings.Contains(Admin().DescribeLogins(), "3 failures per login name") {
		t.Error(Admin().DescribeLogins())
	}
}
