package blaze

import (
	"testing"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf/tdfcheck"
)

// ROOMS1: the chat rooms' golden replies, notifications and requests (tdf_golden.py ROOMS1_CASES, written from the
// client's member tables; the requests rebuilt from a static reading of its senders) decode as the client's classes.
// The builders that must produce these bytes come with the rooms themselves.
func TestROOMS1GoldenMatchClientMemberTables(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct{ name, class string }{
		{"roomviewadded_notification", "Blaze::Rooms::RoomViewData"},
		{"roomcategoryadded_notification", "Blaze::Rooms::RoomCategoryData"},
		{"roomadded_notification", "Blaze::Rooms::RoomData"},
		{"roomadded_second_notification", "Blaze::Rooms::RoomData"},
		{"roomupdated_notification", "Blaze::Rooms::RoomData"},
		{"selectcategoryupdates_view_reply", "Blaze::Rooms::SelectCategoryUpdatesResponse"},
		{"joinroom_reply", "Blaze::Rooms::JoinRoomResponse"},
		{"joinroom_second_reply", "Blaze::Rooms::JoinRoomResponse"},
		{"roommemberjoined_notification", "Blaze::Rooms::RoomMemberData"},
		{"roommemberjoined_second_notification", "Blaze::Rooms::RoomMemberData"},
		{"roommemberleft_notification", "Blaze::Rooms::RoomMemberRemoved"},
		{"leaveroom_reply", "Blaze::Rooms::LeaveRoomResponse"},
		{"roompopulationupdated_notification", "Blaze::Rooms::RoomsPopulationUpdate"},
		{"roomremoved_notification", "Blaze::Rooms::RoomRemoved"},
		{"notifymessage_room_notification", "Blaze::Messaging::ServerMessage"},
		// ROOMS3: the room the client opens by itself, in the category CTID 1000
		{"roomcategoryadded_open_notification", "Blaze::Rooms::RoomCategoryData"},
		{"roomadded_open_notification", "Blaze::Rooms::RoomData"},
		{"joinroom_open_reply", "Blaze::Rooms::JoinRoomResponse"},
		{"leaveroom_open_reply", "Blaze::Rooms::LeaveRoomResponse"},
	} {
		tdfcheck.Require(t, c.class, golden.Bytes(t, g.WriterCase(t, c.name).Hex))
	}
	requests := map[string]string{
		"selectviewupdates_request":             "Blaze::Rooms::SelectViewUpdatesRequest",
		"selectcategoryupdates_view_request":    "Blaze::Rooms::SelectCategoryUpdatesRequest",
		"joinroom_request":                      "Blaze::Rooms::JoinRoomRequest",
		"joinroom_open_request":                 "Blaze::Rooms::JoinRoomRequest",
		"leaveroom_request":                     "Blaze::Rooms::LeaveRoomRequest",
		"togglejoinedroomnotifications_request": "Blaze::Rooms::ToggleRoomNotificationsRequest",
		"sendmessage_room_request":              "Blaze::Messaging::ClientMessage",
	}
	for _, c := range g.Decode {
		if class, ok := requests[c.Name]; ok {
			tdfcheck.Require(t, class, golden.Bytes(t, c.Hex))
			delete(requests, c.Name)
		}
	}
	for name := range requests {
		t.Errorf("no decode case %q in tdf_golden.json", name)
	}
}
