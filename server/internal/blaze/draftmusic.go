// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.

package blaze

import (
	"encoding/json"
	"errors"

	"dawngate/server/internal/store"
)

// The draft's music switch. The lobby's draft plays the game's shaper-select music on each player's client while it
// runs (client_ui/lobby/DraftSound.as); the speaker button on the board turns it off or on for that player alone. The
// choice is the player's: draft.music {"on": bool} from any player (a drafter, a spectator, or someone in no lobby),
// kept in the account's settings (SettingDraftMusic, "off" or "on") and sent back in the player's own draft.state
// (DraftState.MusicOff, never to anyone else), so it holds across client restarts. The game's own setting for
// shaper-select music can't be read from the lobby, hence a switch of ours.
//
// Only draft.music writes the setting: saveUserSettings drops the key and getUserSettings doesn't show it (the client
// never sees or sends it as one of its own settings), as SettingPreferredRegion.

// CallDraftMusic is the draft's music switch (DRAFT2c).
const CallDraftMusic = "draft.music"

// SettingDraftMusic is the account setting holding the draft's music choice: "off", or "on" (or no entry: on).
const SettingDraftMusic = "DraftMusic"

// ourSetting tells the account settings the server keeps for itself, which the client's settings calls never read or
// write.
func ourSetting(key string) bool {
	return key == SettingPreferredRegion || key == SettingDraftMusic
}

// parseDraftMusic reads draft.music's arguments: {"on":true|false}, strictly as the other draft calls.
func parseDraftMusic(args json.RawMessage) (bool, error) {
	var a struct {
		On *bool `json:"on"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return false, err
	}
	if a.On == nil {
		return false, errors.New("no on in the arguments")
	}
	return *a.On, nil
}

// draftMusicOff is user's (a persona id) stored choice: true when the draft's music is off.
func (s *Server) draftMusicOff(user int64) bool {
	if s.Store == nil {
		return false
	}
	a, _, ok := s.Store.PersonaByID(user)
	return ok && a.Settings[SettingDraftMusic] == "off"
}

// draftMusicCall is user's draft.music: the choice stored when it changes (a log line), and the caller's board sent
// again when it is in a lobby with a draft (the echo). The client applies its own click at once and doesn't wait.
func (s *Server) draftMusicCall(user int64, on bool) error {
	if s.Store == nil {
		return errors.New("no account store")
	}
	acct, _, ok := s.Store.PersonaByID(user)
	if !ok {
		return errors.New("no account")
	}
	want := "on"
	if !on {
		want = "off"
	}
	if was := acct.Settings[SettingDraftMusic]; was != want && (was != "" || want != "on") {
		set := map[string]string{SettingDraftMusic: want}
		if fit, why := settingsFit(acct.Settings, set); !fit { // access.go: the account's settings are at their limit
			return errors.New("the account's settings are full (" + why + ")")
		}
		if _, err := s.Store.Update(acct.UserID, func(a *store.Account) {
			if a.Settings == nil {
				a.Settings = map[string]string{}
			}
			a.Settings[SettingDraftMusic] = want
		}); err != nil {
			return errors.New("not stored: " + err.Error())
		}
		s.Log.Logf("blaze", "   draft: player %d turns the draft's music %s", user, want)
	}
	if hg := s.lobbyOf(user); hg != nil {
		hg.sendDraftTo(user)
	}
	return nil
}
