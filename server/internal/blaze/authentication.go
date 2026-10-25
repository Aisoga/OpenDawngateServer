package blaze

import (
	"net/netip"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Authentication component (1) commands, from the client's name switch GetAuthenticationCommandName
// (0x00CC1530; docs/data/blaze_components.txt).
const (
	ComponentAuthentication = 1
	CmdAuthLogin            = 0x28
	CmdAuthLogout           = 0x46
	CmdAuthLoginPersona     = 0x6E
)

// PlatformPC is the ClientPlatformType the client reports for itself (preAuth CINF CPFT 4); we give it
// to every persona (PersonaDetails PLAT, UserSessionLoginInfo PLAT).
const PlatformPC = 4

// LocaleEnUS is 'enUS' as the client packs locales (preAuth CDAT LANG, CINF LOC).
const LocaleEnUS = 0x656E5553

func registerAuthentication(s *Server) {
	s.Register(ComponentAuthentication, CmdAuthLogin, "login", handleLogin)
	s.Register(ComponentAuthentication, CmdAuthLoginPersona, "loginPersona", handleLoginPersona)
	s.Register(ComponentAuthentication, CmdAuthLogout, "logout", handleLogout)
}

// handleLogout answers Authentication::logout (0x46, roadmap B12) with an empty reply and ends the user's
// session on the connection, which stays open: the session-end hooks run after the reply (the hosted game's
// removePlayer BLAZESERVER_CONN_LOST and its end per C1b's rules, the playgroups, a matchmaking session, the
// Messaging relay's online entry; Conn.OnSessionEnd), and a new login on the same connection starts afresh.
//
// Client evidence (docs/BLAZE.md *Logout and the AFK kick (B12)*): the request has no payload (sender
// 0x00CD0410 passes no request object; c1b_run11: 0 bytes). Its RPC job (constructor 0x00C7B180, vtable
// 0x012E54D0, also used by other senders) has no response class: the reply slot (0x00C7CF30) only hands the
// error code and job id to the callback, the login manager's (0x00CCF410 -> its vtable +0xA0, 0x00CCF7D0),
// which doesn't look at the error either: it resets the login state machine to state 0, calls the caller's
// callback (the platform's, 0x00AD3640: empties the user manager's cache and calls the connection manager's
// +0x18, presumably the disconnect) and moves the user manager to state 9. So any reply ends the client's
// logout at once; without one it waited for defaultRequestTimeout (80 s, preAuth CONF), then closed its Blaze
// connection, went to kGameStateLogin and reconnected (c1b_run11).
func handleLogout(r *Request) []byte {
	sess := r.Conn.Session
	if sess == nil {
		r.Server.Log.Log("blaze", "   (logout without a login on this connection)")
		return nil
	}
	r.Conn.Session = nil
	r.Server.Log.Logf("blaze", "   account %d (persona %d) logged out; its session ends", sess.Account.UserID, sess.Persona.ID)
	conn := r.Conn
	r.After(func() { conn.endSession(false) })
	return nil
}

// handleLogin answers Authentication::login (LoginRequest MAIL/PASS/TOKN/TYPE, member table 0x01462A70). The
// the password is checked (Server.authenticate, internal/accounts): MAIL is the login name, PASS the password as typed
// (the client sends both as typed, unchecked; an empty PASS when LOGIN is pressed again after a refusal). A
// refusal is the error reply of its code, which the client shows in its LOGIN ERROR panel on the same connection
// (SnLobbyLogin_OnLoginFailure 0x00AC9750); the connection
// stays open for the next try. A login that passes gets the account (made by it under open sign-up, with the
// new-account content once: B7a, Server.grant).
func handleLogin(r *Request) []byte {
	now := r.Server.Now()
	mail, _ := r.Body.Str("MAIL")
	pass, _ := r.Body.Str("PASS")
	if codes := r.Server.TestLoginErrors; len(codes) > 0 { // test aid (--test-login-errors)
		if n := r.Server.testLoginTries.Add(1) - 1; n < int64(len(codes)) && codes[n] != 0 {
			r.Server.Log.Logf("blaze", "   (test --test-login-errors: login %d refused with error %#04x)", n+1, codes[n])
			r.Fail(codes[n])
			return nil
		}
	}
	res := r.Server.authenticate(mail, pass, r.Conn.Addr(), now)
	if res.Code != 0 {
		r.Server.Log.Logf("blaze", "   login refused (error %#04x): %s", res.Code, res.Note)
		r.Fail(res.Code)
		return nil
	}
	r.Server.Log.Logf("blaze", "   %s", res.Note)
	acct := r.Server.grant(res.Account)
	acct = r.Server.withhold(acct)    // WIPSTONES: an older account loses the withheld items
	acct = r.Server.ensurePages(acct) // B7c: a perk page per loadout page owned
	sess := &Session{Account: acct, Key: r.Server.NewKey(), First: res.First}
	r.Conn.Session = sess
	r.Server.sessionStarted(r.Conn, acct.UserID) // a status change can end it (kick.go)
	return LoginReply(acct, sess.Key, uint32(now.Unix()))
}

// authenticate is the login check of one Authentication::login: the login name and password from the client's
// address at now, under the server's sign-up policy (accounts.Policy.Login: the hash, the rules, the account's status,
// the sign-up mode, the per-address cap). It changes nothing on the connection, so a limiter (attempts per
// address and per account) can sit around it.
func (s *Server) authenticate(name, password string, from netip.Addr, now time.Time) accounts.Result {
	return s.Accounts.Login(s.Store, name, password, from, now)
}

// LoginReply is Authentication::login -> Blaze::Authentication::LoginResponse (member table
// 0x012F9B60; response class confirmed from the RPC job of AuthenticationSendLogin 0x00CD16B0 in B4).
// The login state's response handler (0x00CD4260, vtable 0x012FEE40 slot 51) reads UNDR (+0x5A),
// ANON (+0x5B), SPAM (+0x58), PLST (+0x1C, copied by 0x00CD3F70), UID (+0x08) and PCTK (+0x10, kept
// as a token if not empty). NTOS (+0x59) = 0 sends it straight to the persona state (state 8, vtable
// 0x012FF180); NTOS = 1 would go to the terms-of-service state 5 first. SKEY isn't read there.
// PLST is the LoginResponse::mPersonaDetailsList (string at 0x012FC0E0), one PersonaDetails each.
func LoginReply(acct store.Account, key string, last uint32) []byte {
	personas := make([]*tdf.Writer, len(acct.Personas))
	for i, p := range acct.Personas {
		personas[i] = personaDetails(p, last)
	}
	w := tdf.NewWriter()
	w.Int("ANON", 0)
	w.Int("NTOS", 0)
	w.String("PCTK", "")
	w.List("PLST", tdf.TypeStruct, personas)
	w.String("SKEY", key)
	w.Int("SPAM", 0)
	w.Int("UID", acct.UserID)
	w.Int("UNDR", 0)
	return w.Bytes()
}

// personaDetails is a Blaze::Authentication::PersonaDetails (member table 0x01462E20): DSNM display
// name, LAST last authentication (uint32 seconds), PID persona id, PLAT platform enum.
func personaDetails(p store.Persona, last uint32) *tdf.Writer {
	return tdf.NewWriter().String("DSNM", p.Name).Int("LAST", int64(last)).Int("PID", p.ID).Int("PLAT", PlatformPC)
}

// handleLoginPersona answers Authentication::loginPersona (LoginPersonaRequest PNAM, member table
// 0x012F9DD8). Without a login on this connection, or for a persona the account doesn't have, it
// leaves the request unanswered (logged), so the case stays visible. Since B14 the system messages (kill switches,
// broadcasts; sysmsg.go) follow the reply's two notifications.
func handleLoginPersona(r *Request) []byte {
	sess := r.Conn.Session
	if sess == nil {
		r.Drop("loginPersona before login")
		return nil
	}
	acct := sess.Account
	name, _ := r.Body.Str("PNAM")
	p, ok := acct.Persona(name)
	if !ok {
		r.Server.Log.Logf("blaze", "   (loginPersona: no persona %q; using the first)", name)
		p = acct.Personas[0]
	}
	sess.Persona = p
	r.Server.messagingOnline(r.Conn, p) // B10c: messages to p reach this connection
	r.Server.friendsLogin(r, p)         // B13b: p's friends hear it is online (and offline at the session's end)
	// the persona of the session that a status change ends (UserUnauthenticated's BID)
	r.Server.sessionPersona(r.Conn, acct.UserID, p.ID)
	now := uint32(r.Server.Now().Unix())
	r.Notify(ComponentUserSessions, NotifyUserAuthenticated, "UserAuthenticated",
		UserAuthenticatedNotification(acct, p, sess.Key, sess.First, now))
	r.Notify(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate",
		ExtendedDataUpdateNotification(p.ID, acct.UserInfo, progress.Wins(acct.Stats[progress.PlayerStats])))
	conn := r.Conn
	r.After(func() { r.Server.sendSystemMessages(conn, p) }) // B14: kill switches and broadcasts, after those
	return SessionInfoReply(acct, p, sess.Key, sess.First, now)
}

// SessionInfoReply is Authentication::loginPersona -> Blaze::Authentication::SessionInfo (member
// table 0x012F9938; response class from the RPC job, B4): BUID Blaze user id (= persona id), FRST
// first login, KEY session key, LLOG last login, MAIL, PDTL PersonaDetails, UID user id. The persona
// state's response handler (0x00CD4840, vtable 0x012FF180 slot 51) only keeps PDTL's PID (+0x48).
func SessionInfoReply(acct store.Account, p store.Persona, key string, first bool, now uint32) []byte {
	w := tdf.NewWriter()
	w.Int("BUID", p.ID)
	w.Int("FRST", b2i(first))
	w.String("KEY", key)
	w.Int("LLOG", int64(now))
	w.String("MAIL", acct.Email)
	w.Struct("PDTL", func(w *tdf.Writer) {
		w.String("DSNM", p.Name).Int("LAST", int64(now)).Int("PID", p.ID).Int("PLAT", PlatformPC)
	})
	w.Int("UID", acct.UserID)
	return w.Bytes()
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
