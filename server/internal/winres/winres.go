// Package winres puts an icon into Windows programs without outside tools: it reads an .ico file and writes a
// COFF object (.syso) holding the icon as RT_ICON and RT_GROUP_ICON resources. The Go linker links every
// rsrc_windows_<arch>.syso of a main package into the program's resource section when it builds for
// GOOS=windows (and ignores it for every other GOOS), so a plain go build carries the icon. The package also reads
// the resources back, from a .syso or a built program, for the tests.
//
// The formats: the ICO file (ICONDIR, ICONDIRENTRY), the group icon (GRPICONDIR, GRPICONDIRENTRY), the resource
// directory (IMAGE_RESOURCE_DIRECTORY, _ENTRY, _DATA_ENTRY) and the COFF object, as Microsoft's PE format
// documentation describes them.
package winres

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Resource types.
const (
	RTIcon      = 3
	RTGroupIcon = 14
)

// Lang is the language of the resources written (en-US).
const Lang = 0x0409

// GroupID is the name ID of the one group icon written; the Windows shell shows the first group icon.
const GroupID = 1

// Icon is one image of an icon file.
type Icon struct {
	Width, Height, Colors, Reserved byte // as in the file; 0 means 256 pixels
	Planes, BitCount                uint16
	Data                            []byte // a BMP (from its BITMAPINFOHEADER on) or a PNG
}

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// ParseICO reads an .ico file.
func ParseICO(b []byte) ([]Icon, error) {
	if len(b) < 6 {
		return nil, errors.New("ico: too short")
	}
	le := binary.LittleEndian
	if le.Uint16(b[0:]) != 0 || le.Uint16(b[2:]) != 1 {
		return nil, errors.New("ico: not an icon file (header)")
	}
	n := int(le.Uint16(b[4:]))
	if n == 0 {
		return nil, errors.New("ico: no images")
	}
	if len(b) < 6+16*n {
		return nil, errors.New("ico: directory truncated")
	}
	icons := make([]Icon, n)
	for i := range icons {
		e := b[6+16*i:]
		size, off := le.Uint32(e[8:]), le.Uint32(e[12:])
		if size == 0 || uint64(off)+uint64(size) > uint64(len(b)) {
			return nil, fmt.Errorf("ico: image %d out of the file (offset %d, size %d)", i, off, size)
		}
		data := b[off : off+size]
		if !bytes.HasPrefix(data, pngMagic) && (len(data) < 40 || le.Uint32(data) < 40) {
			return nil, fmt.Errorf("ico: image %d is neither a PNG nor a BMP", i)
		}
		icons[i] = Icon{Width: e[0], Height: e[1], Colors: e[2], Reserved: e[3],
			Planes: le.Uint16(e[4:]), BitCount: le.Uint16(e[6:]), Data: data}
	}
	return icons, nil
}

// GroupIcon is the RT_GROUP_ICON resource for icons whose RT_ICON resources have the IDs 1..len(icons).
func GroupIcon(icons []Icon) []byte {
	le := binary.LittleEndian
	b := make([]byte, 6+14*len(icons))
	le.PutUint16(b[2:], 1)
	le.PutUint16(b[4:], uint16(len(icons)))
	for i, ic := range icons {
		e := b[6+14*i:]
		e[0], e[1], e[2], e[3] = ic.Width, ic.Height, ic.Colors, ic.Reserved
		le.PutUint16(e[4:], ic.Planes)
		le.PutUint16(e[6:], ic.BitCount)
		le.PutUint32(e[8:], uint32(len(ic.Data)))
		le.PutUint16(e[12:], uint16(i+1))
	}
	return b
}

// The COFF machine and the relocation type of a 32-bit image-relative address, per Go architecture.
var machines = map[string]struct{ machine, reloc uint16 }{
	"amd64": {0x8664, 3}, // IMAGE_FILE_MACHINE_AMD64, IMAGE_REL_AMD64_ADDR32NB
	"386":   {0x014c, 7}, // IMAGE_FILE_MACHINE_I386, IMAGE_REL_I386_DIR32NB
}

// Archs are the Go architectures IconSyso writes for.
var Archs = []string{"amd64", "386"}

// IconSyso writes the COFF object for goarch that holds icons as RT_ICON resources 1..n and their group as
// RT_GROUP_ICON GroupID, all in language Lang. Its bytes depend on nothing but its input (no time stamp).
func IconSyso(icons []Icon, goarch string) ([]byte, error) {
	m, ok := machines[goarch]
	if !ok {
		return nil, fmt.Errorf("winres: no COFF machine for GOARCH %q", goarch)
	}
	if len(icons) == 0 || len(icons) > 0xFFFF {
		return nil, fmt.Errorf("winres: %d icon images", len(icons))
	}
	sec, relocs := resourceSection(icons)

	le := binary.LittleEndian
	const fileHeader, sectionHeader = 20, 40
	rawOff := fileHeader + sectionHeader
	relOff := rawOff + len(sec)
	symOff := relOff + 10*len(relocs)
	var out bytes.Buffer
	w := func(v any) { _ = binary.Write(&out, le, v) }
	// IMAGE_FILE_HEADER
	w(m.machine)
	w(uint16(1))      // NumberOfSections
	w(uint32(0))      // TimeDateStamp
	w(uint32(symOff)) // PointerToSymbolTable
	w(uint32(1))      // NumberOfSymbols
	w(uint16(0))      // SizeOfOptionalHeader
	w(uint16(0))      // Characteristics
	// IMAGE_SECTION_HEADER
	out.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0))           // VirtualSize
	w(uint32(0))           // VirtualAddress
	w(uint32(len(sec)))    // SizeOfRawData
	w(uint32(rawOff))      // PointerToRawData
	w(uint32(relOff))      // PointerToRelocations
	w(uint32(0))           // PointerToLinenumbers
	w(uint16(len(relocs))) // NumberOfRelocations
	w(uint16(0))           // NumberOfLinenumbers
	w(uint32(0x40000040))  // IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
	out.Write(sec)
	// IMAGE_RELOCATION: each data entry's OffsetToData holds its data's offset in the section, the addend to the
	// section symbol's image-relative address
	for _, r := range relocs {
		w(r)         // VirtualAddress
		w(uint32(0)) // SymbolTableIndex: the section symbol
		w(m.reloc)   // Type
	}
	// the section symbol (IMAGE_SYMBOL), the static symbol named after its section that the linker relocates to
	out.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0)) // Value
	w(int16(1))  // SectionNumber
	w(uint16(0)) // Type
	w(uint8(3))  // StorageClass: IMAGE_SYM_CLASS_STATIC
	w(uint8(0))  // NumberOfAuxSymbols
	w(uint32(4)) // the string table: only its size
	return out.Bytes(), nil
}

// resourceSection lays out the resource directory: the type table, a name table per type, a language table per
// resource, then the data entries, then the data (8-aligned). It returns the section and the offsets of the data
// entries' OffsetToData fields, which need relocations.
func resourceSection(icons []Icon) ([]byte, []uint32) {
	type res struct {
		typ, id uint32
		data    []byte
	}
	var list []res
	for i, ic := range icons {
		list = append(list, res{RTIcon, uint32(i + 1), ic.Data})
	}
	list = append(list, res{RTGroupIcon, GroupID, GroupIcon(icons)})

	const dirSize, entrySize, dataEntrySize = 16, 8, 16
	types := []struct {
		typ   uint32
		items []int // indexes into list
	}{{RTIcon, nil}, {RTGroupIcon, nil}}
	for i, r := range list {
		for t := range types {
			if types[t].typ == r.typ {
				types[t].items = append(types[t].items, i)
			}
		}
	}
	// offsets
	off := dirSize + entrySize*len(types)
	nameDir := make([]int, len(types))
	for t := range types {
		nameDir[t] = off
		off += dirSize + entrySize*len(types[t].items)
	}
	langDir := make([]int, len(list))
	for i := range list {
		langDir[i] = off
		off += dirSize + entrySize
	}
	dataEntry := make([]int, len(list))
	for i := range list {
		dataEntry[i] = off
		off += dataEntrySize
	}
	dataOff := make([]int, len(list))
	for i, r := range list {
		off = (off + 7) &^ 7
		dataOff[i] = off
		off += len(r.data)
	}
	off = (off + 7) &^ 7

	sec := make([]byte, off)
	le := binary.LittleEndian
	dir := func(at, ids int) { le.PutUint16(sec[at+14:], uint16(ids)) } // no named entries, no time stamp
	entry := func(at int, id uint32, target int, subdir bool) {
		le.PutUint32(sec[at:], id)
		v := uint32(target)
		if subdir {
			v |= 0x80000000
		}
		le.PutUint32(sec[at+4:], v)
	}
	dir(0, len(types))
	for t, ty := range types {
		entry(dirSize+entrySize*t, ty.typ, nameDir[t], true)
		dir(nameDir[t], len(ty.items))
		for k, i := range ty.items {
			entry(nameDir[t]+dirSize+entrySize*k, list[i].id, langDir[i], true)
		}
	}
	relocs := make([]uint32, len(list))
	for i, r := range list {
		dir(langDir[i], 1)
		entry(langDir[i]+dirSize, Lang, dataEntry[i], false)
		le.PutUint32(sec[dataEntry[i]:], uint32(dataOff[i])) // OffsetToData (relocated)
		le.PutUint32(sec[dataEntry[i]+4:], uint32(len(r.data)))
		relocs[i] = uint32(dataEntry[i])
		copy(sec[dataOff[i]:], r.data)
	}
	return sec, relocs
}

// Resource is one resource read back from a resource section.
type Resource struct {
	Type, ID, Lang uint32
	Data           []byte
}

// ReadResources reads the resources with numeric type and ID from a resource section. dataBase is subtracted from
// each data entry's OffsetToData: the section's virtual address in a program, 0 in a .syso (whose data entries
// hold their section offsets before relocation). Named entries are skipped.
func ReadResources(sec []byte, dataBase uint32) ([]Resource, error) {
	le := binary.LittleEndian
	bad := func(what string, at uint32) error {
		return fmt.Errorf("winres: %s at 0x%x out of the section", what, at)
	}
	type entry struct{ id, target uint32 }
	readDir := func(at uint32) ([]entry, error) {
		if uint64(at)+16 > uint64(len(sec)) {
			return nil, bad("directory", at)
		}
		named, ids := uint32(le.Uint16(sec[at+12:])), uint32(le.Uint16(sec[at+14:]))
		n := named + ids
		if uint64(at)+16+8*uint64(n) > uint64(len(sec)) {
			return nil, bad("directory entries", at)
		}
		var es []entry
		for k := named; k < n; k++ {
			e := at + 16 + 8*k
			es = append(es, entry{le.Uint32(sec[e:]), le.Uint32(sec[e+4:])})
		}
		return es, nil
	}
	var out []Resource
	types, err := readDir(0)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.target&0x80000000 == 0 {
			return nil, fmt.Errorf("winres: type %d is not a directory", t.id)
		}
		names, err := readDir(t.target &^ 0x80000000)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n.target&0x80000000 == 0 {
				return nil, fmt.Errorf("winres: resource %d/%d is not a directory", t.id, n.id)
			}
			langs, err := readDir(n.target &^ 0x80000000)
			if err != nil {
				return nil, err
			}
			for _, l := range langs {
				if l.target&0x80000000 != 0 || uint64(l.target)+16 > uint64(len(sec)) {
					return nil, bad("data entry", l.target)
				}
				rva, size := le.Uint32(sec[l.target:]), le.Uint32(sec[l.target+4:])
				if rva < dataBase || uint64(rva-dataBase)+uint64(size) > uint64(len(sec)) {
					return nil, bad("data", rva)
				}
				start := rva - dataBase
				out = append(out, Resource{t.id, n.id, l.id, sec[start : start+size]})
			}
		}
	}
	return out, nil
}
