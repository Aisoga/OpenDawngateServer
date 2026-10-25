// The resource points a client gets in a CombatantDataUpdate (gms/core). No game addresses: the engine side
// (gms/engine/replicate.cpp) passes the object's value, its maximum and whether its resource is its health.
//
// The client's health and resource bar widget (HealthResource.cpp; the ability panel, the target panel and two other
// HUD panels have one) caches the resource's fraction (value / maximum, 0 while the maximum is 0) and rewrites its two number fields
// only when that fraction changes. The widget starts with the cached fraction 0, and the movie's number fields start
// with their placeholder text "9999". So a shaper whose resource is exactly 0 when the widget first looks at it (a
// pool that starts empty) shows "9999" until the value first changes. Sending a tiny positive value instead of an exact
// 0 makes the fraction differ from the cache: the widget then writes the whole numbers, "0" and the maximum, and the
// bar's fill is too small to see. The server's own value stays 0.
#pragma once

namespace gms {

// What goes on the wire in place of an exact 0. Far below any ability's cost (the client's own request check stays a
// refusal) and below 1 (the widget prints the whole part), but large enough that value / maximum stays a non-zero float
// for any pool.
constexpr float kResourceWireFloor = 0.001f;

// The value to send for an object's resource points: `value`, or kResourceWireFloor when the value is exactly 0, the
// object has a pool (a maximum above 0) and its resource is not its health.
inline float ResourceWireValue(float value, float maximum, bool healthResource) {
  if (healthResource || !(maximum > 0.f) || value != 0.f) return value;
  return kResourceWireFloor;
}

}  // namespace gms
