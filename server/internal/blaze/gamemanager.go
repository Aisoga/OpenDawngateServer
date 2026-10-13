package blaze

import (
	"fmt"
	"net"

	"dawngate/server/internal/tdf"
)

// GameManager component (4), BlazeSDK's standard one (B8). Command and notification ids from
// docs/data/blaze_components.txt; the notification dispatcher is 0x00CA6010 (jump table 0x00CA6EDC,
// handler functors at component +0x0C MatchmakingFailed, +0x24 MatchmakingAsyncStatus, +0x6C GameSetup,
// +0x9C JoiningPlayerInitiateConnections, +0xFC PlayerJoinCompleted, +0x1BC GameStateChange).
const (
	ComponentGameManager               = 0x0004
	CmdGameManagerRemovePlayer         = 0x0B
	CmdGameManagerResetDedicatedServer = 0x19
	CmdGameManagerUpdateMeshConnection = 0x1D
	NotifyGameSetupID                  = 0x14
)

// ServerBuildVersionAttr is the game attribute cBlazeGameManager::CreateGameResult (0x00AC6420) reads
// once the create succeeded, i.e. once the client is connected to the GMS (B9): it looks the name up in
// the game's attribute map (0x00C7F230) and builds an event string from the value (0x00966A20, posted
// with id 0x0DE0B523) without a null check. Without the attribute the client crashes there (session
// b9_run1: access violation at 0x00966A45, strlen of NULL). The original GMS presumably set it on the
// game it hosted; we announce the client's own version (VSTR), since no reader of the value was found.
const ServerBuildVersionAttr = "ServerBuildVersion"

// PlayerNetConnectionStatus (name table 0x012E7610), UpdateMeshConnectionRequest.TARG[].STAT.
var meshStatusNames = map[int64]string{0: "DISCONNECTED", 1: "ESTABLISHING_CONNECTION", 2: "CONNECTED"}

const meshConnected = 2 // PlayerNetConnectionStatus CONNECTED

// Enum values, from the client's enum name tables (name pointer, value pairs in .rdata).
const (
	GameStatePreGame              = 0x82 // Blaze::GameManager::GameState (0x012E7528): NEW_STATE 0, INITIALIZING 1, PRE_GAME 0x82, IN_GAME 0x83, ...
	TopologyClientServerDedicated = 1    // GameNetworkTopology (0x012F1A84): CLIENT_SERVER_PEER_HOSTED 0, CLIENT_SERVER_DEDICATED 1, PEER_TO_PEER_FULL_MESH 0x82
	PresenceModeStandard          = 1    // PresenceMode (0x012F1AAC): NONE 0, STANDARD 1, PRIVATE 2
	SetupContextCreateGame        = 0    // DatalessSetupContext.DCTX (0x012E77E0): CREATE_GAME_SETUP_CONTEXT 0, JOIN_GAME_SETUP_CONTEXT 1
	JoinStateJoinedGame           = 0    // JoinGameResponse.JGS (0x012E779C): JOINED_GAME 0, IN_QUEUE 1
	PlayerStateActiveConnecting   = 2    // PlayerState (0x012E75E0): RESERVED 0, QUEUED 1, ACTIVE_CONNECTING 2, ACTIVE_MIGRATING 3, ACTIVE_CONNECTED 4
	SlotPublic                    = 0    // SlotType (0x012E75C8): SLOT_PUBLIC 0, SLOT_PRIVATE 1
	NetworkAddressIpPair          = 2    // NetworkAddress union (0x012F22E8) member 2: Blaze::IpPairAddress
	setupReasonDataless           = 0    // GameSetupReason union (0x012EA098) member 0: DatalessSetupContext
)

// DedicatedServerID stands for the dedicated server (the GMS) as THST/PHST's host id. It must not be a
// player's id: the topology host of a CLIENT_SERVER_DEDICATED game is the server, not the client.
const DedicatedServerID = 3000000001

// GMSPort is our default GMS port: the port of the address NotifyGameSetup hands the client (HNET).
const GMSPort = 10042

// Addr is an IPv4 address and port as Blaze::IpAddress carries them (IP as a big-endian uint32).
type Addr struct {
	IP   uint32
	Port int64
}

// ParseAddr turns "a.b.c.d" and a port into an Addr; ok is false for anything but IPv4.
func ParseAddr(host string, port int) (Addr, bool) {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return Addr{}, false
	}
	return Addr{IP: uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3]), Port: int64(port)}, true
}

// Loopback keeps a's port but replaces an address outside 127.0.0.0/8 with 127.0.0.1. Every address we
// hand the client stays on this machine (no LAN or internet contact), so the player's
// own address in NotifyGameSetup (PNET, from the request's HNET: its LAN address as INIP) goes back as
// loopback.
func Loopback(a Addr) Addr {
	if a.IP>>24 != 127 {
		a.IP = 0x7F000001
	}
	return a
}

// ClientAddr is how a client's own address goes back to it (NotifyGameSetup's PNET). While the server
// announces a loopback address (dgserver's default) it is forced to loopback, as since B8: nothing we
// hand out points off this machine. Since A6 that is the announced address itself (a second test lane
// runs on 127.0.0.2, so its client's address goes back as 127.0.0.2, not as the other lane's 127.0.0.1).
// With a public host (dgserver --public-host, roadmap A5) clients on other machines are expected, and
// their addresses go back as they sent them, as Blaze echoes a player's network address. The client
// picks its GMS address from HNET alone (ClientNetworkAdapter_SelectNetworkAddress 0x00AB5160 compares
// the host's EXIP with its own adapter's address, not with PNET), so PNET doesn't steer where it connects.
func (s *Server) ClientAddr(a Addr) Addr {
	h, ok := ParseAddr(s.Host, 0)
	if !ok {
		return Loopback(a)
	}
	if h.IP>>24 == 127 {
		if a.IP>>24 != 127 {
			a.IP = h.IP
		}
		return a
	}
	return a
}

func (a Addr) String() string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", a.IP>>24, a.IP>>16&0xFF, a.IP>>8&0xFF, a.IP&0xFF, a.Port)
}

// ipPair writes a Blaze::IpPairAddress body (table 0x012F1B10: EXIP struct +0x18, INIP struct +0x08,
// each a Blaze::IpAddress 0x012F1ADC: IP uint32, PORT uint16).
func ipPair(w *tdf.Writer, ext, in Addr) {
	w.Struct("EXIP", func(w *tdf.Writer) { w.Int("IP", int64(ext.IP)).Int("PORT", ext.Port) })
	w.Struct("INIP", func(w *tdf.Writer) { w.Int("IP", int64(in.IP)).Int("PORT", in.Port) })
}

// Game is a game as NotifyGameSetup describes it. Most fields echo the client's CreateGameRequest.
type Game struct {
	ID       int64
	Name     string     // GNAM
	Attrs    []tdf.Pair // ATTR (string -> string), in the request's order
	Settings int64      // GSET
	Capacity []int64    // CAP: the request's PCAP (public, private slots)
	TeamCap  int64      // TCAP
	TeamIDs  []int64    // TIDS
	Version  string     // VSTR, the game protocol version string
	GMS      Addr       // the dedicated server's address, announced in HNET
	Seed     int64      // SEED
	// G3 (reconnect.go): PGID, the persisted game id the client saves for a reconnect (written only when set: every
	// hosted game has one), and GSTA (0 = PRE_GAME, the state at the start; a rejoin gets the state of the moment).
	PersistedID string
	State       int64
}

// Player is the game's one player, the client that created it.
type Player struct {
	ID       int64 // BUID (persona id), also the user session id (UID)
	Name     string
	Team     int64 // TIDX
	Ext, Int Addr  // the client's own addresses, from the request's HNET
	JoinTime int64 // TIME, microseconds
}

// JoinGameReply is GameManager::resetDedicatedServer's reply, Blaze::GameManager::JoinGameResponse
// (table 0x01461530: GID uint64 +0x08, JGS enum +0x10; RPC job 0x012E5510 sent from 0x00C7C5AA).
func JoinGameReply(gameID int64) []byte {
	return tdf.NewWriter().Int("GID", gameID).Int("JGS", JoinStateJoinedGame).Bytes()
}

// GameSetupNotification is GameManager::NotifyGameSetup (0x14, dispatcher case 0x00CA6688, class
// Blaze::GameManager::NotifyGameSetup table 0x012E7B60: GAME ptr struct, PROS list, QUEU list, REAS
// union). GAME is Blaze::GameManager::ReplicatedGameData (0x014615A8), PROS one
// ReplicatedGamePlayer (0x01461EA0), REAS the GameSetupReason union (0x012EA098) member 0,
// DatalessSetupContext {DCTX CREATE_GAME_SETUP_CONTEXT}.
//
// The client hands the game (a BlazeSDK Mesh) to its network adapter, cClientNetworkAdapter
// (snlobby_client_network_adapter.cpp). ClientNetworkAdapter_InitNetworkMesh (0x00AB6570) picks the
// GMS address with SelectNetworkAddress (0x00AB5160) and connects to it with cClientSession
// (0x00AB3980, "cClientSession initiating connection to GMS [addr=%s][port=%u]"). That address is the
// host's NetworkAddress, which must be member 2 (IpPairAddress) with a non-zero EXIP: if the game
// attribute InternalHost is set, or the host's EXIP equals the client's own, the client takes INIP,
// otherwise EXIP. We put the GMS address into both, so the client connects to it either way. HNET is a
// list of unions: see tdf.ListUnion.
//
// THST and PHST (Blaze::GameManager::HostInfo 0x012E76F0: HPID, HSLT) name the dedicated server, not
// the player. GSTA is PRE_GAME; NTOP CLIENT_SERVER_DEDICATED as the client asked. Members we don't
// know a use for keep their defaults.
func GameSetupNotification(g Game, p Player) []byte {
	w := tdf.NewWriter()
	w.Struct("GAME", func(w *tdf.Writer) { replicatedGameData(w, g, p.ID) })
	w.List("PROS", tdf.TypeStruct, []*tdf.Writer{replicatedGamePlayer(g, p, 0)})
	w.Union("REAS", setupReasonDataless, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { w.Int("DCTX", SetupContextCreateGame) })
	})
	return w.Bytes()
}

// replicatedGameData writes the game; admin is ADMN's one entry (the creator).
func replicatedGameData(w *tdf.Writer, g Game, admin int64) {
	replicatedGameDataAdmins(w, g, []int64{admin})
}

// replicatedGameDataAdmins writes the game with its admin list (B15: a custom game's ADMN, which the client's
// Player::isAdmin 0x00C7BCA0 reads: only an admin may start the game, set its attributes, move or kick players).
func replicatedGameDataAdmins(w *tdf.Writer, g Game, admins []int64) {
	maxCap := int64(0)
	for _, c := range g.Capacity {
		maxCap += c
	}
	host := func(w *tdf.Writer) { w.Int("HPID", DedicatedServerID).Int("HSLT", 0) }
	w.List("ADMN", tdf.TypeInt, admins)
	w.Map("ATTR", tdf.TypeString, tdf.TypeString, gameAttrs(g)...)
	w.List("CAP", tdf.TypeInt, g.Capacity)
	w.Int("GID", g.ID)
	w.String("GNAM", g.Name)
	w.Int("GSET", g.Settings)
	state := g.State
	if state == 0 {
		state = GameStatePreGame
	}
	w.Int("GSTA", state)
	w.List("HNET", tdf.TypeStruct, []tdf.UnionItem{{Member: NetworkAddressIpPair,
		Body: func() *tdf.Writer { b := tdf.NewWriter(); ipPair(b, g.GMS, g.GMS); return b }()}})
	w.Int("MCAP", maxCap)
	w.Int("NTOP", TopologyClientServerDedicated)
	if g.PersistedID != "" {
		w.String("PGID", g.PersistedID) // G3: what the client saves for a reconnect (reconnect.go)
	}
	w.Struct("PHST", host)
	w.Int("PRES", PresenceModeStandard)
	w.String("PSAS", "local")
	w.Int("SEED", g.Seed)
	w.Int("TCAP", g.TeamCap)
	w.Struct("THST", host)
	w.List("TIDS", tdf.TypeInt, g.TeamIDs)
	w.String("UUID", fmt.Sprintf("00000000-0000-0000-0000-%012x", g.ID))
	w.String("VSTR", g.Version)
}

// gameAttrs is the game's attribute map as the GMS would have left it: the request's attributes plus
// ServerBuildVersion (unless the request has it), appended so the names stay in the request's sorted order.
func gameAttrs(g Game) []tdf.Pair {
	for _, a := range g.Attrs {
		if a.Key == ServerBuildVersionAttr {
			return g.Attrs
		}
	}
	return append(append([]tdf.Pair{}, g.Attrs...), tdf.KV(ServerBuildVersionAttr, g.Version))
}

// replicatedGamePlayer is Blaze::GameManager::ReplicatedGamePlayer (0x01461EA0): ACTIVE_CONNECTING (it
// connects to the GMS next), public slot SID (0 for a practice game's creator; B10a: the position in a
// matchmade game's PROS), its own addresses as PNET (the NetworkAddress union, here a top-level union
// member with a VALU header).
func replicatedGamePlayer(g Game, p Player, slot int64) *tdf.Writer {
	w := tdf.NewWriter()
	writeReplicatedGamePlayer(w, g, p, slot)
	return w
}

// writeReplicatedGamePlayer writes replicatedGamePlayer's members into w (G3: NotifyPlayerJoining's PDAT).
func writeReplicatedGamePlayer(w *tdf.Writer, g Game, p Player, slot int64) {
	w.Int("GID", g.ID)
	w.Int("LOC", LocaleEnUS)
	w.String("NAME", p.Name)
	w.Int("PID", p.ID)
	w.Union("PNET", NetworkAddressIpPair, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { ipPair(w, p.Ext, p.Int) })
	})
	w.Int("SID", slot)
	w.Int("SLOT", SlotPublic)
	w.Int("STAT", PlayerStateActiveConnecting)
	w.Int("TIDX", p.Team)
	w.Int("TIME", p.JoinTime)
	w.Int("UID", p.ID)
}

func registerGameManager(s *Server) {
	registerMatchmaking(s) // B10a: startMatchmaking, cancelMatchmaking (matchmaking.go)
	registerCustomGames(s) // B15: the custom lobby and the game browser (customgames.go, gamebrowser.go)
	// resetDedicatedServer (Blaze::GameManager::CreateGameRequest, table 0x01461AF0) is what PRACTICE
	// DAWNGATE sends (b8_run4): cGameEntryManager::Game_Create (0x00691DC0, type 3 PracticeSolo, flags
	// 0x50C) -> cBlazeGameManager::CreateGame ("kCreateGameFlags_Solo") -> BlazeSDK's dedicated-server
	// create. The request asks for NTOP CLIENT_SERVER_DEDICATED, 2 teams of 5 and carries the client's
	// own addresses in HNET. We make the game at once and tell the client where its GMS is.
	s.Register(ComponentGameManager, CmdGameManagerResetDedicatedServer, "resetDedicatedServer", func(r *Request) []byte {
		if r.Conn.Session == nil {
			r.Drop("resetDedicatedServer before login")
			return nil
		}
		gms, ok := ParseAddr(r.Server.Host, r.Server.GMSPort)
		if !ok {
			r.Drop("no IPv4 GMS address")
			return nil
		}
		g := gameFromRequest(r.Body, r.Server.gameIDs.Add(1), gms)
		p := Player{ID: r.Conn.Session.Persona.ID, Name: r.Conn.Session.Persona.Name, JoinTime: r.Server.Now().UnixMicro()}
		p.Team, _ = r.Body.Int("TIDX")
		ext, in := requestHostAddress(r.Body)
		p.Ext, p.Int = r.Server.ClientAddr(ext), r.Server.ClientAddr(in)
		if IsLobbyGame(int64(g.descriptors())) { // B15: CREATE GAME's custom game (or a practice group): a lobby first
			return r.Server.startCustomGame(r, g, p)
		}
		if r.Server.Hosts != nil { // C1: a match host per game; NotifyGameSetup once it is ready (gamehost.go)
			return r.Server.startHostedGame(r, g, p)
		}
		r.Server.Log.Logf("blaze", "   game %d %q: GMS %s (HNET), player %d", g.ID, g.Name, g.GMS, p.ID)
		r.Notify(ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", GameSetupNotification(r.Conn.ForClient(g), p))
		return JoinGameReply(g.ID)
	})
	// removePlayer (Blaze::GameManager::RemovePlayerRequest 0x01461550: GID, PID, REAS, CNTX, SCTX, BTPL)
	// follows a failed GMS connection (b8_run5: GID 1, PID = BUID, REAS 6 = PLAYER_LEFT from the
	// PlayerRemovedReason name table 0x012E7668). Its sender 0x00C7B872 uses the generic job 0x011F7C00
	// without a response class, so the reply is empty. Without a match host we keep no game state, so
	// there is nothing to remove; with one (C1) the host hears of it (removePlayer, then abortGame
	// once nobody is left; gamehost.go).
	s.Register(ComponentGameManager, CmdGameManagerRemovePlayer, "removePlayer", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		pid, _ := r.Body.Int("PID")
		reason, _ := r.Body.Int("REAS")
		// B15: an admin removing another player from its custom game (the lobby's kick: Game_KickPlayer 0x00684540 ->
		// 0x00C7EFF0, REAS PLAYER_KICKED 8 or banned 9)
		if r.Conn.Session != nil && pid != r.Conn.Session.Persona.ID {
			r.Server.kickFromCustomGame(r, gid, pid, reason)
			return nil
		}
		r.Server.leaveHostedGame(r, gid, pid, reason)
		return nil
	})
	// updateMeshConnection (Blaze::GameManager::UpdateMeshConnectionRequest 0x012E7828: GID, TARG list of
	// PlayerConnectionStatus 0x01461970 {FLGS, PID, STAT}) is how BlazeSDK reports the client's connection
	// to the game's other endpoints. Session b9_run1: right after the RakNet connection to our GMS
	// ("cClientSession::OnConnected", "cClientNetworkAdapter::connectToEndpoint") the client sends GID 1,
	// TARG [{FLGS 0, PID 3000000001 (DedicatedServerID, the topology host), STAT 2 CONNECTED}]. Its sender
	// 0x00C7BA12 uses the generic job 0x011F7C00 without a response class, so the reply is empty.
	s.Register(ComponentGameManager, CmdGameManagerUpdateMeshConnection, "updateMeshConnection", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		if f, ok := r.Body.Get("TARG"); ok {
			if l, ok := f.Value.(*tdf.List); ok {
				for _, it := range l.Items {
					if t, ok := it.(*tdf.Struct); ok {
						pid, _ := t.Int("PID")
						stat, _ := t.Int("STAT")
						r.Server.Log.Logf("blaze", "   game %d: mesh connection to %d %s", gid, pid, meshStatusNames[stat])
						// B15: in a custom game the player's join completes with its connection to the GMS
						if pid == DedicatedServerID && stat == meshConnected && r.Conn.Session != nil {
							me := r.Conn.Session.Persona.ID
							r.After(func() { r.Server.customJoinCompleted(gid, me) })
						}
					}
				}
			}
		}
		return nil
	})
}

// gameFromRequest takes the game's settings from a CreateGameRequest.
func gameFromRequest(req *tdf.Struct, id int64, gms Addr) Game {
	g := Game{ID: id, GMS: gms, Seed: id * 7919}
	g.Name, _ = req.Str("GNAM")
	g.Settings, _ = req.Int("GSET")
	g.TeamCap, _ = req.Int("TCAP")
	g.Version, _ = req.Str("VSTR")
	g.Capacity = intList(req, "PCAP")
	g.TeamIDs = intList(req, "TIDS")
	g.Attrs = stringMap(req, "ATTR")
	return g
}

func intList(s *tdf.Struct, label string) []int64 {
	out := []int64{}
	if f, ok := s.Get(label); ok {
		if l, ok := f.Value.(*tdf.List); ok {
			for _, it := range l.Items {
				if v, ok := it.(int64); ok {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// requestHostAddress reads the first IpPairAddress of a CreateGameRequest's HNET (the client's own
// external and internal address).
func requestHostAddress(req *tdf.Struct) (ext, in Addr) {
	f, ok := req.Get("HNET")
	if !ok {
		return
	}
	l, ok := f.Value.(*tdf.List)
	if !ok || len(l.Items) == 0 {
		return
	}
	u, ok := l.Items[0].(*tdf.ListUnion)
	if !ok || u.Member != NetworkAddressIpPair || u.Value == nil {
		return
	}
	get := func(label string) Addr {
		s, ok := u.Value.Sub(label)
		if !ok {
			return Addr{}
		}
		ip, _ := s.Int("IP")
		port, _ := s.Int("PORT")
		return Addr{IP: uint32(ip), Port: port}
	}
	return get("EXIP"), get("INIP")
}
