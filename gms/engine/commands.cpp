// Command intake on the server (D7-live): see commands.h.
#include "commands.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "core/commands/queue.h"  // D7c: the queued-move step and the host's takeover
#include "core/protocol/commands.h"
#include "core/protocol/wire.h"
#include "core/session/pump.h"  // Q3c: the arrival of the message in hand
#include "agents.h"
#include "banter.h"
#include "bots.h"
#include "death.h"
#include "game.h"
#include "hook.h"
#include "log.h"
#include "perf.h"
#include "replicate.h"

using namespace game;

namespace cmds {
namespace {

constexpr unsigned kMaxPlayers = 10;
constexpr int kGoldSourceCheat = 13;  // nGoldSourceType.Cheat (GlobalDefinitions.lua; death::GrantGold takes Lua's)

Options g_opt;
bool g_installed;
void* g_sim;  // the server simulator the stub detour serves (set by every entry point)
std::vector<Answer> g_answers;
int g_failedSeq = -1;  // the sequence GameSimulator_SendActionCommandFailed was called with, while it runs
SendActionCommandFailedFn g_origFailed;

struct Stats {
  unsigned received = 0, parseErrors = 0, queued = 0, notConverted = 0, queueFailed = 0;
  unsigned byType[12] = {};
  unsigned rankUps = 0, rankUpsRefused = 0, items = 0, other = 0;
  unsigned ticks = 0, tickErrors = 0, btTicks = 0;
  unsigned stubMessages = 0, stubForeign = 0, stubUnreadable = 0;
  unsigned started = 0, failed = 0, ended = 0, failedCalls = 0;
  unsigned locoUpdates = 0, pings = 0, fowUpdates = 0;
  unsigned itemsDone[6] = {};     // D7b: carried out, by transaction type 1-5
  unsigned itemsRefused[6] = {};  // refused (the answer says success 0)
  unsigned itemsMismatched = 0;   // D15: reported done by the binary, but nothing changed (answered as refused)
  unsigned goldGrants = 0, reports = 0;  // D15: the commands file's "gold" and "report" lines
  unsigned dances = 0, feedbacks = 0, feedbackFailed = 0, interacts = 0;
  // D7c: plain moves that met an engaged attack-move (taken over / deferred by the binary's rule with move_takeover 0),
  // attack-moves deferred behind an engaged one (the binary's rule in both), the queue trace's lines (and the ones past
  // its limit)
  unsigned takeovers = 0, moveDeferrals = 0, attackMoveDeferrals = 0, decideErrors = 0;
  unsigned queueLines = 0, queueLinesDropped = 0;
};
Stats g_st;
unsigned g_logLines;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 50 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in commands (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// ---- the server player and its creature -------------------------------------------------------------------------

struct PlayerInfo {
  void* player = nullptr;
  std::uint32_t creature = 0;  // mCurrentCreatureId
  void* object = nullptr;      // the creature's cGameObject
  std::uint8_t team = 0;
  unsigned points = 0, level = 0;
  unsigned ranks[4] = {};  // slots 3-6
};

bool SehReadPlayer(void* sim, unsigned index, PlayerInfo* out) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    out->player = p;
    out->creature = Field<std::uint32_t>(p, player::kCurrentCreature);
    out->object = out->creature ? GetObjectById(out->creature) : nullptr;
    out->team = Field<std::uint8_t>(p, player::kTeam);
    out->points = Field<unsigned>(p, player::kAbilityPoints);
    out->level = Field<unsigned>(p, player::kLevel);
    for (int s = 0; s < 4; ++s) out->ranks[s] = Field<unsigned>(p, player::kAbilityRanks + (3 + s) * 4);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadPlayer")) {
    return false;
  }
}

struct MoveInfo {
  float pos[3] = {};
  float goal[3] = {};
  unsigned goalFlags = 0;
  unsigned short block = 0, xy = 0;
  unsigned char sendFlag = 0;  // locomotion +0x8E: the server's tick flagged a position to send
};

bool SehReadMove(void* object, MoveInfo* out) {
  __try {
    std::memcpy(out->pos, static_cast<char*>(object) + gobj::kPosition, sizeof out->pos);
    void* l = Field<void*>(object, loco::kOfObject);
    if (!l) return false;
    std::memcpy(out->goal, static_cast<char*>(l) + loco::kGoal, sizeof out->goal);
    out->goalFlags = Field<unsigned>(l, loco::kGoalFlags);
    out->block = Field<unsigned short>(l, loco::kClientPathBlock);
    out->xy = Field<unsigned short>(l, loco::kClientPathXY);
    out->sendFlag = Field<unsigned char>(l, 0x8E);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadMove")) {
    return false;
  }
}

std::uint16_t SehKnownMask(void* object) {
  __try {
    void* net = Field<void*>(object, gobj::kNetwork);
    return net ? Field<std::uint16_t>(net, kNetworkKnownMask) : 0;
  } __except (SehNote(GetExceptionInformation(), "KnownMask")) {
    return 0;
  }
}

// ---- the server command --------------------------------------------------------------------------------------------

// ServerCommand_Init, the fields of the proposal's conversion (SERVER_MODE.md *Proposal for gms/*), then
// ServerCommandQueue_QueueCommand(index, &cmd, tickNow 1). `rank` gets what went into +0x28.
bool SehQueue(unsigned index, void* player, const gms::ActionCommand& c, int* rank) {
  __try {
    alignas(8) unsigned char cmd[kServerCommandSize];
    std::memset(cmd, 0, sizeof cmd);
    ServerCommandInit(cmd);
    Field<std::uint32_t>(cmd, scmd::kType) = c.type;
    Field<std::uint8_t>(cmd, scmd::kSeq) = c.seq;
    Field<std::uint32_t>(cmd, scmd::kCaster) = c.caster;
    *rank = Field<int>(cmd, scmd::kRank);
    if (c.type == gms::kCmdMove || c.type == gms::kCmdAttackMove) {
      std::memcpy(cmd + scmd::kPosition, &c.pos, 12);
      Field<std::uint32_t>(cmd, scmd::kGoalFlags) = c.moveFlags;
    } else if (c.type == gms::kCmdAbility) {
      Field<std::uint32_t>(cmd, scmd::kTarget) = c.target;
      std::memcpy(cmd + scmd::kTargetPos, &c.targetPos, 12);
      std::memcpy(cmd + scmd::kPosition, &c.aim, 12);  // [assumed: nothing in the server reads +0x10 for abilities]
      if (c.slot != -1) ServerCommandSetSlot(cmd, c.slot);
      else ServerCommandSetGuid(cmd, static_cast<int>(c.guid));
      Field<std::uint32_t>(cmd, scmd::kKeepAttacking) = c.keepAttacking;
      Field<int>(cmd, scmd::kRank) = *rank = GetAbilityRank(player, c.slot);  // as the auto-attack does [assumed]
      Field<std::uint8_t>(cmd, scmd::kWaitUntilFree) = 0;  // [unclear; the server decides the walk-up itself]
    }
    QueueCommand(index, cmd, 1);
    return true;
  } __except (SehNote(GetExceptionInformation(), "QueueCommand")) {
    return false;
  }
}

bool SehRankUp(void* player, int slot) {
  __try {
    RankUpAbility(player, slot);
    return true;
  } __except (SehNote(GetExceptionInformation(), "RankUpAbility")) {
    return false;
  }
}

bool SehGrantXp(void* player, float amount) {
  __try {
    GrantXp(player, amount, 0.f, false, 10u + kXpSourceOffset, 0, 0);  // nXPSourceType.Cheat
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_GrantXPIfValid")) {
    return false;
  }
}

bool SehGrantPoints(void* player, unsigned points) {
  __try {
    Field<unsigned>(player, player::kAbilityPoints) = points;
    return true;
  } __except (SehNote(GetExceptionInformation(), "GrantPoints")) {
    return false;
  }
}

float SehDt(void* sim) {
  __try {
    return GetDt(sim);
  } __except (SehNote(GetExceptionInformation(), "GetDeltaSeconds")) {
    return 0.0f;
  }
}

// ---- D7c: the command queue's state and the move takeover -------------------------------------------------------------

void* g_queues[kMaxPlayers];  // each human player's queue as the last tick saw it (the stub's queue -> player index)

int IndexOfQueue(const void* queue) {
  for (unsigned i = 0; i < kMaxPlayers; ++i)
    if (g_queues[i] == queue) return static_cast<int>(i);
  return -1;
}

void ReadCommand(void* q, std::uintptr_t at, gms::cmdqueue::Command* c) {
  char* p = static_cast<char*>(q) + at;
  c->valid = Field<std::uint8_t>(p, scmd::kValid) != 0;
  c->type = Field<std::uint32_t>(p, scmd::kType);
  c->seq = Field<std::uint8_t>(p, scmd::kSeq);
  c->keepAttacking = Field<std::uint32_t>(p, scmd::kKeepAttacking);
  c->target = Field<std::uint32_t>(p, scmd::kTarget);
  c->slot = Field<std::int32_t>(p, scmd::kSlot);
  c->guid = Field<std::uint32_t>(p, scmd::kGuid);
}

bool SehReadQueue(void* q, gms::cmdqueue::QueueView* v) {
  __try {
    ReadCommand(q, pqueue::kCurrentAbility, &v->currentAbility);
    ReadCommand(q, pqueue::kQueuedAbility, &v->queuedAbility);
    ReadCommand(q, pqueue::kCurrentMove, &v->currentMove);
    ReadCommand(q, pqueue::kQueuedMove, &v->queuedMove);
    v->deferred = Field<std::uint8_t>(q, pqueue::kDeferred) != 0;
    v->stopped = Field<std::uint8_t>(q, pqueue::kStopped) != 0;
    v->holding = Field<std::uint8_t>(q, pqueue::kHolding) != 0;
    v->idle = Field<std::uint64_t>(q, pqueue::kIdleStamp) != 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadQueue")) {
    return false;
  }
}

// The ability holding the player's creature (AbilityDataContainer_GetAgentAbility), for the trace.
struct AgentView {
  std::uint32_t id = 0;
  bool found = false, holds = false, requiresAgent = false, allowsMovement = false, selfInterruptible = false;
  char name[64] = "";
};

bool SehAgentView(std::uint32_t creature, AgentView* a) {
  __try {
    a->id = creature ? GetAgentAbility(creature) : 0;
    void* in = a->id ? GetInstance(a->id) : nullptr;
    if (!in) return true;
    a->found = true;
    a->holds = Field<std::uint8_t>(in, inst::kHoldsAgent) != 0;
    void* st = Field<void*>(in, inst::kStatic);
    if (st) {
      const char* n = *reinterpret_cast<const char**>(st);
      if (n) strncpy_s(a->name, n, _TRUNCATE);
      a->requiresAgent = Field<std::uint8_t>(st, kStaticRequiresAgent) != 0;
      a->allowsMovement = Field<std::uint8_t>(st, kStaticAllowsMovement) != 0;
      a->selfInterruptible = Field<std::uint8_t>(st, kStaticSelfInterruptible) != 0;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "AgentView")) {
    return false;
  }
}

struct QueueTrace {
  bool seen = false;
  gms::cmdqueue::QueueView view;
  std::uint32_t agent = 0;
  bool holds = false;
  unsigned goalFlags = 0;
};
QueueTrace g_queueTrace[kMaxPlayers];

// A "queue:" line when the player's queue, its agent ability or its locomotion goal flags changed since the last one.
void TraceQueue(unsigned index, std::uint32_t creature, void* object, const char* when) {
  if (!g_opt.queueTrace || index >= kMaxPlayers || !g_queues[index]) return;
  gms::cmdqueue::QueueView v;
  if (!SehReadQueue(g_queues[index], &v)) return;
  AgentView a;
  SehAgentView(creature, &a);
  MoveInfo mv;
  const bool haveMove = object && SehReadMove(object, &mv);
  QueueTrace& t = g_queueTrace[index];
  if (t.seen && gms::cmdqueue::SameState(t.view, v) && t.agent == a.id && t.holds == a.holds &&
      t.goalFlags == (haveMove ? mv.goalFlags : 0))
    return;
  t.seen = true;
  t.view = v;
  t.agent = a.id;
  t.holds = a.holds;
  t.goalFlags = haveMove ? mv.goalFlags : 0;
  if (g_st.queueLines >= static_cast<unsigned>(g_opt.queueLogLimit)) {
    ++g_st.queueLinesDropped;
    return;
  }
  ++g_st.queueLines;
  char agent[160];
  if (a.found)
    std::snprintf(agent, sizeof agent, "%s %08X%s (requiresAgent %d, allowsMovement %d, selfInterruptible %d)", a.name,
                  a.id, a.holds ? " holds the agent" : "", a.requiresAgent, a.allowsMovement, a.selfInterruptible);
  else
    std::snprintf(agent, sizeof agent, "none");
  Log("queue: player %u %s: %s; agent ability %s; locomotion flags 0x%x at (%.2f, %.2f)", index, when,
      gms::cmdqueue::Describe(v).c_str(), agent, haveMove ? mv.goalFlags : 0, haveMove ? mv.pos[0] : 0.f,
      haveMove ? mv.pos[1] : 0.f);
}

void* SehPlayerQueue(unsigned index) {
  __try {
    return GetPlayerQueue(index);
  } __except (SehNote(GetExceptionInformation(), "GetPlayerQueue")) {
    return nullptr;
  }
}

bool SehClearDeferred(void* queue) {
  __try {
    Field<std::uint8_t>(queue, pqueue::kDeferred) = 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "DecideQueuedMove")) {
    return false;
  }
}

// Called by MoveTakeoverStub from inside ServerCommandQueue_TickPlayer, at the queued-move step once IsBusy(0) said free
// and the move is not a stop moving: 1 = run the queued move now (ExecuteMove at 0x0092398F), 0 = the binary's check.
// The engine thread, the server simulator bound (TickPlayer runs only from the host's tick and QueueCommand).
int DecideQueuedMove(void* queue) {
  gms::cmdqueue::QueueView v;
  if (!SehReadQueue(queue, &v)) {
    ++g_st.decideErrors;
    return 0;
  }
  const bool moveMeetsEngaged = gms::cmdqueue::IsMoveAgainstEngagedAttackMove(v);
  const bool attackMoveMeetsEngaged = v.queuedMove.valid && v.queuedMove.type == gms::cmdqueue::kAttackMove &&
                                      v.currentMove.valid && v.currentMove.type == gms::cmdqueue::kAttackMove &&
                                      v.deferred;
  if (!moveMeetsEngaged && !attackMoveMeetsEngaged) return 0;
  const int index = IndexOfQueue(queue);
  if (attackMoveMeetsEngaged) {
    ++g_st.attackMoveDeferrals;
    if (LogBudget())
      Log("commands: player %d: attack-move seq %u meets the attack-move seq %u engaged on a target: deferred behind "
          "the fight (the binary's rule)",
          index, v.queuedMove.seq, v.currentMove.seq);
    return 0;
  }
  if (!g_opt.moveTakeover) {
    ++g_st.moveDeferrals;
    Log("commands: player %d: move seq %u meets the attack-move seq %u engaged on a target: deferred until the "
        "walk-up and the attack are over (the binary's rule, [host] move_takeover 0)",
        index, v.queuedMove.seq, v.currentMove.seq);
    return 0;
  }
  if (!SehClearDeferred(queue)) {  // a move that runs now is not deferred (core/commands/queue.h)
    ++g_st.decideErrors;
    return 0;
  }
  ++g_st.takeovers;
  Log("commands: player %d: move seq %u takes over the attack-move seq %u engaged on a target at once (D7c, [host] "
      "move_takeover 1; current ability [%s])",
      index, v.queuedMove.seq, v.currentMove.seq, gms::cmdqueue::DescribeCommand(v.currentAbility).c_str());
  return 1;
}

int g_decideAnswer;  // DecideQueuedMove's answer across the stub's popad

// Jumped to from 0x0092396D (TickPlayer's queued-move step; the 16 bytes there are game.h's kQueuedMoveBranchBytes):
// does the replaced `mov eax,[esp+27Ch]` (the queue), asks DecideQueuedMove with every register kept, then either runs
// the replaced `cmp dword [eax+94h],9; jne 0092398F` and goes on at 0x0092397D as the binary does, or goes straight to
// the ExecuteMove at 0x0092398F. Reached by a jump, so ESP is TickPlayer's own; ECX, EDX and the flags are dead at both
// targets (the next instructions set them), and push/ret jumps without a register.
__declspec(naked) void MoveTakeoverStub() {
  __asm {
    mov eax, dword ptr [esp + 0x27C]
    pushad
    push eax
    call DecideQueuedMove
    add esp, 4
    mov g_decideAnswer, eax
    popad
    cmp g_decideAnswer, 0
    jne execute
    cmp dword ptr [eax + 0x94], 9
    jne execute
    push 0x0092397D
    ret
  execute:
    push 0x0092398F
    ret
  }
}

// TickPlayer for one player; the creature's behavior tree after it (AgentBehaviorTree_Tick(object, dt), whose per-frame
// caller is stripped, docs/AGENT_AI.md). -1 on an exception.
int SehTickPlayer(unsigned index, void* player, void* object, bool bt, float dt) {
  __try {
    void* q = GetPlayerQueue(index);
    if (!q) return 0;
    g_queues[index] = q;
    TickPlayerQueue(player, q);
    if (bt && object && Field<void*>(object, obj::kBehaviorTree)) {
      if (agents::Installed()) return agents::TickTree(object, dt) ? 2 : 1;  // D1b-live: once per frame
      AgentTickDt(object, dt);
      return 2;
    }
    return 1;
  } __except (SehNote(GetExceptionInformation(), "TickPlayer")) {
    return -1;
  }
}

// ---- answers ------------------------------------------------------------------------------------------------------

void Push(std::uint16_t players, gms::Bytes msg, std::string what) {
  Answer a;
  // B15b: the spectators' bits 10-14 stay: the server's own sends address them ((1 << index) | 0x7C00 for the action
  // command responses, which a spectator's agent summary shows for the shaper it watches)
  a.players = static_cast<std::uint16_t>(players & 0x7FFF);
  a.msg = std::move(msg);
  a.what = std::move(what);
  g_answers.push_back(std::move(a));
}

void SendFowUpdate(void* sim, unsigned index, const char* why) {
  if (!g_opt.fowUpdates) return;  // D2g-live: the replication's delta pass sends what changed
  gms::Bytes m;
  if (!repl::PlayerFowUpdate(sim, index, {4, 5, 9, 10, 11, 12}, &m)) {
    Log("commands: no FoW player update for player %u (%s)", index, why);
    return;
  }
  ++g_st.fowUpdates;
  Push(static_cast<std::uint16_t>(1u << index), m,
       std::string("FoW player update from the server (mLevel, mAbilityPoints, mAbilityRankMap): ") + why);
}

std::uint16_t TeamMask(void* sim, std::uint8_t team) {
  std::uint16_t mask = 0;
  for (unsigned j = 0; j < kMaxPlayers; ++j) {
    PlayerInfo pj;
    if (SehReadPlayer(sim, j, &pj) && pj.team == team) mask = static_cast<std::uint16_t>(mask | (1u << j));
  }
  return mask;
}

// ---- item transactions (D7b) ----------------------------------------------------------------------------------------

// The player's wallet and inventory, for the log.
struct Wallet {
  float gold = 0, total = 0;
  unsigned spellPoints = 0;
  std::uint32_t item[player::kItemSlots] = {};  // asset id per slot (0 = empty)
  int count[player::kItemSlots] = {};
};

Wallet SehWallet(void* player) {
  Wallet w;
  __try {
    w.gold = Field<float>(player, player::kAvailableGold);
    w.total = Field<float>(player, player::kTotalGold);
    w.spellPoints = Field<unsigned>(player, player::kSpellPoints);
    for (unsigned s = 0; s < player::kItemSlots; ++s) {
      char* item = static_cast<char*>(player) + player::kItems + s * player::kItemSize;
      void* asset = Field<void*>(item, 0);
      w.item[s] = asset ? Field<std::uint32_t>(asset, asset::kId) : 0;
      w.count[s] = Field<int>(item, player::kItemCount);
    }
  } __except (SehNote(GetExceptionInformation(), "Wallet")) {
  }
  return w;
}

// D15: the wallet as the engine-free check sees it (gms::ItemTransactionTookEffect).
gms::InventoryState StateOf(const Wallet& w) {
  gms::InventoryState s;
  s.gold = w.gold;
  s.spellPoints = w.spellPoints;
  for (int i = 0; i < gms::kInventorySlots && i < static_cast<int>(player::kItemSlots); ++i) {
    s.item[i] = w.item[i];
    s.count[i] = w.count[i];
  }
  return s;
}

// "slot 0: 0x.. x1 -> 0x.. x2; ..." for every slot that changed, or "inventory unchanged".
std::string InventoryChanges(const Wallet& a, const Wallet& b) {
  std::string s;
  for (unsigned i = 0; i < player::kItemSlots; ++i) {
    if (a.item[i] == b.item[i] && a.count[i] == b.count[i]) continue;
    char buf[96];
    std::snprintf(buf, sizeof buf, "%sslot %u: 0x%08X x%d -> 0x%08X x%d", s.empty() ? "" : ", ", i, a.item[i],
                  a.count[i], b.item[i], b.count[i]);
    s += buf;
  }
  return s.empty() ? "inventory unchanged" : s;
}

// The creature's attributes an item changes most visibly (nAttributeType 0 Power, 2 MaxHealth, 4 HealthRegen, 16
// Haste), for the log.
struct Attrs {
  float v[4] = {-1, -1, -1, -1};
};
Attrs SehAttrs(void* object) {
  Attrs a;
  __try {
    void* data = object ? Field<void*>(object, gobj::kAttributes) : nullptr;
    if (!data) return a;
    const int idx[4] = {0, 2, 4, 16};
    for (int i = 0; i < 4; ++i) a.v[i] = GetAttribute(data, idx[i], 1);
  } __except (SehNote(GetExceptionInformation(), "Attributes")) {
  }
  return a;
}

// Where the player stands relative to its shop (LabsPlayer_InProximityToItemShop's test, recomputed for the log).
struct ShopReach {
  bool haveBase = false, dead = false;
  float distance = -1, reach = 0;
  float basePos[3] = {};
};

ShopReach SehShopReach(void* sim, const PlayerInfo& pi) {
  ShopReach r;
  __try {
    if (!pi.object) return r;
    void* combatant = Field<void*>(pi.object, combatant::kOfObject);
    r.dead = combatant && Field<float>(combatant, combatant::kHitPoints) <= 0.0f;
    if (pi.team >= 3) return r;
    void* base = Field<void*>(sim, kSimBaseObjects + pi.team * 4u);
    if (!base) return r;
    r.haveBase = true;
    std::memcpy(r.basePos, static_cast<char*>(base) + gobj::kPosition, sizeof r.basePos);
    const float* p = reinterpret_cast<const float*>(static_cast<char*>(pi.object) + gobj::kPosition);
    const float dx = p[0] - r.basePos[0], dy = p[1] - r.basePos[1], dz = p[2] - r.basePos[2];
    r.distance = std::sqrt(dx * dx + dy * dy + dz * dz);
    r.reach = FootprintRadius(pi.object) + *reinterpret_cast<const float*>(kItemShopReach);
  } __except (SehNote(GetExceptionInformation(), "ShopReach")) {
  }
  return r;
}

int SehInProximity(void* player) {
  __try {
    return InProximityToItemShop(player) ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "InProximityToItemShop")) {
    return -1;
  }
}

const char* AssetName(void* a) {
  __try {
    const char* n = a ? Field<const char*>(a, asset::kName) : nullptr;
    return n ? n : "?";
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

struct ItemOutcome {
  bool ran = false;      // the switch ran without an exception
  bool success = false;  // the transaction was carried out
  void* asset = nullptr;
  void* asset2 = nullptr;
  std::uint8_t slot = 0;  // the slot after LabsPlayer_CheckPurchase (purchase, consume), else the request's
  int check = -1;         // LabsPlayer_CheckPurchase's result (purchase, consume)
  int cost = 0;
  int shop = -1;          // LabsPlayer_CanUseItemShop (purchase, sell)
  int spell = -1;         // Item_IsSpell (purchase)
  const char* why = "";   // the refusal
};

// GameSimulator_OnItemTransaction's switch (0x008F1390), case for case, on the server's player.
void SehItemSwitch(void* player, const gms::ItemTransactionRequest& r, ItemOutcome* o) {
  __try {
    o->asset = FindAssetById(r.item);
    o->asset2 = r.item2 ? FindAssetById(r.item2) : nullptr;
    o->slot = r.slot;
    if (!o->asset && r.type != gms::kItemSwap) {
      o->why = "no asset with that id (the binary asserts \"pItemAsset || ... SwapItems\")";
      o->ran = true;
      return;
    }
    std::uint8_t slot = r.slot;
    int cost = 0;
    switch (r.type) {
      case gms::kItemPurchase:
        o->shop = CanUseItemShop(player) ? 1 : 0;
        if (!o->shop) o->spell = ItemIsSpell(o->asset) ? 1 : 0;
        if (!o->shop && !o->spell) {
          o->why = "not at the shop (LabsPlayer_CanUseItemShop: alive and out of reach)";
          break;
        }
        o->check = CheckPurchase(player, o->asset, &slot, &cost);
        if (o->check == 1 || o->check == 2) {
          PurchaseItem(player, o->asset, slot, cost);
          o->success = true;
        } else {
          o->why = o->check == 3 ? "LabsPlayer_CheckPurchase 3 (consume on purchase: type 4)"
                                 : "LabsPlayer_CheckPurchase 0 (gold, slot or a free slot)";
        }
        break;
      case gms::kItemSell:
        o->shop = CanUseItemShop(player) ? 1 : 0;
        if (!o->shop) {
          o->why = "not at the shop (LabsPlayer_CanUseItemShop)";
        } else if (!CanSellItem(player, o->asset, r.slot)) {
          o->why = "LabsPlayer_CanSellItem refused (the slot doesn't hold the item)";
        } else {
          SellItem(player, o->asset, r.slot);
          o->success = true;
        }
        break;
      case gms::kItemUse:
        if (!CanUseItem(player, o->asset, r.slot)) {
          o->why = "LabsPlayer_CanUseItem refused (dead, suppressed, not in the slot or not usable)";
        } else {
          UseItem(player, o->asset, r.slot);
          o->success = true;
        }
        break;
      case gms::kItemConsume:
        o->check = CheckPurchase(player, o->asset, &slot, &cost);
        if (o->check == 3) {
          ConsumeItem(player, o->asset, cost);
          o->success = true;
        } else {
          o->why = "LabsPlayer_CheckPurchase is not 3 (consume)";
        }
        break;
      case gms::kItemSwap:
        if (!CanSwapItems(player, r.slot, r.slot2)) {
          o->why = "LabsPlayer_CanSwapItems refused (both slots must be 6-8)";
        } else {
          SwapItems(player, o->asset, r.slot, o->asset2, r.slot2);
          o->success = true;
        }
        break;
      default:
        o->why = "unknown transaction type (the binary asserts \"invalid transaction type\")";
        break;
    }
    o->slot = slot;
    o->cost = cost;
    o->ran = true;
  } __except (SehNote(GetExceptionInformation(), "ItemTransaction")) {
    o->ran = false;
  }
}

void OnItemTransaction(void* sim, unsigned index, const PlayerInfo& pi, const gms::ItemTransactionRequest& r,
                       const char* source) {
  const std::string what = gms::DescribeItemTransaction(r);
  const Wallet before = SehWallet(pi.player);
  const Attrs attrsBefore = SehAttrs(pi.object);
  const ShopReach reach = SehShopReach(sim, pi);
  ItemOutcome o;
  SehItemSwitch(pi.player, r, &o);
  const Wallet after = SehWallet(pi.player);
  const Attrs attrsAfter = SehAttrs(pi.object);
  // D15: a step the binary reports as done must have changed the player (LabsPlayer_PurchaseItem returns quietly after
  // its "bAdded"/"bUpgraded" asserts, the sale after "Player_RemoveItem"); the original still answered success, and
  // the client's repeat on its copy would then change it alone. The host answers success 0 and counts it.
  if (o.success && g_opt.itemEffectCheck && !gms::ItemTransactionTookEffect(r.type, StateOf(before), StateOf(after))) {
    o.success = false;
    o.why = "MISMATCH: the binary's step changed nothing (an assert inside it?), answered as refused";
    ++g_st.itemsMismatched;
  }
  const unsigned t = r.type <= 5 ? r.type : 0;
  if (o.success) ++g_st.itemsDone[t];
  else ++g_st.itemsRefused[t];
  char shop[160];
  if (reach.haveBase)
    std::snprintf(shop, sizeof shop, "%.2f from the base object at (%.2f, %.2f), reach %.2f%s", reach.distance,
                  reach.basePos[0], reach.basePos[1], reach.reach, reach.dead ? ", dead" : "");
  else
    std::snprintf(shop, sizeof shop, "no base object for team %u%s", pi.team, reach.dead ? ", dead" : "");
  Log("commands: %s player %u: ItemTransaction %s (%s) -> %s%s; shop %d, spell %d, CheckPurchase %d, slot %u, cost "
      "%d; gold %.1f -> %.1f (total %.1f), spell points %u -> %u, %s; %s",
      source, index, what.c_str(), o.asset ? AssetName(o.asset) : "no asset",
      !o.ran ? "EXCEPTION" : o.success ? "done" : "refused: ", o.ran && !o.success ? o.why : "", o.shop, o.spell,
      o.check, o.slot, o.cost, before.gold, after.gold, after.total, before.spellPoints, after.spellPoints,
      InventoryChanges(before, after).c_str(), shop);
  if (o.success)
    Log("commands: player %u: creature %08X attributes after the %s: Power %.1f -> %.1f, MaxHealth %.1f -> %.1f, "
        "HealthRegen %.1f -> %.1f, Haste %.1f -> %.1f",
        index, pi.creature, gms::ItemTransactionName(r.type), attrsBefore.v[0], attrsAfter.v[0], attrsBefore.v[1],
        attrsAfter.v[1], attrsBefore.v[2], attrsAfter.v[2], attrsBefore.v[3], attrsAfter.v[3]);
  // The answer the binary's emptied function would have sent (player index, type, item, slot, success). A success goes
  // to the player's team, whose clients keep the player's fog data (AA) and repeat the transaction on their copy; a
  // refusal only to the player [recipients assumed]. The slot is the server's final one: the client's reader runs
  // LabsPlayer_CheckPurchase on its copy and asserts "slot == data.m_itemIndex" for a purchase.
  const std::uint16_t to = o.success ? TeamMask(sim, pi.team) : static_cast<std::uint16_t>(1u << index);
  gms::Bytes m = gms::BuildItemTransactionResult(static_cast<std::uint8_t>(index), r.type, r.item, o.slot, o.success);
  Answer a;
  a.players = static_cast<std::uint16_t>(to & 0x3FF);
  a.what = gms::DescribeCommandAnswer(m);
  a.msg = std::move(m);
  a.early = true;
  g_answers.push_back(std::move(a));
}

// ---- emotes and voice lines (D7b) -----------------------------------------------------------------------------------

std::uint32_t g_rng = 0x2545F491u;
unsigned NextVariant() {
  g_rng ^= g_rng << 13;
  g_rng ^= g_rng >> 17;
  g_rng ^= g_rng << 5;
  return 1 + g_rng % gms::kVoiceLineVariants;
}

// A ClientFeedbackEvent with the def `defName` for the speaker's creature (the client plays
// "Play_<clientFeedbackTokenName>_VO_<line>_<variant %02u>" on it), queued on the outgoing vector for `teamBits`'
// players as nClientFeedback.SendEventToPlayersOnTeam does (the replication's drain sends it). False on a failure.
bool SehSendFeedback(void* sim, const char* defName, std::uint32_t creature, unsigned teamBits, unsigned variant,
                     unsigned* mask, std::uint32_t target = 0, unsigned excludePlayers = 0) {
  __try {
    void* def = FindAssetByName(defName);
    if (!def) return false;
    alignas(8) unsigned char ev[kClientFeedbackEventSize];
    std::memset(ev, 0, sizeof ev);
    Field<std::uint32_t>(ev, feedback::kEventId) = Field<std::uint32_t>(def, asset::kId);
    Field<void*>(ev, feedback::kEventDef) = def;
    Field<std::uint32_t>(ev, feedback::kObject) = creature;
    Field<std::uint32_t>(ev, feedback::kTarget) = target;  // D11b: ~targetnounname~ (0 = none, as before)
    Field<std::uint32_t>(ev, feedback::kUint) = variant;
    *mask = PlayerMaskForTeams(sim, teamBits, true, true) & ~excludePlayers;
    if (!*mask) return true;  // nobody to send to (D11b: the only player is the one left out)
    SendClientFeedbackEvent(ev, *mask, false);
    return true;
  } __except (SehNote(GetExceptionInformation(), "SendClientFeedbackEvent")) {
    return false;
  }
}

void Feedback(void* sim, unsigned index, const PlayerInfo& pi, const char* defName, bool team, const char* what) {
  const unsigned teamBits = team ? (1u << pi.team) : 0x07u;
  // D11b: a line the speaker's CharacterBanterData.sequentialEmote lists (Desecrator's joke) plays its variants in order
  const unsigned variant = banter::EmoteVariant(sim, index, pi.creature, defName, NextVariant());
  unsigned mask = 0;
  if (pi.creature && SehSendFeedback(sim, defName, pi.creature, teamBits, variant, &mask)) {
    ++g_st.feedbacks;
    Log("commands: player %u: %s -> ClientFeedbackEvent %s, variant %02u, speaker %08X, to players 0x%04x (%s)", index,
        what, defName, variant, pi.creature, mask, team ? "the team" : "everyone");
  } else {
    ++g_st.feedbackFailed;
    Log("commands: player %u: %s -> ClientFeedbackEvent %s FAILED (no creature, no asset or an exception)", index, what,
        defName);
  }
}

// The shaper's PlayerClass.emoteAbility (its Key hash), 0 if it has none (PC_Planeswalker).
std::uint32_t SehEmoteAbility(void* player) {
  __try {
    void* noun = Field<void*>(player, player::kNounDef);
    void* nounData = noun ? Field<void*>(noun, asset::kData) : nullptr;
    void* pcAsset = nounData ? Field<void*>(nounData, kNounPlayerClass) : nullptr;
    void* pc = pcAsset ? Field<void*>(pcAsset, asset::kData) : nullptr;
    return pc ? Field<std::uint32_t>(pc, kPlayerClassEmoteAbility) : 0;
  } __except (SehNote(GetExceptionInformation(), "EmoteAbility")) {
    return 0;
  }
}

// ---- the sixth seam: the send stub ---------------------------------------------------------------------------------

int SehReadMessage(void* message, unsigned char* wire, unsigned char* buf, unsigned cap) {
  __try {
    if (!message) return -1;
    *wire = Field<unsigned char>(message, msg::kTransportId);
    const unsigned size = Field<unsigned>(message, msg::kSize);
    const unsigned char* body = Field<unsigned char*>(message, msg::kBuffer);
    if (size > cap || (size && !body)) return -1;
    if (size) std::memcpy(buf, body, size);
    return static_cast<int>(size);
  } __except (SehNote(GetExceptionInformation(), "ReadMessage")) {
    return -1;
  }
}

void OnStubMessage(void* message, unsigned mask) {
  static unsigned char buf[8192];
  unsigned char wire = 0;
  const int n = SehReadMessage(message, &wire, buf, sizeof buf);
  if (n < 0) {
    ++g_st.stubUnreadable;
    Log("commands: send stub: unreadable message %p (mask 0x%04x); dropped", message, mask);
    return;
  }
  ++g_st.stubMessages;
  gms::Bytes m;
  m.push_back(wire);
  m.insert(m.end(), buf, buf + n);
  std::string note;
  if (wire == gms::kGmsActionCommandResponse && n == static_cast<int>(gms::kActionCommandResponseSize)) {
    const unsigned char kind = buf[6];
    if (kind == gms::kResponseAbilityStarted) ++g_st.started;
    else if (kind == gms::kResponseFailed) ++g_st.failed;
    else if (kind == gms::kResponseAbilityEnded) ++g_st.ended;
    if (kind == gms::kResponseFailed && buf[5] == 0 && g_failedSeq > 0) {
      gms::SetResponseSequence(&m, static_cast<std::uint8_t>(g_failedSeq));
      note = " (sequence filled in by the host)";
    }
  }
  std::string what = gms::DescribeCommandAnswer(m);
  if (what.empty()) what = gms::GmsName(wire);
  if (LogBudget())
    Log("commands: send stub: %02x %s (%d B body), players 0x%04x%s: %s", wire, gms::GmsName(wire), n, mask,
        note.c_str(), gms::Hex(m, 80).c_str());
  Push(static_cast<std::uint16_t>(mask), std::move(m), "from the simulator (send stub): " + what + note);
}

// Replaces GameSimulator_SendToPlayerListStub: cdecl (12-byte holder {message, control block, -} by value, u32 mask,
// u32 1), caller cleans. The holder is released as the stub does.
int __cdecl SendToPlayerListHook(void* message, void* control, void* pad, unsigned mask, unsigned one) {
  (void)one;
  if (g_sim && GetCurrentSim() == g_sim) {
    OnStubMessage(message, mask);
  } else {
    ++g_st.stubForeign;  // another simulator's message: dropped as before
  }
  void* holder[3] = {message, control, pad};
  SharedPtrRelease(holder);
  return 0;
}

void __cdecl SendActionCommandFailedHook(unsigned unused, unsigned player, unsigned seq, unsigned type, unsigned guid,
                                         int result) {
  ++g_st.failedCalls;
  const int prev = g_failedSeq;
  g_failedSeq = static_cast<int>(seq & 0xFF);
  if (LogBudget())
    Log("commands: GameSimulator_SendActionCommandFailed(player %u, seq %u, command type %u, guid 0x%08X, result %d%s)",
        player & 0xFF, seq & 0xFF, type, guid, result, (seq & 0xFF) ? "" : "; seq 0: nothing sent");
  g_origFailed(unused, player, seq, type, guid, result);
  g_failedSeq = prev;
}

// ---- movement log -----------------------------------------------------------------------------------------------

struct Watch {
  int ticksLeft = 0;
  int sinceLog = 0;
  int stillTicks = 0;
  std::uint8_t seq = 0;
  bool moving = false;
};
Watch g_watch[kMaxPlayers];
unsigned g_locoLogged;
int g_inShopReach[kMaxPlayers] = {-1, -1, -1, -1, -1, -1, -1, -1, -1, -1};  // D7b: the last proximity seen, logged on change

// ---- Q3c: the command latency (perf.h) ------------------------------------------------------------------------------

int KindOfType(std::uint8_t type) {
  switch (type) {
    case gms::kCmdMove: return gms::timing::kCmdKindMove;
    case gms::kCmdAttackMove: return gms::timing::kCmdKindAttackMove;
    case gms::kCmdStopMoving:
    case gms::kCmdStop:
    case gms::kCmdHold: return gms::timing::kCmdKindStop;
    case gms::kCmdAbility:
    case gms::kCmdDance: return gms::timing::kCmdKindAbility;
    default: return gms::timing::kCmdKindOther;
  }
}

// A command taken: its arrival in RakNet (the client's; a test-side command from the commands file has none), now.
void NoteIntake(unsigned index, int kind, std::uint8_t seq, bool hasTarget = false, float x = 0, float y = 0) {
  if (!perf::Enabled()) return;
  perf::Commands().OnIntake(static_cast<int>(index), kind, seq, gms::CurrentArrivalMs(), gms::timing::NowMs(),
                            hasTarget, x, y);
}

// After the tick: a pending move's effect is a locomotion goal within 3 units of its point (or the creature there), an
// attack-move's any goal (it may chase a target on the way), a stop's no goal [the rule is ours: what the client's
// prediction would show first].
void MoveEffects(unsigned index, const MoveInfo& mv) {
  if (!perf::Enabled()) return;
  for (const gms::timing::CommandLatency::PendingMove& m : perf::Commands().PendingMoves(static_cast<int>(index))) {
    bool done;
    if (m.kind == gms::timing::kCmdKindStop) {
      done = mv.goalFlags == 0;
    } else {
      const float gx = mv.goal[0] - m.x, gy = mv.goal[1] - m.y, px = mv.pos[0] - m.x, py = mv.pos[1] - m.y;
      done = (mv.goalFlags != 0 && gx * gx + gy * gy < 9.f) || px * px + py * py < 9.f ||
             (m.kind == gms::timing::kCmdKindAttackMove && mv.goalFlags != 0);
    }
    if (done) perf::Commands().OnEffect(static_cast<int>(index), m.seq, 1u << m.kind, gms::timing::NowMs());
  }
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (g_installed) return true;
  struct Check {
    std::uintptr_t va;
    const unsigned char* bytes;
    std::size_t n;
    const char* name;
  };
  const Check checks[] = {
      {0x00922850, kServerCommandInitBytes, sizeof kServerCommandInitBytes, "ServerCommand_Init"},
      {0x00922950, kServerCommandSetSlotBytes, sizeof kServerCommandSetSlotBytes, "ServerCommand_SetAbilitySlot"},
      {0x009227F0, kServerCommandSetGuidBytes, sizeof kServerCommandSetGuidBytes, "ServerCommand_SetAbilityGUID"},
      {0x00923E60, kQueueCommandBytes, sizeof kQueueCommandBytes, "ServerCommandQueue_QueueCommand"},
      {0x00923450, kTickPlayerBytes, sizeof kTickPlayerBytes, "ServerCommandQueue_TickPlayer"},
      {0x009149C0, kRankUpAbilityBytes, sizeof kRankUpAbilityBytes, "LabsPlayer_RankUpAbility"},
  };
  for (const Check& c : checks)
    if (!CodeMatches(c.va, c.bytes, c.n)) {
      Log("commands: %s at %08X has other bytes; command intake off", c.name, static_cast<unsigned>(c.va));
      return false;
    }
  g_origFailed = reinterpret_cast<SendActionCommandFailedFn>(
      HookDetour(kSendActionCommandFailed, kSendActionCommandFailedBytes, sizeof kSendActionCommandFailedBytes,
                 reinterpret_cast<void*>(&SendActionCommandFailedHook)));
  if (!g_origFailed) {
    Log("commands: GameSimulator_SendActionCommandFailed not detoured; command intake off");
    return false;
  }
  if (!HookReplace(kSendToPlayerListStub, kSendToPlayerListStubBytes, sizeof kSendToPlayerListStubBytes,
                   reinterpret_cast<void*>(&SendToPlayerListHook))) {
    Log("commands: GameSimulator_SendToPlayerListStub not replaced; command intake off");
    return false;
  }
  // D7b: the item transaction and feedback functions; a mismatch turns only that part off
  const Check itemChecks[] = {
      {0x00913B10, kCanUseItemShopBytes, sizeof kCanUseItemShopBytes, "LabsPlayer_CanUseItemShop"},
      {0x00912E30, kCheckPurchaseBytes, sizeof kCheckPurchaseBytes, "LabsPlayer_CheckPurchase"},
      {0x00914690, kPurchaseItemBytes, sizeof kPurchaseItemBytes, "LabsPlayer_PurchaseItem"},
      {0x00914080, kSellItemBytes, sizeof kSellItemBytes, "LabsPlayer_SellItem"},
      {0x00914160, kUseItemBytes, sizeof kUseItemBytes, "LabsPlayer_UseItem"},
      {0x00913360, kSwapItemsBytes, sizeof kSwapItemsBytes, "LabsPlayer_SwapItems"},
  };
  for (const Check& c : itemChecks)
    if (g_opt.itemTransactions && !CodeMatches(c.va, c.bytes, c.n)) {
      Log("commands: %s at %08X has other bytes; item transactions off", c.name, static_cast<unsigned>(c.va));
      g_opt.itemTransactions = false;
    }
  const Check feedbackChecks[] = {
      {0x008F71D0, kSendClientFeedbackEventBytes, sizeof kSendClientFeedbackEventBytes,
       "GameSimulator_SendClientFeedbackEvent"},
      {0x008F08D0, kPlayerMaskForTeamsBytes, sizeof kPlayerMaskForTeamsBytes, "GameSimulator_GetPlayerMaskForTeams"},
  };
  for (const Check& c : feedbackChecks)
    if (g_opt.emotes && !CodeMatches(c.va, c.bytes, c.n)) {
      Log("commands: %s at %08X has other bytes; emotes and voice lines off", c.name, static_cast<unsigned>(c.va));
      g_opt.emotes = false;
    }
  // D7c: TickPlayer's queued-move step asks the host's rule (MoveTakeoverStub); with move_takeover 0 the stub runs the
  // binary's check unchanged (and still counts the deferrals). A mismatch leaves the binary's rule in place.
  const bool takeoverPatched =
      HookReplace(kQueuedMoveBranch, kQueuedMoveBranchBytes, sizeof kQueuedMoveBranchBytes,
                  reinterpret_cast<void*>(&MoveTakeoverStub));
  if (!takeoverPatched) Log("commands: TickPlayer's queued-move step at %08X has other bytes; D7c's takeover off",
                            static_cast<unsigned>(kQueuedMoveBranch));
  g_installed = true;
  Log("commands: installed (send stub 0x008F9090 replaced, 0x008F0110 detoured); behavior tree %d, locomotion "
      "updates %d, FoW updates %d, item transactions %d (effect check %d), emotes and voice lines %d; D7c: queued-move "
      "step %s, move takeover %d, queue trace %d (limit %d)",
      g_opt.behaviorTree, g_opt.locomotionUpdates, g_opt.fowUpdates, g_opt.itemTransactions, g_opt.itemEffectCheck,
      g_opt.emotes, takeoverPatched ? "patched (0x0092396D)" : "NOT patched", g_opt.moveTakeover, g_opt.queueTrace,
      g_opt.queueLogLimit);
  return true;
}

bool Installed() { return g_installed; }

bool OnClientMessage(void* sim, unsigned index, std::uint8_t wire, const std::uint8_t* body, std::size_t n,
                     const char* source) {
  g_sim = sim;
  if (wire != gms::kGmsActionCommandMsgs && wire != gms::kGmsAbilityRankUp && wire != gms::kGmsItemTransaction &&
      wire != gms::kGmsSurrenderMsgs && wire != gms::kGmsPauseMsgs)
    return false;
  ++g_st.received;
  PlayerInfo pi;
  const bool havePlayer = index < kMaxPlayers && SehReadPlayer(sim, index, &pi);
  if (wire == gms::kGmsActionCommandMsgs) {
    gms::ActionCommand c;
    std::string error;
    if (!gms::ParseActionCommand(body, n, &c, &error)) {
      ++g_st.parseErrors;
      Log("commands: %s ActionCommandMsgs from player %u not parsed: %s (%s)", source, index, error.c_str(),
          gms::Hex(body, n, 64).c_str());
      return true;
    }
    if (c.type < 12) ++g_st.byType[c.type];
    const std::string what = gms::DescribeActionCommand(c);
    if (!g_installed || !havePlayer) {
      ++g_st.notConverted;
      Log("commands: %s player %u: %s; not converted (%s)", source, index, what.c_str(),
          !g_installed ? "intake off" : "no server player");
      return true;
    }
    switch (c.type) {
      case gms::kCmdMove:
      case gms::kCmdAttackMove:
      case gms::kCmdStopMoving:
      case gms::kCmdStop:
      case gms::kCmdHold:
      case gms::kCmdAbility: {
        int rank = 0;
        if (!SehQueue(index, pi.player, c, &rank)) {
          ++g_st.queueFailed;
          Log("commands: %s player %u: %s -> QueueCommand FAILED (exception)", source, index, what.c_str());
          return true;
        }
        ++g_st.queued;
        NoteIntake(index, KindOfType(c.type), c.seq, c.type == gms::kCmdMove || c.type == gms::kCmdAttackMove, c.pos.x,
                   c.pos.y);
        if (LogBudget())
          Log("commands: %s player %u: %s -> server command type %u, seq %u%s queued (ServerCommandQueue_QueueCommand, "
              "tick now); creature %08X",
              source, index, what.c_str(), c.type, c.seq,
              c.type == gms::kCmdAbility ? (std::string(", rank ") + std::to_string(rank) + ",").c_str() : "",
              pi.creature);
        {
          char when[64];  // D7c: the queue right after QueueCommand (and its immediate tick for a non-move)
          std::snprintf(when, sizeof when, "after %s command type %u seq %u", source, c.type, c.seq);
          if (!g_queues[index]) g_queues[index] = SehPlayerQueue(index);
          TraceQueue(index, pi.creature, pi.object, when);
        }
        {
          // the movement log: 15 s after a move or stop, 60 s after an ability (a walk-up can be long)
          Watch& w = g_watch[index];
          w.ticksLeft = 30 * (c.type == gms::kCmdAbility ? 60 : 15);
          w.sinceLog = 1000;  // log the next tick
          w.stillTicks = 0;
          w.seq = c.seq;
        }
        return true;
      }
      case gms::kCmdPing: {
        std::uint16_t team = 0;
        for (unsigned j = 0; j < kMaxPlayers; ++j) {
          PlayerInfo pj;
          if (SehReadPlayer(sim, j, &pj) && pj.team == pi.team) team = static_cast<std::uint16_t>(team | (1u << j));
        }
        ++g_st.pings;
        Push(team, gms::BuildMiniMapPing(c.caster, c.target, c.pos, static_cast<std::uint8_t>(c.value)),
             "ping relay (MiniMapPing) to the pinger's team");
        Log("commands: %s player %u: %s -> MiniMapPing to players 0x%04x (team %u)", source, index, what.c_str(), team,
            pi.team);
        return true;
      }
      case gms::kCmdDance: {
        if (!g_opt.emotes) break;
        // D7b: the shaper's PlayerClass.emoteAbility as an ability command by GUID (the queue takes any type but 8 and
        // 11 in its ability slot as an ability), plus the PlayerCommDance voice ("highemote") to everyone
        gms::ActionCommand e = c;
        e.slot = -1;
        e.guid = SehEmoteAbility(pi.player);
        int rank = 0;
        if (!e.guid) {
          ++g_st.notConverted;
          Log("commands: %s player %u: %s; the shaper has no emoteAbility", source, index, what.c_str());
        } else if (SehQueue(index, pi.player, e, &rank)) {
          ++g_st.queued;
          ++g_st.dances;
          NoteIntake(index, gms::timing::kCmdKindAbility, e.seq);
          Log("commands: %s player %u: %s -> server command type %u, seq %u, emoteAbility guid 0x%08X queued "
              "(ServerCommandQueue_QueueCommand, tick now); creature %08X",
              source, index, what.c_str(), e.type, e.seq, e.guid, pi.creature);
        } else {
          ++g_st.queueFailed;
          Log("commands: %s player %u: %s -> QueueCommand FAILED (exception)", source, index, what.c_str());
        }
        Feedback(sim, index, pi, gms::kFeedbackDance, false, "dance");
        return true;
      }
      case gms::kCmdTaunt:
        if (!g_opt.emotes) break;
        // D7b: no taunt ability exists in ServerData; the taunt animation state (CharacterAnimation
        // tauntEmoteAnimState "emote_taunt_all") is in no shaper's animstate, so the taunt is its voice
        Feedback(sim, index, pi, gms::kFeedbackTaunt, false, "taunt");
        return true;
      case gms::kCmdVoiceLine: {
        if (!g_opt.emotes) break;
        const gms::VoiceLine* v = gms::FindVoiceLine(c.value);
        if (!v) {
          ++g_st.notConverted;
          Log("commands: %s player %u: %s; unknown voice line", source, index, what.c_str());
          return true;
        }
        Feedback(sim, index, pi, v->feedbackDef, v->team, what.c_str());
        return true;
      }
      case gms::kCmdInteract:
        // D7b: unreachable in build 854: no noun has a cInteractableDef (noun +0x1D0), so no object gets an interactable
        // component and Interactable_CanBeUsedBy (0x008FC5B0) refuses every right-click target
        ++g_st.interacts;
        Log("commands: %s player %u: %s; not converted (no interactable object exists in build 854's data)", source,
            index, what.c_str());
        return true;
      default:
        break;
    }
    ++g_st.notConverted;
    Log("commands: %s player %u: %s; not converted (%s)", source, index, what.c_str(),
        g_opt.emotes ? "unknown type" : "[host] emotes = 0");
    return true;
  }
  if (wire == gms::kGmsAbilityRankUp) {
    std::uint32_t slot = 0;
    if (!gms::ParseAbilityRankUp(body, n, &slot) || !havePlayer || !g_installed) {
      ++g_st.parseErrors;
      Log("commands: %s AbilityRankUp from player %u not handled (%s)", source, index, gms::Hex(body, n).c_str());
      return true;
    }
    PlayerInfo after;
    const bool ok = SehRankUp(pi.player, static_cast<int>(slot)) && SehReadPlayer(sim, index, &after);
    const unsigned s = slot - 3;
    const bool ranked = ok && s < 4 && after.ranks[s] == pi.ranks[s] + 1;
    if (ranked) ++g_st.rankUps;
    else ++g_st.rankUpsRefused;
    if (ranked) NoteIntake(index, gms::timing::kCmdKindRankUp, 0);  // Q3c: answered and done by the next AA delta
    Log("commands: %s player %u: AbilityRankUp slot %u -> LabsPlayer_RankUpAbility: %s; rank %u -> %u, ability points "
        "%u -> %u, level %u",
        source, index, slot, ranked ? "ranked up" : ok ? "refused (no points, maximum rank or a bad slot)" : "FAILED",
        s < 4 ? pi.ranks[s] : 0, s < 4 && ok ? after.ranks[s] : 0, pi.points, ok ? after.points : 0, pi.level);
    SendFowUpdate(sim, index, ranked ? "after a rank-up" : "after a refused rank-up");
    return true;
  }
  if (wire == gms::kGmsItemTransaction) {
    ++g_st.items;
    gms::ItemTransactionRequest r;
    if (!gms::ParseItemTransaction(body, n, &r)) {
      ++g_st.parseErrors;
      Log("commands: %s player %u: ItemTransaction of %zu B not parsed", source, index, n);
    } else if (!g_installed || !havePlayer || !g_opt.itemTransactions) {
      Log("commands: %s player %u: ItemTransaction %s; not handled (%s)", source, index,
          gms::DescribeItemTransaction(r).c_str(),
          !g_installed ? "intake off" : !havePlayer ? "no server player" : "[host] item_transactions = 0");
    } else {
      NoteIntake(index, gms::timing::kCmdKindItem, 0);  // Q3c: answered by C3, done by the next AA delta
      OnItemTransaction(sim, index, pi, r, source);
    }
    return true;
  }
  ++g_st.other;
  Log("commands: %s player %u: %s %s; not handled (the game mode's, D8)", source, index, gms::GmsName(wire),
      gms::Hex(body, n).c_str());
  return true;
}

void Tick(void* sim) {
  g_sim = sim;
  if (!g_installed) return;
  static bool granted[kMaxPlayers];
  const float dt = SehDt(sim);
  for (unsigned i = 0; i < kMaxPlayers; ++i) {
    PlayerInfo pi;
    if (!SehReadPlayer(sim, i, &pi) || !pi.creature) continue;
    // D6a: a bot has no command queue (ServerCommandQueue_QueueCommand ignores bots); its brain requests its abilities
    // and the agent step ticks its behavior tree after the brain step
    if (bots::IsBotIndex(static_cast<int>(i))) continue;
    static bool xpGranted[kMaxPlayers];
    if (g_opt.grantXp > 0 && !xpGranted[i]) {
      // D10 test aid ([host] grant_xp): levels and their ability points the way XP gives them (nXPSourceType.Cheat)
      xpGranted[i] = true;
      const bool ok = SehGrantXp(pi.player, static_cast<float>(g_opt.grantXp));
      PlayerInfo after;
      if (ok && SehReadPlayer(sim, i, &after))
        Log("commands: player %u granted %d XP ([host] grant_xp): level %u -> %u, ability points %u -> %u", i,
            g_opt.grantXp, pi.level, after.level, pi.points, after.points);
      else
        Log("commands: player %u: grant_xp %d failed", i, g_opt.grantXp);
    }
    if (g_opt.grantAbilityPoints > 0 && !granted[i]) {
      // test aid ([host] grant_ability_points): the level-up and XP rules that give points are the game mode's
      granted[i] = true;
      const unsigned ranked = pi.ranks[0] + pi.ranks[1] + pi.ranks[2] + pi.ranks[3];
      if (pi.points == 0 && ranked == 0 &&
          SehGrantPoints(pi.player, static_cast<unsigned>(g_opt.grantAbilityPoints))) {
        Log("commands: player %u (level %u) had no ability points; granted %d ([host] grant_ability_points)", i,
            pi.level, g_opt.grantAbilityPoints);
        SendFowUpdate(sim, i, "ability points granted");
      } else {
        Log("commands: player %u: level %u, ability points %u, ranks %u/%u/%u/%u; nothing granted", i, pi.level,
            pi.points, pi.ranks[0], pi.ranks[1], pi.ranks[2], pi.ranks[3]);
      }
    }
    const int r = SehTickPlayer(i, pi.player, pi.object, g_opt.behaviorTree, dt);
    if (r < 0) ++g_st.tickErrors;
    else if (r > 0) ++g_st.ticks;
    if (r == 2) ++g_st.btTicks;
    TraceQueue(i, pi.creature, pi.object, "after the tick");  // D7c
  }
}

namespace {

// D11: every player's level, ability points and ranks as last logged (humans and bots: a bot's level-ups rank its
// BotClass.skillOrder inside LabsPlayer_GrantXP, which no host code calls; this is where they show).
struct RankState {
  bool seen = false;
  unsigned level = 0, points = 0;
  unsigned ranks[4] = {};
};
RankState g_rankState[kMaxPlayers];

void RankWatch(void* sim) {
  for (unsigned i = 0; i < kMaxPlayers; ++i) {
    PlayerInfo pi;
    if (!SehReadPlayer(sim, i, &pi) || !pi.creature) continue;
    RankState& s = g_rankState[i];
    if (s.seen && s.level == pi.level && s.points == pi.points && std::memcmp(s.ranks, pi.ranks, sizeof s.ranks) == 0)
      continue;
    Log("ranks: player %u (%s, team %u): level %u -> %u, ability points %u -> %u, ranks Q/W/E/R %u/%u/%u/%u -> "
        "%u/%u/%u/%u",
        i, bots::IsBotIndex(static_cast<int>(i)) ? "bot" : "human", pi.team, s.level, pi.level, s.points, pi.points,
        s.ranks[0], s.ranks[1], s.ranks[2], s.ranks[3], pi.ranks[0], pi.ranks[1], pi.ranks[2], pi.ranks[3]);
    s.seen = true;
    s.level = pi.level;
    s.points = pi.points;
    std::memcpy(s.ranks, pi.ranks, sizeof s.ranks);
  }
}

}  // namespace

unsigned SendFeedback(void* sim, const char* defName, std::uint32_t object, unsigned teamBits, unsigned uintValue,
                      std::uint32_t target, unsigned excludePlayers) {
  unsigned mask = 0;
  if (!SehSendFeedback(sim, defName, object, teamBits, uintValue, &mask, target, excludePlayers)) return 0;
  return mask ? mask : 0x10000u;  // queued, but no player on those teams: not a failure
}

bool GrantXpNow(void* sim, unsigned index, float amount, const char* source) {
  PlayerInfo before;
  if (!SehReadPlayer(sim, index, &before)) {
    Log("commands: %s xp %.0f for player %u: no server player", source, amount, index);
    return false;
  }
  const bool ok = SehGrantXp(before.player, amount);
  PlayerInfo after;
  if (ok && SehReadPlayer(sim, index, &after)) {
    Log("commands: %s player %u granted %.0f XP: level %u -> %u, ability points %u -> %u, ranks Q/W/E/R %u/%u/%u/%u",
        source, index, amount, before.level, after.level, before.points, after.points, after.ranks[0], after.ranks[1],
        after.ranks[2], after.ranks[3]);
    return true;
  }
  Log("commands: %s xp %.0f for player %u FAILED", source, amount, index);
  return false;
}

bool GrantGoldNow(void* sim, unsigned index, float amount, const char* source) {
  PlayerInfo pi;
  if (!SehReadPlayer(sim, index, &pi)) {
    Log("commands: %s gold %.0f for player %u: no server player", source, amount, index);
    return false;
  }
  const Wallet before = SehWallet(pi.player);
  const bool ok = death::GrantGold(static_cast<int>(index), amount, kGoldSourceCheat, false, 0, 0);
  const Wallet after = SehWallet(pi.player);
  if (ok) ++g_st.goldGrants;
  Log("commands: %s player %u granted %.0f gold (nGoldSourceType.Cheat): %s; gold %.1f -> %.1f (total %.1f)", source,
      index, amount, ok ? "done" : "FAILED (the death module's gold path is off?)", before.gold, after.gold,
      after.total);
  return ok;
}

namespace {

// D15's report: what an instance id is now.
struct InstView {
  std::uint32_t id = 0;
  bool found = false, ended = false;
  int stacks = 0, durationMs = 0;
  char name[80] = "?";
};

bool SehInstance(std::uint32_t id, InstView* v) {
  __try {
    v->id = id;
    void* in = id ? GetInstance(id) : nullptr;
    if (!in) return true;
    v->found = true;
    void* st = Field<void*>(in, inst::kStatic);
    const char* n = st ? *reinterpret_cast<const char**>(st) : nullptr;
    if (n) strncpy_s(v->name, n, _TRUNCATE);
    v->stacks = Field<int>(in, inst::kStackCount);
    v->durationMs = Field<int>(in, inst::kDurationMs);
    v->ended = Field<unsigned char>(in, inst::kEndPending) != 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "report: instance")) {
    return false;
  }
}

// The object's modifier instance ids (ability data container +0x9C..+0xA0), at most `max`; -1 on an exception.
int SehModifierIds(void* object, std::uint32_t* out, int max) {
  __try {
    char* c = Field<char*>(object, obj::kAbilityData);
    if (!c) return 0;
    int n = 0;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd) && n < max; ++p)
      out[n++] = *p;
    return n;
  } __except (SehNote(GetExceptionInformation(), "report: modifiers")) {
    return -1;
  }
}

struct ObjView {
  std::uint32_t id = 0;
  unsigned team = 0;
  float hp = -1.f, maxHp = -1.f, distance = 0.f;
  float pos[3] = {};
  char noun[64] = "?";
};

bool SehObjView(void* o, const float* from, ObjView* v) {
  __try {
    v->id = Field<std::uint32_t>(o, gobj::kId);
    v->team = Field<unsigned char>(o, gobj::kTeam);
    void* nounAsset = Field<void*>(o, gobj::kNoun);
    const char* n = nounAsset ? Field<const char*>(nounAsset, 0) : nullptr;
    if (n) strncpy_s(v->noun, n, _TRUNCATE);
    char* cd = Field<char*>(o, combatant::kOfObject);
    if (cd) {
      v->hp = Field<float>(cd, combatant::kHitPoints);
      v->maxHp = MaxHitPoints(o);
    }
    const float* p = reinterpret_cast<const float*>(static_cast<char*>(o) + gobj::kPosition);
    std::memcpy(v->pos, p, sizeof v->pos);
    if (from) {
      const float dx = p[0] - from[0], dy = p[1] - from[1];
      v->distance = std::sqrt(dx * dx + dy * dy);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "report: object")) {
    return false;
  }
}

// The live objects within `radius` of `self` (not itself, no pending delete) with their distances, at most `max`, in
// the object manager's order; -1 on an exception.
int SehNearObjects(void* sim, void* self, float radius, void** out, float* dist, int max) {
  __try {
    const float* me = reinterpret_cast<const float*>(static_cast<char*>(self) + gobj::kPosition);
    void* om = Field<void*>(sim, sim::kObjectManager);
    int n = 0;
    int it = ObjMgrIterBegin(om);
    for (void* o = ObjMgrIterNext(om, &it); o && n < max; o = ObjMgrIterNext(om, &it)) {
      if (o == self || Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      const float* p = reinterpret_cast<const float*>(static_cast<char*>(o) + gobj::kPosition);
      const float dx = p[0] - me[0], dy = p[1] - me[1];
      const float d = std::sqrt(dx * dx + dy * dy);
      if (d > radius) continue;
      out[n] = o;
      dist[n] = d;
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "report: near objects")) {
    return -1;
  }
}

// The passive instance ids an item slot holds (player::kItemPassives).
bool SehSlotPassives(void* player, unsigned slot, std::uint32_t* out) {
  __try {
    char* item = static_cast<char*>(player) + player::kItems + slot * player::kItemSize;
    for (int k = 0; k < player::kItemPassiveCount; ++k)
      out[k] = Field<std::uint32_t>(item, player::kItemPassives + 4 * k);
    return true;
  } __except (SehNote(GetExceptionInformation(), "report: item slot")) {
    return false;
  }
}

float SehAttrValue(void* object, int index) {
  __try {
    void* data = Field<void*>(object, gobj::kAttributes);
    return data ? GetAttribute(data, index, 1) : 0.f;
  } __except (SehNote(GetExceptionInformation(), "report: attribute")) {
    return 0.f;
  }
}

// "<script> x<stacks> <duration> s[ ENDED|GONE]" for each modifier of `object`, in groups of `perLine`.
std::vector<std::string> ModifierTexts(void* object, int perLine) {
  std::uint32_t ids[64];
  const int n = SehModifierIds(object, ids, 64);
  std::vector<std::string> lines;
  std::string cur;
  int inLine = 0;
  for (int i = 0; i < n; ++i) {
    InstView v;
    if (!SehInstance(ids[i], &v)) continue;
    char buf[160];
    std::snprintf(buf, sizeof buf, "%s%s x%d %.1f s%s", inLine ? ", " : "", v.name, v.stacks, v.durationMs / 1000.0,
                  !v.found ? " GONE" : v.ended ? " ENDED" : "");
    cur += buf;
    if (++inLine == perLine) {
      lines.push_back(cur);
      cur.clear();
      inLine = 0;
    }
  }
  if (!cur.empty() || lines.empty()) lines.push_back(cur.empty() ? (n < 0 ? "EXCEPTION" : "none") : cur);
  return lines;
}

}  // namespace

bool ItemReportNow(void* sim, unsigned index, const char* label, float nearRadius) {
  PlayerInfo pi;
  if (!SehReadPlayer(sim, index, &pi) || !pi.object) {
    Log("items: report %s player %u: no server player or creature", label, index);
    return false;
  }
  ++g_st.reports;
  const Wallet w = SehWallet(pi.player);
  ObjView me;
  SehObjView(pi.object, nullptr, &me);
  Log("items: report %s player %u: creature %08X (%s) at (%.1f, %.1f) level %u, hp %.1f/%.1f, gold %.1f (total %.1f), "
      "spell points %u",
      label, index, pi.creature, me.noun, me.pos[0], me.pos[1], pi.level, me.hp, me.maxHp, w.gold, w.total,
      w.spellPoints);
  for (unsigned s = 0; s < player::kItemSlots; ++s) {
    if (!w.item[s]) continue;
    std::uint32_t passive[player::kItemPassiveCount] = {};
    SehSlotPassives(pi.player, s, passive);
    std::string ps;
    for (int k = 0; k < player::kItemPassiveCount; ++k) {
      if (!passive[k]) continue;
      InstView v;
      SehInstance(passive[k], &v);
      char buf[160];
      std::snprintf(buf, sizeof buf, "%s%s %08X %s", ps.empty() ? "" : ", ", v.name, passive[k],
                    !v.found ? "GONE (stale id)" : v.ended ? "ENDED" : "live");
      ps += buf;
    }
    Log("items: report %s player %u slot %u: 0x%08X x%d; passives: %s", label, index, s, w.item[s], w.count[s],
        ps.empty() ? "none" : ps.c_str());
  }
  std::string attrs;
  for (int a = 0; a < 72; ++a) {
    const float v = SehAttrValue(pi.object, a);
    if (v == 0.f) continue;
    char buf[32];
    std::snprintf(buf, sizeof buf, "%s%d=%g", attrs.empty() ? "" : " ", a, v);
    attrs += buf;
  }
  Log("items: report %s player %u attributes: %s", label, index, attrs.c_str());
  for (const std::string& m : ModifierTexts(pi.object, 16))
    Log("items: report %s player %u modifiers: %s", label, index, m.c_str());
  if (nearRadius > 0.f) {
    constexpr int kMaxNear = 128, kShown = 12;
    void* objs[kMaxNear];
    float dist[kMaxNear];
    const int n = SehNearObjects(sim, pi.object, nearRadius, objs, dist, kMaxNear);
    std::vector<int> order;
    for (int i = 0; i < n; ++i) order.push_back(i);
    std::sort(order.begin(), order.end(), [&](int a, int b) { return dist[a] < dist[b]; });
    const float* from = reinterpret_cast<const float*>(static_cast<char*>(pi.object) + gobj::kPosition);
    for (std::size_t k = 0; k < order.size() && k < static_cast<std::size_t>(kShown); ++k) {
      ObjView v;
      if (!SehObjView(objs[order[k]], from, &v)) continue;
      // what the on-hit debuffs change on a target: Power 0, MovementSpeedPercentDecrease 12, AttackSpeedDecrease 14,
      // PhysicalDamageReduction 24, MagicalDamageReduction 25, HealingReduction 39
      char targetAttrs[160];
      void* o = objs[order[k]];
      std::snprintf(targetAttrs, sizeof targetAttrs, "0=%g 12=%g 14=%g 24=%g 25=%g 39=%g", SehAttrValue(o, 0),
                    SehAttrValue(o, 12), SehAttrValue(o, 14), SehAttrValue(o, 24), SehAttrValue(o, 25),
                    SehAttrValue(o, 39));
      for (const std::string& m : ModifierTexts(o, 12))
        Log("items: report %s near %08X %s team %u at %.1f, hp %.1f/%.1f, attributes %s: modifiers: %s", label, v.id,
            v.noun, v.team, v.distance, v.hp, v.maxHp, targetAttrs, m.c_str());
    }
    if (n <= 0) Log("items: report %s: nothing within %.0f", label, nearRadius);
  }
  return true;
}

void AfterTick(void* sim) {
  g_sim = sim;
  if (!g_installed) return;
  if (g_opt.rankLog) RankWatch(sim);
  for (unsigned i = 0; i < kMaxPlayers; ++i) {
    PlayerInfo pi;
    if (!SehReadPlayer(sim, i, &pi) || !pi.object) continue;
    if (g_opt.itemTransactions) {
      // D7b: where the client's own shop check (LabsPlayer_InProximityToItemShop) changes its answer, for the reach
      const int in = SehInProximity(pi.player);
      if (in >= 0 && in != g_inShopReach[i]) {
        const ShopReach r = SehShopReach(sim, pi);
        Log("commands: player %u %s the item shop's reach (LabsPlayer_InProximityToItemShop %d): %.2f from the base "
            "object at (%.2f, %.2f); footprint + [0x01437FFC] = %.2f",
            i, in ? "entered" : "left", in, r.distance, r.basePos[0], r.basePos[1], r.reach);
        g_inShopReach[i] = in;
      }
    }
    MoveInfo mv;
    if (!SehReadMove(pi.object, &mv)) continue;
    MoveEffects(i, mv);  // Q3c: a pending move's, attack-move's or stop's effect on the locomotion
    // a piece of D2g: the position the server's locomotion tick flagged for sending (locomotion +0x8E)
    if (g_opt.locomotionUpdates && mv.sendFlag) {
      gms::Bytes m;
      const std::uint16_t known = SehKnownMask(pi.object);
      if (known && repl::LocomotionUpdate(pi.object, pi.creature, {0, 1}, &m)) {
        ++g_st.locoUpdates;
        if (++g_locoLogged <= 20 || g_locoLogged % 100 == 0)
          Log("commands: LocomotionDataUpdate #%u for %08X at (%.2f, %.2f): %s", g_st.locoUpdates, pi.creature, mv.pos[0],
              mv.pos[1], gms::Hex(m).c_str());
        char what[96];
        std::snprintf(what, sizeof what, "locomotion update of %08X (position %.2f, %.2f)", pi.creature, mv.pos[0],
                      mv.pos[1]);
        Push(known, std::move(m), what);
      }
    }
    Watch& w = g_watch[i];
    if (w.ticksLeft <= 0) continue;
    --w.ticksLeft;
    const bool moving = mv.goalFlags != 0;
    if (++w.sinceLog >= 15 || moving != w.moving || w.ticksLeft == 0) {
      w.sinceLog = 0;
      Log("commands: player %u creature %08X (seq %u): position (%.2f, %.2f, %.2f), locomotion goal (%.2f, %.2f, %.2f) "
          "flags 0x%x, quantized %04x/%04x%s",
          i, pi.creature, w.seq, mv.pos[0], mv.pos[1], mv.pos[2], mv.goal[0], mv.goal[1], mv.goal[2], mv.goalFlags,
          mv.block, mv.xy, moving ? "" : " (no goal)");
    }
    w.moving = moving;
    if (!moving && ++w.stillTicks > 60) w.ticksLeft = 0;  // no goal for 2 s: the watch ends
    if (moving) w.stillTicks = 0;
  }
}

void TakeAnswers(std::vector<Answer>* out) {
  out->clear();
  out->swap(g_answers);
}

void TakeEarlyAnswers(std::vector<Answer>* out) {
  out->clear();
  std::vector<Answer> rest;
  for (Answer& a : g_answers) (a.early ? *out : rest).push_back(std::move(a));
  g_answers.swap(rest);
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "received %u (move %u, stop moving %u, ability %u, stop %u, attack-move %u, ping %u, other %u), queued "
                "%u, not converted %u, parse errors %u, queue exceptions %u; rank-ups %u (refused %u); player ticks %u "
                "(behavior tree %u, exceptions %u); send stub %u messages (ability started %u, failed %u, ended %u; "
                "other simulators %u, unreadable %u), failure builder %u; locomotion updates %u, FoW updates %u, pings "
                "%u; items %u (purchased %u, sold %u, used %u, consumed %u, swapped %u; refused %u, of them "
                "mismatched %u); test gold grants %u, item reports %u; dances %u, voice events %u (failed %u), "
                "interacts %u; D7c: moves taking over an engaged attack-move %u, deferred by the binary's rule %u, "
                "attack-moves deferred behind one %u, errors %u; queue trace lines %u (past the limit %u)",
                g_st.received, g_st.byType[1], g_st.byType[2], g_st.byType[3], g_st.byType[8], g_st.byType[9],
                g_st.byType[7], g_st.byType[4] + g_st.byType[5] + g_st.byType[6] + g_st.byType[10] + g_st.byType[11],
                g_st.queued, g_st.notConverted, g_st.parseErrors, g_st.queueFailed, g_st.rankUps, g_st.rankUpsRefused,
                g_st.ticks, g_st.btTicks, g_st.tickErrors, g_st.stubMessages, g_st.started, g_st.failed, g_st.ended,
                g_st.stubForeign, g_st.stubUnreadable, g_st.failedCalls, g_st.locoUpdates, g_st.fowUpdates, g_st.pings,
                g_st.items, g_st.itemsDone[1], g_st.itemsDone[2], g_st.itemsDone[3], g_st.itemsDone[4],
                g_st.itemsDone[5],
                g_st.itemsRefused[0] + g_st.itemsRefused[1] + g_st.itemsRefused[2] + g_st.itemsRefused[3] +
                    g_st.itemsRefused[4] + g_st.itemsRefused[5],
                g_st.itemsMismatched, g_st.goldGrants, g_st.reports, g_st.dances, g_st.feedbacks, g_st.feedbackFailed,
                g_st.interacts, g_st.takeovers, g_st.moveDeferrals, g_st.attackMoveDeferrals, g_st.decideErrors,
                g_st.queueLines, g_st.queueLinesDropped);
}

}  // namespace cmds
