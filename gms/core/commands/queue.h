// D7c: the server command queue's rule for a queued move (ServerCommandQueue_TickPlayer 0x00923450,
// ServerCommandQueue.cpp in Dawngate.exe), engine-free and header-only, so gms_tests can check it without the game
// (docs/SERVER_MODE.md *A move during an attack order (D7c)*).
//
// The binary's rule, read from TickPlayer's queued-move step (0x009238EA-0x009239D0):
//   - a queued move waits while ServerCommandQueue_IsBusy(0) (0x00922540) says 1: a modifier or ability holds the agent
//     and doesn't let it move, the creature is movement-impaired, or an agent ability that doesn't allow movement and is
//     neither a basic attack (descriptor IsBasic, 0x00922370) nor selfInterruptible (static +0x3EA, 0x009221D0) holds
//     it; so a move does NOT wait for a basic attack or a walk-up;
//   - a stop moving (type 2) stops at once (ServerCommandQueue_ExecuteStop) and clears the current move;
//   - any other move runs at once (ServerCommandQueue_ExecuteMove 0x00922630: Locomotion_MoveToPoint and, unless the
//     agent ability allows movement, behavior-tree request 4, whose ClearRequests leaf aborts the walk-up and the
//     UseAbility leaf's basic attack) and becomes the current move,
//   - EXCEPT when the current move is an attack-move (type 9) engaged on a target (the current move's +0x20 byte, queue
//     +0xB0, set while an attack-move acquires a target, 0x00923647 and 0x009238C1): then the new move is not executed
//     but becomes the current move with that byte still set ("deferred"), and runs only once the creature stands still
//     and no ability holds the agent (IsBusy(1), the resume step 0x009236AF-0x00923727): after the walk-up and the
//     attack. The check reads the current move's type, not the new one's, so a second move (the current move is now a
//     plain move) runs at once.
// The host's D7c rule ([host] move_takeover 1): a plain move (type 1) that meets an engaged attack-move takes the
// general path, as the second move does: it runs at once and is not deferred. A new attack-move keeps the binary's
// exception (the engagement goes on, the new point after it).
#pragma once

#include <cstdint>
#include <cstdio>
#include <string>

namespace gms::cmdqueue {

// The command types the queue handles (the wire's, ActionCommandMsgs).
constexpr std::uint32_t kMove = 1;
constexpr std::uint32_t kStopMoving = 2;
constexpr std::uint32_t kAbility = 3;
constexpr std::uint32_t kStop = 8;
constexpr std::uint32_t kAttackMove = 9;
constexpr std::uint32_t kHold = 11;

// One of the queue's four 0x48-byte server commands, as far as the rules and the trace read it.
struct Command {
  bool valid = false;               // +0x00
  std::uint32_t type = 0;           // +0x04
  std::uint8_t seq = 0;             // +0x08
  std::uint32_t keepAttacking = 0;  // +0x24
  std::uint32_t target = 0;         // +0x2C
  std::int32_t slot = -1;           // +0x40
  std::uint32_t guid = 0;           // +0x44
};

// The 0x130-byte per-player queue (ServerCommandQueue_GetPlayerQueue 0x00923230).
struct QueueView {
  Command currentAbility;  // +0x00
  Command queuedAbility;   // +0x48: every type but 1, 2, 9
  Command currentMove;     // +0x90
  Command queuedMove;      // +0xD8: types 1, 2, 9
  // +0xB0, the current move's +0x20 byte: an attack-move engaged on a target, or a move paused behind an ability that
  // doesn't let the agent move (0x00923B67); such a move runs (again) once the creature stands still and no ability
  // holds the agent
  bool deferred = false;
  bool stopped = false;  // +0x128: no auto-acquire (stop, type 8)
  bool holding = false;  // +0x129: no chasing (hold, type 11)
  bool idle = false;     // +0x120 (u64 time stamp) set: the creature stood still at the last tick
};

// What TickPlayer does with the queued move.
enum class QueuedMoveStep {
  kNone,     // no queued move
  kWait,     // IsBusy(0) answered 1: it stays queued
  kStop,     // a stop moving: ExecuteStop now, the current move cleared
  kExecute,  // ExecuteMove now, then it is the current move
  kDefer,    // the attack-move exception: the current move, deferred, not executed
};

inline const char* StepName(QueuedMoveStep s) {
  switch (s) {
    case QueuedMoveStep::kNone: return "none";
    case QueuedMoveStep::kWait: return "wait";
    case QueuedMoveStep::kStop: return "stop";
    case QueuedMoveStep::kExecute: return "execute";
    case QueuedMoveStep::kDefer: return "defer";
  }
  return "?";
}

// The case D7c changes: a queued plain move while the current move is an attack-move engaged on a target.
inline bool IsMoveAgainstEngagedAttackMove(const QueueView& q) {
  return q.queuedMove.valid && q.queuedMove.type == kMove && q.currentMove.valid &&
         q.currentMove.type == kAttackMove && q.deferred;
}

// TickPlayer's step for the queued move, given IsBusy(0)'s answer (0 free, 1 busy; the binary only knows these two and
// drops the move on a 2, which IsBusy never returns). moveTakeover = the host's D7c rule; false = the binary's.
inline QueuedMoveStep StepQueuedMove(const QueueView& q, int isBusy, bool moveTakeover) {
  if (!q.queuedMove.valid) return QueuedMoveStep::kNone;
  if (isBusy != 0) return QueuedMoveStep::kWait;
  if (q.queuedMove.type == kStopMoving) return QueuedMoveStep::kStop;
  if (moveTakeover && IsMoveAgainstEngagedAttackMove(q)) return QueuedMoveStep::kExecute;
  if (q.currentMove.type == kAttackMove && q.currentMove.valid && q.deferred) return QueuedMoveStep::kDefer;
  return QueuedMoveStep::kExecute;
}

// The queue after that step. The binary copies the queued move into the current one with the old +0xB0 byte as its
// +0x20 (0x009239A3), clears the queued move, stopped and holding, and the current ability command (0x009239D0); a stop
// clears the current move and the byte. With the host's takeover the byte is cleared first: a move that ran at once is
// not deferred (else it would run a second time when the creature arrives).
inline QueueView AfterQueuedMove(const QueueView& q, QueuedMoveStep s, bool moveTakeover) {
  QueueView n = q;
  switch (s) {
    case QueuedMoveStep::kNone:
    case QueuedMoveStep::kWait:
      break;
    case QueuedMoveStep::kStop:
      n.currentMove.valid = false;
      n.deferred = false;
      n.queuedMove.valid = false;
      break;
    case QueuedMoveStep::kExecute:
    case QueuedMoveStep::kDefer: {
      const bool deferred = moveTakeover && IsMoveAgainstEngagedAttackMove(q) ? false : q.deferred;
      n.currentMove = q.queuedMove;
      n.deferred = deferred;
      n.queuedMove.valid = false;
      n.stopped = false;
      n.holding = false;
      n.currentAbility.valid = false;
      break;
    }
  }
  return n;
}

// The binary's resume of a deferred current move (0x009236AF-0x00923727): once the creature stood still (`idle`) and
// IsBusy(1) is 0 (no ability holds the agent, a basic attack included), a deferred plain move runs; a deferred
// attack-move runs on to its point only when no target is left (else it attacks the target, the auto-acquire).
inline bool DeferredMoveRuns(const QueueView& q, bool idle, int isBusyStrict, bool bestTarget) {
  if (!idle || !q.currentMove.valid || !q.deferred || isBusyStrict != 0) return false;
  if (q.currentMove.type == kMove) return true;
  if (q.currentMove.type == kAttackMove) return !bestTarget;
  return false;
}

inline std::string DescribeCommand(const Command& c) {
  if (!c.valid) return "-";
  char buf[96];
  int n = std::snprintf(buf, sizeof buf, "%u seq %u", static_cast<unsigned>(c.type), static_cast<unsigned>(c.seq));
  if (c.type != kMove && c.type != kStopMoving && c.type != kAttackMove && c.type != kStop && c.type != kHold) {
    if (c.slot != -1) n += std::snprintf(buf + n, sizeof buf - n, " slot %d", static_cast<int>(c.slot));
    else n += std::snprintf(buf + n, sizeof buf - n, " guid 0x%08X", static_cast<unsigned>(c.guid));
    if (c.target) n += std::snprintf(buf + n, sizeof buf - n, " -> %08X", static_cast<unsigned>(c.target));
    if (c.keepAttacking) std::snprintf(buf + n, sizeof buf - n, " keep");
  }
  return buf;
}

// One line for the host's queue trace ("queue:" lines in dgsim.log).
inline std::string Describe(const QueueView& q) {
  std::string s = "ability [" + DescribeCommand(q.currentAbility) + "] queued [" + DescribeCommand(q.queuedAbility) +
                  "]; move [" + DescribeCommand(q.currentMove) + "]" + (q.deferred ? " deferred" : "") + " queued [" +
                  DescribeCommand(q.queuedMove) + "]";
  if (q.idle) s += "; idle";
  if (q.stopped) s += "; stopped";
  if (q.holding) s += "; holding";
  return s;
}

inline bool SameCommand(const Command& a, const Command& b) {
  if (a.valid != b.valid) return false;
  if (!a.valid) return true;
  return a.type == b.type && a.seq == b.seq && a.keepAttacking == b.keepAttacking && a.target == b.target &&
         a.slot == b.slot && a.guid == b.guid;
}

inline bool SameState(const QueueView& a, const QueueView& b) {
  return SameCommand(a.currentAbility, b.currentAbility) && SameCommand(a.queuedAbility, b.queuedAbility) &&
         SameCommand(a.currentMove, b.currentMove) && SameCommand(a.queuedMove, b.queuedMove) &&
         a.deferred == b.deferred && a.stopped == b.stopped && a.holding == b.holding && a.idle == b.idle;
}

}  // namespace gms::cmdqueue
