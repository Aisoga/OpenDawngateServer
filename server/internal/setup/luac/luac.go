// Package luac reads the game's Lua 5.1 binary chunks (ServerData.package's scripts) and runs small parts of them:
// a script's main function, which sets its constants (a modifier's fields, its Locale GUIDs, its handlers), and a
// handler such as TranslateToken, which turns a tooltip's token into a number. It reads the chunk format
// the reference reader does; the interpreter has no game behind it, so anything that asks the match
// (a creature's attribute, a modifier's stacks) comes back as an unknown or a scaled value (interp.go).
//
// The chunks' header: Lua 5.1, official format, little endian, 4-byte int, size_t and Instruction, and a 4-byte
// float lua_Number (integral flag 0), so every number is a float32.
package luac

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Signature starts every binary chunk.
const Signature = "\x1bLua"

// ErrNotBinary says the data is no Lua 5.1 binary chunk (e.g. a script shipped as source text).
var ErrNotBinary = errors.New("luac: not a Lua 5.1 binary chunk")

// Function is one function prototype.
type Function struct {
	Source       string
	LineDefined  int32
	LastLine     int32
	NumUpvalues  byte
	NumParams    byte
	IsVararg     byte
	MaxStackSize byte
	Code         []uint32
	Constants    []Value // nil, bool, float64 (a float32's value) or string
	Protos       []*Function
}

// The opcodes of Lua 5.1 (lopcodes.h), in order.
const (
	opMOVE = iota
	opLOADK
	opLOADBOOL
	opLOADNIL
	opGETUPVAL
	opGETGLOBAL
	opGETTABLE
	opSETGLOBAL
	opSETUPVAL
	opSETTABLE
	opNEWTABLE
	opSELF
	opADD
	opSUB
	opMUL
	opDIV
	opMOD
	opPOW
	opUNM
	opNOT
	opLEN
	opCONCAT
	opJMP
	opEQ
	opLT
	opLE
	opTEST
	opTESTSET
	opCALL
	opTAILCALL
	opRETURN
	opFORLOOP
	opFORPREP
	opTFORLOOP
	opSETLIST
	opCLOSE
	opCLOSURE
	opVARARG
)

// The instruction fields (lopcodes.h: op 6 bits, A 8, C 9, B 9; Bx = B:C; sBx = Bx - 131071; an RK operand
// with bit 8 set names constant x & 0xFF).
func op(i uint32) int  { return int(i & 0x3F) }
func ia(i uint32) int  { return int(i >> 6 & 0xFF) }
func ic(i uint32) int  { return int(i >> 14 & 0x1FF) }
func ib(i uint32) int  { return int(i >> 23 & 0x1FF) }
func ibx(i uint32) int { return int(i >> 14) }
func isbx(i uint32) int {
	return int(i>>14) - 131071
}

const bitRK = 0x100

// Parse reads a binary chunk whose header is the game's (see the package comment).
func Parse(data []byte) (*Function, error) {
	if len(data) < 12 || string(data[:4]) != Signature {
		return nil, ErrNotBinary
	}
	if data[4] != 0x51 || data[5] != 0 {
		return nil, fmt.Errorf("luac: version %#x format %d, want Lua 5.1's official format", data[4], data[5])
	}
	if data[6] != 1 || data[7] != 4 || data[8] != 4 || data[9] != 4 || data[10] != 4 || data[11] != 0 {
		return nil, fmt.Errorf("luac: header % x, want little endian, 4-byte int, size_t, Instruction and float", data[6:12])
	}
	r := &reader{d: data, p: 12}
	f, err := r.function(0)
	if err != nil {
		return nil, err
	}
	if r.p != len(data) {
		return nil, fmt.Errorf("luac: %d trailing bytes", len(data)-r.p)
	}
	return f, nil
}

type reader struct {
	d []byte
	p int
}

var errShort = errors.New("luac: chunk cut short")

func (r *reader) need(n int) error {
	if n < 0 || r.p+n > len(r.d) {
		return errShort
	}
	return nil
}

func (r *reader) int32() (int32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := int32(binary.LittleEndian.Uint32(r.d[r.p:]))
	r.p += 4
	return v, nil
}

func (r *reader) count() (int, error) {
	n, err := r.int32()
	if err != nil {
		return 0, err
	}
	if n < 0 || int(n) > len(r.d) {
		return 0, fmt.Errorf("luac: bad count %d", n)
	}
	return int(n), nil
}

func (r *reader) str() (string, bool, error) {
	n, err := r.count()
	if err != nil {
		return "", false, err
	}
	if n == 0 {
		return "", false, nil
	}
	if err := r.need(n); err != nil {
		return "", false, err
	}
	s := string(r.d[r.p : r.p+n-1]) // without the trailing NUL
	r.p += n
	return s, true, nil
}

func (r *reader) function(depth int) (*Function, error) {
	if depth > 200 {
		return nil, errors.New("luac: functions nested too deep")
	}
	f := &Function{}
	var err error
	if f.Source, _, err = r.str(); err != nil {
		return nil, err
	}
	if f.LineDefined, err = r.int32(); err != nil {
		return nil, err
	}
	if f.LastLine, err = r.int32(); err != nil {
		return nil, err
	}
	if err := r.need(4); err != nil {
		return nil, err
	}
	f.NumUpvalues, f.NumParams, f.IsVararg, f.MaxStackSize = r.d[r.p], r.d[r.p+1], r.d[r.p+2], r.d[r.p+3]
	r.p += 4
	n, err := r.count()
	if err != nil {
		return nil, err
	}
	if err := r.need(4 * n); err != nil {
		return nil, err
	}
	f.Code = make([]uint32, n)
	for i := range f.Code {
		f.Code[i] = binary.LittleEndian.Uint32(r.d[r.p+4*i:])
	}
	r.p += 4 * n
	if n, err = r.count(); err != nil {
		return nil, err
	}
	for range n {
		if err := r.need(1); err != nil {
			return nil, err
		}
		t := r.d[r.p]
		r.p++
		switch t {
		case 0:
			f.Constants = append(f.Constants, nil)
		case 1:
			if err := r.need(1); err != nil {
				return nil, err
			}
			f.Constants = append(f.Constants, r.d[r.p] != 0)
			r.p++
		case 3:
			if err := r.need(4); err != nil {
				return nil, err
			}
			f.Constants = append(f.Constants, float64(math.Float32frombits(binary.LittleEndian.Uint32(r.d[r.p:]))))
			r.p += 4
		case 4:
			s, _, err := r.str()
			if err != nil {
				return nil, err
			}
			f.Constants = append(f.Constants, s)
		default:
			return nil, fmt.Errorf("luac: bad constant type %d", t)
		}
	}
	if n, err = r.count(); err != nil {
		return nil, err
	}
	for range n {
		p, err := r.function(depth + 1)
		if err != nil {
			return nil, err
		}
		f.Protos = append(f.Protos, p)
	}
	// debug info: line info, local variables, upvalue names (stripped in the game's chunks, but read anyway)
	if n, err = r.count(); err != nil {
		return nil, err
	}
	if err := r.need(4 * n); err != nil {
		return nil, err
	}
	r.p += 4 * n
	if n, err = r.count(); err != nil {
		return nil, err
	}
	for range n {
		if _, _, err := r.str(); err != nil {
			return nil, err
		}
		if err := r.need(8); err != nil {
			return nil, err
		}
		r.p += 8
	}
	if n, err = r.count(); err != nil {
		return nil, err
	}
	for range n {
		if _, _, err := r.str(); err != nil {
			return nil, err
		}
	}
	return f, nil
}
