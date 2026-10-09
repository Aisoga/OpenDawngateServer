#include "script/hosted_match.h"

#include "core/common/log.h"
#include "script/roster_shaper.h"

namespace gms {

bool HostedMatch::ShaperSelectSetup(ShaperSelectConfig* config) {
  if (!inner_.ShaperSelectSetup(config)) return false;
  if (!control_.HasGame()) return true;
  for (const dggms::control::RosterEntry& e : control_.Game().roster) {
    if (e.bot || e.spectator || !HasShaperContent(e) || e.index < 0 || e.index >= 15) continue;
    bool taken = false;  // an --ally on the same index keeps it
    for (const ShaperSelectPlayer& p : config->players) taken = taken || p.gameplayIndex == e.index;
    if (taken) continue;
    ShaperSelectPlayer p;
    p.gameplayIndex = static_cast<uint8_t>(e.index);
    p.team = static_cast<uint8_t>(e.team);
    p.content = ShaperContentFromRoster(e);
    config->players.push_back(p);
    Logf("%s match: shaper select content from createGame's roster: [%lld: user %lld, %zu shapers, skins of %zu, %zu saved, "
         "%zu wards, %zu announcers, %zu perk pages]",
         Clock().c_str(), static_cast<long long>(e.index), static_cast<long long>(e.user), e.shapers.size(),
         e.skins.size(), e.savedSkins.size(), e.wards.size(), e.announcers.size(), e.perkPages.size());
  }
  return true;
}

bool HostedMatch::OnHelloReq(ConnId conn, const HelloReq& req, PlayerSlot* slot) {
  const auto user = static_cast<int64_t>(req.userId);
  const dggms::control::RosterEntry* e = control_.FindUser(user);
  if (!e) {
    Logf("%s match: HelloReq from user %lld refused: not on createGame's roster", Clock().c_str(),
         static_cast<long long>(user));
    return false;
  }
  if (!inner_.OnHelloReq(conn, req, slot)) return false;
  slot->gameplayIndex = static_cast<uint8_t>(e->index);
  slot->team = static_cast<uint8_t>(e->team);
  if (!e->noun.empty()) slot->noun = AssetIdOf(e->noun);
  if (e->spectator) {  // B15b: the session serves it as an observer (its index 10-14); its player's mTeam, no shaper
    slot->team = kSpectatorTeam;
    slot->noun = 0;
  }
  joined_[conn] = Joined{user, static_cast<int>(e->index), static_cast<int>(e->team)};
  Logf("%s match: HelloReq from user %lld (roster \"%s\") -> gameplay index %d, team %d, noun 0x%08x", Clock().c_str(),
       static_cast<long long>(user), e->name.c_str(), static_cast<int>(e->index), static_cast<int>(e->team), slot->noun);
  control_.PlayerAdded(user, e->index);
  return true;
}

void HostedMatch::OnPoint(ConnId conn, SessionPoint point, ISender& out) {
  inner_.OnPoint(conn, point, out);
  if (point == SessionPoint::kPreGameFlow && control_.Phase() == dggms::control::kPhaseLoading) {
    control_.SetPhase(dggms::control::kPhasePreGame, msNow_());
  } else if (point == SessionPoint::kEnterGame) {
    if (!enterGameMs_) enterGameMs_ = msNow_();
    if (control_.Phase() != dggms::control::kPhasePostGame) control_.SetPhase(dggms::control::kPhaseInGame, msNow_());
  }
}

void HostedMatch::OnDisconnected(ConnId conn) {
  inner_.OnDisconnected(conn);
  auto it = joined_.find(conn);
  if (it == joined_.end()) return;
  control_.PlayerGone(it->second.user, "disconnected");
  joined_.erase(it);
}

}  // namespace gms
