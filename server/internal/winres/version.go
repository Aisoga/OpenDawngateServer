package winres

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf16"
)

// RTVersion is the version resource's type; a program has one, with the ID VersionID.
const (
	RTVersion = 16
	VersionID = 1
)

// The version resource's string table: en-US (Lang), Unicode (code page 1200).
const (
	versionTable   = "040904B0"
	versionCharset = 0x04B0
)

// Version is what a program's version resource says: the strings Windows shows in a file's Properties (Details) and
// that code signing services check (SignPath's metadata restrictions: product name and version), and the numbers of
// its fixed part (VS_FIXEDFILEINFO), which Windows uses for comparisons.
type Version struct {
	ProductName, ProductVersion, FileVersion, FileDescription string
	InternalName, OriginalFilename, LegalCopyright            string
	Numbers                                                   [4]uint16 // file and product version: major.minor.patch.build
	DLL                                                       bool      // VFT_DLL, else VFT_APP
}

// The keys of the string table, in the order written; an empty value is left out.
func (v Version) strings() [][2]string {
	all := [][2]string{
		{"FileDescription", v.FileDescription}, {"FileVersion", v.FileVersion}, {"InternalName", v.InternalName},
		{"LegalCopyright", v.LegalCopyright}, {"OriginalFilename", v.OriginalFilename},
		{"ProductName", v.ProductName}, {"ProductVersion", v.ProductVersion},
	}
	var out [][2]string
	for _, kv := range all {
		if kv[1] != "" {
			out = append(out, kv)
		}
	}
	return out
}

var numberedName = regexp.MustCompile(`^v?(\d{1,5})\.(\d{1,5})(?:\.(\d{1,5}))?$`)

// NumbersOf gives a release name's numbers for the fixed part: v<major>.<minor>[.<patch>] (a numbered release) gives
// them, any other name (a nightly or test build) 0.0.0.0. A number above 65535 gives 0.0.0.0 too.
func NumbersOf(name string) [4]uint16 {
	m := numberedName.FindStringSubmatch(name)
	if m == nil {
		return [4]uint16{}
	}
	var n [4]uint16
	for i, s := range m[1:] {
		if s == "" {
			continue
		}
		x, err := strconv.ParseUint(s, 10, 16)
		if err != nil {
			return [4]uint16{}
		}
		n[i] = uint16(x)
	}
	return n
}

// A VS_VERSIONINFO node: wLength, wValueLength, wType, the key (UTF-16, NUL), padding to 4, the value, then the
// children, each starting at a multiple of 4. wValueLength counts bytes for a binary value (wType 0) and UTF-16
// units (with the NUL) for a text one (wType 1).
func versionNode(key string, value []byte, valueLen uint16, text bool, children ...[]byte) []byte {
	le := binary.LittleEndian
	b := make([]byte, 6)
	le.PutUint16(b[2:], valueLen)
	if text {
		le.PutUint16(b[4:], 1)
	}
	b = append(b, utf16z(key)...)
	b = pad4(b)
	b = append(b, value...)
	for _, c := range children {
		b = append(pad4(b), c...)
	}
	le.PutUint16(b[0:], uint16(len(b)))
	return b
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u)+2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

// Resource is the version resource (RT_VERSION) holding v.
func (v Version) Resource() []byte {
	le := binary.LittleEndian
	n := v.Numbers
	fixed := make([]byte, 52)
	ms, ls := uint32(n[0])<<16|uint32(n[1]), uint32(n[2])<<16|uint32(n[3])
	fileType := uint32(1) // VFT_APP
	if v.DLL {
		fileType = 2 // VFT_DLL
	}
	for i, x := range []uint32{
		0xFEEF04BD, 0x00010000, // the signature, the structure's version
		ms, ls, ms, ls, // file version, product version
		0x3F, 0, // the flags' mask, no flags
		0x00040004, fileType, 0, // VOS_NT_WINDOWS32, the type, no subtype
		0, 0, // no date
	} {
		le.PutUint32(fixed[4*i:], x)
	}
	var strs [][]byte
	for _, kv := range v.strings() {
		val := utf16z(kv[1])
		strs = append(strs, versionNode(kv[0], val, uint16(len(val)/2), true))
	}
	table := versionNode(versionTable, nil, 0, true, strs...)
	translation := make([]byte, 4)
	le.PutUint16(translation, Lang)
	le.PutUint16(translation[2:], versionCharset)
	return versionNode("VS_VERSION_INFO", fixed, uint16(len(fixed)), false,
		versionNode("StringFileInfo", nil, 0, true, table),
		versionNode("VarFileInfo", nil, 0, true, versionNode("Translation", translation, 4, false)))
}

// ParseVersion reads a version resource: the strings of its first string table and the numbers of its fixed part
// (the file version's; Resource writes the same product version).
func ParseVersion(b []byte) (map[string]string, [4]uint16, error) {
	le := binary.LittleEndian
	var nums [4]uint16
	type node struct {
		key      string
		value    []byte
		children []byte
	}
	parse := func(b []byte) (node, int, error) {
		if len(b) < 6 {
			return node{}, 0, errors.New("winres: version node too short")
		}
		total, vlen, typ := int(le.Uint16(b)), int(le.Uint16(b[2:])), le.Uint16(b[4:])
		if total < 6 || total > len(b) {
			return node{}, 0, fmt.Errorf("winres: version node length %d of %d bytes", total, len(b))
		}
		b = b[:total]
		var key []uint16
		at := 6
		for ; at+1 < total; at += 2 {
			c := le.Uint16(b[at:])
			if c == 0 {
				break
			}
			key = append(key, c)
		}
		if at+1 >= total {
			return node{}, 0, errors.New("winres: version node key without its end")
		}
		at = (at + 2 + 3) &^ 3
		size := vlen
		if typ == 1 {
			size = 2 * vlen
		}
		if at > total || at+size > total {
			return node{}, 0, errors.New("winres: version node value out of the node")
		}
		n := node{key: string(utf16.Decode(key)), value: b[at : at+size]}
		at = (at + size + 3) &^ 3
		if at < total {
			n.children = b[at:]
		}
		return n, total, nil
	}
	each := func(b []byte, f func(node) error) error {
		for len(b) > 0 {
			n, size, err := parse(b)
			if err != nil {
				return err
			}
			if err := f(n); err != nil {
				return err
			}
			size = (size + 3) &^ 3
			if size >= len(b) {
				break
			}
			b = b[size:]
		}
		return nil
	}
	root, _, err := parse(b)
	if err != nil {
		return nil, nums, err
	}
	if root.key != "VS_VERSION_INFO" || len(root.value) != 52 || le.Uint32(root.value) != 0xFEEF04BD {
		return nil, nums, errors.New("winres: not a version resource")
	}
	ms, ls := le.Uint32(root.value[8:]), le.Uint32(root.value[12:])
	nums = [4]uint16{uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls)}
	strs := map[string]string{}
	err = each(root.children, func(n node) error {
		if n.key != "StringFileInfo" {
			return nil
		}
		first := true
		return each(n.children, func(t node) error {
			if !first {
				return nil
			}
			first = false
			return each(t.children, func(s node) error {
				u := make([]uint16, 0, len(s.value)/2)
				for i := 0; i+1 < len(s.value); i += 2 {
					c := le.Uint16(s.value[i:])
					if c == 0 {
						break
					}
					u = append(u, c)
				}
				strs[s.key] = string(utf16.Decode(u))
				return nil
			})
		})
	})
	if err != nil {
		return nil, nums, err
	}
	return strs, nums, nil
}

// ProgramResources reads the resources of a program or DLL (its .rsrc section); none when it has no such section.
func ProgramResources(path string) ([]Resource, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := f.Section(".rsrc")
	if s == nil {
		return nil, nil
	}
	data, err := s.Data()
	if err != nil {
		return nil, err
	}
	return ReadResources(data[:min(int(s.VirtualSize), len(data))], s.VirtualAddress)
}

// ProgramVersion reads a program's or DLL's version resource (the first one, of any language).
func ProgramVersion(path string) (map[string]string, [4]uint16, error) {
	res, err := ProgramResources(path)
	if err != nil {
		return nil, [4]uint16{}, err
	}
	for _, r := range res {
		if r.Type == RTVersion {
			return ParseVersion(r.Data)
		}
	}
	return nil, [4]uint16{}, errors.New("no version resource")
}
