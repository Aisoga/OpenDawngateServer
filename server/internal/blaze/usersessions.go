package blaze

import (
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// UserSessions component (0x7802) notification ids, from GetUserSessionsNotificationName (0x00C79E50).
// The component's notification dispatcher (0x00C7A8B0) builds one TDF per id: 1
// UserSessionExtendedDataUpdate, 2 NotifyUserAdded, 3 NotifyUserRemoved, 4 UserSessionDisconnectReason,
// 5 UserStatus, 8 UserSessionLoginInfo, 9 UserSessionLogoutInfo.
const (
	ComponentUserSessions               = 0x7802
	NotifyUserSessionExtendedDataUpdate = 0x01
	NotifyUserAdded                     = 0x02
	NotifyUserUpdated                   = 0x05
	NotifyUserAuthenticated             = 0x08
)

// UserStatus FLGS (UserUpdated) bits, as the client's reader 0x00C8A130 takes them: bit 0 = the status is known
// (else 0 "unknown"), bit 1 = online (1 offline, 2 online). BlazeSDK calls them SUBSCRIBED 0x1 and ONLINE 0x2
// [assumed names]. The client keeps them at the user's +0x44 (UserUpdated's handler 0x00C8C460); an online status
// (2) is what its "is this user online" check (0x00AAA380) wants, and an offline one (1) keeps
// GameEntryInviteManager (0x0067EC70) from asking the user which invites it takes.
const (
	UserStatusKnown  = 0x1
	UserStatusOnline = 0x2
)

// PresenceAttribute is the user attribute that carries a user's presence (B13b): the client sets it with
// updateExtendedDataAttribute (ATID 21840 = 0x5550; the presence manager 0x00AA9DC0, from the game state's
// 0x004A79B0 and the chat's Global.SetUserPresence 0x004AC330) and reads another user's from that user's
// extended data, first CMAP[0x5550], else DMAP (component 0, key 0x5550) (0x00AA9A90). The value is
// game presence << 16 | user presence (0x00AA96E0; live: 0 at login, 0x10001 on entering pre-game, 0x20001 in
// the game, B5). The chat (0x0049FD70) shows a user presence of 0 or -1 as offline, 1 with a game presence of 0
// or -1 as available, anything else as busy (in a game).
const PresenceAttribute = 0x5550

// UserSessions commands (GetUserSessionsCommandName).
const (
	CmdUserSessionsUpdateExtendedDataAttribute = 0x05
	CmdUserSessionsLookupUser                  = 0x0C
	CmdUserSessionsUpdateNetworkInfo           = 0x14
	CmdUserSessionsSetUserInfoAttribute        = 0x1A
	// ExtendedDataWins is the UserSessionExtendedData.DMAP key of the main menu's wins (R11).
	ExtendedDataWins = 0x00070034
	// ErrUserNotFound is USER_ERR_USER_NOT_FOUND (0x00017802; GetUserSessionsErrorName 0x00C79CC0).
	ErrUserNotFound = 0x0001
	// UserDataFlagOnline is UserData.FLGS's "online" bit [assumed: BlazeSDK's UserDataFlags subscribed 0x1,
	// online 0x2; the client only copies FLGS into the user (+0x44 of the user object, 0x00C8CAA0)].
	UserDataFlagOnline = 0x2
)

func registerUserSessions(s *Server) {
	// updateNetworkInfo (Blaze::NetworkInfo 0x012F1C08: ADDR NetworkAddress union, NLMP ping-site
	// latencies map<string,int>, NQOS) is sent twice right after postAuth (b5_run2). Its RPC job
	// (0x011F7C00 at 0x00CCA872) has no response class, so the reply is empty. Since B10a the
	// connection keeps it: choosing a game server region on the play screen (Client_SetGameServerRegion
	// 0x00670B70) pushes that region's made-up latencies as NLMP, and the matchmaker takes the region
	// from them (matchmaking.go); ADDR is a party member's address in a matchmade game.
	s.Register(ComponentUserSessions, CmdUserSessionsUpdateNetworkInfo, "updateNetworkInfo",
		func(r *Request) []byte {
			n := &r.Conn.Net
			if f, ok := r.Body.Get("NLMP"); ok {
				if m, ok := f.Value.(*tdf.Map); ok {
					n.PingSites = map[string]int64{}
					for _, e := range m.Entries {
						k, kok := e.Key.(string)
						v, vok := e.Value.(int64)
						if kok && vok {
							n.PingSites[k] = v
						}
					}
				}
			}
			if ext, in := unionAddress(r.Body, "ADDR"); ext != (Addr{}) || in != (Addr{}) {
				n.Ext, n.Int = ext, in
			}
			return nil
		})
	// updateExtendedDataAttribute (UpdateExtendedDataAttributeRequest ATID 21840, OPER 0, UREA 0, VALU 0
	// after enterService in b5_run3): no response class either (job 0x011F7C00 at 0x00C8A5E2). ATID 0x5550 is
	// the user's presence (PresenceAttribute); since B13b the server keeps it while the user is online and
	// passes it on to the user's online friends (associationlists.go). OPER 1 removes it [assumed: BlazeSDK's
	// remove flag; the client sends 0].
	s.Register(ComponentUserSessions, CmdUserSessionsUpdateExtendedDataAttribute, "updateExtendedDataAttribute",
		func(r *Request) []byte {
			atid, _ := r.Body.Int("ATID")
			oper, _ := r.Body.Int("OPER")
			valu, _ := r.Body.Int("VALU")
			sess := r.Conn.Session
			if atid == PresenceAttribute && sess != nil && sess.Persona.ID != 0 {
				if oper != 0 {
					valu = 0
				}
				r.Server.presenceChanged(r, valu)
			}
			return nil
		})
	// setUserInfoAttribute (Blaze::SetUserInfoAttributeRequest, table 0x012F20E0: ATTV uint64, MASK
	// uint64, ULST list<objid>) is sent when the player dismisses the main menu's NewFeatureNotify
	// overlay (b8_run3: ATTV 1<<62, MASK 0, ULST [(0x7802, 1, BUID)]). Sender 0x00AD35BF uses the generic
	// job 0x011F7C00 without a response class, so the reply is empty.
	//
	// Since B7a the attribute is stored with the account (UserInfoAttribute) and goes out as
	// UserSessionExtendedData.UATT at the next login. B6 found the bit (UserInfoNewsSeen: the overlay
	// shows only while it is clear) and answers with UserSessionExtendedDataUpdate (userInfoChanged), so
	// the overlay stays away for the rest of the session too; the live check is B6-live.
	s.Register(ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute, "setUserInfoAttribute",
		func(r *Request) []byte {
			attv, _ := r.Body.Int("ATTV")
			mask, _ := r.Body.Int("MASK")
			r.update("setUserInfoAttribute", func(a *store.Account) {
				a.UserInfo = UserInfoAttribute(a.UserInfo, uint64(attv), uint64(mask))
			})
			userInfoChanged(r)
			return nil
		})
	// lookupUser (A5b; request Blaze::UserIdentification 0x012F1C70, reply Blaze::UserData 0x012F20C0, RPC job
	// 0x012E5D84 at 0x00C8A359). The PvP lobby's ADD PLAYERS (GroupLobbyUI_OnAddPlayersButtonClicked 0x00461E00)
	// opens MatchInviteUI, whose manual invite field looks the typed persona name up 750 ms after the last key
	// (0x004E8A20 -> UserManager_LookupUserByName 0x00C8CDB0, unless the user is cached): the request carries
	// NAME. The reply's user (0x00C8CB20 -> 0x00C8CAA0: USER's ID, NAME, EXID, AID, ALOC, ORIG, FLGS; EDAT as the
	// user's extended data) goes to 0x004E7D40, which takes it as the invite target unless it is the local user
	// or already in the party; INVITE then sends B10c's PvP invite to that BlazeId. An error reply (we send
	// USER_ERR_USER_NOT_FOUND for an unknown name or id) leaves the field "not found". By ID when ID is set.
	s.Register(ComponentUserSessions, CmdUserSessionsLookupUser, "lookupUser", func(r *Request) []byte {
		id, _ := r.Body.Int("ID")
		name, _ := r.Body.Str("NAME")
		var acct store.Account
		var p store.Persona
		var ok bool
		if id != 0 {
			acct, p, ok = r.Server.Store.PersonaByID(id)
		} else {
			acct, p, ok = r.Server.Store.PersonaByName(name)
		}
		if !ok {
			r.Server.Log.Logf("blaze", "   lookupUser id %d name %q: not found", id, name)
			r.Fail(ErrUserNotFound)
			return nil
		}
		online := r.Server.personaOnline(p.ID)
		r.Server.Log.Logf("blaze", "   lookupUser id %d name %q: persona %d %q (%s)", id, name, p.ID, p.Name,
			map[bool]string{true: "online", false: "offline"}[online])
		return UserDataReply(acct, p, online)
	})
}

// UserDataReply is lookupUser's reply, Blaze::UserData (table 0x012F20C0): EDAT = the user's extended data
// (UserSessionExtendedData 0x012F1F70, as ExtendedDataUpdateNotification's DATA with the stored user info
// attribute and wins), FLGS = UserDataFlagOnline while the persona has a connection, USER =
// UserIdentification (0x012F1C70) {AID the account id, ALOC, ID the persona id = BlazeId, NAME}; EXBB, EXID and
// ORIG keep their defaults, as in UserAuthenticated (a PC account has no first-party id).
func UserDataReply(acct store.Account, p store.Persona, online bool) []byte {
	w := tdf.NewWriter()
	w.Struct("EDAT", extendedData(acct.UserInfo, progress.Wins(acct.Stats[progress.PlayerStats]), nil))
	w.Int("FLGS", b2i(online)*UserDataFlagOnline)
	w.Struct("USER", userIdentification(acct, p))
	return w.Bytes()
}

// userIdentification writes Blaze::UserIdentification (0x012F1C70) for persona p: AID the account id, ALOC, ID the
// persona id = BlazeId, NAME; EXBB, EXID and ORIG keep their defaults (a PC account has no first-party id).
func userIdentification(acct store.Account, p store.Persona) func(w *tdf.Writer) {
	return func(w *tdf.Writer) {
		w.Int("AID", acct.UserID)
		w.Int("ALOC", LocaleEnUS)
		w.Int("ID", p.ID)
		w.String("NAME", p.Name)
	}
}

// extendedData writes Blaze::UserSessionExtendedData (0x012F1F70) as the server keeps it for a user: BPS (best ping
// site, our only one), CMAP (the client attributes: since B13b only the presence, written when presence isn't nil),
// CTY, DMAP (the wins the main menu shows, when there are any), QDAT and UATT (the user info attribute); see
// ExtendedDataUpdateNotification for the members left out.
func extendedData(userInfo uint64, wins int64, presence *int64) func(w *tdf.Writer) {
	return func(w *tdf.Writer) {
		w.String("BPS", "local")
		if presence != nil {
			w.Map("CMAP", tdf.TypeInt, tdf.TypeInt, tdf.KV(int64(PresenceAttribute), *presence))
		}
		w.String("CTY", "")
		if wins > 0 {
			w.Map("DMAP", tdf.TypeInt, tdf.TypeInt, tdf.KV(int64(ExtendedDataWins), wins))
		}
		w.Struct("QDAT", func(w *tdf.Writer) { w.Int("DBPS", 0).Int("NATT", 0).Int("UBPS", 0) })
		w.Uint("UATT", userInfo)
	}
}

// UserAddedNotification is UserSessions::UserAdded -> Blaze::NotifyUserAdded (member table 0x012F2020: DATA
// UserSessionExtendedData, USER UserIdentification; the dispatcher's case 2 0x00CB3730). Its handler
// (HandleUserAddedNotification, registered by InitializeBlazeUserManager 0x00C8D020) creates the user in the
// client's UserManager (0x00C8C5E0), copies DATA into it and tells the user listeners (slot 1: the presence
// manager's 0x00AA9C40 reads the presence from CMAP). B13b sends it for every member of the user's association
// lists before the list reply: the client waits until every listed member (with a BlazeId and a name) is a known
// user before it calls the lists' callbacks (0x00C99770 after getLists, 0x00C994C0 after an add, polling through a
// job), and the invite dialog (MatchInviteUI_FillFriendsList 0x004E8F90) and the friend events (0x00ABB300) read
// the member's user without a null check. B14 refined the reading: the handler acquires the user (0x00C8C5E0: found by
// BlazeId, else created; a reference held until a UserRemoved) and copies DATA only while bit 0 of the user's +0x2A is
// clear, i.e. no UserAdded copied it before (a user made from a playgroup's member or a game roster has it clear, so
// UserAdded fills it); with the bit set it only releases the reference it just took (0x00C8BFD0, the user release).
// UserSessionExtendedDataUpdate replaces a known user's data at any time.
func UserAddedNotification(acct store.Account, p store.Persona, presence int64) []byte {
	w := tdf.NewWriter()
	w.Struct("DATA", extendedData(acct.UserInfo, progress.Wins(acct.Stats[progress.PlayerStats]), &presence))
	w.Struct("USER", userIdentification(acct, p))
	return w.Bytes()
}

// UserUpdatedNotification is UserSessions::UserUpdated -> Blaze::UserStatus (member table 0x012F1CD8: FLGS bitfield,
// ID; the dispatcher's case 5 0x00CB1F20). Its handler (0x00C8C460, the UserSessions slot at +0x6C) finds the user by
// ID, stores FLGS (UserStatusKnown | UserStatusOnline while online, UserStatusKnown alone when offline) and tells
// the user listeners (slot 3).
func UserUpdatedNotification(id int64, online bool) []byte {
	return tdf.NewWriter().Int("FLGS", UserStatusKnown|b2i(online)*UserStatusOnline).Int("ID", id).Bytes()
}

// PresenceNotification is another user's UserSessionExtendedDataUpdate (B13b): the user's extended data with its
// presence in CMAP, so the presence manager's listener (0x00AA9C40) sees the new value and the chat shows it
// (UpdateRemoteUserPresence, 0x0049DC20). The handler (0x00C8C400) ignores a user the client doesn't know.
func PresenceNotification(acct store.Account, p store.Persona, presence int64) []byte {
	w := tdf.NewWriter()
	w.Struct("DATA", extendedData(acct.UserInfo, progress.Wins(acct.Stats[progress.PlayerStats]), &presence))
	w.Int("SUBS", 1)
	w.Int("USID", p.ID)
	return w.Bytes()
}

// personaOnline reports whether persona id has a logged-in connection (loginPersona registers it with the
// messaging relay, B10c).
func (s *Server) personaOnline(id int64) bool {
	m := &s.msgs
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.online[id]) > 0
}

// UserInfoAttribute applies setUserInfoAttribute's ATTV and MASK to the stored value: with MASK 0 (as
// the client sends it) ATTV replaces the value, otherwise only the MASK bits change. This is our
// reading of BlazeSDK's request (the server side isn't in the client); unverified.
func UserInfoAttribute(old, attv, mask uint64) uint64 {
	if mask == 0 {
		return attv
	}
	return old&^mask | attv&mask
}

// UserAuthenticatedNotification is UserSessions::UserAuthenticated -> Blaze::UserSessionLoginInfo
// (member table 0x01462488, class from the dispatcher's case 8, constructor 0x00CB20D0). The client's
// UserManager (constructor 0x00C8D020) handles it in 0x00C8C9A0: it makes the user from BUID (+0x18),
// UID (+0x20), ALOC (+0x40), DSNM (+0x50) and XREF (+0x60), creates the local user object and stores it
// as local user 0. XREF (external id) stays 0: a PC account has no first-party id.
func UserAuthenticatedNotification(acct store.Account, p store.Persona, key string, first bool, now uint32) []byte {
	w := tdf.NewWriter()
	w.Int("ALOC", LocaleEnUS)
	w.Int("BUID", p.ID)
	w.String("DSNM", p.Name)
	w.Int("FRST", b2i(first))
	w.String("KEY", key)
	w.Int("LAST", int64(now))
	w.Int("LLOG", int64(now))
	w.String("MAIL", acct.Email)
	w.Int("PID", p.ID)
	w.Int("PLAT", PlatformPC)
	w.Int("UID", acct.UserID)
	w.Int("XREF", 0)
	return w.Bytes()
}

// ExtendedDataUpdateNotification is UserSessions::UserSessionExtendedDataUpdate ->
// Blaze::UserSessionExtendedDataUpdate (member table 0x012F1FF8: DATA struct +0x118, SUBS +0x128, USID
// +0x120; the DATA entry's tag word has its low byte set, which tdf_tables.py skipped before B5). The
// UserManager's handler 0x00C8C400 finds the user by USID, stores SUBS and copies DATA
// (UserSessionExtendedData, table 0x012F1F70) into the user (+0x168, 0x00C8A0C0).
//
// The local user needs that copy: the postAuth reply handler 0x00C8A150 only moves the connection to
// "authenticated" (state 11) and tells the hub (vtable +0x58) if the user has extended data, so this
// notification must arrive before the postAuth reply. We send it right after UserAuthenticated.
//
// DATA carries only members whose wire types are certain: BPS (best ping site, our only one, "local"),
// CTY (country), QDAT (Blaze::Util::NetworkQosData, table 0x01462610) and UATT, the user info attribute
// that setUserInfoAttribute stored (B7a; 0 for a new account). ADDR (type code 0x09), CVAR (0x06), HWFG
// (0x07), CMAP, PSLM and ULST keep their defaults.
//
// DMAP (map<int,int>, B7c) carries the wins the main menu shows: MainMenuUI_OnUserExtendedDataChanged
// (0x004DE0D0) reads the local user's DMAP[0x00070034] (component 7 Stats << 16 | key 0x34, through
// BlazeUser_GetExtendedDataValue 0x00C8B560; R11) and shows it with SetExtendedData(wins, rating), 0
// when absent. So DMAP is left out while wins is 0, and the server pushes this notification again after
// a game (gamehost.go), since its handler (0x00C8C400) replaces the user's extended data.
func ExtendedDataUpdateNotification(blazeID int64, userInfo uint64, wins int64) []byte {
	w := tdf.NewWriter()
	w.Struct("DATA", extendedData(userInfo, wins, nil))
	w.Int("SUBS", 1)
	w.Int("USID", blazeID)
	return w.Bytes()
}
