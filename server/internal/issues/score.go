package issues

import (
	"cmp"
	"slices"

	"dawngate/server/internal/store"
)

// A claim weighs by its evidence: 1 with a note only, 2 with a video, 3 with a video and a timestamp. An open issue's
// status is computed from its visible claims' weights (a hidden claim counts for nothing); a verdict sets it.

// Weight is what a claim with this evidence weighs.
func Weight(e store.Evidence) int {
	switch e {
	case store.EvidenceTimed:
		return 3
	case store.EvidenceVideo:
		return 2
	}
	return 1
}

// ClaimWeight is what the claim weighs (also when it is hidden; Tally leaves hidden claims out).
func ClaimWeight(c store.Claim) int { return Weight(c.Evidence()) }

// Tally is an issue's visible claims summed up.
type Tally struct {
	Correct int // the weight of the claims "looks correct"
	Wrong   int // the weight of the claims "looks wrong"
	Claims  int // visible claims
	Videos  int // their video links
	Hidden  int // claims hidden by an admin
}

func weightOf(c store.StanceCount) int {
	w := 0
	for e, n := range c.ByEvidence {
		w += n * Weight(store.Evidence(e))
	}
	return w
}

// TallyOf sums up an issue's claims.
func TallyOf(r store.IssueRow) Tally {
	return Tally{
		Correct: weightOf(r.Correct),
		Wrong:   weightOf(r.Wrong),
		Claims:  r.Correct.Claims() + r.Wrong.Claims(),
		Videos:  r.Correct.Videos + r.Wrong.Videos,
		Hidden:  r.Hidden,
	}
}

// Status is an issue's status as the pages show it.
type Status string

// The statuses: the first five are computed for an open issue, the last three are the verdicts.
const (
	NeedsVerification Status = "needs-verification" // no claim
	NeedsVideo        Status = "needs-video"        // claims, but no video
	LeaningCorrect    Status = "leaning-correct"    // videos, and "looks correct" weighs at least twice "looks wrong"
	LeaningWrong      Status = "leaning-wrong"      // the other way round
	Disputed          Status = "disputed"           // videos, but neither side weighs twice the other
	Confirmed         Status = "confirmed"
	Disproven         Status = "disproven" // a fix is planned
	Closed            Status = "closed"    // can't be verified, or withdrawn
)

// Statuses lists every status in the pages' order.
var Statuses = []Status{NeedsVerification, NeedsVideo, Disputed, LeaningCorrect, LeaningWrong, Confirmed, Disproven, Closed}

// Label is the status's name on the pages.
func (s Status) Label() string {
	switch s {
	case NeedsVerification:
		return "Needs verification"
	case NeedsVideo:
		return "Needs video proof"
	case LeaningCorrect:
		return "Leaning correct"
	case LeaningWrong:
		return "Leaning wrong"
	case Disputed:
		return "Disputed"
	case Confirmed:
		return "Confirmed"
	case Disproven:
		return "Disproven"
	case Closed:
		return "Closed"
	}
	return string(s)
}

// Open reports whether the status is computed (the issue takes claims).
func (s Status) Open() bool {
	return s == NeedsVerification || s == NeedsVideo || s == LeaningCorrect || s == LeaningWrong || s == Disputed
}

// ParseStatus reads a status by its id (a page's filter); ok false for an unknown one.
func ParseStatus(id string) (Status, bool) {
	for _, s := range Statuses {
		if string(s) == id {
			return s, true
		}
	}
	return "", false
}

// Computed is an open issue's status by its tally: no claim (NeedsVerification); claims but no video on either side
// (NeedsVideo); else one side leaning when its weight is at least twice the other's (so a lone video claim leans);
// else disputed (both sides have claims and neither weighs twice the other).
func Computed(t Tally) Status {
	switch {
	case t.Claims == 0:
		return NeedsVerification
	case t.Videos == 0:
		return NeedsVideo
	case t.Correct >= 2*t.Wrong:
		return LeaningCorrect
	case t.Wrong >= 2*t.Correct:
		return LeaningWrong
	}
	return Disputed
}

// StatusOf is an issue's status: its verdict, or computed from its claims while it is open.
func StatusOf(r store.IssueRow) Status {
	switch r.State {
	case store.IssueConfirmed:
		return Confirmed
	case store.IssueDisproven:
		return Disproven
	case store.IssueClosed:
		return Closed
	}
	return Computed(TallyOf(r))
}

// BestFirst returns the claims in the order the issue's page shows them: visible ones first, the heaviest first,
// then the older first; hidden ones last.
func BestFirst(cs []store.Claim) []store.Claim {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b store.Claim) int {
		if a.Hidden != b.Hidden {
			if a.Hidden {
				return 1
			}
			return -1
		}
		if wa, wb := ClaimWeight(a), ClaimWeight(b); wa != wb {
			return wb - wa
		}
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}
