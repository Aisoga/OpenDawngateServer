package blaze

import "dawngate/server/internal/tdf"

// BGWeb component (0x0827), Dawngate's own (B4): version of the web content.
const (
	ComponentBGWeb         = 0x0827
	CmdBGWebGetCurrentVers = 0x6E
)

func registerBGWeb(s *Server) {
	s.Register(ComponentBGWeb, CmdBGWebGetCurrentVers, "getCurrentVersion", func(r *Request) []byte {
		return BGWebCurrentVersionReply()
	})
}

// BGWebCurrentVersionReply is BGWeb::getCurrentVersion (empty request) ->
// Blaze::BGWeb::GetCurrentVersionResponse (member table 0x012FC738: DRC string; RPC job 0x012B829C at
// 0x00ACECAF). DRC is the web content's version: an empty one fails the assert "pVersion && *pVersion"
// in WebContentManager.cpp (session b5_run4). WebContentVersion is our placeholder.
func BGWebCurrentVersionReply() []byte {
	return tdf.NewWriter().String("DRC", WebContentVersion).Bytes()
}

// WebContentVersion is the web-content version we announce (BGWeb DRC).
const WebContentVersion = "1"
