// ShaperSelectMsgs (GMS id 71, wire 0xC8) and the pre-game loadout (BattlegroundsPreGameMsgs 0xC0 subtype 2), byte for
// byte as the client (build 1.0.903.854) reads and writes them (gms/core, D3-core). The layouts, the rules behind them
// and the golden bytes are docs/GMS_PROTOCOL.md *Shaper select (D2c, D3-static)* and *Script B (final)*; the reference
// encoder is re/scripts/shaper_select.py, whose `script-b` rows gms_tests compares these builders with (O1).
//
// GMS -> client (reader ShaperSelect_HandleGmsMessage 0x00521B50), subtype byte first:
//   01 u64 phase end (server ms, the ClockSync clock), u32 phase 1-4
//   02 n x {u32 Noun, u8 flags, u8 0 (never read), u32 offer id}            the player's own shaper list (n > 0)
//   03 {u8 index, u32 shaper, u32 skin, u8 role, u32 page (never read), u8 flags}   a player's 15-byte record
//   04 u32 Noun (not read), u8 result      answer to every C8 04 (0 = refused; either unlocks the client's UI)
//   05 u32 Noun (not read), u8 01          answer to C8 05; 00 asserts and leaves the UI locked: never sent
//   06 u8 index, u8 role                   echo of a preferred-role toggle
//   09 u8 n, n x u32 perk page id          the perk pages the role/perk phase offers (B7c)
//   0B u8 n (> 0), n x {u32 skin Noun, u8 0}   the locked shaper's skins, default first; before C8 05 01 and phase 3
//   0E u8 kind (0 ward, 1 announcer), u32 asset id   echo of a valid vanity pick
// Client -> GMS (senders 0x006B4480-0x006B4D20): 01 hello, 02 ready (records created), 04 u32 request, 05 u32 lock,
//   06 u8 preferred role, 07 u8 role, 08 u8 role lock, 0A u32 perk page, 0C u32 skin, 0D u32 skin lock,
//   0E u8 kind + u32 asset.
// Builders return the whole message, wire byte first; the parsers take the body after the wire byte.
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

#include "core/common/bytes.h"

namespace gms {

// ---- subtypes -------------------------------------------------------------------------------------------------------
// GMS -> client
constexpr uint8_t kShaperPhase = 0x01;
constexpr uint8_t kShaperList = 0x02;
constexpr uint8_t kShaperRecord = 0x03;
constexpr uint8_t kShaperRequestResult = 0x04;
constexpr uint8_t kShaperLockResult = 0x05;
constexpr uint8_t kShaperRoleToggle = 0x06;
constexpr uint8_t kShaperPerkPages = 0x09;
constexpr uint8_t kShaperSkins = 0x0B;
constexpr uint8_t kShaperVanity = 0x0E;
// client -> GMS
constexpr uint8_t kShaperHello = 0x01;           // shaper select created
constexpr uint8_t kShaperReady = 0x02;           // the player records exist (not sent as observer)
constexpr uint8_t kShaperRequest = 0x04;         // u32 Noun or kNounRandom: a card click
constexpr uint8_t kShaperLockIn = 0x05;          // u32 Noun (the record's): Lock in
constexpr uint8_t kShaperPreferredRole = 0x06;   // u8 role: the preferred-role check box
constexpr uint8_t kShaperSelectRole = 0x07;      // u8 role: a role card, and on entering phase 2
constexpr uint8_t kShaperLockRole = 0x08;        // u8 role: Lock in on the role/perk page, or the client on 2 -> 3
constexpr uint8_t kShaperPerkPage = 0x0A;        // u32 perk page id: each drop-down selection (D3-perks: also at once
                                                 // on C8 09 and on entering phase 2) and just before 08
constexpr uint8_t kShaperSelectSkin = 0x0C;      // u32 skin Noun
constexpr uint8_t kShaperLockSkin = 0x0D;        // u32 skin Noun: Lock in (skin); only a hidden debug button sends it
                                                 // (D3-perks: PhaseSkin.mcDebugLockIn, never shown)
constexpr uint8_t kShaperSelectVanity = 0x0E;    // u8 kind, u32 asset id

// ---- values ---------------------------------------------------------------------------------------------------------
constexpr uint32_t kNounRandom = 0xFFFFFFFFu;  // the client's own Random card (and a record's "random" shaper)

// Shaper-list entry flags (0x0051A66C-0x0051A891): selectable = bit 0, 1 or 5, not bit 4, and no teammate holds it.
constexpr uint8_t kShaperOwned = 0x01;
constexpr uint8_t kShaperFreeRotation = 0x02;   // icon_shaperRotation
constexpr uint8_t kShaperHidden = 0x10;
constexpr uint8_t kShaperAvailable = 0x20;      // available for a third reason (no icon; name unknown)
constexpr uint8_t kShaperSelectableFlags = kShaperOwned | kShaperFreeRotation | kShaperAvailable;  // filter "Available"

// Player-record flags (0x0051F753, 0x0051F8E9).
constexpr uint8_t kRecordShaperLocked = 0x01;
constexpr uint8_t kRecordRoleLocked = 0x02;

constexpr uint8_t kNumRoles = 4;  // 0 gladiator, 1 tactician, 2 hunter, 3 predator
constexpr uint8_t kVanityWard = 0;
constexpr uint8_t kVanityAnnouncer = 1;

// AssetData ShaperSelectTuning, in ms: the GMS keeps them, the client only counts down to the end it is sent.
struct ShaperSelectTimers {
  uint32_t pickMs = 62000;      // phase 1, ShaperSelectionTime
  uint32_t rolePerkMs = 45500;  // phase 2, RolePerkSelectTime
  uint32_t skinMs = 15500;      // phase 3, SkinSelectTime
  uint32_t revealMs = 16;       // phase 4, RevealTime
  uint32_t ForPhase(int phase) const {
    return phase == 1 ? pickMs : phase == 2 ? rolePerkMs : phase == 3 ? skinMs : revealMs;
  }
};
ShaperSelectTimers ShaperTimersNormal();    // 62000, 45500, 15500, 16
ShaperSelectTimers ShaperTimersPractice();  // 300000, 300000, 15500, 16

struct ShaperListEntry {
  uint32_t noun = 0;  // the shaper's default skin Noun (the shaper item's parentAsset, e.g. alpha.Noun)
  uint8_t flags = 0;
  uint32_t offer = 0;  // FNV-1 of the store offer's PUII; 0 = no purchase option in the details
};

struct ShaperRecord {
  uint8_t index = 0;
  uint32_t shaper = 0;  // 0 = nothing picked, kNounRandom = random
  uint32_t skin = 0;    // 0 = the shaper
  uint8_t role = 0;
  uint32_t page = 0;    // read by nothing; the C8 0A perk page
  uint8_t flags = 0;    // kRecordShaperLocked, kRecordRoleLocked
};

// One perk-page attribute of C0 02 (cPerkAttributeData {id, op, value}).
struct LoadoutAttribute {
  uint8_t attribute = 0;  // AssetData attribute enum 0x012464B8 (Power, Mastery, MaxHealth = 2, ...)
  uint8_t op = 1;         // 0 NoOp, 1 Add, 2 PercentChange, 3 Clamp, ...
  float value = 0.0f;
};

// ---- GMS -> client --------------------------------------------------------------------------------------------------
Bytes BuildShaperPhase(uint64_t endServerMs, uint32_t phase);
Bytes BuildShaperList(const std::vector<ShaperListEntry>& entries);  // empty -> empty Bytes (the client asserts n > 0)
Bytes BuildShaperRecord(const ShaperRecord& r);
Bytes BuildShaperRequestResult(uint32_t noun, bool ok);
Bytes BuildShaperLockResult(uint32_t noun);  // always result 01
Bytes BuildRoleToggle(uint8_t gameplayIndex, uint8_t role);
Bytes BuildPerkPages(const std::vector<uint32_t>& pageIds);
Bytes BuildSkins(const std::vector<uint32_t>& skinNouns);  // empty -> empty Bytes (the client asserts numSkins > 0)
Bytes BuildVanity(uint8_t kind, uint32_t asset);
// C0 02 (LoadoutManager_ReadPlayerLoadout 0x00886AC0): u8 index, u8 n, n x {u8 attribute, u8 op, f32 value}, u8 m,
// m x u32 modifier id (non-zero). Minimal: C0 02 <index> 00 00.
Bytes BuildPreGameLoadout(uint8_t gameplayIndex, const std::vector<LoadoutAttribute>& attributes,
                          const std::vector<uint32_t>& modifiers);

// Parses a C8 03 body after the subtype (15 bytes); for tests and decoders.
bool ParseShaperRecord(const uint8_t* p, size_t n, ShaperRecord* out);

// ---- client -> GMS --------------------------------------------------------------------------------------------------
struct ShaperSelectRequest {
  uint8_t sub = 0;   // kShaperHello ... kShaperSelectVanity
  uint32_t id = 0;   // Noun (04, 05, 0C, 0D), perk page (0A), asset (0E)
  uint8_t role = 0;  // 06, 07, 08
  uint8_t kind = 0;  // 0E
};
// The client's ShaperSelectMsgs body (after the wire byte): false for an unknown subtype or a short body.
bool ParseShaperSelectRequest(const uint8_t* body, size_t n, ShaperSelectRequest* out);
// What the client sends for `r` (wire byte first): the test client model's side.
Bytes BuildShaperSelectRequest(const ShaperSelectRequest& r);
// "hello", "ready", "request shaper", ... ("?" for an unknown subtype).
const char* ShaperRequestName(uint8_t sub);

}  // namespace gms
