// ScriptedMatch (gms/script, G1): an IMatch from command-line options, for protocol work and tests without the game
// (ADR-2 decision 5). It accepts every HelloReq, gives each player the same slot (gameplay index, team, shaper noun),
// answers GamePrepareForStart's values, and sends raw messages at session points: D2e's level objects and shaper
// (gms_reflect.py level --hp --point loaded) arrive this way until the host builds them from its simulator (D2f).
// D3-core: shaper select's content for every human (--shaper-list, --skins), its timers (--shaper-timers) and
// GMS-scripted allies (--ally: players without a connection, with a pick plan), for script B.
#pragma once

#include <map>
#include <string>
#include <utility>
#include <vector>

#include "core/session/match.h"

namespace gms {

// ---- D3-core: shaper select options (gms_script's syntax) ------------------------------------------------------------
// A GMS-scripted player: --ally <index>:<team>:<noun>[:<pick ms>:<lock ms>:<role>] (defaults 2000, 4000, 0).
struct AllySpec {
  uint8_t gameplayIndex = 1;
  uint8_t team = kTeamBlue;
  ShaperPickPlan plan;
};
// A Noun: an asset name ("artificer.Noun"; without a '.' ".Noun" is added), a number ("0xEF044AAB"), or "-1"/"random"
// (kNounRandom).
bool ParseNoun(const std::string& text, uint32_t* out);
// --shaper-list <noun>:<flags>[:<offer>],...  (flags 1 owned, 2 free rotation, 0x10 hidden, 0x20 available)
bool ParseShaperList(const std::string& text, std::vector<ShaperListEntry>* out, std::string* error);
// --skins <shaper>:<skin>[:<skin>...][,<shaper>:...]  (a shaper's owned skins; C8 0B lists the shaper first)
bool ParseSkins(const std::string& text, std::map<uint32_t, std::vector<uint32_t>>* out, std::string* error);
// --shaper-timers <pick ms>,<role/perk ms>,<skin ms>,<reveal ms>
bool ParseShaperTimers(const std::string& text, ShaperSelectTimers* out, std::string* error);
bool ParseAlly(const std::string& text, AllySpec* out, std::string* error);

// One message to send at a session point, optionally <delayMs> after it. Text form "<point>[+<ms>]:<hex>", the hex
// starting with the wire byte (>= 0x81); points: hello, prepare, clock5, pregameflow, load, loaded, done, entergame.
struct InjectSpec {
  SessionPoint point = SessionPoint::kHello;
  int delayMs = 0;
  Bytes msg;
};
bool ParseInject(const std::string& text, InjectSpec* out, std::string* error);
// One spec per line; '#' starts a comment; blank lines, a UTF-8 BOM and CR are ignored. Appends to `out`; returns
// the number read, or -1 with `error` set (a missing file or a bad line).
int ReadInjectFile(const std::string& path, std::vector<InjectSpec>* out, std::string* error);
std::string DescribeInject(const InjectSpec& s, size_t maxBytes = 32);  // "<point>[+ms]:<hex>"

struct ScriptOptions {
  GameSetup setup;
  PlayerSlot slot;
  std::vector<InjectSpec> inject;  // in order; messages of one point go out in this order
  // D3-core (with SessionOptions::shaperSelect): every human's content, the timers, the allies
  ShaperContent shapers;            // --shaper-list (empty: alpha.Noun owned), --skins
  ShaperSelectTimers shaperTimers;  // --shaper-timers (default: ShaperSelectTuning's normal values)
  std::vector<AllySpec> allies;     // --ally; the n-th has online id n ("BOT n")
};

class ScriptedMatch final : public IMatch {
 public:
  explicit ScriptedMatch(const ScriptOptions& o) : o_(o) {}

  GameSetup Setup() override { return o_.setup; }
  void OnConnected(ConnId conn, const std::string& addr) override;
  bool OnHelloReq(ConnId conn, const HelloReq& req, PlayerSlot* slot) override;
  void OnPoint(ConnId conn, SessionPoint point, ISender& out) override;
  void OnDisconnected(ConnId conn) override;
  void Tick(SteadyClock::time_point now, ISender& out) override;
  std::string StateNote(ConnId conn) override;  // "injected n/m" when there is something to inject
  // D3-core: the allies (players without a connection) and shaper select's setup from the options
  std::vector<OtherPlayer> OtherPlayers() override;
  bool ShaperSelectSetup(ShaperSelectConfig* config) override;

  int Injected(ConnId conn) const;

 private:
  struct ConnState {
    std::vector<std::pair<SteadyClock::time_point, size_t>> pending;  // delayed entries (due, index), in order
    int injected = 0;
  };
  void SendOne(ConnId conn, size_t index, ISender& out);

  const ScriptOptions o_;
  std::map<ConnId, ConnState> conns_;
};

}  // namespace gms
