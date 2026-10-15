// Package moviepatch makes the server picker's login movie (re/scripts/patch_login_movie.py, byte for byte): the
// player's own login movie, UI/Scaleform/LoginScreen/LoginScreen_BG.gfx from game\Data\LoginScreen.snap, with the
// picker's four SWF tags (picker_tags.hex: our compiled ActionScript class, client_ui/serverpicker, built by
// re/scripts/build_picker.py) before the root timeline's first ShowFrame, in an added archive
// game\Data\zz_ServerPicker.snap that replaces the movie's path. The game's own tags and bytecode are kept as they
// are; nothing of the game is shipped (the movie is made from the player's files).
//
// The output is deterministic: the movie keeps its CFX header (the version, the u32 length as stored plus the tags'
// size) and its body is a zlib stream of stored blocks (65,535 bytes each, the last one final), which the archive
// compresses (Snappy, snap.Encode). The picker's character id and depth (2000 each) must be free in the movie.
package moviepatch

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/adler32"
	"io"
	"strings"

	"dawngate/server/internal/setup/snap"
)

// PickerTags are the picker's four tags: DefineSprite 2000, DoABC (the class), SymbolClass 2000 -> ServerPicker,
// PlaceObject3 of 2000 at depth 2000 without an instance name.
//
// They are committed as hex text (re/scripts/build_picker.py), so the repository holds no binary.
var PickerTags = mustHex(pickerTagsHex)

//go:embed picker_tags.hex
var pickerTagsHex string

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		panic("moviepatch: picker_tags.hex: " + err.Error())
	}
	return b
}

const (
	// Source is the login screen's archive in a copy; Rel the added archive.
	Source = "game/Data/LoginScreen.snap"
	Rel    = "game/Data/zz_ServerPicker.snap"
	// MoviePath is the login movie's path in both archives.
	MoviePath = "UI/Scaleform/LoginScreen/LoginScreen_BG.gfx"
	// CharID and Depth are the picker sprite's character id and root depth; ClassName its class.
	CharID    = 2000
	Depth     = 2000
	ClassName = "ServerPicker"
)

// defining are the tags whose first u16 is the character id they define (standard SWF and Scaleform's 1001-1009).
var defining = map[int]bool{2: true, 6: true, 7: true, 10: true, 11: true, 14: true, 20: true, 21: true, 22: true, 32: true,
	33: true, 34: true, 35: true, 36: true, 37: true, 39: true, 46: true, 48: true, 60: true, 75: true, 83: true, 84: true,
	87: true, 90: true, 91: true, 1001: true, 1003: true, 1005: true, 1006: true, 1007: true, 1008: true, 1009: true}

type tagPos struct{ code, at, start, end int }

var le = binary.LittleEndian

// tagsOf lists buf[pos:end]'s tags up to and including the first End tag.
func tagsOf(buf []byte, pos, end int) ([]tagPos, error) {
	var out []tagPos
	for pos < end {
		if pos+2 > end {
			return nil, fmt.Errorf("a tag header runs past its container at %#x", pos)
		}
		h := int(le.Uint16(buf[pos:]))
		code, ln, hdr := h>>6, h&0x3F, 2
		if ln == 0x3F {
			if pos+6 > end {
				return nil, fmt.Errorf("a long tag header runs past its container at %#x", pos)
			}
			ln, hdr = int(le.Uint32(buf[pos+2:])), 6
		}
		start := pos + hdr
		if ln < 0 || start+ln > end {
			return nil, fmt.Errorf("tag %d at %#x runs past its container", code, pos)
		}
		out = append(out, tagPos{code, pos, start, start + ln})
		pos = start + ln
		if code == 0 {
			break
		}
	}
	return out, nil
}

func cstringEnd(buf []byte, pos, end int) (int, error) {
	if pos > end {
		return 0, errors.New("a string past its tag")
	}
	i := bytes.IndexByte(buf[pos:end], 0)
	if i < 0 {
		return 0, errors.New("a string without its NUL")
	}
	return pos + i + 1, nil
}

type pair struct {
	id   int
	name string
}

// pairs reads a u16 count and (u16 id, NUL-terminated name) pairs.
func pairs(buf []byte, pos, end int) ([]pair, error) {
	if pos+2 > end {
		return nil, errors.New("a symbol list past its tag")
	}
	n := int(le.Uint16(buf[pos:]))
	pos += 2
	out := make([]pair, 0, n)
	for range n {
		if pos+2 > end {
			return nil, errors.New("a symbol list past its tag")
		}
		id := int(le.Uint16(buf[pos:]))
		next, err := cstringEnd(buf, pos+2, end)
		if err != nil {
			return nil, err
		}
		out = append(out, pair{id, string(buf[pos+2 : next-1])})
		pos = next
	}
	return out, nil
}

// scanned is what scan finds in a movie body.
type scanned struct {
	ids, depths map[int]bool
	names       map[string]bool
	firstShow   int // the root's first ShowFrame's offset, -1 for none
}

func scan(body []byte) (*scanned, error) {
	if len(body) < 1 {
		return nil, errors.New("an empty movie")
	}
	nbits := int(body[0] >> 3)
	pos := (5+4*nbits+7)/8 + 4
	if pos > len(body) {
		return nil, errors.New("the movie's header runs past its end")
	}
	s := &scanned{ids: map[int]bool{}, depths: map[int]bool{}, names: map[string]bool{}, firstShow: -1}
	var walk func(buf []byte, start, end int, root bool) error
	walk = func(buf []byte, start, end int, root bool) error {
		tags, err := tagsOf(buf, start, end)
		if err != nil {
			return err
		}
		for _, t := range tags {
			if defining[t.code] && t.end-t.start >= 2 {
				s.ids[int(le.Uint16(buf[t.start:]))] = true
			}
			switch t.code {
			case 39:
				if t.end-t.start >= 4 {
					if err := walk(buf, t.start+4, t.end, false); err != nil {
						return err
					}
				}
			case 56, 76: // ExportAssets, SymbolClass
				ps, err := pairs(buf, t.start, t.end)
				if err != nil {
					return err
				}
				for _, p := range ps {
					s.ids[p.id] = true
					s.names[p.name] = true
				}
			case 57, 71: // ImportAssets, ImportAssets2
				p, err := cstringEnd(buf, t.start, t.end)
				if err != nil {
					return err
				}
				if t.code == 71 {
					p += 2
				}
				ps, err := pairs(buf, p, t.end)
				if err != nil {
					return err
				}
				for _, q := range ps {
					s.ids[q.id] = true
				}
			}
			if root {
				switch {
				case t.code == 26 && t.end-t.start >= 3:
					s.depths[int(le.Uint16(buf[t.start+1:]))] = true
				case t.code == 70 && t.end-t.start >= 4:
					s.depths[int(le.Uint16(buf[t.start+2:]))] = true
				case t.code == 1 && s.firstShow < 0:
					s.firstShow = t.at
				}
			}
		}
		return nil
	}
	if err := walk(body, pos, len(body), true); err != nil {
		return nil, err
	}
	return s, nil
}

// ZlibStored is a zlib stream of stored deflate blocks (65,535 bytes each, the last one final), Adler-32 at the end.
func ZlibStored(data []byte) []byte {
	out := make([]byte, 0, len(data)+len(data)/0xFFFF*5+16)
	out = append(out, 0x78, 0x01)
	for i := 0; ; {
		n := min(len(data)-i, 0xFFFF)
		final := i+n >= len(data)
		var b byte
		if final {
			b = 1
		}
		out = append(out, b)
		out = le.AppendUint16(out, uint16(n))
		out = le.AppendUint16(out, uint16(n)^0xFFFF)
		out = append(out, data[i:i+n]...)
		i += n
		if final {
			break
		}
	}
	return binary.BigEndian.AppendUint32(out, adler32.Checksum(data))
}

// PatchMovie returns the login movie (a CFX file) with tags inserted before the root timeline's first ShowFrame.
func PatchMovie(raw, tags []byte) ([]byte, error) {
	if len(raw) < 8 || (string(raw[:3]) != "CFX" && string(raw[:3]) != "CWS") {
		return nil, errors.New("not a compressed Scaleform movie (CFX)")
	}
	length := le.Uint32(raw[4:])
	zr, err := zlib.NewReader(bytes.NewReader(raw[8:]))
	if err != nil {
		return nil, fmt.Errorf("zlib: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("zlib: %v", err)
	}
	s, err := scan(body)
	if err != nil {
		return nil, err
	}
	switch {
	case s.names[ClassName]:
		return nil, errors.New("the movie has the server picker already")
	case s.ids[CharID]:
		return nil, fmt.Errorf("the movie uses character id %d", CharID)
	case s.depths[Depth]:
		return nil, fmt.Errorf("the movie uses depth %d on its root timeline", Depth)
	case s.firstShow < 0:
		return nil, errors.New("no ShowFrame on the root timeline")
	}
	nb := make([]byte, 0, len(body)+len(tags))
	nb = append(append(append(nb, body[:s.firstShow]...), tags...), body[s.firstShow:]...)
	out := append([]byte(nil), raw[:4]...)
	out = le.AppendUint32(out, length+uint32(len(tags)))
	return append(out, ZlibStored(nb)...), nil
}

// Build returns zz_ServerPicker.snap's bytes from the player's LoginScreen.snap.
func Build(loginSnap []byte) ([]byte, error) {
	a, err := snap.Parse(loginSnap)
	if err != nil {
		return nil, err
	}
	for _, e := range a.Entries {
		if e.Name != MoviePath {
			continue
		}
		raw, err := a.Read(e)
		if err != nil {
			return nil, err
		}
		movie, err := PatchMovie(raw, PickerTags)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", MoviePath, err)
		}
		return snap.Pack([]snap.File{{Name: MoviePath, Data: movie}}), nil
	}
	return nil, fmt.Errorf("no %s in the archive", MoviePath)
}
