package accounts

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id's cost parameters (RFC 9106): memory in KiB, passes, lanes.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

// DefaultParams are the parameters new hashes get: OWASP's first argon2id setting (19 MiB, 2 passes, 1 lane), about
// 30 ms on a desktop CPU. A server is no password manager: the memory per check stays small, so a burst of logins can't
// exhaust a home PC (and hashSlots bounds how many run at once). A hash with other parameters still verifies; a
// successful login rehashes it with these (NeedsRehash).
var DefaultParams = Params{Memory: 19 * 1024, Time: 2, Threads: 1}

const (
	saltLen = 16
	keyLen  = 32
	prefix  = "$argon2id$v=19$"
)

// hashSlots bounds the argon2 runs at once (each takes Params.Memory): a flood of logins queues here instead of
// allocating memory without limit (the limits come in front of it).
var hashSlots = make(chan struct{}, min(max(runtime.NumCPU()/2, 1), 4))

func argon(password string, salt []byte, p Params, n uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, n)
}

var b64 = base64.RawStdEncoding

// Hash returns password's argon2id hash with a new random salt and DefaultParams, in the PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash> (unpadded standard base64), as the reference implementation writes it.
func Hash(password string) (string, error) {
	return hashWith(password, DefaultParams)
}

func hashWith(password string, p Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon(password, salt, p, keyLen)
	return fmt.Sprintf("%sm=%d,t=%d,p=%d$%s$%s", prefix, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt),
		b64.EncodeToString(key)), nil
}

// errHash is a stored hash that isn't one of ours.
var errHash = errors.New("accounts: not an argon2id hash")

// parseHash splits a PHC argon2id string into its parameters, salt and key.
func parseHash(h string) (Params, []byte, []byte, error) {
	var p Params
	rest, ok := strings.CutPrefix(h, prefix)
	if !ok {
		return p, nil, nil, errHash
	}
	parts := strings.Split(rest, "$")
	if len(parts) != 3 {
		return p, nil, nil, errHash
	}
	var threads uint32
	if n, err := fmt.Sscanf(parts[0], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); n != 3 || err != nil ||
		p.Memory < 8 || p.Memory > 1<<18 || p.Time < 1 || p.Time > 64 || threads < 1 || threads > 255 {
		return p, nil, nil, errHash
	}
	p.Threads = uint8(threads)
	salt, err := b64.DecodeString(parts[1])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, errHash
	}
	key, err := b64.DecodeString(parts[2])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, errHash
	}
	return p, salt, key, nil
}

// Verify reports whether password matches the stored hash (in constant time over the key). A hash that doesn't parse
// matches nothing.
func Verify(hash, password string) bool {
	p, salt, key, err := parseHash(hash)
	if err != nil {
		return false
	}
	got := argon(password, salt, p, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}

// NeedsRehash reports whether a stored hash was made with other parameters than DefaultParams (or other lengths), so
// that a login that just verified it should store a new one.
func NeedsRehash(hash string) bool {
	p, salt, key, err := parseHash(hash)
	return err != nil || p != DefaultParams || len(salt) != saltLen || len(key) != keyLen
}

// dummyHash is verified for logins of unknown names, so that they take as long as a wrong password (no account
// enumeration by timing).
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("not a password of anyone")
	if err != nil {
		panic(err) // crypto/rand failed
	}
	return h
})
