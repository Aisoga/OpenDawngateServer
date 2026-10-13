// Package fire implements Blaze's "Fire" framing (as in Darkspore's BlazeSDK; docs/BLAZE.md):
//
//	u16 payload length, u16 component, u16 command, u16 error, u32 (message type << 28 | message id)
//
// followed by a TDF payload of that length. Message types: 0 request, 1 reply, 2 notification,
// 3 error reply. Requests start at message id 0; a reply carries its request's id.
package fire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tdf"
)

// MsgType is a frame's message type.
type MsgType uint8

// Message types.
const (
	Request      MsgType = 0
	Reply        MsgType = 1
	Notification MsgType = 2
	ErrorReply   MsgType = 3
)

var msgTypeNames = [...]string{"request", "reply", "notification", "error-reply"}

func (t MsgType) String() string {
	if int(t) < len(msgTypeNames) {
		return msgTypeNames[t]
	}
	return fmt.Sprint(uint8(t))
}

// HeaderLen is the size of a frame header.
const HeaderLen = 12

// Frame is one Fire message.
type Frame struct {
	Component uint16
	Command   uint16
	Error     uint16
	Type      MsgType
	ID        uint32 // 20 bits on the wire
	Payload   []byte
}

// Encode returns the frame's wire bytes. Payloads are limited to 65535 bytes (the u16 length).
func Encode(f Frame) ([]byte, error) {
	if len(f.Payload) > 0xFFFF {
		return nil, fmt.Errorf("fire: payload of %d bytes doesn't fit the u16 length", len(f.Payload))
	}
	b := make([]byte, HeaderLen, HeaderLen+len(f.Payload))
	binary.BigEndian.PutUint16(b[0:], uint16(len(f.Payload)))
	binary.BigEndian.PutUint16(b[2:], f.Component)
	binary.BigEndian.PutUint16(b[4:], f.Command)
	binary.BigEndian.PutUint16(b[6:], f.Error)
	binary.BigEndian.PutUint32(b[8:], uint32(f.Type)<<28|f.ID&0xFFFFF)
	return append(b, f.Payload...), nil
}

// Reader splits a byte stream into frames.
type Reader struct {
	src   io.Reader
	buf   []byte
	chunk []byte
}

// NewReader reads frames from src.
func NewReader(src io.Reader) *Reader { return &Reader{src: src, chunk: make([]byte, 65536)} }

// Next returns the next complete frame. At the end of the stream it returns io.EOF (or the read
// error); Pending then holds any bytes that didn't form a whole frame.
func (r *Reader) Next() (Frame, error) {
	for {
		if len(r.buf) >= HeaderLen {
			n := int(binary.BigEndian.Uint16(r.buf[0:]))
			if len(r.buf) >= HeaderLen+n {
				msg := binary.BigEndian.Uint32(r.buf[8:])
				f := Frame{
					Component: binary.BigEndian.Uint16(r.buf[2:]),
					Command:   binary.BigEndian.Uint16(r.buf[4:]),
					Error:     binary.BigEndian.Uint16(r.buf[6:]),
					Type:      MsgType(msg >> 28),
					ID:        msg & 0xFFFFF,
					Payload:   append([]byte(nil), r.buf[HeaderLen:HeaderLen+n]...),
				}
				r.buf = r.buf[HeaderLen+n:]
				return f, nil
			}
		}
		n, err := r.src.Read(r.chunk)
		r.buf = append(r.buf, r.chunk[:n]...)
		if err != nil && n == 0 {
			return Frame{}, err
		}
	}
}

// Pending returns the bytes read but not yet returned as a frame.
func (r *Reader) Pending() []byte { return r.buf }

// Components names the Blaze component ids for the log; preAuth announces them (CIDS). These are the
// 19 components compiled into the client (B4, docs/data/blaze_components.txt): each component's
// constructor stores its id at +8 next to its vtable, whose slot 1 returns "<Name>Component". The first
// 13 are standard BlazeSDK 3.x components; 0x081F to 0x0827 are Dawngate's own, registered from
// snlobby_platform_blaze.cpp. BGAMetagame is not a component: its classes belong to
// BattlegroundsAccounts (getBasicData 0x73, getCompleteData 0x76).
var Components = map[uint16]string{
	1: "Authentication", 4: "GameManager", 5: "Redirector", 6: "Playgroups", 7: "Stats",
	9: "Util", 10: "CensusData", 15: "Messaging", 21: "Rooms", 25: "AssociationLists",
	27: "GpsContentController", 28: "GameReporting", 0x7802: "UserSessions",
	0x081F: "BattlegroundsAccounts", 0x0820: "BGEcommerce", 0x0823: "BGOps", 0x0825: "Entry",
	0x0826: "LivingLore", 0x0827: "BGWeb",
}

// ComponentName returns a component's name, or "component<id>".
func ComponentName(id uint16) string {
	if n, ok := Components[id]; ok {
		return n
	}
	return fmt.Sprintf("component%d", id)
}

// ComponentIDs returns the known component ids in ascending order.
func ComponentIDs() []uint16 {
	ids := make([]uint16, 0, len(Components))
	for id := range Components {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Describe logs an incoming frame: a header line and its decoded TDF (or a hexdump if it doesn't
// decode). the password members (SecretLabels) are written as *** (an empty one stays empty), and a
// payload that doesn't decode but holds such a member's tag isn't dumped, so no log holds a password.
func Describe(log *logging.Log, who string, f Frame) {
	log.Logf(who, "<- %s(0x%04x) cmd 0x%04x %s id %d err %#x, %d bytes",
		ComponentName(f.Component), f.Component, f.Command, f.Type, f.ID, f.Error, len(f.Payload))
	s, err := tdf.Decode(f.Payload)
	if err != nil {
		log.Log(who, fmt.Sprintf("   (TDF decode failed: %v)\n", err)+SafeHexdump(f.Payload))
		return
	}
	MaskSecrets(s)
	log.Log(who, "   TDF:\n"+tdf.Pretty(s, 2))
}

// SecretLabels are the TDF members that hold a password (docs/data/tdf_member_tables.txt): LoginRequest,
// CreateAccountParameters, ExpressLoginRequest and CreatePersonaRequest's PASS, UpdateAccountRequest's CPWD, the
// Rooms' PSWD. Logs never show their values.
var SecretLabels = []string{"PASS", "CPWD", "PSWD"}

// Masked is what a non-empty secret member is logged as.
const Masked = "***"

func isSecret(label string) bool {
	for _, l := range SecretLabels {
		if label == l {
			return true
		}
	}
	return false
}

// MaskSecrets replaces the value of every non-empty secret member (SecretLabels) in s, at any depth, with Masked; an
// empty one stays empty (so the log still shows the client's empty retry after a refused login).
func MaskSecrets(s *tdf.Struct) {
	for i := range s.Fields {
		f := &s.Fields[i]
		if isSecret(f.Label) {
			switch v := f.Value.(type) {
			case string:
				if v != "" {
					f.Value = Masked
				}
			case []byte:
				if len(v) > 0 {
					f.Value = Masked
				}
			default:
				f.Value = Masked
			}
			continue
		}
		maskValue(f.Value)
	}
}

func maskValue(v tdf.Value) {
	switch x := v.(type) {
	case *tdf.Struct:
		MaskSecrets(x)
	case *tdf.List:
		for _, it := range x.Items {
			maskValue(it)
		}
	case *tdf.Map:
		for _, e := range x.Entries {
			maskValue(e.Value)
		}
	case *tdf.Union:
		if x.Field != nil { // a union member is a field of its own: mask it as a struct of one
			one := tdf.Struct{Fields: []tdf.Field{*x.Field}}
			MaskSecrets(&one)
			*x.Field = one.Fields[0]
		}
	case *tdf.ListUnion:
		if x.Value != nil {
			MaskSecrets(x.Value)
		}
	}
}

// SafeHexdump is logging.Hexdump of bytes that may hold a request, unless they contain the wire tag of a secret
// member (SecretLabels): then only a note, since the bytes could hold a password.
func SafeHexdump(b []byte) string {
	for _, l := range SecretLabels {
		tag := tdf.EncodeLabel(l)
		if bytes.Contains(b, tag[:]) {
			return fmt.Sprintf("    (%d bytes not dumped: they hold the tag %s, which may carry a password)", len(b), l)
		}
	}
	return logging.Hexdump(b)
}

// LogEnd logs the end of a connection: leftover bytes and a read error other than EOF, if any.
func LogEnd(log *logging.Log, who string, r *Reader, err error) {
	if p := r.Pending(); len(p) > 0 {
		log.Logf(who, "connection closed with %d unparsed bytes:\n%s", len(p), SafeHexdump(p))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		log.Logf(who, "read error: %s: %v", logging.ErrName(err), err)
	}
}
