package blaze

import (
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
)

// Util component (9) commands, from the client's command-name switch.
const (
	ComponentUtil             = 9
	CmdUtilPing               = 0x02
	CmdUtilGetTelemetryServer = 0x05
	CmdUtilPreAuth            = 0x07
	CmdUtilPostAuth           = 0x08
)

func registerUtil(s *Server) {
	s.Register(ComponentUtil, CmdUtilPing, "ping", func(r *Request) []byte {
		return PingReply(uint32(r.Server.Now().Unix()))
	})
	s.Register(ComponentUtil, CmdUtilPreAuth, "preAuth", func(r *Request) []byte {
		return PreAuthReply(r.Server.Host, r.Server.QosPort, r.Server.Service)
	})
	s.Register(ComponentUtil, CmdUtilPostAuth, "postAuth", func(r *Request) []byte {
		var uid int64
		if r.Conn.Session != nil {
			uid = r.Conn.Session.Account.UserID
		}
		return PostAuthReply(r.Server.Host, r.Server.TelemetryPort, uid)
	})
	s.Register(ComponentUtil, CmdUtilGetTelemetryServer, "getTelemetryServer", func(r *Request) []byte {
		return TelemetryServerReply(r.Server.Host, r.Server.TelemetryPort)
	})
}

func pingSite(host string, port int) *tdf.Writer {
	return tdf.NewWriter().String("PSA", host).Int("PSP", int64(port)).String("SNA", "local")
}

// PreAuthReply is Util::preAuth -> Blaze::Util::PreAuthResponse. Members, types and order are from
// the client's TDF member table at 0x012FCBE8 (tags ascending, as the encoder expects). EEFA (bool,
// default true) and MINR are left at their defaults. CIDS announces the component ids we know
// (fire.Components); ASRC, ESRC, RSRC and NASP are placeholders in the style of other Blaze
// emulators. CONF is a FetchConfigResponse whose pingPeriod sets the keep-alive interval; QOSS is a
// QosConfigInfo pointing the client's QoS check at our ping site (host:qosPort).
func PreAuthReply(host string, qosPort int, service string) []byte {
	w := tdf.NewWriter()
	w.String("ASRC", "303107")
	w.List("CIDS", tdf.TypeInt, fire.ComponentIDs())
	w.Struct("CONF", func(w *tdf.Writer) { // FetchConfigResponse
		w.Map("CONF", tdf.TypeString, tdf.TypeString,
			tdf.KV("connIdleTimeout", "90s"), tdf.KV("defaultRequestTimeout", "80s"), tdf.KV("pingPeriod", "20s"))
	})
	w.String("ESRC", "303107")
	w.String("INST", service)
	w.String("NASP", "cem_ea_id")
	w.String("PILD", "")
	w.String("PLAT", "pc")
	w.Struct("QOSS", func(w *tdf.Writer) { // QosConfigInfo
		w.Struct("BWPS", func(w *tdf.Writer) { // QosPingSiteInfo used for the bandwidth test
			w.String("PSA", host).Int("PSP", int64(qosPort)).String("SNA", "local")
		})
		w.Int("LNP", 10)
		w.Map("LTPS", tdf.TypeString, tdf.TypeStruct, tdf.KV("local", pingSite(host, qosPort)))
		w.Int("SVID", 0x45410805) // the client's compiled-in default
	})
	w.String("RSRC", "303107")
	w.String("SVER", "Blaze 3.19.11.0 (Dawngate restoration stub)")
	return w.Bytes()
}

// PingReply is Util::ping -> Blaze::Util::PingResponse: one member, STIM (uint32 server time, Unix
// seconds). Member table 0x012FCAC4 (getter 0x00CC6200). The client sends ping once right after
// preAuth (SendBlazeInitialPing 0x00CCCF60; its callback 0x00CCCD60 stores STIM and moves the
// connection on, which shows the login form) and then every pingPeriod as a keep-alive (0x00CCC060).
func PingReply(stim uint32) []byte {
	return tdf.NewWriter().Int("STIM", int64(stim)).Bytes()
}

// PostAuthReply is Util::postAuth -> Blaze::Util::PostAuthResponse (member table 0x012FCCB4; response
// class from the RPC job 0x012E5DAC, sent by 0x00C8AC40). The client sends postAuth as soon as
// UserAuthenticated has made its local user (0x00C8BEF0). Its reply handler 0x00C8A150 copies the four
// structs into the local user and, if the user already has extended data, moves the connection to
// "authenticated" (state 11).
//
//	PSS   Blaze::Util::PssConfig (0x012FCA70): no PSS server (empty address, port 0).
//	TELE  Blaze::Util::GetTelemetryServerResponse (0x012FCAD0): our telemetry sink (host:telemetryPort),
//	      the same place Config.package's NewTelemetryServerIP/Port point at since A1.
//	TICK  Blaze::Util::GetTickerServerResponse (0x012FCA44): no ticker server.
//	UROP  Blaze::Util::UserOptions (0x01463318): TMOP 0 (telemetry opt-out, assumed), UID.
//
// The nested classes follow from the member offsets (each struct fills the gap to the next member)
// and BlazeSDK's naming; members with uncertain wire types (PssConfig CSIG, OIDS, RPRT) are left out.
func PostAuthReply(host string, telemetryPort int, uid int64) []byte {
	w := tdf.NewWriter()
	w.Struct("PSS", func(w *tdf.Writer) {
		w.String("ADRS", "").String("PJID", "").Int("PORT", 0).Int("TIID", 0)
	})
	w.Struct("TELE", func(w *tdf.Writer) { telemetryServer(w, host, telemetryPort) })
	w.Struct("TICK", func(w *tdf.Writer) {
		w.String("ADRS", "").Int("PORT", 0).String("SKEY", "")
	})
	w.Struct("UROP", func(w *tdf.Writer) {
		w.Int("TMOP", 0).Int("UID", uid)
	})
	return w.Bytes()
}

// TelemetryServerReply is Util::getTelemetryServer (GetTelemetryServerRequest CMAC/SNAM) ->
// Blaze::Util::GetTelemetryServerResponse (member table 0x012FCAD0; RPC job 0x012FC6A0 at 0x00CC52C2).
// The client sends it right after postAuth (session b5_run2). Same content as postAuth's TELE.
func TelemetryServerReply(host string, telemetryPort int) []byte {
	w := tdf.NewWriter()
	telemetryServer(w, host, telemetryPort)
	return w.Bytes()
}

// telemetryServer writes a GetTelemetryServerResponse's members: our telemetry sink, no session key,
// locale enUS. SDLY (send delay, ms) and SPCT (sample percent) are guesses; ANON, EDCT and MINR are 0.
// SKEY stays empty: with a key, BlazeSDK would open its own TelemetryApi connection to this address
// (0x00CC4D00, docs/TELEMETRY.md §3.5), which nothing in the game feeds; the game's telemetry goes
// to Config.package's host.
func telemetryServer(w *tdf.Writer, host string, port int) {
	w.String("ADRS", host)
	w.Int("ANON", 0)
	w.String("DISA", "")
	w.Int("EDCT", 0)
	w.String("FILT", "")
	w.Int("LOC", LocaleEnUS)
	w.Int("MINR", 0)
	w.String("NOOK", "")
	w.Int("PORT", int64(port))
	w.Int("SDLY", 15000)
	w.String("SESS", "")
	w.String("SKEY", "")
	w.Int("SPCT", 75)
	w.String("STIM", "")
	w.String("SVNM", "")
}
