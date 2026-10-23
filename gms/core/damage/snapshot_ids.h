// gms/core/damage: the two ability identities an attribute snapshot carries, and which of them the damage events
// (OnTookDamage, OnDealtDamage) report. Header-only, no game addresses.
//
// The snapshot (pool 0x0993DBD5, 0x150 bytes) holds a "credit" pair (+0x10 GUID, +0x14 instance) and an "own" pair
// (+0x18 GUID, +0x24 instance) [code]:
//   - AttributeSnapshot_Create (0x008D8A80), an instance's first snapshot: both pairs = (its GUID, its instance);
//   - AttributeSnapshot_CreateFromSnapshot (0x008D8C70), a snapshot made from another one (LabsAbilities_ActivateAbility
//     0x0090A390 for every modifier, the projectile helper's request): the credit pair is the source's credit pair
//     (where it is 0, the source's own pair; where that is 0 too, the new instance's); the own pair is the new
//     instance's, unless the new instance's static descriptors have IsUtility (0x1000; ability_track_homing_projectile
//     and the other helpers), which keeps the source's own pair.
// So the credit pair names the root ability (the cast), the own pair the ability or modifier whose script dealt the hit.
//
// What the events take [inferred from the two kinds of scripts that read them; the client's damage core is compiled
// out]:
//   - the GUID is the credit one: modifier_LoreMaster_Q_DamageLink deals its shared damage with its own snapshot and
//     skips hits whose tookDamageAbility is ability_LoreMaster_Q_Chains (its credit GUID), or it would share its own
//     damage again; modifier_Binding_Passive expects the tower attack's GUID for its projectile's hits;
//   - the instance is the own one: modifier_Item_Drain.OnTookDamage heals its caster only when tookDamageAbilityInstance
//     == nAbility.GetAbilityInstanceID() (the modifier's own instance; its ticks deal the damage with its own snapshot),
//     the only script that reads either instance field. With the credit instance (the Drain cast's, ended by then) the
//     test never held, and the battle spell Drain healed nobody.
#pragma once

namespace gms::damage {

constexpr unsigned kIsUtility = 0x1000;  // nDescriptors.IsUtility

struct SnapshotAbility {
  unsigned creditGuid = 0;      // +0x10
  unsigned creditInstance = 0;  // +0x14
  unsigned ownGuid = 0;         // +0x18
  unsigned ownInstance = 0;     // +0x24
};

// AttributeSnapshot_Create: an instance's first snapshot.
inline SnapshotAbility RootSnapshotAbility(unsigned guid, unsigned instance) {
  SnapshotAbility s;
  s.creditGuid = s.ownGuid = guid;
  s.creditInstance = s.ownInstance = instance;
  return s;
}

// AttributeSnapshot_CreateFromSnapshot: the snapshot of a new instance (`guid`, `instance`, the static ability's
// descriptors `staticDescLo`) made from `src`.
inline SnapshotAbility ChildSnapshotAbility(const SnapshotAbility& src, unsigned guid, unsigned instance,
                                            unsigned staticDescLo) {
  SnapshotAbility s;
  s.creditGuid = src.creditGuid ? src.creditGuid : src.ownGuid ? src.ownGuid : guid;
  s.creditInstance = src.creditInstance ? src.creditInstance : src.ownInstance ? src.ownInstance : instance;
  if (staticDescLo & kIsUtility) {
    s.ownGuid = src.ownGuid;
    s.ownInstance = src.ownInstance;
  } else {
    s.ownGuid = guid;
    s.ownInstance = instance;
  }
  return s;
}

// The events' tookDamageAbility / dealtDamageAbility: the credit GUID.
inline unsigned DamageEventGuid(const SnapshotAbility& s) { return s.creditGuid; }
// The events' tookDamageAbilityInstance / dealtDamageAbilityInstance: the own instance (the credit one when a snapshot
// has none).
inline unsigned DamageEventInstance(const SnapshotAbility& s) { return s.ownInstance ? s.ownInstance : s.creditInstance; }

}  // namespace gms::damage
