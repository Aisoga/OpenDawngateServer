// Package rating is the players' skill rating (RATING1): Glicko-2 (Mark E. Glickman, "Example of the Glicko-2
// system", 2013) from the rated games, the balanced PvP and custom games (Judge), one game per rating period, each
// player rated against the other team as one composite opponent (Composite). The main menu's Leaderboard ranks by it
// (store.RatingLeaderboard); dgserver seeds it once from the stored matches (Seed).
package rating

import (
	"math"

	"dawngate/server/internal/store"
)

// Tau is Glicko-2's system constant, which limits how fast the volatility changes: Glickman suggests 0.3 to 1.2; 0.5
// is his worked example's and a middle value for a game whose results are fairly random (team games, bots filling).
const Tau = 0.5

// glickoScale converts the Glicko scale (1500, RD 350) to Glicko-2's (0, 2.0148): 173.7178 = 400 / ln 10.
const glickoScale = 173.7178

// epsilon is the volatility iteration's convergence tolerance (Glickman's 0.000001).
const epsilon = 0.000001

// MaxRD is the highest rating deviation: the start's. One game is one rating period, so a deviation only grows by the
// volatility before each game and never past this.
var MaxRD = store.StartRating.RD

// Result is one game's result against one opponent (Glicko scale): its rating, its RD and the score (1 a win, 0 a
// loss, 0.5 a draw).
type Result struct {
	Rating, RD, Score float64
}

// Update is one Glicko-2 rating period (Glickman's steps 1 to 8) for a player rated r with these results: the new
// rating, RD and volatility (r's games and wins unchanged). Without results only the RD grows by the volatility (step
// 6), capped at MaxRD.
func Update(r store.Rating, results []Result) store.Rating {
	r0 := r.OrStart()
	mu, phi, sigma := (r0.Value-1500)/glickoScale, r0.RD/glickoScale, r0.Vol
	out := r
	if len(results) == 0 {
		out.Value, out.Vol = r0.Value, sigma
		out.RD = math.Min(math.Sqrt(phi*phi+sigma*sigma)*glickoScale, MaxRD)
		return out
	}
	// step 3: the estimated variance v; step 4: the estimated improvement delta
	var vInv, sum float64
	for _, o := range results {
		muJ, phiJ := (o.Rating-1500)/glickoScale, o.RD/glickoScale
		g := gOf(phiJ)
		e := expected(mu, muJ, g)
		vInv += g * g * e * (1 - e)
		sum += g * (o.Score - e)
	}
	v := 1 / vInv
	delta := v * sum
	// step 5: the new volatility (the Illinois algorithm)
	sigmaNew := volatility(phi, sigma, v, delta)
	// steps 6 and 7: the new deviation and rating
	phiStar := math.Sqrt(phi*phi + sigmaNew*sigmaNew)
	phiNew := 1 / math.Sqrt(1/(phiStar*phiStar)+1/v)
	muNew := mu + phiNew*phiNew*sum
	// step 8: back to the Glicko scale
	out.Value = glickoScale*muNew + 1500
	out.RD = math.Min(glickoScale*phiNew, MaxRD)
	out.Vol = sigmaNew
	return out
}

// gOf is Glickman's g(phi).
func gOf(phi float64) float64 { return 1 / math.Sqrt(1+3*phi*phi/(math.Pi*math.Pi)) }

// expected is Glickman's E(mu, mu_j, phi_j) with g = g(phi_j).
func expected(mu, muJ, g float64) float64 { return 1 / (1 + math.Exp(-g*(mu-muJ))) }

// volatility is step 5: the root of f by the Illinois algorithm.
func volatility(phi, sigma, v, delta float64) float64 {
	a := math.Log(sigma * sigma)
	f := func(x float64) float64 {
		ex := math.Exp(x)
		d := phi*phi + v + ex
		return ex*(delta*delta-phi*phi-v-ex)/(2*d*d) - (x-a)/(Tau*Tau)
	}
	A := a
	var B float64
	if delta*delta > phi*phi+v {
		B = math.Log(delta*delta - phi*phi - v)
	} else {
		k := 1.0
		for f(a-k*Tau) < 0 {
			k++
		}
		B = a - k*Tau
	}
	fA, fB := f(A), f(B)
	for i := 0; math.Abs(B-A) > epsilon && i < 1000; i++ {
		C := A + (A-B)*fA/(fB-fA)
		fC := f(C)
		if fC*fB <= 0 {
			A, fA = B, fB
		} else {
			fA /= 2
		}
		B, fB = C, fC
	}
	return math.Exp(A / 2)
}

// Composite is the other team as one opponent for a player's update: the mean of its players' ratings and the root
// mean square of their RDs. The mean is the team's typical strength; the root mean square keeps the RD of a typical
// member (a team of well-known players is well known, one of new players isn't), where the RD of the mean
// (sqrt(sum RD^2) / n) would call a team of five new players far better known than any of them. Zero players: the
// start rating.
func Composite(team []store.Rating) Result {
	if len(team) == 0 {
		return Result{Rating: store.StartRating.Value, RD: store.StartRating.RD}
	}
	var sum, sq float64
	for _, r := range team {
		r = r.OrStart()
		sum += r.Value
		sq += r.RD * r.RD
	}
	n := float64(len(team))
	return Result{Rating: sum / n, RD: math.Sqrt(sq / n)}
}
