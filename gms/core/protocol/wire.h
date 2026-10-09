// GMS wire bytes (gms/core, G1): the first byte of every GMS message on the RakNet connection, after RakNet's own
// ids (< 0x81). From docs/data/gms_transport_ids.csv, D1's runtime read of the client's message factory (equal to
// gms_messages.py's static derivation). Only ids 0-20 and some later ranges are 0x81 + id; the others follow the
// factory's registration order (docs/GMS_PROTOCOL.md "Transport"). gms_tests checks this table against the CSV.
#pragma once

#include <cstddef>
#include <cstdint>

namespace gms {

enum GmsWire : uint8_t {
  kGmsHelloReq = 0x81,  // id 0, client -> GMS
  kGmsHelloPlayer = 0x82,  // id 1
  kGmsReconnectPlayer = 0x83,  // id 2
  kGmsConnected = 0x84,  // id 3
  kGmsGoodbye = 0x85,  // id 4
  kGmsPlayerJoined = 0x86,  // id 5
  kGmsPlayerDeparted = 0x87,  // id 6
  kGmsPlayerStatusUpdate = 0x88,  // id 7, client -> GMS
  kGmsGameAborted = 0x89,  // id 8
  kGmsOnLocalPlayerReconnect = 0x8A,  // id 9
  kGmsUIDataState = 0x8B,  // id 10
  kGmsUISpiritWellUpdate = 0x8C,  // id 11
  kGmsUICreepCampUpdate = 0x8D,  // id 12
  kGmsObjectCreateBegin = 0x8E,  // id 13
  kGmsObjectCreateComplete = 0x8F,  // id 14
  kGmsObjectUpdate = 0x90,  // id 15
  kGmsObjectDelete = 0x91,  // id 16
  kGmsAttachedEffectUpdate = 0x92,  // id 17
  kGmsObjectEnterFoW = 0x93,  // id 18
  kGmsObjectPrepareForExitFoW = 0x94,  // id 19
  kGmsObjectExitFoW = 0x95,  // id 20
  kGmsPlayerCharacterDeploy = 0xB3,  // id 21
  kGmsObjectTeleport = 0x98,  // id 22
  kGmsObjectJump = 0x96,  // id 23
  kGmsObjectAttachedToNavMesh = 0x99,  // id 24
  kGmsDynamicNavMeshUpdate = 0x9A,  // id 25
  kGmsLocomotionCommand = 0x9B,  // id 26
  kGmsObjectJumpCancel = 0x97,  // id 27
  kGmsLocomotionDataUpdate = 0x9C,  // id 28
  kGmsProjectileDataCreate = 0x9D,  // id 29
  kGmsAnimationDataUpdate = 0x9E,  // id 30
  kGmsAttributeDataUpdate = 0x9F,  // id 31
  kGmsCombatantDataUpdate = 0xA0,  // id 32
  kGmsInteractableDataUpdate = 0xA1,  // id 33
  kGmsAgentBlackboardUpdate = 0xA2,  // id 34
  kGmsDisabledObjectUpdate = 0xA3,  // id 35
  kGmsServerEvent = 0xA4,  // id 36
  kGmsClientFeedbackEvent = 0xA5,  // id 37
  kGmsActionCommandMsgs = 0xA6,  // id 38, client -> GMS
  kGmsActionCommandResponse = 0xB4,  // id 39
  kGmsAbilityRankUp = 0xA7,  // id 40, client -> GMS
  kGmsPlayerDamage = 0xA8,  // id 41
  kGmsLabsPlayerGlobalUpdate = 0xA9,  // id 42
  kGmsLabsPlayerFowUpdate = 0xAA,  // id 43
  kGmsModifierCreated = 0xAB,  // id 44
  kGmsModifierUpdated = 0xAC,  // id 45
  kGmsModifierDeleted = 0xAD,  // id 46
  kGmsClientAbilityMessage = 0xAE,  // id 47
  kGmsSetAnimationState = 0xAF,  // id 48
  kGmsFreezeAnimation = 0xB0,  // id 49
  kGmsInitAnimationInfo = 0xB1,  // id 50
  kGmsSetObjectGfxState = 0xB2,  // id 51
  kGmsGamePrepareForStart = 0xB5,  // id 52
  kGmsEnterPreGameFlow = 0xB6,  // id 53
  kGmsEnterGame = 0xB7,  // id 54
  kGmsCheatMessage = 0xB8,  // id 55
  kGmsCheatResponseMessage = 0xB9,  // id 56
  kGmsCombatEvent = 0xBA,  // id 57
  kGmsReloadLevel = 0xBB,  // id 58
  kGmsAbilityUpdate = 0xBC,  // id 59
  kGmsCinematicGMSMsgs = 0xBD,  // id 60
  kGmsDebugPing = 0xBE,  // id 61
  kGmsClockSync = 0xC7,  // id 62, both directions
  kGmsBattlegroundsMsgs = 0xBF,  // id 63
  kGmsBattlegroundsPreGameMsgs = 0xC0,  // id 64, both directions
  kGmsBattlegroundsPostGameMsgs = 0xC1,  // id 65
  kGmsMiniMapPing = 0xC2,  // id 66
  kGmsItemTransaction = 0xC3,  // id 67
  kGmsBlackboardDataUpdate = 0xC4,  // id 68
  kGmsComponentDataUpdate = 0xC5,  // id 69
  kGmsComponentMessage = 0xC6,  // id 70
  kGmsShaperSelectMsgs = 0xC8,  // id 71, both directions
  kGmsDeathRecap = 0xC9,  // id 72
  kGmsSurrenderMsgs = 0xCA,  // id 73
  kGmsFriendInfo = 0xCB,  // id 74, client -> GMS
  kGmsDemoModeMsgs = 0xCC,  // id 75, client -> GMS
  kGmsPauseMsgs = 0xCD,  // id 76
};

// The first byte of a GMS message; everything below is RakNet's.
constexpr uint8_t kGmsFirstWireByte = 0x81;
constexpr size_t kGmsMessageTypeCount = 77;

struct GmsMessageType {
  uint8_t id;        // the client's GMS message id (kGms* enum value in the client)
  const char* name;  // the client's name without "kGms"
  uint8_t wire;
};

// All 77, in id order.
extern const GmsMessageType kGmsMessageTypes[kGmsMessageTypeCount];

const GmsMessageType* FindGmsByWire(uint8_t wire);
// The name without "kGms", or "?" for a byte that is no GMS message.
const char* GmsName(uint8_t wire);

}  // namespace gms
