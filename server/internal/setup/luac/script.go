package luac

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Script is an ability or modifier script after its main function ran: the name it registered
// (nAbility.RegisterAbility) and the table it built under that name (its fields and handlers).
type Script struct {
	Name  string
	Table *Table
	Env   *Env

	srcTokens map[string]srcToken // a source script's plain TranslateToken branches (LoadSource)
}

// srcToken is one branch `if p.token == "<token>" then p.translatedToken = self.<field> [* or / <n>]`.
type srcToken struct {
	field string
	op    byte
	n     float64
}

var srcTokenRe = regexp.MustCompile(`token\s*==\s*"(\w+)"\s*then\s*\w+\.translatedToken\s*=\s*(\w+)\.(\w+)(?:[ \t]*([*/])[ \t]*([0-9.]+))?[ \t]*\r?\n`)

// Load runs a script's main function. A script shipped as source text (one of build 854's) is read by its plain
// "<name>.<field> = <constant>" lines instead (LoadSource).
func Load(data []byte) (*Script, error) {
	f, err := Parse(data)
	if errors.Is(err, ErrNotBinary) {
		return LoadSource(string(data))
	}
	if err != nil {
		return nil, err
	}
	env := NewEnv()
	if _, err := env.Run(f); err != nil {
		return nil, err
	}
	if len(env.Registered) == 0 {
		return nil, errors.New("luac: the script registers no ability")
	}
	s := &Script{Name: env.Registered[0], Env: env}
	s.Table, _ = env.Globals[s.Name].(*Table)
	if s.Table == nil {
		s.Table = NewTable()
	}
	return s, nil
}

var (
	srcRegister = regexp.MustCompile(`nAbility\.RegisterAbility\(\s*"([^"]+)"\s*\)`)
	srcField    = regexp.MustCompile(`(?m)^(\w+)\.(\w+)\s*=\s*(.+?)\s*$`)
	srcGUID     = regexp.MustCompile(`^nUtil\.ToGUID\(\s*"([^"]*)"\s*\)$`)
	srcSPID     = regexp.MustCompile(`^nUtil\.SPID\(\s*"([^"]*)"\s*\)$`)
	srcString   = regexp.MustCompile(`^"([^"\\]*)"$`)
	srcGlobal   = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)+$`)
)

// LoadSource reads a script shipped as Lua source: its registered name and the fields its top level sets to a
// number, a string, true/false, a GUID, an SPID or a global's field ("nActivationType.Unique"); handlers and
// anything computed are left out (no Env: its handlers can't be run).
func LoadSource(src string) (*Script, error) {
	m := srcRegister.FindStringSubmatch(src)
	if m == nil {
		return nil, errors.New("luac: the script registers no ability")
	}
	s := &Script{Name: m[1], Table: NewTable()}
	for _, f := range srcField.FindAllStringSubmatch(src, -1) {
		if f[1] != s.Name {
			continue
		}
		v := f[3]
		var val Value
		if g := srcGUID.FindStringSubmatch(v); g != nil {
			val = GUID(g[1])
		} else if g := srcSPID.FindStringSubmatch(v); g != nil {
			val = SPID(g[1])
		} else if g := srcString.FindStringSubmatch(v); g != nil {
			val = g[1]
		} else if v == "true" || v == "false" {
			val = v == "true"
		} else if x, err := strconv.ParseFloat(v, 32); err == nil {
			val = f32(x)
		} else if srcGlobal.MatchString(v) {
			val = Global(v)
		} else if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") { // a list of numbers per rank ({ 0 }, {2, 3})
			t := NewTable()
			for i, item := range strings.Split(strings.Trim(v, "{} "), ",") {
				if x, err := strconv.ParseFloat(strings.TrimSpace(item), 32); err == nil {
					t.Set(float64(i+1), f32(x))
				}
			}
			val = t
		} else {
			continue
		}
		s.Table.Set(f[2], val)
	}
	for _, m := range srcTokenRe.FindAllStringSubmatch(src, -1) {
		if m[2] != "self" && m[2] != s.Name {
			continue // a value of the match (myTable.myPower …): not a constant
		}
		t := srcToken{field: m[3]}
		if m[4] != "" {
			t.op = m[4][0]
			t.n, _ = strconv.ParseFloat(m[5], 64)
		}
		if s.srcTokens == nil {
			s.srcTokens = map[string]srcToken{}
		}
		s.srcTokens[m[1]] = t
	}
	return s, nil
}

// sourceToken is a source script's TranslateToken for token, from its plain branches (ok false without one).
func (s *Script) sourceToken(token string) (Value, bool) {
	t, ok := s.srcTokens[token]
	if !ok {
		return nil, false
	}
	v, isNum := s.Field(t.field).(float64)
	if !isNum {
		return Unknown{}, true
	}
	switch t.op {
	case '*':
		v = f32(v * f32(t.n))
	case '/':
		v = f32(v / f32(t.n))
	}
	return v, true
}

// Field returns a field of the script's table.
func (s *Script) Field(name string) Value { return s.Table.Get(name) }

// GUIDField returns a field set by nUtil.ToGUID as a number ("0x1052c3ef" → 0x1052C3EF).
func (s *Script) GUIDField(name string) (uint32, bool) {
	g, ok := s.Field(name).(GUID)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(string(g)), "0x"), 16, 32)
	return uint32(v), err == nil
}

// Call runs the handler nAbilityFns.<fn> with p as its argument (the handler's context table) and returns its first
// result; ok is false when the script has no such handler or the run failed.
func (s *Script) Call(fn string, p *Table) (Value, bool) {
	if s.Env == nil && fn == "TranslateToken" { // a source script: its plain branches only
		tok, _ := p.Get("token").(string)
		return s.sourceToken(tok)
	}
	h := s.Handler(fn)
	if h == nil {
		return nil, false
	}
	res, err := s.Env.Run(h.Proto, p)
	if err != nil {
		return nil, false
	}
	if len(res) == 0 {
		return nil, true
	}
	return res[0], true
}

// Handler returns the closure the script set for nAbilityFns.<fn> (nil when none, or for a source script).
func (s *Script) Handler(fn string) *Closure {
	c, _ := s.Table.Get(Global("nAbilityFns." + fn)).(*Closure)
	if s.Env == nil {
		return nil
	}
	return c
}
