package exepatch

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// peImage is the little of a PE32 file the patches need: the file header's place and fields, DllCharacteristics and
// the section table (with each header's file offset). Offsets of virtual addresses follow pefile's
// get_offset_from_rva, which patch_client.py uses.
type peImage struct {
	data            []byte
	fileHeader      int // offset of IMAGE_FILE_HEADER (after "PE\0\0"; pefile's FILE_HEADER.get_file_offset())
	characteristics uint16
	dllChars        uint16
	fileAlign       uint32
	sectionAlign    uint32
	sections        []peSection // in table order
}

type peSection struct {
	name       string
	va, vsize  uint32
	rawPtr     uint32
	rawSize    uint32
	headerAt   int    // file offset of the IMAGE_SECTION_HEADER
	nextVA     uint32 // the next section's VirtualAddress by address order (0: none)
	hasNextVA  bool
	adjVA      uint32
	adjRawPtr  uint32
	sizeForRVA uint32
}

func parsePE(data []byte) (*peImage, error) {
	u16 := func(off int) uint16 { return binary.LittleEndian.Uint16(data[off:]) }
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }
	if len(data) < 0x40 || data[0] != 'M' || data[1] != 'Z' {
		return nil, fmt.Errorf("not a PE file (no MZ header)")
	}
	pe := int(u32(0x3C))
	if pe < 0 || pe+24 > len(data) || string(data[pe:pe+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("not a PE file (no PE signature)")
	}
	img := &peImage{data: data, fileHeader: pe + 4}
	fh := img.fileHeader
	nsec := int(u16(fh + 2))
	optSize := int(u16(fh + 16))
	img.characteristics = u16(fh + 18)
	opt := fh + 20
	if opt+optSize > len(data) || optSize < 72 || u16(opt) != 0x10B {
		return nil, fmt.Errorf("not a PE32 file (optional header)")
	}
	img.sectionAlign = u32(opt + 32)
	img.fileAlign = u32(opt + 36)
	img.dllChars = u16(opt + 70)
	table := opt + optSize
	if table+nsec*40 > len(data) {
		return nil, fmt.Errorf("truncated section table")
	}
	for i := 0; i < nsec; i++ {
		h := table + i*40
		name := data[h : h+8]
		for len(name) > 0 && name[len(name)-1] == 0 {
			name = name[:len(name)-1]
		}
		img.sections = append(img.sections, peSection{
			name: string(name), vsize: u32(h + 8), va: u32(h + 12), rawSize: u32(h + 16), rawPtr: u32(h + 20),
			headerAt: h,
		})
	}
	// pefile: each section's next_section_virtual_address by VirtualAddress order, the alignment adjustments, and
	// the size contains_rva uses
	order := make([]int, len(img.sections))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return img.sections[order[a]].va < img.sections[order[b]].va })
	for k, i := range order {
		s := &img.sections[i]
		if k+1 < len(order) {
			s.nextVA, s.hasNextVA = img.sections[order[k+1]].va, true
		}
		s.adjVA = img.adjustSectionAlignment(s.va)
		s.adjRawPtr = img.adjustFileAlignment(s.rawPtr)
		size := s.vsize // pefile: a SizeOfRawData beyond the file's end is not believed
		if uint64(s.adjRawPtr)+uint64(s.rawSize) <= uint64(len(data)) && s.rawSize > size {
			size = s.rawSize
		}
		if s.hasNextVA && s.nextVA > s.va && uint64(s.adjVA)+uint64(size) > uint64(s.nextVA) {
			size = s.nextVA - s.adjVA
		}
		s.sizeForRVA = size
	}
	return img, nil
}

// pefile's adjust_FileAlignment: raw pointers are rounded down to 0x200 unless the file alignment is smaller.
func (p *peImage) adjustFileAlignment(v uint32) uint32 {
	if p.fileAlign < 0x200 {
		return v
	}
	return v / 0x200 * 0x200
}

// pefile's adjust_SectionAlignment.
func (p *peImage) adjustSectionAlignment(v uint32) uint32 {
	align := p.sectionAlign
	if align < 0x1000 {
		align = p.fileAlign
	}
	if align != 0 && v%align != 0 {
		return align * (v / align)
	}
	return v
}

// offset is the file offset of virtual address va (image base ImageBase), with n bytes available there.
func (p *peImage) offset(va uint32, n int) (int, error) {
	rva := va - ImageBase
	for _, s := range p.sections {
		if s.adjVA <= rva && uint64(rva) < uint64(s.adjVA)+uint64(s.sizeForRVA) {
			off := int(rva-s.adjVA) + int(s.adjRawPtr)
			if off+n > len(p.data) {
				return 0, fmt.Errorf("0x%08x is beyond the end of the file (wrong build?)", va)
			}
			return off, nil
		}
	}
	if int(rva)+n <= len(p.data) { // pefile: an address in no section is taken as a file offset (the headers)
		return int(rva), nil
	}
	return 0, fmt.Errorf("0x%08x is in no section (wrong build?)", va)
}

// bytesAt returns n bytes at va (a view into the data).
func (p *peImage) bytesAt(va uint32, n int) ([]byte, error) {
	off, err := p.offset(va, n)
	if err != nil {
		return nil, err
	}
	return p.data[off : off+n], nil
}
