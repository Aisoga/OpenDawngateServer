package dgserver

// The match recorder (--match-record, off by default): every match host writes a file per match beside its dgsim.log
// (<gms-data>\<match>\matchrec_<match>_<UTC start>.txt): the seats, every player's commands, every player's state five
// times a second and the match's events, at most 64 MB a match. dgserver only asks the hosts for it ([host]
// match_record=1, after the operator's --gms-options, which win); the host writes the file (gms/engine/matchrec.cpp)
// and --log-days deletes it with the match's folder.

// matchRecordOption adds match_record=1 to the hosts' options when --match-record is on and the server starts match
// hosts. It returns the start-up line's note.
func matchRecordOption(cfg *config) string {
	if !cfg.matchRecord {
		return "off (--match-record on records every match on the match hosts)"
	}
	if !cfg.hostsOn() {
		return "on, but no match hosts run here (--gms-dir): nothing is recorded"
	}
	cfg.gmsOptions = withOption(cfg.gmsOptions, "match_record", "1")
	return "on: a file per match beside the match host's dgsim.log (matchrec_<match>_<start>.txt, at most 64 MB)"
}
