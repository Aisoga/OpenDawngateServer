package blaze

import (
	"strconv"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Stats component (7), standard BlazeSDK. Commands from GetStatsCommandName.
const (
	ComponentStats               = 7
	CmdStatsGetStatGroup         = 0x04
	CmdStatsGetKeyScopesMap      = 0x0F
	CmdStatsGetStatsByGroupAsync = 0x10
	NotifyStatsGetStatsAsync     = 0x32
)

// StatGroups are the stats of each group the client asks for (getStatGroup after enterService:
// PlayerStats, ShaperStats, ShaperHistoryStats, RoleHistoryStats). PlayerStats' names are the fields of
// the profile's PlayerStatsData (UIRuntime.gfx, docs/data/ui_externalinterface.txt), which C++ fills
// by stat name (B7a, below).
//
// The three keyed groups (B7d, docs/ACCOUNT_CONTENT.md §11): their handlers in the client take the values
// by position and never read a descriptor, so the original stat names are unknown and these are ours,
// in the order the client reads the values (progress.ShaperStatsOf, ShaperHistoryOf, RoleHistoryOf).
var StatGroups = map[string][]string{
	"PlayerStats": {"ASSISTS", "ASSISTSINGAME", "KILLS", "KILLSINGAME", "DOUBLEKILLS", "TRIPLEKILLS", "OMNIKILLS",
		"TETRAKILLS", "STREAKINGAME", "WINS", "PLAYED", "MPS", "MPSG"},
	progress.ShaperStats:        {"PLAYED", "WINS"},
	progress.ShaperHistoryStats: {"PLAYED", "WINS", "KILLS", "DEATHS", "ASSISTS", "CS", "VIM", "SECONDS"},
	progress.RoleHistoryStats:   {"PLAYED", "WINS"},
}

// Key scope names of the keyed groups (ours: the client only looks for the first '=' and reads the number
// after it, StatsManager_OnShaperStatsResult 0x00AA5B80 and its two siblings; search 0x0066A330).
const (
	KeyScopeShaper = "shaper"
	KeyScopeRole   = "role"
)

func registerStats(s *Server) {
	s.Register(ComponentStats, CmdStatsGetKeyScopesMap, "getKeyScopesMap", func(r *Request) []byte {
		return KeyScopesReply()
	})
	s.Register(ComponentStats, CmdStatsGetStatGroup, "getStatGroup", func(r *Request) []byte {
		name, _ := r.Body.Str("NAME")
		return StatGroupReply(name, StatGroups[name])
	})
	s.Register(ComponentStats, CmdStatsGetStatsByGroupAsync, "getStatsByGroupAsync", func(r *Request) []byte {
		name, _ := r.Body.Str("NAME")
		vid, _ := r.Body.Int("VID")
		blazeID, acct, ok := r.statsEntity()
		switch name {
		case progress.ShaperStats, progress.ShaperHistoryStats, progress.RoleHistoryStats:
			var keys []progress.Keyed
			if ok {
				poff, _ := r.Body.Int("POFF")
				pctr, _ := r.Body.Int("PCTR")
				today := r.Server.Now()
				switch name {
				case progress.ShaperStats:
					keys = progress.ShaperStatsOf(acct.History)
				case progress.ShaperHistoryStats:
					keys = progress.ShaperHistoryOf(acct.History, today, int(poff), int(pctr))
				default:
					keys = progress.RoleHistoryOf(acct.History, today, int(poff), int(pctr))
				}
			}
			for _, n := range KeyedStatsNotifications(name, vid, blazeID, keys) {
				r.Notify(ComponentStats, NotifyStatsGetStatsAsync, "GetStatsAsyncNotification", n)
			}
		default:
			var values []string
			if ok && len(StatGroups[name]) > 0 {
				for _, stat := range StatGroups[name] {
					v := acct.Stats[name][stat]
					if v == "" {
						v = "0"
					}
					values = append(values, v)
				}
			} else {
				blazeID = 0
			}
			r.Notify(ComponentStats, NotifyStatsGetStatsAsync, "GetStatsAsyncNotification",
				StatsAsyncNotification(name, vid, blazeID, values))
		}
		return nil
	})
}

// statsEntity is whose stats getStatsByGroupAsync asks for: the first of the request's EID (the client
// sends the profile's Blaze id: StatsManager_RequestPlayerStats 0x00AA6C60 passes one entity, the local
// user's in session g2_run2) if it is a known persona, else the logged-in persona. ok is false before login
// and for an unknown persona.
func (r *Request) statsEntity() (int64, store.Account, bool) {
	if r.Conn.Session == nil {
		return 0, store.Account{}, false
	}
	if ids := intList(r.Body, "EID"); len(ids) > 0 && ids[0] != r.Conn.Session.Persona.ID {
		acct, _, ok := r.Server.Store.PersonaByID(ids[0])
		return ids[0], acct, ok
	}
	acct, ok := r.account()
	return r.Conn.Session.Persona.ID, acct, ok
}

// KeyScopesReply is Stats::getKeyScopesMap (empty request) -> Blaze::Stats::KeyScopes (member table
// 0x012EFF7C: KSIT map; RPC job 0x012EFC00 at 0x00CAC379). No key scopes: KSIT left out.
//
// B7d: the keyed groups need none. The client caches this reply (StatsAPI +0x180, FUN_00CADB50) and its
// view request (0x00CAE670) reads it only for a group whose StatGroupResponse names key scopes (KSUM): with
// KSUM and no KeyScopes the view fails at once with STATS_ERR_BAD_SCOPE_INFO (0x00100007). Our groups name
// none, so their requests carry no KSUM, and the keyed values come back as one notification per key
// (KeyedStatsNotifications), which the client's views take whatever the key (0x00CAE320).
func KeyScopesReply() []byte {
	return nil
}

// StatGroupReply is Stats::getStatGroup (GetStatGroupRequest NAME, e.g. 'PlayerStats', 'ShaperStats',
// 'ShaperHistoryStats', 'RoleHistoryStats') -> Blaze::Stats::StatGroupResponse (member table 0x012F0178:
// CNAM, DESC, ETYP objtype, KSUM map, META, NAME, STAT list; RPC job 0x012EFC20 at 0x00CAC409). STAT
// lists the group's stats as Blaze::Stats::StatDescSummary (0x012EFFF8): NAME, SDSC and LDSC the stat
// name, DFLT '0', FRMT '%d'; the rest keep their defaults (TYPE 0, presumably int). No stats: STAT
// left out (B5). ETYP and KSUM are left out (KSUM: KeyScopesReply). The client's callback (0x00AA6810) only
// files the reply under its NAME.
func StatGroupReply(name string, stats []string) []byte {
	w := tdf.NewWriter().String("CNAM", "").String("DESC", "").String("META", "").String("NAME", name)
	if len(stats) > 0 {
		list := make([]*tdf.Writer, len(stats))
		for i, s := range stats {
			list[i] = tdf.NewWriter().String("DFLT", "0").String("FRMT", "%d").String("LDSC", s).String("NAME", s).
				String("SDSC", s)
		}
		w.List("STAT", tdf.TypeStruct, list)
	}
	return w.Bytes()
}

// StatsAsyncNotification answers Stats::getStatsByGroupAsync (GetStatsByGroupRequest, table 0x012F0220:
// NAME group, EID entity ids, VID view id, ...; four of them after enterService in b5_run4, VID 1-4). The
// reply itself is empty; the stats follow as notification GetStatsAsyncNotification (0x32), which the
// component's dispatcher (DispatchStatsNotification 0x00CB11C0) decodes as Blaze::Stats::KeyScopedStatValues
// (constructor 0x00CB05D0, table 0x012F06F8: GRNM group name, KEY, LAST last notification of the view,
// STS StatValues, VID). The view files the values under KEY, and the game's callbacks (e.g.
// 0x00AA5910 for PlayerStats) look them up under StatsNoScope (0x00CAD610); an empty KEY made that
// lookup return null and crashed the game (b5_run5). LAST = 1.
//
// STS (Blaze::Stats::StatValues 0x012F0128) holds one Blaze::Stats::EntityStats (0x012F0090: EID the
// entity, the player's Blaze id; STAT the values as strings, in the order of the group's StatGroupReply
// STAT). HandlePlayerStatsResult (0x00AA5910, B7a capstone reading) takes the first entity's values and
// pairs them by index with the group's stat descriptors into a name -> value map, but only if both
// counts are equal (0x00AA5A30). Without values (no stats for the group) STS is left out (B5).
//
// This is PlayerStats' answer; the keyed groups get KeyedStatsNotifications.
func StatsAsyncNotification(group string, viewID, blazeID int64, values []string) []byte {
	w := tdf.NewWriter().String("GRNM", group).String("KEY", StatsNoScope).Int("LAST", 1)
	if len(values) > 0 {
		w.Struct("STS", func(w *tdf.Writer) {
			entity := tdf.NewWriter().Int("EID", blazeID).List("STAT", tdf.TypeString, values)
			w.List("STAT", tdf.TypeStruct, []*tdf.Writer{entity})
		})
	}
	return w.Int("VID", viewID).Bytes()
}

// StatsNoScope is the client's key for stats without key scopes (string at 0x012EFF0C).
const StatsNoScope = "No_Scope_Defined"

// KeyedStatsNotifications answers getStatsByGroupAsync for one of the profile's keyed groups (B7d,
// docs/ACCOUNT_CONTENT.md §11): one KeyScopedStatValues per key, in key order, with LAST only on the last.
//
//   - The client's view (StatsAPI notification handler 0x00CAE320) finds itself by VID, stores the values
//     under a non-empty KEY (the first notification of a key wins, 0x00CADEB0) and calls the group's
//     handler on LAST. The handler walks every stored key, so a key it can't read fails the whole profile:
//     today's (B5) answer, No_Scope_Defined without values, did that for all three groups (no number after
//     '=' parses as 0, which they refuse).
//   - KEY is "<scope>=<number>": "shaper=<the shaper's catalogID>" for ShaperStats and ShaperHistoryStats,
//     "role=<1-4>" for RoleHistoryStats. The handlers parse the part after '=' with strtoul (base 10) and
//     refuse 0; the role handler also anything above 4.
//   - STS holds one EntityStats per row (EID the entity, STAT the row's values as decimal strings). The
//     daily groups carry each row's day as POFF (0 = today), which ShaperHistoryStats' handler uses as an
//     index (a negative one would write out of bounds; progress never makes one).
//   - Without keys (no games, or before login): one notification with an empty KEY, which the view doesn't
//     store, and LAST, so the handler runs over no keys and succeeds; STS left out.
func KeyedStatsNotifications(group string, viewID, blazeID int64, keys []progress.Keyed) [][]byte {
	if len(keys) == 0 {
		return [][]byte{tdf.NewWriter().String("GRNM", group).String("KEY", "").Int("LAST", 1).Int("VID", viewID).Bytes()}
	}
	scope := KeyScopeShaper
	if group == progress.RoleHistoryStats {
		scope = KeyScopeRole
	}
	daily := group != progress.ShaperStats
	out := make([][]byte, len(keys))
	for i, k := range keys {
		last := int64(0)
		if i == len(keys)-1 {
			last = 1
		}
		w := tdf.NewWriter().String("GRNM", group).String("KEY", scope+"="+strconv.FormatUint(uint64(k.ID), 10)).
			Int("LAST", last)
		w.Struct("STS", func(w *tdf.Writer) {
			rows := make([]*tdf.Writer, len(k.Rows))
			for j, row := range k.Rows {
				values := make([]string, len(row.Values))
				for n, v := range row.Values {
					values[n] = strconv.FormatUint(uint64(v), 10)
				}
				e := tdf.NewWriter().Int("EID", blazeID)
				if daily {
					e.Int("POFF", int64(row.Offset))
				}
				rows[j] = e.List("STAT", tdf.TypeString, values)
			}
			w.List("STAT", tdf.TypeStruct, rows)
		})
		out[i] = w.Int("VID", viewID).Bytes()
	}
	return out
}
