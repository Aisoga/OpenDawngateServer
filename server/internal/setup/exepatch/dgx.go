package exepatch

import (
	"bytes"
	"encoding/binary"
)

// The section .dgx: room for code and data that no longer fit .text's slack (patch_client.py's add_dgx_section,
// dgx_piece and dgx_place). A sixth section, appended: its header in the free slot after the fifth, VirtualAddress
// dgxVA - ImageBase (the untouched exe's SizeOfImage), dgxSize bytes virtual and raw, its raw data after the file's
// end aligned to 0x200 (EA's certificate before it stays, unused), SizeOfImage and SizeOfCode raised, the security
// directory zeroed (the certificate has been invalid since the first patch and no longer ends the file), the PE
// checksum left stale as by every other patch (the loader checks it only for drivers). Code, execute and read, as
// .text; not writable: writable state goes into .tls's slack.
const (
	dgxName            = ".dgx"
	dgxVA              = 0x01C84000 // the untouched SizeOfImage: .rsrc (0x01C01000, 0x82338 bytes) ends in the page before
	dgxSize            = 0x1000     // one page; it may grow by whole pages, no piece ever moves
	dgxCharacteristics = 0x60000020 // IMAGE_SCN_CNT_CODE | MEM_EXECUTE | MEM_READ
	securityDir        = 4          // IMAGE_DIRECTORY_ENTRY_SECURITY
)

// dgxMagic is the section's first bytes: its name and the layout's version 1.
var dgxMagic = []byte{'D', 'G', 'X', 0, 1, 0, 0, 0}

// dgxPiece is a piece's fixed place in .dgx: its offset from dgxVA and its room in bytes.
type dgxPiece struct {
	name         string
	offset, room uint32
}

// dgxLayout is patch_client.py's DGX_LAYOUT. Append-only: a new piece goes after the last one (16-byte aligned), and a
// piece that has shipped never moves or shrinks, so the addresses the game's code is patched to reach stay where they
// are.
var dgxLayout = []dgxPiece{
	{"header", 0x000, 0x010},   // dgxMagic
	{"draggrab", 0x010, 0x3F0}, // dragGrabCode (452 bytes: the cave, the spectator helper, eps2)
	{"options", 0x400, 0x100},  // the gameplay options' table with a 13th entry, and its name (reserved)
}

// dgxPieceAt returns a piece's VA and room.
func dgxPieceAt(name string) (uint32, uint32, error) {
	for _, p := range dgxLayout {
		if p.name == name {
			return dgxVA + p.offset, p.room, nil
		}
	}
	return 0, 0, errorf("no piece %s in .dgx's layout", pyRepr(name))
}

// addDgxSection returns orig's bytes with the empty section .dgx appended (patch_client.py's add_dgx_section).
func addDgxSection(orig []byte) ([]byte, error) {
	img, err := parsePE(orig)
	if err != nil {
		return nil, err
	}
	data := bytes.Clone(orig)
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(data[off:], v) }
	fh := img.fileHeader
	opt := fh + 20
	sizeOfImage, sizeOfHeaders := u32(opt+56), u32(opt+60)
	named := false
	for _, s := range img.sections {
		named = named || s.name == dgxName
	}
	if len(img.sections) != 5 || img.sectionAlign != 0x1000 || img.fileAlign != 0x200 ||
		sizeOfImage != dgxVA-ImageBase || named {
		return nil, errorf("unexpected PE header for .dgx (%d sections, SizeOfImage %#x; wrong build, or not the untouched exe?)",
			len(img.sections), sizeOfImage)
	}
	var end, rawEnd uint64
	for _, s := range img.sections {
		end = max(end, (uint64(s.va)+uint64(s.vsize)+0xFFF)&^0xFFF)
		rawEnd = max(rawEnd, uint64(s.rawPtr)+uint64(s.rawSize))
	}
	if end != uint64(sizeOfImage) {
		return nil, errorf("the last section doesn't end at SizeOfImage (wrong build?)")
	}
	slot := img.sections[len(img.sections)-1].headerAt + 40
	if uint64(slot)+40 > uint64(sizeOfHeaders) || slot+40 > len(data) || !bytes.Equal(data[slot:slot+40], make([]byte, 40)) {
		return nil, errorf("no free section header after the last one (wrong build?)")
	}
	secDir := opt + 96 + 8*securityDir
	certAt, certLen := u32(secDir), u32(secDir+4)
	if (certAt != 0 || certLen != 0) && (uint64(certAt) < rawEnd || uint64(certAt)+uint64(certLen) != uint64(len(data))) {
		return nil, errorf("the certificate (%#x, %#x bytes) doesn't end the file (wrong build?)", certAt, certLen)
	}
	if uint64(len(data)) < rawEnd {
		return nil, errorf("the file ends before its last section's raw data (truncated?)")
	}
	put(secDir, 0)
	put(secDir+4, 0)
	raw := (len(data) + 0x1FF) &^ 0x1FF
	data = append(data, make([]byte, raw-len(data)+dgxSize)...)
	copy(data[slot:slot+8], dgxName)
	put(slot+8, dgxSize)          // VirtualSize
	put(slot+12, dgxVA-ImageBase) // VirtualAddress
	put(slot+16, dgxSize)         // SizeOfRawData
	put(slot+20, uint32(raw))     // PointerToRawData (relocations, line numbers: none)
	put(slot+36, dgxCharacteristics)
	binary.LittleEndian.PutUint16(data[fh+2:], uint16(len(img.sections)+1)) // NumberOfSections
	put(opt+56, sizeOfImage+dgxSize)                                        // SizeOfImage
	put(opt+4, u32(opt+4)+dgxSize)                                          // SizeOfCode
	return data, nil
}

// dgxPlace writes b at its piece's fixed place in .dgx (data: an exe that has the section; the room must still be
// empty) and returns its VA (patch_client.py's dgx_place). The API for patches whose code or data no longer fit
// .text's slack: declare the piece in dgxLayout (and DGX_LAYOUT), build its bytes for its VA, place them.
func dgxPlace(data []byte, piece string, b []byte) (uint32, error) {
	va, room, err := dgxPieceAt(piece)
	if err != nil {
		return 0, err
	}
	if uint32(len(b)) > room {
		return 0, errorf(".dgx's %s piece has room for %d bytes, not %d", piece, room, len(b))
	}
	img, err := parsePE(data)
	if err != nil {
		return 0, err
	}
	var sec *peSection
	for i := range img.sections {
		if img.sections[i].name == dgxName {
			sec = &img.sections[i]
			break
		}
	}
	if sec == nil || sec.va != dgxVA-ImageBase || sec.rawSize < dgxSize {
		return 0, errorf("%s needs the section .dgx (NoDgx unset)", piece)
	}
	at := int(sec.rawPtr) + int(va-dgxVA)
	if at+len(b) > len(data) {
		return 0, errorf(".dgx's %s piece is beyond the end of the file", piece)
	}
	if !bytes.Equal(data[at:at+len(b)], make([]byte, len(b))) {
		return 0, errorf(".dgx's %s piece is written already", piece)
	}
	copy(data[at:], b)
	return va, nil
}

// describeDgx is Description["dgx"]: "on" (the section as this package makes it), "as-shipped" (none) or "unknown".
func describeDgx(img *peImage) string {
	for _, s := range img.sections {
		if s.name != dgxName {
			continue
		}
		if s.va == dgxVA-ImageBase && s.vsize == dgxSize && s.rawSize == dgxSize && s.characteristics == dgxCharacteristics {
			if magic, err := img.bytesAt(dgxVA, len(dgxMagic)); err == nil && bytes.Equal(magic, dgxMagic) {
				return "on"
			}
		}
		return "unknown"
	}
	return "as-shipped"
}
