package blaze

import (
	"sort"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// BattlegroundsAccounts component (0x081F), Dawngate's own (B4): the player's metagame account
// ("BGA"). Commands from GetBattlegroundsAccountsCommandName (docs/data/blaze_components.txt).
const (
	ComponentBattlegroundsAccounts = 0x081F
	CmdBGACreateAccountAsync       = 0x07
	CmdBGAGetBasicData             = 0x73
	CmdBGAGetCompleteData          = 0x76
	CmdBGASetAvatar                = 0x7F
	CmdBGAGetUserSettings          = 0x8A
	CmdBGASaveUserSettings         = 0x8B
)

func registerBattlegroundsAccounts(s *Server) {
	s.Register(ComponentBattlegroundsAccounts, CmdBGAGetCompleteData, "getCompleteData", func(r *Request) []byte {
		acct, ok := r.account()
		if !ok {
			r.Drop("getCompleteData before login")
			return nil
		}
		id := r.Conn.Session.Persona.ID
		return CompleteAccountDataReply(r.Server.basicDataOf(acct, id), InventoryOf(acct, r.Server.Content), acct.PerkPages)
	})
	registerPerkPages(s)
	s.Register(ComponentBattlegroundsAccounts, CmdBGAGetBasicData, "getBasicData", func(r *Request) []byte {
		var list []BasicData
		if l, ok := r.Body.Get("BZID"); ok {
			if ids, ok := l.Value.(*tdf.List); ok {
				for _, v := range ids.Items {
					if id, ok := v.(int64); ok {
						if acct, _, known := r.Server.Store.PersonaByID(id); known {
							list = append(list, r.Server.basicDataOf(acct, id))
						}
					}
				}
			}
		}
		return BasicAccountDataReply(list)
	})
	s.Register(ComponentBattlegroundsAccounts, CmdBGAGetUserSettings, "getUserSettings", func(r *Request) []byte {
		acct, _ := r.account()
		return GetUserSettingsReply(acct.Settings)
	})
	// saveUserSettings (SaveUserSettingsRequest, table 0x012E3414: USET map<string,string>; RPC job
	// 0x011BBD3C at 0x004106D2, response class SaveUserSettingsResponse without members, so the reply
	// is empty). The keys in the request replace the stored ones; others stay. B7b saw the live client send
	// one key per change, so the merge is needed: LOADOUT_0x<shaper catalogID> at game entry
	// (ClientAccountSettings_SaveShaperSelectSettings 0x00411E30, with SKIN_0x%X and the ward and announcer
	// keys), GameServerRegionId and GameServerRegionAny when the play screen's region changes; at the next
	// login Client_RestoreGameServerRegion (0x00670F70) reads the region back (sessions b7b_run9/b7b_run10).
	s.Register(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, "saveUserSettings", func(r *Request) []byte {
		settings := map[string]string{}
		if f, ok := r.Body.Get("USET"); ok {
			if m, ok := f.Value.(*tdf.Map); ok {
				for _, e := range m.Entries {
					k, kok := e.Key.(string)
					v, vok := e.Value.(string)
					if kok && vok {
						settings[k] = v
					}
				}
			}
		}
		r.update("saveUserSettings", func(a *store.Account) {
			for k, v := range settings {
				a.Settings[k] = v
			}
		})
		return nil
	})
	// setAvatar (BGASetAvatarRequest, table 0x012E3350: AVTR uint32; RPC job 0x012B3144 at 0x00AA782F,
	// response class BGASetAvatarResponse without members), sent by the user metadata manager
	// (0x00AA8AB0) when the avatar picker closes. AVTR is an avatar id of the client's avatar list
	// (0x004B8C80, R11): the catalogID of an avatar collection item, or for the built-in avatars the
	// FNV-1 of the lower-cased file name in UI/Icons/Avatars/textures/ (e.g. ui_avatar_fish1 ->
	// 0xE19AFE3B), which the content file doesn't know. So any id is stored as sent; one that isn't a
	// collection avatar is logged.
	s.Register(ComponentBattlegroundsAccounts, CmdBGASetAvatar, "setAvatar", func(r *Request) []byte {
		avtr, _ := r.Body.Int("AVTR")
		if it, ok := r.Server.Content.Item(uint32(avtr)); !ok || it.ItemType != content.ItemPlayerAvatar {
			r.Server.Log.Logf("blaze", "   (setAvatar: %#x is not a collection avatar, presumably a built-in one; stored)", avtr)
		}
		r.update("setAvatar", func(a *store.Account) { a.Avatar = uint32(avtr) })
		return nil
	})
	// createAccountAsync (BattlegroundsAccountsCreateAccountRequest ID '') is sent on every login by the
	// game's LoginState step 15 (platform vtable 0x012B8DA0 slot 16, 0x00AD5AD0 -> sender 0x00AD39F0);
	// step 16 waits for its callback (0x00AD2990: event 0x05BEEAB0 with success = no error). The response
	// class BattlegroundsAccountsCreateAccountResponse has no members (its member-table getter is the
	// shared empty one), so the reply is empty. Our accounts already exist from login.
	s.Register(ComponentBattlegroundsAccounts, CmdBGACreateAccountAsync, "createAccountAsync", func(r *Request) []byte {
		return nil
	})
}

// BasicData is what a Blaze::BattlegroundsAccounts::BGABasicData carries for one account.
type BasicData struct {
	BlazeID int64
	Level   uint32
	XP      uint32
	Avatar  uint32
	Flags   uint64
}

// basicDataOf is the account's BGABasicData. Accounts start at level 1 (an account loaded without a
// level gets 1 too). With Policy.FTUEDone every account has store.FlagFTUEDone.
func (s *Server) basicDataOf(a store.Account, blazeID int64) BasicData {
	b := BasicData{BlazeID: blazeID, Level: max(a.Level, 1), XP: a.XP, Avatar: a.Avatar, Flags: a.Flags}
	if s.Policy.FTUEDone {
		b.Flags |= store.FlagFTUEDone
	}
	return b
}

// basicData writes a Blaze::BattlegroundsAccounts::BGABasicData (member table 0x012E30C0). The game
// copies it into its client data (SetClientBasicInfo 0x00ACE210): BZID (+0x08) the Blaze id, FLAG (+0x38,
// uint64, to account data +0xD10 and the global 0x019CD6E8) user flags, DEVF (+0x10), ALEV (+0x20)
// account level, ALXP (+0x24) account XP, VOTE (+0x2C, float), FIRS (+0x28) seconds until the first-win
// bonus (the game stores now + FIRS * 1000 ms). ABAD (badge) and ATIL (title) are ids, 0 here. GOOD and
// VOTE are floats (type code 0x02), left at their defaults.
//
// AVTR (R11) is not read from getCompleteData: the avatar shown for a player (the main menu's
// Global.GetLocalUserProfileAvatarPath, lobby and profile) comes from getBasicData's BASD, which the
// user metadata manager stores per user (0x00AA78B0: BGABasicData +0x14 -> record +0x30, flag 2). The
// value is an avatar id (see setAvatar): a collection avatar's catalogID or the FNV-1 of a built-in
// avatar's file name; an unknown id (0 included) shows no avatar in the main menu.
//
// FLAG bit 0 (store.FlagFTUEDone) is the Practice-only gate (B7a, capstone): cGameEntryManager's
// vtable +0xF0 (0x006816B0, "Unable to get the Blaze MetagameManager when checking FTUE") returns
// account data +0xD10 & 1 (0x00ACE1E0); the main menu's play-button state (0x004DC920) picks the
// first-time-user action (2 -> kMainMenuContent_MatchTypeFTUE, 0x004DDD9F) only while it is 0, else
// the normal one (1 -> kMainMenuContent_MatchType).
func basicData(w *tdf.Writer, b BasicData) {
	w.Int("ABAD", 0)
	w.Int("ALEV", int64(b.Level))
	w.Int("ALXP", int64(b.XP))
	w.Int("ATIL", 0)
	w.Int("AVTR", int64(b.Avatar))
	w.Int("BZID", b.BlazeID)
	w.Int("DEVF", 0)
	w.Int("FIRS", 0)
	w.Uint("FLAG", b.Flags)
}

// InventoryItem is one Blaze::BattlegroundsAccounts::BGAInventoryItem.
type InventoryItem struct {
	CatalogID uint32
	Type      int // BGEcommerce item type (content.ItemShaper, ...)
	Count     uint32
}

// InventoryOf lists the account's items that the content knows, by catalogID. Items of an unknown
// catalogID (content from another build) are left out, since their type is unknown.
func InventoryOf(a store.Account, c *content.Content) []InventoryItem {
	var list []InventoryItem
	for id, n := range a.Items {
		if it, ok := c.Item(id); ok && it.ItemType != content.ItemInvalid && n > 0 {
			list = append(list, InventoryItem{CatalogID: id, Type: it.ItemType, Count: n})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CatalogID < list[j].CatalogID })
	return list
}

// CompleteAccountDataReply is BattlegroundsAccounts::getCompleteData (empty request) ->
// Blaze::BattlegroundsAccounts::BGAMetagameGetCompleteAccountDataResponse (member table 0x012E343C:
// LIST; RPC job 0x012B821C at 0x00ACE86F). The game's callback (HandleGetCompleteAccountDataResponse
// 0x00AD23B0, snlobby_metagame_manager.cpp) needs exactly one element in LIST, a BGACompleteAccount
// (0x012E3244: BASD BGABasicData +0x08, ITEM list of BGAInventoryItem +0x48, PERK list of
// BGACompletePerkPage +0x78), and hands it to SetClientDataFromCompleteAccount 0x00AD1FA0, which copies
// BASD, the perk pages (0x00AD1C60) and, in its loop at 0x00AD1FF0, each ITEM's ITID (+0x08) -> CONT
// (+0x10) into the metagame manager's map at +0x64; TYPE (+0x0C) isn't read there. Then it raises
// event 0xCEBF3D04 (an error raises 0x90F69C04).
//
// ITEM (BGAInventoryItem, table 0x012E3150: CONT count, ITID catalogID, TYPE) carries the inventory;
// TYPE is the BGEcommerce item type. The client never reads TYPE (R11): the loadout manager's own
// m_componentType (1 perk, 2 gem) comes from the AssetData collection type (0x00ACF990), so any non-zero
// value works. Of the inventory only perk shapes and gems count as owned (Collection_IsOwned 0x00878840
// treats every other collection type as owned), and a perk or gem becomes a loadout component only if
// the catalog has an offer for it and getCatalog's reply came first (0x00ACF990 asserts the catalog is
// loaded). ITEM is left out when empty.
//
// PERK (B7c) carries the account's perk pages (perkpages.go); MetagameClientData_SetPerkPages
// (0x00AD1C60) replaces the client's pages with them. Left out when the account has none.
func CompleteAccountDataReply(b BasicData, items []InventoryItem, pages []store.PerkPage) []byte {
	acct := tdf.NewWriter()
	acct.Struct("BASD", func(w *tdf.Writer) { basicData(w, b) })
	if len(items) > 0 {
		list := make([]*tdf.Writer, len(items))
		for i, it := range items {
			list[i] = tdf.NewWriter().Int("CONT", int64(it.Count)).Int("ITID", int64(it.CatalogID)).Int("TYPE", int64(it.Type))
		}
		acct.List("ITEM", tdf.TypeStruct, list)
	}
	if len(pages) > 0 {
		acct.List("PERK", tdf.TypeStruct, perkPages(pages))
	}
	return tdf.NewWriter().List("LIST", tdf.TypeStruct, []*tdf.Writer{acct}).Bytes()
}

// BasicAccountDataReply is BattlegroundsAccounts::getBasicData (BGAMetagameGetBasicAccountDataRequest:
// BZID list of Blaze ids, member table 0x012E32F8) -> BGAMetagameGetBasicAccountDataResponse (0x012E3430:
// BASD list of BGABasicData; RPC job 0x012B3124 at 0x00AA771F, sent by the game's batched profile
// lookup 0x00AA93D0, callback 0x00AA9100). One entry per known id.
func BasicAccountDataReply(list []BasicData) []byte {
	items := make([]*tdf.Writer, len(list))
	for i, b := range list {
		items[i] = tdf.NewWriter()
		basicData(items[i], b)
	}
	return tdf.NewWriter().List("BASD", tdf.TypeStruct, items).Bytes()
}

// GetUserSettingsReply is BattlegroundsAccounts::getUserSettings (empty request) ->
// GetUserSettingsResponse (member table 0x012E34D0: USET map<string,string>; RPC job 0x011BBD1C at
// 0x004105C2): the settings saveUserSettings stored, by key. No settings: USET left out.
func GetUserSettingsReply(settings map[string]string) []byte {
	if len(settings) == 0 {
		return nil
	}
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]tdf.Pair, len(keys))
	for i, k := range keys {
		pairs[i] = tdf.KV(k, settings[k])
	}
	return tdf.NewWriter().Map("USET", tdf.TypeString, tdf.TypeString, pairs...).Bytes()
}
