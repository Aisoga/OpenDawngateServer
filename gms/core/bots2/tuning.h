// gms/core/bots2 (BOTS2): the second bot brain's one tuning table. Every threshold, range and timing the decision layer
// uses is here, each with its reason; nothing else in gms/core/bots2 holds a number of its own (data.h holds the game
// data's: the battle spells' ranges and cooldowns, the potion, the ward, SM_Map_v5's ward spots). Header-only, no game
// addresses.
//
// Where the first values come from: the game's data (the bot AI's own conditions, the structures' ranges), the 2014
// player guides and what the classic bots showed in the logs. Values marked [rec] are the
// ones a recording of human matches (the match recorder) should revisit first: they say how a player trades risk for
// tempo, which no data file states.
//
// `[host] bot2_tune='key=value,key=value'` overrides any of them for one match (ApplyOverrides); the host logs the
// table it runs with (Describe).
#pragma once

#include <cstdio>
#include <cstdlib>
#include <string>

namespace gms::bots2 {

struct Tuning {
  // ---- timing ---------------------------------------------------------------------------------------------------------
  int thinkMs = 250;        // a decision 4 times a second: a basic attack takes about 1 s, so no hit is lost waiting;
                            // ten bots cost nothing at that rate
  float retargetS = 1.5f;   // a target outside a fight (a minion, creature or structure) is kept at least this long
                            // unless it dies or leaves reach: no switching at every think (each switch restarts the
                            // follow leaf)
  float threatHoldS = 3.0f;  // a threat to the base stays on this long after its last enemy left: no flicker between
                             // Defend and the lane
  float reaimDist = 5.0f;   // a walk's goal that moved less than this keeps the walk (the follow leaf restarts on a new
                            // goal, which costs a step)

  // ---- the base and the shop ------------------------------------------------------------------------------------------
  float baseRadius = 45.0f;   // the spawn platform: SM_Map_v5's spawn is 22.6 from its Locus and 62 from its Guardian,
                              // so this is the fountain and the shop, not the Cores
  float shopNear = 6.0f;      // standing this close to the spawn point is inside the shop's reach (bots respawn there and
                              // buy at once)
  float shopStandS = 1.5f;    // the host's shop visit buys after 1 s in the shop's reach ([host] bot_shop_visit_ms 1000)
  float leaveBaseHp = 0.90f;  // heal at the fountain to this before walking out [rec]

  // ---- retreat and recall ---------------------------------------------------------------------------------------------
  float retreatHp = 0.35f;             // the bot AI's own condition_Bot_HealthLow is 30 % [data]; 35 since the first v2
                                       // match, where HP fell from 54 % to 17 % in 1.6 s of a fight [rec]
  float retreatExitHp = 0.70f;         // back to the fight only well healed: the gap is the hysteresis [rec]
  float retreatOutnumberedHp = 0.55f;  // outnumbered (fleeOdds) and below this: leave before it is too late [rec]
  float recallSafeRange = 40.0f;       // the recall is an 8-s channel any hit breaks: no visible enemy shaper this near
                                       // (30 in round 2: 10 of 68 deaths of one match came during a recall)
  float recallRetryS = 4.0f;           // a recall that broke or was refused is asked again after this
  float recallMinionRange = 15.0f;     // a gold recall also wants no enemy minion this near (their hits broke the gold
                                       // recalls in lane, 4 in 20 s for one bot of the first v2 match)
  float recallQuietS = 2.0f;           // ... and no hit taken for this long
  float recallGold = 1400.0f;          // with HP below recallGoldHp: go shopping (an item step costs about 1,100-1,600)
  float recallGoldHp = 0.70f;          //   [rec]
  float recallGoldAlways = 2600.0f;    // this much gold is a whole item: shop at any HP [rec]
  float siegeNoRecallRange = 90.0f;    // within this of the enemy Guardian a bot on a push doesn't recall for gold

  // ---- fights ---------------------------------------------------------------------------------------------------------
  float engageRange = 22.0f;      // an enemy shaper this near is engaged: most targeted abilities reach 17-25 and the
                                  // ranged basic attack 16.18 [data]
  float fightCountRange = 32.0f;  // the shapers around the bot and around its target that count for the odds
  float engageOdds = 1.0f;        // own strength / enemy strength to engage: at least even (0.85 in the first v2 match
                                  // gave 56 deaths against the classic brain's 37) [rec]
  float fleeOdds = 0.55f;         // below this the bot is outnumbered and backs off [rec]
  float disengageExitOdds = 0.75f;  // ... until the odds are back to this (hysteresis; no enemy near: at once) ...
  float disengageMinS = 2.0f;     // ... and at least this long (the first v2 match flipped push and retreat 4 times in
                                  // 3 s as enemies left and entered the team's sight)
  float towerOdds = 1.5f;         // a shooting structure in reach counts as this many full-HP shapers for its side
  float lowTargetBonus = 0.5f;    // a target below diveHp counts this much less: finish kills
  float fightCommitS = 3.0f;      // an engaged fight lasts at least this long unless the bot must retreat [rec]
  float chaseRange = 40.0f;       // a target is followed this far past the point where the fight began [rec]
  float lostSightS = 2.0f;        // a target the team hasn't seen this long is dropped
  float hitBackS = 3.0f;          // an enemy shaper that hit the bot this recently is fought back (any odds above flee)
  float diveHp = 0.25f;           // a target this low may be chased into its structure's reach ...
  float diveSelfHp = 0.50f;       // ... by a bot with at least this much HP [rec]
  float ultHp = 0.45f;            // the R on a target at or below this (kits.h rTargetHp) ...
  int ultCrowd = 2;               // ... or with this many more enemy shapers within ultCrowdRange of it
  float ultCrowdRange = 12.0f;

  // ---- lanes, structures and the push ----------------------------------------------------------------------------------
  float laneBackOff = 6.0f;       // stand this far behind the own front: the minions take the first hits
  float farmRange = 20.0f;        // enemy minions this near are attacked (the ranged basic attack 16.18 plus a step)
  float laneWidth = 20.0f;        // a minion counts for a lane within this of its path (core/bots/lanes.h)
  float towerMargin = 4.0f;       // an unsafe enemy structure's reach plus this is kept clear
  int towerMinMinions = 2;        // own minions in a Binding's reach make it safe to hit: Bindings shoot minions
                                  // first (the game's tips) [data]
  int siegeAllies = 3;            // or this many bots of the team (the bot included) near it
  float siegeAllyRange = 30.0f;
  float groupAtS = 840.0f;        // from match 14:00 the whole team pushes one lane together, so a match ends in a
                                  // reasonable time; 2014 matches ran 25-35 minutes [rec]
  int advantageLead = 2;          // or earlier, while the team has this many more living shapers than the enemy: a won
                                  // fight is the window in which a base falls [rec]
  float pushLaneHoldS = 90.0f;    // the push lane is changed at most this often
  float groupRadius = 35.0f;      // a team mate this near the rally point, or within the objective's reach plus 10,
                                  // counts for the group; the group hits the objective together, the others gather
  float rallyBack = 12.0f;        // the rally point stands this far outside the objective's reach, on the push lane
  float siegeAnchorRange = 120.0f;  // round 9: the team's bots this near the enemy spawn choose the next Core (the
                                    // nearest to them; on SM the Cores stand about 50-115 from it)
  float siegeCoreHpWeight = 40.0f;  // ... a full Core counts this much farther than a dead-low one (damaged first)
  float siegeKeep = 15.0f;          // ... and the current objective this much nearer (no flip between two)
  // round 11: the Guardian phase (from the enemy's last Core on; the user's rule: group up, in together, out together)
  float guardStageBack = 10.0f;     // the staging point stands this far outside the target's reach (and every shooting
                                    // enemy structure's), toward the own base
  float guardStageRadius = 14.0f;   // a bot this near the staging point is there
  int guardMinBots = 3;             // the group goes in with every living bot there, or with this many and as many as
                                    // the enemy has alive, or ... (round 12: 3, was 4; round 11's `_d1`/`_d2` never got 4
                                    // there against v2's defence)
  float guardStragglerS = 4.0f;     // ... once they have waited this long for the rest (a straggler arriving this soon
                                    // after the engage still joins it; round 12: 4, was 6)
  float guardSpawnKeep = 75.0f;     // round 12: the staging point stays this far from the enemy spawn (its defenders
                                    // respawn there; round 11's `_d1` staged 61 from it)
  float guardLaneStep = 35.0f;      // round 12: RouteTo's step along the push lane
  float guardRetryS = 8.0f;         // no new engage this soon after a back-off
  float locusAvoid = 50.0f;       // the enemy Locus kills shapers within its aggro range 38.5 [data]: keep this clear
  float defendRadius = 45.0f;     // enemies this near an own Core or the Guardian are a threat to the base
  int defenders = 3;              // the team's nearest bots that answer a threat to the base ...
  float defendReach = 160.0f;     // ... if they are this near it (in the first v2 match bots crossed the map to defend
                                  // a lane's last Binding and no push converted); a threat to the Guardian calls all

  // ---- the jungle -----------------------------------------------------------------------------------------------------
  float campJoin = 14.0f;         // creatures this close together are one camp (a camp's members stand within 10.3)
  float campPenalty = 90.0f;      // a camp on the enemy's half costs this much more (core/bots/jungle.h)
  float campEngage = 25.0f;       // a camp this near is fought, a farther one walked to
  float hunterJoinS = 840.0f;     // the Hunter farms camps until this, then pushes with the team (= groupAtS)

  // ---- the laning phase (BOTS2c; the user's first match against v2: "no traditional laning and trading. It's KILL THE
  // OTHER", 33 recalls in 4.5 minutes, two bots of a lane walking the same points) -------------------------------------
  // Until its team pushes (groupAtS or an advantage) a bot lanes: it stands behind its own minions, farms, and fights an
  // enemy shaper only in a short trade, a clear kill chance or to answer a hit.
  float tradeRange = 16.0f;       // an enemy shaper this near is traded with (the ranged basic attack reaches 16.18)
  float tradeS = 2.5f;            // a trade lasts this long (the kit's Q, W, E and a basic), then the bot steps back ...
  float tradeGapS = 10.0f;        // ... tradeBackOff further behind its minions, and trades again only after this
                                  // (6 in round 3: the trades' damage drove v2's recalls against the classic brain)
  float tradeBackOff = 8.0f;
  float tradeHp = 0.60f;          // a trade only with this much HP (0.5 in round 3) [rec]
  float threatS = 2.0f;           // a hurt laner retreats only from an enemy shaper within escapeRange or a shaper's hit
                                  // this recent, and lanes on once neither has been there this long
  float killOdds = 1.8f;          // a fight to the kill in the laning phase: the odds at least this ...
  float killHp = 0.30f;           // ... with the target at or below this HP ...
  float gankOdds = 2.5f;          // ... or the odds at least this (two on one, or one under its own Binding) with the
                                  // bot at tradeHp or more (round 2 at 1.6 / 40 % / 2.0: 170-175 kill fights against
                                  // 47-74 trades; the user: "KILL THE OTHER") [rec]
  float firstWaveWaitS = 75.0f;   // until this match second a bot without own minions on its lane waits in its base
                                  // (the first wave comes at 60 s [data]): not 25-40 s standing at its Binding
  float laneChaseRange = 25.0f;   // a kill in the laning phase is chased this far (chaseRange in a push)
  float laneCarryBack = 7.0f;     // the Gladiator (the last hits) stands this far behind its own minions' front ...
  float laneSupportBack = 13.0f;  // ... its lane mate this far (a third bot as much again) ...
  float laneSide = 5.0f;          // ... each this far to its own side of the lane's line (the first one side, the
                                  // second the other): two bots of a lane never stand or walk on one point
  float minionKeepOut = 10.0f;    // outside a trade never nearer the enemy minions' front than this (their aggro)
  float towerWait = 8.0f;         // without own minions on the lane: wait this far in front of the own outermost
                                  // Binding (not at mid lane, where both teams' bots met at 0:31 and fought)
  float walkSpread = 4.0f;        // every long walk's goal moves this far to the bot's side (no single file)
  float laneRetreatExitHp = 0.55f;  // in the laning phase a retreat ends at this HP (a potion brings it back) ...
  float recallHp = 0.20f;         // ... and recalls only below this (0.25 in round 4: 2-6 recalls a laner in 10 minutes
                                  // against classic's 0-1; 0.15 in round 5: v2 died more than classic in 3 of 4) [rec]
  int hunterJungleLevel = 2;      // the Hunter lanes until this level (round 5: his first camp at level 1 took him from
                                  // full HP to 13-14 % and home, in every match)
  float rallyPull = 15.0f;        // in a push a bot whose lane work keeps it this much farther from the objective than
                                  // the rally point walks to the rally point (round 4: a group never formed at the inner
                                  // Binding or the Cores for 6 and 9 minutes, its waves dying before the rally point)
  float parasiteNear = 100.0f;    // an attempt starts only with parasiteAllies of the able bots this near the parasite
                                  // (round 8: 100, was 80; rounds 5-7 started no attempt at all)
                                  // (round 4: "0 of 4 there" at the start, the group 20-45 s late, "too long"; with 4 in
                                  // round 5 no attempt started at all, while classic took it 2-4 times a match)
  float goldRecallFromS = 240.0f; // no gold recall in the first 4 minutes, and only with gold for the next item step

  // ---- spirit wells (BOTS2c) ------------------------------------------------------------------------------------------
  // The host's well rules: a capture takes 16 s for one capturer (8 s for five), an owner's shaper in the 12-unit ring
  // contests it, wells unlock at 15:00 and lock 4 minutes after each capture; a worker pays 6 gold to the killer's team
  // near it (the Predator's role doubles it). Only an owned well can be captured, so "taking a well" is an enemy's.
  float wellReach = 120.0f;       // an own-side well the enemy took is retaken by the team's nearest bot within this
                                  // (WELLDEF's Tactician range)
  float wellTakerReach = 200.0f;  // the team's well taker (its Predator, else its Hunter) goes this far to take or raid
                                  // an enemy well (WELLDEF's 220 for Hunters and Predators, less the platform)
  float wellSafeRange = 45.0f;    // a capture or raid is started and kept only while no enemy shaper the team sees is
                                  // this near the well
  float wellHp = 0.60f;           // ... by a bot with this much HP [rec]
  float wellStand = 4.0f;         // a capturer or defender stands this far from the centre toward its base: inside the
                                  // 12-unit ring, beside the workers' drop-off point (4 out)
  float wellTaskMaxS = 60.0f;     // a capture or raid ends after this (one capturer's 16 s plus a walk of up to ~30 s)
  float wellRetryS = 60.0f;       // after a well task the bot does its lane (or its camps) at least this long
  int raidMinWorkers = 6;         // a raid starts on an enemy well with this many workers (the classic harass: 5) ...
  int raidEndWorkers = 2;         // ... and ends below this (the classic harass's 2)
  float raidRange = 28.0f;        // the raid's workers: this near the well's centre (their spots stand 18-22 out)
  float wellDefendReach = 150.0f; // an own well under capture is defended by the nearest bot within this (WELLDEF:
                                  // 120-220 by role) ...
  float walkSpeed = 7.0f;         // ... that gets there in time at this speed (WELLDEF's 7 units/s) ...
  float wellInTimeS = 3.0f;       // ... with this margin; a capture that doesn't move (contested, paused) always is
  float wellDefendHp = 0.45f;     // a defender sets out with this much HP (WELLDEF's 45 %)

  // ---- battle spells (BOTS2c; the shaper's spellOrder: one at levels 1, 10 and 20, inventory 6-8) ---------------------
  float spellRangeMargin = 1.5f;  // a targeted spell is cast only inside its range less this (no walk-up for a spell)
  float castRetryS = 2.0f;        // a spell or potion the engine refused is asked again after this
  float hitRecentS = 1.0f;        // "being hit": damage taken this recently
  float spellHurtHp = 0.50f;      // the shields (Bastion, Deflect on itself) below this HP while being hit
  float lastStandHp = 0.20f;      // Stasis below this HP while being hit with an enemy shaper near
  float escapeRange = 20.0f;      // a retreating bot's escapes (Blitz, Tailwind, Dispel, Blink) and its heal (Drain)
                                  // when a seen enemy shaper is this near
  float blinkStep = 16.0f;        // Blink this far back along the bot's lane (range 18.39): only on a lane, whose ground
                                  // is walkable (the server puts a teleport exactly where it is aimed)
  float chaseSpellDist = 12.0f;   // Blitz or Tailwind in a fight to close in on a target farther than this
  float exceedRange = 12.0f;      // Exceed (+Power 5 s) when the fight's target is this near
  float allyShieldHp = 0.50f;     // Deflect at a team mate below this within its range

  // ---- potions and wards (BOTS2c) ----------------------------------------------------------------------------------
  float potionHp = 0.65f;         // a Vitality Potion (150 HP over 15 s) below this HP outside the base, one at a time
                                  // [rec]
  float wardDetour = 40.0f;       // a free ward spot this near the bot's way is warded: a lane or push walk turns to it
                                  // (SM_Map_v5's lane-side spots stand 25-30 from the lanes)
  float wardCastMargin = 1.5f;    // the ward is cast within its range (15.8) less this
  float wardRetryS = 20.0f;       // a ward the engine refused: the next try after this (no walk back and forth)

  // ---- the parasite (BOTS2c; the user: "a possibility after min 15 after a won fight") -------------------------------
  float parasiteFromS = 900.0f;   // a team objective from 15:00 (its stage 2 comes then [data])
  int parasiteAllies = 3;         // the team's bots that hit it together (fewer when fewer live, never under 2): it has
                                  // 13,050-15,000 HP and heals 400 a second out of combat [data]; round 2's groups of
                                  // three gave up 5 of 6 attempts ("too few"), with 4 (round 5) none started; since
                                  // round 4 the bots also start healthy (parasiteHp)
  float parasiteHp = 0.50f;       // an attempt counts only the bots this healthy near it (round 8: per bot, 50 %; was
                                  // the able bots' average 70 %, rarely met right after a won fight)
  float parasiteGather = 34.0f;   // the group gathers this far from its centre toward the own base: outside its aggro
                                  // (27) and its ranged attack's reach (30 from the map's centre) [data]
  float parasiteMaxS = 75.0f;     // the attempt is given up after this ...
  float parasiteRetryS = 60.0f;   // ... and not taken up again for this long

  // ---- the hybrid (BOTS2c round 7: the classic brain's micro, v2's macro) -----------------------------------------------
  float hybridFightRange = 30.0f; // an enemy shaper this near hands a hybrid bot to the classic brain (its fights and
                                  // retreats: rounds 3-6 head to head, classic fought, farmed and recalled better)
  float hybridIdleS = 3.0f;       // the classic brain leaving a calm bot this long without a move or a hit: v2 rescues it
  float hybridHoldS = 2.0f;       // a takeover lasts at least this long (no flip every think)
  float hybridCalmRange = 38.0f;  // round 8: a takeover for a task needs no enemy shaper this near ...
  float hybridCalmS = 3.0f;       // ... for this long (round 7: 262 of 306 releases were "an enemy near", in bursts of
                                  // flips at the 30-unit line; 8 more than the fight range, about a second's walk)
  float hybridTaskBackoffS = 20.0f;  // round 8: a task ended by an enemy within hybridFightRange isn't taken again by
                                    // that bot for this long (round 7: 125-268 such releases a match, then deaths)
  int hybridPushBots = 3;         // round 8: the push takes this many bots outside the enemy base (siegeAllies'), the
                                  // rest farm with the classic brain (round 7: every calm bot pushed, gold fell 8,000
                                  // behind classic's from the push phase on); at a Core or the Guardian every healthy bot
  float groupHoldS = 5.0f;        // a group formed holds at least this long (round 6: "grouped" toggled about 4 times a
                                  // second at a Binding as bots stepped in and out of its fire)
  int parasiteLead = 1;           // the parasite after a won fight: this many more living shapers than the enemy (with
                                  // advantageLead 2 no v2 team tried it in rounds 5 and 6; classic took it 2-4 times)

  // ---- the census (measurement only, both brains) ----------------------------------------------------------------------
  float idleMove = 1.0f;          // a living bot away from its base that moved less than this in a second ...
  float idleHitS = 2.0f;          // ... and dealt no damage for this long had an idle second
};

// One key of the table for ApplyOverrides and Describe.
struct TuningField {
  const char* name;
  float Tuning::*f;
  int Tuning::*i;
};

inline const TuningField* TuningFields(int* n) {
  static const TuningField k[] = {
      {"thinkMs", nullptr, &Tuning::thinkMs},
      {"retargetS", &Tuning::retargetS, nullptr},
      {"threatHoldS", &Tuning::threatHoldS, nullptr},
      {"reaimDist", &Tuning::reaimDist, nullptr},
      {"baseRadius", &Tuning::baseRadius, nullptr},
      {"shopNear", &Tuning::shopNear, nullptr},
      {"shopStandS", &Tuning::shopStandS, nullptr},
      {"leaveBaseHp", &Tuning::leaveBaseHp, nullptr},
      {"retreatHp", &Tuning::retreatHp, nullptr},
      {"retreatExitHp", &Tuning::retreatExitHp, nullptr},
      {"retreatOutnumberedHp", &Tuning::retreatOutnumberedHp, nullptr},
      {"recallSafeRange", &Tuning::recallSafeRange, nullptr},
      {"recallRetryS", &Tuning::recallRetryS, nullptr},
      {"recallMinionRange", &Tuning::recallMinionRange, nullptr},
      {"recallQuietS", &Tuning::recallQuietS, nullptr},
      {"recallGold", &Tuning::recallGold, nullptr},
      {"recallGoldHp", &Tuning::recallGoldHp, nullptr},
      {"recallGoldAlways", &Tuning::recallGoldAlways, nullptr},
      {"siegeNoRecallRange", &Tuning::siegeNoRecallRange, nullptr},
      {"engageRange", &Tuning::engageRange, nullptr},
      {"fightCountRange", &Tuning::fightCountRange, nullptr},
      {"engageOdds", &Tuning::engageOdds, nullptr},
      {"fleeOdds", &Tuning::fleeOdds, nullptr},
      {"disengageExitOdds", &Tuning::disengageExitOdds, nullptr},
      {"disengageMinS", &Tuning::disengageMinS, nullptr},
      {"towerOdds", &Tuning::towerOdds, nullptr},
      {"lowTargetBonus", &Tuning::lowTargetBonus, nullptr},
      {"fightCommitS", &Tuning::fightCommitS, nullptr},
      {"chaseRange", &Tuning::chaseRange, nullptr},
      {"lostSightS", &Tuning::lostSightS, nullptr},
      {"hitBackS", &Tuning::hitBackS, nullptr},
      {"diveHp", &Tuning::diveHp, nullptr},
      {"diveSelfHp", &Tuning::diveSelfHp, nullptr},
      {"ultHp", &Tuning::ultHp, nullptr},
      {"ultCrowd", nullptr, &Tuning::ultCrowd},
      {"ultCrowdRange", &Tuning::ultCrowdRange, nullptr},
      {"laneBackOff", &Tuning::laneBackOff, nullptr},
      {"farmRange", &Tuning::farmRange, nullptr},
      {"laneWidth", &Tuning::laneWidth, nullptr},
      {"towerMargin", &Tuning::towerMargin, nullptr},
      {"towerMinMinions", nullptr, &Tuning::towerMinMinions},
      {"siegeAllies", nullptr, &Tuning::siegeAllies},
      {"siegeAllyRange", &Tuning::siegeAllyRange, nullptr},
      {"groupAtS", &Tuning::groupAtS, nullptr},
      {"advantageLead", nullptr, &Tuning::advantageLead},
      {"pushLaneHoldS", &Tuning::pushLaneHoldS, nullptr},
      {"groupRadius", &Tuning::groupRadius, nullptr},
      {"rallyBack", &Tuning::rallyBack, nullptr},
      {"siegeAnchorRange", &Tuning::siegeAnchorRange, nullptr},
      {"siegeCoreHpWeight", &Tuning::siegeCoreHpWeight, nullptr},
      {"siegeKeep", &Tuning::siegeKeep, nullptr},
      {"guardStageBack", &Tuning::guardStageBack, nullptr},
      {"guardStageRadius", &Tuning::guardStageRadius, nullptr},
      {"guardMinBots", nullptr, &Tuning::guardMinBots},
      {"guardStragglerS", &Tuning::guardStragglerS, nullptr},
      {"guardRetryS", &Tuning::guardRetryS, nullptr},
      {"guardSpawnKeep", &Tuning::guardSpawnKeep, nullptr},
      {"guardLaneStep", &Tuning::guardLaneStep, nullptr},
      {"locusAvoid", &Tuning::locusAvoid, nullptr},
      {"defendRadius", &Tuning::defendRadius, nullptr},
      {"defenders", nullptr, &Tuning::defenders},
      {"defendReach", &Tuning::defendReach, nullptr},
      {"campJoin", &Tuning::campJoin, nullptr},
      {"campPenalty", &Tuning::campPenalty, nullptr},
      {"campEngage", &Tuning::campEngage, nullptr},
      {"hunterJoinS", &Tuning::hunterJoinS, nullptr},
      {"tradeRange", &Tuning::tradeRange, nullptr},
      {"tradeS", &Tuning::tradeS, nullptr},
      {"tradeGapS", &Tuning::tradeGapS, nullptr},
      {"tradeBackOff", &Tuning::tradeBackOff, nullptr},
      {"tradeHp", &Tuning::tradeHp, nullptr},
      {"threatS", &Tuning::threatS, nullptr},
      {"killOdds", &Tuning::killOdds, nullptr},
      {"killHp", &Tuning::killHp, nullptr},
      {"gankOdds", &Tuning::gankOdds, nullptr},
      {"laneChaseRange", &Tuning::laneChaseRange, nullptr},
      {"laneCarryBack", &Tuning::laneCarryBack, nullptr},
      {"laneSupportBack", &Tuning::laneSupportBack, nullptr},
      {"laneSide", &Tuning::laneSide, nullptr},
      {"minionKeepOut", &Tuning::minionKeepOut, nullptr},
      {"towerWait", &Tuning::towerWait, nullptr},
      {"walkSpread", &Tuning::walkSpread, nullptr},
      {"laneRetreatExitHp", &Tuning::laneRetreatExitHp, nullptr},
      {"recallHp", &Tuning::recallHp, nullptr},
      {"hunterJungleLevel", nullptr, &Tuning::hunterJungleLevel},
      {"rallyPull", &Tuning::rallyPull, nullptr},
      {"parasiteNear", &Tuning::parasiteNear, nullptr},
      {"goldRecallFromS", &Tuning::goldRecallFromS, nullptr},
      {"wellReach", &Tuning::wellReach, nullptr},
      {"wellTakerReach", &Tuning::wellTakerReach, nullptr},
      {"wellSafeRange", &Tuning::wellSafeRange, nullptr},
      {"wellHp", &Tuning::wellHp, nullptr},
      {"wellStand", &Tuning::wellStand, nullptr},
      {"wellTaskMaxS", &Tuning::wellTaskMaxS, nullptr},
      {"wellRetryS", &Tuning::wellRetryS, nullptr},
      {"raidMinWorkers", nullptr, &Tuning::raidMinWorkers},
      {"raidEndWorkers", nullptr, &Tuning::raidEndWorkers},
      {"raidRange", &Tuning::raidRange, nullptr},
      {"wellDefendReach", &Tuning::wellDefendReach, nullptr},
      {"walkSpeed", &Tuning::walkSpeed, nullptr},
      {"wellInTimeS", &Tuning::wellInTimeS, nullptr},
      {"wellDefendHp", &Tuning::wellDefendHp, nullptr},
      {"spellRangeMargin", &Tuning::spellRangeMargin, nullptr},
      {"castRetryS", &Tuning::castRetryS, nullptr},
      {"hitRecentS", &Tuning::hitRecentS, nullptr},
      {"spellHurtHp", &Tuning::spellHurtHp, nullptr},
      {"lastStandHp", &Tuning::lastStandHp, nullptr},
      {"escapeRange", &Tuning::escapeRange, nullptr},
      {"blinkStep", &Tuning::blinkStep, nullptr},
      {"chaseSpellDist", &Tuning::chaseSpellDist, nullptr},
      {"exceedRange", &Tuning::exceedRange, nullptr},
      {"allyShieldHp", &Tuning::allyShieldHp, nullptr},
      {"potionHp", &Tuning::potionHp, nullptr},
      {"wardDetour", &Tuning::wardDetour, nullptr},
      {"wardCastMargin", &Tuning::wardCastMargin, nullptr},
      {"wardRetryS", &Tuning::wardRetryS, nullptr},
      {"parasiteFromS", &Tuning::parasiteFromS, nullptr},
      {"parasiteAllies", nullptr, &Tuning::parasiteAllies},
      {"parasiteHp", &Tuning::parasiteHp, nullptr},
      {"firstWaveWaitS", &Tuning::firstWaveWaitS, nullptr},
      {"parasiteGather", &Tuning::parasiteGather, nullptr},
      {"parasiteMaxS", &Tuning::parasiteMaxS, nullptr},
      {"parasiteRetryS", &Tuning::parasiteRetryS, nullptr},
      {"hybridFightRange", &Tuning::hybridFightRange, nullptr},
      {"hybridIdleS", &Tuning::hybridIdleS, nullptr},
      {"hybridHoldS", &Tuning::hybridHoldS, nullptr},
      {"hybridCalmRange", &Tuning::hybridCalmRange, nullptr},
      {"hybridCalmS", &Tuning::hybridCalmS, nullptr},
      {"hybridTaskBackoffS", &Tuning::hybridTaskBackoffS, nullptr},
      {"hybridPushBots", nullptr, &Tuning::hybridPushBots},
      {"groupHoldS", &Tuning::groupHoldS, nullptr},
      {"parasiteLead", nullptr, &Tuning::parasiteLead},
      {"idleMove", &Tuning::idleMove, nullptr},
      {"idleHitS", &Tuning::idleHitS, nullptr},
  };
  *n = static_cast<int>(sizeof k / sizeof k[0]);
  return k;
}

// `spec` = "key=value,key=value" (blanks and ';' also separate); unknown keys and bad numbers go into `bad` (comma
// separated) and change nothing. Returns how many values were set.
inline int ApplyOverrides(Tuning* t, const std::string& spec, std::string* bad = nullptr) {
  int n = 0, set = 0;
  const TuningField* fields = TuningFields(&n);
  std::size_t i = 0;
  while (i < spec.size()) {
    std::size_t j = spec.find_first_of(",; ", i);
    if (j == std::string::npos) j = spec.size();
    const std::string item = spec.substr(i, j - i);
    i = j + 1;
    if (item.empty()) continue;
    const std::size_t eq = item.find('=');
    bool ok = false;
    if (eq != std::string::npos && eq > 0 && eq + 1 < item.size()) {
      const std::string key = item.substr(0, eq), value = item.substr(eq + 1);
      char* end = nullptr;
      const double v = std::strtod(value.c_str(), &end);
      if (end && *end == '\0') {
        for (int k = 0; k < n; ++k) {
          if (key != fields[k].name) continue;
          if (fields[k].f) t->*(fields[k].f) = static_cast<float>(v);
          else t->*(fields[k].i) = static_cast<int>(v);
          ok = true;
          ++set;
          break;
        }
      }
    }
    if (!ok && bad) {
      if (!bad->empty()) *bad += ',';
      *bad += item;
    }
  }
  return set;
}

// The whole table as one line ("key=value ..."), for the host's log.
inline std::string Describe(const Tuning& t) {
  int n = 0;
  const TuningField* fields = TuningFields(&n);
  std::string s;
  char buf[64];
  for (int k = 0; k < n; ++k) {
    if (fields[k].f) std::snprintf(buf, sizeof buf, "%s=%g", fields[k].name, static_cast<double>(t.*(fields[k].f)));
    else std::snprintf(buf, sizeof buf, "%s=%d", fields[k].name, t.*(fields[k].i));
    if (!s.empty()) s += ' ';
    s += buf;
  }
  return s;
}

}  // namespace gms::bots2
