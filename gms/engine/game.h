// Dawngate.exe build 1.0.903.854: the addresses and calling conventions the harness uses.
// Each entry names its evidence (Ghidra name or the client function it was read from).
// Conventions: __cdecl unless noted; "this" functions take ECX (MSVC __thiscall).
#pragma once

#include <cstdint>

namespace game {

template <typename T>
inline T At(std::uintptr_t va) { return reinterpret_cast<T>(va); }
template <typename T>
inline T& Field(void* obj, std::uintptr_t off) { return *reinterpret_cast<T*>(static_cast<char*>(obj) + off); }

// --- globals ---------------------------------------------------------------------------
constexpr std::uintptr_t kMasterSimulator = 0x019E1FF8;  // cMasterGameSimulator* (InitializeMasterGameSimulator 0x008EFCC0)
constexpr std::uintptr_t kGameClient = 0x019768C0;       // cGameClient*; +8 = client cGameSimulator (GameClient_Init 0x0066D8D0)
constexpr std::uintptr_t kAssetGlobals = 0x019CD820;     // gspAssetGlobals (AssetCatalog.cpp)
constexpr std::uintptr_t kComponentTypes = 0x019A32BC;   // head of the component type list (0x0087F580 links it)

// --- memory --------------------------------------------------------------------------
// void* Alloc(size, name, 0, 0, 0, 0): the game's tracked allocator (0x006A9B50, every "SP_Simulator/..." new).
using AllocFn = void*(__cdecl*)(std::uint32_t size, const char* name, int, int, int, int);
inline AllocFn Alloc = At<AllocFn>(0x006A9B50);

// --- simulator lifecycle (InitializeClientGameSimulator 0x008F5650 does exactly these steps) ---
constexpr std::uint32_t kSimulatorSize = 0x1CFE0;
using SimCtorFn = void*(__thiscall*)(void* sim);             // cGameSimulator ctor 0x008F50B0
inline SimCtorFn SimCtor = At<SimCtorFn>(0x008F50B0);
using SimResetFn = void(__thiscall*)(void* sim);             // field reset 0x008EF590
inline SimResetFn SimReset = At<SimResetFn>(0x008EF590);
using SimInitFn = void(__thiscall*)(void* sim);              // GameSimulator init 0x008F3A10 (server branch on +0x17 == 0)
inline SimInitFn SimInit = At<SimInitFn>(0x008F3A10);

constexpr std::uint32_t kObjectManagerSize = 0x2006C;        // as cSporeLabsClientObjectManager (0x0067CC30)
using ObjMgrCtorFn = void*(__thiscall*)(void* om);           // base cGameObjectManager ctor 0x008E5270 (vtable 0x0124BE7C)
inline ObjMgrCtorFn ObjMgrCtor = At<ObjMgrCtorFn>(0x008E5270);
using ObjMgrSetSimFn = void(__thiscall*)(void* om, void* sim);  // 0x00B04BF0: om+0x14 = sim
inline ObjMgrSetSimFn ObjMgrSetSim = At<ObjMgrSetSimFn>(0x00B04BF0);
using ObjMgrIterBeginFn = int(__thiscall*)(void* om);        // 0x008E4930 (cookie 0)
inline ObjMgrIterBeginFn ObjMgrIterBegin = At<ObjMgrIterBeginFn>(0x008E4930);
using ObjMgrIterNextFn = void*(__thiscall*)(void* om, int* it);  // 0x008E4940 -> cGameObject* or null
inline ObjMgrIterNextFn ObjMgrIterNext = At<ObjMgrIterNextFn>(0x008E4940);

// Thread-bound "current simulator" (TLS slot +0x34): GetCurrentGameSimulator 0x008EF090, set 0x008EF0B0.
using GetCurrentSimFn = void*(__cdecl*)();
inline GetCurrentSimFn GetCurrentSim = At<GetCurrentSimFn>(0x008EF090);
using SetCurrentSimFn = void(__cdecl*)(void* sim);
inline SetCurrentSimFn SetCurrentSim = At<SetCurrentSimFn>(0x008EF0B0);

// --- level (Client_LoadLevel 0x0066CB90) ------------------------------------------------
using FindAssetByNameFn = void*(__cdecl*)(const char* nameType);  // AssetCatalog 0x008CF0F0, e.g. "SM_Map_v5.Level"
inline FindAssetByNameFn FindAssetByName = At<FindAssetByNameFn>(0x008CF0F0);
using SetLevelFn = void(__cdecl*)(void* sim, void* levelAsset, std::uint32_t seed, std::uint32_t flags);  // 0x008EF0E0
inline SetLevelFn SetLevel = At<SetLevelFn>(0x008EF0E0);
using StartLevelFn = void(__cdecl*)(void* sim);              // 0x008F0C80: LoS map, tag map, world, NavPower, markers
inline StartLevelFn StartLevel = At<StartLevelFn>(0x008F0C80);

// --- tick pieces (the client tick GameSimulator_UpdateClient 0x008EFAE0, minus its client assert) ---
using TimeTickFn = void(__cdecl*)(void* sim);                // GameTimeManager 0x008F9DF0
inline TimeTickFn TimeTick = At<TimeTickFn>(0x008F9DF0);
using LuaTickFn = void(__thiscall*)(void* luaSim);           // GearsLua thread scheduler 0x00875CF0, ECX = sim+0x6614
inline LuaTickFn LuaTick = At<LuaTickFn>(0x00875CF0);
using IsPausedFn = bool(__cdecl*)(void* sim);                // 0x008F9B80
inline IsPausedFn IsPaused = At<IsPausedFn>(0x008F9B80);
using GetDtFn = float(__cdecl*)(void* sim);                  // 0x008F99C0, seconds
inline GetDtFn GetDt = At<GetDtFn>(0x008F99C0);
using LocomotionTickFn = void(__cdecl*)(int replay, int frame);  // 0x008A6CA0 (server branch when !IsClientGameSimulator)
inline LocomotionTickFn LocomotionTick = At<LocomotionTickFn>(0x008A6CA0);
using ProjectileTickFn = void(__cdecl*)();                   // 0x0091DA90
inline ProjectileTickFn ProjectileTick = At<ProjectileTickFn>(0x0091DA90);
using WorldTickFn = void(__thiscall*)(void* world, float dt);  // vtable slot 3 of sim+0xDC
using DeletePendingFn = void(__cdecl*)();                    // 0x008EECA0: objects with +0x55 set
inline DeletePendingFn DeletePending = At<DeletePendingFn>(0x008EECA0);
using FowTickFn = void(__thiscall*)(void* fow);              // 0x00894B70, ECX = sim+0xCCB0 (server branch 0x00894930)
inline FowTickFn FowTick = At<FowTickFn>(0x00894B70);
// Agent behavior tree tick (AgentBehaviorTrees.cpp, 0x008A9060): (cGameObject*, 0) -> 1. In the client
// only ability requests (0x0090A670) call it, once per request; nothing ticks agents per frame.
using AgentTickFn = int(__cdecl*)(void* object, int unused);
inline AgentTickFn AgentTick = At<AgentTickFn>(0x008A9060);
// The same function with its real second argument (D1b-static, docs/AGENT_AI.md): f32 dt in seconds (request
// lifetimes count down with it). Since D1b-live the agent step (agents.cpp) calls it for every agent once per frame;
// the D7-live shaper tick goes through agents::TickTree.
using AgentTickDtFn = int(__cdecl*)(void* object, float dt);
inline AgentTickDtFn AgentTickDt = At<AgentTickDtFn>(0x008A9060);

// --- players (G2: the host adds a player to the server simulator for each accepted HelloReq) ----------------------
// GameSimulator_AddPlayer (0x008F3E20; D1 had the names of 0x008F3E20 and 0x008F1F00 swapped, fixed in D2b-live):
// (sim, gameplayIndex, spectator) -> cLabsPlayer*. LabsPlayer_Create (0x00915C60, on the current simulator's player
// pool; +0x2C = index), then the player map (sim +0x1A810, spectators +0x1A8FC); on a server (+0x17 == 0) also an
// entry in the replay recording (ReplayRecorder_RecordPlayerAdded 0x00921B00 on sim +0x55C). Callers: the client's
// HelloPlayer/PlayerJoined callback (GameClient_OnSessionPlayerAdded 0x006B36F0) and the replay player
// (ReplayPlayer_Update 0x00920DF0), both cdecl.
using AddPlayerFn = void*(__cdecl*)(void* sim, unsigned gameplayIndex, int spectator);
inline AddPlayerFn AddPlayer = At<AddPlayerFn>(0x008F3E20);
// GameSimulator_RemovePlayer (0x008F1F00): (sim, gameplayIndex). Asserts that sim is the current simulator, destroys
// the player and erases it from the maps; on a server also clears its bit in every network component
// (GameSimulator_ClearPlayerFromNetworkComponents 0x008EFA80) and records the removal for the replay (0x00921B30).
using RemovePlayerFn = void(__cdecl*)(void* sim, unsigned gameplayIndex);
inline RemovePlayerFn RemovePlayer = At<RemovePlayerFn>(0x008F1F00);
// GameSimulator_GetPlayerByIndex (0x008F0D90): (sim, gameplayIndex) -> cLabsPlayer* or null (spectator map for 10-14).
using GetPlayerByIndexFn = void*(__cdecl*)(void* sim, unsigned gameplayIndex);
inline GetPlayerByIndexFn GetPlayerByIndex = At<GetPlayerByIndexFn>(0x008F0D90);
// AssetCatalog_FindById (0x008CF2D0): asset id (FNV-1 of the lower-case "<name>.<class>") -> asset or null.
using FindAssetByIdFn = void*(__cdecl*)(std::uint32_t id);
inline FindAssetByIdFn FindAssetById = At<FindAssetByIdFn>(0x008CF2D0);
// cLabsPlayer: the members of labsPlayerGlobalData (docs/GMS_PROTOCOL.md *Player updates and the pre-game (D2c)*).
namespace player {
constexpr std::uintptr_t kNounDef = 0x04;         // mpNounDef: the shaper's Noun asset (pointer; LabsPlayer_DeployCharacter
                                                  // 0x00915400 asserts it non-zero)
constexpr std::uintptr_t kCurrentCreature = 0x0C;  // mCurrentCreatureId (tObjID)
constexpr std::uintptr_t kStatus = 0x10;           // mStatus (u32 flags)
constexpr std::uintptr_t kStatusProgress = 0x14;   // mStatusProgress (f32)
constexpr std::uintptr_t kIndex = 0x2C;            // mPlayerIndex (u8, set by LabsPlayer_Create)
constexpr std::uintptr_t kOnlineId = 0x30;         // mPlayerOnlineId (u64; Blaze user id)
constexpr std::uintptr_t kTeam = 0xA0;             // mTeam (u8: 1 blue, 2 red, 255 unset)
constexpr std::uintptr_t kIsBot = 0x48;            // mbIsBot (bool; LabsPlayer_IsBot 0x00912030)
constexpr std::uintptr_t kBotData = 0x4C;          // mpBotData (BotClass asset)
// D3-live: the perk page shaper select picked (labsPlayerGlobalData members 13/14; LabsPlayer_GetLoadout 0x00911AA0
// reads both for the LOADOUT_0x<catalogID> user setting at EnterGame)
constexpr std::uintptr_t kLoadoutId = 0x2CC;       // mLoadoutID (u32)
constexpr std::uintptr_t kLoadoutVersion = 0x2D0;  // mLoadoutVersion (u32)
// D3-perks: the player's loadout stats, two hash maps {+4 buckets, +8 bucket count, ...} (LabsPlayer_Construct
// 0x00914B80; the client's LabsPlayer_SetLoadout 0x00915C30 copies the pre-game loadout C0 02 into them):
// the attributes, key (op << 16 | attribute) -> {u32 attribute, u32 op, f32 value}, which
// AttributeData_ComputePlayerAttribute (0x008D8180) adds for op 1 (Add) to every attribute of the player's creature
// (LabsPlayer_GetLoadoutAttribute 0x00914890); and the modifiers, id -> {id, ability}, which only the perk tooltips read
constexpr std::uintptr_t kLoadoutAttributes = 0x2D4;
constexpr std::uintptr_t kLoadoutModifiers = 0x2F4;
}  // namespace player
// D3-perks: PerkAttributeMap_GetOrInsert (0x0047AF60, thiscall on the map, (const u32* key), ret 4): the entry for the
// key, inserted as {NumAttributes 72, Add, 0.0} if missing; returns the entry ({u32 attribute, u32 op, f32 value}, the
// node + 4). LoadoutManager_ReadPlayerLoadout (0x00886AC0) fills the C0 02 stats with it.
using LoadoutAttributeSlotFn = std::uint32_t*(__thiscall*)(void* map, const std::uint32_t* key);
inline LoadoutAttributeSlotFn LoadoutAttributeSlot = At<LoadoutAttributeSlotFn>(0x0047AF60);
constexpr unsigned char kLoadoutAttributeSlotBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x83, 0xEC, 0x1C};  // mov eax,[esp+4]; sub esp,1Ch
// D3-perks: LabsPlayer_GetLoadoutAttribute (0x00914890): (player, attribute, op) -> the loadout's value (ST0), 0 if none.
using GetLoadoutAttributeFn = float(__cdecl*)(void* player, unsigned attribute, unsigned op);
inline GetLoadoutAttributeFn GetLoadoutAttribute = At<GetLoadoutAttributeFn>(0x00914890);
// D3-perks: AttributeData_AddModifier (0x008D9C70, thiscall on object +0x8E0, (index, op, value), ret 0xC): adds an
// attribute modifier (op: nAttributeOp, 1 Add, 2 PercentChange, ...), recalculates and runs the index's on-modify hook;
// returns the modifier's handle. nAttribute.AddAttributeModifier (0x00944990) calls it.
using AddAttributeModifierFn = int(__thiscall*)(void* attributeData, int index, int op, float value);
inline AddAttributeModifierFn AddAttributeModifier = At<AddAttributeModifierFn>(0x008D9C70);
constexpr unsigned char kAddAttributeModifierBytes[] = {0xD9, 0x44, 0x24, 0x0C, 0x53, 0x55};  // fld [esp+0Ch]; push ebx; push ebp

// --- bots (D6a; docs/AGENT_AI.md §11 and *D6a*) ------------------------------------------------------------------
// LabsPlayer_ReflectBotData (0x00915D30, cdecl (player, -, -, const u32* assetId) -> 1): the reflection callback of
// labsPlayerGlobalData member 15. Asserts the asset exists, sets mbIsBot = 1 and mpBotData, then LabsPlayer_SetNounDef
// (0x00914AD0: mpNounDef = BotClass.nounDef, status | 0x100), mRole = BotClass.role and the "<shaper> BOT" name
// (LabsPlayer_UpdateBotName 0x009159D0).
using ReflectBotDataFn = int(__cdecl*)(void* player, unsigned unused1, unsigned unused2, const std::uint32_t* assetId);
inline ReflectBotDataFn ReflectBotData = At<ReflectBotDataFn>(0x00915D30);
constexpr unsigned char kReflectBotDataBytes[] = {0x8B, 0x44, 0x24, 0x10, 0x8B, 0x00, 0x56, 0x57};
// GameObject_IsBot (0x00880440, cdecl object): player-controlled (+0x56) and its player (+0x4C on the current
// simulator) has mbIsBot.
using IsBotFn = bool(__cdecl*)(void* object);
inline IsBotFn GameObjectIsBot = At<IsBotFn>(0x00880440);
// AgentBlackboard_SetTarget (0x008A9D40, cdecl (object, target id)): the agent's mTarget (+0x1E5C), what
// AgentBlackboard_GetBestTarget returns for a bot (aggro type 5) once BotDirector_IsValidTarget (0x008AFD10) accepts it.
using SetAgentTargetFn = void(__cdecl*)(void* object, std::uint32_t target);
inline SetAgentTargetFn SetAgentTarget = At<SetAgentTargetFn>(0x008A9D40);
// AgentBehaviorTree_PushRequest (0x008A7940, cdecl (behavior tree data, type lo, type hi, lifetime f32 bits, +0xC, ?))
// -> the request slot; 0x400 = reset to the default behavior (what nAgent.ResetDefaultBehavior 0x0093CE20 pushes, with
// FLT_MAX as the lifetime 0x7F7FFFFF).
using PushRequestFn = void*(__cdecl*)(void* btData, std::uint32_t typeLo, std::uint32_t typeHi, std::uint32_t lifetime,
                                      std::uint32_t param, std::uint32_t unused);
inline PushRequestFn PushTreeRequest = At<PushRequestFn>(0x008A7940);
constexpr std::uint32_t kRequestResetDefaultBehavior = 0x400;
constexpr std::uint32_t kLifetimeForever = 0x7F7FFFFF;
// BotClass (asset data +0x68, 0x1C bytes): +0 nounDef asset, +4 aiDefinition asset, +8 role, +0xC skillOrder {ptr,
// count}, +0x14 spellOrder {ptr, count}.
namespace botclass {
constexpr std::uintptr_t kNounDef = 0x00;
constexpr std::uintptr_t kAiDefinition = 0x04;
constexpr std::uintptr_t kRole = 0x08;
}  // namespace botclass
// BotPlan (Tuning.BotPlan, BattlegroundsGameplayTuning.defaultBotPlan; data 0x20 bytes): +0 bot {asset* ptr, count},
// +8 mission {cMissionDefinition 0x10, count}, +0x10 topLaners, +0x14 botLaners, +0x18 slot {cBotSlot 8: {cBotShaperType
// 0xC: archetype, attackStyle, damageType} ptr, count}.
namespace botplan {
constexpr std::uintptr_t kBots = 0x00;
constexpr std::uintptr_t kTopLaners = 0x10;
constexpr std::uintptr_t kBotLaners = 0x14;
constexpr std::uintptr_t kSlots = 0x18;
constexpr std::uint32_t kSlotSize = 0x8, kShaperTypeSize = 0xC;
}  // namespace botplan
// PlayerClass (noun data +0x1D8 -> asset, data +0x68): +0x114 CharacterArchetype, +0x11C ShaperStyle_AttackStyle,
// +0x120 ShaperStyle_DamageStyle (enums: gms/core/bots/teamfill.h ShaperTypeOf).
namespace playerclass {
constexpr std::uintptr_t kArchetype = 0x114;
constexpr std::uintptr_t kAttackStyle = 0x11C;
constexpr std::uintptr_t kDamageStyle = 0x120;
}  // namespace playerclass
// D6b (docs/AGENT_AI.md *D6b*): the bots' shopping reads the items and builds (docs/data/assetdata_types.txt). Item data
// (asset +0x68, 0xBC bytes): +0 Category (0 Stat, 1 Ability = a spell, 2 Consumable), +0xC StackCount (u8), +0x10
// ItemCost (u32), +0x90 Consumable (bool), +0x9C UpgradesFrom {asset* ptr, count}. A shaper's recommended build: the
// "PC_<Shaper>" Noun's data +0x320 recommendedItems {ItemBuild asset* ptr, count} (the BotClass "PC_<Shaper>"'s namesake;
// the in-game noun a BotClass names has none); ItemBuild data +0 category {BuildCategory 0x18 bytes: +0 item {asset* ptr,
// count}} (Starting, Core, Offensive, Defensive).
namespace itemdata {
constexpr std::uintptr_t kCategory = 0x00;
constexpr std::uintptr_t kStackCount = 0x0C;
constexpr std::uintptr_t kCost = 0x10;
constexpr std::uintptr_t kConsumable = 0x90;
constexpr std::uintptr_t kUpgradesFrom = 0x9C;
constexpr int kCategoryConsumable = 2;
}  // namespace itemdata
constexpr std::uintptr_t kNounRecommendedItems = 0x320;
constexpr std::uint32_t kBuildCategorySize = 0x18;
// The bot director (cBotDirector, sim +0xCD80: GameSimulator_GetBotDirector 0x008EE620; server simulators only) and its
// fire tracker (AGENT_AI.md §11.3): the enable byte +0x978 that only the constructor, reset and destroy write (0), the
// tracker at +0xBF0 (600 x 600 cells per team, x, y in [-300, 300)). FireTracker_IsPositionSafe (0x00876900, thiscall
// (tracker, team, const float* xy) -> bool): false while a cell's start + duration >= the current simulator's clock;
// FireTracker_GetCell (0x00876700) asserts for a team other than 1/2 or a point off the grid and then returns the
// assert's result as a cell pointer, so the host checks both before the call.
constexpr std::uintptr_t kSimBotDirector = 0xCD80;
constexpr std::uintptr_t kBotDirectorFireTrackerOn = 0x978;
constexpr std::uintptr_t kBotDirectorFireTracker = 0xBF0;
using FireTrackerIsSafeFn = bool(__thiscall*)(void* tracker, int team, const float* xy);
inline FireTrackerIsSafeFn FireTrackerIsPositionSafe = At<FireTrackerIsSafeFn>(0x00876900);
constexpr unsigned char kFireTrackerIsSafeBytes[] = {0x8B, 0x44, 0x24, 0x08, 0xD9, 0x40, 0x04, 0x56};

// --- GMS messages ---------------------------------------------------------------------------
// The message factory (GetMessageFactory 0x00ADA7F0 caches it here) maps a GMS id (kGms*, 0-76) to
// the transport id, the first byte on the wire (MessageFactory_GetTransportID 0x00AD9A40, 0xFF if
// unknown). A message object (0x20 bytes, vtable 0x012B9F88) keeps its transport id at +5.
constexpr std::uintptr_t kMessageFactory = 0x01BB6340;
using GetTransportIdFn = unsigned char(__thiscall*)(void* factory, int gmsId);
inline GetTransportIdFn GetTransportId = At<GetTransportIdFn>(0x00AD9A40);
constexpr std::uint32_t kMessageVtable = 0x012B9F88;

// --- replication (D2f; docs/GMS_PROTOCOL.md *Replication (D2d)*, *The world from the server simulator (D2f)*) -----
// The client's own reflection code, called by the host on server objects (ADR-2 decision 5: the bytes match the
// client's readers by construction). One body = ReflectData_Init, set the mode, Analyze, Write into a message.
// ReflectionRegistry_FindType (0x008B49B0): FNV-1 of a type name -> its descriptor (+8 member records of 0x60 bytes,
// +0xC member count) or null.
using FindTypeFn = void*(__cdecl*)(std::uint32_t nameHash);
inline FindTypeFn FindType = At<FindTypeFn>(0x008B49B0);
constexpr unsigned char kFindTypeBytes[] = {0xB8, 0x01, 0x00, 0x00, 0x00, 0x84, 0x05, 0xCC};
// ReflectData_Init (0x008F90A0, thiscall, ret 0x10): zeroes the 0x334-byte cReflectData (+0 mode, +4 body bytes,
// +8 member bits, +0xC member count, +0x10/+0x14/+0x18 the chosen members {u8 index, u32 hash} with inline room for
// 96, +0x324 object, +0x32C type).
using ReflectDataInitFn = void(__thiscall*)(void* data, void* object, std::uint32_t zero, void* type, std::uint32_t flag);
inline ReflectDataInitFn ReflectDataInit = At<ReflectDataInitFn>(0x008F90A0);
constexpr unsigned char kReflectDataInitBytes[] = {0x8B, 0x54, 0x24, 0x08, 0x8B, 0xC1, 0x33, 0xC9};
constexpr std::uint32_t kReflectDataSize = 0x334;
// NetworkUtils_AnalyzeReflectedFields (0x008F90F0): picks the members to send. Mode (cReflectData +0): 1 = include
// conditional members (record +0x44 & 0x20) whose condition byte is 0, 4 = skip members equal to their default
// (record +0x20), 8 = incremental (skip frequency-1 members); 0 = every member (the host's create: full state).
using AnalyzeReflectedFn = void(__cdecl*)(void* object, void* type, void* data);
inline AnalyzeReflectedFn AnalyzeReflected = At<AnalyzeReflectedFn>(0x008F90F0);
constexpr unsigned char kAnalyzeReflectedBytes[] = {0x83, 0xEC, 0x10, 0x8B, 0x44, 0x24, 0x18, 0x53};
// NetworkUtils_GetReflectedSize (0x008F8680): mask bytes + member bytes; 0 if nothing was chosen.
using GetReflectedSizeFn = int(__cdecl*)(void* data);
inline GetReflectedSizeFn GetReflectedSize = At<GetReflectedSizeFn>(0x008F8680);
constexpr unsigned char kGetReflectedSizeBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xC0, 0x81, 0xEC};
// NetworkUtils_WriteReflectedFields (0x008F8770): (object, field state or 0, type, message, data). Writes the mask
// (one byte up to 8 members, else the length nibble form) and the chosen members, `asset` members as the asset's
// id (+0x60), through GmsMessage_WriteBytes (0x00ADAEE0, thiscall on the message). Writes nothing if nothing was
// chosen (the reader would then run past the end).
using WriteReflectedFn = void(__cdecl*)(void* object, void* fieldState, void* type, void* message, void* data);
inline WriteReflectedFn WriteReflected = At<WriteReflectedFn>(0x008F8770);
constexpr unsigned char kWriteReflectedBytes[] = {0x81, 0xEC, 0x18, 0x02, 0x00, 0x00, 0x56, 0x8B};
// The GMS message (0x20 bytes, constructor 0x00ADADF0) as GmsMessage_WriteBytes and message.size() (0x008230F0)
// use it: +0 vtable, +5 transport id (the wire byte), +8 bytes written, +0xC buffer, +0x10 capacity, +0x14 owns
// buffer, +0x18 read position, +0x1C write cursor. The host writes into its own buffer through such a struct.
constexpr unsigned char kWriteBytesBytes[] = {0x56, 0x8B, 0xF1, 0x8B, 0x46, 0x10, 0x03, 0x46};
namespace msg {
constexpr std::uintptr_t kTransportId = 0x05;
constexpr std::uintptr_t kSize = 0x08;
constexpr std::uintptr_t kBuffer = 0x0C;
}  // namespace msg
// Reflected type names (FNV-1, as gms_reflect.py and the readers use them).
constexpr std::uint32_t kTypeCreateData = 0x37748BD7;   // cGameObjectCreateData (0x74 bytes, 9 members)
constexpr std::uint32_t kTypeSporelabsObject = 0x258CF09D;  // sporelabsObject (cGameObject fields, 18 members)
constexpr std::uint32_t kTypeAttributeData = 0xCA29D1B4;    // cAttributeData (object +0x8E0, 69 members)
constexpr std::uint32_t kTypeCombatantData = 0xFAB3107F;    // cCombatantData (object +0x8EC, 5 members)
constexpr std::uint32_t kTypeLabsPlayerGlobalData = 0x7AA3CBAB;  // labsPlayerGlobalData (cLabsPlayer, 18 members)
constexpr std::uint32_t kTypeLabsPlayerFowData = 0x253E5C4A;     // labsPlayerFowData (cLabsPlayer, 63 members; D7-live)
constexpr std::uint32_t kTypeLocomotionData = 0xB6F447EF;        // cLocomotionData (object +0x8A0, 21 members; D7-live)
// A member record (0x60 bytes, type +8): +4 type hash, +8 size, +0x14 offset, +0x18 count, +0x20 default, +0x44
// frequency and flags, +0x48 condition offset, +0x4C read callback. `asset` members (hash 0x9C617503) are as wide as
// the global 0x014322FC (4) says, as the analyzer computes it.
constexpr std::uint32_t kMemberRecordSize = 0x60;
constexpr std::uint32_t kAssetTypeHash = 0x9C617503;
constexpr std::uintptr_t kAssetMemberSize = 0x014322FC;
constexpr std::uint32_t kCreateDataSize = 0x74;
namespace create {  // cGameObjectCreateData members (GameClient_OnGmsObjectCreateBegin 0x006BB040 reads them)
constexpr std::uintptr_t kNoun = 0x00;      // asset (Noun asset pointer; written as its id)
constexpr std::uintptr_t kPosition = 0x04;  // f32 x 3
constexpr std::uintptr_t kRotX = 0x10;      // f32 degrees each; the reader builds q = Rz * Ry * Rx (0x00673A90)
constexpr std::uintptr_t kRotY = 0x14;
constexpr std::uintptr_t kRotZ = 0x18;
constexpr std::uintptr_t kScale = 0x1C;     // f32
constexpr std::uintptr_t kTeam = 0x20;      // u8
constexpr std::uintptr_t kOwner = 0x24;     // tObjID, arrives unmapped
constexpr std::uintptr_t kFlags = 0x28;     // u8: 1 collision, 2 player controlled
}  // namespace create

// The outgoing vector (sim +0x1A998, 28-byte entries {kind, object id, LoS cell, recipients, message, control
// block, -}): kind 0 delete (GameSimulator_QueueObjectDelete 0x008F4370: object id, +0xC = the object's known-players
// mask), 1 recipients mask (GameSimulator_QueueOutgoingMessage 0x008F4410: +0xC; 0x7FFF = everyone, e.g.
// GameSimulator_QueueCombatEvent), 2 the players that know object +4 (GameSimulator_QueueObjectMessage 0x008F4580:
// ModifierCreated, PlayerCharacterDeploy, ...), 3 the players that see a position (GameSimulator_QueuePositionalMessage
// 0x008F4740: +8 = the line-of-sight cell). The message is a shared pointer at +0x10 (control block +0x14).
constexpr std::uint32_t kOutgoingEntrySize = 28;
// OutgoingVector_DestroyRange (0x008F1CA0, thiscall on the vector, (begin, end), ret 8): releases the entries'
// messages; GameSimulator_Shutdown (0x008F3BC0) calls it and then sets end = begin, which is how the host empties it.
using OutgoingDestroyRangeFn = void(__thiscall*)(void* vector, void* begin, void* end);
inline OutgoingDestroyRangeFn OutgoingDestroyRange = At<OutgoingDestroyRangeFn>(0x008F1CA0);
constexpr unsigned char kOutgoingDestroyRangeBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x8B, 0x4C, 0x24, 0x08};

// LabsPlayer_DeployCharacter (0x00915400): (cLabsPlayer*, const float* position[3], const float* orientation[4]).
// The position equal to the global zero vector (0x01B41A38) means the player's start point
// (GameSimulator_GetPlayerStartPoint with team +0xA0 and index +0x2C); the orientation equal to the identity at
// 0x01435E18 means "face as the start point says". Deletes the previous creature, creates mpNounDef's creature
// (0x008E2060), sets mCurrentCreatureId (+0x0C) and, on a server, its behavior tree data and
// GameSimulator_QueuePlayerCharacterDeploy (0x008F5C80: kind 2 for the creature).
using DeployCharacterFn = void(__cdecl*)(void* player, const float* position, const float* orientation);
inline DeployCharacterFn DeployCharacter = At<DeployCharacterFn>(0x00915400);
constexpr unsigned char kDeployCharacterBytes[] = {0x83, 0xEC, 0x2C, 0x53, 0x56, 0x8B, 0x74, 0x24};
constexpr std::uintptr_t kZeroVector = 0x01B41A38;
constexpr std::uintptr_t kIdentityQuaternion = 0x01435E18;

// cGameObject fields the world is built from (GMS_PROTOCOL.md *Object identity*).
namespace gobj {
constexpr std::uintptr_t kId = 0x00;
constexpr std::uintptr_t kNoun = 0x04;              // Noun asset: +0 name, +0x60 id, +0x68 data
constexpr std::uintptr_t kScale = 0x08;             // mScale
constexpr std::uintptr_t kPosition = 0x10;          // f32 x 3
constexpr std::uintptr_t kOrientation = 0x1C;       // quaternion x, y, z, w (Math_EulerToQuaternion 0x00673A90's order)
constexpr std::uintptr_t kOwner = 0x48;             // mOwnerID
constexpr std::uintptr_t kPlayerIndex = 0x4C;       // mPlayerIdx (0xFF none)
constexpr std::uintptr_t kTeam = 0x4D;              // mTeam
constexpr std::uintptr_t kPendingDelete = 0x55;
constexpr std::uintptr_t kPlayerControlled = 0x56;  // mbPlayerControlled
constexpr std::uintptr_t kHasCollision = 0x58;      // mbHasCollision
constexpr std::uintptr_t kMarkerId = 0x84;          // sourceMarkerKey.markerId
constexpr std::uintptr_t kNetwork = 0x8BC;          // network component (NetworkComponent_Create 0x00895670)
constexpr std::uintptr_t kAttributes = 0x8E0;       // cAttributeData*
constexpr std::uintptr_t kCombatant = 0x8EC;        // cCombatantData*
}  // namespace gobj
// The network component (0xA4 bytes): +4 owner object, +8 server id (client side), +0xE u16 the players that know
// the object (NetworkComponent_GetPlayerMask 0x00895480; deletes go to them), +0x10 u16 a second player mask
// (cleared with it by NetworkComponent_ClearPlayer 0x008954F0), +0x14 sporelabsObject field state. Nothing in the
// binary sets a bit; the host does when it creates the object for a player.
constexpr std::uintptr_t kNetworkKnownMask = 0x0E;
// Noun data (asset +0x68; AssetData type Noun, docs/data/assetdata_types.txt).
namespace noun {
constexpr std::uintptr_t kData = 0x68;
constexpr std::uintptr_t kId = 0x60;                // on the asset
constexpr std::uintptr_t kType = 0x000;             // nounType (enum 0x01438A48)
constexpr std::uintptr_t kHasNetworkComponent = 0x286;
constexpr std::int32_t kTypeCreature = 113409280;
constexpr std::int32_t kTypeInteractable = 158838625;
constexpr std::int32_t kTypeTower = 213110983;
constexpr std::int32_t kTypeSpawnPoint = -638914300;
// D2g-static: the other networked noun types (enum 0x01438A48)
constexpr std::int32_t kTypeProjectile = 624914268;
constexpr std::int32_t kTypeTitan = -639573535;          // the Guardians
constexpr std::int32_t kTypeAbilityObject = 1214237073;
constexpr std::int32_t kTypePlaceableEffect = 943327861;
constexpr std::int32_t kTypeLuaJob = -1567585774;
constexpr std::int32_t kTypeVisionObject = 235566511;
constexpr std::int32_t kTypeObstacle = 44938362;
}  // namespace noun

// --- live replication (D2g-live; docs/GMS_PROTOCOL.md *Live replication (D2g-static)*) -------------------------------
// The structs with an update message keep a field state: per member {u32 hash of the value last sent, u8 flags}, 8
// bytes, inline in the struct (NetworkUtils_InitFieldState 0x008F7950 seeds the hashes with the members' defaults at
// the component's creation; NetworkUtils_WriteReflectedFields stores the hash of each member it writes, and asserts
// "fieldToUpdate.fieldDataHash != 0xcdcdcdcd", so the host fills in every chosen member's hash first). The value hash
// is the analyzer's: a 1-, 2- or 4-byte member (an asset member counts 4 bytes per element) is its zero-extended value,
// anything wider FNV-1 32 over its bytes.
constexpr std::uint32_t kTypeProjectileData = 0xB1BDBEC9;   // cProjectileData (object +0x8A4, 29 members)
constexpr std::uint32_t kTypeInteractableData = 0x84A158AA;  // cInteractableData (object +0x90C, 1 member)
constexpr std::uint32_t kTypeAgentBlackboard = 0x16A4D2AC;   // cAgentBlackboard (object +0x8C4, 6 members)
namespace part {  // where each struct and its field state live
constexpr std::uintptr_t kLocomotion = 0x8A0;     // cLocomotionData*, field state +0x228 (LocomotionData_Create 0x00893090)
constexpr std::uintptr_t kProjectile = 0x8A4;     // cProjectileData*, +0xF8 (ProjectileData_Create 0x00920C40)
constexpr std::uintptr_t kBlackboard = 0x8C4;     // cAgentBlackboard*, +0x1F54 (AgentBlackboard_Create 0x008AB3E0)
constexpr std::uintptr_t kAttributes = 0x8E0;     // cAttributeData*, +0x25C (AttributeData_Create 0x008DB4A0)
constexpr std::uintptr_t kCombatant = 0x8EC;      // cCombatantData*, +0x5C (CombatantData_Create 0x0087E4C0)
constexpr std::uintptr_t kInteractable = 0x90C;   // cInteractableData*, +0xC (InteractableData_Create 0x008FC7E0)
constexpr std::uintptr_t kFsLocomotion = 0x228;
constexpr std::uintptr_t kFsProjectile = 0xF8;
constexpr std::uintptr_t kFsBlackboard = 0x1F54;
constexpr std::uintptr_t kFsAttributes = 0x25C;
constexpr std::uintptr_t kFsCombatant = 0x5C;
constexpr std::uintptr_t kFsInteractable = 0xC;
constexpr std::uintptr_t kFsNetwork = 0x14;       // sporelabsObject's field state, in the network component
constexpr std::uintptr_t kFsPlayerGlobal = 0x31C;  // labsPlayerGlobalData's, in cLabsPlayer (LabsPlayer_InitReflection)
constexpr std::uintptr_t kFsPlayerFow = 0x3AC;     // labsPlayerFowData's
}  // namespace part
// The network component's second mask: the players that currently see the object (NetworkComponent_ClearPlayer
// 0x008954F0 always clears it); kind-2 messages and deltas go to known & visible. Without fog of war the host sets it
// together with the known mask.
constexpr std::uintptr_t kNetworkVisibleMask = 0x10;
// The network-rate timers (D2g-static, *Movement*): GameSimulator_UpdateNetworkRateTimers (0x008EE900; ESI = the
// simulator, saves EBX and ESI, clobbers EAX/ECX/EDX) adds the frame's ms (GameTimeManager_GetDeltaMs 0x008F9930) to
// five timers at sim +0x1C5B0 (100, 200, 500, 1000, 5000 ms) and builds the mask at sim +0x1C600 (6 | 0x8, 0x10, 0x20,
// 0x40, 0x80 for the timers that fired; GameSimulator_BuildNetworkRateMask 0x008EE8A0). Its per-frame caller is
// stripped; Locomotion_TickMovers marks a non-player mover's sample for sending only in frames with 0x10.
constexpr std::uintptr_t kUpdateNetworkRateTimers = 0x008EE900;
constexpr unsigned char kUpdateNetworkRateTimersBytes[] = {0x53, 0x56, 0xE8};
// The line-of-sight manager (sim +0x56C, a pointer; LineOfSight.cpp). A kind-3 entry of the outgoing vector holds one
// of its tracked records at entry +8 (LineOfSight_AllocTracked 0x00917450 from GameSimulator_QueuePositionalMessage
// 0x008F4740); the stripped drain released it, OutgoingVector_DestroyRange doesn't. LineOfSight_ReleaseTracked
// (0x00917670, thiscall (manager, record**), ret 4) unlinks it, returns it to the pool and nulls *record. Pool: +0x1A030
// record pointers, +0x1A034 in use, +0x1A038 capacity ("ERROR - LineOfSight pool is exhausted").
using ReleaseTrackedFn = void(__thiscall*)(void* manager, void** record);
inline ReleaseTrackedFn ReleaseTracked = At<ReleaseTrackedFn>(0x00917670);
constexpr unsigned char kReleaseTrackedBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x8B, 0x06, 0x8B, 0x40, 0x04};
namespace los {
constexpr std::uintptr_t kOfSim = 0x56C;
constexpr std::uintptr_t kPoolInUse = 0x1A034;
constexpr std::uintptr_t kPoolCapacity = 0x1A038;
}  // namespace los

// --- replication extras (D2g-live step 3; docs/GMS_PROTOCOL.md *The replication extras (D2g-live step 3)*) ----------
// The network component's flags (+0x0C): bit 1 kNetFlag_SentCreationMessage (the checker 0x00895450 reads it; asserted
// by nEffect.UpdateAttachedEffectTarget 0x0094A550). GameObject_CreateAttachedEffect (0x00881D30) and
// GameObject_DestroyAttachedEffect (0x00880A40) send their ServerEvents (A4, kinds 2 and 1) only while it is set; the
// stripped creation path set it, so the host sets it with an object's first create.
constexpr std::uintptr_t kNetworkFlags = 0x0C;
constexpr std::uint8_t kNetFlagSentCreationMessage = 0x01;
// cGameObject's 16 attached-effect slots (+0x90, 0x38 bytes each; the reflected cAttachedEffectData's offsets are the
// slot's: +0x10 mEffectAsset (0 = empty), +0x14 target, +0x18 point, +0x24 facing, +0x30 caster index, +0x31 caster
// team, +0x34 attach handle). AttachedEffectUpdate (92) carries the whole set (GameClient_OnGmsAttachedEffectUpdate
// 0x006BA9E0 starts from 16 default slots and restarts a slot whose asset changed; it asserts "curData.mEffectAsset !=
// newData.mEffectAsset || curData.mAttachHandle == newData.mAttachHandle").
constexpr std::uint32_t kTypeAttachedEffectData = 0x2C41E42B;  // 7 members, no field state
constexpr std::uintptr_t kAttachedEffects = 0x90;
constexpr std::uint32_t kAttachedEffectSize = 0x38;
constexpr int kNumAttachedEffects = 16;
constexpr std::uintptr_t kAttachedEffectAsset = 0x10;
// cAnimationData (object +0x894, no field state): members 0-6 mOverrideAnimations[7] (+0x2C, u32 each, every update),
// 7-11 the last animation (+0x08 state, +0x10 u64 play time in server ms, +0x18 f32 time scale, +0x1C overlay state,
// +0x20 u64 overlay time; full state only), which InitAnimationInfo (B1, 32 bytes, GameClient_OnGmsInitAnimationInfo
// 0x006BA330: only for an object with graphics +0x890) carries.
constexpr std::uint32_t kTypeAnimationData = 0x784B5D66;  // 12 members
namespace anim {
constexpr std::uintptr_t kOfObject = 0x894;
constexpr std::uintptr_t kLastState = 0x08;
constexpr std::uintptr_t kLastPlayTime = 0x10;
constexpr std::uintptr_t kLastTimeScale = 0x18;
constexpr std::uintptr_t kOverlayState = 0x1C;
constexpr std::uintptr_t kOverlayPlayTime = 0x20;
constexpr std::uintptr_t kOverrides = 0x2C;
constexpr int kNumOverrides = 7;
}  // namespace anim
// cBattlegroundsUICampData (the camp list at UI data +0x368, count +0x36C; 0x18 bytes: +0 mCampVisibleToTeam (u8),
// +4 mSpawnMarkerId (full state only), +8 field state); UICreepCampUpdate (8D): u32 camp mask, a body per set bit
// (GameClient_OnGmsUICreepCampUpdate 0x006B7CE0 walks the client's list up to its count).
constexpr std::uint32_t kTypeUICampData = 0xC1622B6F;  // 2 members
constexpr std::uint32_t kCampSize = 0x18;
namespace camp {
constexpr std::uintptr_t kSpawnMarkerId = 0x04;
constexpr std::uintptr_t kFieldState = 0x08;
}  // namespace camp

// --- command intake (D7-live; docs/SERVER_MODE.md *Command intake on the server (D7-static)* and *(D7-live)*) ----
// The server's own command (0x48 bytes), as ServerCommand_Init (0x00922850, thiscall, no arguments) leaves it: slot
// -1, rank 1, zero vectors. Field meanings from its readers in ServerCommandQueue_TickPlayer and the Execute functions.
constexpr std::uint32_t kServerCommandSize = 0x48;
namespace scmd {
constexpr std::uintptr_t kValid = 0x00;          // u8; QueueCommand sets 1
constexpr std::uintptr_t kType = 0x04;           // u32: the wire type 1-11
constexpr std::uintptr_t kSeq = 0x08;            // u8: answers are sent only if != 0
constexpr std::uintptr_t kCaster = 0x0C;         // tObjID; must be the player's creature (ValidateCaster 0x00922790)
constexpr std::uintptr_t kPosition = 0x10;       // f32 x 3: move target (Locomotion_MoveToPoint)
constexpr std::uintptr_t kGoalFlags = 0x1C;      // u32, OR'd into locomotion +0xB8
constexpr std::uintptr_t kWaitUntilFree = 0x20;  // u8 [meaning assumed in D7-static]
constexpr std::uintptr_t kKeepAttacking = 0x24;  // u32: 1 = a basic attack repeats
constexpr std::uintptr_t kRank = 0x28;           // u32
constexpr std::uintptr_t kTarget = 0x2C;         // tObjID
constexpr std::uintptr_t kTargetPos = 0x30;      // f32 x 3
constexpr std::uintptr_t kUnknown3C = 0x3C;      // 0 in every server-built command
constexpr std::uintptr_t kSlot = 0x40;           // i32 (ServerCommand_SetAbilitySlot); -1 = none
constexpr std::uintptr_t kGuid = 0x44;           // u32 (ServerCommand_SetAbilityGUID); 0 = none
}  // namespace scmd
using ServerCommandInitFn = void(__thiscall*)(void* cmd);
inline ServerCommandInitFn ServerCommandInit = At<ServerCommandInitFn>(0x00922850);
constexpr unsigned char kServerCommandInitBytes[] = {0x33, 0xC0, 0x88, 0x01, 0x89, 0x41, 0x04, 0x88};
// ServerCommand_SetAbilitySlot (0x00922950) / ServerCommand_SetAbilityGUID (0x009227F0): thiscall (cmd, value), ret 4;
// each asserts if the other one is already set.
using ServerCommandSetFn = void(__thiscall*)(void* cmd, int value);
inline ServerCommandSetFn ServerCommandSetSlot = At<ServerCommandSetFn>(0x00922950);
inline ServerCommandSetFn ServerCommandSetGuid = At<ServerCommandSetFn>(0x009227F0);
constexpr unsigned char kServerCommandSetSlotBytes[] = {0x56, 0x8B, 0xF1, 0x83, 0x7E, 0x44, 0x00, 0x57};
constexpr unsigned char kServerCommandSetGuidBytes[] = {0x56, 0x8B, 0xF1, 0x83, 0x7E, 0x40, 0xFF, 0x57};
// ServerCommandQueue_QueueCommand (0x00923E60): cdecl (u8 player, ServerCommand*, bool tickNow). Bots are ignored, the
// caster validated; types 1, 2, 9 go to the queue's move slot without a tick, every other type to its ability slot,
// ticked at once with tickNow.
using QueueCommandFn = void(__cdecl*)(unsigned player, void* cmd, int tickNow);
inline QueueCommandFn QueueCommand = At<QueueCommandFn>(0x00923E60);
constexpr unsigned char kQueueCommandBytes[] = {0x83, 0xEC, 0x08, 0x53, 0x56, 0x8B, 0x74, 0x24};
// ServerCommandQueue_TickPlayer (0x00923450): cdecl (cLabsPlayer*, queue*). Its per-frame caller is stripped; the host
// calls it every tick. ServerCommandQueue_GetPlayerQueue (0x00923230): cdecl (u8 player) -> the 0x130-byte queue of the
// current simulator (created on first use; asserts player < 10).
using TickPlayerFn = void(__cdecl*)(void* player, void* queue);
inline TickPlayerFn TickPlayerQueue = At<TickPlayerFn>(0x00923450);
constexpr unsigned char kTickPlayerBytes[] = {0x81, 0xEC, 0x64, 0x02, 0x00, 0x00, 0x53, 0x55};
using GetPlayerQueueFn = void*(__cdecl*)(unsigned player);
inline GetPlayerQueueFn GetPlayerQueue = At<GetPlayerQueueFn>(0x00923230);
namespace pqueue {
constexpr std::uintptr_t kCurrentAbility = 0x00;  // ServerCommand
constexpr std::uintptr_t kQueuedAbility = 0x48;   // ServerCommand (every non-move type)
constexpr std::uintptr_t kCurrentMove = 0x90;     // ServerCommand
constexpr std::uintptr_t kQueuedMove = 0xD8;      // ServerCommand (types 1, 2, 9)
constexpr std::uintptr_t kDeferred = 0xB0;        // u8, the current move's +0x20 (D7c: engaged attack-move / paused move)
constexpr std::uintptr_t kIdleStamp = 0x120;      // u64: the time the creature stood still (0 while it moves)
constexpr std::uintptr_t kStopped = 0x128;        // u8: no auto-acquire (type 8)
constexpr std::uintptr_t kHolding = 0x129;        // u8: no chasing (type 11)
}  // namespace pqueue
// D7c (docs/SERVER_MODE.md *A move during an attack order (D7c)*): TickPlayer's queued-move step, after IsBusy(0) said
// free and the move is not a stop moving (ESI = the queued move, [ESP+0x27C] = the queue):
//   0092396D  mov eax,[esp+27Ch]; cmp dword [eax+94h],9; jne 0092398F    (current move an attack-move?)
//   0092397D  cmp byte [eax+90h],0; je 0092398F; cmp byte [eax+0B0h],0; jne 00923996   (valid? engaged: no execute)
//   0092398F  mov eax,esi; call ServerCommandQueue_ExecuteMove (0x00922630)
//   00923996  ... the queued move becomes the current one with the old +0xB0 byte
// The host replaces the first 16 bytes with a jump to its stub (cmds' MoveTakeoverStub), which asks the host's rule and
// either runs the check as the binary does (back to 0x0092397D) or goes to the ExecuteMove at 0x0092398F.
constexpr std::uintptr_t kQueuedMoveBranch = 0x0092396D;
constexpr std::uintptr_t kQueuedMoveCheckValid = 0x0092397D;
constexpr std::uintptr_t kQueuedMoveExecute = 0x0092398F;
constexpr unsigned char kQueuedMoveBranchBytes[] = {0x8B, 0x84, 0x24, 0x7C, 0x02, 0x00, 0x00, 0x83,
                                                    0xB8, 0x94, 0x00, 0x00, 0x00, 0x09, 0x75, 0x12};
// AbilityDataContainer_GetAgentAbility (0x008A8800): cdecl (object id) -> the instance id holding the agent (container
// +0x53C, the behavior tree's UseAbility instance, else +0x540 if an agent ability), 0 = none.
using GetAgentAbilityFn = std::uint32_t(__cdecl*)(std::uint32_t objectId);
inline GetAgentAbilityFn GetAgentAbility = At<GetAgentAbilityFn>(0x008A8800);
// cStaticAbility (instance +8) flags the queue's rules read (docs/data/assetdata_types.txt, `ability`):
constexpr std::uintptr_t kStaticRequiresAgent = 0x57;       // bool
constexpr std::uintptr_t kStaticAllowsMovement = 0x98;      // bool (StaticAbility_LetsAgentMove 0x008FDDC0)
constexpr std::uintptr_t kStaticSelfInterruptible = 0x3EA;  // bool (ServerCommandQueue_AgentAbilitySelfInterruptible)
// LabsPlayer_GetAbilityRank (0x00911B50): cdecl (cLabsPlayer*, slot) -> +0x2A8 + slot * 4 for slots 3-6, else 1.
using GetAbilityRankFn = int(__cdecl*)(void* player, int slot);
inline GetAbilityRankFn GetAbilityRank = At<GetAbilityRankFn>(0x00911B50);
// LabsPlayer_RankUpAbility (0x009149C0): cdecl (cLabsPlayer*, slot 3-6): rank + 1 if below the level's maximum
// (0x009136A0) and ability points (+0x1C) remain; on a server also the creature's ability rank (0x00907B20).
using RankUpAbilityFn = void(__cdecl*)(void* player, int slot);
inline RankUpAbilityFn RankUpAbility = At<RankUpAbilityFn>(0x009149C0);
constexpr unsigned char kRankUpAbilityBytes[] = {0x64, 0xA1, 0x00, 0x00, 0x00, 0x00, 0x6A, 0xFF};
namespace player {
constexpr std::uintptr_t kAbilityPoints = 0x1C;   // mAbilityPoints (labsPlayerFowData member 5)
constexpr std::uintptr_t kLevel = 0xB0;           // mLevel (member 4)
constexpr std::uintptr_t kAbilityRanks = 0x2A8;   // + slot * 4, slots 3-6 (mAbilityRankMap, members 9-12)
}  // namespace player
// The sixth seam (D7-static): GameSimulator_SendToPlayerListStub (0x008F9090, cdecl (12-byte message holder
// {message*, control block*, -} by value, u32 player mask, u32 1), caller cleans): releases the holder
// (SharedPtr_Release 0x00515440, ECX = holder) and returns 0, so ActionCommandResponse, PauseMsgs, CheatResponse,
// SetObjectGfxState, ObjectJump, ComponentMessage and one ClientFeedbackEvent path are built and dropped. The host
// replaces it (12 bytes, a relative call inside: replaced, not trampolined).
constexpr std::uintptr_t kSendToPlayerListStub = 0x008F9090;
constexpr unsigned char kSendToPlayerListStubBytes[] = {0x8D, 0x4C, 0x24, 0x04, 0xE8, 0xA7, 0xC3, 0xC1,
                                                        0xFF, 0x33, 0xC0, 0xC3};
using SharedPtrReleaseFn = void(__fastcall*)(void* holder);
inline SharedPtrReleaseFn SharedPtrRelease = At<SharedPtrReleaseFn>(0x00515440);
// GameSimulator_SendActionCommandFailed (0x008F0110): cdecl (?, u8 player, u8 seq, u32 command type, u32 guid, i32
// result); sends nothing when seq is 0, and its message leaves +5 (the sequence) at 0. The host's detour remembers the
// sequence for the stub detour.
constexpr std::uintptr_t kSendActionCommandFailed = 0x008F0110;
constexpr unsigned char kSendActionCommandFailedBytes[] = {0x64, 0xA1, 0x00, 0x00, 0x00, 0x00, 0x6A, 0xFF};
using SendActionCommandFailedFn = void(__cdecl*)(unsigned unused, unsigned player, unsigned seq, unsigned type,
                                                 unsigned guid, int result);
// cLocomotionData (cGameObject +0x8A0, mpLocomotionData; Locomotion_MoveToPoint 0x00891600 asserts it): +0xB8 goal
// flags (1 = move to point), +0xC8 the goal position, +0xE0/+0xE2 mClientPathBlock/XY (the quantized position the
// client reads), +0x11C mTargetPosition.
namespace loco {
constexpr std::uintptr_t kOfObject = 0x8A0;
constexpr std::uintptr_t kGoalFlags = 0xB8;
constexpr std::uintptr_t kGoal = 0xC8;
constexpr std::uintptr_t kClientPathBlock = 0xE0;
constexpr std::uintptr_t kClientPathXY = 0xE2;
}  // namespace loco

// --- item transactions, emotes and voice lines (D7b; docs/SERVER_MODE.md *Item transactions, emotes and voice lines on
// the host (D7b)*) ---------------------------------------------------------------------------------------------------
// GameSimulator_OnItemTransaction (0x008F1390) validates and applies the five transactions on the server's cLabsPlayer
// with these functions (all cdecl); the host calls the same switch with the connection's player, since the dispatcher
// passes 0xFF and the answer function is the emptied 0x009A3F00. Names in [brackets] are D7b's proposals (Ghidra
// read-only during D7b).
// [LabsPlayer_CanUseItemShop] (0x00913B10, nPlayer.CanUseItemShop): player, its +0x10 bit 5 clear, and its creature dead
// or within reach of the shop ([LabsPlayer_InProximityToItemShop] 0x009137C0, nPlayer.InProximityToItemShop: the
// creature within its footprint radius + the float at 0x01437FFC of its team's base object, sim +0x19E30 + team * 4, the
// "baseMarker" marker GameWorld_CreateMarkerObject registers through 0x008EF210). 0x01437FFC holds 16.0 in the file but
// 40.0 at run time: 0x00916380 loads it at start-up from ServerData.package's property list 00B1B104-3B01D7F6-3B01D7F6,
// property 0x3AD22078 (D7b; measured: Fenmore left the reach at 41.92 units, footprint 1.77 + 40).
using PlayerCheckFn = bool(__cdecl*)(void* player);
inline PlayerCheckFn CanUseItemShop = At<PlayerCheckFn>(0x00913B10);
constexpr unsigned char kCanUseItemShopBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x74};
inline PlayerCheckFn InProximityToItemShop = At<PlayerCheckFn>(0x009137C0);
constexpr std::uintptr_t kSimBaseObjects = 0x19E30;  // cGameObject* per team 0-2 (mBaseObjects)
constexpr std::uintptr_t kItemShopReach = 0x01437FFC; // f32 global, added to the creature's footprint radius (40.0)
// [Item_IsSpell] (0x008FCD00): Item.Category == 1 (the "Ability" items Spell_*: slots 6-8, bought with spell points
// anywhere).
using ItemCheckFn = bool(__cdecl*)(void* itemAsset);
inline ItemCheckFn ItemIsSpell = At<ItemCheckFn>(0x008FCD00);
// LabsPlayer_CheckPurchase (0x00912E30): (player, item, u8* slot in/out, int* cost out) -> 0 refused, 1 purchase, 2
// upgrade (the cost less the component's), 3 consume on purchase (Item.ConsumeOnFullInventory with no free slot; alive
// only). The cost must fit mAvailableGold (player +0xA8). A slot >= 11 lets it pick a free one.
using CheckPurchaseFn = int(__cdecl*)(void* player, void* item, std::uint8_t* slot, int* cost);
inline CheckPurchaseFn CheckPurchase = At<CheckPurchaseFn>(0x00912E30);
constexpr unsigned char kCheckPurchaseBytes[] = {0x8B, 0x44, 0x24, 0x10, 0x83, 0xEC, 0x08, 0x56};
// LabsPlayer_PurchaseItem (0x00914690): (player, item, slot, cost): adds or upgrades the slot, mAvailableGold -= cost, a
// spell takes a spell point; on a server the item's passives (0x00912950) and AttributeData_Recalculate.
using PurchaseItemFn = void(__cdecl*)(void* player, void* item, unsigned slot, int cost);
inline PurchaseItemFn PurchaseItem = At<PurchaseItemFn>(0x00914690);
constexpr unsigned char kPurchaseItemBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8, 0x81, 0xEC};
// LabsPlayer_CanSellItem (0x009132E0) / LabsPlayer_SellItem (0x00914080): (player, item, slot); the sale pays the
// item's sell value (0x008FCBF0) and removes it.
using ItemSlotCheckFn = bool(__cdecl*)(void* player, void* item, unsigned slot);
using ItemSlotActionFn = void(__cdecl*)(void* player, void* item, unsigned slot);
inline ItemSlotCheckFn CanSellItem = At<ItemSlotCheckFn>(0x009132E0);
inline ItemSlotActionFn SellItem = At<ItemSlotActionFn>(0x00914080);
constexpr unsigned char kSellItemBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x0F};
// [LabsPlayer_CanUseItem] (0x00913560) / [LabsPlayer_UseItem] (0x00914160): (player, item, slot): alive, not
// Suppressed, the slot holds the item, the item is activatable; the use requests Item.ActiveAbility or activates
// Item.ActiveModifier on a server and removes one of a consumable's stack (D7-static's "undo").
inline ItemSlotCheckFn CanUseItem = At<ItemSlotCheckFn>(0x00913560);
inline ItemSlotActionFn UseItem = At<ItemSlotActionFn>(0x00914160);
constexpr unsigned char kUseItemBytes[] = {0x53, 0x55, 0x8B, 0x6C, 0x24, 0x0C, 0x56, 0x57};
// LabsPlayer_ConsumeItem (0x00913100): (player, item, cost): pays and, on a server, uses the item at once.
using ConsumeItemFn = void(__cdecl*)(void* player, void* item, int cost);
inline ConsumeItemFn ConsumeItem = At<ConsumeItemFn>(0x00913100);
// LabsPlayer_CanSwapItems (0x00911A40): (player, slot, slot 2): both in 6-8 (the spells). LabsPlayer_SwapItems
// (0x00913360): (player, item, slot, item 2, slot 2).
using CanSwapItemsFn = bool(__cdecl*)(void* player, unsigned slot, unsigned slot2);
inline CanSwapItemsFn CanSwapItems = At<CanSwapItemsFn>(0x00911A40);
using SwapItemsFn = void(__cdecl*)(void* player, void* item, unsigned slot, void* item2, unsigned slot2);
inline SwapItemsFn SwapItems = At<SwapItemsFn>(0x00913360);
constexpr unsigned char kSwapItemsBytes[] = {0x83, 0xEC, 0x30, 0x57, 0x8B, 0x7C, 0x24, 0x38};
// cLabsPlayer's inventory (labsPlayerFowData members 19-62): mItems[11] of 0x24 bytes at +0xB4 {asset +0, count +0x18,
// upgradePath +0x1C/+0x20}; mSpellPoints +0x20.
namespace player {
constexpr std::uintptr_t kItems = 0xB4;
constexpr std::uint32_t kItemSize = 0x24;
constexpr std::uintptr_t kItemCount = 0x18;
// Q3: the item's passive instance ids, 4 per slot (+8..+0x14): LabsPlayer_OnItemAdded (0x00912B20) activates the item
// data's passives (Item_GetData +0x78/+0x7C) and stores the ids (0x00910E10); LabsPlayer_OnItemRemoved (0x00913C50) ends
// them by these ids (0x00910EF0 asserts on a stale one, LabsPlayer.cpp 278).
constexpr std::uintptr_t kItemPassives = 0x08;
constexpr int kItemPassiveCount = 4;
constexpr unsigned kItemSlots = 11;
constexpr std::uintptr_t kSpellPoints = 0x20;
}  // namespace player
// An asset: +0 name (char*), +0x60 id, +0x68 data. Item data (AssetData "Item"): +0x0 Category, +0x10 ItemCost.
namespace asset {
constexpr std::uintptr_t kName = 0x00;
constexpr std::uintptr_t kId = 0x60;
constexpr std::uintptr_t kData = 0x68;
}  // namespace asset
constexpr std::uintptr_t kItemCost = 0x10;
// The shaper's PlayerClass (LabsPlayer_GetAbilityInSlot 0x00911B80's walk): player +4 (mpNounDef) -> +0x68 noun data ->
// +0x1D8 playerClassData asset -> +0x68 PlayerClass data; emoteAbility's hash at +0x104 (the Key member +0x110, delta
// -12, as specialAbility1's +0x54 for +0x60). PC_Alpha's is SPID("ability_dance") 0x41AE3D59.
constexpr std::uintptr_t kNounPlayerClass = 0x1D8;
constexpr std::uintptr_t kPlayerClassEmoteAbility = 0x104;
// D6c: LabsPlayer_GetAbilityInSlot (0x00911B80, cdecl (player, slot, lookup 0 current / 1 default / 2 alternate)):
// the GUID of a slot's ability from the shaper's PlayerClass (0 basic +0x1C or alternateBasic +0x94, 1 ward +0xF4,
// 2 passive +0xE4, 3-6 specialAbility1-4 +0x54.. or alternateAbility1-4 +0xA4..) or an item's active (7-11).
using GetAbilityInSlotFn = std::uint32_t(__cdecl*)(void* player, int slot, int lookup);
inline GetAbilityInSlotFn GetAbilityInSlot = At<GetAbilityInSlotFn>(0x00911B80);
constexpr int kSlotPassive = 2;
// D6c: LabsAbilities_FindStaticAbility (0x0090E430, cdecl GUID): the cStaticAbility an instance's +8 points to, or null.
using FindStaticAbilityFn = void*(__cdecl*)(std::uint32_t guid);
inline FindStaticAbilityFn FindStaticAbility = At<FindStaticAbilityFn>(0x0090E430);
// D6c: LabsAbilities_ActivateFromDefinition (0x00908AC0, cdecl): a modifier from its GUID on `agent` by `caster` (the
// stacking and immunity checks, then LabsAbilities_ActivateAbility 0x0090A390); AgentBrain_Init passes (passive, self,
// self, 0, npcRank, the zero vector, 0) for an AI definition's passive. Returns the modifier instance id or 0.
using ActivateFromDefinitionFn = std::uint32_t(__cdecl*)(std::uint32_t guid, std::uint32_t agent, std::uint32_t caster,
                                                         std::uint32_t snapshot, int rank, float x, float y, float z,
                                                         std::uint32_t extra);
inline ActivateFromDefinitionFn ActivateFromDefinition = At<ActivateFromDefinitionFn>(0x00908AC0);
// ClientFeedbackEvent (reflected, 0x24 bytes; ClientFeedbackEvent_Init 0x008DF170 zeroes it and sets +0 / +0x1C from the
// def): +0 eventId (the def's asset id), +4 objectId, +8 targetId, +0xC uintVal (the sound's ~uintdata~), +0x10
// vectorVal, +0x1C eventDef, +0x20 objectNoun.
constexpr std::uint32_t kClientFeedbackEventSize = 0x24;
namespace feedback {
constexpr std::uintptr_t kEventId = 0x00;
constexpr std::uintptr_t kObject = 0x04;
constexpr std::uintptr_t kTarget = 0x08;
constexpr std::uintptr_t kUint = 0x0C;
constexpr std::uintptr_t kEventDef = 0x1C;
}  // namespace feedback
// GameSimulator_SendClientFeedbackEvent (0x008F71D0): (event*, u32 player mask, bool through the send stub); 0 queues it
// on the outgoing vector with the mask (kind 1), as the nClientFeedback.SendEventTo* bindings (0x0094B970, ...) do.
using SendClientFeedbackEventFn = void(__cdecl*)(void* event, unsigned mask, bool viaStub);
inline SendClientFeedbackEventFn SendClientFeedbackEvent = At<SendClientFeedbackEventFn>(0x008F71D0);
constexpr unsigned char kSendClientFeedbackEventBytes[] = {0x6A, 0xFF, 0x68, 0xF1, 0x3E, 0x0A, 0x01};
// GameSimulator_GetPlayerMaskForTeams (0x008F08D0): (sim, u8 team bits (1 << team), bool spectators, bool skip status 4).
using PlayerMaskForTeamsFn = unsigned(__cdecl*)(void* sim, unsigned teamBits, bool spectators, bool skipStatus4);
inline PlayerMaskForTeamsFn PlayerMaskForTeams = At<PlayerMaskForTeamsFn>(0x008F08D0);
constexpr unsigned char kPlayerMaskForTeamsBytes[] = {0x83, 0xEC, 0x0C, 0x80, 0x7C, 0x24, 0x14, 0x00};

// --- banter (D11b; docs/GMS_PROTOCOL.md *The shapers' banter and kill lines (D11b)*) ---------------------------------
// Tuning.BanterTuning's asset ref (the static initializer 0x0110E590 constructs it with 0x008D4EC0; nothing else reads
// it): +0 the asset (+0x68 its BanterTuning data, 0x3C bytes, gms/core/banter Tuning's offsets).
constexpr std::uintptr_t kBanterTuningRef = 0x019D03D4;
namespace banterdata {
constexpr std::uintptr_t kNounBanterData = 0x2FC;    // Noun.BanterData (CharacterBanterData asset)
constexpr std::uintptr_t kNounFeedbackToken = 0x318;  // Noun.clientFeedbackTokenName (char*; hutank_catbus only)
constexpr std::uintptr_t kPlayerClassFeedbackToken = 0x04;  // PlayerClass.clientFeedbackTokenName (char*)
// The object's class asset as ClientFeedback_GetObjectTokenName's helper 0x008DDE50 reads it: +0x74 (a player's
// creature: its PlayerClass) when mbPlayerControlled, else +0x78 (the NonPlayerClass); +0x68 its data.
constexpr std::uintptr_t kObjectPlayerClass = 0x74;
// CharacterBanterData (0x10): {targets, count}, {sequentialEmote ints, count}; a target (0x14): {noun asset pointers,
// count}, friendlyCount, enemyCount, killCount.
constexpr std::uintptr_t kTargets = 0x0;
constexpr std::uintptr_t kSequentialEmotes = 0x8;
constexpr std::uintptr_t kTargetSize = 0x14;
constexpr std::uintptr_t kTargetNouns = 0x0;
constexpr std::uintptr_t kTargetFriendly = 0x8;
constexpr std::uintptr_t kTargetEnemy = 0xC;
constexpr std::uintptr_t kTargetKill = 0x10;
}  // namespace banterdata
// mVisibleToTeam (sporelabsObject member 0): bit 1 << team per team that sees the object (fow.cpp writes it).
constexpr std::uintptr_t kObjectVisibleToTeam = 0x888;

// --- agent AI (D1b-live; docs/AGENT_AI.md) ------------------------------------------------------------------------
// cAgentBlackboard (object +0x8C4, 0x1F90 bytes; AgentBlackboard_Construct 0x008AB0E0 lays out the vectors as EASTL
// fixed vectors {begin, end, capacity end, -, inline buffer}).
namespace agent {
constexpr std::uintptr_t kOfObject = 0x8C4;
constexpr std::uintptr_t kOwner = 0x04;
constexpr std::uintptr_t kUpdateTimer = 0x0C;       // ms; init GameSimulator_RandomInt(500), nAgent.ForceAggroListUpdate 0
constexpr std::uintptr_t kPerceptionBegin = 0x10;   // {u32 id, f32 distance}, inline 128 at +0x24
constexpr std::uintptr_t kPerceptionEnd = 0x14;
constexpr std::uintptr_t kPerceptionCap = 0x18;
constexpr std::uintptr_t kAggroBegin = 0x430;       // 0x30-byte entries, inline 128 at +0x448
constexpr std::uintptr_t kAggroEnd = 0x434;
constexpr std::uintptr_t kAggroCap = 0x438;
constexpr std::uintptr_t kTarget = 0x1E5C;          // mTarget
// D6d: the target position (f32 x 3; the constructor 0x008AB0E0 sets the zero vector, nothing else in the client writes
// it). AgentBlackboard_GetTargetPosition (0x008AAA90) and nAgent.GetTargetPosition read it: the Pursue leaf
// (BTNode_Pursue_Evaluate 0x008A8CA0) runs when the best target lives or this point isn't zero and hands it to
// nBehavior_Follow, which walks to it (MoveToPointWithinRange, 0.5) when the best target is none.
constexpr std::uintptr_t kTargetPosition = 0x1E64;
constexpr std::uintptr_t kInCombat = 0x1E70;        // nGameObject.IsInCombat
constexpr std::uintptr_t kEngaged = 0x1E71;         // mbHasEngaged: behavior tree flag 0x10
constexpr std::uintptr_t kFlag1E72 = 0x1E72;        // NPC_UpdateLevelForMatchTime's in-combat veto
constexpr std::uintptr_t kBattleReady = 0x1E73;     // mbBattleReady
constexpr std::uintptr_t kAiOff = 0x1F85;           // GetBestTarget and auto-attack return none when 1
constexpr std::uintptr_t kTetherDisabled = 0x1F86;
constexpr std::uintptr_t kPerceptionProxy = 0x1F8C;
constexpr std::uint32_t kAggroEntrySize = 0x30;     // +0 id, +8 u64 taunt start, +0x10 u64 taunt end, +0x18 u64 last
                                                    // report by a player-controlled source, +0x20 the same for others,
                                                    // +0x28 u8 flag
constexpr std::uint32_t kAggroEntryFlag = 0x28;     // u8, the adder's last argument, set only on insertion (D5: the
                                                    // host's base-structure mark, gms/core/agents/aggro.h)
constexpr std::uint32_t kPerceptionEntrySize = 8;
}  // namespace agent
// NonPlayerClass (object +0x78 -> asset, data +0x68): the AI ranges.
namespace npc {
constexpr std::uintptr_t kOfObject = 0x78;
constexpr std::uintptr_t kAggroRange = 0x58;
constexpr std::uintptr_t kCallForHelpRange = 0x5C;
constexpr std::uintptr_t kDropAggroRange = 0x60;
constexpr std::uintptr_t kTetherRange = 0x64;
constexpr std::uintptr_t kType = 0x68;               // nNPCType: 0 melee minion, 1 ranged minion, ...
constexpr std::uintptr_t kRank = 0x6C;
}  // namespace npc
constexpr std::uintptr_t kObjectTeam = 0x4D;         // u8 mTeam (gobj::kTeam)
// AgentBlackboard_GetPerceptionCircle (0x008AA4A0, thiscall agent, ret 0xC): centre (the object or its perception
// proxy, resolved when the last argument is non-zero) and radius max(aggroRange, callForHelpRange) + 5 (20 + 5 without
// an NPC class, 30 + 5 for bots).
using PerceptionCircleFn = void(__thiscall*)(void* agent, float* centre, float* radius, void* objectManager);
inline PerceptionCircleFn PerceptionCircle = At<PerceptionCircleFn>(0x008AA4A0);
constexpr unsigned char kPerceptionCircleBytes[] = {0xF3, 0x0F, 0x10, 0x05, 0xB0, 0xDB, 0x23, 0x01};
// The prologues the agent step checks before it calls the functions below.
constexpr unsigned char kAddToAggroListBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x85, 0xC0, 0x74, 0x24};
constexpr unsigned char kGetBestTargetBytes[] = {0x57, 0x8B, 0xF9, 0x80, 0xBF, 0x85, 0x1F, 0x00};
constexpr unsigned char kIsInTetherBytes[] = {0x83, 0xEC, 0x0C, 0x56, 0x8B, 0xF1, 0x80, 0xBE};
constexpr unsigned char kCallForHelpBytes[] = {0x64, 0xA1, 0x00, 0x00, 0x00, 0x00, 0x6A, 0xFF};
constexpr unsigned char kFootprintRadiusBytes[] = {0x83, 0xEC, 0x14, 0x56, 0x8B, 0x74, 0x24, 0x1C};
constexpr unsigned char kFireEventBytes[] = {0x6A, 0xFF, 0x68, 0x0C, 0x42, 0x0A, 0x01};
// GameObjects_GetInRadius (0x008820B0, cdecl): the spatial query behind nObjectManager.GetObjectsInRadius: the
// broadphase into `out` (asserts unless its raw count < max; max must be <= 256, the physics query's own buffer, or it
// asserts in GamePhysicsPhysX.cpp 1151 on every query, d1b_run2), then GameObjects_FilterAndSortQuery (0x00881E00):
// target types relative to the team and `self`, not dead, targetable (unless flag 1), ... sorted by sortType. -> count.
struct ObjectQuery {
  std::uint32_t flags;        // nGetObjectsFlags: 1 FindUntargetableObjects, 2 IgnoreNeutralUnits, 4 CheckFoWVisibility
  std::uint32_t sortType;     // nGetObjectsSortType: 0 none, 1 distance, 2 distance with players first, 3/4 furthest
  std::uint32_t targetTypes;  // nTargetType bits: 1/2/4/8 enemy players/NPCs/structures/others, 16-128 the same allied,
                              // 256 self
  std::uint8_t team;
  std::uint8_t pad[3];
  std::uint32_t ignoreModifier;  // objects carrying this modifier GUID are left out (0 none)
  std::uint32_t self;            // the asking object (for Self and "allied")
  std::uint32_t descLo, descHi;  // descriptors (immunity checks)
};
static_assert(sizeof(ObjectQuery) == 0x20, "ObjectQuery layout");
using GetInRadiusFn = int(__cdecl*)(std::uint32_t* out, int max, float x, float y, float z, float radius,
                                    const ObjectQuery* query);
inline GetInRadiusFn GetInRadius = At<GetInRadiusFn>(0x008820B0);
constexpr unsigned char kGetInRadiusBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8, 0x81, 0xEC};
// Agent_AddToAggroList (0x008AB710, cdecl (agent object, id, source object or null, record time, flag)) -> entry or
// null; AgentBlackboard_AddToAggroList (0x008AB4B0) asserts on itself and on its own team.
using AddToAggroListFn = void*(__cdecl*)(void* agentObject, std::uint32_t id, void* source, int recordTime, int flag);
inline AddToAggroListFn AddToAggroList = At<AddToAggroListFn>(0x008AB710);
// AgentBlackboard_GetBestTarget (0x008AA270, thiscall agent) -> the first valid entry's id or 0.
using GetBestTargetFn = std::uint32_t(__thiscall*)(void* agent);
inline GetBestTargetFn GetBestTarget = At<GetBestTargetFn>(0x008AA270);
// AgentBlackboard_IsInTether (0x008AB040, thiscall agent, target object, ret 4).
using IsInTetherFn = bool(__thiscall*)(void* agent, void* target);
inline IsInTetherFn IsInTether = At<IsInTetherFn>(0x008AB040);
// Agent_IsVisibleTo (0x008A9B90, cdecl (viewer, target)): same team, a target whose noun doesn't control its visibility
// (fowControlsObjectVisibility, noun +0x2CD; D2g-fog-core), or seen under the viewer's vision rule (mVisibleToTeam
// +0x888, which GameObjectManager_CreateObject sets to 6: both teams; since D2g-live step 2 the host's vision model).
using IsVisibleToFn = bool(__cdecl*)(void* viewer, void* target);
inline IsVisibleToFn IsVisibleTo = At<IsVisibleToFn>(0x008A9B90);
// GameObject_IsTargetable / GameObject_IsAttackable (0x008A9C50 / 0x008A9C90, cdecl object): attributes 48/49 are 0.
using ObjectTestFn = bool(__cdecl*)(void* object);
inline ObjectTestFn IsTargetable = At<ObjectTestFn>(0x008A9C50);
inline ObjectTestFn IsAttackable = At<ObjectTestFn>(0x008A9C90);
// GameObject_ValidateHostileTarget (0x008E22C0, cdecl (team, object, includeDead)): a valid live target of another team.
using ValidateHostileFn = bool(__cdecl*)(int team, void* object, int includeDead);
inline ValidateHostileFn ValidateHostile = At<ValidateHostileFn>(0x008E22C0);
// Agent_CallForHelp (0x008AB860, cdecl (victim, attacker, descriptors lo, hi)): the attacker onto the aggro lists of
// the victim's helpers (its perception list; jungle creatures: its camp).
using CallForHelpFn = void(__cdecl*)(void* victim, void* attacker, std::uint32_t descLo, std::uint32_t descHi);
inline CallForHelpFn CallForHelp = At<CallForHelpFn>(0x008AB860);
// GameObject_GetFootprintRadius (0x008E3390, cdecl object) -> f32.
using FootprintRadiusFn = float(__cdecl*)(void* object);
inline FootprintRadiusFn FootprintRadius = At<FootprintRadiusFn>(0x008E3390);
// DamageTracker_GetSecondsSinceCombat (0x008E1090, cdecl object): seconds since the damage tracker's (object +0x918)
// last hit; 0 without a tracker.
using SecondsSinceCombatFn = float(__cdecl*)(void* object);
inline SecondsSinceCombatFn DamageTrackerSecondsSinceCombat = At<SecondsSinceCombatFn>(0x008E1090);
// Agent_GetAggroType (0x008ABC30, cdecl object): AIDefinition.aggroType through the brain, 0 without one.
using GetAggroTypeFn = int(__cdecl*)(void* object);
inline GetAggroTypeFn GetAggroType = At<GetAggroTypeFn>(0x008ABC30);
// AbilityDataContainer_FireEvent (0x009064E0, thiscall on object +0x8A8, (event, records, count), ret 0xC): every
// ability and modifier instance with a handler for the event gets the records in its private table
// (LuaUtilities_FillOutPrivateTable 0x008939E0) and its handler called (LabsAbilities_CallEventFunction 0x009063F0).
// A record is 0x68 bytes: +0 the name (char*, an EASTL string's begin), +0x54 the value, +0x60 u16 type (9 int, 1 bool,
// 0xD float, 10 object id, 0xC u64), +0x64 u8.
using FireEventFn = bool(__thiscall*)(void* container, int event, const void* records, int count);
inline FireEventFn FireEvent = At<FireEventFn>(0x009064E0);
constexpr std::uint32_t kEventRecordSize = 0x68;
// The brain (object +0x8C0; AgentBrain_Init 0x008AE050): +4 u8 active, +0x10 the current AIDefinition asset, +0x18 the
// current AI node index (AgentBrain_GetCurrentPhase 0x008ABDF0 reads it), +0x1AE8 the minion fast path's static ability.
// D6a: a bot's brain (AgentBrain_InitBot 0x008AD970) also uses +0x14, the current plan node (the start plan, -1 without
// one), and leaves +0x18 (the phase node) at -1.
namespace brain {
constexpr std::uintptr_t kActive = 0x04;
constexpr std::uintptr_t kAiDefinition = 0x10;
constexpr std::uintptr_t kPlan = 0x14;
constexpr std::uintptr_t kNode = 0x18;
}  // namespace brain
// AIDefinition and its parts as the brain reads them (AssetData layouts, docs/data/assetdata_types.txt; data = asset
// +0x68): AIDefinition +0 ainode array, +4 count; cAINode (0x1C) +0 phase asset, +0x14 output links, +0x18 count; Phase
// +0 gambits, +4 count, +8 phaseType, +0x10 defaultBehavior (char*); cGambitDefinition (0x2C) +0 conditions, +4 count,
// +8 the ability GUID (Key instance; AgentBrain_Init passes it to LabsAbilities_FindStaticAbility), +0x1C
// randomizeCooldown, +0x1D waitOnCondition, +0x20 weight, +0x24 cooldown, +0x28 maxCooldown; cConditionDefinition (0x20)
// +0 the condition GUID, +0x10 postfixOperator (1 and, 2 or), +0x14 negate, +0x15 useTargetInfo, +0x18 params (0xC
// each), +0x1C count; cLinkDefinition (0x2C) +0 outputNode, +4 conditions, +8 count, +0xC startOnCooldown, +0xD
// activateOnce, +0x10 activateTime, +0x14 checkTimeInterval, +0x1C cooldown, +0x20 maxCooldown.
namespace aidef {
constexpr std::uint32_t kNodeSize = 0x1C, kGambitSize = 0x2C, kConditionSize = 0x20, kLinkSize = 0x2C;
}  // namespace aidef
// LabsAbilities_RequestAbility (0x0090A670; detoured by the harness's request log, so no prologue check here): (GUID,
// slot, agent id, target id, x, y, z, rank, sequence, snapshot, force, noTick, lua_State, &instance) -> 1/2 started,
// 5 out of range (pursue), 3, negative refusals (-0x270F cooldown, -0x270B agent busy, ...). For requires-agent
// abilities it pushes request 1 into the behavior tree; noTick 1 leaves its tick to the agent step.
// AIConditions_Evaluate (0x008AE9B0, cdecl (condition GUID, object id, out {f32 x, y, z; u32 target; u8 useTarget},
// params (cConditionParamDefinition*), count)) -> pass: the condition's Lua function [1] (AIConditions_CallLuaCondition).
using ConditionEvaluateFn = bool(__cdecl*)(std::uint32_t guid, std::uint32_t objectId, void* out, const void* params,
                                           int count);
inline ConditionEvaluateFn ConditionEvaluate = At<ConditionEvaluateFn>(0x008AE9B0);
constexpr unsigned char kConditionEvaluateBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x53, 0x57, 0x50, 0xB3};
constexpr std::uint16_t kEventTypeObjectId = 10;
constexpr int kEventOnBestTargetChanged = 62;
constexpr int kEventOnEnterBattleReady = 63;
constexpr int kEventOnExitBattleReady = 64;

// --- match flow (D1c-live; docs/MATCH_FLOW.md) --------------------------------------------------------------------
// GameSimulator_StartGame (0x008F2EC0, cdecl (sim, replayPath, gameIdLo, gameIdHi, descriptors), caller cleans 0x14):
// asserts sim is the current simulator; game id +0x08, +0x14 = 1 (u16, so +0x15 = 0), +0x16 = 0, match start +0x19E40 =
// the game clock now, end time/reason cleared, winner 0xFF, descriptors +0x19E58, network-rate timers reset. Its only
// caller is the client's EnterGame (Client_StartGame 0x0066D2F0); the host calls it at the first EnterGame.
using StartGameFn = void(__cdecl*)(void* sim, const char* replayPath, std::uint32_t gameIdLo, std::uint32_t gameIdHi,
                                   std::uint32_t descriptors);
inline StartGameFn StartGame = At<StartGameFn>(0x008F2EC0);
constexpr unsigned char kStartGameBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8, 0x81, 0xEC, 0x2C, 0x02};
constexpr unsigned char kEndGameBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x56, 0xC6, 0x46, 0x15, 0x01};
// GameSimulator_EndGame (0x008EF160, cdecl (sim, reason)): +0x15 = 1, end time +0x19E48, reason +0x19E50, winner
// +0x19E54 (1, 2 -> team 1; 3, 4 -> team 2). Only the client's game-over handler calls it.
using EndGameFn = void(__cdecl*)(void* sim, std::uint32_t reason);
inline EndGameFn EndGame = At<EndGameFn>(0x008EF160);
// GameSimulator_GetMatchElapsedMs (0x008EF780, cdecl sim): game clock now - +0x19E40 (0 before StartGame; asserts on a
// client simulator).
using MatchElapsedMsFn = int(__cdecl*)(void* sim);
inline MatchElapsedMsFn MatchElapsedMs = At<MatchElapsedMsFn>(0x008EF780);
namespace matchsim {
constexpr std::uintptr_t kGameOver = 0x15;         // byte
constexpr std::uintptr_t kMatchStart = 0x19E40;    // u64 ms
constexpr std::uintptr_t kMatchEnd = 0x19E48;      // u64 ms
constexpr std::uintptr_t kEndReason = 0x19E50;     // u32
constexpr std::uintptr_t kWinner = 0x19E54;        // u8, 0xFF none
constexpr std::uintptr_t kDescriptors = 0x19E58;   // u32
constexpr std::uintptr_t kUIData = 0xC930;         // cBattlegroundsUIData (GameSimulator_GetBattlegroundsUIData 0x008EE600)
// The spline-path table (GameSimulator_FindSplinePath 0x008F05C0): an EASTL map<u32 spline id, vector<cSPVector3>>
// at +0x19E60: anchor {right = rightmost, left = leftmost, parent = root, u8 colour} at +0x19E64, root +0x19E6C, size
// +0x19E74. Nodes {right, left, parent, colour, u32 key (+0x10), vector {begin, end, capacity, -} (+0x14)} of 0x24
// bytes; the reset/destructor (0x008FB290) frees a node with the game's free (0x006A9B30) and its vector's buffer only
// if the word before `begin` is non-zero. Nothing in the binary inserts into it; the host does (director.cpp).
constexpr std::uintptr_t kSplineAnchor = 0x19E64;
constexpr std::uintptr_t kSplineRoot = 0x19E6C;
constexpr std::uintptr_t kSplineSize = 0x19E74;
}  // namespace matchsim
// GameSimulator_FindSplinePath (0x008F05C0, cdecl (sim, id)) -> the point vector {begin, end, ...} or null.
using FindSplinePathFn = void*(__cdecl*)(void* sim, std::uint32_t id);
inline FindSplinePathFn FindSplinePath = At<FindSplinePathFn>(0x008F05C0);
// cLocomotionData (object +0x8A0): mSplineId (+0x134; the spline table's key, the head node's object id [inferred]) and
// mSplineIndex (+0x138). Nothing in the binary sets the id; the host does for every wave minion. With it, the minions'
// own bindings work as built: nDirector.GetMinionDestination (0x008DC2C0, LocomotionData_UpdateSplineIndex 0x0089ABA0),
// GetNextMinionDestination (0x008DBE90), nThread.WaitForNearSplinePathGoal (0x00930160, LocomotionData_IsNearSpline-
// PathGoal 0x00892A80), and the lane tether: AgentBlackboard_GetTetherCircle (0x008AAB10) centres a mover with a spline
// id on the point of its current segment nearest the target (0x0089A7E0), not on its spawn.
namespace loco {
constexpr std::uintptr_t kSplineId = 0x134;
constexpr std::uintptr_t kSplineIndex = 0x138;
}  // namespace loco
// D1k (docs/MATCH_FLOW.md *D1k: waves one by one, and the minions' repulsion*): the NavPower mover's repulsion, which
// the client sets up for every server mover and the host doesn't touch (read for the director's log only).
// LocomotionData_Create (0x00893090) -> 0x0088E750: +0x94/+0x98 = 0/0 for a noun with noRepulsion (noun data +0x8),
// 0xFFFFFFFF/4 for NPC type 12, else 0xFFFFFFFB and 1 (player controlled) or 2; 0x0088E7F0: +0x9C obstacle blockage
// flags, +0xA0 = 2 for an NPC (1 for a player) unless noRepulsion. Pathfinder_NavPower.cpp's mover creation (0x0089F3A0)
// and Locomotion_TickMovers' per-mover tune update (0x0089D890 -> 0x0089D1B0) copy them into NavPower's MoverTune
// (m_repulsorBlockageFlags +0x80, m_repulsorIdentityFlags +0x84; 0 and 0 with mDisableRepulsion, blockage 0 with the
// attribute 45 "Unit Pass Through"; on the client both 0 unless it predicts), the radius (0x008E3490, extentsNavmesh:
// 1.24 for a minion) and the cushions below.
namespace loco {
constexpr std::uintptr_t kDisableRepulsion = 0x16;   // bool mDisableRepulsion (reflected member 7)
constexpr std::uintptr_t kRepulsorBlockage = 0x94;   // u32 -> MoverTune m_repulsorBlockageFlags
constexpr std::uintptr_t kRepulsorIdentity = 0x98;   // u32 -> MoverTune m_repulsorIdentityFlags
constexpr std::uintptr_t kObstacleBlockage = 0x9C;   // u32 -> MoverTune m_obstacleBlockageFlags
constexpr std::uintptr_t kAutoObstacle = 0xA0;       // u32: non-zero -> AutoObstacleTune on (server only, delay 0.1 s)
}  // namespace loco
// LabsTuning's NavPower cushions (0x0089E2D0 reads them at NavPower_InitializeSystem from ServerData.package's property
// list 00B1B104-3B01D7F6-ABE12AA6): f32 innerCushion (exe default 1.0, the file 0.5), outerCushion (2.0, 1.0),
// initialAcc (30, 10), outerCushionAcc (50, 30), innerCushionAcc (100, 200).
constexpr std::uintptr_t kLabsTuningCushions = 0x0141D7E4;
// The simulator's NavPower space and its constraint solver (D1k). Sim +0x564 = cLabsAStarSystem (0xD38 bytes, made per
// simulator by 0x008A3F80 "SP_Simulator/cLabsAStarSystem": its mover callbacks Pathfinder_NavPower 0x0089F3A0/0x0089F8C0
// and a bfx space from CreateSpace 0x00C5C880); its +0xD20 is the bfx space handle that the mover creation (0x00BEE540)
// takes; the space object is **handle (0x00C5BBF0). The space's +0xBC -> +0xC is its navigator space (0x30 bytes,
// made by the navigator module's slot 4, 0x00C17A40 -> 0x00C177C0), whose byte +0x2C is the module's constraint solver
// for that space ("constraint solver = %d/%d spaces", the stats printer 0x00C17710): 0 at creation, and only
// bfx::NavigatorEnableConstraintSolver (0x00C173E0, cdecl (space handle*, bool), under the bfx lock) sets it, which
// nothing in the client calls but bfx's own replay player (0x00C1E820). The navigator module is
// *(*(*0x01BEDE18 + 8) + 0xC): +0x04/+0x08 its navigator spaces (array, count), +0x24 navigators, +0x28 repulsors (each
// mover makes one at its creation: Mover::EnableRepulsor 0x00BF8180 from the mover constructor 0x00BFB1F0).
namespace navpower {
constexpr std::uintptr_t kSimAStarSystem = 0x564;
constexpr std::uintptr_t kSpaceHandle = 0xD20;      // in cLabsAStarSystem
constexpr std::uintptr_t kSpaceComponents = 0xBC;   // in the space object; -> +0xC the navigator space
constexpr std::uintptr_t kNavigatorSpace = 0xC;
constexpr std::uintptr_t kConstraintSolver = 0x2C;  // u8 in the navigator space
constexpr std::uintptr_t kSystem = 0x01BEDE18;      // bfx's system (pointer)
constexpr std::uintptr_t kNavigatorCount = 0x24;    // in the navigator module
constexpr std::uintptr_t kRepulsorCount = 0x28;
constexpr std::uintptr_t kMoverRepulsor = 0x1B8;    // in a bfx mover (object +0x8DC): its repulsor
constexpr std::uintptr_t kMoverRepulsorOn = 0x1C0;  // u8: the repulsor exists (Mover::EnableRepulsor)
using EnableConstraintSolverFn = void(__cdecl*)(void* spaceHandle, int enable);
inline EnableConstraintSolverFn EnableConstraintSolver = At<EnableConstraintSolverFn>(0x00C173E0);
constexpr unsigned char kEnableConstraintSolverBytes[] = {0x55, 0x8B, 0xEC, 0x6A, 0xFF, 0x68, 0x28, 0x1F, 0x0B, 0x01};
}  // namespace navpower
// GameObject_CreateFromNoun (0x008E1EC0, cdecl (noun asset, u8 team, owner id, const float* scale or null, const float*
// position, const float* quaternion or null, const float* p7 or null)) -> cGameObject* (id at +0). Fills the create
// data (rotation as Euler degrees) and calls GameObjectManager_CreateObject; the function behind nObjectManager.
// CreateObject (0x00937020).
using CreateFromNounFn = void*(__cdecl*)(void* noun, int team, std::uint32_t owner, const float* scale,
                                         const float* position, const float* quaternion, const float* p7);
inline CreateFromNounFn CreateFromNoun = At<CreateFromNounFn>(0x008E1EC0);
constexpr unsigned char kCreateFromNounBytes[] = {0x81, 0xEC, 0xD4, 0x00, 0x00, 0x00, 0x33, 0xC0};
// Math_DegreesToQuaternion (0x00674430, cdecl (float* out[4], const float* degrees[3])) -> out: degrees * pi/180, then
// Math_EulerToQuaternion (0x00673A90); what AIDirector_RegisterSpawnerMarkerImpl does with a marker's rotDegrees.
using DegreesToQuaternionFn = float*(__cdecl*)(float* out, const float* degrees);
inline DegreesToQuaternionFn DegreesToQuaternion = At<DegreesToQuaternionFn>(0x00674430);
// NPC_UpdateLevelForMatchTime (0x00881170, cdecl (object, f32 match seconds, bool force)): needs an agent (+0x8C4, read
// without a check); skips objects with +0x56, no NPC class, or levelIncreaseOnlyOnSpawn without force; vetoed in
// combat (agent +0x1E72). Called by the engine only at object creation (force 1).
using UpdateNpcLevelFn = void(__cdecl*)(void* object, float matchSeconds, int force);
inline UpdateNpcLevelFn UpdateNpcLevel = At<UpdateNpcLevelFn>(0x00881170);
constexpr unsigned char kUpdateNpcLevelBytes[] = {0x83, 0xEC, 0x0C, 0x57, 0x8B, 0x7C, 0x24, 0x14, 0x85, 0xFF};
constexpr unsigned char kFindCampBytes[] = {0x51, 0x56, 0x8D, 0xB1, 0xB0, 0x00, 0x00, 0x00};
constexpr unsigned char kAddCampMemberBytes[] = {0x83, 0xEC, 0x08, 0x56, 0x8B, 0xF1, 0x8B, 0x86, 0x64, 0x01};
constexpr unsigned char kDegreesToQuaternionBytes[] = {0x83, 0xEC, 0x1C, 0x8B, 0x44, 0x24, 0x24};
constexpr unsigned char kCreateNodeListBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8, 0x6A, 0xFF};
constexpr unsigned char kFindSplinePathBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x74, 0x44};
// The AI director (sim +0x560 -> cAIDirector; +0x18 the parasite's id, +0x1C mImpl). AIDirectorImpl_FindCampByMarkerId
// (0x008AF240, thiscall impl, (marker id), ret 4) -> cCreepCampData* (+0x00 its record id, +0x24 marker id) or null;
// AIDirectorImpl_AddCampMember (0x008AF780, thiscall impl, (camp*, member id), ret 8) appends {member, camp record id}
// to impl +0x160 (begin, +0x164 end, +0x168 capacity); the member's object +0x50 = the camp's marker id (as
// AIDirector_TransferCampMember 0x008AF7E0 does).
using FindCampFn = void*(__thiscall*)(void* impl, std::uint32_t markerId);
inline FindCampFn FindCampByMarkerId = At<FindCampFn>(0x008AF240);
using AddCampMemberFn = void(__thiscall*)(void* impl, void* camp, std::uint32_t member);
inline AddCampMemberFn AddCampMember = At<AddCampMemberFn>(0x008AF780);
namespace aidir {
constexpr std::uintptr_t kParasiteId = 0x18;
constexpr std::uintptr_t kImpl = 0x1C;
constexpr std::uintptr_t kCampMembersBegin = 0x160;  // in mImpl: {u32 member, u32 camp record id}
constexpr std::uintptr_t kCampMembersEnd = 0x164;
}  // namespace aidir
constexpr std::uintptr_t kObjectCampMarker = 0x50;  // cGameObject: the camp's marker id
// cBattlegroundsUIData (sim +0xC930; docs/data/gms_reflected_types.txt; field state +0x168).
namespace ui {
constexpr std::uintptr_t kGameTime = 0x000;           // i32 whole match seconds (the HUD clock)
// D8b: mSurrenderAvailablity (8B member 3, u8): the in-game menu's SURRENDER GAME is enabled while it isn't 0
// (GameUI_IsSurrenderAvailable 0x004AA9C0, its only reader; docs/MATCH_FLOW.md *Surrender and the karma vote*)
constexpr std::uintptr_t kSurrenderAvailability = 0x008;
constexpr std::uintptr_t kTowerRespawnTime = 0x010;   // u64 [2][2][4]
constexpr std::uintptr_t kTowers = 0x090;             // tObjID [2][2][4]
constexpr std::uintptr_t kNumTowersPerLane = 0x0D0;   // u32
constexpr std::uintptr_t kNubRespawnTime = 0x0D8;     // u64 [12]
constexpr std::uintptr_t kNubTeams = 0x138;           // i32 [12]
constexpr std::uintptr_t kFieldState = 0x168;
constexpr std::uintptr_t kNodeList = 0x360;           // cBattlegroundsUINodeData* (0x114 each; field state +0x6C)
constexpr std::uintptr_t kNodeCapacity = 0x364;
constexpr std::uintptr_t kCampList = 0x368;           // cBattlegroundsUICampData* (0x18 each; field state +0x8)
constexpr std::uintptr_t kCampCapacity = 0x36C;
constexpr std::uint32_t kNodeSize = 0x114;
namespace node {
constexpr std::uintptr_t kTeamControl = 0x00;   // u8
constexpr std::uintptr_t kLockedTimer = 0x18;   // f32 seconds left locked
constexpr std::uintptr_t kLockedTimerDuration = 0x1C;
constexpr std::uintptr_t kServerObjId = 0x2C;
constexpr std::uintptr_t kWorldPos = 0x30;      // f32 x 3
constexpr std::uintptr_t kFollowerCapacity = 0x3C;  // u8
constexpr std::uintptr_t kDisplayIndex = 0x3D;  // u8 mHUDMineStatusDisplayIndex (< 4)
constexpr std::uintptr_t kPositionX = 0x40;
constexpr std::uintptr_t kPositionY = 0x44;
constexpr std::uintptr_t kFieldState = 0x6C;
}  // namespace node
}  // namespace ui
constexpr std::uint32_t kTypeUIData = 0xB69C5158;      // cBattlegroundsUIData (63 members)
constexpr std::uint32_t kTypeUINodeData = 0xDDA9E2DC;  // cBattlegroundsUINodeData (21 members)
// BattlegroundsUIData_CreateNodeList (0x008DDAF0, thiscall (ui, count)): allocates the node list (count x 0x114,
// "SP_UI/cBattlegroundsUINodeData") with each node's field state; asserts on a second call.
using CreateNodeListFn = void(__thiscall*)(void* ui, unsigned count);
inline CreateNodeListFn CreateNodeList = At<CreateNodeListFn>(0x008DDAF0);
// The titan-node state of an interactable (object +0x88E, u16; TitanNode_GetState 0x008FC380): 1 locked, 2 unlocking,
// 3 or 0 unlocked, 4 being captured, 5 locking, 6 destroyed. Only TitanNode_InitState (0x008FC370) writes it (0).
constexpr std::uintptr_t kObjectTitanNodeState = 0x88E;
// --- spirit wells (D9; docs/MATCH_FLOW.md *D9: the spirit wells on the match host*) ----------------------------------
// The graphics-state start (object +0x840, u64 server ms; sporelabsObject member 8, mGraphicsStateStartTimeMs): a titan
// node plays its titan-node state's animation from it (GameGraphics_UpdateGraphicsStateAnimation 0x00562280: elapsed =
// now - start, asserting when the start is more than 0.1 s ahead), so the host writes it at every state change.
constexpr std::uintptr_t kObjectGraphicsStateStartMs = 0x840;
// cLocomotionData's mFollowerParams (object +0x8A0; reflected members 11-16, written only while +0xA4 is set) and the
// return-to-work point +0x34 (not reflected; nLocomotion.GetFollowerMiningReturnToWorkPoint 0x00935480 returns it, else
// the drop-off point). Locomotion_StartFollowerMining (0x00919ED0) sets movement type 3 (Locomotion_SetMovementType
// 0x0088EA00, which also sets +0xA4 = (type == 3)), mTravelTime (+0x48) = |gather - drop-off| / 9, mFullLoopTime (+0x4C)
// = 2 travel + 6 + 1 and mStartTimeMs (+0x40) = now, minus travel when the return point is the gather point, minus
// 2 travel + 6 when it is the drop-off point. Locomotion_UpdateFollowerMining (0x0091C480, every frame on both sides)
// then places the worker by the time alone: drop-off -> gather (travel), 6 s at the gather point, back (travel), 1 s at
// the drop-off point, where its team sees the gold icon at mMine (combattext_gold_icon_only, mine_follower_deposit_effect).
namespace loco {
constexpr std::uintptr_t kMovementType = 0x14;          // u8 mMovementType (3 = follower mining)
constexpr std::uintptr_t kFollowerMine = 0x18;          // tObjID mMine
constexpr std::uintptr_t kFollowerGather = 0x1C;        // f32 x 3 mGatherPoint
constexpr std::uintptr_t kFollowerDropOff = 0x28;       // f32 x 3 mDropOffPoint
constexpr std::uintptr_t kFollowerReturnPoint = 0x34;   // f32 x 3
constexpr std::uintptr_t kFollowerTravelTime = 0x48;    // f32 s
constexpr std::uintptr_t kFollowerMiningFlag = 0xA4;    // bool: reflection condition of members 11-16
}  // namespace loco
// LevelGameplayTuning's spirit-well part (docs/data/assetdata_types.txt, members 20-36; Level data +0x614 -> asset +0x68).
namespace gameplayTuning {
constexpr std::uintptr_t kWellCaptureDurations = 0x64;   // float[] {pointer, u32 count}
constexpr std::uintptr_t kWellHitSpeedMultiplier = 0x6C;
constexpr std::uintptr_t kWellHitDurationMs = 0x70;      // int64
constexpr std::uintptr_t kWellAwardPerPlayer = 0x78;
constexpr std::uintptr_t kWellLockFirst = 0x7C;
constexpr std::uintptr_t kWellLockAfterCapture = 0x80;
constexpr std::uintptr_t kWellCaptureRate = 0x84;
constexpr std::uintptr_t kWellDecayRate = 0x88;
constexpr std::uintptr_t kWellFollowerNoun = 0x8C;       // Noun asset (Titan_Node_Follower)
constexpr std::uintptr_t kWellFollowersOnSpawn = 0x90;   // u16
constexpr std::uintptr_t kWellAutoFillPeriod = 0x94;
constexpr std::uintptr_t kWellAutoFillCount = 0x98;      // u16
constexpr std::uintptr_t kWellAutoFillMode = 0x9C;       // enum 0x014280c8: 0 unlimited, 1 gold mine not empty
constexpr std::uintptr_t kWellStartCapacity = 0xA0;
constexpr std::uintptr_t kWellMinedPerMinute = 0xA4;
constexpr std::uintptr_t kWellMinedPerMinutePerFollower = 0xA8;
constexpr std::uintptr_t kWellBountyPercentage = 0xAC;
}  // namespace gameplayTuning
namespace titanNode {  // cTitanNodeDef (noun data +0x1CC)
constexpr std::uintptr_t kNumFollowerSlots = 0x1C;       // u16
}  // namespace titanNode
namespace rewardsTuning {  // RewardsTuning (Level data +0x610)
constexpr std::uintptr_t kTitanGoldRewardInterval = 0x1C;  // the well gold's payout interval (1 s)
}  // namespace rewardsTuning
// The UI node's members beyond D1c-live's (cBattlegroundsUINodeData, docs/data/gms_reflected_types.txt).
namespace ui {
constexpr std::uintptr_t kRedGpm = 0x004;                // u16 mRedGpm (team 2)
constexpr std::uintptr_t kBlueGpm = 0x006;               // u16 mBlueGpm (team 1)
namespace node {
constexpr std::uintptr_t kFollowerCount = 0x01;          // u8
constexpr std::uintptr_t kGoldPerMinute = 0x04;
constexpr std::uintptr_t kAvailableResource = 0x08;
constexpr std::uintptr_t kBounty = 0x0C;
constexpr std::uintptr_t kCaptureScore = 0x10;
constexpr std::uintptr_t kCaptureScoreSpeed = 0x14;
constexpr std::uintptr_t kClaimedTimer = 0x20;
constexpr std::uintptr_t kCaptureDuration = 0x24;
constexpr std::uintptr_t kFlags = 0x28;                  // u8
constexpr std::uintptr_t kGoldMineCapacity = 0x48;
}  // namespace node
}  // namespace ui
// GameSimulator_QueueCombatEvent (0x008F7360, cdecl (const CombatEvent* 0x40 bytes, object id, bool toAll)): reflects
// the event into a CombatEvent (BA) and queues it for everyone (mask 0x7FFF) or the players that know the object
// (GameSimulator_QueueObjectMessage). CombatEvent_Construct (0x008DF6B0) is a memset to 0. A capture is a killing blow
// (flag 4) on the well with a capturer as the source: the client's CombatEvent_KillingBlow shows MineCaptured for a
// target of NPC type GoldMine (10).
using QueueCombatEventFn = void(__cdecl*)(const void* event, std::uint32_t objectId, bool toAll);
inline QueueCombatEventFn QueueCombatEvent = At<QueueCombatEventFn>(0x008F7360);
constexpr unsigned char kQueueCombatEventBytes[] = {0x6A, 0xFF, 0x68, 0x2C, 0x3F, 0x0A, 0x01};
namespace combatEvent {
constexpr std::uint32_t kSize = 0x40;
constexpr std::uintptr_t kFlags = 0x00;
constexpr std::uintptr_t kTarget = 0x14;
constexpr std::uintptr_t kSource = 0x18;
constexpr std::uint32_t kKillingBlow = 0x4;
}  // namespace combatEvent
// lua_createtable (0x0085B820, cdecl (L, narr, nrec)) and lua_toboolean (0x0085B2A0, cdecl (L, idx) -> 0/1).
using LuaCreateTableFn = void(__cdecl*)(void* L, int narr, int nrec);
inline LuaCreateTableFn LuaCreateTable = At<LuaCreateTableFn>(0x0085B820);
using LuaToBooleanFn = int(__cdecl*)(void* L, int idx);
inline LuaToBooleanFn LuaToBoolean = At<LuaToBooleanFn>(0x0085B2A0);
// The stored attribute VimPer5 (69): gold per 5 s (the perk shape Vimgain_A6_B2C1, "+0.7 Vim Per 5 Seconds"); nothing in
// the client pays it (D1i).
constexpr int kAttrVimPer5 = 69;
// Asset data layouts (AssetData, docs/data/assetdata_types.txt; asset +0x68 -> data; arrays are {pointer, count}):
namespace level {
constexpr std::uintptr_t kMarkersets = 0x000;      // Level: LevelMarkerset[] (each an asset pointer)
constexpr std::uintptr_t kGameplayTuning = 0x614;  // Level: LevelGameplayTuning asset
}  // namespace level
namespace marker {  // cLabsMarker, 0xC8 bytes (Markerset data +0: markers {pointer, count})
constexpr std::uint32_t kSize = 0xC8;
constexpr std::uintptr_t kName = 0x00;
constexpr std::uintptr_t kId = 0x04;
constexpr std::uintptr_t kNoun = 0x08;
constexpr std::uintptr_t kPos = 0x14;
constexpr std::uintptr_t kRotDegrees = 0x20;
constexpr std::uintptr_t kScale = 0x2C;             // f32 (D14: 2.0 on each buff camp's major marker, else 1.0)
constexpr std::uintptr_t kTarget = 0x54;
constexpr std::uintptr_t kChildren = 0x58;          // u32[] {pointer, count}
constexpr std::uintptr_t kSplinePathNode = 0xA4;    // cSplinePathNodeData* {char name[32], knot, lane, team, delete}
constexpr std::uintptr_t kSpawnPointDef = 0xBC;     // componentData (+0xAC) .spawnPointDef (+0x10), nullable
}  // namespace marker
namespace nounData {
constexpr std::uintptr_t kTitanNodeDef = 0x1CC;   // cTitanNodeDef*: +0x18 spawnedTitanNoun, +0x1E isBaseNode, +0x1F isGoldMine
constexpr std::uintptr_t kNpcClass = 0x1D4;       // NonPlayerClass asset
constexpr std::uintptr_t kSpawnTeam = 0x294;      // spawnTeamId: 0 npc, 1 blue, 2 red
constexpr std::uintptr_t kSpawnPointDef = 0x2AC;  // componentData.spawnPointDef, nullable
}  // namespace nounData
namespace spawnDef {  // SpawnPointDef, 0x3C bytes
constexpr std::uintptr_t kType = 0x00;            // 1 minions, 2 creeps, 3 parasite, 4 super minion
constexpr std::uintptr_t kTeam = 0x04;            // 0 none, 1 blue, 2 red
constexpr std::uintptr_t kDelay = 0x0C;
constexpr std::uintptr_t kRespawn = 0x10;
constexpr std::uintptr_t kCreeps = 0x18;          // 4 assets: minor, medium, major, uber
constexpr std::uintptr_t kCampUpgrade = 0x28;     // cSPVector4[] {pointer, count}; [0] = the counts
}  // namespace spawnDef
namespace gameplayTuning {  // LevelGameplayTuning
constexpr std::uintptr_t kMinionSpawnDelay1 = 0x0C;
constexpr std::uintptr_t kMinionSpawnInterval1 = 0x14;
constexpr std::uintptr_t kNubRespawnTime = 0xDC;
}  // namespace gameplayTuning
// GearsLua_RegisterNamespace (0x00873170, cdecl (lua_State*, const char* ns, const luaL_Reg* fns, int count)): fetches
// (or creates) the global table and sets each field, so it replaces the client's stubs (SERVER_LUA_API.md).
struct LuaReg {
  const char* name;
  int(__cdecl* fn)(void* L);
};
using RegisterNamespaceFn = void(__cdecl*)(void* L, const char* ns, const LuaReg* fns, int count);
inline RegisterNamespaceFn RegisterNamespace = At<RegisterNamespaceFn>(0x00873170);
constexpr unsigned char kRegisterNamespaceBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x57, 0x56};
// The Lua 5.1 C API pieces the host's bindings use (lua_Number is a 4-byte float).
using LuaTypeFn = int(__cdecl*)(void* L, int idx);              // lua_type 0x0085B050: 2 light userdata, 3 number
inline LuaTypeFn LuaType = At<LuaTypeFn>(0x0085B050);
using LuaToNumberFn = float(__cdecl*)(void* L, int idx);        // lua_tonumber 0x0085B210 (x87 result)
inline LuaToNumberFn LuaToNumber = At<LuaToNumberFn>(0x0085B210);
using LuaPushNumberFn = void(__cdecl*)(void* L, float n);       // lua_pushnumber 0x0085B4C0
inline LuaPushNumberFn LuaPushNumber = At<LuaPushNumberFn>(0x0085B4C0);
using LuaPushLightUserdataFn = void(__cdecl*)(void* L, void* p);  // lua_pushlightuserdata 0x0085B6C0 (object ids)
inline LuaPushLightUserdataFn LuaPushLightUserdata = At<LuaPushLightUserdataFn>(0x0085B6C0);
using LuaPushBooleanFn = void(__cdecl*)(void* L, int b);        // lua_pushboolean 0x0085B6A0 (D6a: nBotHost)
inline LuaPushBooleanFn LuaPushBoolean = At<LuaPushBooleanFn>(0x0085B6A0);
constexpr int kLuaTypeLightUserdata = 2;
constexpr int kLuaTypeNumber = 3;

// --- diagnostics ---------------------------------------------------------------------------
// ShipLog_ReportAssertFailure(kind, expr, file, line, count, message): logs "Assert Failed: ..." and
// returns (shipping build); the asserting function then usually returns early.
constexpr std::uintptr_t kReportAssertFailure = 0x008965E0;
constexpr unsigned char kReportAssertFailureBytes[] = {0x64, 0xA1, 0x00, 0x00, 0x00, 0x00};  // mov eax, fs:[0]
// GearsLua error handler for a failed thread resume (0x00875080, called by the resume 0x00875B20):
// runs _TRACEBACK and reports "Lua Runtime %s" through the assert path, which the game throttles
// per file:line (0x00896970), so most Lua errors never reach the log. Hooked to log every one.
constexpr std::uintptr_t kLuaThreadError = 0x00875080;
constexpr unsigned char kLuaThreadErrorBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8};  // push ebp; mov ebp,esp; and esp,-8
// LabsAbilities: request an ability on an agent (0x0090A670, behind nAbility.RequestAbility 0x008C33D0;
// 1 or 2 = started) and its validation (0x009053C0; negative codes = refused, e.g. -0x2703 unknown
// ability, -0x2707, -0x2708 target, -0x270A rank, -0x270D range). Hooked to log the codes.
constexpr std::uintptr_t kRequestAbility = 0x0090A670;
constexpr unsigned char kRequestAbilityBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8};
using RequestAbilityFn = int(__cdecl*)(unsigned ability, int slot, unsigned agent, unsigned target, float x, float y, float z,
                                       int rank, unsigned flags, unsigned snapshot, int b11, char b12, void* L, int* out);
// D1b-live's brain step calls it (through the request log's detour); see the agent AI block above.
inline RequestAbilityFn RequestAbilityCall = At<RequestAbilityFn>(0x0090A670);
constexpr std::uintptr_t kValidateAbility = 0x009053C0;
constexpr unsigned char kValidateAbilityBytes[] = {0x6A, 0xFF, 0x68, 0xCB, 0x41, 0x0A, 0x01};
using ValidateAbilityFn = int(__cdecl*)(unsigned ability, int slot, unsigned agent, unsigned target, float x, float y, float z,
                                        int rank, int b9);
// Create and activate an ability instance right away (0x0090A390; called by 0x00908AC0, e.g. for an
// AIDefinition's passive at brain init): builds the instance, queues its server messages and runs its
// Lua (0x008FEE80). Arguments as the brain init passes them: (ability, agent, target, 0, rank, x, y, z, 0).
using ActivateAbilityFn = unsigned(__cdecl*)(unsigned ability, unsigned agent, unsigned target, unsigned parent, int rank,
                                            float x, float y, float z, unsigned snapshot);
inline ActivateAbilityFn ActivateAbility = At<ActivateAbilityFn>(0x0090A390);
// --- the server ability update (D1a, docs/SERVER_MODE.md *D1a static analysis*) ----------------
// LabsAbilities_CreateTickThread (0x009020A0): allocates the instance's GearsLua thread, fills its
// private table and loads Tick (nAbilityFns.Tick = 2) onto it, status 2; instance +0xE8 = thread id.
// Called by LabsAbilities_CreateAbilityInstance 0x00909810 and LabsAbilities_ActivateAbility
// 0x0090A390, caller cleans (cdecl). LabsAbilities_RecreateTickThread (0x00902580, cdecl) does the same
// on reactivation (0x00909E40). Neither hands the thread to the scheduler; the harness does.
constexpr std::uintptr_t kCreateTickThread = 0x009020A0;
constexpr std::uintptr_t kRecreateTickThread = 0x00902580;
constexpr unsigned char kTickThreadBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8};  // push ebp; mov ebp,esp; and esp,-8
using CreateTickThreadFn = void(__cdecl*)(void* instance, void* L, unsigned killerId);
using RecreateTickThreadFn = void(__cdecl*)(void* instance);
// GearsLua_GetThread (0x00873AC0, thiscall ECX = cLuaSimulator): thread id -> thread object or null
// (handle table at +0x64; a freed id gives null). GearsLua_AddSleepingThread (0x00874170, thiscall,
// ret 4): inserts the id into the sleeping set +0x114 and erases it from the due set +0x315C; it only
// asserts (and inserts anyway) while the scheduler walks the sleeping set (+0x60 set).
using GetThreadFn = void*(__thiscall*)(void* luaSim, unsigned tid);
inline GetThreadFn GetThread = At<GetThreadFn>(0x00873AC0);
using AddSleepingThreadFn = void(__thiscall*)(void* luaSim, unsigned tid);
inline AddSleepingThreadFn AddSleepingThread = At<AddSleepingThreadFn>(0x00874170);
// GearsLua_ResumeThread (0x00875B20, fastcall ECX = the thread object, +0 its id, +8 its lua_State, +0xF8 status):
// resumes one due thread; GearsLua_TickThreads (0x00875CF0) calls it per due id and afterwards uses only the returned
// status and the id. D10: the host holds the thread's ability instance "in use" (+0x230) around it, as the stripped
// ability update presumably did, so an EndAbility from inside the running Tick (nAbility.MarkForDelete on its own
// projectile, ability_Alpha_E_HealWolf's OnHitTarget) is deferred (+0x234) instead of freeing the running thread.
constexpr std::uintptr_t kResumeThread = 0x00875B20;
constexpr unsigned char kResumeThreadBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8};  // push ebp; mov ebp,esp; and esp,-8
using ResumeThreadFn = int(__fastcall*)(void* thread);
// D12: GearsLua_CallFunction (0x00875330, thiscall ECX = the thread object, ret 0x1C): runs a Lua table's function
// synchronously: (table name, function index (nAbilityFns for abilities), argument (the ability instance for the
// LabsAbilities callers, e.g. LabsAbilities_RunActivateFunction 0x008FEE80), key table, 1, 0, 0); returns a bool in
// AL. Detoured to know which script runs when an assert or Lua error comes (abilities.cpp's Lua context).
constexpr std::uintptr_t kCallFunction = 0x00875330;
constexpr unsigned char kCallFunctionBytes[] = {0x53, 0x55, 0x56, 0x57, 0x8B, 0xF9};  // push ebx/ebp/esi/edi; mov edi,ecx
using CallFunctionFn = bool(__fastcall*)(void* thread, void* edx, const char* table, int fn, void* arg, void* key,
                                         int a5, int a6, int a7);
// D15c: the call itself, through the address (so abilities.cpp's detour sees it). Its stack arguments as
// LabsAbilities_CallEventFunction (0x009063F0) passes them: (table, fn, instance, params, nparams, results, nresults),
// a parameter or result 0x10 bytes {type, value, ...}: parameter type 8 = the global table of that name (the private
// table "_PRIVATETHREADTABLE", GearsLua 0x00873360), result type 5 = a bool (value byte at +4). It asserts unless the
// thread's lua_State status is 0 or 1 (yielded) and sets 0 around its lua_pcall (0x00874F80).
inline CallFunctionFn CallFunction = At<CallFunctionFn>(kCallFunction);
struct GearsLuaArg {
  std::uint32_t type;
  const void* value;
  std::uint32_t pad[2];
};
constexpr std::uint32_t kGearsArgGlobalTable = 8;
constexpr std::uint32_t kGearsArgBool = 5;
// LabsAbilities_GetInstance (0x008FF220): instance id -> instance or null (pool 0x3C41D3D8 of the
// current simulator). LabsAbilities_EndAbility (0x00905E90): removes the instance (Deactivate, thread
// freed, "ability ended" message) unless a synchronous Lua call holds it (+0x230 > 0); then it only
// sets +0x234 and the instance ends when that call returns.
using GetInstanceFn = void*(__cdecl*)(unsigned instanceId);
inline GetInstanceFn GetInstance = At<GetInstanceFn>(0x008FF220);
using EndAbilityFn = void(__cdecl*)(unsigned instanceId);
inline EndAbilityFn EndAbility = At<EndAbilityFn>(0x00905E90);
// D6c: the early-end trace detours EndAbility and LabsAbilities_InterruptAbility (0x00905FA0, below) to see who ends an
// agent ability before its Tick finished. Both prologues are whole instructions without relative operands.
constexpr std::uintptr_t kEndAbility = 0x00905E90;
constexpr unsigned char kEndAbilityBytes[] = {0x68, 0xD8, 0xD3, 0x41, 0x3C};  // push 0x3C41D3D8 (the instance pool)
constexpr std::uintptr_t kInterruptAbility = 0x00905FA0;
constexpr unsigned char kInterruptAbilityBytes[] = {0x83, 0xEC, 0x30, 0x53, 0x56};  // sub esp,0x30; push ebx; push esi
// The return addresses of their callers in the client (the call instruction + 5), named in the trace: InterruptAbility
// is called only by the behavior tree's two abort actions and the player's command queue; EndAbility (14 callers) by
// InterruptAbility, the MarkForDelete bindings, event functions that return false, stacking and the inventory.
constexpr std::uintptr_t kRetBtUseAbilityAbort = 0x008A7FEF;       // BTAction_UseAbility_Abort
constexpr std::uintptr_t kRetBtHoldsAgentAbort = 0x008A810F;       // BTAction_AbilityHoldsAgent_Abort
constexpr std::uintptr_t kRetCommandQueue = 0x00923E03;            // ServerCommandQueue_TickPlayer
constexpr std::uintptr_t kRetInterruptAbility = 0x009060DC;        // LabsAbilities_InterruptAbility
constexpr std::uintptr_t kRetMarkForDelete = 0x008C593F;           // nAbilityModifier_MarkForDelete
constexpr std::uintptr_t kRetMarkSelfForDelete = 0x008C1F30;       // nAbilityModifier_MarkSelfForDelete
constexpr std::uintptr_t kRetEventReturnedFalse = 0x009064BE;      // LabsAbilities_CallEventFunction
constexpr std::uintptr_t kRetDecrementStackCount = 0x008C83D1;     // nModifier_DecrementStackCount
// GameTimeManager_GetTimeMs (0x008F98A0): the simulator's clock in ms (64 bit), the one instance
// start times (+0x208) are taken from.
using GetTimeMsFn = unsigned long long(__cdecl*)(void* sim);
inline GetTimeMsFn GetTimeMs = At<GetTimeMsFn>(0x008F98A0);

// --- damage (Damage.cpp) -------------------------------------------------------------------
// cGameObject::TakeDamage core (0x008E01C0), behind nGameObject.TakeDamage (0x0093FD60) and the
// health-cost payment (0x0087E200): (object, snapshot, damage, coefficient, descriptors lo/hi,
// facing, &damageDealt, &damageAbsorbed, &isCrit) -> success. In the client build its body is gone:
// it asserts &dealt != &absorbed and returns !(current sim +0x15), with nothing written (D1a-live).
constexpr std::uintptr_t kTakeDamage = 0x008E01C0;
constexpr unsigned char kTakeDamageBytes[] = {0x8B, 0x4C, 0x24, 0x20, 0x33, 0xC0};  // mov ecx,[esp+20h]; xor eax,eax
using TakeDamageFn = bool(__cdecl*)(void* object, void* snapshot, float damage, float coefficient, unsigned descLo,
                                   unsigned descHi, const float* facing, float* dealt, float* absorbed, bool* isCrit);
// The kill core (0x008E0210, only caller nGameObject.KillObject 0x00940650): (object, killer object or
// null, flag -> combatant +0x28, 1, fire events): HP and combatant +0x4C..+0x54 = 0, then OnTookDamage
// (LabsAbilities_FireOnTookDamage 0x00906760, event 26) and the killer's OnDealtDamage (0x00906CA0)
// with the old HP as damage.
using KillObjectFn = void(__cdecl*)(void* object, void* killer, char flag, int one, char fireEvents);
inline KillObjectFn KillObject = At<KillObjectFn>(0x008E0210);
// Object id -> cGameObject* of the current simulator (0x008E2220).
using GetObjectByIdFn = void*(__cdecl*)(unsigned id);
inline GetObjectByIdFn GetObjectById = At<GetObjectByIdFn>(0x008E2220);
// Snapshot attribute (0x008D9FD0, thiscall on the ability snapshot; index = nAttributeType, 0 Power).
// The snapshot's +4 is the caster's object id. The heal core (0x008E0720) uses it for power x coefficient.
using SnapshotAttributeFn = float(__thiscall*)(void* snapshot, int attribute);
inline SnapshotAttributeFn SnapshotAttribute = At<SnapshotAttributeFn>(0x008D9FD0);
namespace combatant {
constexpr std::uintptr_t kOfObject = 0x8EC;  // cGameObject +0x8EC -> combatant data
constexpr std::uintptr_t kHitPoints = 0x48;  // float, current HP (0x0087DF20 reads it)
}  // namespace combatant
using MaxHitPointsFn = float(__cdecl*)(void* object);  // 0x0087DD90
inline MaxHitPointsFn MaxHitPoints = At<MaxHitPointsFn>(0x0087DD90);

// --- the damage core and death (D1f-live; docs/DAMAGE.md *The damage pipeline*, *Death handling*) -----------------
// The pieces of the pipeline the client kept, called by gms/engine/damage.cpp in DAMAGE.md's step order.
// Damage_ComputeInitialDamage (0x008E0130, cdecl, ST0): damage + Damage_GetPowerScaledDamage (Power x coefficient, the
// basic damage per level for IsBasic) unless IsSpellOverload, then ModifyDamageInitial (10) on the attacker's container
// (other object = *targetId, GUID = snapshot +0x18).
using ComputeInitialDamageFn = float(__cdecl*)(float damage, float coefficient, unsigned descLo, unsigned descHi,
                                               void* attacker, void* snapshot, const unsigned* targetId,
                                               const float* direction);
inline ComputeInitialDamageFn ComputeInitialDamage = At<ComputeInitialDamageFn>(0x008E0130);
constexpr unsigned char kComputeInitialDamageBytes[] = {0x53, 0x56, 0x8B, 0x74, 0x24, 0x18, 0x57, 0x8B};
// AbilityDataContainer_ModifyDamage (0x00906220, thiscall on object +0x8A8, ret 0x1C, ST0): every ability, then every
// modifier (sorted by remaining time, 0x009052F0) with a handler for `index` (static +0x14 + index) and not ending
// (+0x234) gets modifyDamage* in its private table (LabsAbilities_SetModifyDamageArgs 0x008FE110) and its handler
// called; the value is threaded through.
using ModifyDamageFn = float(__thiscall*)(void* container, float damage, unsigned descLo, unsigned descHi, unsigned guid,
                                          const float* direction, unsigned otherId, int index);
inline ModifyDamageFn ModifyDamage = At<ModifyDamageFn>(0x00906220);
constexpr unsigned char kModifyDamageBytes[] = {0x83, 0xEC, 0x2C, 0x53, 0x55, 0x56, 0x8B, 0xD9};
// LabsAbilities_CallModifyDamageFunction (0x00900460, stdcall, ret 0x20, ST0): ONE instance's number-returning handler
// `index` with the private table as its argument, value in and out (unchanged if the instance has no handler or is
// ending). It also writes modifyDamage* into the table; the host sets the handler's own fields first (absorbDamage,
// grantGold*), which is how it calls the AbsorbDamage* (14-19) and OnGrantGold (48) handlers the client lost.
using CallNumberHandlerFn = float(__stdcall*)(unsigned instanceId, int index, float value, const float* direction,
                                              unsigned otherId, unsigned descLo, unsigned descHi, unsigned guid);
inline CallNumberHandlerFn CallNumberHandler = At<CallNumberHandlerFn>(0x00900460);
constexpr unsigned char kCallNumberHandlerBytes[] = {0x83, 0xEC, 0x20, 0x56, 0xE8};
// Damage_RollCritical (0x008DFD50, cdecl, AL): Mastery / AttributeTuning +0xC (capped at 100 %) through the PRD roll
// (Random_RollPseudoRandomDistribution 0x008DF9B0); the counter in snapshot +0x148 and the caster's attribute data +0x258.
// Arguments 2, 3 and 5 are unused by the code (the heal core passes descriptors and the target).
using RollCriticalFn = bool(__cdecl*)(void* snapshot, unsigned descLo, unsigned descHi, void* attributeTuning,
                                      void* target);
inline RollCriticalFn RollCritical = At<RollCriticalFn>(0x008DFD50);
constexpr unsigned char kRollCriticalBytes[] = {0x83, 0xEC, 0x10, 0x57, 0x8B, 0x7C, 0x24, 0x18};
// Damage_GetPhysicalMultiplier / Damage_GetMagicalMultiplier (0x008DFEB0 / 0x008DFFF0, cdecl (target, snapshot), ST0):
// 100 / (100 + defense) with TotalArmor (86) / TotalMR (90), penetration 30-32 from the snapshot unless a structure.
using DefenseMultiplierFn = float(__cdecl*)(void* target, void* snapshot);
inline DefenseMultiplierFn PhysicalMultiplier = At<DefenseMultiplierFn>(0x008DFEB0);
inline DefenseMultiplierFn MagicalMultiplier = At<DefenseMultiplierFn>(0x008DFFF0);
constexpr unsigned char kPhysicalMultiplierBytes[] = {0x83, 0xEC, 0x08, 0x53, 0x56, 0x8B, 0x74, 0x24, 0x14};
// Damage_ReportHpChange (0x008DFB10, cdecl, 12 arguments): builds the CombatEvent (0x40 bytes; flags 1 damage, 2 heal,
// 4 killing blow (+0x8000 / 0x80000 / 0x2000 / 0x4000 for players), 8 crit, 0x200 DoT, 0x20 self-inflicted, plus
// `extraFlags`) and queues it (GameSimulator_QueueCombatEvent 0x008F7360). Asserts integerHpChange >= 0; the
// direction is read (never null: the heal report passes the zero vector 0x019E1C88).
using ReportHpChangeFn = void(__cdecl*)(void* target, void* snapshot, float dealt, bool isCrit, bool lethal,
                                        const float* direction, unsigned extraFlags, unsigned descLo, unsigned descHi,
                                        int integerHpChange, unsigned victimKillStreak, unsigned killerPrevKillStreak);
inline ReportHpChangeFn ReportHpChange = At<ReportHpChangeFn>(0x008DFB10);
constexpr std::uintptr_t kZeroDirection = 0x019E1C88;
// Damage_RecordAttacker (0x008E1AD0, cdecl, 8 arguments): last enemy player to hit (tracker +0x88/+0x90), combat times
// on both trackers (+0x918), the death recap for player victims, assist times.
using RecordAttackerFn = void(__cdecl*)(void* target, float dealt, unsigned descLo, unsigned descHi, unsigned attackerId,
                                        unsigned attackerPlayerIdx, unsigned abilityId, unsigned abilityInstance);
inline RecordAttackerFn RecordAttacker = At<RecordAttackerFn>(0x008E1AD0);
// LabsAbilities_FireOnTookDamage (0x00906760, cdecl, 11) / LabsAbilities_FireOnDealtDamage (0x00906CA0, cdecl, 9).
using FireOnTookDamageFn = void(__cdecl*)(unsigned targetId, unsigned attackerId, unsigned attackerTeam,
                                          unsigned attackerPlayerIdx, float damage, unsigned descLo, unsigned descHi,
                                          unsigned abilityGuid, unsigned abilityInstance, float originalDamage,
                                          float originalCoefficient);
inline FireOnTookDamageFn FireOnTookDamage = At<FireOnTookDamageFn>(0x00906760);
using FireOnDealtDamageFn = void(__cdecl*)(unsigned attackerId, unsigned targetId, float damage, unsigned descLo,
                                           unsigned descHi, unsigned abilityGuid, unsigned abilityInstance,
                                           unsigned snapshotId, bool isCrit);
inline FireOnDealtDamageFn FireOnDealtDamage = At<FireOnDealtDamageFn>(0x00906CA0);
// GameObject_HealDamage (0x008E0720, cdecl, 13 arguments; the intact heal core): (object, snapshot, amount, coefficient,
// descriptors, noScaling, &healed, &isCrit, 0, isLifesteal (combat event 0x400), canCrit, allowOverload).
using HealDamageFn = void(__cdecl*)(void* object, void* snapshot, float amount, float coefficient, unsigned descLo,
                                    unsigned descHi, bool noScaling, float* healed, bool* isCrit, unsigned unknown,
                                    bool isLifesteal, bool canCrit, bool allowOverload);
inline HealDamageFn HealDamage = At<HealDamageFn>(0x008E0720);
// AttributeData_GetAttribute (0x008D9C00, thiscall on object +0x8E0, (index, 1), ret 8, ST0): +8 + 4 x index, the
// derived attributes (> 72) through AttributeData_GetDerivedAttribute (0x008D7800).
using GetAttributeFn = float(__thiscall*)(void* attributeData, int index, int flag);
inline GetAttributeFn GetAttribute = At<GetAttributeFn>(0x008D9C00);
// The attribute tuning: thiscall 0x006C19A0 on the holder 0x019E1CB4 -> AttributeTuning asset; data (+0x68): +0xC
// criticalChanceFromMasteryRating, +0x10 spellOverloadFromMasteryRating, +0x14 criticalDamageBonusPercent.
using GetTuningAssetFn = void*(__thiscall*)(void* holder);
inline GetTuningAssetFn GetTuningAsset = At<GetTuningAssetFn>(0x006C19A0);
constexpr std::uintptr_t kAttributeTuningHolder = 0x019E1CB4;
namespace attrTuning {
constexpr std::uintptr_t kCritChanceFromMastery = 0x0C;
constexpr std::uintptr_t kOverloadFromMastery = 0x10;
constexpr std::uintptr_t kCritDamageBonus = 0x14;
}  // namespace attrTuning
// nAttributeType indices the core reads (GlobalDefinitions.lua).
namespace attr {
constexpr int kPower = 0, kMastery = 1, kDamageReduction = 21, kPercentDamageDebuff = 33, kPercentCritDamage = 34,
              kFlatCritDamage = 35, kFlatOverloadDamage = 37, kImmune = 46, kBehindDamageIncrease = 62,
              kAoeResistance = 63, kLifeDrain = 64, kStructureDamageDealt = 67, kStructureDamageTaken = 68;
}  // namespace attr
// GameObject_IsStructure (0x008809E0, cdecl object, AL): the Titan type and NPC types 9, 10, 13, 14, 15.
using IsStructureFn = bool(__cdecl*)(void* object);
inline IsStructureFn IsStructure = At<IsStructureFn>(0x008809E0);
// GameObject_IsPlayerPet (0x00880830, cdecl object, AL): the noun's NPC class has NPC type 11 (object +0x78 -> +0x68 ->
// +0x68 == 0xB). DamageTracker_RecordCombatTimes (0x008E0DC0) counts a pet's hit as player damage (D9c).
using IsPlayerPetFn = bool(__cdecl*)(void* object);
inline IsPlayerPetFn IsPlayerPet = At<IsPlayerPetFn>(0x00880830);
constexpr unsigned char kIsPlayerPetBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x8B, 0x40, 0x78, 0x85, 0xC0};
// DamageTracker_HasTakenDamageWithin (0x008E0FC0, cdecl (object, int64 ms, kind), AL): the object's damage tracker
// (+0x918) took damage > 0 within `ms` of the server clock; kind 0 from any enemy attacker (+0x28), 1 from an enemy
// player's creature or pet (+0x50), 2 from a structure (+0x78) (written by DamageTracker_RecordCombatTimes 0x008E0DC0
// through Damage_RecordAttacker). nGameObject.HasTakenDamage / HasTakenPlayerDamage / HasTakenTowerDamage (0x00943320 /
// 0x00943360 / 0x009433A0) are kinds 0 / 1 / 2 with 5000 ms. D9c reads kind 1 beside the wells' own record.
using HasTakenDamageWithinFn = bool(__cdecl*)(void* object, long long ms, int kind);
inline HasTakenDamageWithinFn HasTakenDamageWithin = At<HasTakenDamageWithinFn>(0x008E0FC0);
constexpr unsigned char kHasTakenDamageWithinBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x74};
constexpr int kDamageTrackerAny = 0, kDamageTrackerPlayer = 1, kDamageTrackerStructure = 2;
// cCombatantData beyond HP (cGameObject +0x8EC): the killing hit's direction, the death descriptors (nHitDescriptors),
// the three shield totals CombatantDataUpdate replicates.
namespace combatant {
constexpr std::uintptr_t kHitDirection = 0x1C;   // f32 x 3
constexpr std::uintptr_t kDeathDescriptors = 0x28;
constexpr std::uintptr_t kShield = 0x4C;         // mShieldHealth
constexpr std::uintptr_t kMagicalShield = 0x50;  // mMagicalShieldHealth
constexpr std::uintptr_t kPhysicalShield = 0x54; // mPhysicalShieldHealth
}  // namespace combatant
// GameObject_Kill (0x008E0210, the kill core; detoured by D1f-live's death module): see KillObject above.
constexpr unsigned char kKillObjectBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08};  // push esi; mov esi,[esp+8]
// LabsAbilities_DoAbilityEvent_OnGrantGold (0x008FE500, cdecl (creature id, amount, target id, killer id, ?), ST0): the
// client build's stub (asserts "server only function", returns 0), called by LabsPlayer_AddGold (0x00912440) for every
// grant. Replaced whole by the host (OnGrantGold 48 on the creature's modifiers, static +0x44, like OnGrantXP's
// 0x008FF440 / 0x008FF2E0).
constexpr std::uintptr_t kOnGrantGoldStub = 0x008FE500;
constexpr unsigned char kOnGrantGoldStubBytes[] = {0x68, 0xFE, 0x15, 0x00, 0x00, 0x68, 0x28, 0xF2, 0x24, 0x01};
// LabsPlayer_GrantGold (0x00912790, cdecl): (player, amount, bonus (source 0x13), hide the combat event, internal source
// (nGoldSourceType + 7, table 0x0143E2D8), target id, killer id, ?) -> LabsPlayer_AddGold (thiscall, 0x00912440):
// mAvailableGold +0xA8, mTotalGold +0xAC, the stub above, the gold combat event (0x200000) unless hidden.
using GrantGoldFn = void(__cdecl*)(void* player, float amount, float bonus, bool hide, unsigned source, unsigned target,
                                   unsigned killer, unsigned unknown);
inline GrantGoldFn GrantGold = At<GrantGoldFn>(0x00912790);
constexpr unsigned char kGrantGoldBytes[] = {0x8B, 0x4C, 0x24, 0x04, 0x83, 0xEC, 0x10};
constexpr unsigned kGoldSourceOffset = 7;  // nGoldSourceType (Lua) -> internal
// LabsPlayer_GrantXPIfValid (0x00915770, cdecl): (player, amount, bonus, hide, internal source (nXPSourceType + 21,
// table 0x0143E2AC), target id, last player to hit) -> LabsPlayer_GrantXP (0x00914D80): OnGrantXP, levels.
using GrantXpFn = void(__cdecl*)(void* player, float amount, float bonus, bool hide, unsigned source, unsigned target,
                                 unsigned lastPlayerToHit);
inline GrantXpFn GrantXp = At<GrantXpFn>(0x00915770);
constexpr unsigned kXpSourceOffset = 21;
namespace player {
constexpr std::uintptr_t kAvailableGold = 0xA8;  // mAvailableGold (f32, labsPlayerFowData)
constexpr std::uintptr_t kTotalGold = 0xAC;      // mTotalGold
constexpr std::uintptr_t kXp = 0xA4;             // mExperience
}  // namespace player
// GameObject_GetRewardForNPC (0x0091FF80, cdecl (object, &xp, &gold)): CharacterRewardData xp / gold + the per-level
// bonus x (NPC level - 1). GameObject_GetCharacterRewardData (0x008DDE80, cdecl object): noun data +0x2F8 -> its data
// (+0 gold, +4 xp, +8/+0xC per level, +0x10 overrideRadius, +0x14 xpRewardType, +0x18 goldRewardType).
using GetRewardForNpcFn = void(__cdecl*)(void* object, float* xp, float* gold);
inline GetRewardForNpcFn GetRewardForNpc = At<GetRewardForNpcFn>(0x0091FF80);
using GetRewardDataFn = char*(__cdecl*)(void* object);
inline GetRewardDataFn GetRewardData = At<GetRewardDataFn>(0x008DDE80);
namespace reward {
constexpr std::uintptr_t kOverrideRadius = 0x10;
constexpr std::uintptr_t kXpType = 0x14;
constexpr std::uintptr_t kGoldType = 0x18;
// D14: +0x1C buffs {cCharacterRewardBuffData*, count (+0x20)}, 0x14 bytes each: +0 buffType (the reward-type enum),
// +4 the buff Key's instance = the modifier's GUID (Key {instance, type, group, char*} with delta -12 from +0x10;
// docs/data/assetdata_types.txt). GameObject_GetCharacterRewardData's only caller is GameObject_GetRewardForNPC, so
// nothing in the client reads the list: the stripped reward code granted them (MATCH_FLOW.md *D14*).
constexpr std::uintptr_t kBuffs = 0x1C;
constexpr std::uintptr_t kBuffSize = 0x14;
constexpr std::uintptr_t kBuffType = 0x0;
constexpr std::uintptr_t kBuffGuid = 0x4;
constexpr std::uintptr_t kBuffName = 0x10;  // the Key's char* (the modifier's name)
}  // namespace reward
// RewardsTuning (Level data +0x610 -> asset, data +0x68; AssetData layout).
namespace level {
constexpr std::uintptr_t kRewardsTuning = 0x610;
}  // namespace level
// The match-stats object (sim +0x558; GameSimulator_GetMatchStats 0x008EF480 reads it, only the reset 0x008EF590 writes
// it: the server shell that created it is stripped; its class and size are unknown). Its known part, at +0x194: an
// EASTL vector_map<u64 online id, vector<f32> of 24 stats> (begin, end, capacity, -, u8 at +0x10; 0x20-byte elements;
// lookup 0x00AD8BA0, each new player's vector of 24 zeros from 0x00AD86C0): stat 7 += every gold grant (0x00AD8CC0),
// 8 += XP, 1 = the level (0x00AD8CF0). The replay writer (0x00921050) checks the object for null; LabsPlayer_AddGold
// and LabsPlayer_GrantXP use +0x194 without a check. The host gives the server simulator a zeroed block (an empty map).
namespace sim {
constexpr std::uintptr_t kMatchStats = 0x558;
}  // namespace sim
constexpr std::uintptr_t kMatchStatsPlayerMap = 0x194;
constexpr std::uint32_t kMatchStatsSize = 0x400;  // [assumed] >= 0x194 + 0x20; zeroed
namespace rewardsTuning {
constexpr std::uintptr_t kStartingGold = 0x0C;
constexpr std::uintptr_t kPassiveStartDelay = 0x10;
constexpr std::uintptr_t kPassiveGold = 0x18;
constexpr std::uintptr_t kRadiusPlayer = 0x20;
constexpr std::uintptr_t kPlayerXpShare = 0x28;          // {float*, count}
constexpr std::uintptr_t kPlayerGoldShare = 0x30;
constexpr std::uintptr_t kMinionFatalHitGoldRatio = 0x78;
constexpr std::uintptr_t kRadiusMinion = 0x7C;
constexpr std::uintptr_t kMinionXpShare = 0x80;
constexpr std::uintptr_t kMinionGoldShare = 0x88;
constexpr std::uintptr_t kJungleFatalHitGoldRatio = 0x90;
constexpr std::uintptr_t kRadiusJungle = 0x94;
constexpr std::uintptr_t kJungleXpShare = 0x98;
constexpr std::uintptr_t kJungleGoldShare = 0xA0;
constexpr std::uintptr_t kTowerGoldReward = 0xA8;
}  // namespace rewardsTuning
// DamageTracker_GetLastPlayerToHit (0x008E1020, cdecl object, AL): the tracker's last enemy player (+0x88) while its
// time (+0x90) is recent enough, else 0xFF.
using LastPlayerToHitFn = unsigned char(__cdecl*)(void* object);
inline LastPlayerToHitFn LastPlayerToHit = At<LastPlayerToHitFn>(0x008E1020);
// LabsAbilities_InterruptAbility (0x00905FA0, cdecl instance id): OnInterrupt (47) and its linger rules, else EndAbility.
using InterruptAbilityFn = int(__cdecl*)(unsigned instanceId);
inline InterruptAbilityFn InterruptAbility = At<InterruptAbilityFn>(0x00905FA0);
// LabsAbilities_GetFinishOnDeath (0x008FF240, ECX = instance): static +0x3E9 or handler 50.
using GetFinishOnDeathFn = bool(__fastcall*)(void* instance);
inline GetFinishOnDeathFn GetFinishOnDeath = At<GetFinishOnDeathFn>(0x008FF240);
// The Lua C API pieces the private-table writes use (the same calls 0x008FE110 makes on the instance's thread): the
// thread's lua_State is thread +8 (0x008230F0).
using LuaPushStringFn = void(__cdecl*)(void* L, const char* s);  // lua_pushstring 0x0085B540
inline LuaPushStringFn LuaPushString = At<LuaPushStringFn>(0x0085B540);
using LuaSetTableFn = void(__cdecl*)(void* L, int idx);           // lua_settable 0x0085B9B0
inline LuaSetTableFn LuaSetTable = At<LuaSetTableFn>(0x0085B9B0);
using LuaPushVectorFn = void(__cdecl*)(void* L, const float* v);  // SporeLabsLua_PushVector 0x00925ED0
inline LuaPushVectorFn LuaPushVector = At<LuaPushVectorFn>(0x00925ED0);
constexpr std::uintptr_t kThreadLuaState = 0x08;
// The ability data container (object +0x8A8): ability instance ids +0x08..+0x0C, modifier ids +0x9C..+0xA0.
namespace container {
constexpr std::uintptr_t kAbilitiesBegin = 0x08;
constexpr std::uintptr_t kAbilitiesEnd = 0x0C;
constexpr std::uintptr_t kModifiersBegin = 0x9C;
constexpr std::uintptr_t kModifiersEnd = 0xA0;
// D1j: the rest of the 0x560-byte element (AbilityDataContainer_Construct 0x009050F0): +0 its pool handle, +4 the
// object's id; three fixed vectors {begin, end, capacity end} whose inline buffers lie inside the element (+0x08: 32
// instance ids at +0x1C; +0x9C: 32 modifier ids at +0xB0; +0x340: 4 spell-shield records of 0x18 bytes at +0x358,
// AbilityDataContainer_RecordSpellShield 0x009034C0, grown on the heap by 0x00901DD0 with Alloc 0x006A9B50); two fixed
// hash maps keyed by ability GUID (+0x130: the cooldowns, node 0x30 with its next at +0x28, AbilityDataContainer_
// GetCooldown 0x009030E0; +0x3B8: the second cooldown table, node 0x20 with its next at +0x18, 0x009031A0): +4 the
// bucket array (inline, +0x38; the 10000.0 load factor keeps it from growing), +8 the bucket count, 8 inline nodes,
// a ninth and later node from Alloc. No function in the client erases, clears or destroys any of them.
constexpr std::uintptr_t kHandle = 0x00;
constexpr std::uintptr_t kSpellShieldsBegin = 0x340;
constexpr std::uintptr_t kCooldowns = 0x130;
constexpr std::uintptr_t kCooldownNext = 0x28;
constexpr std::uintptr_t kCooldowns2 = 0x3B8;
constexpr std::uintptr_t kCooldown2Next = 0x18;
constexpr std::uintptr_t kMapBuckets = 0x04;
constexpr std::uintptr_t kMapBucketCount = 0x08;
}  // namespace container

// --- the component pools and the container's release (D1j; docs/SERVER_MODE.md *The ability data containers (D1j)*) --
// ComponentType_Register (0x0087F580, thiscall on its 0x34-byte descriptor, linked into the list at kComponentTypes):
// +0 name, +4 hash, +8 flags (1 client, 2 server), +0xC parent name, +0x10 its hash, +0x14 the object's field,
// +0x18/+0x1C/+0x20/+0x24 the per-simulator create, destroy, reset and get pool functions, +0x28 a per-object create,
// +0x2C a per-object destroy (0x0087EF20 calls it from GameObjectManager_OnObjectDestroyed for every type whose flag
// bit matches the simulator), +0x30 the next descriptor. re/scripts/component_types.py lists all 40
// (docs/data/component_types.txt); cAbilityDataContainer (hash 0x850A1302, flags 2, field +0x8A8) is the only type
// with an object field and a pool whose element nothing releases.
namespace ctype {
constexpr std::uintptr_t kName = 0x00;
constexpr std::uintptr_t kHash = 0x04;
constexpr std::uintptr_t kFlags = 0x08;
constexpr std::uintptr_t kOffset = 0x14;
constexpr std::uintptr_t kNext = 0x30;
}  // namespace ctype
constexpr std::uint32_t kAbilityDataContainerType = 0x850A1302;
// A simulator's component manager is sim +0x550 (0x008EE590 returns the current simulator's; its first byte is the
// server flag 0x0087EF20 reads); its pool for a type: 0x0087F010 (thiscall, hash) -> cObjectPoolT* or 0.
constexpr std::uintptr_t kSimComponents = 0x550;
using ComponentPoolFn = void*(__thiscall*)(void* components, std::uint32_t hash);
inline ComponentPoolFn ComponentPool = At<ComponentPoolFn>(0x0087F010);
// cObjectPoolT (SPLib SPObjectPool.cpp): +0 data, +4 free list head, +8 free list size, +0xC serial, +0x10 live
// elements, +0x14 high water (slots ever used), +0x18 element size, +0x1C capacity, +0x20 name. A handle is
// serial << 16 | slot; an element's first dword is its handle (the free list's link while free).
namespace opool {
constexpr std::uintptr_t kLive = 0x10;
constexpr std::uintptr_t kHighWater = 0x14;
constexpr std::uintptr_t kElementSize = 0x18;
constexpr std::uintptr_t kCapacity = 0x1C;
constexpr std::uintptr_t kName = 0x20;
}  // namespace opool
// ObjectPool_TryToGet (0x00A81980, thiscall handle) -> element or null; ObjectPool_DeleteElement (0x00A81BB0,
// thiscall handle): back on the free list. The component releases call them as the cAgentBrain's (0x008AC5B0) and
// cAttributeData's (0x008D9320) do: TryToGet, the element's heap parts freed, its first dword set to the handle, Delete.
using PoolTryToGetFn = void*(__thiscall*)(void* pool, std::uint32_t handle);
inline PoolTryToGetFn PoolTryToGet = At<PoolTryToGetFn>(0x00A81980);
using PoolDeleteFn = void(__thiscall*)(void* pool, std::uint32_t handle);
inline PoolDeleteFn PoolDelete = At<PoolDeleteFn>(0x00A81BB0);
// The game's free (0x006A9B30, null-safe), the partner of Alloc (0x006A9B50).
using FreeFn = void(__cdecl*)(void* p);
inline FreeFn Free = At<FreeFn>(0x006A9B30);
// GameObjectManager_OnObjectDestroyed (0x008E4F40, thiscall (object manager, cGameObject*); slot 7 of cGameObjectManager's
// vtable 0x0124BE7C, which GameSimulator_DeletePendingObjects 0x008EECA0 calls through 0x008E4970 before the object's
// own release): only the holding instances of the container go (AbilityDataContainer_RemoveHoldingInstances
// 0x00906120), and the container stays allocated. Prologue: push ecx; push ebp; push esi; mov esi, [esp+0x10].
constexpr std::uintptr_t kOnObjectDestroyed = 0x008E4F40;
constexpr unsigned char kOnObjectDestroyedBytes[] = {0x51, 0x55, 0x56, 0x8B, 0x74, 0x24, 0x10};
using OnObjectDestroyedFn = void(__thiscall*)(void* objectManager, void* object);
// D12c: trigger volumes attached to an object (nTriggerVolumeUtils.AttachToObject_Sphere = nObjectManager_AttachTriggerVolume
// 0x009364B0: FUN_009541E0 makes the volume (cTriggerVolume pool hash 0x869DA269, element +0 = its handle, +4 = the physics
// shape, +0x54 = the attachment record) as a free kinematic shape at the owner's position, then AttachmentData_Create
// (0x008D6E70, cdecl (object*, kind 0 object / 1 volume, attached handle, transform*) -> the record's handle in pool hash
// 0xFFDE3EAE, +4 the owner's id, +0x40 kind, +0x44 the attached handle). Nothing in the build reads a kind-1 record to move
// the shape, so it stays where it was made. TriggerVolume_SetPose (0x00951FB0, cdecl (volume handle, const f32 pos[3],
// const f32 orientation[4])) moves its shape through the world's vtable slot 0xA4 (a kinematic actor's moveGlobalPose).
// Prologue of 0x008D6E70: push esi; push edi; push 0xFFDE3EAE.
namespace attach {
constexpr std::uintptr_t kCreate = 0x008D6E70;
constexpr unsigned char kCreateBytes[] = {0x56, 0x57, 0x68, 0xAE, 0x3E, 0xDE, 0xFF};
using CreateFn = int(__cdecl*)(void* object, int kind, std::uint32_t attached, const void* transform);
constexpr std::uint32_t kRecordPoolHash = 0xFFDE3EAE;
constexpr std::uintptr_t kRecordOwner = 0x04, kRecordKind = 0x40, kRecordAttached = 0x44;
constexpr std::uintptr_t kVolumeSetPose = 0x00951FB0;
using SetPoseFn = void(__cdecl*)(std::uint32_t volume, const float* position, const float* orientation);
inline SetPoseFn VolumeSetPose = At<SetPoseFn>(kVolumeSetPose);
}  // namespace attach
// nAbilityFns indices (GlobalDefinitions.lua); a static ability has handler i when byte static +0x14 + i is set.
namespace fns {
constexpr int kAbsorbDamage = 14, kAbsorbMagical = 15, kAbsorbPhysical = 16, kAbsorbAvailable = 17,
              kAbsorbAvailableMagical = 18, kAbsorbAvailablePhysical = 19, kOnNearbyDeath = 32, kOnGrantGold = 48,
              kOnPlayerDeath = 51, kOnKill = 65, kOnPlayerKillOrAssist = 66;
constexpr std::uintptr_t kHasHandler = 0x14;
}  // namespace fns
// The attribute snapshot (pool 0x0993DBD5; AttributeSnapshot_Create 0x008D8A80): +0 its id, +4 caster, +0xC team,
// +0xD player index, +0xE kill-credit player index, +0x10 the credited ability GUID, +0x14 the instance, +0x18 the
// original ability GUID.
namespace snap {
constexpr std::uintptr_t kId = 0x00;
constexpr std::uintptr_t kCaster = 0x04;
constexpr std::uintptr_t kTeam = 0x0C;
constexpr std::uintptr_t kPlayerIndex = 0x0D;
constexpr std::uintptr_t kCreditPlayerIndex = 0x0E;
constexpr std::uintptr_t kAbilityGuid = 0x10;
constexpr std::uintptr_t kInstance = 0x14;
constexpr std::uintptr_t kOriginalGuid = 0x18;
}  // namespace snap

// --- player death and respawn (D1g; docs/DAMAGE.md *D1g: player death and respawn on the host*) ---------------------
// labsPlayerGlobalData's death timer (cLabsPlayer; members 6 and 7, sent with LabsPlayerGlobalUpdate A9): u64 times on
// the server clock (GameTimeManager_GetTimeMs), which the client's reader (Reflect_ReadServerTimeField 0x008B43E0,
// TimeTranslator_ServerToClientTime 0x008F93B0) moves to its own clock (0 and -1 kept). The client's getters
// LabsPlayer_GetDeathTime / LabsPlayer_GetRespawnTime (0x00912400 / 0x00912420) feed its panels (0x005326F0: progress
// (now - death) / (respawn - death) and respawn - now; a respawn time in the future = dead). Nothing in the client
// writes them on a server; the host does.
namespace player {
constexpr std::uintptr_t kDeathTime = 0x270;    // mDeathTime
constexpr std::uintptr_t kRespawnTime = 0x278;  // mRespawnTime
}  // namespace player
namespace gameplayTuning {  // LevelGameplayTuning (Level data +0x614 -> asset, data +0x68)
constexpr std::uintptr_t kDeathTimeBase = 0x00;      // 15
constexpr std::uintptr_t kDeathTimePerLevel = 0x04;  // 0.5
constexpr std::uintptr_t kDeathTimePerMinute = 0x08; // 1.25
// D8b: the surrender part (docs/data/assetdata_types.txt; MATCH_FLOW.md *Surrender and the karma vote* §2). Nothing in
// the client reads them but +0x50 (the vote window's timer bar) and +0x60 (CA 04's required percent).
constexpr std::uintptr_t kSurrenderEnableMatchMinutes = 0x38;  // i64 surrenderEnableOn_MatchTimeInMinutes (20)
constexpr std::uintptr_t kSurrenderEnableEconomy = 0x40;       // f32 surrenderEnableOn_Economy (20000)
constexpr std::uintptr_t kSurrenderEnableKills = 0x44;         // i32 surrenderEnableOn_Kills (19)
constexpr std::uintptr_t kSurrenderEnableDisconnectS = 0x48;   // i64 surrenderEnableOn_DisconnectionTimeInSeconds (300)
constexpr std::uintptr_t kSurrenderVoteS = 0x50;               // i64 surrenderVoteDurationInSeconds (60)
constexpr std::uintptr_t kSurrenderCooldownS = 0x58;           // i64 surrenderVoteCooldownInSeconds (300)
constexpr std::uintptr_t kSurrenderThreshold = 0x60;           // f32 surrenderVoteSuccessThreshold (0.666)
}  // namespace gameplayTuning
// GameSimulator_GetPlayerStartPoint (0x008F3310, cdecl (sim, u8 team, u8 player index, float* position[3], float*
// orientation[4])): the level's player start for that team and the player's index within it (asserts on team >= 3 or
// no start point); what LabsPlayer_DeployCharacter and nGameSimulator.GetPlayerStartingPoint use.
using GetPlayerStartPointFn = void(__cdecl*)(void* sim, unsigned team, unsigned index, float* position, float* orientation);
inline GetPlayerStartPointFn GetPlayerStartPoint = At<GetPlayerStartPointFn>(0x008F3310);
constexpr unsigned char kGetPlayerStartPointBytes[] = {0x55, 0x8B, 0xEC, 0x83, 0xE4, 0xF8, 0x6A, 0xFF, 0x68, 0xE4, 0x39, 0x0A};
// ServerCommandQueue_QueueStop (0x00924010, cdecl (u8 player, creature id)): a stop command (type 2) into the player's
// queue, as nLocomotion.Teleport* do on a server before they teleport a player's creature.
using QueueStopFn = void(__cdecl*)(unsigned player, std::uint32_t creature);
inline QueueStopFn QueueStop = At<QueueStopFn>(0x00924010);
constexpr unsigned char kQueueStopBytes[] = {0x83, 0xEC, 0x48, 0x8D, 0x0C, 0x24, 0xE8};
// GameObject_Teleport (0x008E2510, cdecl (object, const float* position[3], const float* orientation[4], bool stop)):
// GameObject_SetPositionAndOrientation (0x008E2390), Locomotion_Stop when `stop`, and on a server ObjectTeleport (98) to
// the players that know the object (GameSimulator_QueueObjectTeleport 0x008F60C0, outgoing kind 2). The body of
// nLocomotion.TeleportToPositionAndOrientation (0x00935320), which ability_returntobase calls.
using TeleportFn = void(__cdecl*)(void* object, const float* position, const float* orientation, int stop);
inline TeleportFn Teleport = At<TeleportFn>(0x008E2510);
constexpr unsigned char kTeleportBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x74, 0x49};
// GameObject_Resurrect (0x008E05C0, cdecl object): CombatantData_ResetToFull (HP = max, shields 0) and GameObject_OnRevived
// (0x008E0510): the last-player-to-hit reset, for a player-controlled object a call (player, 0) into a function the client
// build emptied (0x009A3F00, a shared no-op), then OnRevive (52) on its instances; for an NPC its AI passive first.
// nGameObject.ResurrectObject (0x009406D0) is exactly this call (the nub and parasite respawns use it).
using ResurrectFn = void(__cdecl*)(void* object);
inline ResurrectFn Resurrect = At<ResurrectFn>(0x008E05C0);
constexpr unsigned char kResurrectBytes[] = {0x56, 0x8B, 0x74, 0x24, 0x08, 0x85, 0xF6, 0x74, 0x18};

// --- player kill bookkeeping (D1h; docs/DAMAGE.md *D1h: player kill bookkeeping*) ---------------------------------
// cLabsPlayer's counters (+0x240..+0x284 are zeroed by the constructor 0x0090152B and the reset 0x00914C32; nothing in
// the client build writes them otherwise): labsPlayerGlobalData members 3-5 (A9) and the creep score (labsPlayerFowData
// member 3, AA); the getters 0x00912320/0x00912380/0x009123A0/0x009123C0 feed the scoreboard, the game-end stats
// (GameOverState_BuildPostGameStats 0x005441C0) and GameSimulator_GetTeamKills (0x008F2AB0, the team's mKills sum).
// The multi-kill count and the kill streak are read only by Damage_ReportHpChange (0x00912360 / 0x00912340) for a
// player's killing blow.
namespace player {
constexpr std::uintptr_t kRole = 0x08;        // mRole (u8, labsPlayerGlobalData member 9; RolesTuning's role index)
constexpr std::uintptr_t kKills = 0x240;      // mKills
constexpr std::uintptr_t kDeaths = 0x244;     // mDeaths
constexpr std::uintptr_t kAssists = 0x248;    // mAssists
constexpr std::uintptr_t kCreeps = 0x24C;     // the creep score (LabsPlayer_AddCreeps adds to it)
constexpr std::uintptr_t kMultiKill = 0x250;  // kills in the current multi-kill sequence (combat event 0x2000 if > 1)
constexpr std::uintptr_t kKillStreak = 0x260; // kills since the last death (0x4000 / 0x8000 against killStreakStart)
}  // namespace player
// LabsPlayer_AddCreeps (0x009123E0, cdecl (player, n)): +0x24C += n; the client calls it only from
// nPlayer.IncrementCreepScore (0x00947F50; modifier_Role_Gladiator's stored last hits).
using AddCreepsFn = void(__cdecl*)(void* player, int n);
inline AddCreepsFn AddCreeps = At<AddCreepsFn>(0x009123E0);
constexpr unsigned char kAddCreepsBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x85, 0xC0, 0x74, 0x0A};
// The assist tracker (sim +0xCD84; read by AssistTracker_GetFromSimulator 0x008EF530, only the reset 0x008EF590 writes
// it: the stripped server shell created it): 10 x 10 u64 server ms at [attacker * 10 + target], written by
// AssistTracker_RecordHit (0x008E1140, from Damage_RecordAttacker for player-on-player hits) and
// AssistTracker_HandleBuffEvent (0x008E0A70, from 0x008E1480 for modifiers: a buff passes the buffed teammate's
// credits of the last 10 s on to the caster, a debuff credits the caster). Both skip their writes while it is null.
namespace sim {
constexpr std::uintptr_t kAssistTracker = 0xCD84;
// The ground-height provider (nLocomotion.GetTerrainHeightAtPosition 0x009275C0 reads it from the current simulator
// through 0x008EE4D0): its vtable slot 1 is thiscall (const f32 position[3]) -> f32 (x87), the terrain's height there.
constexpr std::uintptr_t kGround = 0xD8;
}  // namespace sim
// Tuning holders read through GetTuningAsset (thiscall 0x006C19A0 = the holder's first word): asset, data at +0x68.
// BattlegroundsGameplayTuning: Damage_ReportHpChange reads killStreakStart (+0x10); multiKillTimeoutMs is an i64 at +0x8.
constexpr std::uintptr_t kBattlegroundsTuningHolder = 0x019D0C54;
namespace bgTuning {
constexpr std::uintptr_t kMultiKillTimeoutMs = 0x08;  // int64 (10,000)
constexpr std::uintptr_t kKillStreakStart = 0x10;     // u32 (3)
}  // namespace bgTuning
// UITuning: the damage tracker prunes its events older than deathRecapLongCombatTime x 1000 (0x008E16B0).
constexpr std::uintptr_t kUiTuningHolder = 0x019E1D3C;
namespace uiTuning {
constexpr std::uintptr_t kRecapShort = 0x04;  // deathRecapShortCombatTime (5)
constexpr std::uintptr_t kRecapLong = 0x08;   // deathRecapLongCombatTime (30)
}  // namespace uiTuning
// The level's role and level-up tunings (Level data; AssetData layouts, asset +0x68 -> data, arrays {pointer, count}).
namespace level {
constexpr std::uintptr_t kRolesTuning = 0x608;    // RolesTuning: +0 role[] (0xB0 each)
constexpr std::uintptr_t kLevelUpTuning = 0x60C;  // LevelUpTuning: +0 Level[] (0xC each: +0 xpRequired)
}  // namespace level
namespace roleTuning {
constexpr std::uint32_t kSize = 0xB0;
constexpr std::uintptr_t kKillGold = 0x18;    // enemyPlayerKillGoldModifier
constexpr std::uintptr_t kAssistGold = 0x1C;  // enemyPlayerAssistGoldModifier
constexpr std::uintptr_t kKillXp = 0x20;      // enemyPlayerKillOrAssistXPModifier
constexpr std::uintptr_t kRolePassives = 0x24;  // D13: rolePassive[] {ptr, count}, 8 bytes each: +0 FNV-1 (GUID), +4 name
}  // namespace roleTuning
namespace rewardsTuning {  // the player-kill part (the rest is above)
constexpr std::uintptr_t kPlayerKillXpPercent = 0x24;
constexpr std::uintptr_t kPlayerAssistShare = 0x38;      // {float*, count}
constexpr std::uintptr_t kPlayerKillStreakGold = 0x40;
constexpr std::uintptr_t kPlayerDeathStreakGold = 0x48;
constexpr std::uintptr_t kPlayerXpLevelGold = 0x50;      // [0.0] in the data; not used [assumed meaning unknown]
constexpr std::uintptr_t kPlayerLowerLevelGold = 0x58;
constexpr std::uintptr_t kPlayerHigherLevelGold = 0x60;
constexpr std::uintptr_t kPlayerLowerLevelXp = 0x68;
constexpr std::uintptr_t kPlayerHigherLevelXp = 0x70;
constexpr std::uintptr_t kFirstBloodGold = 0xAC;
}  // namespace rewardsTuning

// Ability instance fields (pool 0x3C41D3D8; LabsAbilities.cpp).
namespace inst {
constexpr std::uintptr_t kId = 0x0;
constexpr std::uintptr_t kType = 0x4;            // 0 ability, 1 modifier (thread mUserType = type != 0 ? 2 : 1)
constexpr std::uintptr_t kStatic = 0x8;          // cStaticAbility*; +0 its Lua table name
constexpr std::uintptr_t kAgent = 0xD4;          // object id of the agent / carrier
constexpr std::uintptr_t kTarget = 0xD8;         // target (ability) or caster (modifier) object id
constexpr std::uintptr_t kThreadId = 0xE8;       // GearsLua thread id
constexpr std::uintptr_t kHoldsAgent = 0xEC;     // cleared by nAbility.ReleaseAgent (0x00900280)
constexpr std::uintptr_t kStartMs = 0x208;
constexpr std::uintptr_t kStackCount = 0x210;    // D15: int; the stacking path (0x009070D0) passes +0x210 + 1 as
                                                 // desiredStackCount, the unstacking one (0x00907570) ends it at 1
constexpr std::uintptr_t kDurationMs = 0x220;
constexpr std::uintptr_t kInUse = 0x230;         // synchronous Lua calls running on it
constexpr std::uintptr_t kEndPending = 0x234;    // byte, EndAbility deferred
}  // namespace inst

// cStaticAbility (instance +8): +0x428 deactivationType (dword; written by the constructor 0x00901219
// and the copy 0x0090EA7C, read by no client instruction).
constexpr std::uintptr_t kStaticDeactivationType = 0x428;
// D6c: cStaticAbility +0x494 castOnTopFlags (nCastOnTopFlags: CastOnTop 1, CastOnTopOnReactivation 2,
// CastOnTopOfAbilitiesThatYield 4, YieldToOtherAbilities 8; read by LabsAbilities_CanCastOnTop 0x00904040, which
// LabsAbilities_RequestAbility asks before it pushes a behavior-tree request that would abort the running ability) and
// +0x49A canInterrupt (byte; the constructor 0x009010E0 writes 0, the reflection reads the script's value; no client
// instruction reads it: the stripped brain update did [inferred]. Set true only by the bots' movement abilities,
// ability_Bot_LanePosition and its kin, and ability_base_titan_zap; false by ability_Bot_AvoidFireNP, _CatchUpXP and
// the Guardian's, Titan's and parasite's abilities).
constexpr std::uintptr_t kStaticCastOnTopFlags = 0x494;
constexpr std::uintptr_t kStaticCanInterrupt = 0x49A;
// D6c: the ability data container's agent ability (AbilityDataContainer_GetAgentAbility 0x008A8800): +0x53C the
// behavior tree's UseAbility instance (BTAction_UseAbility_Start writes it, its abort interrupts it while it holds the
// agent), else +0x540 another agent ability holding the agent.
constexpr std::uintptr_t kContainerTreeAbility = 0x53C;
constexpr std::uintptr_t kContainerHeldAbility = 0x540;

// GearsLua thread fields (constructor 0x008740B0).
namespace thread {
constexpr std::uintptr_t kStatus = 0xF8;  // 1 new, 2 function pending, 0 yielded, 3 reset, -1 finished, -2 running or failed
}  // namespace thread

using LuaPushLStringFn = void(__cdecl*)(void* L, const char* s, unsigned len);  // lua_pushlstring 0x0085B500
inline LuaPushLStringFn LuaPushLString = At<LuaPushLStringFn>(0x0085B500);
using LuaGetTableFn = void(__cdecl*)(void* L, int idx);                         // lua_gettable 0x0085B710
inline LuaGetTableFn LuaGetTable = At<LuaGetTableFn>(0x0085B710);
using LuaToUserdataFn = void*(__cdecl*)(void* L, int idx);                      // lua_touserdata 0x0085B3E0
inline LuaToUserdataFn LuaToUserdata = At<LuaToUserdataFn>(0x0085B3E0);
constexpr int kLuaGlobalsIndex = -10002;
using LuaToStringFn = const char*(__cdecl*)(void* L, int idx, unsigned* len);  // lua_tolstring 0x0085B2D0
inline LuaToStringFn LuaToString = At<LuaToStringFn>(0x0085B2D0);
// Lua 5.1 C API, located through the base library's registration table at 0x01233CD0
// (luaB_loadstring 0x0085D8F0 -> luaL_loadbuffer, luaB_pcall 0x0085DC30 -> lua_pcall, lua_gettop).
using LuaLoadBufferFn = int(__cdecl*)(void* L, const char* buf, unsigned size, const char* name);
inline LuaLoadBufferFn LuaLoadBuffer = At<LuaLoadBufferFn>(0x0085C9A0);
using LuaPcallFn = int(__cdecl*)(void* L, int nargs, int nresults, int errfunc);
inline LuaPcallFn LuaPcall = At<LuaPcallFn>(0x0085BC20);
using LuaGetTopFn = int(__cdecl*)(void* L);
inline LuaGetTopFn LuaGetTop = At<LuaGetTopFn>(0x0085AE90);
using LuaSetTopFn = void(__cdecl*)(void* L, int idx);
inline LuaSetTopFn LuaSetTop = At<LuaSetTopFn>(0x0085AEA0);

// --- cGameSimulator fields (offsets) ---------------------------------------------------------
namespace sim {
constexpr std::uintptr_t kRunning = 0x14;           // byte; the client tick runs only if set and +0x16 != 1
constexpr std::uintptr_t kState16 = 0x16;           // byte
constexpr std::uintptr_t kIsClient = 0x17;          // byte mbClientInstance (IsClientGameSimulator 0x008EFC80)
constexpr std::uintptr_t kObjectManager = 0xD4;
constexpr std::uintptr_t kWorld = 0xDC;             // embedded interface, vtable slot 3 = tick(dt)
constexpr std::uintptr_t kComponentMode = 0x550;    // byte: component storage map mode (0x0087EE10)
constexpr std::uintptr_t kReplayRecorder = 0x55C;   // server only: records player adds/removes and messages per
                                                    // frame (ReplayRecorder_RecordPlayerAdded 0x00921B00, read back by
                                                    // ReplayPlayer_Update 0x00920DF0); D1 had it as the command queue
                                                    // (the per-player command queues are at +0x574, D7-static)
constexpr std::uintptr_t kAIDirector = 0x560;       // cAIDirector (server only, 0x008AF5F0)
constexpr std::uintptr_t kPathfinder = 0x564;
constexpr std::uintptr_t kLua = 0x6614;
constexpr std::uintptr_t kBotDirector = 0xCD80;     // cBotDirector (server only, 0x008B0650)
constexpr std::uintptr_t kFow = 0xCCB0;
constexpr std::uintptr_t kLevel = 0x19DF0;          // level asset (SetLevel)
constexpr std::uintptr_t kOutgoingBegin = 0x1A998;  // vector of 28-byte entries (GameSimulator_QueueOutgoingMessage 0x008F4410)
constexpr std::uintptr_t kOutgoingEnd = 0x1A99C;
constexpr std::uintptr_t kNetworkRateTimers = 0x1C5B0;  // D2g: 5 x 16 bytes {u8 fired, -, u64 ms}
constexpr std::uintptr_t kNetworkRateMask = 0x1C600;    // D2g: GameSimulator_GetNetworkRateMask 0x008EF570 reads it
constexpr std::uintptr_t kObjectUpdates = 0x1C5AC;  // count of queued object updates (GameSimulator_QueueObjectUpdate 0x008F4370)
constexpr std::uintptr_t kLuaThreadId = 0x1CFD4;    // server: its Lua thread in the master state
}  // namespace sim

// cLuaSimulator (at sim+0x6614): thread table and per-tick resume counter (0x00875CF0, 0x00875B20).
namespace luasim {
constexpr std::uintptr_t kLuaState = 0xC;           // lua_State* (a server simulator shares the master's, 0x008739D0)
constexpr std::uintptr_t kCheckingSleeping = 0x60;  // byte, set while the scheduler walks the sleeping set (phase 1)
constexpr std::uintptr_t kThreadBuckets = 0x118;    // hash table: bucket array, nodes {key, next}
constexpr std::uintptr_t kThreadBucketCount = 0x11C;
constexpr std::uintptr_t kResumesThisTick = 0x61A4;  // reset by the scheduler, +1 per resume
}  // namespace luasim

// cGameObject fields the census reads (set by the object manager's OnObjectCreated 0x008E5310).
namespace obj {
constexpr std::uintptr_t kNoun = 0x4;
constexpr std::uintptr_t kPendingDelete = 0x55;
constexpr std::uintptr_t kAbilityData = 0x8A8;       // ability data container (server: 0x00906680)
constexpr std::uintptr_t kLuaThread = 0x8B0;         // server: object Lua thread (0x008E4820)
constexpr std::uintptr_t kBehaviorTree = 0x89C;      // server: behavior tree data (0x008E1CC0)
constexpr std::uintptr_t kReplicated = 0x8BC;        // GameSimulator_QueueObjectUpdate needs it non-zero
constexpr std::uintptr_t kBrain = 0x8C0;             // agent brain (0x008AE8D0)
constexpr std::uintptr_t kAgent = 0x8C4;             // agent / aggro list (+0x430/+0x434)
constexpr std::uintptr_t kMover = 0x8DC;             // locomotion mover (0x008A6CA0)
}  // namespace obj

// cAIDirector (sim+0x560, 0x20 bytes, 0x008AF5F0): byte +0 = active (AIDirector_IsActive 0x008AF220).
// Nothing in the client sets it; GameWorld's marker loader asserts it (GameWorld.cpp:369).
namespace aidir {
constexpr std::uintptr_t kActive = 0x0;
}  // namespace aidir

// --- regeneration (D1i; docs/DAMAGE.md *D1i: regeneration on the host*) ---------------------------------------------
// Nothing in the client applies HealthRegen / ResourcePointRegen / ResourcePointPercentRegen: their only readers are the
// attribute code itself (AttributeData_ComputeAttribute 0x008D8360 through the class base 0x008D7340 and the per-level
// part 0x008D7470) and the HUD (AbilityPanelUI 0x00408130: "Hp5" = TotalHealthRegen, "Mp5" = ResourcePointRegen). The
// step that spent them per frame went with the server's frame update; the host runs regen.cpp instead.
namespace regenAttr {  // nAttributeType / nDerivedAttributeType (GlobalDefinitions.lua)
constexpr int kMaxHealth = 2, kMaxResourcePoints = 3, kHealthRegen = 4, kResourcePointRegen = 5,
              kResourcePointPercentRegen = 6, kDisableResourcePointRegen = 7, kHealingReduction = 39, kHealIncrease = 40;
constexpr int kTotalHealthRegen = 72 + 33;  // derived: HealthRegen x (1 + HealIncrease) x (1 - HealingReduction), 0x008DB1A0
}  // namespace regenAttr
namespace combatant {
constexpr std::uintptr_t kResourcePoints = 0x58;  // mResourcePoints (CombatantDataUpdate member 4)
}  // namespace combatant
// GameObject_GetClassAttributes (0x008DE350, cdecl object): object +0x7C (the ClassAttributes asset) +0x68, its data
// (+0 characterResourceType, +8/+0xC tieredHealth array and count; docs/data/assetdata_types.txt).
using GetClassAttributesFn = char*(__cdecl*)(void* object);
inline GetClassAttributesFn GetClassAttributes = At<GetClassAttributesFn>(0x008DE350);
constexpr std::uintptr_t kClassTierCount = 0x0C;
// GameObject_GetResourceType (0x008DE370, cdecl object): characterResourceType (nCharacterResourceType; 8 = Health).
using GetResourceTypeFn = int(__cdecl*)(void* object);
inline GetResourceTypeFn GetResourceType = At<GetResourceTypeFn>(0x008DE370);
// GameObject_GetCurrentHealthBar (0x0087E420, cdecl object): the index of the health bar the HP is in (0 = the first,
// lost first; 0 without tiers). GameObject_GetHealthBarMax (0x0087E2F0, cdecl (object, index), ST0): bar `index`'s
// health (tieredHealth + (level - 1) x tieredHealthPerLevel; the max HP without tiers). Both clamp the tier count to 4
// above 4 (the constant 0x011BA3AC).
using GetCurrentHealthBarFn = unsigned(__cdecl*)(void* object);
inline GetCurrentHealthBarFn GetCurrentHealthBar = At<GetCurrentHealthBarFn>(0x0087E420);
using GetHealthBarMaxFn = float(__cdecl*)(void* object, unsigned index);
inline GetHealthBarMaxFn GetHealthBarMax = At<GetHealthBarMaxFn>(0x0087E2F0);
constexpr unsigned char kGetCurrentHealthBarBytes[] = {0x83, 0xEC, 0x08, 0x53, 0x8B, 0x5C, 0x24, 0x10};
constexpr unsigned char kGetResourceTypeBytes[] = {0x8B, 0x44, 0x24, 0x04, 0x85, 0xC0, 0x75, 0x39};

// --- the headless host's start-up (H1-live; docs/SERVER_MODE.md *Headless host, stage 2 (H1-live)*) ----------------
// Dawngate_Main 0x006B2E20 -> SporeApp_Initialize 0x006B1FD0 (SP_App: +0xC frame on, +0xD loop on) -> MainLoop_Run
// 0x006B1E80 (while +0xD: MainLoop_RunFrame 0x006B1B80 unless re-entered, then the window manager's pump, slot 36
// 0x00763DA0, the only PeekMessageW). MainLoop_RunFrame does nothing while +0xC is 0; otherwise the loader's update
// (0x00AE3AE0), the frame-rate limit, the message manager's dispatch (slot 14), the UI, audio and the app system's
// update and render (slots 29, 30).
namespace startup {
// Step 0 (startup_marks): call-site probes. kind 0 = `call rel32` to `callee`; kind 1 = `prefix` then `call eax`
// (FF D0), the prefix (no relative operand) re-executed by the probe's stub. The site gets `call stub` (+ NOPs).
struct ProbeSite {
  std::uintptr_t site;
  std::uint8_t kind;
  std::uintptr_t callee;          // kind 0
  std::uint8_t prefix[6];         // kind 1
  std::uint8_t prefixLen;         // kind 1
  const char* what;
};
inline constexpr ProbeSite kProbeSites[] = {
    {0x006B20E3, 0, 0x0096CCD0, {}, 0, "AppSystem_Create"},
    {0x006B229A, 0, 0x00AE3A10, {}, 0, "Resources_Initialize (depots, every *.snap mounted)"},
    {0x006B2651, 1, 0, {0x68, 0x98, 0xA5, 0x1F, 0x01}, 5, "window manager slot 12: the main window"},
    {0x006B26CD, 1, 0, {0x8B, 0x42, 0x1C, 0x55}, 4, "app system slot 7 (SP_App +0x28)"},
    {0x006B2742, 1, 0, {0x8B, 0x42, 0x20, 0x6A, 0x00, 0x55}, 6, "app system slot 8 (SP_App +0x28)"},
    {0x006B27C7, 0, 0x0058FD80, {}, 0, "SP_Graphics (0x0058FD80)"},
    {0x006B2811, 1, 0, {0x8B, 0x42, 0x1C}, 3, "graphics system slot 7: the renderer"},
    {0x006B2831, 0, 0x006BF5A0, {}, 0, "SPWwise_InitSoundEngine"},
    {0x006B2873, 0, 0x00655C90, {}, 0, "Audio_LoadBanksFromPackage"},
    {0x006B28F4, 0, 0x00AFA3C0, {}, 0, "SP_UI/MainWin (0x00AFA3C0)"},
    {0x006B2A07, 1, 0, {0x68, 0x18, 0xA1, 0x1F, 0x01}, 5, "the AppInit message (queued; Battlegrounds_AppInit runs in the first frame)"},
    {0x006639C1, 0, 0x008D5010, {}, 0, "AssetCatalog_Initialize"},
    {0x006639C8, 0, 0x008EFCC0, {}, 0, "InitializeMasterGameSimulator"},
    {0x006639CF, 0, 0x00910140, {}, 0, "LabsGlobalData_Initialize"},
    {0x006639D6, 0, 0x0090D970, {}, 0, "NavPower_InitializeSystem"},
    {0x00663A19, 0, 0x006B3C50, {}, 0, "SporeOnline_Initialize"},
    {0x00663A55, 0, 0x00672350, {}, 0, "GameClient_Create (GameClient_Init)"},
    {0x0066D9FB, 0, 0x008F5650, {}, 0, "InitializeClientGameSimulator"},
    {0x0066DA44, 0, 0x005795E0, {}, 0, "GameGraphics_Create"},
    {0x0057850A, 0, 0x0040B3F0, {}, 0, "BakePortrait_Construct"},
    {0x0057903E, 0, 0x004EE0D0, {}, 0, "Preloader_Construct"},
    {0x00663AFA, 1, 0, {0x8B, 0x42, 0x04}, 3, "the game state machine's start (slot 1)"},
};
// Step 1 (nobake, nosound): CommandLine_FindSwitch (0x0084F360, thiscall (cmdline, const wchar_t* name, bool,
// eastl::wstring* value or 0, int start), ret 0x10): the switch's index or -1. Its callers for "nobake"
// (BakePortrait_Construct, 0x0040BA59) and "noSound" (SPWwise_InitSoundEngine, 0x006BF5C5) only test for -1.
constexpr std::uintptr_t kFindSwitch = 0x0084F360;
constexpr unsigned char kFindSwitchBytes[] = {0x83, 0xEC, 0x08, 0x53, 0x55, 0x56, 0x57};  // sub esp,8; push x4
// Step 2 (preload): Preloader_Construct (0x004EE0D0) hands PreloadList_ForEach (0x004EDF20) the callback
// Preloader_LoadDirectory (0x004EDB80, thiscall (preloader, const char* dir) -> bool, ret 4) with `push imm32` at
// 0x004EE13C; ForEach calls it per line of script/preload.txt and only ANDs the results.
constexpr std::uintptr_t kPreloadCallbackPush = 0x004EE13C;
constexpr unsigned char kPreloadCallbackPushBytes[] = {0x68, 0x80, 0xDB, 0x4E, 0x00};
constexpr std::uintptr_t kPreloaderLoadDirectory = 0x004EDB80;
// Step 3 (core_only): SporeApp_Initialize from the jump point to 0x006B2D93 (the success byte; then +0xD = 1, the
// locals' destructors and the return) is skipped. Two jump points, both at the body's stack depth with the EH state
// byte [esp+0x80] = 3 (set at 0x006B2598, next changed at 0x006B27B9; 3 again at 0x006B2D76), and the three local
// strings the exit destroys (esp+0x28, +0x38, +0x58) are built before either:
//  - core_only = 1, 0x006B26C8 (`mov ecx,[edi+0x28]; mov edx,[ecx]`, a branch target): from the app system's slot 7
//    (0x0096B3F0: the D3D9 device through the render manager's slot 5, "Could not create the Graphics Device") and
//    slot 8 (0x0096F0C0: shaders, effects resources, config.smt, the camera) on; found by H1-live's probes (the
//    device is made here, before SP_Graphics: +28 MB private and +268 MB of address space, then +40 MB / +35 MB);
//  - core_only = 2, 0x006B279E (the SP_Graphics allocation's first push), H1-static's point: the device and the
//    graphics resources stay, the rest (SP_Graphics, Wwise and its banks, the UI, the AppInit message and with it
//    Battlegrounds_AppInit, the state manager, the frame timer, +0xC = 1) goes.
constexpr std::uintptr_t kCoreJumpFromDevice = 0x006B26C8;
constexpr unsigned char kCoreJumpFromDeviceBytes[] = {0x8B, 0x4F, 0x28, 0x8B, 0x11};
constexpr std::uintptr_t kCoreJumpFromGraphics = 0x006B279E;
constexpr unsigned char kCoreJumpFromGraphicsBytes[] = {0x6A, 0x00, 0x6A, 0x00, 0x6A, 0x00};
constexpr std::uintptr_t kCoreJumpTo = 0x006B2D93;
// d3d9!Direct3DCreate9 (imported by name, the exe's only d3d9 import; RwDevice_Initialize 0x00B914C0 calls it): the
// host's IAT watch logs and counts every call (since H1d in every host; test_no_d3d9 answers NULL).
constexpr char kD3d9Dll[] = "d3d9.dll";
// H1d (gms/core/d3d9/stand_in.h): the app system's
// systems set-up (slot 5, 0x0096A7B0, called at 0x006B25F5, before the core-only jump) creates the graphics system
// (0x00A63A20) -> GraphicsSystem_CreateRenderManager (0x00A63540) -> RenderManager_InitializeDevice (0x00997150,
// render manager vtable 0x012A8970 slot 3), whose `call RwDevice_Initialize` at 0x00997195 is the only call of it.
// RwDevice_Initialize: RW driver globals reset (0x00B89020, cdecl, returns 1: g_RwD3D9, the device, the present
// parameters and the state tables zeroed), Direct3DCreate9(32) into g_RwD3D9 (0x01BCE0E0), D3DXCheckVersion(32, 43),
// GetAdapterCount into rwg_D3D9NumAdapters (0x01BD00A4), GetDeviceCaps HAL per adapter (else REF), the display mode
// (0x01BD007C), nine depth formats (0x0145DD34) through CheckDeviceFormat/CheckDepthStencilMatch, then MXCSR FTZ and
// _controlfp(_DN_FLUSH, _MCW_DN) (the systems set-up sets both again right after the renderer, the call at
// 0x0096AF5A, so the host's answer leaves them to it), the device state
// (0x01BD00A8) = 1 and a caps refresh that fails without a device (0x00B88C40(0)). With answerRwDevice the host's
// function replaces the call: the reset, g_RwD3D9 = the stand-in, no adapter, the state 1, success.
constexpr std::uintptr_t kRwDeviceInitializeCall = 0x00997195;
constexpr std::uintptr_t kRwDeviceInitialize = 0x00B914C0;
constexpr std::uintptr_t kRwDriverResetGlobals = 0x00B89020;
constexpr std::uintptr_t kRwD3D9 = 0x01BCE0E0;          // IDirect3D9* (rw::graphics, g_RwD3D9)
constexpr std::uintptr_t kRwNumAdapters = 0x01BD00A4;   // rwg_D3D9NumAdapters
constexpr std::uintptr_t kRwDeviceState = 0x01BD00A8;   // 0 cold, 1 after Initialize, 2 started (StartInternal)
// The config manager's set-up (SPAppConfigManager, vtable 0x012A1C10 slot 3, 0x00963750, also in the systems set-up)
// calls ConfigManager_BuildResolutionList (0x00961FA0, fastcall this) at 0x0096381B: with no adapter it asserts
// "numAdapters" (SPAppConfigManager.cpp 1969) and returns, with adapters it enumerates their modes through g_RwD3D9.
// With skipResolutionList the call is NOPed (the list stays empty; every reader checks its size).
constexpr std::uintptr_t kConfigResolutionListCall = 0x0096381B;
constexpr std::uintptr_t kConfigBuildResolutionList = 0x00961FA0;
// Battlegrounds_AppInit's simulation half (0x0066394C-0x006639D6), every one cdecl: four type registrations without
// arguments (string_cow 0x00950E50, 0x008F9280, asset types 0x008B57B0, SP_Simulator/CollectionData 0x0087CA90),
// AssetCatalog_Initialize(settings*) 0x008D5010, InitializeMasterGameSimulator(1) 0x008EFCC0,
// LabsGlobalData_Initialize(0) 0x00910140, NavPower_InitializeSystem(1) 0x0090D970.
constexpr std::uintptr_t kAppInitTypeSteps[] = {0x00950E50, 0x008F9280, 0x008B57B0, 0x0087CA90};
constexpr std::uintptr_t kAssetCatalogInitialize = 0x008D5010;
constexpr std::uintptr_t kInitializeMasterGameSimulator = 0x008EFCC0;
constexpr std::uintptr_t kLabsGlobalDataInitialize = 0x00910140;
constexpr std::uintptr_t kNavPowerInitializeSystem = 0x0090D970;
// AssetCatalog_Initialize's settings (built at 0x0066397C-0x006639BC): +0 u16 0x0101, +4 eastl::wstring (begin, end,
// capacity, allocator) = the data folder (0x00A73790 returns it), assigned with 0x00402BA0 (thiscall (string, begin,
// end)). The empty string is 0x0199D2A0 (capacity 0x0199D2A2).
constexpr std::uintptr_t kDataFolder = 0x00A73790;
constexpr std::uintptr_t kWStringAssign = 0x00402BA0;
constexpr std::uintptr_t kEmptyWString = 0x0199D2A0;
// The part of SporeOnline_Initialize (0x006B3C50, 0x006B3CD3-0x006B3D57) the server needs: the GMS message types'
// transport ids. A transport-id allocator (0x14 bytes, TransportIdAllocator_Construct 0x00ADACA0: vtable 0x012B9F74,
// next byte +0x10 = 0x81) and the transport platform (4 bytes, 0x00ADB0E0, vtable 0x012B9FE4) that SnTransport_SetPlatform (0x00ADAB90) stores at 0x01BB6358,
// then MessageFactory_RegisterGmsMessages (0x00ADA830, thiscall (factory, allocator)) on GetMessageFactory()
// (0x00ADA7F0, created on first use). Without it every message the simulation builds gets no wire byte.
constexpr std::uintptr_t kTransportIdAllocatorConstruct = 0x00ADACA0;
constexpr std::uint32_t kTransportIdAllocatorSize = 0x14;
constexpr std::uintptr_t kSnPlatformConstruct = 0x00ADB0E0;
constexpr std::uintptr_t kSnTransportSetPlatform = 0x00ADAB90;
constexpr std::uintptr_t kGetMessageFactory = 0x00ADA7F0;
constexpr std::uintptr_t kRegisterGmsMessages = 0x00ADA830;
constexpr std::uintptr_t kSporeNetAllocName = 0x011EEC5C;  // "SP_SporeNet", the allocation name SporeOnline uses
// What MainLoop_RunFrame would do per frame that the server may need: the loader's update (0x00AE3AE0: the Loader
// 0x01BB6828's async completions) and the message manager's dispatch (SP singleton 0x00A6F8C0 -> slot 14, +0x38).
constexpr std::uintptr_t kLoaderUpdate = 0x00AE3AE0;
constexpr std::uintptr_t kGetMessageManager = 0x00A6F8C0;
constexpr std::uintptr_t kMessageManagerDispatchSlot = 0x38;
// The Gears module's frame update (0x00663330, its vtable 0x011EEB28 slot 12; called with the frame's dt in seconds as
// a float) starts with the parts the server needs, before the client game's: GameClock_Advance (0x008F9310: the global
// game clock 0x019E21B8 (u64 ms, +elapsed capped at 333 ms; the frame count 0x019E21B0) that GameTimeManager_Tick
// 0x008F9DF0 reads; GameClock_Initialize 0x008F9280 is Battlegrounds_AppInit's second call), NavPower's frame
// (0x0089D7E0(dt) -> 0x00C5A5C0: the bfx system update under its lock) and the update list (0x0087EA20(dt): slot 8 of
// every object 0x0087EAA0 created with the "updated" flag, list 0x019A32B0). In a core-only host nothing calls
// it, so the host's frame does these three (the first core-only session, h1_run4c, ran without them: its match clock
// stood at 0.0 s).
constexpr std::uintptr_t kGameClockAdvance = 0x008F9310;
constexpr std::uintptr_t kNavPowerFrame = 0x0089D7E0;
constexpr std::uintptr_t kUpdateListFrame = 0x0087EA20;
}  // namespace startup

}  // namespace game
