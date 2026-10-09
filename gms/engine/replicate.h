// Replication from the server simulator (roadmap D2f, D2g-live; ADR-2 decision 5, seams 1, 2, 3 and 5;
// docs/GMS_PROTOCOL.md *The world from the server simulator (D2f)* and *Live replication (D2g-static)*, *(D2g-live)*).
// The host builds what a client sees from the server's own objects, with the client's own reflection code (game.h
// *replication*), so the bytes match the client's readers by construction:
//   - creation (seam 2): for every networked server object (network component +0x8BC) whose noun type is replicated
//     (D2g: every type but spawn points; D2f's set was Tower, creature, interactable), ObjectCreateBegin (8E: server id,
//     cGameObjectCreateData filled from the object, sporelabsObject), ProjectileDataCreate (9D) for projectiles,
//     LocomotionDataUpdate (9C) for movers, AttributeDataUpdate (9F), CombatantDataUpdate (A0), AgentBlackboardUpdate
//     (A2), InteractableDataUpdate (A1) when the object has them, and ObjectCreateComplete (8F) with the object's
//     modifier records. Every body is the full state (analyzer mode 1). Written with the structs' field states when
//     no player knew the object yet, so the field states hold what was sent;
//   - updates (seam 3, D2g-live): per struct the members whose value hash differs from the field state (analyzer mode
//     8, the comparison the stripped analyzer made), written with the field state, which the writer then updates;
//   - the network-rate timers whose per-frame update is stripped (0x008EE900), so non-player movers mark positions;
//   - the player's shaper: LabsPlayer_DeployCharacter (0x00915400); the simulator queues PlayerCharacterDeploy (B3);
//   - ready-made messages (seam 1): the outgoing vector (sim +0x1A998) is read and emptied; kind-3 entries' line-of-
//     sight records are released (D2f leaked them);
//   - player data (seam 5): LabsPlayerGlobalUpdate (A9) / LabsPlayerFowUpdate (AA), selected members or deltas.
// Every engine call runs under SEH and needs the server simulator bound to the thread (the caller binds it).
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace repl {

using Bytes = std::vector<std::uint8_t>;

// Checks the prologues of the client functions it calls and finds the reflected types (and their member counts).
// Logs and returns false if anything differs. Call with the server simulator bound.
bool Init();

// LabsPlayer_DeployCharacter for the server player `index` at its start point. Returns the creature's server id
// (cLabsPlayer +0x0C), 0 if there is no player or the deploy failed.
std::uint32_t DeployShaper(void* sim, unsigned index);

struct WorldMessage {
  Bytes msg;         // wire byte first
  std::string what;  // for the session's send log
};

// ---- objects (D2g-live) ---------------------------------------------------------------------------------------------

// The structs an object replicates, in their create order after 8E.
enum Part : int {
  kPartObject = 0,    // sporelabsObject (the cGameObject), 90 ObjectUpdate; field state in the network component
  kPartProjectile,    // cProjectileData, 9D ProjectileDataCreate (create only)
  kPartLocomotion,    // cLocomotionData, 9C
  kPartAttributes,    // cAttributeData, 9F
  kPartCombatant,     // cCombatantData, A0
  kPartBlackboard,    // cAgentBlackboard, A2
  kPartInteractable,  // cInteractableData, A1
  kNumParts
};
std::uint8_t PartWire(int part);
const char* PartName(int part);

struct NetObject {
  void* object = nullptr;
  std::uint32_t id = 0;
  char name[48] = {};
  std::int32_t nounType = 0;
  std::uint16_t known = 0;    // network component +0xE: the players that know the object
  std::uint16_t visible = 0;  // network component +0x10: the players that see it
  void* data[kNumParts] = {};
  float pos[3] = {};
  float hp = -1.f;  // -1 without combatant data
  std::uint32_t owner = 0;  // object +0x48 (mOwnerID): BuildWorld creates an owner first (D9b, core/replication/create_order.h)
};

// The live networked objects (network component, not pending delete), in the object manager's order. -1 on an
// exception.
int Scan(void* sim, std::vector<NetObject>* out);

// Whether objects of this noun type are created for the players: all = D2g's rule (every networked type but spawn
// points: the 23 networked spawn points are invisible and Base_Health_Regen_* load on both sides), else D2f's (Tower,
// creature, interactable).
bool Replicated(std::int32_t nounType, bool all);
const char* NounTypeName(std::int32_t nounType);

// The create messages of one object (8E, 9D, 9C, 9F, A0, A2, A1, 8F + `modifierRecords`), full state. With
// `withFieldState` the bodies are written with the structs' field states (no player knew the object). `bytes` gets
// their total size. False (logged) if a body could not be written.
bool BuildCreate(const NetObject& o, bool withFieldState, const Bytes& modifierRecords, std::vector<WorldMessage>* out,
                 int* bytes);

// D2g-live step 2, a fog exit (between 94 and 95): the full state of every struct the object has, as at its create but
// without 8E/8F: 90 (sporelabsObject), 9D, 9C, 9F, A0, A2, A1, analyzer mode 1, written without the field states (the
// other players' deltas stay pending), and (step 3) 9E with every animation override. `after` (may be null) gets what
// goes after 95: the attached effects (92, cleared first when there are any) and the last animation (B1). `bytes` gets
// the total size of both. False (logged) if a body could not be written.
bool BuildFullState(const NetObject& o, std::vector<WorldMessage>* out, std::vector<WorldMessage>* after, int* bytes);

// ---- the replication extras (D2g-live step 3) -----------------------------------------------------------------------
// AttachedEffectUpdate (92): u32 id, u8 n, n x {u8 slot, reflected cAttachedEffectData (full)} of the object's used
// slots (asset != 0; n may be 0, which clears the client's). `slots` gets n. False (logged) on an error.
bool AttachedEffectsUpdate(const NetObject& o, Bytes* out, int* slots);
// The animation state of an object with cAnimationData (+0x894): `overrides` = AnimationDataUpdate (9E) with members
// 0-6 (mOverrideAnimations) when any is set or `allOverrides`; `init` = InitAnimationInfo (B1, 33 bytes with the wire
// byte) when a last animation is recorded. Both empty without animation data.
bool AnimationMessages(const NetObject& o, bool allOverrides, Bytes* overrides, Bytes* init);
// A 9E with the overrides that differ from `shadow` (7 u32, the host's copy of what the players were sent; no field
// state exists for cAnimationData), which it then updates. The first call (`*seeded` false) only fills the shadow.
// Returns the members written: 0 = none, < 0 = error.
int AnimationOverrideDelta(const NetObject& o, std::uint32_t* shadow, bool* seeded, Bytes* out);

// One incremental update: `part`'s members whose value hash differs from the field state (mode 8), as the whole
// message (wire byte, u32 id, body). Returns the number of members written: 0 = nothing changed, < 0 = error (logged).
int BuildDelta(const NetObject& o, int part, Bytes* out);

// network component +0xE |= known, +0x10 |= visible. False if the object has no network component.
bool AddPlayerBits(void* object, std::uint16_t known, std::uint16_t visible);
// G3: network component +0xE and +0x10 &= ~bits (a player that left knows nothing any more). False if the object has
// no network component.
bool RemovePlayerBits(void* object, std::uint16_t bits);

// The object's known & visible masks if it still exists (GetObjectById), else false.
bool ObjectSeers(std::uint32_t id, std::uint16_t* seers);
// LabsAbilities_GetInstance(id) != null.
bool InstanceExists(std::uint32_t instance);

// The initial world for the server player `index` (D2f), in the server's object order, written with the field states
// for objects nobody knew; sets bit `index` in every created object's known and visible masks. `all`: Replicated()'s
// rule. `records(carrier)` gives an object's modifier records (8F).
struct WorldCounts {
  int networked = 0;  // live objects with a network component
  int created = 0;    // created for the player
  int withHp = 0;     // of those, with attribute or combatant data
  int skipped = 0;    // networked, but not of a replicated noun type (spawn points, base regeneration zones)
  int failed = 0;     // an object whose bodies could not be built (logged)
  int messages = 0;
  int bytes = 0;
};
using RecordsFn = Bytes (*)(void* ctx, std::uint32_t carrier);
void BuildWorld(void* sim, unsigned index, bool all, RecordsFn records, void* ctx, std::vector<WorldMessage>* out,
                WorldCounts* counts);

// ---- the outgoing vector (seam 1) -----------------------------------------------------------------------------------

struct Outgoing {
  int kind = 0;               // the entry's kind (0 delete, 1 recipients, 2 object, 3 position)
  std::uint32_t object = 0;   // kinds 0 and 2: the object id
  std::uint16_t recipients = 0;  // kind 0: the known mask at the deletion; kind 1: the entry's mask
  bool objectAlive = false;   // kind 2: the object exists at the drain
  std::uint16_t seers = 0;    // kind 2: its known & visible masks
  bool hasPoint = false;      // kind 3: the entry's line-of-sight record (position, team, radius), read before its
  float x = 0.f, y = 0.f;     //   release (D2g-live step 2 routes the message by the vision model)
  float radius = 0.f;
  std::uint8_t team = 0;
  Bytes msg;                  // wire byte first (kind 0: 91 + id)
};

struct DrainCounts {
  int entries = 0;
  int kinds[4] = {};
  int unreadable = 0;  // no message object or an unknown kind (logged)
  int released = 0;    // kind-3 line-of-sight records released
};

// Every entry of the outgoing vector, oldest first; releases the kind-3 entries' line-of-sight records and empties
// the vector. Recipients are routed by the caller (gms::RouteOutgoing).
void Drain(void* sim, std::vector<Outgoing>* out, DrainCounts* counts);

// The number of entries waiting in the outgoing vector (-1 on an exception).
int PendingOutgoing(void* sim);

// The line-of-sight pool's records in use and capacity (T10); false without a manager.
bool LineOfSightPool(void* sim, int* inUse, int* capacity);

// ---- the network-rate timers ----------------------------------------------------------------------------------------

// GameSimulator_UpdateNetworkRateTimers (0x008EE900) for `sim`; `mask` gets sim +0x1C600 afterwards. False on an
// exception or if the prologue differs (then never again).
bool UpdateNetworkRateTimers(void* sim, unsigned* mask);

// ---- players (seam 5) -----------------------------------------------------------------------------------------------

// LabsPlayerGlobalUpdate (A9, gameplay index, reflected labsPlayerGlobalData) for the server player `index` with
// exactly the members `members` (labsPlayerGlobalData indexes, gms::PlayerMember), written from the server's
// cLabsPlayer by the client's writer with the player's field state (so the delta pass doesn't repeat them).
bool PlayerUpdate(void* sim, unsigned index, const std::vector<int>& members, Bytes* out);

// D7-live: LabsPlayerFowUpdate (AA, gameplay index, reflected labsPlayerFowData) with exactly `members`, the same way.
bool PlayerFowUpdate(void* sim, unsigned index, const std::vector<int>& members, Bytes* out);

// D2g-live: the members of labsPlayerGlobalData (A9) / labsPlayerFowData (AA) that changed since they were last sent
// (mode 8 against the player's field state). Returns the members written: 0 = none, < 0 = error.
int PlayerGlobalDelta(void* sim, unsigned index, Bytes* out);
int PlayerFowDelta(void* sim, unsigned index, Bytes* out);

// D2h: the server player's mLevel (cLabsPlayer +0xB0, labsPlayerFowData member 4), for the host's log lines.
bool PlayerLevel(void* sim, unsigned index, std::uint32_t* level);

// ---- the HUD's battleground data (D1c-live; MATCH_FLOW.md *Game-mode handlers, energy lines and spirit wells (R10)* §3)

// UIDataState (8B): the reflected cBattlegroundsUIData of the server simulator (sim +0xC930). `full`: every member
// (mode 1), with the frequency-1 capacities mNodeCapacity/mCampCapacity, which make the client create its well and camp
// lists: once per player only (a second one asserts "attempting to recreate"); written with the field state when
// `withFieldState` (the first player). Else the members that changed since the field state (mode 8). Returns the
// members written: 0 = nothing (no message), < 0 = error.
int UIDataUpdate(void* sim, bool full, bool withFieldState, Bytes* out);

// UISpiritWellUpdate (8C): u8 node mask, then one reflected cBattlegroundsUINodeData per set bit, from the server's node
// list (UI data +0x360, field state +0x6C per node); full or the changed nodes' changed members, as above. Must follow
// the player's first 8B (the client walks the mask only up to its node capacity).
int SpiritWellUpdate(void* sim, bool full, bool withFieldState, Bytes* out);

// UICreepCampUpdate (8D): u32 camp mask, then one reflected cBattlegroundsUICampData per set bit, from the server's camp
// list (UI data +0x368, count +0x36C, field state +0x8 per camp); full (the camps the director added, with the field
// states if `withFieldState`) or the changed members, as SpiritWellUpdate. Must follow the player's first 8B.
int CampUpdate(void* sim, bool full, bool withFieldState, Bytes* out);

// The server player's team (false if there is no such player).
bool PlayerTeam(void* sim, unsigned index, std::uint8_t* team);

// D7-live (superseded by the delta pass in D2g-live, kept for [host] replicate = 0): LocomotionDataUpdate (9C) for
// `object` with the members `members` (0 mClientPathXY, 1 mClientPathBlock).
bool LocomotionUpdate(void* object, std::uint32_t id, const std::vector<int>& members, Bytes* out);

// Logs the run-time member records (index, offset, size) of labsPlayerFowData members `first`..`last` (D7-live's
// recheck of members 9-13).
void LogPlayerFowMembers(int first, int last);

}  // namespace repl
