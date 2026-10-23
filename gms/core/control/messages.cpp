#include "control/messages.h"

#include "control/json.h"

namespace dggms::control {

namespace {

// ------------------------------------------------------------------------------------- encoding

Json IntMap(const std::map<std::string, int64_t>& m) {  // std::map: keys sorted, as Go sorts map keys
  Json o = Json::Object();
  for (const auto& kv : m) o.Set(kv.first, Json::Int(kv.second));
  return o;
}

Json StrList(const std::vector<std::string>& v) {
  Json a = Json::Array();
  for (const std::string& s : v) a.Push(Json::Str(s));
  return a;
}

// gmshost.Rewards' field order
Json RewardsJson(const Rewards& r) {
  Json o = Json::Object();
  o.Set("lootBag", Json::Int(r.lootBag)).Set("destiny", Json::Int(r.destiny)).Set("cc", Json::Int(r.cc));
  Json items = Json::Array();
  for (const RewardItem& it : r.items) {
    Json e = Json::Object();
    e.Set("catalogId", Json::Int(it.catalogId)).Set("duplicate", Json::Bool(it.duplicate));
    e.Set("alternate", Json::Int(it.alternate));
    items.Push(std::move(e));
  }
  o.Set("items", std::move(items)).Set("ftue", Json::Bool(r.ftue)).Set("baseRoll", Json::Int(r.baseRoll));
  o.Set("winPoints", Json::Int(r.winPoints)).Set("winAltPoints", Json::Int(r.winAltPoints));
  o.Set("firstWinPoints", Json::Int(r.firstWinPoints));
  if (r.karma) {  // D8c: only where dgserver counted the karma (Go: omitempty)
    Json k = Json::Object();
    k.Set("points", Json::Int(r.karma->points)).Set("party", Json::Int(r.karma->party));
    k.Set("team", Json::Int(r.karma->team)).Set("enemy", Json::Int(r.karma->enemy));
    o.Set("karma", std::move(k));
  }
  return o;
}

// A roster as createGame, updateRoster and startGame carry it (gmshost.RosterEntry's fields in order; the content
// fields, party and spectator only when set: Go's omitempty).
Json RosterJson(const std::vector<RosterEntry>& entries) {
  Json roster = Json::Array();
  for (const RosterEntry& r : entries) {
    Json e = Json::Object();
    e.Set("user", Json::Int(r.user)).Set("name", Json::Str(r.name)).Set("index", Json::Int(r.index));
    e.Set("team", Json::Int(r.team)).Set("noun", Json::Str(r.noun)).Set("bot", Json::Bool(r.bot));
    // D3-live step 4: gmshost.RosterEntry's content fields, each only when not empty (Go: omitempty)
    if (!r.shapers.empty()) {
      Json list = Json::Array();
      for (const RosterEntry::Shaper& s : r.shapers) {
        Json se = Json::Object();
        se.Set("noun", Json::Str(s.noun)).Set("flags", Json::Int(s.flags));
        if (s.offer != 0) se.Set("offer", Json::Int(s.offer));
        list.Push(std::move(se));
      }
      e.Set("shapers", std::move(list));
    }
    if (!r.skins.empty()) {
      Json skins = Json::Object();
      for (const auto& kv : r.skins) skins.Set(kv.first, StrList(kv.second));
      e.Set("skins", std::move(skins));
    }
    if (!r.savedSkins.empty()) {
      Json saved = Json::Object();
      for (const auto& kv : r.savedSkins) saved.Set(kv.first, Json::Str(kv.second));
      e.Set("savedSkins", std::move(saved));
    }
    if (!r.wards.empty()) e.Set("wards", StrList(r.wards));
    if (!r.announcers.empty()) e.Set("announcers", StrList(r.announcers));
    if (!r.perkPages.empty()) {  // D3-perks
      Json pages = Json::Array();
      for (const RosterEntry::PerkPage& p : r.perkPages) {
        Json pe = Json::Object();
        pe.Set("id", Json::Int(p.id)).Set("version", Json::Int(p.version));
        if (!p.attributes.empty()) {
          Json attrs = Json::Array();
          for (const RosterEntry::PerkAttribute& a : p.attributes) {
            Json ae = Json::Object();
            ae.Set("attribute", Json::Int(a.attribute)).Set("op", Json::Int(a.op)).Set("milli", Json::Int(a.milli));
            attrs.Push(std::move(ae));
          }
          pe.Set("attributes", std::move(attrs));
        }
        if (!p.modifiers.empty()) pe.Set("modifiers", StrList(p.modifiers));
        pages.Push(std::move(pe));
      }
      e.Set("perkPages", std::move(pages));
    }
    if (r.party != 0) e.Set("party", Json::Int(r.party));  // D8b: the PvP party (Go: omitempty)
    if (r.spectator) e.Set("spectator", Json::Bool(true));  // B15: an observer (Go: omitempty)
    roster.Push(std::move(e));
  }
  return roster;
}

struct Encoder {
  Json& o;
  void operator()(const Hello& m) {
    o.Set("match", Json::Int(m.match)).Set("token", Json::Str(m.token)).Set("pid", Json::Int(m.pid));
    o.Set("build", Json::Str(m.build));
  }
  void operator()(const CreateGame& m) {
    o.Set("gameId", Json::Int(m.gameId)).Set("level", Json::Str(m.level)).Set("mode", Json::Str(m.mode));
    o.Set("seed", Json::Int(m.seed));
    o.Set("roster", RosterJson(m.roster));
    if (!m.timers.empty()) o.Set("timers", IntMap(m.timers));
    if (m.descriptors != 0) o.Set("descriptors", Json::Int(m.descriptors));
    if (m.shaperSelect) o.Set("shaperSelect", Json::Bool(true));
    if (m.fillTeams != 0) o.Set("fillTeams", Json::Int(m.fillTeams));  // B10d
    if (m.levelFlags != 0) o.Set("levelFlags", Json::Int(m.levelFlags));  // HOLIDAY1
  }
  void operator()(const GameReady& m) { o.Set("port", Json::Int(m.port)); }
  void operator()(const AddPlayer& m) { o.Set("user", Json::Int(m.user)).Set("index", Json::Int(m.index)); }
  void operator()(const DisconnectPlayer& m) {
    o.Set("user", Json::Int(m.user)).Set("reason", Json::Str(m.reason));
  }
  void operator()(const GMSUpdate& m) {
    o.Set("phase", Json::Str(m.phase)).Set("elapsedMs", Json::Int(m.elapsedMs));
    Json players = Json::Array();
    for (int64_t p : m.players) players.Push(Json::Int(p));
    o.Set("players", std::move(players));
  }
  void operator()(const PlayerFinish& m) {
    o.Set("user", Json::Int(m.user));
    if (!m.stats.empty()) o.Set("stats", IntMap(m.stats));
    if (m.rewards) o.Set("rewards", RewardsJson(*m.rewards));
  }
  void operator()(const PlayerRewards& m) {
    o.Set("user", Json::Int(m.user)).Set("rewards", RewardsJson(m.rewards));
  }
  void operator()(const PlayerKarma& m) {
    o.Set("user", Json::Int(m.user)).Set("party", Json::Int(m.party)).Set("team", Json::Int(m.team));
    o.Set("enemy", Json::Int(m.enemy));
  }
  void operator()(const FinishGame& m) {
    o.Set("winnerTeam", Json::Int(m.winnerTeam)).Set("durationMs", Json::Int(m.durationMs));
    o.Set("reason", Json::Str(m.reason));
  }
  void operator()(const GMSError& m) {
    o.Set("code", Json::Int(m.code)).Set("text", Json::Str(m.text)).Set("fatal", Json::Bool(m.fatal));
  }
  void operator()(const LogoutGMS&) {}
  void operator()(const RemovePlayer& m) {
    o.Set("user", Json::Int(m.user)).Set("reason", Json::Str(m.reason));
  }
  void operator()(const AbortGame& m) { o.Set("reason", Json::Str(m.reason)); }
  void operator()(const UpdateRoster& m) { o.Set("roster", RosterJson(m.roster)); }  // B15
  void operator()(const StartGame& m) { o.Set("roster", RosterJson(m.roster)); }     // B15
};

struct Namer {
  const char* operator()(const Hello&) { return "hello"; }
  const char* operator()(const CreateGame&) { return "createGame"; }
  const char* operator()(const GameReady&) { return "gameReady"; }
  const char* operator()(const AddPlayer&) { return "addPlayer"; }
  const char* operator()(const DisconnectPlayer&) { return "disconnectPlayer"; }
  const char* operator()(const GMSUpdate&) { return "reportGMSUpdate"; }
  const char* operator()(const PlayerFinish&) { return "playerFinish"; }
  const char* operator()(const FinishGame&) { return "finishGame"; }
  const char* operator()(const GMSError&) { return "reportGMSError"; }
  const char* operator()(const LogoutGMS&) { return "logoutGMS"; }
  const char* operator()(const RemovePlayer&) { return "removePlayer"; }
  const char* operator()(const AbortGame&) { return "abortGame"; }
  const char* operator()(const PlayerRewards&) { return "playerRewards"; }
  const char* operator()(const PlayerKarma&) { return "playerKarma"; }
  const char* operator()(const UpdateRoster&) { return "updateRoster"; }
  const char* operator()(const StartGame&) { return "startGame"; }
};

// ------------------------------------------------------------------------------------- decoding

// Fields reads typed members of one object; the first type error wins.
struct Fields {
  const Json& o;
  std::string err;

  bool Bad(std::string_view key, const char* want) {
    if (err.empty()) err = "field \"" + std::string(key) + "\": want " + want;
    return false;
  }

  // Each getter leaves out unchanged for a missing member or null.
  bool Int(std::string_view key, int64_t& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsInt()) return Bad(key, "an integer");
    out = v->AsInt();
    return true;
  }
  bool Str(std::string_view key, std::string& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsString()) return Bad(key, "a string");
    out = v->AsString();
    return true;
  }
  bool Bool(std::string_view key, bool& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (v->type() != Json::Type::Bool) return Bad(key, "a boolean");
    out = v->AsBool();
    return true;
  }
  bool IntList(std::string_view key, std::vector<int64_t>& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsArray()) return Bad(key, "an array");
    out.clear();
    for (const Json& item : v->Items()) {
      if (!item.IsInt()) return Bad(key, "an array of integers");
      out.push_back(item.AsInt());
    }
    return true;
  }
  bool IntMap(std::string_view key, std::map<std::string, int64_t>& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsObject()) return Bad(key, "an object");
    out.clear();
    for (const Json::Member& m : v->Members()) {
      if (!m.second.IsInt()) return Bad(key, "an object of integers");
      out[m.first] = m.second.AsInt();
    }
    return true;
  }
  // D3-live step 4: the roster's content fields
  static bool ReadStrList(const Json& v, std::vector<std::string>& out) {
    if (!v.IsArray()) return false;
    out.clear();
    for (const Json& item : v.Items()) {
      if (!item.IsString()) return false;
      out.push_back(item.AsString());
    }
    return true;
  }
  bool StrList(std::string_view key, std::vector<std::string>& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    return ReadStrList(*v, out) || Bad(key, "an array of strings");
  }
  bool StrMap(std::string_view key, std::map<std::string, std::string>& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsObject()) return Bad(key, "an object");
    out.clear();
    for (const Json::Member& m : v->Members()) {
      if (!m.second.IsString()) return Bad(key, "an object of strings");
      out[m.first] = m.second.AsString();
    }
    return true;
  }
  bool StrListMap(std::string_view key, std::map<std::string, std::vector<std::string>>& out) {
    const Json* v = o.Find(key);
    if (!v || v->IsNull()) return true;
    if (!v->IsObject()) return Bad(key, "an object");
    out.clear();
    for (const Json::Member& m : v->Members())
      if (!ReadStrList(m.second, out[m.first])) return Bad(key, "an object of string arrays");
    return true;
  }
};

// The rewards object under `key` (B7c, D8-live). `present` says whether it was there (not missing, not null).
bool DecodeRewards(Fields& f, std::string_view key, Rewards& out, bool* present) {
  *present = false;
  const Json* v = f.o.Find(key);
  if (!v || v->IsNull()) return true;
  if (!v->IsObject()) return f.Bad(key, "an object");
  Fields r{*v, {}};
  r.Int("lootBag", out.lootBag);
  r.Int("destiny", out.destiny);
  r.Int("cc", out.cc);
  const Json* items = v->Find("items");
  if (items && !items->IsNull()) {
    if (!items->IsArray()) {
      r.Bad("items", "an array");
    } else {
      for (const Json& item : items->Items()) {
        if (!item.IsObject()) {
          r.Bad("items", "an array of objects");
          break;
        }
        Fields e{item, {}};
        RewardItem it;
        e.Int("catalogId", it.catalogId);
        e.Bool("duplicate", it.duplicate);
        e.Int("alternate", it.alternate);
        if (!e.err.empty()) {
          if (r.err.empty()) r.err = "items: " + e.err;
          break;
        }
        out.items.push_back(it);
      }
    }
  }
  r.Bool("ftue", out.ftue);
  r.Int("baseRoll", out.baseRoll);
  r.Int("winPoints", out.winPoints);
  r.Int("winAltPoints", out.winAltPoints);
  r.Int("firstWinPoints", out.firstWinPoints);
  const Json* karma = v->Find("karma");  // D8c: optional; null = none
  if (karma && !karma->IsNull()) {
    if (!karma->IsObject()) {
      r.Bad("karma", "an object");
    } else {
      Fields k{*karma, {}};
      Karma kv;
      k.Int("points", kv.points);
      k.Int("party", kv.party);
      k.Int("team", kv.team);
      k.Int("enemy", kv.enemy);
      if (!k.err.empty()) {
        if (r.err.empty()) r.err = "karma: " + k.err;
      } else {
        out.karma = kv;
      }
    }
  }
  if (!r.err.empty()) {
    f.err = std::string(key) + ": " + r.err;
    return false;
  }
  *present = true;
  return true;
}

bool DecodeRoster(Fields& f, std::vector<RosterEntry>& out) {
  const Json* v = f.o.Find("roster");
  if (!v || v->IsNull()) return true;
  if (!v->IsArray()) return f.Bad("roster", "an array");
  for (const Json& item : v->Items()) {
    if (!item.IsObject()) return f.Bad("roster", "an array of objects");
    Fields e{item, {}};
    RosterEntry r;
    e.Int("user", r.user);
    e.Str("name", r.name);
    e.Int("index", r.index);
    e.Int("team", r.team);
    e.Str("noun", r.noun);
    e.Bool("bot", r.bot);
    const Json* shapers = item.Find("shapers");  // D3-live step 4
    if (e.err.empty() && shapers && !shapers->IsNull()) {
      if (!shapers->IsArray()) {
        e.Bad("shapers", "an array");
      } else {
        for (const Json& s : shapers->Items()) {
          if (!s.IsObject()) {
            e.Bad("shapers", "an array of objects");
            break;
          }
          Fields se{s, {}};
          RosterEntry::Shaper sh;
          se.Str("noun", sh.noun);
          se.Int("flags", sh.flags);
          se.Int("offer", sh.offer);
          if (!se.err.empty()) {
            e.err = "shapers: " + se.err;
            break;
          }
          r.shapers.push_back(std::move(sh));
        }
      }
    }
    if (e.err.empty()) e.StrListMap("skins", r.skins);
    if (e.err.empty()) e.StrMap("savedSkins", r.savedSkins);
    if (e.err.empty()) e.StrList("wards", r.wards);
    if (e.err.empty()) e.StrList("announcers", r.announcers);
    const Json* pages = item.Find("perkPages");  // D3-perks
    if (e.err.empty() && pages && !pages->IsNull()) {
      if (!pages->IsArray()) {
        e.Bad("perkPages", "an array");
      } else {
        for (const Json& p : pages->Items()) {
          if (!p.IsObject()) {
            e.Bad("perkPages", "an array of objects");
            break;
          }
          Fields pf{p, {}};
          RosterEntry::PerkPage pg;
          pf.Int("id", pg.id);
          pf.Int("version", pg.version);
          const Json* attrs = p.Find("attributes");
          if (pf.err.empty() && attrs && !attrs->IsNull()) {
            if (!attrs->IsArray()) {
              pf.Bad("attributes", "an array");
            } else {
              for (const Json& a : attrs->Items()) {
                if (!a.IsObject()) {
                  pf.Bad("attributes", "an array of objects");
                  break;
                }
                Fields af{a, {}};
                RosterEntry::PerkAttribute at;
                af.Int("attribute", at.attribute);
                af.Int("op", at.op);
                af.Int("milli", at.milli);
                if (!af.err.empty()) {
                  pf.err = "attributes: " + af.err;
                  break;
                }
                pg.attributes.push_back(at);
              }
            }
          }
          if (pf.err.empty()) pf.StrList("modifiers", pg.modifiers);
          if (!pf.err.empty()) {
            e.err = "perkPages: " + pf.err;
            break;
          }
          r.perkPages.push_back(std::move(pg));
        }
      }
    }
    if (e.err.empty()) e.Int("party", r.party);  // D8b
    if (e.err.empty()) e.Bool("spectator", r.spectator);  // B15
    if (!e.err.empty()) {
      f.err = "roster: " + e.err;
      return false;
    }
    out.push_back(std::move(r));
  }
  return true;
}

}  // namespace

const char* TypeName(const Message& m) { return std::visit(Namer{}, m); }

const char* ErrorCodeName(int64_t code) {
  switch (code) {
    case kErrGmsEndpoint: return "GMS endpoint";
    case kErrTickStalled: return "tick stalled";
    case kErrStepFailing: return "step failing";
    case kErrStepException: return "step exception";
    default: return "error";
  }
}

std::string Encode(const Message& m) {
  Json o = Json::Object();
  o.Set("t", Json::Str(TypeName(m)));
  std::visit(Encoder{o}, m);
  std::string line = WriteJson(o);
  line += '\n';
  return line;
}

bool Decode(std::string_view line, Message& out, std::string* err) {
  while (!line.empty() && (line.back() == '\n' || line.back() == '\r')) line.remove_suffix(1);
  auto fail = [&](const std::string& msg) {
    if (err) *err = msg;
    return false;
  };
  Json o;
  std::string jerr;
  if (!ParseJson(line, o, &jerr)) return fail("bad line: " + jerr);
  if (!o.IsObject()) return fail("bad line: not an object");
  const Json* t = o.Find("t");
  if (!t) return fail("line without \"t\"");
  if (!t->IsString()) return fail("bad line: \"t\" is not a string");
  const std::string& type = t->AsString();
  Fields f{o, {}};
  if (type == "hello") {
    Hello m;
    f.Int("match", m.match); f.Str("token", m.token); f.Int("pid", m.pid); f.Str("build", m.build);
    out = std::move(m);
  } else if (type == "createGame") {
    CreateGame m;
    f.Int("gameId", m.gameId); f.Str("level", m.level); f.Str("mode", m.mode); f.Int("seed", m.seed);
    if (f.err.empty()) DecodeRoster(f, m.roster);
    if (f.err.empty()) f.IntMap("timers", m.timers);
    if (f.err.empty()) f.Int("descriptors", m.descriptors);
    if (f.err.empty()) f.Bool("shaperSelect", m.shaperSelect);
    if (f.err.empty()) f.Int("fillTeams", m.fillTeams);  // B10d
    if (f.err.empty()) f.Int("levelFlags", m.levelFlags);  // HOLIDAY1
    out = std::move(m);
  } else if (type == "gameReady") {
    GameReady m;
    f.Int("port", m.port);
    out = m;
  } else if (type == "addPlayer") {
    AddPlayer m;
    f.Int("user", m.user); f.Int("index", m.index);
    out = m;
  } else if (type == "disconnectPlayer") {
    DisconnectPlayer m;
    f.Int("user", m.user); f.Str("reason", m.reason);
    out = std::move(m);
  } else if (type == "reportGMSUpdate") {
    GMSUpdate m;
    f.Str("phase", m.phase); f.Int("elapsedMs", m.elapsedMs); f.IntList("players", m.players);
    out = std::move(m);
  } else if (type == "playerFinish") {
    PlayerFinish m;
    f.Int("user", m.user); f.IntMap("stats", m.stats);
    Rewards r;
    bool present = false;
    if (f.err.empty() && DecodeRewards(f, "rewards", r, &present) && present) m.rewards = std::move(r);
    out = std::move(m);
  } else if (type == "finishGame") {
    FinishGame m;
    f.Int("winnerTeam", m.winnerTeam); f.Int("durationMs", m.durationMs); f.Str("reason", m.reason);
    out = std::move(m);
  } else if (type == "reportGMSError") {
    GMSError m;
    f.Int("code", m.code); f.Str("text", m.text); f.Bool("fatal", m.fatal);
    out = std::move(m);
  } else if (type == "logoutGMS") {
    out = LogoutGMS{};
  } else if (type == "removePlayer") {
    RemovePlayer m;
    f.Int("user", m.user); f.Str("reason", m.reason);
    out = std::move(m);
  } else if (type == "abortGame") {
    AbortGame m;
    f.Str("reason", m.reason);
    out = std::move(m);
  } else if (type == "playerRewards") {
    PlayerRewards m;
    f.Int("user", m.user);
    bool present = false;
    if (f.err.empty()) DecodeRewards(f, "rewards", m.rewards, &present);
    out = std::move(m);
  } else if (type == "updateRoster") {  // B15
    UpdateRoster m;
    DecodeRoster(f, m.roster);
    out = std::move(m);
  } else if (type == "startGame") {  // B15
    StartGame m;
    DecodeRoster(f, m.roster);
    out = std::move(m);
  } else if (type == "playerKarma") {
    PlayerKarma m;
    f.Int("user", m.user); f.Int("party", m.party); f.Int("team", m.team); f.Int("enemy", m.enemy);
    out = m;
  } else {
    return fail("unknown message type \"" + type + "\"");
  }
  if (!f.err.empty()) return fail("bad " + type + ": " + f.err);
  return true;
}

}  // namespace dggms::control
