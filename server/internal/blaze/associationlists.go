package blaze

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// AssociationLists component (0x19), standard BlazeSDK: the user's friends, recent players and block list.
// The client (B13, B13b, static reading; docs/BLAZE.md *Association lists (B13)* and *Friends, recent players and
// blocked users (B13b)*) keeps them in its AssociationListAPI, keyed by list type
// (AssociationListAPI_GetListByType 0x00C992E0), and uses three. It sends only addUsersToList, removeUsersFromList
// and getLists; since B13b the server keeps the members with the account (store.Lists) and tells the other side of
// a friendship with NotifyUpdateListMembership.
const (
	ComponentAssociationLists  = 0x19
	CmdALAddUsersToList        = 0x01
	CmdALRemoveUsersFromList   = 0x02
	CmdALGetLists              = 0x06
	NotifyUpdateListMembership = 0x01

	// ListTypeFriends is the friends list: the PvP lobby's invite dialog lists its members (MatchInviteUI.cpp
	// 1557 asserts "pFriendsList" without it, 0x004E8F90 -> 0x00ABB5D0), and presence updates check it.
	ListTypeFriends = 1
	// ListTypeRecentPlayers is the recent players list: every game's loading step adds the game's other Blaze
	// players to it (PreDungeonState_TransitionToLoading -> 0x00AC4850 -> 0x00ABD200 -> addUsersToList).
	ListTypeRecentPlayers = 2
	// ListTypeBlock is the block list: every incoming message and presence update checks its sender against it
	// (0x00ABB130, 0x00ABB220), which asserts "Race condition : Checking block list before its loaded!"
	// (snlobby_friends.cpp 205/1308) while the client has no list of this type.
	ListTypeBlock = 4
)

// AssociationLists errors (docs/data/blaze_components.txt: code << 16 | 0x19). The client's add callback
// (BlazeFriends_OnUpdateListResult 0x00ABC090) shows nothing for MEMBER_ALREADY_IN_THE_LIST on the friends list,
// posts 0xA00A0D25 for LIST_IS_FULL_OR_TOO_MANY_USERS and 0x188A8F7A for the others (the chat UI ignores both
// events); its remove callback (0x00ABC250) treats MEMBER_NOT_FOUND_IN_THE_LIST on the block list as success.
const (
	ErrALUserNotFound        = 0x01 // ASSOCIATIONLIST_ERR_USER_NOT_FOUND
	ErrALCannotIncludeSelf   = 0x03 // ASSOCIATIONLIST_ERR_CANNOT_INCLUDE_SELF
	ErrALInvalidUser         = 0x04 // ASSOCIATIONLIST_ERR_INVALID_USER
	ErrALMemberAlreadyInList = 0x05 // ASSOCIATIONLIST_ERR_MEMBER_ALREADY_IN_THE_LIST
	ErrALMemberNotFound      = 0x06 // ASSOCIATIONLIST_ERR_MEMBER_NOT_FOUND_IN_THE_LIST
	ErrALListNotFound        = 0x0A // ASSOCIATIONLIST_ERR_LIST_NOT_FOUND
	ErrALListFull            = 0x0B // ASSOCIATIONLIST_ERR_LIST_IS_FULL_OR_TOO_MANY_USERS
	ErrALPairedListFull      = 0x11 // ASSOCIATIONLIST_ERR_PAIRED_LIST_IS_FULL_OR_TOO_MANY_USERS
)

// ListMemberInfoUpdate LUPT values (the enum's names at .rdata 0x012F4558): the notification's handler
// (AssociationListAPI_OnNotifyUpdateListMembership 0x00C9A730 -> 0x00C9A500) adds a member for 1 unless the list has
// it, removes one for 2.
const (
	ListUpdateAdd    = 1 // LISTUPDATETYPE_ADD
	ListUpdateRemove = 2 // LISTUPDATETYPE_REMOVE
)

// The lists' sizes (ours). The client asks with LMS 0 and reads no maximum; it keeps every member of every list a
// known user (UserAdded), and its UserManager starts with a pool of 128 users (InitializeBlazeUserManager
// 0x00C8D020, hub +0x328 or 0x80), so the three lists together stay below that. When the recent players list is
// full the oldest member goes (it is in the add reply's REM).
const (
	MaxFriends       = 100
	MaxBlockedUsers  = 50
	MaxRecentPlayers = 20
)

// listObjectType is an association list's Blaze object type (ListInfo BOID): (0x19, 1), the type the client's
// lookup by object id checks (.data 0x01461154, read at 0x00C99730).
var listObjectType = tdf.ObjType{Component: ComponentAssociationLists, Type: 1}

// listNames are the names dgserver gives the lists (ListIdentification LNM). The client asks by type with an
// empty name and never looks a list up by name; it keys a second map by name, so each list gets its own.
// "friendList" is the name ME3's EA server gave list type 1 (PocketRelay's reply); the others are ours.
var listNames = map[int64]string{
	ListTypeFriends:       "friendList",
	ListTypeRecentPlayers: "recentPlayerList",
	ListTypeBlock:         "blockList",
}

// ListInfo is one association list's description, Blaze::Association::ListInfo (table 0x012F45A4): BOID the
// list's object id, FLGS a bitfield (ListStatusFlags; the bits' meanings are unknown), LID =
// ListIdentification (0x012F4588) {LNM, TYPE}, LMS the maximum size, PRID the paired list's type.
type ListInfo struct {
	ObjectID tdf.ObjID
	Flags    int64
	Name     string
	Type     int64
	MaxSize  int64
	PairID   int64
}

// RequestedLists reads a GetListsRequest's ALST (Blaze::Association::GetListsRequest 0x012F4614: ALST list of
// ListInfo, MXRC, OFRC). The client sends types 2, 4 and 1 with FLGS 1, 0 and 0x15, everything else zero
// (BlazeFriends_RequestLists 0x00ABEFD0; a5b_run6d's log).
func RequestedLists(body *tdf.Struct) []ListInfo {
	f, ok := body.Get("ALST")
	if !ok {
		return nil
	}
	l, ok := f.Value.(*tdf.List)
	if !ok {
		return nil
	}
	var out []ListInfo
	for _, item := range l.Items {
		st, ok := item.(*tdf.Struct)
		if !ok {
			continue
		}
		var li ListInfo
		if b, ok := st.Get("BOID"); ok {
			li.ObjectID, _ = b.Value.(tdf.ObjID)
		}
		li.Flags, _ = st.Int("FLGS")
		if lid, ok := st.Sub("LID"); ok {
			li.Name, _ = lid.Str("LNM")
			li.Type, _ = lid.Int("TYPE")
		}
		li.MaxSize, _ = st.Int("LMS")
		li.PairID, _ = st.Int("PRID")
		out = append(out, li)
	}
	return out
}

// DescribeList is the server's description of a requested list: the client's own ListInfo, with the two
// things only the server knows filled in where the request leaves them empty, a name (listNames, else
// "list<type>") and an object id (listObjectType, id = the list type: unique among one user's lists, which is
// all the client needs; the original's ids were server-side, ME3's friend list had 0x74b09c4). The client keys
// its lists by type, object id and name (AssociationListAPI_CreateOrUpdateList 0x00C9A010), so the three must
// differ between the lists. FLGS, LMS and PRID stay as asked [no client reader of a list's flags or maximum
// size was found; ME3's server sent FLGS 4 and LMS 200 for its friend list].
func DescribeList(req ListInfo) ListInfo {
	li := req
	if li.Name == "" {
		li.Name = listName(li.Type)
	}
	if li.ObjectID == (tdf.ObjID{}) {
		li.ObjectID = tdf.ObjID{Component: listObjectType.Component, Type: listObjectType.Type, ID: li.Type}
	}
	return li
}

// listName is the name dgserver gives list type typ.
func listName(typ int64) string {
	if n := listNames[typ]; n != "" {
		return n
	}
	return fmt.Sprintf("list%d", typ)
}

func (li ListInfo) write(w *tdf.Writer) {
	w.Value("BOID", tdf.TypeObjID, li.ObjectID).Int("FLGS", li.Flags).
		Struct("LID", func(w *tdf.Writer) { w.String("LNM", li.Name).Int("TYPE", li.Type) }).
		Int("LMS", li.MaxSize).Int("PRID", li.PairID)
}

// writeListMemberID writes Blaze::Association::ListMemberId (0x014627B8) for a member: BLID the persona id (the
// BlazeId), EXBB empty, PLAT PlatformPC (the persona's platform, as PersonaDetails says), PNAM the persona name,
// XREF 0. The client indexes a member by BLID and, when PNAM isn't empty, by name (AssociationList_AddMember
// 0x00C99C80): unfriending and unblocking by name find it there (BlazeFriends_FindListMemberByName 0x00ABB630).
func writeListMemberID(w *tdf.Writer, m store.ListMember) {
	w.Int("BLID", m.ID).Blob("EXBB", nil).Int("PLAT", PlatformPC).String("PNAM", m.Name).Int("XREF", 0)
}

// listMemberInfo is Blaze::Association::ListMemberInfo (0x012F4568): LMID the member's ListMemberId, TIME when it
// was added, in Unix seconds [our choice: no reader of TIME was found].
func listMemberInfo(m store.ListMember) *tdf.Writer {
	w := tdf.NewWriter()
	writeListMemberInfo(w, m)
	return w
}

func writeListMemberInfo(w *tdf.Writer, m store.ListMember) {
	var added int64
	if !m.Added.IsZero() {
		added = m.Added.Unix()
	}
	w.Struct("LMID", func(w *tdf.Writer) { writeListMemberID(w, m) }).Int("TIME", added)
}

func listMemberInfos(members []store.ListMember) []*tdf.Writer {
	out := make([]*tdf.Writer, 0, len(members))
	for _, m := range members {
		out = append(out, listMemberInfo(m))
	}
	return out
}

// ListContent is one list of getLists' reply: its description, the members on the requested page and the list's
// whole size.
type ListContent struct {
	Info    ListInfo
	Members []store.ListMember
	Total   int
}

// GetListsReply is AssociationLists::getLists' reply, Blaze::Association::Lists (member table 0x012F4608; RPC
// job 0x012E64A0 at 0x00C97E89): LMAP, one ListMembers (0x012F45D8) per list in the request's order, INFO =
// DescribeList's ListInfo, MEML (list of ListMemberInfo 0x012F4568) the members, OFRC = the request's offset, TOCT
// the list's size. B13 sent every list empty; since B13b the members are the account's (store.Lists).
//
// What the client does with it (AssociationListAPI_OnGetListsResult 0x00C9A770): for every LMAP element it creates
// or updates the list of INFO.LID.TYPE (0x00C9A010: a new AssociationList 0x00C99EB0 holds a copy of INFO and
// MEML's members, its member count is TOCT or else MEML's length), then (0x00C99770) waits until every member with a
// BlazeId and a name is a known user, rescheduling itself (0x00C99420), and only then calls the title's callback
// (BlazeFriends_OnGetListsResult 0x00ABBF80: the event 0xAB5BADD4, which the chat UI shows as "FriendsLoaded"
// true). So the server announces every member (UserAdded) before this reply. Without LMAP (B5's empty reply) the
// client had no lists at all: the block-list check asserted on every incoming message and the invite dialog
// asserted "pFriendsList" (A5b).
func GetListsReply(lists []ListContent, offset int64) []byte {
	items := make([]*tdf.Writer, 0, len(lists))
	for _, l := range lists {
		items = append(items, tdf.NewWriter().Struct("INFO", l.Info.write).
			List("MEML", tdf.TypeStruct, listMemberInfos(l.Members)).Int("OFRC", offset).Int("TOCT", int64(l.Total)))
	}
	return tdf.NewWriter().List("LMAP", tdf.TypeStruct, items).Bytes()
}

// UpdateListMembersReply is addUsersToList's and removeUsersFromList's reply, Blaze::Association::
// UpdateListMembersResponse (0x012F46B0): LMID the members added (ListMemberInfo), REM the members removed
// (ListMemberId). The client applies both to its list (AssociationList_ApplyUpdateResponse 0x00C9A2D0: REM out,
// LMID in, the count by the difference) and hands the add's LMID (0x00C9A360 -> 0x00C994C0, after waiting for the
// members' users) or the remove's REM (0x00C9A490) to the title's callback.
func UpdateListMembersReply(added, removed []store.ListMember) []byte {
	rem := make([]*tdf.Writer, 0, len(removed))
	for _, m := range removed {
		w := tdf.NewWriter()
		writeListMemberID(w, m)
		rem = append(rem, w)
	}
	return tdf.NewWriter().List("LMID", tdf.TypeStruct, listMemberInfos(added)).List("REM", tdf.TypeStruct, rem).Bytes()
}

// ListMembershipNotification is AssociationLists::NotifyUpdateListMembership (notification 1), whose payload is a
// Blaze::Association::UpdateListWithMembersRequest (0x012F4770; the component's dispatcher 0x00CB6C50 decodes it as
// "UpdateListWithMembersRequest::mListMemberInfoVector"): BIDL one ListMemberInfoUpdate (0x01462798: LMID the
// ListMemberInfo, LUPT ListUpdateAdd or ListUpdateRemove) per member, LID the list. B13b sends it to the other side
// of a friendship that changed (the client's accept and unfriend change their own list only; the inviter's and the
// former friend's clients change theirs only through this).
func ListMembershipNotification(typ int64, members []store.ListMember, update int64) []byte {
	items := make([]*tdf.Writer, 0, len(members))
	for _, m := range members {
		items = append(items, tdf.NewWriter().Struct("LMID", func(w *tdf.Writer) { writeListMemberInfo(w, m) }).
			Int("LUPT", update))
	}
	return tdf.NewWriter().List("BIDL", tdf.TypeStruct, items).
		Struct("LID", func(w *tdf.Writer) { w.String("LNM", listName(typ)).Int("TYPE", typ) }).Bytes()
}

// listMembers returns the account's list of type typ, nil for a type the server doesn't keep.
func listMembers(l *store.Lists, typ int64) *[]store.ListMember {
	switch typ {
	case ListTypeFriends:
		return &l.Friends
	case ListTypeRecentPlayers:
		return &l.Recent
	case ListTypeBlock:
		return &l.Blocked
	}
	return nil
}

// memberRef is one ListMemberId of a request: by BLID, or by PNAM alone (blocking and unblocking by name:
// 0x00ABD480 and 0x00ABD8F0 set only the name).
type memberRef struct {
	ID   int64
	Name string
}

func (m memberRef) String() string {
	if m.ID != 0 {
		return fmt.Sprint(m.ID)
	}
	return fmt.Sprintf("%q", m.Name)
}

// requestedMembers reads an UpdateListMembersRequest's BIDL.
func requestedMembers(body *tdf.Struct) []memberRef {
	var out []memberRef
	if f, ok := body.Get("BIDL"); ok {
		if l, ok := f.Value.(*tdf.List); ok {
			for _, item := range l.Items {
				if st, ok := item.(*tdf.Struct); ok {
					var m memberRef
					m.ID, _ = st.Int("BLID")
					m.Name, _ = st.Str("PNAM")
					out = append(out, m)
				}
			}
		}
	}
	return out
}

// requestedList reads an UpdateListMembersRequest's LID.
func requestedList(body *tdf.Struct) (name string, typ int64) {
	if lid, ok := body.Sub("LID"); ok {
		name, _ = lid.Str("LNM")
		typ, _ = lid.Int("TYPE")
	}
	return name, typ
}

// persona finds a request's member: by id, else by name.
func (s *Server) persona(m memberRef) (store.Account, store.Persona, bool) {
	if m.ID != 0 {
		return s.Store.PersonaByID(m.ID)
	}
	if m.Name == "" {
		return store.Account{}, store.Persona{}, false
	}
	return s.Store.PersonaByName(m.Name)
}

// memberID returns a request's member's persona id: BLID, else the id of the persona PNAM names (0 if none). It
// takes the store's lock, so it must not run inside a store update.
func (s *Server) memberID(m memberRef) int64 {
	if m.ID == 0 && m.Name != "" {
		if _, p, ok := s.Store.PersonaByName(m.Name); ok {
			return p.ID
		}
	}
	return m.ID
}

// findMember returns the index in members of a request's member: by id, else by the name it was added with, else by
// id, the persona id its name belongs to (memberID); -1 if it isn't there.
func findMember(members []store.ListMember, m memberRef, id int64) int {
	if m.ID == 0 && m.Name != "" {
		if i := slices.IndexFunc(members, func(x store.ListMember) bool { return strings.EqualFold(x.Name, m.Name) }); i >= 0 {
			return i
		}
	}
	if id == 0 {
		return -1
	}
	return store.IndexOf(members, id)
}

// blockedBy reports whether persona owner has persona who on its block list.
func (s *Server) blockedBy(owner, who int64) bool {
	acct, _, ok := s.Store.PersonaByID(owner)
	return ok && store.IndexOf(acct.Lists.Blocked, who) >= 0
}

// listChange collects what an add or remove did, for the reply and the log.
type listChange struct {
	added, removed []store.ListMember
	refreshed      []int64
	skipped        []string
	err            uint16 // the last refusal's error (sent when nothing changed)
}

func (c *listChange) skip(m memberRef, err uint16, why string) {
	c.skipped = append(c.skipped, fmt.Sprintf("%s (%s)", m, why))
	c.err = err
}

func (c *listChange) changed() bool {
	return len(c.added) > 0 || len(c.removed) > 0 || len(c.refreshed) > 0
}

func (c *listChange) String() string {
	var parts []string
	ids := func(ms []store.ListMember) string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = fmt.Sprintf("%d %q", m.ID, m.Name)
		}
		return strings.Join(out, ", ")
	}
	if len(c.added) > 0 {
		parts = append(parts, "added ["+ids(c.added)+"]")
	}
	if len(c.refreshed) > 0 {
		parts = append(parts, fmt.Sprintf("refreshed %v", c.refreshed))
	}
	if len(c.removed) > 0 {
		parts = append(parts, "removed ["+ids(c.removed)+"]")
	}
	if len(c.skipped) > 0 {
		parts = append(parts, "skipped ["+strings.Join(c.skipped, ", ")+"]")
	}
	if len(parts) == 0 {
		return "nothing to do"
	}
	return strings.Join(parts, ", ")
}

func refsString(refs []memberRef) string {
	out := make([]string, len(refs))
	for i, m := range refs {
		out[i] = m.String()
	}
	return strings.Join(out, " ")
}

func registerAssociationLists(s *Server) {
	// getLists (request Blaze::Association::GetListsRequest 0x012F4614, sent once per login after enterService:
	// BlazeFriends_RequestLists 0x00ABEFD0 -> AssociationListAPI_GetLists 0x00C9AF00 ->
	// AssociationListsComponent_SendGetLists 0x00C98960). Answered with the account's members of every requested
	// type (MXRC members from OFRC on; the client asks for all), each member announced first (UserAdded,
	// UserUpdated), so the client can finish the reply (GetListsReply).
	s.Register(ComponentAssociationLists, CmdALGetLists, "getLists", func(r *Request) []byte {
		req := RequestedLists(r.Body)
		offset, _ := r.Body.Int("OFRC")
		limit, ok := r.Body.Int("MXRC")
		if !ok {
			limit = 0xFFFFFFFF // the member table's default
		}
		var acct store.Account
		if a, ok := r.account(); ok {
			acct = a
		}
		lists := make([]ListContent, len(req))
		desc := make([]string, len(req))
		total := 0
		for i, l := range req {
			lists[i].Info = DescribeList(l)
			var all []store.ListMember
			if ms := listMembers(&acct.Lists, l.Type); ms != nil {
				all = *ms
			}
			lists[i].Total = len(all)
			from := min(int64(len(all)), max(offset, 0))
			to := min(int64(len(all)), from+max(limit, 0))
			lists[i].Members = all[from:to]
			total += len(all)
			desc[i] = fmt.Sprintf("%d %q", lists[i].Info.Type, lists[i].Info.Name)
			if len(all) > 0 {
				desc[i] += fmt.Sprintf(": %d", len(all))
			}
			for _, m := range lists[i].Members {
				r.introduce(m.ID)
			}
		}
		if total == 0 {
			r.Server.Log.Logf("blaze", "   getLists: %d lists (%s), all empty", len(lists), strings.Join(desc, ", "))
		} else {
			r.Server.Log.Logf("blaze", "   getLists: %d lists (%s members)", len(lists), strings.Join(desc, ", "))
		}
		return GetListsReply(lists, offset)
	})
	// addUsersToList (request Blaze::Association::UpdateListMembersRequest 0x012F4660: BID, BIDL list of
	// ListMemberId 0x014627B8, LID = the list's ListIdentification from our getLists reply, MUTA, VALA, VALD; reply
	// UpdateListMembersResponse 0x012F46B0 {LMID added, REM removed}; RPC job 0x012E6480 at 0x00C97DF9, sender
	// 0x00C987C0 via AssociationList_AddUsersToList 0x00C9AA30, or 0x00C9AB20 with MUTA 1 for the friends list).
	// The client sends it for:
	//   - list 2, at every game's loading step with the game's other Blaze players by BLID, an empty BIDL in a
	//     practice game (0x00AC4850 -> BlazeFriends_AddRecentPlayers 0x00ABD200; it doesn't leave out players the
	//     list has: those are refreshed, not added again);
	//   - list 1 with MUTA 1, when the player accepts a friend request (MaxisChat.AcceptFriendInvite -> 0x00ABFBC0
	//     -> 0x00ABE7C0 -> 0x00ABD5F0), with the inviter by BLID;
	//   - list 4, blocking by id (MaxisChat.BlockUserByID -> 0x00ABDBA0: BLID) or by name (BlockUserByName ->
	//     0x00ABFE10 -> 0x00ABD480: PNAM only).
	// It sends nothing for a member its own list already has (0x00ABD5F0 checks first).
	s.Register(ComponentAssociationLists, CmdALAddUsersToList, "addUsersToList", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("addUsersToList before login")
			return nil
		}
		name, typ := requestedList(r.Body)
		refs := requestedMembers(r.Body)
		var c listChange
		switch typ {
		case ListTypeFriends:
			c = r.Server.addFriends(r, refs)
		case ListTypeRecentPlayers, ListTypeBlock:
			c = r.Server.addMembers(r, typ, refs)
		default:
			r.Server.Log.Logf("blaze", "   addUsersToList list %d %q users [%s]: no such list", typ, name, refsString(refs))
			r.Fail(ErrALListNotFound)
			return nil
		}
		r.Server.Log.Logf("blaze", "   addUsersToList list %d %q users [%s]: %s", typ, name, refsString(refs), &c)
		// the game's loading step sends the recent players without looking at the result (an error would only
		// reach BlazeFriends_OnUpdateListResult, which ignores it for list 2), so that list never refuses
		if !c.changed() && c.err != 0 && typ != ListTypeRecentPlayers {
			r.Fail(c.err)
			return nil
		}
		for _, m := range c.added {
			r.introduce(m.ID)
		}
		return UpdateListMembersReply(c.added, c.removed)
	})
	// removeUsersFromList (cmd 2, sender AssociationListsComponent_SendRemoveUsersFromList 0x00C98890 via
	// 0x00C9AC10, or 0x00C9AD00 with MUTA 1 for the friends list; the same request and reply classes, its result
	// 0x00C9A490): unfriending (MaxisChat.UnfriendByID -> 0x00ABEB80 -> 0x00ABD780: BLID; UnfriendByName ->
	// 0x00ABFD30 finds the member in the list first) and unblocking (UnblockUserByID -> 0x00ABDBD0: BLID;
	// UnblockUserByName -> 0x00ABDBF0 -> 0x00ABD8F0: PNAM only). Removing a friend removes the friendship on both
	// sides.
	s.Register(ComponentAssociationLists, CmdALRemoveUsersFromList, "removeUsersFromList", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("removeUsersFromList before login")
			return nil
		}
		name, typ := requestedList(r.Body)
		refs := requestedMembers(r.Body)
		if listMembers(&store.Lists{}, typ) == nil {
			r.Server.Log.Logf("blaze", "   removeUsersFromList list %d %q users [%s]: no such list", typ, name, refsString(refs))
			r.Fail(ErrALListNotFound)
			return nil
		}
		c := r.Server.removeMembers(r, typ, refs)
		r.Server.Log.Logf("blaze", "   removeUsersFromList list %d %q users [%s]: %s", typ, name, refsString(refs), &c)
		if !c.changed() {
			r.Fail(ErrALMemberNotFound)
			return nil
		}
		return UpdateListMembersReply(nil, c.removed)
	})
}

// addMembers adds to the requester's recent players or block list. The recent players list refreshes a member it
// has (the time, and it moves to the end) and drops its oldest members beyond MaxRecentPlayers; the block list
// refuses a member it has and refuses new ones beyond MaxBlockedUsers.
func (s *Server) addMembers(r *Request, typ int64, refs []memberRef) listChange {
	var c listChange
	me := r.Conn.Session.Persona
	now := s.Now().UTC().Truncate(time.Second)
	type candidate struct {
		ref memberRef
		p   store.Persona
	}
	var cands []candidate
	for _, ref := range refs {
		_, p, ok := s.persona(ref)
		switch {
		case !ok:
			c.skip(ref, ErrALUserNotFound, "unknown user")
		case p.ID == me.ID:
			c.skip(ref, ErrALCannotIncludeSelf, "the user itself")
		default:
			cands = append(cands, candidate{ref, p})
		}
	}
	if len(cands) == 0 {
		return c
	}
	r.update("addUsersToList", func(a *store.Account) {
		list := listMembers(&a.Lists, typ)
		for _, cand := range cands {
			i := store.IndexOf(*list, cand.p.ID)
			m := store.ListMember{ID: cand.p.ID, Name: cand.p.Name, Added: now}
			switch {
			case typ == ListTypeRecentPlayers && i >= 0:
				*list = append(slices.Delete(*list, i, i+1), m)
				c.refreshed = append(c.refreshed, m.ID)
			case i >= 0:
				c.skip(cand.ref, ErrALMemberAlreadyInList, "already in the list")
			case typ == ListTypeBlock && len(*list) >= MaxBlockedUsers:
				c.skip(cand.ref, ErrALListFull, "list full")
			default:
				*list = append(*list, m)
				c.added = append(c.added, m)
			}
		}
		for typ == ListTypeRecentPlayers && len(*list) > MaxRecentPlayers {
			old := (*list)[0]
			*list = slices.Delete(*list, 0, 1)
			if j := store.IndexOf(c.added, old.ID); j >= 0 {
				c.added = slices.Delete(c.added, j, j+1)
				continue
			}
			c.removed = append(c.removed, old)
		}
	})
	return c
}

// addFriends makes the requester and each member friends (both lists at once). Our rules (the original server's
// aren't in the client): the member must have a pending friend request to the requester (a MessageTypeFriendRequest
// in the requester's inbox; the client accepts only invites it got, so this keeps a client from adding anyone), must
// not have blocked the requester, and both lists must have room. The member hears of it (UserAdded for the
// requester, then NotifyUpdateListMembership) after the reply, before the accept message the client sends next.
func (s *Server) addFriends(r *Request, refs []memberRef) listChange {
	var c listChange
	sess := r.Conn.Session
	me := sess.Persona
	now := s.Now().UTC().Truncate(time.Second)
	for _, ref := range refs {
		other, p, ok := s.persona(ref)
		switch {
		case !ok:
			c.skip(ref, ErrALUserNotFound, "unknown user")
			continue
		case p.ID == me.ID:
			c.skip(ref, ErrALCannotIncludeSelf, "the user itself")
			continue
		case !s.msgs.hasMessage(me.ID, p.ID, MessageTypeFriendRequest):
			c.skip(ref, ErrALInvalidUser, "no friend request from it")
			continue
		}
		var why string
		var code uint16
		mine, _, err := s.Store.UpdatePair(sess.Account.UserID, other.UserID, func(a, b *store.Account) {
			switch {
			case store.IndexOf(a.Lists.Friends, p.ID) >= 0:
				code, why = ErrALMemberAlreadyInList, "already a friend"
			case store.IndexOf(b.Lists.Blocked, me.ID) >= 0:
				code, why = ErrALInvalidUser, "it blocked the user"
			case len(a.Lists.Friends) >= MaxFriends:
				code, why = ErrALListFull, "list full"
			case store.IndexOf(b.Lists.Friends, me.ID) < 0 && len(b.Lists.Friends) >= MaxFriends:
				code, why = ErrALPairedListFull, "its list is full"
			default:
				a.Lists.Friends = append(a.Lists.Friends, store.ListMember{ID: p.ID, Name: p.Name, Added: now})
				if store.IndexOf(b.Lists.Friends, me.ID) < 0 {
					b.Lists.Friends = append(b.Lists.Friends, store.ListMember{ID: me.ID, Name: me.Name, Added: now})
				}
			}
		})
		if err != nil {
			s.Log.Logf("blaze", "   (addUsersToList: %v)", err)
		}
		if why != "" {
			c.skip(ref, code, why)
			continue
		}
		if err == store.ErrNoAccount {
			c.skip(ref, ErrALUserNotFound, "unknown user")
			continue
		}
		sess.Account = mine
		c.added = append(c.added, store.ListMember{ID: p.ID, Name: p.Name, Added: now})
		them, friendID := store.ListMember{ID: me.ID, Name: me.Name, Added: now}, p.ID
		r.After(func() { s.tellListChange(friendID, me.ID, ListTypeFriends, them, ListUpdateAdd) })
	}
	return c
}

// removeMembers removes from the requester's list; a friend loses the requester from its friends too, and hears of
// it after the reply.
func (s *Server) removeMembers(r *Request, typ int64, refs []memberRef) listChange {
	var c listChange
	sess := r.Conn.Session
	me := sess.Persona
	for _, ref := range refs {
		id := s.memberID(ref)
		if typ != ListTypeFriends {
			found := false
			r.update("removeUsersFromList", func(a *store.Account) {
				list := listMembers(&a.Lists, typ)
				if i := findMember(*list, ref, id); i >= 0 {
					c.removed = append(c.removed, (*list)[i])
					*list = slices.Delete(*list, i, i+1)
					found = true
				}
			})
			if !found {
				c.skip(ref, ErrALMemberNotFound, "not in the list")
			}
			continue
		}
		acct, ok := r.account()
		i := -1
		if ok {
			i = findMember(acct.Lists.Friends, ref, id)
		}
		if i < 0 {
			c.skip(ref, ErrALMemberNotFound, "not in the list")
			continue
		}
		friend := acct.Lists.Friends[i]
		drop := func(l *[]store.ListMember, id int64) {
			if j := store.IndexOf(*l, id); j >= 0 {
				*l = slices.Delete(*l, j, j+1)
			}
		}
		if other, _, ok := s.Store.PersonaByID(friend.ID); ok {
			mine, _, err := s.Store.UpdatePair(sess.Account.UserID, other.UserID, func(a, b *store.Account) {
				drop(&a.Lists.Friends, friend.ID)
				drop(&b.Lists.Friends, me.ID)
			})
			if err != nil {
				s.Log.Logf("blaze", "   (removeUsersFromList: %v)", err)
			} else {
				sess.Account = mine
			}
			them := store.ListMember{ID: me.ID, Name: me.Name}
			r.After(func() { s.tellListChange(friend.ID, me.ID, ListTypeFriends, them, ListUpdateRemove) })
		} else {
			r.update("removeUsersFromList", func(a *store.Account) { drop(&a.Lists.Friends, friend.ID) })
		}
		c.removed = append(c.removed, friend)
	}
	return c
}

// tellListChange tells every connection of persona to that member (persona about) was added to or removed from its
// list typ: first UserAdded and UserUpdated if that client doesn't know the user yet (the client's member events
// read the member's user without a null check: 0x00ABB300, 0x00ABB390), then NotifyUpdateListMembership.
func (s *Server) tellListChange(to, about int64, typ int64, member store.ListMember, update int64) {
	note := ListMembershipNotification(typ, []store.ListMember{member}, update)
	for _, c := range s.connsOf(to) {
		s.pushUserIfNew(c, about)
		c.Push(s, ComponentAssociationLists, NotifyUpdateListMembership, "NotifyUpdateListMembership", note)
	}
}
