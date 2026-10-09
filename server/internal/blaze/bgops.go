package blaze

import "dawngate/server/internal/tdf"

// BGOps component (0x0823), Dawngate's own (B4): operations; most of its commands are the match
// server's channel to Blaze, the client sends only a few.
const (
	ComponentBGOps            = 0x0823
	CmdBGOpsSetGameID         = 0x09
	CmdBGOpsGetGameID         = 0x0A
	CmdBGOpsGetCurrentVersion = 0x13
	CmdBGOpsIsUserMMLockedout = 0x20
	ClientBuildStamp          = "2014-11-04_10-25-56"
)

func registerBGOps(s *Server) {
	// G3 (reconnect.go): getGameID returns what the persona last saved with setGameID (a hosted game's PGID until the
	// game ended for it), so the client offers the reconnect.
	s.Register(ComponentBGOps, CmdBGOpsGetGameID, "getGameID", func(r *Request) []byte {
		gmid := ""
		if r.Conn.Session != nil {
			gmid = r.Server.reconnect.get(r.Conn.Session.Persona.ID)
		}
		if gmid != "" {
			r.Server.Log.Logf("blaze", "   getGameID: persona %d saved game %q (the client checks it for a reconnect)",
				r.Conn.Session.Persona.ID, gmid)
		}
		return GetGameIDReply(gmid)
	})
	// setGameID (SetPlayerGameIDRequest ACID 0, GMID '', right after getGameID in b5_run7): the response
	// class SetPlayerGameIDResponse has no member table in the dump (no members), so the reply is empty. G3: the
	// client saves NotifyGameSetup's PGID with it (cBlazeGameManager::SaveReconnectGameId 0x00AC2680) and clears
	// it with ''; kept per persona, in memory.
	s.Register(ComponentBGOps, CmdBGOpsSetGameID, "setGameID", func(r *Request) []byte {
		if r.Conn.Session != nil {
			gmid, _ := r.Body.Str("GMID")
			r.Server.reconnect.set(r.Conn.Session.Persona.ID, gmid)
		}
		return nil
	})
	s.Register(ComponentBGOps, CmdBGOpsGetCurrentVersion, "getCurrentVersion", func(r *Request) []byte {
		menv, _ := r.Body.Str("MENV")
		return BGOpsCurrentVersionReply(menv)
	})
	s.Register(ComponentBGOps, CmdBGOpsIsUserMMLockedout, "isUserMMLockedout", func(r *Request) []byte {
		return MMLockoutReply(0)
	})
}

// MMLockoutReply is BGOps::isUserMMLockedout (IsUserMMLockedoutRequest ACID = Blaze id, member table
// 0x012EED90; sent right after the main menu appears in b5_run8) -> Blaze::BGOps::IsUserMMLockedoutResponse
// (0x012EF1E8: LOCK uint64; RPC job 0x011F3354 at 0x006823BF). LOCK is the lockout's length in seconds
// from now, not its end: the callback (0x00687000, "cGameEntryManager::UpdateLockoutTimeCb") stores now +
// LOCK * 1000 ms (0x00685A40), and while it runs the play screen shows a "-mm:ss" countdown and
// matchmaking is blocked (R9, docs/UI_CONTRACT.md); 0 = not locked out.
func MMLockoutReply(until int64) []byte {
	return tdf.NewWriter().Int("LOCK", until).Bytes()
}

// BGOpsCurrentVersionReply is BGOps::getCurrentVersion (GetCurrentVersionRequest MENV, the manifest
// environment: registry value HKCU/HKLM\Software\Electronic Arts\Dawngate\Manifest\env, default
// 'prod-info') -> Blaze::BGOps::GetCurrentVersionResponse (member table 0x012EF208; RPC job 0x012B6358
// at 0x00AC1EDF). It is cBlazeGameManager::InitiateVersionCheck (0x00AC4260, snlobby_game_manager.cpp).
// Its callback (0x00AC3C50) takes NVSR up to the first '.', compares it with the client's build stamp
// (string at 0x011FA410, ClientBuildStamp; Application.log's "Build version: 1.0.903.854,2014-11-04_
// 10-25-56") and raises event 0x103C0ACE with the result, which LoginState::Update step 22 waits for:
// equal goes on, different presumably starts the patch flow. The other members (build date, manifest
// URL, ...) aren't read there; we fill MENV and leave the rest at their defaults.
func BGOpsCurrentVersionReply(menv string) []byte {
	return tdf.NewWriter().String("MENV", menv).String("NVSR", ClientBuildStamp).Bytes()
}

// GetGameIDReply is BGOps::getGameID (GetPlayerGameIDRequest ACID, member table 0x012EED30; the client
// sends ACID 0 after createAccountAsync in b5_run6) -> Blaze::BGOps::GetPlayerGameIDResponse (0x012EF1C0:
// GMID string; RPC job 0x012B6378 at 0x00AC1FEF). The game's callback (0x00AC4040) treats a non-empty
// GMID as a match to rejoin (it then asks GameManager for that game); an empty one raises event
// 0x37F0634E, which LoginState::Update step 20 waits for. We have no running matches.
func GetGameIDReply(gameID string) []byte {
	return tdf.NewWriter().String("GMID", gameID).Bytes()
}
