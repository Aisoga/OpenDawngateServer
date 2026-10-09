#include "script/scripted_match.h"

#include <cstdlib>
#include <fstream>

namespace gms {

bool ParseInject(const std::string& text, InjectSpec* out, std::string* error) {
  const size_t colon = text.find(':');
  if (colon == std::string::npos) {
    if (error) *error = "no ':' in '" + text + "' (expected <point>[+ms]:<hex>)";
    return false;
  }
  std::string point = text.substr(0, colon);
  // trim blanks around the point (lines of an inject file may be indented)
  const size_t first = point.find_first_not_of(" \t");
  const size_t last = point.find_last_not_of(" \t");
  point = first == std::string::npos ? std::string() : point.substr(first, last - first + 1);
  InjectSpec s;
  const size_t plus = point.find('+');
  if (plus != std::string::npos) {
    char* end = nullptr;
    const long ms = std::strtol(point.c_str() + plus + 1, &end, 10);
    if (end == point.c_str() + plus + 1 || *end || ms < 0) {
      if (error) *error = "bad delay in '" + point + "'";
      return false;
    }
    s.delayMs = static_cast<int>(ms);
    point.resize(plus);
  }
  if (!ParseSessionPoint(point, &s.point)) {
    if (error)
      *error = "unknown point '" + point +
               "' (hello, prepare, clock5, pregameflow, shaperselect, picked, load, loaded, done, entergame)";
    return false;
  }
  if (!ParseHex(text.substr(colon + 1), s.msg) || s.msg[0] < 0x81) {
    if (error) *error = "bad message hex in '" + text.substr(0, 60) + "' (even digit count, wire byte >= 0x81 first)";
    return false;
  }
  *out = std::move(s);
  return true;
}

int ReadInjectFile(const std::string& path, std::vector<InjectSpec>* out, std::string* error) {
  std::ifstream in(path);
  if (!in) {
    if (error) *error = "cannot open " + path;
    return -1;
  }
  std::string line;
  int n = 0, lineNo = 0;
  while (std::getline(in, line)) {
    ++lineNo;
    const size_t hash = line.find('#');
    if (hash != std::string::npos) line.resize(hash);
    if (lineNo == 1 && line.compare(0, 3, "\xEF\xBB\xBF") == 0) line.erase(0, 3);
    if (line.find_first_not_of(" \t\r\n") == std::string::npos) continue;
    InjectSpec s;
    std::string why;
    if (!ParseInject(line, &s, &why)) {
      if (error) *error = path + ":" + std::to_string(lineNo) + ": " + why;
      return -1;
    }
    out->push_back(std::move(s));
    ++n;
  }
  return n;
}

std::string DescribeInject(const InjectSpec& s, size_t maxBytes) {
  return std::string(SessionPointName(s.point)) + (s.delayMs ? "+" + std::to_string(s.delayMs) : std::string()) +
         ":" + Hex(s.msg, maxBytes);
}

void ScriptedMatch::OnConnected(ConnId conn, const std::string& addr) {
  (void)addr;
  conns_[conn] = ConnState{};
}

bool ScriptedMatch::OnHelloReq(ConnId conn, const HelloReq& req, PlayerSlot* slot) {
  (void)conn, (void)req;
  *slot = o_.slot;  // everyone gets the configured slot; the roster check comes with C1
  return true;
}

void ScriptedMatch::OnPoint(ConnId conn, SessionPoint point, ISender& out) {
  ConnState& st = conns_[conn];
  const auto now = SteadyClock::now();
  for (size_t i = 0; i < o_.inject.size(); ++i) {
    const InjectSpec& in = o_.inject[i];
    if (in.point != point) continue;
    if (in.delayMs > 0)
      st.pending.emplace_back(now + std::chrono::milliseconds(in.delayMs), i);
    else
      SendOne(conn, i, out);
  }
}

void ScriptedMatch::Tick(SteadyClock::time_point now, ISender& out) {
  for (auto& kv : conns_) {
    ConnState& st = kv.second;
    // in inject order among the due ones (the vector keeps insertion order)
    for (auto it = st.pending.begin(); it != st.pending.end();) {
      if (now < it->first) {
        ++it;
        continue;
      }
      const size_t index = it->second;
      it = st.pending.erase(it);
      SendOne(kv.first, index, out);
    }
  }
}

void ScriptedMatch::SendOne(ConnId conn, size_t index, ISender& out) {
  const InjectSpec& in = o_.inject[index];
  const std::string where = std::string("injected at ") + SessionPointName(in.point) +
                            (in.delayMs ? "+" + std::to_string(in.delayMs) : std::string());
  out.Send(conn, in.msg, where);
  ++conns_[conn].injected;
}

void ScriptedMatch::OnDisconnected(ConnId conn) { conns_.erase(conn); }

std::string ScriptedMatch::StateNote(ConnId conn) {
  if (o_.inject.empty()) return std::string();
  return "injected " + std::to_string(Injected(conn)) + "/" + std::to_string(o_.inject.size());
}

int ScriptedMatch::Injected(ConnId conn) const {
  auto it = conns_.find(conn);
  return it == conns_.end() ? 0 : it->second.injected;
}

// ---- D3-core: shaper select -----------------------------------------------------------------------------------------

std::vector<OtherPlayer> ScriptedMatch::OtherPlayers() {
  std::vector<OtherPlayer> out;
  for (size_t i = 0; i < o_.allies.size(); ++i) {
    OtherPlayer p;
    p.gameplayIndex = o_.allies[i].gameplayIndex;
    p.team = o_.allies[i].team;
    p.onlineId = i + 1;
    out.push_back(p);
  }
  return out;
}

bool ScriptedMatch::ShaperSelectSetup(ShaperSelectConfig* config) {
  config->timers = o_.shaperTimers;
  config->seed = o_.setup.seed;
  config->humanDefault = o_.shapers;
  if (config->humanDefault.list.empty()) {
    ShaperListEntry e;
    e.noun = kNounAlpha;
    e.flags = kShaperOwned;
    config->humanDefault.list.push_back(e);
  }
  for (const AllySpec& a : o_.allies) {
    ShaperSelectPlayer p;
    p.gameplayIndex = a.gameplayIndex;
    p.team = a.team;
    p.scripted = true;
    p.plan = a.plan;
    config->players.push_back(p);
  }
  return true;
}

namespace {

std::vector<std::string> Split(const std::string& s, char sep) {
  std::vector<std::string> out;
  size_t start = 0;
  for (;;) {
    const size_t at = s.find(sep, start);
    out.push_back(s.substr(start, at == std::string::npos ? std::string::npos : at - start));
    if (at == std::string::npos) return out;
    start = at + 1;
  }
}

bool ParseU32Text(const std::string& s, uint32_t* out) {
  if (s.empty() || s[0] == '-') return false;
  char* end = nullptr;
  const unsigned long v = std::strtoul(s.c_str(), &end, 0);
  if (end == s.c_str() || *end) return false;
  *out = static_cast<uint32_t>(v);
  return true;
}

}  // namespace

bool ParseNoun(const std::string& text, uint32_t* out) {
  if (text.empty()) return false;
  if (text == "-1" || text == "random") {
    *out = kNounRandom;
    return true;
  }
  if (text[0] >= '0' && text[0] <= '9') return ParseU32Text(text, out);
  *out = AssetIdOf(text.find('.') == std::string::npos ? text + ".Noun" : text);
  return true;
}

bool ParseShaperList(const std::string& text, std::vector<ShaperListEntry>* out, std::string* error) {
  std::vector<ShaperListEntry> list;
  for (const std::string& item : Split(text, ',')) {
    const std::vector<std::string> f = Split(item, ':');
    ShaperListEntry e;
    uint32_t flags = 0;
    if (f.size() < 2 || f.size() > 3 || !ParseNoun(f[0], &e.noun) || e.noun == kNounRandom ||
        !ParseU32Text(f[1], &flags) || flags > 0xFF || (f.size() == 3 && !ParseU32Text(f[2], &e.offer))) {
      if (error) *error = "bad --shaper-list entry '" + item + "' (expected <noun>:<flags>[:<offer>])";
      return false;
    }
    e.flags = static_cast<uint8_t>(flags);
    list.push_back(e);
  }
  *out = list;
  return true;
}

bool ParseSkins(const std::string& text, std::map<uint32_t, std::vector<uint32_t>>* out, std::string* error) {
  for (const std::string& item : Split(text, ',')) {
    const std::vector<std::string> f = Split(item, ':');
    uint32_t shaper = 0;
    std::vector<uint32_t> skins;
    bool ok = f.size() >= 2 && ParseNoun(f[0], &shaper) && shaper != kNounRandom;
    for (size_t i = 1; ok && i < f.size(); ++i) {
      uint32_t skin = 0;
      ok = ParseNoun(f[i], &skin) && skin != kNounRandom;
      skins.push_back(skin);
    }
    if (!ok) {
      if (error) *error = "bad --skins entry '" + item + "' (expected <shaper>:<skin>[:<skin>...])";
      return false;
    }
    std::vector<uint32_t>& to = (*out)[shaper];
    to.insert(to.end(), skins.begin(), skins.end());
  }
  return true;
}

bool ParseShaperTimers(const std::string& text, ShaperSelectTimers* out, std::string* error) {
  const std::vector<std::string> f = Split(text, ',');
  ShaperSelectTimers t;
  if (f.size() != 4 || !ParseU32Text(f[0], &t.pickMs) || !ParseU32Text(f[1], &t.rolePerkMs) ||
      !ParseU32Text(f[2], &t.skinMs) || !ParseU32Text(f[3], &t.revealMs)) {
    if (error) *error = "bad --shaper-timers '" + text + "' (expected <pick ms>,<role/perk ms>,<skin ms>,<reveal ms>)";
    return false;
  }
  *out = t;
  return true;
}

bool ParseAlly(const std::string& text, AllySpec* out, std::string* error) {
  const std::vector<std::string> f = Split(text, ':');
  AllySpec a;
  uint32_t index = 0, team = 0, pick = 0, lock = 0, role = 0;
  bool ok = (f.size() == 3 || f.size() == 6) && ParseU32Text(f[0], &index) && index <= 9 &&
            ParseU32Text(f[1], &team) && (team == kTeamBlue || team == kTeamRed) && ParseNoun(f[2], &a.plan.noun);
  if (ok && f.size() == 6)
    ok = ParseU32Text(f[3], &pick) && ParseU32Text(f[4], &lock) && ParseU32Text(f[5], &role) && role < kNumRoles &&
         pick <= 3600000 && lock <= 3600000;
  if (!ok) {
    if (error)
      *error = "bad --ally '" + text +
               "' (expected <index 0-9>:<team 1|2>:<noun>[:<pick ms>:<lock ms>:<role 0-3>])";
    return false;
  }
  a.gameplayIndex = static_cast<uint8_t>(index);
  a.team = static_cast<uint8_t>(team);
  if (f.size() == 6) {
    a.plan.pickMs = static_cast<int>(pick);
    a.plan.lockMs = static_cast<int>(lock);
    a.plan.role = static_cast<uint8_t>(role);
  }
  *out = a;
  return true;
}

}  // namespace gms
