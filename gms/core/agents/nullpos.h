// NULLPOS: the map position (0, 0) is the bots' "no position", a null pointer. Engine-free;
// agents.cpp, bots.cpp, director.cpp and the host's Lua (dgsim_bots_api.lua) use it wherever a position goes to a bot
// or comes back from one.
//
// Why: the engine itself uses the zero vector as "none". The blackboard's target position (+0x1E64) starts as the zero
// vector, and the Pursue leaf's scorer (BTNode_Pursue_Evaluate 0x008A8CA0) compares all three components with the zero
// vector (0x019BDC5C) to mean "no point"; the API's GetMissionDestination, GetLastSeenEnemyPosition and the camp
// conditions answer the zero vector for none. But a behavior that is already running doesn't ask: nBehavior_Follow
// re-reads the point on every pass and walks to it (BACKTOACTION: the Warden 208 s on the map's centre). So on the
// host side a goal at (0, 0) is never a place, always a missing one, and handing it to a bot is a bug that must be
// loud in testing (a "NULLPOS:" line, a count, a failed session) and harmless in a player's match (logged, the request
// refused or the walk ended, never a crash).
//
// The test is on the ground plane only: x and y exactly 0.0f (-0.0f too), any height. The scorer's own test also
// needs z == 0, but Follow walks to (0, 0, z) just the same, so the stricter rule covers both.
//
// No exceptions: nothing that lives stands on (0, 0). The parasite's creature marker is the map's origin, so the host
// spawns it at (0, -1) (director.cpp, [host] parasite_offset_y), and every (0, 0) a bot is given or walks to is a
// NULLPOS. A request at an object that is dead, pending delete or gone is a DEADTARGET, whatever its position.
#pragma once

#include <cstddef>
#include <cstdint>
#include <cstdio>

namespace gms::agents {

inline bool IsNullPos(float x, float y) { return x == 0.f && y == 0.f; }
// A missing point (nullptr) is null too.
inline bool IsNullPos(const float* p) { return !p || IsNullPos(p[0], p[1]); }

// The host's own request result for a request refused as a NULLPOS or a DEADTARGET (beside brain.h's -0x7FFE item
// refused and -0x7FFF exception): a refusal, so a prioritized list goes on with its next gambit (GambitRefusalSkips).
constexpr int kResultNullPos = -0x7FFD;

// How a request uses its position.
enum class NullPosUse {
  kGoal,  // a place: a walk (ability_Bot_MoveToTarget), a cast at a point, a pursuit of a point
  kSelf,  // the bot's own position (a self cast; a bot standing exactly on (0, 0) is suspicious: refused)
};

// What the host read about a request's object right before the request (agents.cpp BotRequest, fresh from the engine).
struct RequestObject {
  bool found = false;          // the id resolves to an object
  bool hasHp = false;          // it has combatant data
  float hp = 0.f;
  bool pendingDelete = false;  // object +0x55
  const char* noun = nullptr;
};
// Dead for a request: gone, pending delete, or hit points at or below 0 (an object without hit points, a well or an
// interactable, is not dead).
inline bool ObjectDead(const RequestObject& o) { return !o.found || o.pendingDelete || (o.hasHp && o.hp <= 0.f); }

enum class RequestVerdict {
  kOk,
  kDeadTarget,      // the request names an object that is dead, pending delete or gone: a DEADTARGET, not sent
  kNullPos,         // the null position as a goal with no object: a NULLPOS, not sent
  kObjectAtOrigin,  // the null position with an object: a NULLPOS of its own (nothing lives on (0, 0)), not sent
  kSelfAtOrigin,    // the bot's own position is exactly (0, 0): a NULLPOS, not sent
};

// The rule for every bot request the host makes (agents.cpp BotRequest). A dead target first (any position), then
// (0, 0) in any form: no offset, no exception.
inline RequestVerdict CheckRequest(std::uint32_t target, const float* pos, NullPosUse use,
                                   const RequestObject& object = RequestObject()) {
  if (target != 0 && ObjectDead(object)) return RequestVerdict::kDeadTarget;
  if (!IsNullPos(pos)) return RequestVerdict::kOk;
  if (target != 0) return RequestVerdict::kObjectAtOrigin;
  if (use == NullPosUse::kSelf) return RequestVerdict::kSelfAtOrigin;
  return RequestVerdict::kNullPos;
}

// The DEADTARGET line (the tests grep "DEADTARGET: "): `who` the bot, `where` the code, the object and why it is dead.
inline int FormatDeadTarget(char* buf, std::size_t size, const char* who, const char* where, std::uint32_t target,
                            const RequestObject& o, const char* detail) {
  const char* why = !o.found ? "gone" : o.pendingDelete ? "pending delete" : "hp 0";
  return std::snprintf(buf, size, "DEADTARGET: %s %s: %08X %s alive 0 (%s)%s%s; not sent", who ? who : "?",
                       where ? where : "?", target, o.noun ? o.noun : "?", why, detail && *detail ? "; " : "",
                       detail ? detail : "");
}


// The places a NULLPOS can come from (the "stats nullpos:" line counts each).
enum class NullPosWhere {
  kRequest,         // a host request (a walk, a cast) with the null position as its goal and no object: refused
  kPointPursuit,    // D6d: a point pursuit at the null position: not started
  kTargetPosition,  // the blackboard's target position held (0, 0) with a height: written back to the zero vector
  kCondition,       // a condition aimed a gambit at the null position with no object: the request falls back
  kLaneFront,       // a lane front at the null position (an empty lane path): not handed out
  kCamp,            // a creep camp at the null position (not the parasite's): left out
  kFollow,          // the engine: a running nBehavior_Follow without an object read the null position (Lua): ended
  kLaneCentre,      // the Lua shim's GetLaneCenterAndDirection for an unknown lane: the static top front instead
  kHiddenPursuit,   // the engine: an object follow of a hidden target whose last-seen position is (0, 0) (no record
                    // for the follower's team): the follower's default behavior restarted
  kObjectAtOrigin,  // a request at (0, 0) naming an object (nothing living stands there): not sent
  kSelfAtOrigin,    // a request at the bot's own position, which is exactly (0, 0): not sent
  kCount
};

inline const char* NullPosWhereName(NullPosWhere w) {
  switch (w) {
    case NullPosWhere::kRequest: return "request";
    case NullPosWhere::kPointPursuit: return "point pursuit";
    case NullPosWhere::kTargetPosition: return "target position";
    case NullPosWhere::kCondition: return "condition";
    case NullPosWhere::kLaneFront: return "lane front";
    case NullPosWhere::kCamp: return "camp";
    case NullPosWhere::kFollow: return "Follow";
    case NullPosWhere::kLaneCentre: return "lane centre";
    case NullPosWhere::kHiddenPursuit: return "hidden pursuit";
    case NullPosWhere::kObjectAtOrigin: return "object at origin";
    case NullPosWhere::kSelfAtOrigin: return "self at origin";
    default: return "?";
  }
}

// What a NULLPOS of that kind does to recover (the line says it).
inline const char* NullPosRecovery(NullPosWhere w) {
  switch (w) {
    case NullPosWhere::kRequest: return "not sent";
    case NullPosWhere::kPointPursuit: return "not pursued";
    case NullPosWhere::kTargetPosition: return "cleared to the zero vector";
    case NullPosWhere::kCondition: return "the request falls back to the object or the bot";
    case NullPosWhere::kLaneFront: return "not handed out";
    case NullPosWhere::kCamp: return "left out";
    case NullPosWhere::kFollow: return "the Follow ends (the bot's own position instead)";
    case NullPosWhere::kLaneCentre: return "the static top front instead";
    case NullPosWhere::kHiddenPursuit: return "the follower's default behavior restarted";
    case NullPosWhere::kObjectAtOrigin: return "not sent";
    case NullPosWhere::kSelfAtOrigin: return "not sent";
    default: return "?";
  }
}

struct NullPosCounts {
  unsigned by[static_cast<int>(NullPosWhere::kCount)] = {};
  unsigned total = 0;
  unsigned deadTargets = 0;     // DEADTARGET lines: host requests at a dead object (a test FAIL too)
  unsigned deadConditions = 0;  // a gambit condition named a dead object: left out at the source (not an error)
  unsigned followReads = 0;     // the Lua's Follow guard: passes of a running Follow on a point (its evidence)
  void Note(NullPosWhere w) {
    ++by[static_cast<int>(w)];
    ++total;
  }
};

// The one line per NULLPOS (the tests and the runner grep "NULLPOS: "): `who` is "bot 7C7F0124 warden.Noun" (or
// "lane 2 team 1" where no bot is at hand), `where` the code that found it, `detail` the rest (may be empty).
inline int FormatNullPos(char* buf, std::size_t size, const char* who, NullPosWhere kind, const char* where,
                         const char* detail) {
  return std::snprintf(buf, size, "NULLPOS: %s %s: goal is the null position (0, 0) [%s]%s%s; %s", who ? who : "?",
                       where ? where : "?", NullPosWhereName(kind), detail && *detail ? "; " : "",
                       detail ? detail : "", NullPosRecovery(kind));
}


// The "stats nullpos:" line's body: the NULLPOS lines by kind, then the DEADTARGET lines and the dead condition
// targets left out at the source.
inline int FormatNullPosStats(char* buf, std::size_t size, const NullPosCounts& c, bool strict) {
  int len = std::snprintf(buf, size, "NULLPOS (strict %s): %u%s", strict ? "on" : "off", c.total,
                          c.total ? " (FAIL in a test session)" : "");
  for (int i = 0; i < static_cast<int>(NullPosWhere::kCount); ++i) {
    if (!c.by[i] || len <= 0 || static_cast<std::size_t>(len) >= size) continue;
    len += std::snprintf(buf + len, size - static_cast<std::size_t>(len), ", %s %u",
                         NullPosWhereName(static_cast<NullPosWhere>(i)), c.by[i]);
  }
  if (len > 0 && static_cast<std::size_t>(len) < size)
    len += std::snprintf(buf + len, size - static_cast<std::size_t>(len),
                         "; DEADTARGET %u%s; dead condition targets left out %u; Follow guard: point reads %u",
                         c.deadTargets, c.deadTargets ? " (FAIL in a test session)" : "", c.deadConditions,
                         c.followReads);
  return len;
}


// The log budget for NULLPOS lines: strict logs every one; otherwise 200, then every 50th (a player's long match).
inline bool NullPosLogBudget(unsigned lineNumber, bool strict) {
  return strict || lineNumber <= 200 || lineNumber % 50 == 0;
}

}  // namespace gms::agents
