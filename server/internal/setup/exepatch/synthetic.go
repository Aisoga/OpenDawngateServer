package exepatch

import (
	"bytes"
	"encoding/binary"
)

// --- a synthetic exe: build 854's section layout and original bytes at every patched place, zeros elsewhere

type secSpec struct {
	name                   string
	va, vsize, raw, rawLen uint32
}

var synthSections = []secSpec{
	{".text", 0x1000, 0xDAA0FB, 0x400, 0xDAA200},
	{".rdata", 0xDAC000, 0x22CCC5, 0xDAA600, 0x22CE00},
	{".data", 0xFD9000, 0x826F00, 0xFD7400, 0xAF400},
}

func synthOffset(va uint32) int {
	rva := va - ImageBase
	for _, s := range synthSections {
		if rva >= s.va && rva < s.va+s.rawLen {
			return int(rva - s.va + s.raw)
		}
	}
	panic("no section")
}

// SyntheticExe is a stand-in for the build-854 exe in tests without the game (this package's and the
// setup's): build 854's section layout and its original bytes at every place Patch reads or writes, zeros elsewhere
// (16.6 MB). Patch, Describe, State and PatchFile (with FileOptions.BaseSHA256 set to its SHA-256) take it like the
// real exe. It is not a program.
func SyntheticExe() []byte {
	last := synthSections[len(synthSections)-1]
	b := make([]byte, last.raw+last.rawLen)
	b[0], b[1] = 'M', 'Z'
	const pe = 0x80
	binary.LittleEndian.PutUint32(b[0x3C:], pe)
	copy(b[pe:], "PE\x00\x00")
	fh := pe + 4
	binary.LittleEndian.PutUint16(b[fh:], 0x14C)
	binary.LittleEndian.PutUint16(b[fh+2:], uint16(len(synthSections)))
	binary.LittleEndian.PutUint16(b[fh+16:], 0xE0)
	binary.LittleEndian.PutUint16(b[fh+18:], charBuild)
	opt := fh + 20
	binary.LittleEndian.PutUint16(b[opt:], 0x10B)
	binary.LittleEndian.PutUint32(b[opt+28:], ImageBase)
	binary.LittleEndian.PutUint32(b[opt+32:], 0x1000)
	binary.LittleEndian.PutUint32(b[opt+36:], 0x200)
	binary.LittleEndian.PutUint16(b[opt+70:], 0x8100)
	for i, s := range synthSections {
		h := opt + 0xE0 + i*40
		copy(b[h:], s.name)
		binary.LittleEndian.PutUint32(b[h+8:], s.vsize)
		binary.LittleEndian.PutUint32(b[h+12:], s.va)
		binary.LittleEndian.PutUint32(b[h+16:], s.rawLen)
		binary.LittleEndian.PutUint32(b[h+20:], s.raw)
	}
	put := func(va uint32, data []byte) { copy(b[synthOffset(va):], data) }
	put(otg3Entry+0x180, []byte("OTG3 Certificate Authority\x00"))
	put(otg3KeyInfo, append(append(append(le32(0x80), le32(otg3Modulus)...), le32(1)...), 3))
	put(otg3Modulus, append(bytes.Clone(otg3ModulusHead), bytes.Repeat([]byte{0xA7}, 120)...))
	put(otg3Flags, le32(1))
	put(redirectorHost, redirectorOrig)
	put(noupnpStrstr, noupnpStrstrOrig)
	put(noupnpJnz, noupnpJnzOrig)
	put(upnpDisc, upnpDiscOrig)
	for _, va := range bindCalls {
		put(va, callBind)
	}
	put(bindThunk, jmpBind)
	put(raknetLoopback, raknetLoopbackOrig)
	put(raknetLoopbackPtr, le32(raknetLoopback))
	for _, f := range dataFolders {
		put(f.va, wide(f.name))
	}
	return b
}
