package botprofiles

import "dawngate/server/internal/setup/assetdata"

// MapRulesFeedbackDefs are a map's own rules' alerts (MAPRULES4): the kill race's milestones (each with the team's own
// line and the enemy's), its opening line, the time limit's opening, lead changes and countdown, and sudden death; then
// (WINBIND-B, from 0x5e70e027) the Binding race's opening lines, match point, time limit and sudden death. The
// match host sends them (gms/core/matchflow/maprules.h holds the same table in the same order) to a team, or, for the
// neutral lines, to everyone and the spectators. No token prints a number, so each message is a def of its own. Their
// text keys are LabsAlerts lines from 0x5e70e010 (localepatch.LabsAlertsLines).
var MapRulesFeedbackDefs = []FeedbackDef{
	{Name: "Rules_Race10Ally", TextKey: "LabsAlerts!0x5e70e010"},
	{Name: "Rules_Race10Enemy", TextKey: "LabsAlerts!0x5e70e011"},
	{Name: "Rules_Race5Ally", TextKey: "LabsAlerts!0x5e70e012"},
	{Name: "Rules_Race5Enemy", TextKey: "LabsAlerts!0x5e70e013"},
	{Name: "Rules_Race3Ally", TextKey: "LabsAlerts!0x5e70e014"},
	{Name: "Rules_Race3Enemy", TextKey: "LabsAlerts!0x5e70e015"},
	{Name: "Rules_MatchPointAlly", TextKey: "LabsAlerts!0x5e70e016"},
	{Name: "Rules_MatchPointEnemy", TextKey: "LabsAlerts!0x5e70e017"},
	{Name: "Rules_RaceTo10", TextKey: "LabsAlerts!0x5e70e018"},
	{Name: "Rules_RaceTo15", TextKey: "LabsAlerts!0x5e70e019"},
	{Name: "Rules_RaceTo20", TextKey: "LabsAlerts!0x5e70e01a"},
	{Name: "Rules_RaceTo25", TextKey: "LabsAlerts!0x5e70e01b"},
	{Name: "Rules_RaceTo30", TextKey: "LabsAlerts!0x5e70e01c"},
	{Name: "Rules_RaceTo50", TextKey: "LabsAlerts!0x5e70e01d"},
	{Name: "Rules_RaceToTarget", TextKey: "LabsAlerts!0x5e70e01e"},
	{Name: "Rules_TimeLimit", TextKey: "LabsAlerts!0x5e70e01f"},
	{Name: "Rules_LeadAlly", TextKey: "LabsAlerts!0x5e70e020"},
	{Name: "Rules_LeadEnemy", TextKey: "LabsAlerts!0x5e70e021"},
	{Name: "Rules_Left10Min", TextKey: "LabsAlerts!0x5e70e022"},
	{Name: "Rules_Left5Min", TextKey: "LabsAlerts!0x5e70e023"},
	{Name: "Rules_Left1Min", TextKey: "LabsAlerts!0x5e70e024"},
	{Name: "Rules_Left30Sec", TextKey: "LabsAlerts!0x5e70e025"},
	{Name: "Rules_SuddenDeath", TextKey: "LabsAlerts!0x5e70e026"},
	// WINBIND-B: the Binding race
	{Name: "Rules_BindRaceTo1", TextKey: "LabsAlerts!0x5e70e027"},
	{Name: "Rules_BindRaceTo2", TextKey: "LabsAlerts!0x5e70e028"},
	{Name: "Rules_BindRaceTo3", TextKey: "LabsAlerts!0x5e70e029"},
	{Name: "Rules_BindRaceTo6", TextKey: "LabsAlerts!0x5e70e02a"},
	{Name: "Rules_BindRaceToTarget", TextKey: "LabsAlerts!0x5e70e02b"},
	{Name: "Rules_BindMatchPointAlly", TextKey: "LabsAlerts!0x5e70e02c"},
	{Name: "Rules_BindMatchPointEnemy", TextKey: "LabsAlerts!0x5e70e02d"},
	{Name: "Rules_BindTimeLimit", TextKey: "LabsAlerts!0x5e70e02e"},
	{Name: "Rules_BindSuddenDeath", TextKey: "LabsAlerts!0x5e70e02f"},
}

// The map rules' alerts are announcements, on the announcer channel as the game's kill and objective lines (under
// the score bar), with the shipped NotPartyLeader's priority 1000 (the channel runs the highest priority first, so a
// kill announcement of a higher one goes before it) and a 4-second line; the host paces them (at least 3 s apart, a lead
// line at most every 30 s), FeedbackCooldown stays (the client records a played def's cooldown, which shows that it
// played); the chat window gets the line too (FeedbackChat), so a player who missed it can read it there.
const (
	MapFeedbackChannel  = "announcer"
	MapFeedbackPriority = 1000
	MapFeedbackLifetime = 4000
)

// MapFeedbackValue is a map rule alert's value: FeedbackValue's with the announcer channel, its priority and
// lifetime.
func MapFeedbackValue(d FeedbackDef) *assetdata.Object {
	v := FeedbackValue(d)
	v.Set("priority", uint64(MapFeedbackPriority))
	v.Set("channel", MapFeedbackChannel)
	v.Set("lifetime", uint64(MapFeedbackLifetime))
	return v
}
