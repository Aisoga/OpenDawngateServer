package blaze

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
)

// A map's own rules in the services (M31, MAPRULES5). A map package's map.json may carry a rules block (mapc.Rules):
// its balance values are compiled into the map's archive (the host and every client read them from there), while its
// victory (a kill race, a time limit) and start level are the match host's, sent in createGame's mapRules to a host
// whose hello names gmshost.FeatureMapRules. dgserver reads them with the maps folder (FolderLevel) into
// PlayableLevel.MapRules, and every hosted game on such a map:
//
//   - sends them in createGame (launchHostedGame), for every mode; the manager leaves them out for a host without the
//     feature, and hostedGame.mapHost records whether the host takes them (its hello, at gameReady);
//   - a custom game: the browser's name gets the victory's prefix after the map's label (MapRules.Prefix: "[Race 20] ",
//     "[15 min] ", "[Race 20/15m] "), lobby.state's rules part the map's short text, its summary and, when the
//     victory changes, Rated forced off with the reason (LobbyRules.Map, MapText, MapUnrated, MapReason); a host
//     without the feature is a finding that refuses START (rulesFindingsLocked);
//   - the players get the System line 10 s into the match (announceRules) with the map's summary: "Rules: Draft ·
//     Map: First to 20 kills wins; everyone starts at level 6 (Unrated)"; a practice or PvP game on a host without
//     the feature says the map's victory and start level aren't played;
//   - the rating: the match's map_rules column (store.MapRules, only what was played) makes rating.Judge refuse a
//     game whose map victory was played (not Unrated: such a game stays in every history list), and the Leaderboard's
//     wins count leaves it out (ranked); a map with only balance values stays rated in the lobby's game type;
//   - the result words (resultHow: "won the kill race 20–17", "won in sudden death"), on Discord and the home page.
//
// WINBIND-B adds the Binding race (Bindings: the first team to destroy that many enemy Bindings wins; with a time
// limit Bindings decide before kills): only a host whose hello names gmshost.FeatureMapRules2 plays it (hostedGame's
// mapHost2); without it the manager leaves it out of createGame, a custom game's START is refused and the System line
// says it isn't played. Its end is the reason "bindings"; finishGame's bindings (each team's destructions) give the
// score ("won the Binding race 3–1") and go into the record (store.MapRules.Destroyed).

// MapRules are a map's own rules as the services use them (comparable: maps.go compares levels with ==). 0 = not set.
type MapRules struct {
	Kills      int  // a kill race: the first team to this many kills wins
	Minutes    int  // a time limit: after this many minutes the team with more kills wins (a tie: sudden death)
	StartLevel int  // everyone, bots too, starts at this level
	Bindings   int  // a Binding race (WINBIND-B): the first team to destroy this many enemy Bindings wins
	Tuned      bool // the map's own balance values (compiled into its archive: always played, the game stays rated)
	// Summary and Short are mapc.DescribeRules' texts: every rule in plain words ("First to 20 kills wins; everyone
	// starts at level 6.") and a few words ("Race 20 · Lv 6")
	Summary, Short string
}

// IsZero tells whether the map has no rules of its own.
func (r MapRules) IsZero() bool { return r == MapRules{} }

// Victory tells whether the map changes how a match is won (a kill race, a Binding race or a time limit): such a game
// isn't rated.
func (r MapRules) Victory() bool { return r.Kills > 0 || r.Minutes > 0 || r.Bindings > 0 }

// HostPart tells whether the match host has a part to play (the victory or the start level).
func (r MapRules) HostPart() bool { return r.Victory() || r.StartLevel > 0 }

// Wire is createGame's mapRules (nil when the host has nothing to play).
func (r MapRules) Wire() *gmshost.MapRules {
	if !r.HostPart() {
		return nil
	}
	return &gmshost.MapRules{Kills: r.Kills, Minutes: r.Minutes, StartLevel: r.StartLevel, Bindings: r.Bindings}
}

// Prefix is the browser's prefix of a game on the map (after the map's label): "[Race 20] ", "[Bind 3] ",
// "[15 min] ", "[Race 20/15m] ", "[Race 20/Bind 3/15m] ", none for a map that keeps the Guardian's victory.
func (r MapRules) Prefix() string {
	var p []string
	if r.Kills > 0 {
		p = append(p, fmt.Sprintf("Race %d", r.Kills))
	}
	if r.Bindings > 0 {
		p = append(p, fmt.Sprintf("Bind %d", r.Bindings))
	}
	switch {
	case r.Minutes > 0 && len(p) == 0:
		return fmt.Sprintf("[%d min] ", r.Minutes)
	case r.Minutes > 0:
		p = append(p, fmt.Sprintf("%dm", r.Minutes))
	case len(p) == 0:
		return ""
	}
	return "[" + strings.Join(p, "/") + "] "
}

// hostWords names the host's parts the map sets, for "can't play the map's …": "kill race", "time limit and start
// level", "Binding race".
func (r MapRules) hostWords() string {
	var w []string
	if r.Kills > 0 {
		w = append(w, "kill race")
	}
	if r.Bindings > 0 {
		w = append(w, "Binding race")
	}
	if r.Minutes > 0 {
		w = append(w, "time limit")
	}
	if r.StartLevel > 0 {
		w = append(w, "start level")
	}
	switch len(w) {
	case 0:
		return ""
	case 1:
		return w[0]
	}
	return strings.Join(w[:len(w)-1], ", ") + " and " + w[len(w)-1]
}

// victoryWords names the victory the map sets: "kill race", "time limit", "kill race and time limit", "Binding race".
func (r MapRules) victoryWords() string {
	return MapRules{Kills: r.Kills, Minutes: r.Minutes, Bindings: r.Bindings}.hostWords()
}

// hostOnly is r's host parts (the victory and the start level), nothing else.
func (r MapRules) hostOnly() MapRules {
	return MapRules{Kills: r.Kills, Minutes: r.Minutes, StartLevel: r.StartLevel, Bindings: r.Bindings}
}

// unplayed is the host parts of r that a match host can't play: all of them without gmshost.FeatureMapRules
// (hostPlays false), the Binding race without gmshost.FeatureMapRules2 (hostPlays2 false; WINBIND-B), else none.
func (r MapRules) unplayed(hostPlays, hostPlays2 bool) MapRules {
	switch {
	case !hostPlays:
		return r.hostOnly()
	case !hostPlays2 && r.Bindings > 0:
		return MapRules{Bindings: r.Bindings}
	}
	return MapRules{}
}

// played is r without the host parts u names (what a match host that can't play u plays).
func (r MapRules) played(u MapRules) MapRules {
	if u.Kills > 0 {
		r.Kills = 0
	}
	if u.Minutes > 0 {
		r.Minutes = 0
	}
	if u.StartLevel > 0 {
		r.StartLevel = 0
	}
	if u.Bindings > 0 {
		r.Bindings = 0
	}
	return r
}

// Record is what the match played (the history's map_rules): the host's parts only when the host took them (played),
// the balance always.
func (r MapRules) Record(played bool) store.MapRules { return r.RecordOf(played, true, nil) }

// RecordOf is Record for a host that may play the map's parts (hostPlays) but not its Binding race (hostPlays2,
// WINBIND-B), with finishGame's bindings (each team's destructions of enemy Bindings; nil when no Binding race ran).
// A played victory records where it came from ("map").
func (r MapRules) RecordOf(hostPlays, hostPlays2 bool, destroyed []int) store.MapRules {
	p := r.played(r.unplayed(hostPlays, hostPlays2))
	rec := store.MapRules{Kills: p.Kills, Minutes: p.Minutes, StartLevel: p.StartLevel, Bindings: p.Bindings,
		Tuned: r.Tuned}
	if p.Victory() {
		rec.From = store.RulesFromMap
	}
	if p.Bindings > 0 && len(destroyed) == 2 {
		rec.Destroyed = &[2]int{destroyed[0], destroyed[1]}
	}
	return rec
}

// unplayedLocked is the map's host parts the match host can't play (hg.mu held).
func (hg *hostedGame) unplayedLocked() MapRules { return hg.mapRules.unplayed(hg.mapHost, hg.mapHost2) }

// mapVictoryLocked reports whether the match plays a victory other than the Guardian's: the map's own (the map sets
// one and the host takes it) or, since WINLOBBY, the lobby's win condition (lobbyVictoryLocked). Either makes the
// game unrated (hg.mu held).
func (hg *hostedGame) mapVictoryLocked() bool {
	return hg.mapRules.played(hg.unplayedLocked()).Victory() || hg.lobbyVictoryLocked().Victory()
}

// lobbyVictoryLocked is the custom game lobby's win condition as its match plays it (WINLOBBY; hg.mu held): the
// rules' Victory in numbers (Kills, Minutes, Bindings), or the zero MapRules for "guardian", a map with its own victory
// (the map decides; such a choice is a finding that refuses the start) and a host without gmshost.FeatureMapRules2
// (a finding too).
func (hg *hostedGame) lobbyVictoryLocked() MapRules {
	if hg.custom == nil || hg.mapRules.Victory() || !hg.mapHost2 {
		return MapRules{}
	}
	g := hg.custom.rules.Goal()
	return MapRules{Kills: g.Kills, Minutes: g.Minutes, Bindings: g.Bindings}
}

// mapRecordLocked is the history's map_rules column, with finishGame's bindings (hg.mu held). A lobby's win condition
// (WINLOBBY) is recorded as the map's victory would be, with from "lobby", beside the map's start level and balance.
func (hg *hostedGame) mapRecordLocked(destroyed []int) string {
	rec := hg.mapRules.RecordOf(hg.mapHost, hg.mapHost2, destroyed)
	if lv := hg.lobbyVictoryLocked(); lv.Victory() {
		rec.Kills, rec.Minutes, rec.Bindings, rec.From = lv.Kills, lv.Minutes, lv.Bindings, store.RulesFromLobby
		if lv.Bindings > 0 && len(destroyed) == 2 {
			rec.Destroyed = &[2]int{destroyed[0], destroyed[1]}
		}
	}
	return rec.Record()
}

// LobbyVictoryReason is the greyed Rated's text when the lobby's win condition makes the game unrated (WINLOBBY;
// lobby.state's mapReason, as a map's victory's).
const LobbyVictoryReason = "The lobby's win condition makes this game unrated."

// startVictory is startGame's victory for the start's rules (WINLOBBY): their win condition in numbers, nil for
// "guardian" or no rules. Only a host with gmshost.FeatureMapRules2 gets one (the option's rules.HostMapRules2: a
// finding refuses the start otherwise), and never on a map with its own victory (rules.Context.MapVictory).
func startVictory(r *gmshost.Rules) *gmshost.Victory {
	if r == nil {
		return nil
	}
	g := rules.Combination(*r).Goal()
	if g.IsZero() {
		return nil
	}
	return &gmshost.Victory{Kills: g.Kills, Minutes: g.Minutes, Bindings: g.Bindings}
}

// mapUnratedReason is why the map makes a custom game unrated ("" when it doesn't): its victory changes.
func (r MapRules) mapUnratedReason() string {
	if !r.Victory() {
		return ""
	}
	n := 0
	for _, set := range []bool{r.Kills > 0, r.Minutes > 0, r.Bindings > 0} {
		if set {
			n++
		}
	}
	verb := " makes"
	if n > 1 {
		verb = " make"
	}
	return "The map's " + r.victoryWords() + verb + " this game unrated."
}

// mapHostFinding is the finding of a custom game on a map whose host parts its match host can't play ("" = none).
func (r MapRules) mapHostFinding(hostPlays bool) string {
	return r.hostFinding(r.unplayed(hostPlays, true))
}

// hostFinding is the finding for the host parts u a custom game's match host can't play ("" = none): the whole list
// for a host from before MAPRULES4, the Binding race for one from before WINBIND-B.
func (r MapRules) hostFinding(u MapRules) string {
	if !u.HostPart() {
		return ""
	}
	return "This game's match host is an older build that can't play the map's " + u.hostWords() + "."
}

// MatchNoteMaxRunes is the most characters of a System line with the match's rules [our choice, as an operator's
// notice: NoticeMaxRunes; the client cuts none (its chat appends the record's text whole, the operator's broadcasts
// carry up to 1,024 bytes), but a line of 200 characters already fills a few rows of the match's chat]. A map whose
// rules don't fit gets more lines (matchNoteLines); the first is never cut.
const MatchNoteMaxRunes = NoticeMaxRunes

// MatchNoteMaxLines is the most System lines a match's rules take (RulesNoteID and the ids after it) [our choice]: a
// map that sets every member of its rules block takes three (MAPRULES9).
const MatchNoteMaxLines = 4

// matchNoteBalance starts the lines after the first: the rest of the map's rules, its balance values.
const matchNoteBalance = "Map balance: "

// matchNoteLines are the match's System lines, one chat message each (announceRules). The first: the lobby's rules
// ("Rules: Draft", "" for none), then the map's ("Map: " and its summary; with a host that can't play its victory or
// start level, what isn't played), then " (Unrated)" when the game would be rated but isn't (the admin's choice, or
// the map's victory): "Rules: Draft · Map: First to 20 kills wins; everyone starts at level 6 (Unrated)". When the
// whole summary doesn't fit in MatchNoteMaxRunes (MAPRULES9: a map that sets every member runs to about 370
// characters), the first line keeps only how the match is won and the start level, whole, and the balance values
// follow as "Map balance: Respawn 8 s; …" lines, each filled with whole parts up to MatchNoteMaxRunes (a map without
// a victory or start level fills its first line the same way). Only a map whose parts need more than
// MatchNoteMaxLines lines loses its last parts, the cut marked "; …" (none of mapc's members do).
func matchNoteLines(lobby string, m MapRules, hostPlays, unrated bool) []string {
	return matchNoteLinesOf(lobby, m, m.unplayed(hostPlays, true), unrated)
}

// matchNoteLinesOf is matchNoteLines with the host parts the match host can't play (u; WINBIND-B: a host without
// maprules2 plays all but the Binding race), which the first line's tail names.
func matchNoteLinesOf(lobby string, m MapRules, u MapRules, unrated bool) []string {
	head := ""
	if lobby != "" {
		head = "Rules: " + lobby
	}
	tail := ""
	if !m.IsZero() && u.HostPart() {
		tail = unplayedTail(m, u)
	}
	if unrated {
		tail += " (Unrated)"
	}
	if m.IsZero() || m.Summary == "" {
		return []string{head + tail}
	}
	if head != "" {
		head += " · "
	}
	head += "Map: "
	parts := strings.Split(strings.TrimSuffix(m.Summary, "."), "; ")
	if line := head + strings.Join(parts, "; ") + tail; runes(line) <= MatchNoteMaxRunes {
		return []string{line}
	}
	// the victory and the start level lead the summary (mapc.DescribeRules' order), one part each
	host := 0
	if m.Victory() {
		host++
	}
	if m.StartLevel > 0 {
		host++
	}
	host = min(host, len(parts))
	var first []string
	if host > 0 {
		first, parts = parts[:host], parts[host:]
	} else {
		first, parts = fillNoteLine(head, parts, tail)
	}
	lines := []string{head + strings.Join(first, "; ") + tail}
	for len(parts) > 0 {
		var next []string
		next, parts = fillNoteLine(matchNoteBalance, parts, "")
		text := strings.Join(next, "; ")
		if len(lines) == MatchNoteMaxLines-1 && len(parts) > 0 {
			text += "; …"
			parts = nil
		}
		lines = append(lines, matchNoteBalance+capitalize(text))
	}
	return lines
}

// unplayedTail is the first System line's note on the host parts u of m the match host can't play (WINBIND-A2: short,
// so that line 1 with every win condition and a start level stays within MatchNoteMaxRunes): " (not played: the match
// host is too old)" when it plays none of them, else " (the Binding race isn't played: the match host is too old)".
func unplayedTail(m, u MapRules) string {
	const why = ": the match host is too old)"
	if u == m.hostOnly() {
		return " (not played" + why
	}
	verb := " isn't played"
	if strings.Contains(u.hostWords(), " and ") {
		verb = " aren't played"
	}
	return " (the " + u.hostWords() + verb + why
}

// fillNoteLine takes the leading parts that fit in one line between head and tail (joined by "; ", room for a "; …" left at
// the end), at least one, and returns them and the rest.
func fillNoteLine(head string, parts []string, tail string) (taken, rest []string) {
	room := MatchNoteMaxRunes - runes(head) - runes(tail) - runes("; …")
	n, size := 0, 0
	for n < len(parts) {
		add := runes(parts[n])
		if n > 0 {
			add += 2
		}
		if n > 0 && size+add > room {
			break
		}
		size += add
		n++
	}
	return parts[:n], parts[n:]
}

// runes is the length of s in characters.
func runes(s string) int { return utf8.RuneCountInString(s) }

// The finishGame reasons of a map's own victory (the host's map rules, MAPRULES4: gms/core/matchflow/maprules).
const (
	ReasonKills       = "kills"       // a team reached the kill race's target
	ReasonTime        = "time"        // the time limit ran out with a team ahead (in Bindings, then kills)
	ReasonSuddenDeath = "suddenDeath" // a tie at the time limit or a target, and the next kill (or Binding) decided
	ReasonBindings    = "bindings"    // WINBIND-B: a team reached the Binding race's target
)

// mapEndReason reports whether a finishGame reason is a map's own victory.
func mapEndReason(reason string) bool {
	return reason == ReasonKills || reason == ReasonTime || reason == ReasonSuddenDeath || reason == ReasonBindings
}

// bindScore is " 3–1": the winner's destructions of enemy Bindings first (finishGame's bindings; "" without them or
// a winner).
func bindScore(winner int, bindings []int) string {
	if len(bindings) != 2 || (winner != 1 && winner != 2) {
		return ""
	}
	return fmt.Sprintf(" %d–%d", bindings[winner-1], bindings[2-winner])
}

// teamKillsLocked are the teams' kills at the end, the measure of the map's victory (the host's: every seat's kills,
// bots too), from the host's seatStats and playerFinish, merged as the match history merges them (mergeStats): a
// human's seatStats carries no kills (live, MAPRULES8: only the server's extras; a bot's carries playerFinish's keys),
// so a human's kills are its playerFinish's unless its seatStats names them. A human with a playerFinish but no
// seatStats counts too. Without seatStats the humans' playerFinish kills, and only when no bot played (known false
// otherwise). hg.mu held.
func (hg *hostedGame) teamKillsLocked() (kills [2]int64, known bool) {
	if len(hg.seatStats) > 0 {
		seen := map[int64]bool{}
		for _, st := range hg.seatStats {
			if st.Team != 1 && st.Team != 2 {
				continue
			}
			k, ok := st.Stats[progress.StatKills]
			if !st.Bot && st.User != 0 {
				seen[st.User] = true
				if pf, has := hg.finishes[st.User]; has && !ok {
					k = pf.Stats[progress.StatKills]
				}
			}
			kills[st.Team-1] += k
		}
		for _, hp := range hg.players {
			if pf, ok := hg.finishes[hp.p.ID]; ok && !seen[hp.p.ID] && (hp.p.Team == 0 || hp.p.Team == 1) {
				kills[hp.p.Team] += pf.Stats[progress.StatKills]
			}
		}
		return kills, true
	}
	if hg.fillTeams > 0 || hg.game.descriptors()&GameFlagBotsFillTeams != 0 {
		return kills, false
	}
	for _, hp := range hg.players {
		if pf, ok := hg.finishes[hp.p.ID]; ok && (hp.p.Team == 0 || hp.p.Team == 1) {
			kills[hp.p.Team] += pf.Stats[progress.StatKills]
		}
	}
	return kills, len(hg.finishes) > 0
}

// killScore is " 20–17": the winner's kills first ("" without a known score or winner).
func killScore(winner int, kills [2]int64, known bool) string {
	if !known || (winner != 1 && winner != 2) {
		return ""
	}
	return fmt.Sprintf(" %d–%d", kills[winner-1], kills[2-winner])
}

// PlayedRulesWords are a stored match's map rules (store.MapRules, what it played) in the few words of
// mapc.DescribeRules' short text: "Race 20 · Bind 3 · 15 min · Lv 6 · Tuned" ("" for none). The match history's
// badge.
func PlayedRulesWords(r store.MapRules) string {
	var tags []string
	if r.Kills > 0 {
		tags = append(tags, fmt.Sprintf("Race %d", r.Kills))
	}
	if r.Bindings > 0 {
		tags = append(tags, fmt.Sprintf("Bind %d", r.Bindings))
	}
	if r.Minutes > 0 {
		tags = append(tags, fmt.Sprintf("%d min", r.Minutes))
	}
	if r.StartLevel > 0 {
		tags = append(tags, fmt.Sprintf("Lv %d", r.StartLevel))
	}
	if r.Tuned {
		tags = append(tags, "Tuned")
	}
	return strings.Join(tags, " · ")
}
