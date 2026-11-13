package exepatch

import (
	"bytes"
	"encoding/binary"
	"math"
)

// The grab-style middle-mouse drag scroll (patch_client.py's DRAG_*, drag_grab_code, drag_grab_sites), switched in the
// game by the preference OptionDragGrab. GameplayControls_UpdateEdgeAndDragScroll (0x00697D80) runs the joystick
// while the middle button's trigger is down; the hook sends its active check into .dgx's draggrab piece, which reads
// the preference at each drag's first frame: off runs the two replaced instructions and the shipped joystick; on
// keeps the ground point grabbed at the drag's start under the cursor by moving the camera's current and target
// look-at points. In the hooked frame ebx is the camera, [esp+0x08] and [esp+0x4C] are AddTranslateInput's dx and dy,
// and the byte [esp+0x18] is the epilogue's "trigger down" flag for SetDragScrollActive.
const (
	dragHook           = 0x00697EB8 // the joystick's first instructions (12 bytes): jmp into .dgx + 7 nops
	dragResume         = 0x00697EC4 // jne 0x00697ED5 (later frames) / the first frame's SetDragScrollActive(1)
	dragNoMove         = 0x006980B5 // the shared "no move" path
	dragMoved          = 0x0069810B // the shared "moved" path: stopwatch, end a temporary lock, mode-6 translate
	dragActive         = 0x01977FDD // byte: a drag is active
	appPrefs           = 0x01BA4118 // the AppPrefs object
	appPrefsGetBool    = 0x00A83800 // thiscall (key), ret 4; a missing key is false
	dragGrabKey        = 0x14958012 // FNV-1 of "optiondraggrab"
	setDragActive      = 0x00697CC0 // GameplayControls_SetDragScrollActive(bool), cdecl
	getCursorGround    = 0x006993A0 // GameplayControls_GetCursorGroundPoint(out, fromMinimap), cdecl
	cameraManager      = 0x00A6F910 // its slot +0x6C, then +0x1C: the viewer
	viewerGetCursorRay = 0x00A6B670 // thiscall viewer (start*, end*), ret 8
	isSpectating       = 0x0066D010 // the observer test before the cursor's jump back
	lookAtCurrent      = 0x013F5724 // SPGearsCamera's look-at point, current (x, y, z)
	lookAtTarget       = 0x013F5730 // and target
	dragState          = 0x01C000C0 // in .tls's slack after the rating text
	dragStateRoom      = 0x30
	dragG0             = dragState        // the grabbed ground point (x, y, z)
	dragGrab           = dragState + 0x0C // byte: this drag is a grab
	dragRayStart       = dragState + 0x10 // the cursor ray's start (x, y, z)
	dragRayEnd         = dragState + 0x1C // and end
	dragEps2           = 1e-6             // |d|^2 below this (1 mm): the mouse stopped
	dragSpecOffset     = 0x1A0            // the spectator helper's offset in the piece
	dragEps2Offset     = 0x1C0            // the constant's offset
)

var dragHookOrig = mustHex("803ddd7f970100" + "c644241801") // cmp byte [0x01977FDD],0; mov byte [esp+0x18],1

// dragSpecSites are the two observer release sites: call IsSpectating before the cursor is put back on the anchor.
var dragSpecSites = []struct {
	va   uint32
	orig []byte
}{
	{0x00697D3C, mustHex("e8cf52fdff")}, // SetDragScrollActive(0)
	{0x006981A0, mustHex("e86b4efdff")}, // the no-camera path
}

// dragGrabCode is the draggrab piece (patch_client.py's drag_grab_code; its docstring has the assembly): the cave the
// hook jumps to, the spectator helper at +dragSpecOffset and the constant dragEps2 at +dragEps2Offset.
func dragGrabCode() []byte {
	va, _, _ := dgxPieceAt("draggrab")
	var c []byte
	labels := map[string]int{}
	type fixup struct {
		at, size int
		label    string
	}
	var fixups []fixup
	emit := func(h string, tail ...[]byte) {
		c = append(c, mustHex(h)...)
		for _, t := range tail {
			c = append(c, t...)
		}
	}
	to := func(op string, target uint32) { // call/jmp rel32 to an exe address
		emit(op)
		c = append(c, le32(target-(va+uint32(len(c))+4))...)
	}
	short := func(op, label string) {
		emit(op)
		fixups = append(fixups, fixup{len(c), 1, label})
		c = append(c, 0)
	}
	near := func(op, label string) {
		emit(op)
		fixups = append(fixups, fixup{len(c), 4, label})
		c = append(c, 0, 0, 0, 0)
	}
	label := func(name string) { labels[name] = len(c) }

	label("cave")
	emit("803d", le32(dragActive), []byte{0}) // cmp byte [active],0
	short("75", "later")
	emit("68", le32(dragGrabKey))   // push key
	emit("8b0d", le32(appPrefs))    // mov ecx,[appprefs]
	to("e8", appPrefsGetBool)       // call AppPrefs_GetBool
	emit("a2", le32(dragGrab))      // mov [grab],al
	emit("84c0")                    // test al,al
	short("74", "shipped")          // jz shipped
	emit("c644241801")              // mov byte [esp+0x18],1
	emit("6a01")                    // push 1
	to("e8", setDragActive)         // call SetDragScrollActive
	emit("6a00"+"68", le32(dragG0)) // push 0; push G0
	to("e8", getCursorGround)       // call GetCursorGroundPoint
	emit("83c40c")                  // add esp,0xC
	to("e9", dragNoMove)
	label("later")
	emit("803d", le32(dragGrab), []byte{0}) // cmp byte [grab],0
	short("75", "grab")
	label("shipped")
	c = append(c, dragHookOrig...) // the two hooked instructions
	to("e9", dragResume)
	label("grab")
	emit("c644241801")
	to("e8", cameraManager)
	emit("8b10" + "8bc8" + "ff526c") // call [vt+0x6c]
	emit("8b10" + "8bc8" + "ff521c") // call [vt+0x1c]: the viewer
	emit("68", le32(dragRayEnd), []byte{0x68}, le32(dragRayStart))
	emit("8bc8")
	to("e8", viewerGetCursorRay)
	emit("f30f1015", le32(dragRayEnd+8))                              // movss xmm2,[end.z]
	emit("f30f5c15", le32(dragRayStart+8))                            // subss xmm2,[start.z]
	emit("0f57db" + "0f2ed3")                                         // xorps xmm3,xmm3; ucomiss xmm2,xmm3
	near("0f8a", "nomove")                                            // jp
	near("0f84", "nomove")                                            // jz
	emit("f30f1005", le32(dragG0+8))                                  // movss xmm0,[G0.z]
	emit("f30f5c05", le32(dragRayStart+8))                            // subss xmm0,[start.z]
	emit("f30f5ec2")                                                  // divss xmm0,xmm2: t
	for axis, regs := range [][2]string{{"15", "d1"}, {"1d", "d9"}} { // x into xmm2, y into xmm3
		off := uint32(4 * axis)
		emit("f30f100d", le32(dragRayEnd+off))
		emit("f30f5c0d", le32(dragRayStart+off))
		emit("f30f59c8")
		emit("f30f580d", le32(dragRayStart+off)) // G
		emit("f30f10"+regs[0], le32(dragG0+off))
		emit("f30f5c" + regs[1]) // d = G0 - G
	}
	emit("0f28c2" + "f30f59c2")
	emit("0f28cb" + "f30f59cb")
	emit("f30f58c1")
	emit("0f2f05", le32(va+dragEps2Offset)) // comiss xmm0,[eps2]
	short("76", "nomove")
	emit("837b3006") // cmp dword [ebx+0x30],6
	short("75", "zero")
	emit("8b432c" + "83f803")
	short("74", "zero")
	emit("83f804")
	short("74", "zero")
	for _, point := range []uint32{lookAtCurrent, lookAtTarget} {
		for axis, reg := range []string{"c2", "c3"} {
			p := point + uint32(4*axis)
			emit("f30f1005", le32(p))
			emit("f30f58" + reg)
			emit("f30f1105", le32(p))
		}
	}
	label("zero")
	emit("33c0" + "89442408" + "8944244c")
	to("e9", dragMoved)
	label("nomove")
	to("e9", dragNoMove)
	for _, f := range fixups {
		rel := labels[f.label] - (f.at + f.size)
		if f.size == 1 {
			if rel < -128 || rel > 127 {
				panic("draggrab: short jump to " + f.label + " out of range")
			}
			c[f.at] = byte(int8(rel))
		} else {
			binary.LittleEndian.PutUint32(c[f.at:], uint32(int32(rel)))
		}
	}
	if len(c) > dragSpecOffset {
		panic("draggrab: the cave runs into the spectator helper")
	}
	c = append(c, make([]byte, dragSpecOffset-len(c))...)
	emit("803d", le32(dragGrab), []byte{0}) // spec: cmp byte [grab],0
	emit("7505")                            // jne +5
	to("e9", isSpectating)
	emit("33c0" + "c3") // xor eax,eax; ret
	c = append(c, make([]byte, dragEps2Offset-len(c))...)
	return binary.LittleEndian.AppendUint32(c, math.Float32bits(dragEps2))
}

// dragGrabSite is one place the drag grab rewrites.
type dragGrabSite struct {
	va        uint32
	orig, new []byte
}

// dragGrabSites are the hook and the two spectator sites (patch_client.py's drag_grab_sites).
func dragGrabSites() []dragGrabSite {
	va, _, _ := dgxPieceAt("draggrab")
	hook := append(append([]byte{0xE9}, le32(va-(dragHook+5))...), bytes.Repeat([]byte{0x90}, len(dragHookOrig)-5)...)
	sites := []dragGrabSite{{dragHook, dragHookOrig, hook}}
	for _, s := range dragSpecSites {
		sites = append(sites, dragGrabSite{s.va, s.orig, append([]byte{0xE8}, le32(va+dragSpecOffset-(s.va+5))...)})
	}
	return sites
}

// describeDragGrab is Description["draggrab"]: "on", "as-shipped" or "unknown".
func describeDragGrab(img *peImage, dgx string) string {
	orig, patched := true, dgx == "on"
	for _, s := range append(dragGrabSites(), dragOptionsSites()...) {
		got, err := img.bytesAt(s.va, len(s.orig))
		if err != nil {
			return "unknown"
		}
		orig = orig && bytes.Equal(got, s.orig)
		patched = patched && bytes.Equal(got, s.new)
	}
	if orig {
		return "as-shipped"
	}
	if patched {
		va, _, _ := dgxPieceAt("draggrab")
		code := dragGrabCode()
		got, err := img.bytesAt(va, len(code))
		if err != nil || !bytes.Equal(got, code) {
			return "unknown"
		}
		table, err := img.bytesAt(optTable, optTableSize)
		if err != nil {
			return "unknown"
		}
		want, err := dragOptionsPiece(table)
		if err != nil {
			return "unknown"
		}
		optVA, _, _ := dgxPieceAt("options")
		if got, err := img.bytesAt(optVA, len(want)); err == nil && bytes.Equal(got, want) {
			return "on"
		}
	}
	return "unknown"
}

// The Options menu's gameplay table (DRAGGRAB2b; patch_client.py's OPT_*, drag_options_piece, drag_options_sites):
// OptionsUI_GetGameSettings (0x004D3F90) and OptionsUI_SaveSettings (0x004D60C0) walk 12 entries {u32 FNV-1 of the
// lower-case name, char *name} at optTable. The patch copies them from the exe into .dgx's options piece with a 13th
// entry {dragGrabKey, "OptionDragGrab"} and points both functions at the copy (four disp32 operands, two bounds
// 0x60 -> 0x68). The option itself (its settings, default and Preferences.prop name) is config data (configpatch).
const (
	optTable      = 0x011D1988
	optTableSize  = 12 * 8
	optNameOffset = 0x70 // the name's offset in the options piece, after the 13 entries
)

var (
	optTableFirst = le32(0x90FBF794) // the table's first hash: FNV-1 of "optionfilterprofanity"
	optName       = []byte("OptionDragGrab\x00")
)

// dragOptionsPiece is the options piece: table (EA's 12 entries, read from the exe being patched), the 13th entry,
// zeros, the name at optNameOffset.
func dragOptionsPiece(table []byte) ([]byte, error) {
	if len(table) != optTableSize {
		return nil, errorf("options table: %d bytes, expected %d", len(table), optTableSize)
	}
	va, _, _ := dgxPieceAt("options")
	b := append(bytes.Clone(table), le32(dragGrabKey)...)
	b = append(b, le32(va+optNameOffset)...)
	b = append(b, make([]byte, optNameOffset-len(b))...)
	return append(b, optName...), nil
}

// dragOptionsSites are the table's four readers and the two loop bounds.
func dragOptionsSites() []dragGrabSite {
	va, _, _ := dgxPieceAt("options")
	mov := func(op string, disp uint32) []byte { return append(mustHex(op), le32(disp)...) }
	return []dragGrabSite{
		{0x004D4070, mov("8bb7", optTable), mov("8bb7", va)},     // mov esi,[edi+table]   (hash)
		{0x004D40AC, mov("8b87", optTable+4), mov("8b87", va+4)}, // mov eax,[edi+table+4] (name)
		{0x004D414E, mustHex("83ff60"), mustHex("83ff68")},       // cmp edi,0x60 -> 0x68
		{0x004D66A0, mov("8b95", optTable+4), mov("8b95", va+4)}, // mov edx,[ebp+table+4] (name)
		{0x004D66A6, mov("8b8d", optTable), mov("8b8d", va)},     // mov ecx,[ebp+table]   (hash)
		{0x004D66BE, mustHex("83fd60"), mustHex("83fd68")},       // cmp ebp,0x60 -> 0x68
	}
}
