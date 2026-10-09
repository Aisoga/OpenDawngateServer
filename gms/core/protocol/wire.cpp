#include "core/protocol/wire.h"

namespace gms {

const GmsMessageType kGmsMessageTypes[kGmsMessageTypeCount] = {
    {0, "HelloReq", kGmsHelloReq}, {1, "HelloPlayer", kGmsHelloPlayer}, {2, "ReconnectPlayer", kGmsReconnectPlayer},
    {3, "Connected", kGmsConnected}, {4, "Goodbye", kGmsGoodbye}, {5, "PlayerJoined", kGmsPlayerJoined},
    {6, "PlayerDeparted", kGmsPlayerDeparted}, {7, "PlayerStatusUpdate", kGmsPlayerStatusUpdate},
    {8, "GameAborted", kGmsGameAborted}, {9, "OnLocalPlayerReconnect", kGmsOnLocalPlayerReconnect},
    {10, "UIDataState", kGmsUIDataState}, {11, "UISpiritWellUpdate", kGmsUISpiritWellUpdate},
    {12, "UICreepCampUpdate", kGmsUICreepCampUpdate}, {13, "ObjectCreateBegin", kGmsObjectCreateBegin},
    {14, "ObjectCreateComplete", kGmsObjectCreateComplete}, {15, "ObjectUpdate", kGmsObjectUpdate},
    {16, "ObjectDelete", kGmsObjectDelete}, {17, "AttachedEffectUpdate", kGmsAttachedEffectUpdate},
    {18, "ObjectEnterFoW", kGmsObjectEnterFoW}, {19, "ObjectPrepareForExitFoW", kGmsObjectPrepareForExitFoW},
    {20, "ObjectExitFoW", kGmsObjectExitFoW}, {21, "PlayerCharacterDeploy", kGmsPlayerCharacterDeploy},
    {22, "ObjectTeleport", kGmsObjectTeleport}, {23, "ObjectJump", kGmsObjectJump},
    {24, "ObjectAttachedToNavMesh", kGmsObjectAttachedToNavMesh},
    {25, "DynamicNavMeshUpdate", kGmsDynamicNavMeshUpdate}, {26, "LocomotionCommand", kGmsLocomotionCommand},
    {27, "ObjectJumpCancel", kGmsObjectJumpCancel}, {28, "LocomotionDataUpdate", kGmsLocomotionDataUpdate},
    {29, "ProjectileDataCreate", kGmsProjectileDataCreate}, {30, "AnimationDataUpdate", kGmsAnimationDataUpdate},
    {31, "AttributeDataUpdate", kGmsAttributeDataUpdate}, {32, "CombatantDataUpdate", kGmsCombatantDataUpdate},
    {33, "InteractableDataUpdate", kGmsInteractableDataUpdate},
    {34, "AgentBlackboardUpdate", kGmsAgentBlackboardUpdate}, {35, "DisabledObjectUpdate", kGmsDisabledObjectUpdate},
    {36, "ServerEvent", kGmsServerEvent}, {37, "ClientFeedbackEvent", kGmsClientFeedbackEvent},
    {38, "ActionCommandMsgs", kGmsActionCommandMsgs}, {39, "ActionCommandResponse", kGmsActionCommandResponse},
    {40, "AbilityRankUp", kGmsAbilityRankUp}, {41, "PlayerDamage", kGmsPlayerDamage},
    {42, "LabsPlayerGlobalUpdate", kGmsLabsPlayerGlobalUpdate}, {43, "LabsPlayerFowUpdate", kGmsLabsPlayerFowUpdate},
    {44, "ModifierCreated", kGmsModifierCreated}, {45, "ModifierUpdated", kGmsModifierUpdated},
    {46, "ModifierDeleted", kGmsModifierDeleted}, {47, "ClientAbilityMessage", kGmsClientAbilityMessage},
    {48, "SetAnimationState", kGmsSetAnimationState}, {49, "FreezeAnimation", kGmsFreezeAnimation},
    {50, "InitAnimationInfo", kGmsInitAnimationInfo}, {51, "SetObjectGfxState", kGmsSetObjectGfxState},
    {52, "GamePrepareForStart", kGmsGamePrepareForStart}, {53, "EnterPreGameFlow", kGmsEnterPreGameFlow},
    {54, "EnterGame", kGmsEnterGame}, {55, "CheatMessage", kGmsCheatMessage},
    {56, "CheatResponseMessage", kGmsCheatResponseMessage}, {57, "CombatEvent", kGmsCombatEvent},
    {58, "ReloadLevel", kGmsReloadLevel}, {59, "AbilityUpdate", kGmsAbilityUpdate},
    {60, "CinematicGMSMsgs", kGmsCinematicGMSMsgs}, {61, "DebugPing", kGmsDebugPing}, {62, "ClockSync", kGmsClockSync},
    {63, "BattlegroundsMsgs", kGmsBattlegroundsMsgs}, {64, "BattlegroundsPreGameMsgs", kGmsBattlegroundsPreGameMsgs},
    {65, "BattlegroundsPostGameMsgs", kGmsBattlegroundsPostGameMsgs}, {66, "MiniMapPing", kGmsMiniMapPing},
    {67, "ItemTransaction", kGmsItemTransaction}, {68, "BlackboardDataUpdate", kGmsBlackboardDataUpdate},
    {69, "ComponentDataUpdate", kGmsComponentDataUpdate}, {70, "ComponentMessage", kGmsComponentMessage},
    {71, "ShaperSelectMsgs", kGmsShaperSelectMsgs}, {72, "DeathRecap", kGmsDeathRecap},
    {73, "SurrenderMsgs", kGmsSurrenderMsgs}, {74, "FriendInfo", kGmsFriendInfo},
    {75, "DemoModeMsgs", kGmsDemoModeMsgs}, {76, "PauseMsgs", kGmsPauseMsgs},
};

const GmsMessageType* FindGmsByWire(uint8_t wire) {
  for (const GmsMessageType& t : kGmsMessageTypes)
    if (t.wire == wire) return &t;
  return nullptr;
}

const char* GmsName(uint8_t wire) {
  const GmsMessageType* t = FindGmsByWire(wire);
  return t ? t->name : "?";
}

}  // namespace gms
