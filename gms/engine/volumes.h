// D12c/D12e: trigger volumes attached to an object follow it.
//
// nTriggerVolumeUtils.AttachToObject_Sphere (nObjectManager_AttachTriggerVolume 0x009364B0) makes the volume as a free
// kinematic physics shape at the owner's position and an attachment record (AttachmentData_Create 0x008D6E70, kind 1)
// that names the owner and the volume. Nothing in the client build reads a kind-1 record to move the shape (the
// physics step only pulls the objects' own bodies, GamePhysicsPhysX.cpp 0x615/0x69d), so on the host a volume stayed
// where it was made: it fired for what stood inside it then and for nothing that came or left later (Marah's trees,
// seeds and markers, Flin's return, Zalgus's tether range, Kensu's marker, perks and items:).
//
// The host records every kind-1 attachment made on the server simulator (a detour on AttachmentData_Create) and, before
// the world's tick, moves each volume to its owner's position when the owner has moved
// (TriggerVolume_SetPose 0x00951FB0, the world's moveGlobalPose), so the world's trigger step reports the enters and
// exits. An entry ends when its record or its owner is gone.
// [host] volume_follow (1), volume_log_limit (40).
#pragma once

#include <cstddef>

namespace volumes {

struct Options {
  bool follow = true;
  int logLimit = 40;
};

bool Install(const Options& options);
bool Installed();
void SetSim(void* sim);

// Before the world's tick, with the server simulator current.
void Tick();

void FormatStats(char* buf, std::size_t size);

}  // namespace volumes
