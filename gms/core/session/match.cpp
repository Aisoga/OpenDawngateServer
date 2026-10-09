#include "core/session/match.h"

namespace gms {
namespace {

struct PointName {
  SessionPoint point;
  const char* name;
};
const PointName kPointNames[] = {
    {SessionPoint::kHello, "hello"},       {SessionPoint::kPrepare, "prepare"},
    {SessionPoint::kClock5, "clock5"},     {SessionPoint::kPreGameFlow, "pregameflow"},
    {SessionPoint::kShaperSelect, "shaperselect"}, {SessionPoint::kPicked, "picked"},
    {SessionPoint::kLoad, "load"},         {SessionPoint::kLoaded, "loaded"},
    {SessionPoint::kDone, "done"},         {SessionPoint::kEnterGame, "entergame"},
};

}  // namespace

const char* SessionPointName(SessionPoint p) {
  for (const PointName& n : kPointNames)
    if (n.point == p) return n.name;
  return "?";
}

bool ParseSessionPoint(const std::string& name, SessionPoint* out) {
  for (const PointName& n : kPointNames) {
    if (name == n.name) {
      *out = n.point;
      return true;
    }
  }
  return false;
}

}  // namespace gms
