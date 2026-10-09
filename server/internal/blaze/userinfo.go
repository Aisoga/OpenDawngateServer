package blaze

import "dawngate/server/internal/progress"

// UserInfoNewsSeen is the user info attribute bit the client sets when the player dismisses the main
// menu's news overlay (NewFeatureNotify, roadmap B6, docs/BLAZE.md *The news overlay and the user info
// attribute (B6)*): MetagameManager_MarkNewFeatureNotifySeen (0x00AD9640) sends setUserInfoAttribute
// with ATTV = UATT | 1<<62 (only if the bit was clear). Each time the main menu activates
// (0x004E053C) the client shows the overlay only while its own copy of the local user's
// UserSessionExtendedData.UATT (+0x108) lacks the bit (0x004765E0 -> 0x00AD9200 -> 0x00AD8E30 with mask
// 0x40000000 on the high word). That reader has one caller and the request's sender (0x00AD3530) one
// caller, so this is the only bit the client uses.
const UserInfoNewsSeen = uint64(1) << 62

// userInfoChanged answers a stored setUserInfoAttribute with UserSessionExtendedDataUpdate, so the
// client's copy of UATT matches the account: the request itself doesn't change that copy, so without
// it the overlay would come back when the main menu activates again in the same session (after a
// match). The handler of that notification (0x00C8C400) replaces the whole extended data, so it carries
// what the login's notification carries. That the original server pushed it here is our assumption.
func userInfoChanged(r *Request) {
	sess := r.Conn.Session
	if sess == nil || sess.Persona.ID == 0 {
		return // before login or loginPersona: nothing to update on the client
	}
	acct := sess.Account
	r.Notify(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate",
		ExtendedDataUpdateNotification(sess.Persona.ID, acct.UserInfo, progress.Wins(acct.Stats[progress.PlayerStats])))
}
