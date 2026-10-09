// The match flow's engine-free rules (D1c-live): see flow.h.
#include "core/matchflow/flow.h"

#include <algorithm>
#include <cmath>

namespace gms {
namespace flow {

bool WaveSchedule::Due(double matchSeconds) const {
  if (delay < 0.0) return false;
  if (limit > 0 && next >= limit) return false;
  if (interval <= 0.0 && next > 0) return false;
  return matchSeconds >= TimeOf(next);
}

uint32_t EndReasonForDeadGuardian(int deadTeam) {
  if (deadTeam == 2) return 1;  // the Spirit Guardian died: Mortal wins
  if (deadTeam == 1) return 3;  // the Mortal Guardian died: Spirit wins
  return 0;
}

int WinnerOfReason(uint32_t reason) {
  if (reason == 1 || reason == 2) return 1;
  if (reason == 3 || reason == 4) return 2;
  return 0xFF;
}

uint64_t EnergyLineTime(float value, uint64_t nowMs) {
  if (value < 0.f) return ~static_cast<uint64_t>(0);
  if (value == 0.f) return 0;
  return nowMs + static_cast<uint64_t>(static_cast<double>(value) * 1000.0);
}

int TeamIndex(int team) { return team == 1 ? 0 : team == 2 ? 1 : -1; }

namespace {
// "Binding_Mortal_Outer.Noun" -> "Binding_Mortal_Outer" (asset names carry their class).
std::string Base(const std::string& name) {
  const size_t dot = name.find('.');
  return dot == std::string::npos ? name : name.substr(0, dot);
}
}  // namespace

int BindingTier(const std::string& name) {
  const std::string n = Base(name);
  if (n.rfind("Binding_", 0) != 0) return -1;
  const size_t us = n.rfind('_');
  if (us == std::string::npos || us < 8) return -1;
  const std::string tier = n.substr(us + 1);
  if (tier == "Outer") return 0;
  if (tier == "Middle") return 1;
  if (tier == "Inner") return 2;
  return -1;
}

int BindingTeam(const std::string& name) {
  const std::string n = Base(name);
  if (n.rfind("Binding_Mortal_", 0) == 0) return 1;
  if (n.rfind("Binding_Spirit_", 0) == 0) return 2;
  return 0;
}

int NubTeam(const std::string& name) {
  const std::string n = Base(name);
  if (n.rfind("nub_mortal_", 0) == 0) return 1;
  if (n.rfind("nub_spirit_", 0) == 0) return 2;
  return 0;
}

std::vector<uint32_t> BaseStructuresAgainst(int minionTeam, const std::vector<BaseStructure>& all) {
  std::vector<uint32_t> out;
  if (minionTeam != 1 && minionTeam != 2) return out;
  for (const BaseStructure& s : all)
    if (s.id && s.alive && (s.team == 1 || s.team == 2) && s.team != minionTeam) out.push_back(s.id);
  return out;
}

float DistanceToPolyline(float x, float y, const std::vector<Point>& pts) {
  float best = 3.0e38f;
  if (pts.empty()) return best;
  if (pts.size() == 1) return std::hypot(x - pts[0].x, y - pts[0].y);
  for (size_t i = 0; i + 1 < pts.size(); ++i) {
    const float ax = pts[i].x, ay = pts[i].y, bx = pts[i + 1].x, by = pts[i + 1].y;
    const float vx = bx - ax, vy = by - ay;
    const float len2 = vx * vx + vy * vy;
    float t = len2 > 0.f ? ((x - ax) * vx + (y - ay) * vy) / len2 : 0.f;
    if (t < 0.f) t = 0.f;
    if (t > 1.f) t = 1.f;
    const float d = std::hypot(x - (ax + t * vx), y - (ay + t * vy));
    if (d < best) best = d;
  }
  return best;
}

void FormationOffset(int i, float dirX, float dirY, float spacing, float* dx, float* dy) {
  const int row = i / 3, col = i % 3;
  const float along = spacing * static_cast<float>(1 - row);   // first row ahead (toward knot 0), then back
  const float across = spacing * static_cast<float>(col - 1);  // -1, 0, +1 across the lane
  // (-dirY, dirX) is the direction to the left of the lane
  *dx = dirX * along - dirY * across;
  *dy = dirY * along + dirX * across;
}

int StaggeredDueCount(double waveTime, double intervalSeconds, int total, double now) {
  if (total <= 0 || now < waveTime) return 0;
  if (intervalSeconds <= 0.0) return total;
  // member i is due at waveTime + i * interval; a microsecond of slack for the tick clock's rounding
  const double steps = (now - waveTime) / intervalSeconds + 1e-6;
  const double due = std::floor(steps) + 1.0;
  return due >= static_cast<double>(total) ? total : static_cast<int>(due);
}

GroupShape ShapeOf(const std::vector<Point>& pts) {
  GroupShape s;
  s.n = static_cast<int>(pts.size());
  if (pts.size() < 2) return s;
  size_t a = 0, b = 1;
  float closest = 3.0e38f, sumNearest = 0.f;
  for (size_t i = 0; i < pts.size(); ++i) {
    float nearest = 3.0e38f;
    for (size_t j = 0; j < pts.size(); ++j) {
      if (i == j) continue;
      const float d = std::hypot(pts[i].x - pts[j].x, pts[i].y - pts[j].y);
      if (d < nearest) nearest = d;
      if (j > i && d > s.length) {
        s.length = d;
        a = i;
        b = j;
      }
    }
    if (nearest < closest) closest = nearest;
    sumNearest += nearest;
  }
  s.closest = closest;
  s.meanNearest = sumNearest / static_cast<float>(pts.size());
  const float vx = pts[b].x - pts[a].x, vy = pts[b].y - pts[a].y;
  const float len = std::hypot(vx, vy);
  for (const Point& p : pts) {
    const float d = len > 0.f ? std::fabs((p.x - pts[a].x) * vy - (p.y - pts[a].y) * vx) / len
                              : std::hypot(p.x - pts[a].x, p.y - pts[a].y);
    if (d > s.width) s.width = d;
  }
  return s;
}

std::vector<int> AssignCampSpots(const std::vector<int>& tiers, const std::vector<float>& scales) {
  const int n = static_cast<int>(tiers.size()), m = static_cast<int>(scales.size());
  std::vector<int> out(n, -1);
  if (m == 0) return out;
  float minScale = scales[0];
  for (float s : scales) minScale = std::min(minScale, s);
  std::vector<int> big;  // markers above the smallest scale, largest first, ties in marker order
  for (int j = 0; j < m; ++j)
    if (scales[j] > minScale) big.push_back(j);
  std::stable_sort(big.begin(), big.end(), [&](int a, int b) { return scales[a] > scales[b]; });
  std::vector<int> byTier(n);  // creatures, highest tier first, ties in creation order
  for (int i = 0; i < n; ++i) byTier[i] = i;
  std::stable_sort(byTier.begin(), byTier.end(), [&](int a, int b) { return tiers[a] > tiers[b]; });
  std::vector<bool> used(m, false);
  for (std::size_t k = 0; k < big.size() && k < byTier.size(); ++k) {
    out[byTier[k]] = big[k];
    used[big[k]] = true;
  }
  int next = 0;
  for (int i = 0; i < n; ++i) {
    if (out[i] >= 0) continue;
    while (next < m && used[next]) ++next;
    if (next >= m) break;
    out[i] = next;
    used[next] = true;
  }
  return out;
}

Composition CompositionOf(const float upgrade0[4]) {
  Composition c;
  for (int k = 0; k < 4; ++k) {
    const float v = upgrade0[k];
    const int n = (v > 0.f && v <= 32.f) ? static_cast<int>(std::lround(v)) : 0;
    c.counts[k] = n;
  }
  return c;
}

}  // namespace flow
}  // namespace gms
