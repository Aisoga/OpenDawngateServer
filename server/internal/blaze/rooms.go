package blaze

// Rooms component (0x15), standard BlazeSDK (chat rooms and lobbies).
const (
	ComponentRooms           = 0x15
	CmdRoomsSelectViewUpdate = 0x0A
)

func registerRooms(s *Server) {
	// selectViewUpdates (SelectViewUpdatesRequest UPDT 1, right after enterService in b5_run3): the RPC
	// job (0x011F7C00 at 0x00C92072) has no response class, so the reply is empty. We don't send room
	// view notifications.
	s.Register(ComponentRooms, CmdRoomsSelectViewUpdate, "selectViewUpdates", func(r *Request) []byte { return nil })
}
