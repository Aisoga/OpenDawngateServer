package blaze

import (
	"strconv"
	"sync/atomic"

	"dawngate/server/internal/tdf"
)

// GpsContentController component (0x1B), standard BlazeSDK: the in-game player report.
const (
	ComponentGpsContentController = 0x1B
	CmdGpsFilePetition            = 0x01
)

// petitions numbers the player reports since dgserver started (the reply's GUID).
var petitions atomic.Int64

func registerGpsContentController(s *Server) {
	s.Register(ComponentGpsContentController, CmdGpsFilePetition, "filePetition", func(r *Request) []byte {
		n := petitions.Add(1)
		var from int64
		if r.Conn.Session != nil {
			from = r.Conn.Session.Persona.ID
		}
		targets := intList(r.Body, "TRGT")
		kind, _ := r.Body.Str("COTY")
		// the request itself, with its chat lines (PTDE), is in the log above this line
		r.Server.Log.Logf("blaze", "   player report %d from persona %d about %v (%s)", n, from, targets, kind)
		return FilePetitionReply("report-" + strconv.FormatInt(n, 10))
	})
}

// FilePetitionReply is GpsContentController::filePetition -> Blaze::GpsContentController::FilePetitionResponse
// (member table 0x012FC3B0: GUID, a string). The request (FilePetitionRequest 0x012FC350) is the player report of the
// player context menu (sender 0x00AC9CF0, built in 0x00ACD2F0: SUBJ "Player Report (name: %s) (type: %i, %s)", COTY
// the reason, PTDE the match's chat lines as JSON, TMZO 'GMT-8', TRGT the reported persona ids). Its callback
// (0x00ACCA50) shows the locale line PlayerReport!0x00000100 on a reply without an error and PlayerReport!0x00000101
// with the error code otherwise, and reads nothing from the reply; GUID is ours ("report-<n>", counted since the
// server started). Unanswered until LBFIX, the request stopped the client's keep-alive pings.
func FilePetitionReply(guid string) []byte {
	return tdf.NewWriter().String("GUID", guid).Bytes()
}
