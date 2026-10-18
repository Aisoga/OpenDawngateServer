package synthgame

import (
	"encoding/binary"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
)

// LoginScreen is the synthetic LoginScreen.snap: the login movie's path with a made-up movie of one frame (a sprite,
// its symbol, a placed object), so the server picker's setup step has something to patch.
func LoginScreen() []byte { return movieSnap(moviepatch.Picker.MoviePath, "maxis.LoginScreen") }

// Battlegrounds is the synthetic Battlegrounds.snap: the CREATE GAME dialog's movie path with the same kind of
// made-up movie, for the bot check box's setup step.
func Battlegrounds() []byte {
	return movieSnap(moviepatch.MatchOptions.MoviePath, "maxis.MatchOptionsUI")
}

func movieSnap(path, docClass string) []byte {
	tag := func(code int, data []byte) []byte {
		return append(binary.LittleEndian.AppendUint16(nil, uint16(code<<6|len(data))), data...)
	}
	var body []byte
	body = append(body, 0, 0, 24, 1, 0) // an empty RECT, 24 fps, 1 frame
	body = append(body, tag(69, []byte{8, 0, 0, 0})...)
	body = append(body, tag(39, append([]byte{5, 0, 1, 0}, append(tag(1, nil), tag(0, nil)...)...))...)
	body = append(body, tag(76, []byte("\x01\x00\x05\x00"+docClass+"\x00"))...)
	body = append(body, tag(26, []byte{2, 3, 0, 5, 0})...)
	body = append(body, tag(1, nil)...)
	body = append(body, tag(0, nil)...)
	movie := binary.LittleEndian.AppendUint32([]byte("CFX\x0c"), uint32(len(body)+8))
	movie = append(movie, moviepatch.ZlibStored(body)...)
	return snap.Pack([]snap.File{{Name: path, Data: movie}})
}
