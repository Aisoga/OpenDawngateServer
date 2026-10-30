package luac

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Value is a value of the interpreter: nil, bool, float64 (always a float32's value), string, *Table, Global (a
// global or a field path of one the script didn't set, such as "nUtil.ToGUID"), GUID and SPID (what nUtil.ToGUID and
// nUtil.SPID return), *Closure, Scaled (a creature's attribute times a factor plus an offset) or Unknown.
type Value any

// Global names a global that the script didn't set, or a field path of one: "nAbilityFns.TranslateToken".
type Global string

// GUID is nUtil.ToGUID's result: the GUID's text ("0x1052c3ef").
type GUID string

// SPID is nUtil.SPID's result: the name it hashes.
type SPID string

// Unknown is a value the interpreter can't know without a match (an API call's result, an upvalue).
type Unknown struct{}

// Scaled is Factor × the attribute Attr of a creature + Offset: what nAttribute.GetAttributeValue gives, carried
// through the arithmetic a tooltip does with it ("40 % of Power").
type Scaled struct {
	Attr           string
	Factor, Offset float64
}

// LevelAttr is the Attr of a Scaled that nGameObject.GetPlayerLevel gives: the shaper's level, not an attribute.
const LevelAttr = "@level"

// Closure is a function the script made (its prototype).
type Closure struct{ Proto *Function }

// Table is a Lua table. Keys are strings, float64, bool, Global, GUID or SPID; Keys keeps the order of first setting.
type Table struct {
	hash map[any]Value
	Keys []any
}

// NewTable makes an empty table.
func NewTable() *Table { return &Table{hash: map[any]Value{}} }

// Get returns a field (nil when unset).
func (t *Table) Get(k any) Value {
	if t == nil {
		return nil
	}
	return t.hash[k]
}

// Set sets a field (nil removes nothing from Keys).
func (t *Table) Set(k any, v Value) {
	if _, ok := t.hash[k]; !ok {
		t.Keys = append(t.Keys, k)
	}
	t.hash[k] = v
}

// Env is the global state of one script's run: its globals and the names it registered (nAbility.RegisterAbility).
type Env struct {
	Globals    map[string]Value
	Registered []string
	// Lookup gives a global the script didn't set (another script's table, which the game would have loaded
	// beside it); nil or a nil result: the global stays a name.
	Lookup func(name string) Value
	steps  int
}

func (e *Env) lookup(name string) Value {
	if e.Lookup == nil || name == "" {
		return nil
	}
	return e.Lookup(name)
}

// NewEnv makes an empty global state.
func NewEnv() *Env { return &Env{Globals: map[string]Value{}} }

// maxSteps bounds the instructions of one Run (a tooltip handler runs a few dozen; a loop over unknown bounds is
// skipped, so only a script's own constant loops count).
const maxSteps = 200000

// ErrSteps says a run went past maxSteps.
var ErrSteps = errors.New("luac: too many steps")

// f32 rounds to a float32's value: the game's lua_Number.
func f32(x float64) float64 { return float64(float32(x)) }

// Run runs a function with these arguments and returns its results. Unsupported operations give Unknown values;
// only a malformed function or a run past maxSteps is an error.
func (e *Env) Run(f *Function, args ...Value) ([]Value, error) {
	return e.run(f, args, 0)
}

func (e *Env) run(f *Function, args []Value, depth int) ([]Value, error) {
	if depth > 8 {
		return []Value{Unknown{}}, nil
	}
	reg := make([]Value, int(f.MaxStackSize)+256)
	for i := 0; i < int(f.NumParams) && i < len(args); i++ {
		reg[i] = args[i]
	}
	top := 0 // the end of the values a CALL with C = 0 or a VARARG left
	k := func(i int) Value {
		if i < 0 || i >= len(f.Constants) {
			return nil
		}
		return f.Constants[i]
	}
	rk := func(x int) Value {
		if x&bitRK != 0 {
			return k(x & 0xFF)
		}
		return reg[x]
	}
	for pc := 0; pc < len(f.Code); pc++ {
		if e.steps++; e.steps > maxSteps {
			return nil, ErrSteps
		}
		in := f.Code[pc]
		a, b, c := ia(in), ib(in), ic(in)
		switch op(in) {
		case opMOVE:
			reg[a] = reg[b]
		case opLOADK:
			reg[a] = k(ibx(in))
		case opLOADBOOL:
			reg[a] = b != 0
			if c != 0 {
				pc++
			}
		case opLOADNIL:
			for r := a; r <= b && r < len(reg); r++ {
				reg[r] = nil
			}
		case opGETUPVAL:
			reg[a] = Unknown{}
		case opGETGLOBAL:
			name, _ := k(ibx(in)).(string)
			if v, ok := e.Globals[name]; ok {
				reg[a] = v
			} else if v := e.lookup(name); v != nil {
				reg[a] = v
			} else {
				reg[a] = Global(name)
			}
		case opGETTABLE:
			reg[a] = index(reg[b], rk(c))
		case opSETGLOBAL:
			if name, ok := k(ibx(in)).(string); ok {
				e.Globals[name] = reg[a]
			}
		case opSETUPVAL, opCLOSE:
		case opSETTABLE:
			setIndex(reg[a], rk(b), rk(c))
		case opNEWTABLE:
			reg[a] = NewTable()
		case opSELF:
			reg[a+1] = reg[b]
			reg[a] = index(reg[b], rk(c))
		case opADD, opSUB, opMUL, opDIV, opMOD, opPOW:
			reg[a] = arith(op(in), rk(b), rk(c))
		case opUNM:
			reg[a] = arith(opSUB, 0.0, reg[b])
		case opNOT:
			if _, unknown := reg[b].(Unknown); unknown {
				reg[a] = Unknown{}
			} else {
				reg[a] = !truthy(reg[b])
			}
		case opLEN:
			switch v := reg[b].(type) {
			case string:
				reg[a] = float64(len(v))
			case *Table:
				n := 0
				for v.Get(float64(n+1)) != nil {
					n++
				}
				reg[a] = float64(n)
			default:
				reg[a] = Unknown{}
			}
		case opCONCAT:
			var sb strings.Builder
			var out Value
			for r := b; r <= c; r++ {
				switch v := reg[r].(type) {
				case string:
					sb.WriteString(v)
				case float64:
					sb.WriteString(numString(v))
				default:
					out = Unknown{}
				}
			}
			if out == nil {
				out = sb.String()
			}
			reg[a] = out
		case opJMP:
			pc += isbx(in)
		case opEQ, opLT, opLE:
			res, known := compare(op(in), rk(b), rk(c))
			if !known {
				res = false // a comparison with a match value: take the "false" branch
			}
			if res != (a != 0) {
				pc++
			}
		case opTEST:
			if truthy(reg[a]) != (c != 0) {
				pc++
			}
		case opTESTSET:
			if truthy(reg[b]) == (c != 0) {
				reg[a] = reg[b]
			} else {
				pc++
			}
		case opCALL, opTAILCALL:
			n := b - 1
			if b == 0 {
				n = top - a - 1
			}
			callArgs := append([]Value(nil), reg[a+1:a+1+max(n, 0)]...)
			res, err := e.call(reg[a], callArgs, depth)
			if err != nil {
				return nil, err
			}
			if op(in) == opTAILCALL {
				return res, nil
			}
			if c == 0 {
				copy(reg[a:], res)
				top = a + len(res)
			} else {
				for i := 0; i < c-1; i++ {
					if i < len(res) {
						reg[a+i] = res[i]
					} else {
						reg[a+i] = nil
					}
				}
			}
		case opRETURN:
			n := b - 1
			if b == 0 {
				n = top - a
			}
			return append([]Value(nil), reg[a:a+max(n, 0)]...), nil
		case opFORPREP:
			init, ok1 := reg[a].(float64)
			_, ok2 := reg[a+1].(float64)
			step, ok3 := reg[a+2].(float64)
			if !ok1 || !ok2 || !ok3 || step == 0 {
				pc += isbx(in) + 1 // bounds from the match: skip the loop (its FORLOOP too)
				continue
			}
			reg[a] = f32(init - step)
			pc += isbx(in)
		case opFORLOOP:
			idx, _ := reg[a].(float64)
			limit, _ := reg[a+1].(float64)
			step, _ := reg[a+2].(float64)
			idx = f32(idx + step)
			reg[a] = idx
			if (step > 0 && idx <= limit) || (step < 0 && idx >= limit) {
				pc += isbx(in)
				reg[a+3] = idx
			}
		case opTFORLOOP:
			// generic for over a match value: end the loop (no iteration)
		case opSETLIST:
			t, _ := reg[a].(*Table)
			n := b
			if n == 0 {
				n = top - a - 1
			}
			if c == 0 { // the block number is the next instruction
				pc++
				if pc < len(f.Code) {
					c = int(f.Code[pc])
				}
			}
			if t != nil {
				for i := 1; i <= n; i++ {
					t.Set(float64((c-1)*50+i), reg[a+i])
				}
			}
		case opCLOSURE:
			if bx := ibx(in); bx < len(f.Protos) {
				reg[a] = &Closure{Proto: f.Protos[bx]}
				pc += int(f.Protos[bx].NumUpvalues) // the upvalues' pseudo-instructions
			} else {
				reg[a] = Unknown{}
			}
		case opVARARG:
			for i := a; i < a+max(b-1, 0); i++ {
				reg[i] = nil
			}
			top = a
		default:
			return nil, fmt.Errorf("luac: bad opcode %d at pc %d", op(in), pc)
		}
	}
	return nil, nil
}

// call calls a value: a closure of the script, or one of the few built-ins a constant or a tooltip needs. Any other
// function is the game's API: its result is Unknown.
func (e *Env) call(fn Value, args []Value, depth int) ([]Value, error) {
	arg := func(i int) Value {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	num := func(i int) (float64, bool) {
		v, ok := arg(i).(float64)
		return v, ok
	}
	switch f := fn.(type) {
	case *Closure:
		return e.run(f.Proto, args, depth+1)
	case Global:
		switch f {
		case "nAbility.create":
			return []Value{NewTable()}, nil
		case "nUtil.ToGUID":
			if s, ok := arg(0).(string); ok {
				return []Value{GUID(s)}, nil
			}
		case "nUtil.SPID":
			if s, ok := arg(0).(string); ok {
				return []Value{SPID(s)}, nil
			}
		case "nAbility.RegisterAbility":
			if s, ok := arg(0).(string); ok {
				e.Registered = append(e.Registered, s)
			}
			return nil, nil
		case "nAbility.GetRankedValue":
			// a rank table's entry for the rank (1-based); past the table's end its last entry [assumed]
			if x, ok := arg(0).(float64); ok { // a value without ranks
				return []Value{x}, nil
			}
			if t, ok := arg(0).(*Table); ok {
				r, _ := num(1)
				n := 0
				for t.Get(float64(n+1)) != nil {
					n++
				}
				if n > 0 {
					i := min(max(int(r), 1), n)
					return []Value{t.Get(float64(i))}, nil
				}
			}
		case "nGameObject.GetPlayerLevel":
			// the shaper's level: a scaled value of its own ("300 (+30 per level)", the spells' damage and shields)
			return []Value{Scaled{Attr: LevelAttr, Factor: 1}}, nil
		case "nAttribute.GetAttributeValue":
			if attr, ok := arg(1).(Global); ok {
				if name, ok := strings.CutPrefix(string(attr), "nAttributeType."); ok {
					return []Value{Scaled{Attr: name, Factor: 1}}, nil
				}
			}
		case "math.floor", "math.ceil", "math.abs":
			if s, ok := arg(0).(Scaled); ok {
				return []Value{s}, nil // a scaled value keeps its factor (the tooltip shows the ratio)
			}
			if x, ok := num(0); ok {
				m := map[Global]func(float64) float64{"math.floor": math.Floor, "math.ceil": math.Ceil, "math.abs": math.Abs}
				return []Value{f32(m[f](x))}, nil
			}
		case "math.pow":
			x, ok1 := num(0)
			y, ok2 := num(1)
			if ok1 && ok2 {
				return []Value{f32(math.Pow(x, y))}, nil
			}
		case "math.max", "math.min":
			best, ok := num(0)
			for i := 1; ok && i < len(args); i++ {
				x, ok2 := num(i)
				ok = ok2
				if (f == "math.max" && x > best) || (f == "math.min" && x < best) {
					best = x
				}
			}
			if ok && len(args) > 0 {
				return []Value{best}, nil
			}
		case "require":
			return nil, nil
		}
	}
	return []Value{Unknown{}}, nil
}

// index is t[k]: a table's field, a global's field path, or Unknown.
func index(t, k Value) Value {
	switch v := t.(type) {
	case *Table:
		if key, ok := tableKey(k); ok {
			return v.Get(key)
		}
	case Global:
		if s, ok := k.(string); ok {
			return Global(string(v) + "." + s)
		}
	}
	return Unknown{}
}

func setIndex(t, k, v Value) {
	if tt, ok := t.(*Table); ok {
		if key, ok := tableKey(k); ok {
			tt.Set(key, v)
		}
	}
}

func tableKey(k Value) (any, bool) {
	switch v := k.(type) {
	case string, float64, bool, Global, GUID, SPID:
		return v, true
	}
	return nil, false
}

func truthy(v Value) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	return true
}

// arith is a binary operation in float32, or with a scaled value as far as it stays Factor × attribute + Offset.
func arith(o int, x, y Value) Value {
	a, aok := x.(float64)
	b, bok := y.(float64)
	if aok && bok {
		switch o {
		case opADD:
			return f32(a + b)
		case opSUB:
			return f32(a - b)
		case opMUL:
			return f32(a * b)
		case opDIV:
			return f32(a / b)
		case opMOD:
			return f32(a - math.Floor(a/b)*b)
		case opPOW:
			return f32(math.Pow(a, b))
		}
	}
	sx, sxok := x.(Scaled)
	sy, syok := y.(Scaled)
	switch {
	case sxok && bok:
		switch o {
		case opADD:
			return Scaled{sx.Attr, sx.Factor, sx.Offset + b}
		case opSUB:
			return Scaled{sx.Attr, sx.Factor, sx.Offset - b}
		case opMUL:
			return Scaled{sx.Attr, sx.Factor * b, sx.Offset * b}
		case opDIV:
			if b != 0 {
				return Scaled{sx.Attr, sx.Factor / b, sx.Offset / b}
			}
		}
	case aok && syok:
		switch o {
		case opADD:
			return Scaled{sy.Attr, sy.Factor, sy.Offset + a}
		case opSUB:
			return Scaled{sy.Attr, -sy.Factor, a - sy.Offset}
		case opMUL:
			return Scaled{sy.Attr, sy.Factor * a, sy.Offset * a}
		}
	case sxok && syok && sx.Attr == sy.Attr:
		switch o {
		case opADD:
			return Scaled{sx.Attr, sx.Factor + sy.Factor, sx.Offset + sy.Offset}
		case opSUB:
			return Scaled{sx.Attr, sx.Factor - sy.Factor, sx.Offset - sy.Offset}
		}
	}
	return Unknown{}
}

// compare is ==, < or <=; known false when an operand is a match value.
func compare(o int, x, y Value) (res, known bool) {
	if isUnknown(x) || isUnknown(y) {
		return false, false
	}
	if o == opEQ {
		kx, okx := tableKey(x)
		ky, oky := tableKey(y)
		if okx && oky {
			return kx == ky, true
		}
		if x == nil || y == nil {
			return x == nil && y == nil, true
		}
		return false, true // tables and closures: by identity, which a tooltip never compares
	}
	a, aok := x.(float64)
	b, bok := y.(float64)
	if aok && bok {
		if o == opLT {
			return a < b, true
		}
		return a <= b, true
	}
	sa, sok := x.(string)
	sb, tok := y.(string)
	if sok && tok {
		if o == opLT {
			return sa < sb, true
		}
		return sa <= sb, true
	}
	return false, false
}

func isUnknown(v Value) bool {
	switch v.(type) {
	case Unknown, Scaled:
		return true
	}
	return false
}

// numString is Lua 5.1's tostring of a number ("%.14g").
func numString(x float64) string {
	return strconv.FormatFloat(x, 'g', 14, 64)
}
