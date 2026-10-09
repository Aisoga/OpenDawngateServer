package blaze

import "dawngate/server/internal/tdf"

// Entry component (0x0825), Dawngate's own (B4): a login queue. Commands from GetEntryCommandName.
const (
	ComponentEntry       = 0x0825
	CmdEntryEnterService = 0x78
)

func registerEntry(s *Server) {
	s.Register(ComponentEntry, CmdEntryEnterService, "enterService", func(r *Request) []byte {
		return EnterServiceReply()
	})
}

// EnterServiceReply is Entry::enterService (empty EnterServiceRequest) -> Blaze::Entry::EnterServiceResponse
// (member table 0x01462308: CHEK +0x0C, QPOS +0x14, RESP +0x08, WAIT +0x10; RPC job 0x011F9118, sent
// from 0x006A6E80 by the game's LoginState step 10, 0x006A72F0).
//
// The game's callback (HandleEntryEnterServiceResponse 0x006A5E60) keeps RESP. LoginState::Update
// (0x006A7610, LoginState.cpp) step 11 then goes on with RESP 0 ("enter": asserts
// PlatformServices()->LoginCompleted() and moves to step 13); RESP 2 means "queued" (the callback
// passes CHEK to 0x008538E0, step 12 waits and asks again); any other value is a login error
// (0xFE94EEC). QPOS and WAIT are presumably the queue position and wait time. We always let the player
// in.
func EnterServiceReply() []byte {
	return tdf.NewWriter().Int("CHEK", 0).Int("QPOS", 0).Int("RESP", 0).Int("WAIT", 0).Bytes()
}
