package blaze

import "dawngate/server/internal/tdf"

// Rooms component (0x15), standard BlazeSDK (chat rooms and lobbies).
const (
	ComponentRooms                = 0x15
	CmdRoomsSelectViewUpdate      = 0x0A
	CmdRoomsSelectCategoryUpdates = 0x0B
)

func registerRooms(s *Server) {
	// selectViewUpdates (SelectViewUpdatesRequest UPDT 1, right after enterService in b5_run3): the RPC
	// job (0x011F7C00 at 0x00C92072) has no response class, so the reply is empty. We don't send room
	// view notifications.
	s.Register(ComponentRooms, CmdRoomsSelectViewUpdate, "selectViewUpdates", func(r *Request) []byte { return nil })
	// selectCategoryUpdates (VWID 0): the rooms manager's teardown (0x00C97660, at a logout) sends it with an
	// empty callback, followed by selectViewUpdates and toggleJoinedRoomNotifications (0x96; not seen live).
	s.Register(ComponentRooms, CmdRoomsSelectCategoryUpdates, "selectCategoryUpdates", func(r *Request) []byte {
		id, _ := r.Body.Int("VWID")
		return SelectCategoryUpdatesReply(uint64(id))
	})
}

// SelectCategoryUpdatesReply is Rooms::selectCategoryUpdates -> Blaze::Rooms::SelectCategoryUpdatesResponse (member
// table 0x012FFAC0: VWID, uint64): the request's view id back. The only sender (0x00C928C0, called by the rooms
// manager's teardown 0x00C97660 with VWID 0 when the client logs out) passes no callback, so nothing reads it; on the
// public server it was the last request of each logout, unanswered (LBFIX).
func SelectCategoryUpdatesReply(viewID uint64) []byte {
	return tdf.NewWriter().Int("VWID", int64(viewID)).Bytes()
}
