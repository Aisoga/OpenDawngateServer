package web

import "strings"

// telemetryKeys are every LABS_* telemetry key found in the client (strings in Dawngate.exe).
var telemetryKeys = strings.Fields(`ABILITY_RANKED CAMERA_MODES CHAT CLIENT_FPS COLLECTION_SEARCHRESULTS CONNECT_TO_GMS_FAILED
	EXTERNAL_BROWSER_REQUEST GAMEEXIT GAME_START GAME_TIP_CLOSED JOINED_MATCH LEVEL_GAINED LEVEL_MARKERSET_INFO
	LOADOUT_DETAILS LOADOUT_SUMMARY LOGIN_ERROR LOGIN_SUCCESS LOST_SERVER_BLAZE LOST_SERVER_GMS
	MATCHMAKER_ACCEPT_DIALOG MATCHMAKER_END OBJECT_POOL_ERROR PACE_AGGR PACE_AGGR_TEAM PING_STATS
	PLAYERLEFT_ABNORMAL PLAYERLEFT_NORMAL PLAYERREPORT PURCHASING QOS_DATA RAKNET_BITS SHOP_BUY SHOP_SELL
	SPECTATOR_SESSION STARTUP_OPTIONS SYSINFO UI USER_CHEATED VANITY VIEWED_PROFILE WARD_PLACED
	WAYPOINTS_PURCHASE WEBWIDGET`)

// TelemetryRules is our telemetry_rules.xml. The client's parser (GOSTelemetry_ParseRulesXml
// 0x008bc430) reads <TelemetryRuleConfiguration> with <GlobalThrottle>, <PaceTimeSecs> and <Rule>
// elements (<Key>, <PercentSample>, <MaxReportsPerFrame>). Observed: with no <Rule> the client never
// opens its telemetry connection; with rules it connects right after the QoS check (LABS_QOS_DATA). So
// every key is sampled at 100 % and the telemetry sink sees it all. One element per line: with
// "<Rule><Key>..." on one line the client's reader fails ("Telemetry configuration parse error 8").
func TelemetryRules() []byte {
	var b strings.Builder
	b.WriteString("<!-- Dawngate restoration stub: telemetry rules -->\n<TelemetryRuleConfiguration>\n")
	b.WriteString("    <GlobalThrottle>100.0</GlobalThrottle>\n    <PaceTimeSecs>10</PaceTimeSecs>\n")
	for _, k := range telemetryKeys {
		b.WriteString("    <Rule>\n        <Key>LABS_" + k + "</Key>\n        <PercentSample>100.0</PercentSample>\n")
		b.WriteString("        <MaxReportsPerFrame>10000</MaxReportsPerFrame>\n    </Rule>\n")
	}
	b.WriteString("</TelemetryRuleConfiguration>\n")
	return []byte(b.String())
}
