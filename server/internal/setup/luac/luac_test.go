package luac

import (
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"testing"
)

// Hand-assembled chunks (made-up names, no game script): the encoder writes a Function in the game's chunk format.
func iABC(o, a, b, c int) uint32 { return uint32(o) | uint32(a)<<6 | uint32(c)<<14 | uint32(b)<<23 }
func iABx(o, a, bx int) uint32   { return uint32(o) | uint32(a)<<6 | uint32(bx)<<14 }
func iAsBx(o, a, sbx int) uint32 { return iABx(o, a, sbx+131071) }
func rkK(i int) int              { return i | bitRK }

func dump(f *Function) []byte {
	out := []byte{0x1B, 'L', 'u', 'a', 0x51, 0, 1, 4, 4, 4, 4, 0}
	i32 := func(v int) { out = binary.LittleEndian.AppendUint32(out, uint32(int32(v))) }
	str := func(s string) {
		i32(len(s) + 1)
		out = append(append(out, s...), 0)
	}
	var fn func(f *Function)
	fn = func(f *Function) {
		i32(0) // no source name
		i32(int(f.LineDefined))
		i32(int(f.LastLine))
		out = append(out, f.NumUpvalues, f.NumParams, f.IsVararg, f.MaxStackSize)
		i32(len(f.Code))
		for _, c := range f.Code {
			out = binary.LittleEndian.AppendUint32(out, c)
		}
		i32(len(f.Constants))
		for _, k := range f.Constants {
			switch v := k.(type) {
			case nil:
				out = append(out, 0)
			case bool:
				b := byte(0)
				if v {
					b = 1
				}
				out = append(out, 1, b)
			case float64:
				out = append(out, 3)
				out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
			case string:
				out = append(out, 4)
				str(v)
			}
		}
		i32(len(f.Protos))
		for _, p := range f.Protos {
			fn(p)
		}
		i32(0)
		i32(0)
		i32(0)
	}
	fn(f)
	return out
}

// testScript is, as Lua:
//
//	modifier_Test = nAbility.create()
//	modifier_Test.localizedName = nUtil.ToGUID("0x00000042")
//	modifier_Test.cooldown = 8
//	modifier_Test.duration = {0}
//	nAbility.RegisterAbility("modifier_Test")
//	modifier_Test[nAbilityFns.TranslateToken] = function(p1)
//	  local v = modifier_Test
//	  p1.translatedToken = 0
//	  if p1.token == "cooldown" then p1.translatedToken = v.cooldown * 100
//	  elseif p1.token == "scaled" then
//	    p1.translatedToken = math.floor(nAttribute.GetAttributeValue(p1.myObjectID, nAttributeType.Power) * 0.4)
//	  end
//	  return p1.translatedToken
//	end
func testScript() *Function {
	tt := &Function{NumParams: 1, MaxStackSize: 8,
		Constants: []Value{"modifier_Test", "translatedToken", 0.0, "token", "cooldown", 100.0, "scaled", "math", "floor",
			"nAttribute", "GetAttributeValue", "myObjectID", "nAttributeType", "Power", float64(float32(0.4))},
		Code: []uint32{
			iABx(opGETGLOBAL, 1, 0),
			iABC(opSETTABLE, 0, rkK(1), rkK(2)),
			iABC(opGETTABLE, 2, 0, rkK(3)),
			iABC(opEQ, 0, 2, rkK(4)),
			iAsBx(opJMP, 0, 4),
			iABC(opGETTABLE, 2, 1, rkK(4)),
			iABC(opMUL, 2, 2, rkK(5)),
			iABC(opSETTABLE, 0, rkK(1), 2),
			iAsBx(opJMP, 0, 14),
			iABC(opGETTABLE, 2, 0, rkK(3)),
			iABC(opEQ, 0, 2, rkK(6)),
			iAsBx(opJMP, 0, 11),
			iABx(opGETGLOBAL, 2, 7),
			iABC(opGETTABLE, 2, 2, rkK(8)),
			iABx(opGETGLOBAL, 3, 9),
			iABC(opGETTABLE, 3, 3, rkK(10)),
			iABC(opGETTABLE, 4, 0, rkK(11)),
			iABx(opGETGLOBAL, 5, 12),
			iABC(opGETTABLE, 5, 5, rkK(13)),
			iABC(opCALL, 3, 3, 2),
			iABC(opMUL, 3, 3, rkK(14)),
			iABC(opCALL, 2, 2, 2),
			iABC(opSETTABLE, 0, rkK(1), 2),
			iABC(opGETTABLE, 1, 0, rkK(1)),
			iABC(opRETURN, 1, 2, 0),
			iABC(opRETURN, 0, 1, 0),
		}}
	return &Function{IsVararg: 2, MaxStackSize: 4, Protos: []*Function{tt},
		Constants: []Value{"modifier_Test", "nAbility", "create", "localizedName", "nUtil", "ToGUID", "0x00000042", "cooldown", 8.0,
			"duration", 0.0, "RegisterAbility", "modifier_Test", "nAbilityFns", "TranslateToken"},
		Code: []uint32{
			iABx(opGETGLOBAL, 0, 1),
			iABC(opGETTABLE, 0, 0, rkK(2)),
			iABC(opCALL, 0, 1, 2),
			iABx(opSETGLOBAL, 0, 0),
			iABx(opGETGLOBAL, 0, 0),
			iABx(opGETGLOBAL, 1, 4),
			iABC(opGETTABLE, 1, 1, rkK(5)),
			iABx(opLOADK, 2, 6),
			iABC(opCALL, 1, 2, 2),
			iABC(opSETTABLE, 0, rkK(3), 1),
			iABx(opGETGLOBAL, 0, 0),
			iABC(opSETTABLE, 0, rkK(7), rkK(8)),
			iABx(opGETGLOBAL, 0, 0),
			iABC(opNEWTABLE, 1, 1, 0),
			iABx(opLOADK, 2, 10),
			iABC(opSETLIST, 1, 1, 1),
			iABC(opSETTABLE, 0, rkK(9), 1),
			iABx(opGETGLOBAL, 0, 1),
			iABC(opGETTABLE, 0, 0, rkK(11)),
			iABx(opLOADK, 1, 12),
			iABC(opCALL, 0, 2, 1),
			iABx(opGETGLOBAL, 0, 0),
			iABx(opGETGLOBAL, 1, 13),
			iABC(opGETTABLE, 1, 1, rkK(14)),
			iABx(opCLOSURE, 2, 0),
			iABC(opSETTABLE, 0, 1, 2),
			iABC(opRETURN, 0, 1, 0),
		}}
}

func TestParseRoundTrip(t *testing.T) {
	f := testScript()
	got, err := Parse(dump(f))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, f) {
		t.Errorf("parsed\n %+v\nwant\n %+v", got, f)
	}
	for name, data := range map[string][]byte{"short": dump(f)[:40], "trailing": append(dump(f), 0),
		"version": append([]byte{0x1B, 'L', 'u', 'a', 0x52}, dump(f)[5:]...), "float size": append(append([]byte{}, dump(f)[:10]...), append([]byte{8}, dump(f)[11:]...)...)} {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
	if _, err := Parse([]byte("-- source")); !errors.Is(err, ErrNotBinary) {
		t.Errorf("source text: %v", err)
	}
}

func TestLoadAndTranslate(t *testing.T) {
	s, err := Load(dump(testScript()))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "modifier_Test" || s.Field("cooldown") != 8.0 {
		t.Errorf("script %q cooldown %v", s.Name, s.Field("cooldown"))
	}
	if g, ok := s.GUIDField("localizedName"); !ok || g != 0x42 {
		t.Errorf("localizedName %#x %v", g, ok)
	}
	if d, ok := s.Field("duration").(*Table); !ok || d.Get(1.0) != 0.0 {
		t.Errorf("duration %v", s.Field("duration"))
	}
	h := s.Handler("TranslateToken")
	if h == nil {
		t.Fatal("no TranslateToken")
	}
	run := func(token string) Value {
		p := NewTable()
		p.Set("token", token)
		res, err := s.Env.Run(h.Proto, p)
		if err != nil || len(res) != 1 {
			t.Fatalf("%s: %v %v", token, res, err)
		}
		return res[0]
	}
	if v := run("cooldown"); v != 800.0 {
		t.Errorf("cooldown %v", v)
	}
	if v := run("scaled"); v != (Scaled{Attr: "Power", Factor: float64(float32(0.4))}) {
		t.Errorf("scaled %#v", v)
	}
	if v := run("other"); v != 0.0 {
		t.Errorf("other %v", v)
	}
}

func TestLoadSource(t *testing.T) {
	s, err := Load([]byte("modifier_Src = nAbility.create()\nmodifier_Src.localizedName = nUtil.ToGUID(\"0x10\")\n" +
		"modifier_Src.amount = 0.25\nmodifier_Src.kind = nActivationType.Unique\nmodifier_Src.label = \"x\"\n" +
		"modifier_Src.group = nUtil.SPID(\"ItemModifiers\")\nother.amount = 3\nnAbility.RegisterAbility(\"modifier_Src\")\n"))
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GUIDField("localizedName"); s.Name != "modifier_Src" || g != 0x10 || s.Field("amount") != float64(float32(0.25)) ||
		s.Field("kind") != Global("nActivationType.Unique") || s.Field("label") != "x" || s.Field("group") != SPID("ItemModifiers") {
		t.Errorf("source script %+v", s.Table)
	}
	if s.Handler("TranslateToken") != nil {
		t.Error("a source script has no handlers to run")
	}
	if _, err := Load([]byte("x = 1\n")); err == nil {
		t.Error("a script without RegisterAbility")
	}
}

// The built-ins a tooltip handler calls, another script's table through Lookup, and a source script's plain
// TranslateToken branches.
func TestBuiltinsAndLookup(t *testing.T) {
	e := NewEnv()
	ranks := NewTable()
	ranks.Set(1.0, 10.0)
	ranks.Set(2.0, 20.0)
	for _, c := range []struct {
		rank float64
		want Value
	}{{1, 10.0}, {2, 20.0}, {5, 20.0}, {0, 10.0}} {
		if got, _ := e.call(Global("nAbility.GetRankedValue"), []Value{ranks, c.rank}, 0); got[0] != c.want {
			t.Errorf("GetRankedValue(rank %v) = %v", c.rank, got)
		}
	}
	if got, _ := e.call(Global("nAbility.GetRankedValue"), []Value{7.0, 2.0}, 0); got[0] != 7.0 {
		t.Errorf("GetRankedValue of a plain value %v", got)
	}
	if got, _ := e.call(Global("nPlayer.HasItem"), nil, 0); got[0] != (Unknown{}) {
		t.Errorf("an API call %v", got)
	}
	other := NewTable()
	other.Set("maxStackCount", 30.0)
	e.Lookup = func(name string) Value {
		if name == "modifier_Other" {
			return other
		}
		return nil
	}
	f := &Function{MaxStackSize: 2, Constants: []Value{"modifier_Other", "maxStackCount", "nobody"},
		Code: []uint32{iABx(opGETGLOBAL, 0, 0), iABC(opGETTABLE, 0, 0, rkK(1)), iABx(opGETGLOBAL, 1, 2), iABC(opRETURN, 0, 3, 0)}}
	if res, err := e.Run(f); err != nil || res[0] != 30.0 || res[1] != Global("nobody") {
		t.Errorf("Lookup: %v %v", res, err)
	}
	s, err := LoadSource("m = nAbility.create()\nm.ratio = 0.5\nm.cd = 6\nm.dur = { 2, 3 }\n" +
		"m[ nAbilityFns.TranslateToken ] = function( myTable )\n  local self = m\n" +
		"  if myTable.token == \"ratio\"\n  then\n    myTable.translatedToken = self.ratio * 100\n" +
		"  elseif myTable.token == \"cd\" then myTable.translatedToken = self.cd\n" +
		"  elseif myTable.token == \"power\" then myTable.translatedToken = myTable.myPower * self.ratio\n  end\nend\n" +
		"nAbility.RegisterAbility(\"m\")\n")
	if err != nil {
		t.Fatal(err)
	}
	call := func(tok string) (Value, bool) {
		p := NewTable()
		p.Set("token", tok)
		return s.Call("TranslateToken", p)
	}
	if v, ok := call("ratio"); !ok || v != 50.0 {
		t.Errorf("ratio %v %v", v, ok)
	}
	if v, ok := call("cd"); !ok || v != 6.0 {
		t.Errorf("cd %v %v", v, ok)
	}
	if _, ok := call("power"); ok {
		t.Error("a branch over a match value is no constant")
	}
	if d, ok := s.Field("dur").(*Table); !ok || d.Get(2.0) != 3.0 {
		t.Errorf("dur %v", s.Field("dur"))
	}
}

// The arithmetic is float32's, and a scaled value keeps its form through the operations a tooltip uses.
func TestArith(t *testing.T) {
	if v := arith(opMUL, float64(float32(0.15)), 100.0); v != float64(float32(float32(0.15)*100)) {
		t.Errorf("0.15*100 = %v", v)
	}
	sc := Scaled{Attr: "Power", Factor: 1}
	if v := arith(opADD, arith(opMUL, sc, 2.0), 5.0); v != (Scaled{"Power", 2, 5}) {
		t.Errorf("scaled %v", v)
	}
	if v := arith(opMUL, sc, Scaled{Attr: "Power", Factor: 1}); v != (Unknown{}) {
		t.Errorf("scaled*scaled %v", v)
	}
	if v := arith(opADD, Unknown{}, 1.0); v != (Unknown{}) {
		t.Errorf("unknown %v", v)
	}
}
