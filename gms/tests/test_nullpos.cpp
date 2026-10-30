// NULLPOS (gms/core/agents/nullpos.h): the map position (0, 0) is the bots' "no position"; the request rule, the
// offset of a legit origin, the lines, and BACKTOACTION's point kept in the blackboard until the restart (lost.h).
#include <algorithm>
#include <cstring>
#include <string>
#include <vector>

#include "core/agents/brain.h"
#include "core/agents/lost.h"
#include "core/agents/nullpos.h"
#include "core/bots/lanes.h"
#include "tests/check.h"

using namespace gms::agents;

GMS_TEST(NullposTheHelper) {
  CHECK(IsNullPos(0.f, 0.f));
  CHECK(IsNullPos(-0.f, 0.f));  // -0.0f is the origin too
  CHECK(IsNullPos(nullptr));    // no point at all
  const float withHeight[3] = {0.f, 0.f, 3.5f};
  CHECK(IsNullPos(withHeight));  // the ground plane only: Follow walks to (0, 0, z) as well
  const float ward[3] = {78.f, 20.f, 0.f};
  CHECK(!IsNullPos(ward));
  CHECK(!IsNullPos(0.f, 1e-6f));
  CHECK(!IsNullPos(0.f, -1.f));  // the parasite's spawn since NULLPOS: a place
  CHECK(!IsNullPos(5.f, 0.f));   // one zero coordinate is not the origin
}

namespace {
RequestObject Obj(bool found, bool hasHp, float hp, bool pendingDelete, const char* noun) {
  RequestObject o;
  o.found = found;
  o.hasHp = hasHp;
  o.hp = hp;
  o.pendingDelete = pendingDelete;
  o.noun = noun;
  return o;
}
constexpr std::uint32_t kParasite = 0x7C810122;
const char* const kParasiteNoun = "Objective_SpiritMonster_State1.Noun";
}  // namespace

// The rule BotRequest applies to every request the host makes for a bot: a dead target first, then (0, 0) in any
// form; no offset, no exception.
GMS_TEST(NullposTheRequestRule) {
  const float origin[3] = {0.f, 0.f, 1.f};
  const float spot[3] = {78.f, 20.f, 1.f};
  const float parasitePos[3] = {0.f, -1.f, 1.f};
  const RequestObject alive = Obj(true, true, 4000.f, false, kParasiteNoun);
  CHECK(CheckRequest(0, origin, NullPosUse::kGoal) == RequestVerdict::kNullPos);
  CHECK(CheckRequest(0, nullptr, NullPosUse::kGoal) == RequestVerdict::kNullPos);
  // even the living parasite: nothing that lives stands on (0, 0); at (0, -1) it is a place
  CHECK(CheckRequest(kParasite, origin, NullPosUse::kGoal, alive) == RequestVerdict::kObjectAtOrigin);
  CHECK(CheckRequest(kParasite, parasitePos, NullPosUse::kGoal, alive) == RequestVerdict::kOk);
  // the bot's own position exactly on the origin: suspicious, refused
  CHECK(CheckRequest(0, origin, NullPosUse::kSelf) == RequestVerdict::kSelfAtOrigin);
  CHECK(CheckRequest(0, spot, NullPosUse::kSelf) == RequestVerdict::kOk);
  // DEADTARGET: any position, the object gone, pending delete or at 0 hp (round 2: the parasite in its death)
  CHECK(CheckRequest(kParasite, parasitePos, NullPosUse::kGoal, Obj(true, true, 0.f, false, kParasiteNoun)) ==
        RequestVerdict::kDeadTarget);
  CHECK(CheckRequest(kParasite, origin, NullPosUse::kGoal, Obj(true, true, 0.f, false, kParasiteNoun)) ==
        RequestVerdict::kDeadTarget);  // dead comes first
  CHECK(CheckRequest(0x7C7B0101, spot, NullPosUse::kGoal, Obj(false, false, 0.f, false, nullptr)) ==
        RequestVerdict::kDeadTarget);
  CHECK(CheckRequest(0x7C7B0101, spot, NullPosUse::kGoal, Obj(true, true, 300.f, true, "Minion.Noun")) ==
        RequestVerdict::kDeadTarget);
  CHECK(CheckRequest(0x7C7B0102, spot, NullPosUse::kGoal, Obj(true, false, 0.f, false, "Well.Noun")) ==
        RequestVerdict::kOk);  // no hit points: not dead
  CHECK(CheckRequest(kParasite, spot, NullPosUse::kGoal, alive) == RequestVerdict::kOk);
  // the refusal is a refusal: a prioritized list goes on with its next gambit
  const std::vector<int> others = {kResultItemRefused, kResultException};
  const int r = kResultNullPos;
  CHECK(std::find(others.begin(), others.end(), r) == others.end());
  CHECK(!GambitRefusalSkips(1, kSlotQ));  // a start is not a refusal; the NULLPOS refusal is
  CHECK(GambitRefusalSkips(r, kSlotQ));
}

// A zero goal is never handed out: of the goals the host computes, every one at (0, 0) is refused, whatever it names;
// an empty lane path's front (core/bots/lanes.h: the default vector) is such a goal.
GMS_TEST(NullposAZeroGoalIsNeverHandedOut) {
  gms::bots::LanePath empty;
  empty.lane = gms::bots::kLaneTop;
  const gms::bots::Front f = gms::bots::ComputeFront(empty, 1, {});
  CHECK(IsNullPos(f.center.x, f.center.y));  // LaneFrontOf / LaneCenterAndDirection must not pass it on
  struct Goal {
    std::uint32_t target;
    float pos[3];
    NullPosUse use;
    RequestObject object;
  };
  const std::vector<Goal> goals = {
      {0, {f.center.x, f.center.y, f.center.z}, NullPosUse::kGoal, {}},                        // that front
      {0, {0.f, 0.f, 0.f}, NullPosUse::kGoal, {}},                                              // a default vector
      {0, {-0.f, 0.f, 4.f}, NullPosUse::kGoal, {}},                                             // -0 with a height
      {0, {36.f, 6.f, 0.f}, NullPosUse::kGoal, {}},                                             // the rally point
      {kParasite, {0.f, -1.f, 0.f}, NullPosUse::kGoal, Obj(true, true, 4000.f, false, kParasiteNoun)},  // moved
      {kParasite, {0.f, 0.f, 0.f}, NullPosUse::kGoal, Obj(true, true, 4000.f, false, kParasiteNoun)},   // unmoved
      {0, {0.f, 0.f, 0.f}, NullPosUse::kSelf, {}},                                              // the bot there
  };
  int handedNull = 0, refused = 0, sent = 0;
  for (const Goal& g : goals) {
    if (CheckRequest(g.target, g.pos, g.use, g.object) != RequestVerdict::kOk) {
      ++refused;
      continue;
    }
    ++sent;
    if (IsNullPos(g.pos)) ++handedNull;  // what would reach the engine
  }
  CHECK_EQ(refused, 5);
  CHECK_EQ(sent, 2);
  CHECK_EQ(handedNull, 0);
}

// DEADTARGET-WORKER: a well capture marks its workers for delete with their hit points left, in the bot's think. The
// gambit whose conditions named none falls back to mTarget; a dead mTarget is none, so the request goes at the bot's
// own position (or the condition's point) and the send guard has nothing to catch.
GMS_TEST(NullposADeadMTargetIsNone) {
  constexpr std::uint32_t kWorker = 0x7C6B00C2;
  const RequestObject removed = Obj(true, true, 88.35f, true, "Titan_Node_Follower.Noun");  // the capture's worker
  const RequestObject working = Obj(true, true, 88.35f, false, "Titan_Node_Follower.Noun");
  CHECK_EQ(LiveTarget(kWorker, removed), 0u);
  CHECK_EQ(LiveTarget(kWorker, working), kWorker);
  CHECK_EQ(LiveTarget(kWorker, Obj(false, false, 0.f, false, nullptr)), 0u);         // gone
  CHECK_EQ(LiveTarget(kParasite, Obj(true, true, 0.f, false, kParasiteNoun)), 0u);  // hp 0
  CHECK_EQ(LiveTarget(0x7C7B0102, Obj(true, false, 0.f, false, "Well.Noun")), 0x7C7B0102u);  // no hit points
  CHECK_EQ(LiveTarget(0, removed), 0u);
  // the gambit's aim, then the request: before, the worker went to the guard (a DEADTARGET line); now no object
  const float self[3] = {-176.4f, 22.6f, 0.f};
  const GambitAim before = AimGambit(0, kWorker, false, false, true);
  CHECK_EQ(before.target, kWorker);
  CHECK(CheckRequest(before.target, self, NullPosUse::kGoal, removed) == RequestVerdict::kDeadTarget);
  const GambitAim now = AimGambit(0, LiveTarget(kWorker, removed), false, false, true);
  CHECK_EQ(now.target, 0u);
  CHECK(!now.atPoint);
  CHECK(CheckRequest(now.target, self, NullPosUse::kSelf) == RequestVerdict::kOk);
  // a condition's own living target still wins over mTarget, dead or not
  CHECK_EQ(AimGambit(0x7C6400CB, LiveTarget(kWorker, removed), false, false, true).target, 0x7C6400CBu);
}

GMS_TEST(NullposTheLineAndTheStats) {
  char line[512];
  FormatNullPos(line, sizeof line, "bot 7C7F0124 warden.Noun", NullPosWhere::kRequest, "back step",
                "ability 2378FAE9 slot -1, no object");
  const std::string s = line;
  CHECK(s.rfind("NULLPOS: bot 7C7F0124 warden.Noun back step: goal is the null position (0, 0) [request]", 0) == 0);
  CHECK(s.find("ability 2378FAE9") != std::string::npos && s.find("not sent") != std::string::npos);
  FormatNullPos(line, sizeof line, "lane 2 team 1", NullPosWhere::kLaneFront, "LaneFrontOf", "");
  CHECK(std::string(line) ==
        "NULLPOS: lane 2 team 1 LaneFrontOf: goal is the null position (0, 0) [lane front]; not handed out");
  FormatNullPos(line, sizeof line, "bot 7C7F0124 warden.Noun", NullPosWhere::kObjectAtOrigin, "gambit", "");
  CHECK(std::string(line).find("[object at origin]; not sent") != std::string::npos);
  FormatDeadTarget(line, sizeof line, "bot 7C7F0124 warden.Noun", "gambit", kParasite,
                   Obj(true, true, 0.f, false, kParasiteNoun), "ability 00000000 slot 0 at (0.0, -1.0)");
  CHECK(std::string(line) ==
        "DEADTARGET: bot 7C7F0124 warden.Noun gambit: 7C810122 Objective_SpiritMonster_State1.Noun alive 0 (hp 0); "
        "ability 00000000 slot 0 at (0.0, -1.0); not sent");
  CHECK(std::string(line).find("NULLPOS: ") == std::string::npos);  // counted apart
  NullPosCounts c;
  FormatNullPosStats(line, sizeof line, c, true);
  CHECK(std::string(line) ==
        "NULLPOS (strict on): 0; DEADTARGET 0; dead condition targets left out 0; dead mTargets left out 0; Follow "
        "guard: point reads 0");
  c.Note(NullPosWhere::kFollow);
  c.Note(NullPosWhere::kFollow);
  c.Note(NullPosWhere::kRequest);
  ++c.deadTargets;
  c.deadConditions = 3;
  c.deadMTargets = 2;
  c.followReads = 9;
  FormatNullPosStats(line, sizeof line, c, false);
  CHECK(std::string(line) ==
        "NULLPOS (strict off): 3 (FAIL in a test session), request 1, Follow 2; DEADTARGET 1 (FAIL in a test session); "
        "dead condition targets left out 3; dead mTargets left out 2; Follow guard: point reads 9");
  // the budget: strict logs every line, a player's match 200 and then every 50th
  CHECK(NullPosLogBudget(5000, true));
  CHECK(NullPosLogBudget(200, false) && !NullPosLogBudget(201, false) && NullPosLogBudget(250, false));
}


namespace {
LostInput At(unsigned long long ms, LostLeaf leaf, bool point = false, bool held = false) {
  LostInput in;
  in.nowMs = ms;
  in.x = 64.f;
  in.y = 8.f;
  in.leaf = leaf;
  in.pointPursuit = point;
  in.held = held;
  return in;
}
}  // namespace

// BACKTOACTION's fix without a window on (0, 0): EndPointPursuit leaves the point in +0x1E64; the next think clears it
// right before the restart, or as soon as the leaf has changed; a hold keeps it (the Follow walks on to the old spot).
GMS_TEST(NullposThePointStaysUntilTheRestart) {
  const LostOptions o;
  bool clear = true;
  LostState s;
  LostNotePointEnded(s, true);  // the ward started; the point stays
  CHECK(LostStep(s, At(1000, LostLeaf::kPursue), o, &clear) == LostAction::kRestartPointFollow);
  CHECK(clear);  // cleared before the restart: the tree redecides without it
  CHECK(!s.clearPending);
  CHECK(LostStep(s, At(1200, LostLeaf::kPursue), o, &clear) == LostAction::kNone && !clear);

  LostState c;  // the leaf changed first (the ward's UseAbility): cleared, no restart
  LostNotePointEnded(c, true);
  CHECK(LostStep(c, At(1000, LostLeaf::kOther), o, &clear) == LostAction::kNone);
  CHECK(clear);

  LostState h;  // held: neither restart nor clear; the next think without the hold does both
  LostNotePointEnded(h, true);
  CHECK(LostStep(h, At(1000, LostLeaf::kPursue, false, true), o, &clear) == LostAction::kNone && !clear);
  CHECK(h.clearPending);
  CHECK(LostStep(h, At(1200, LostLeaf::kPursue), o, &clear) == LostAction::kRestartPointFollow && clear);

  LostState n;  // a new point pursuit replaced the point: nothing to clear
  LostNotePointEnded(n, true);
  LostNotePointBegun(n);
  CHECK(LostStep(n, At(1000, LostLeaf::kPursue, true), o, &clear) == LostAction::kNone && !clear);

  LostState now;  // cleared at once (the fix off, or no think to come): nothing left to clear
  LostNotePointEnded(now);
  CHECK(LostStep(now, At(1000, LostLeaf::kPursue), o, &clear) == LostAction::kRestartPointFollow && !clear);

  LostOptions off;  // the fix off: EndPointPursuit cleared at once (the old path); LostStep never asks again
  off.pointFollowFix = false;
  LostState f;
  LostNotePointEnded(f, false);
  CHECK(LostStep(f, At(1000, LostLeaf::kPursue), off, &clear) == LostAction::kNone && !clear);
}
