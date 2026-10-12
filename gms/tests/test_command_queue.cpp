// D7c: a move during an attack order (core/commands/queue.h): the binary's queued-move step of
// ServerCommandQueue_TickPlayer and the host's takeover, engine-free (docs/SERVER_MODE.md *A move during an attack
// order (D7c)*).
#include <string>

#include "core/commands/gold.h"
#include "core/commands/queue.h"
#include "tests/check.h"

using namespace gms::cmdqueue;

namespace {

Command Cmd(std::uint32_t type, std::uint8_t seq) {
  Command c;
  c.valid = true;
  c.type = type;
  c.seq = seq;
  return c;
}

// The queue in an attack-move that has acquired a target: the current move is the attack-move (seq 11) with +0xB0
// set, the auto-acquire's basic attack (slot 0, keep attacking) is the current ability, and the player's move (seq
// 12) has just been queued.
QueueView EngagedAttackMoveThenMove() {
  QueueView q;
  q.currentMove = Cmd(kAttackMove, 11);
  q.deferred = true;
  q.currentAbility = Cmd(kAbility, 0);
  q.currentAbility.slot = 0;
  q.currentAbility.target = 0x7CA2017F;
  q.currentAbility.keepAttacking = 1;
  q.queuedMove = Cmd(kMove, 12);
  return q;
}

}  // namespace

// The user's report (2026-10-09): after an attack-move that engaged a minion, the first move waits for the walk-up and
// the attack (the binary defers it), a second move runs at once. D7c: the first one runs at once too.
GMS_TEST(CommandQueueFirstMoveAfterEngagedAttackMove) {
  const QueueView q = EngagedAttackMoveThenMove();
  CHECK(IsMoveAgainstEngagedAttackMove(q));
  // the binary: deferred, not executed; the new current move keeps the byte, so it waits for idle + no agent holder
  CHECK(StepQueuedMove(q, 0, false) == QueuedMoveStep::kDefer);
  const QueueView bin = AfterQueuedMove(q, QueuedMoveStep::kDefer, false);
  CHECK_EQ(bin.currentMove.type, kMove);
  CHECK_EQ(static_cast<unsigned>(bin.currentMove.seq), 12u);
  CHECK(bin.deferred);
  CHECK(!bin.queuedMove.valid);
  CHECK(!bin.currentAbility.valid);  // the current basic attack command is dropped from the queue either way
  // ... during the walk-up (moving) and while the basic attack holds the agent (IsBusy(1) = 1) it doesn't run
  CHECK(!DeferredMoveRuns(bin, false, 0, true));
  CHECK(!DeferredMoveRuns(bin, true, 1, true));
  // ... only once the shaper stands and the attack released the agent
  CHECK(DeferredMoveRuns(bin, true, 0, true));
  // the host (D7c): executed at once, not deferred
  CHECK(StepQueuedMove(q, 0, true) == QueuedMoveStep::kExecute);
  const QueueView host = AfterQueuedMove(q, QueuedMoveStep::kExecute, true);
  CHECK_EQ(host.currentMove.type, kMove);
  CHECK(!host.deferred);
  CHECK(!DeferredMoveRuns(host, true, 0, false));  // nothing to resume: it is not run a second time on arrival
}

// The second move: the current move is now the deferred plain move, so the exception (which reads the current move's
// type) doesn't apply: the binary executes it at once. The host changes nothing here.
GMS_TEST(CommandQueueSecondMoveRunsInBoth) {
  QueueView q = AfterQueuedMove(EngagedAttackMoveThenMove(), QueuedMoveStep::kDefer, false);
  q.queuedMove = Cmd(kMove, 13);
  CHECK(!IsMoveAgainstEngagedAttackMove(q));
  CHECK(StepQueuedMove(q, 0, false) == QueuedMoveStep::kExecute);
  CHECK(StepQueuedMove(q, 0, true) == QueuedMoveStep::kExecute);
  // the binary copies the old byte: the second move runs now and is marked deferred (it runs once more on arrival)
  CHECK(AfterQueuedMove(q, QueuedMoveStep::kExecute, false).deferred);
  CHECK(AfterQueuedMove(q, QueuedMoveStep::kExecute, true).deferred);
}

// A new attack-move during an engaged one keeps the binary's exception in both rules: the fight goes on, the new
// point after it.
GMS_TEST(CommandQueueAttackMoveAfterEngagedAttackMoveStaysDeferred) {
  QueueView q = EngagedAttackMoveThenMove();
  q.queuedMove = Cmd(kAttackMove, 12);
  CHECK(!IsMoveAgainstEngagedAttackMove(q));
  CHECK(StepQueuedMove(q, 0, false) == QueuedMoveStep::kDefer);
  CHECK(StepQueuedMove(q, 0, true) == QueuedMoveStep::kDefer);
  const QueueView n = AfterQueuedMove(q, QueuedMoveStep::kDefer, true);
  CHECK_EQ(n.currentMove.type, kAttackMove);
  CHECK(n.deferred);
  // it goes on to its point only when no target is left
  CHECK(!DeferredMoveRuns(n, true, 0, true));
  CHECK(DeferredMoveRuns(n, true, 0, false));
}

// The cases the binary already handles at once, unchanged by the host: an attack-move that hasn't engaged (no unit
// near: the user's "turns at once"), a right-click attack (no current attack-move), a plain move.
GMS_TEST(CommandQueueMoveRunsAtOnceWithoutAnEngagedAttackMove) {
  QueueView q = EngagedAttackMoveThenMove();
  q.deferred = false;  // attack-move, no target acquired
  CHECK(StepQueuedMove(q, 0, false) == QueuedMoveStep::kExecute);
  CHECK(StepQueuedMove(q, 0, true) == QueuedMoveStep::kExecute);
  QueueView r;  // a right-click basic attack on a far target: no current move, the attack walks up
  r.currentAbility = Cmd(kAbility, 20);
  r.currentAbility.slot = 0;
  r.currentAbility.keepAttacking = 1;
  r.queuedMove = Cmd(kMove, 21);
  CHECK(StepQueuedMove(r, 0, false) == QueuedMoveStep::kExecute);
  CHECK(StepQueuedMove(r, 0, true) == QueuedMoveStep::kExecute);
  // a right-click attack given during a move: the move is paused (+0xB0 by 0x00923B67), a new move runs at once
  QueueView p = r;
  p.currentMove = Cmd(kMove, 19);
  p.deferred = true;
  CHECK(StepQueuedMove(p, 0, false) == QueuedMoveStep::kExecute);
}

// What the host must not change: a move waits behind a cast that holds the agent (IsBusy(0) = 1), whatever the
// current move; a stop moving stops at once; no queued move, nothing.
GMS_TEST(CommandQueueWaitStopAndNothingUnchanged) {
  const QueueView q = EngagedAttackMoveThenMove();
  CHECK(StepQueuedMove(q, 1, false) == QueuedMoveStep::kWait);
  CHECK(StepQueuedMove(q, 1, true) == QueuedMoveStep::kWait);
  const QueueView w = AfterQueuedMove(q, QueuedMoveStep::kWait, true);
  CHECK(w.queuedMove.valid);
  CHECK(w.deferred);  // the attack-move stays engaged while the move waits
  QueueView s = q;
  s.queuedMove = Cmd(kStopMoving, 12);
  CHECK(StepQueuedMove(s, 0, false) == QueuedMoveStep::kStop);
  CHECK(StepQueuedMove(s, 0, true) == QueuedMoveStep::kStop);
  const QueueView after = AfterQueuedMove(s, QueuedMoveStep::kStop, true);
  CHECK(!after.currentMove.valid);
  CHECK(!after.deferred);
  QueueView none = q;
  none.queuedMove.valid = false;
  CHECK(StepQueuedMove(none, 0, true) == QueuedMoveStep::kNone);
}

// The trace line and its change test.
GMS_TEST(CommandQueueDescribe) {
  QueueView q = EngagedAttackMoveThenMove();
  q.idle = true;
  CHECK_EQ(Describe(q), std::string("ability [3 seq 0 slot 0 -> 7CA2017F keep] queued [-]; move [9 seq 11] deferred "
                                    "queued [1 seq 12]; idle"));
  QueueView g;
  g.queuedAbility = Cmd(kAbility, 5);
  g.queuedAbility.guid = 0x96B845C6;  // return to base
  g.stopped = true;
  CHECK_EQ(Describe(g), std::string("ability [-] queued [3 seq 5 guid 0x96B845C6]; move [-] queued [-]; stopped"));
  CHECK(SameState(q, q));
  QueueView d = q;
  d.deferred = false;
  CHECK(!SameState(q, d));
  d = q;
  d.queuedMove.seq = 13;
  CHECK(!SameState(q, d));
  d = q;
  d.queuedAbility.seq = 99;  // an invalid command's leftovers don't count
  CHECK(SameState(q, d));
  CHECK_EQ(std::string(StepName(QueuedMoveStep::kDefer)), std::string("defer"));
}

// D15b: the gold test aid in steps of at most 16 bits (CombatEvent.cpp 67).
GMS_TEST(CommandGoldSteps) {
  using gms::cmdgold::GoldGrantSteps;
  CHECK(GoldGrantSteps(0).empty());
  CHECK(GoldGrantSteps(-5).empty());
  auto one = GoldGrantSteps(50000);
  CHECK_EQ(one.size(), static_cast<size_t>(1));
  CHECK_EQ(one[0], 50000.0);
  auto big = GoldGrantSteps(250000);
  CHECK_EQ(big.size(), static_cast<size_t>(4));
  double sum = 0;
  for (double v : big) {
    CHECK(v <= gms::cmdgold::kMaxGoldDelta);
    sum += v;
  }
  CHECK_EQ(sum, 250000.0);
  CHECK_EQ(big[3], 250000.0 - 3 * 65535.0);
  CHECK_EQ(GoldGrantSteps(65535).size(), static_cast<size_t>(1));
  CHECK_EQ(GoldGrantSteps(65536).size(), static_cast<size_t>(2));
}
