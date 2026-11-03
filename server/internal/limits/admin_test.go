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

// the API's limits: no rate of attempts, 5 free failures per address, then 5 min doubling; another address isn't
// affected.
func TestAPIConfig(t *testing.T) {
	l, c := testLimiter(API())
	for i := range 100 { // valid requests: no rate
		if wait, why := l.Login("10.0.0.1", ""); wait != 0 {
			t.Fatalf("request %d refused: %s", i+1, why)
		}
		l.LoginDone("10.0.0.1", "", true)
	}
	for i := range 6 {
		if wait, why := l.Login("10.0.0.1", ""); wait != 0 {
			t.Fatalf("bad token %d refused before it was checked: %s", i+1, why)
		}
		l.LoginDone("10.0.0.1", "", false)
		c.add(time.Second)
	}
	if wait, _ := l.Login("10.0.0.1", ""); wait <= 4*time.Minute || wait > 10*time.Minute {
		t.Errorf("after 6 failures: wait %v, want about 5 min", wait)
	}
	if wait, _ := l.Login("10.0.0.2", ""); wait != 0 {
		t.Errorf("another address: wait %v", wait)
	}
	c.add(5 * time.Minute)
	if wait, _ := l.Login("10.0.0.1", ""); wait != 0 {
		t.Errorf("after the lockout: wait %v", wait)
	}
	l.LoginDone("10.0.0.1", "", false)
	if wait, _ := l.Login("10.0.0.1", ""); wait <= 5*time.Minute {
		t.Errorf("the 7th failure: wait %v, want 10 min", wait)
	}
}
