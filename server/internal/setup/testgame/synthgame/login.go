package synthgame

import (
	"encoding/binary"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
)

// LoginScreen is the synthetic LoginScreen.snap: the login movie's path with a made-up movie of one frame (a sprite,
// its symbol, a placed object), so the server picker's setup step has something to patch.
func LoginScreen() []byte { return movieSnap(moviepatch.Picker.MoviePath, "maxis.LoginScreen") }

// Battlegrounds is the synthetic Battlegrounds.snap: the CREATE GAME dialog's and the After Action Review's movie
// paths with the same kind of made-up movie each, for the setup steps of the bot check box and the review's tabs.
func Battlegrounds() []byte {
	return snap.Pack([]snap.File{
		{Name: moviepatch.MatchOptions.MoviePath, Data: movie("maxis.MatchOptionsUI")},
		{Name: moviepatch.AfterAction.MoviePath, Data: movie("maxis.AfterActionReview")},
	})
}

// Global is the synthetic Global.snap: the chat movie's path with the same kind of made-up movie, for the setup step
// of the chat rooms opened by default.
func Global() []byte { return movieSnap(moviepatch.ChatRooms.MoviePath, "maxis.chat.Chat") }

// HUD is the synthetic HUD.snap: the item shop's movie path with the same kind of made-up movie, for the setup step
// of the shop's inline refund.
func HUD() []byte { return movieSnap(moviepatch.ItemShop.MoviePath, "maxis.Hud.ItemShopUI") }

// Code is the synthetic code.snap: the front end's movie path with the same kind of made-up movie, for the setup step
// of the Collection's items.
func Code() []byte {
	return movieSnap(moviepatch.Collection.MoviePath, "com.gravity.framework.Gravity")
}

func movieSnap(path, docClass string) []byte {
	return snap.Pack([]snap.File{{Name: path, Data: movie(docClass)}})
}

// movie is a made-up movie of one frame (a sprite, its symbol docClass, a placed object).
func movie(docClass string) []byte {
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
	m := binary.LittleEndian.AppendUint32([]byte("CFX\x0c"), uint32(len(body)+8))
	return append(m, moviepatch.ZlibStored(body)...)
}
