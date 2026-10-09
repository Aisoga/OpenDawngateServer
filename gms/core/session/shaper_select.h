// ShaperSelect (gms/core/session, D3-core): the match-wide shaper select the GMS drives between EnterPreGameFlow and
// C0 01, as docs/GMS_PROTOCOL.md *Shaper select (D2c, D3-static)* specifies it (the GMS's handling and validation of
// every client message, the phases and timers, the order rules, 1 to 10 players) and *Script B (final)* plays it.
// Owned by GmsSession (one per match); engine-free and transport-free: it sends through ISender and takes the server
// clock (the ClockSync clock, ms) as an argument, so tests drive it with synthetic times.
//
// The sequence (GMS_PROTOCOL.md *Phases, timers and the GMS's sequence*):
//   C8 02 from a client   -> its list (C8 02), its team's current records (C8 03), its perk pages (C8 09, if any), the
//                            current phase (C8 01). The first C8 02 starts phase 1.
//   phase 1 (pick)        ends when every record is locked, or at the timer: the GMS then locks every unlocked record
//                            (0 = the first selectable entry no teammate holds, -1 = a random one) and sends C8 0B to
//                            each such human (no C8 05: it didn't ask).
//   phase 2 (role, perks) ends when every record has its role locked, or at the timer. Scripted players (and humans
//                            without a connection) lock their role once every connected human has locked its own.
//   phase 3 (skin)        ends when every connected human has sent C8 0D, or at the timer: a player's skin is then its
//                            last C8 0C/0D, else its saved skin (if owned), else the shaper's default skin.
//   phase 4 (reveal)      at once: C8 01 ... 04 to all, then each client gets the other team's records. Revealed();
//                            the picks are final. Done() after RevealTime.
// A timer-ended phase's successor starts at the old end (not at the tick that noticed it), so the ends are exact.
// Records go to the player's team in phases 1-3 and to everyone in phase 4; a shaper is unique within a team only.
// Every C8 04 is answered (00 when refused: the client's UI waits for it); C8 05 is answered 01 only (00 asserts in the
// client); the current C8 01 is repeated every second to every client in shaper select. Messages in the wrong phase or
// for a locked part are ignored and logged.
// Several humans (G3a): a client has a record only for the players its simulator had when it opened shaper select
// (HelloPlayer and PlayerJoined before B6); a record or role toggle for any other index asserts in the client
// ("Using invalid player index"). So with SetKnown a client gets records and toggles only of the indices it knows.
// Players are kept by gameplay index: a human whose connection ended keeps its record and gets the automatic picks,
// and the same index on a new connection (the session's RejoinMap keeps a returning user's index) continues it.
// Observers (B15b; docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*): a spectator's connection has no record
// and picks nothing. Its client creates the players' records like any client, sends C8 01 but no C8 02
// (ShaperSelect_Init 0x00524620 skips GameClient_SendShaperSelectReady for an observer) and draws both teams in every
// phase (ShaperSelect_RefreshPlayerSlots 0x0051F050: no blind pick for an observer), so from its C8 01 it gets every
// record it knows, every change of any team, the phases and their repeats, and the role toggles.
#pragma once

#include <cstdint>
#include <map>
#include <random>
#include <set>
#include <string>
#include <vector>

#include "core/net/endpoint.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"

namespace gms {

class ISender;  // core/session/match.h

// What a GMS-scripted player (or bot) does in shaper select. Times count from the start of phase 1.
struct ShaperPickPlan {
  uint32_t noun = 0;   // its pick (C8 03 to its team at pickMs); 0 = none (the lock takes the first free entry),
                       // kNounRandom = random at the lock
  int pickMs = 2000;
  int lockMs = 4000;   // locked at max(pickMs, lockMs)
  uint8_t role = 0;    // set and locked once every connected human has locked its role, or at the phase-2 timer
};

struct PerkPage {
  uint32_t id = 0;       // the loadout manager's page id (page +8): the store's SUMM.PID
  uint32_t version = 0;  // its Blaze VERS: mLoadoutVersion
  // D3-perks: the stats the page gives (createGame's perkPages, summed by dgserver as LoadoutManager_ComputePageStats
  // does): the pre-game loadout C0 02's entries and the server player's loadout
  std::vector<LoadoutAttribute> attributes;
  std::vector<uint32_t> modifiers;  // the passive modifiers' ability ids (SPID: FNV-1 of the lower-cased Lua name)
};

// What one player may choose from (createGame's content later, D3-live step 4; gms_script's options now).
struct ShaperContent {
  std::vector<ShaperListEntry> list;                // C8 02; a human's must not be empty (the client asserts n > 0)
  std::map<uint32_t, std::vector<uint32_t>> skins;   // default-skin Noun -> its owned skins; C8 0B = the default
                                                     // skin first, then these (an absent shaper: the default only)
  std::vector<PerkPage> pages;                       // C8 09 (B7c); none = no C8 09
  std::vector<uint32_t> wards, announcers;           // owned vanity assets; an empty list accepts any (C8 0E)
  std::map<uint32_t, uint32_t> savedSkins;           // USET SKIN_0x<catalogID>, keyed by the default-skin Noun

  // The C8 0B list for a shaper: the shaper (its default skin) first, then its other owned skins.
  std::vector<uint32_t> SkinsFor(uint32_t shaper) const;
};

struct ShaperSelectPlayer {
  uint8_t gameplayIndex = 0;
  uint8_t team = kTeamBlue;
  bool scripted = false;  // no connection: picks by `plan`; a human otherwise (bound by index at HelloReq)
  ShaperPickPlan plan;
  ShaperContent content;  // a scripted player with an empty list may take any shaper no teammate holds
};

// IMatch::ShaperSelectSetup fills this in.
struct ShaperSelectConfig {
  ShaperSelectTimers timers;
  uint32_t seed = 1;            // the Random picks' generator (std::mt19937)
  ShaperContent humanDefault;   // for a human whose gameplay index has no entry in `players`
  std::vector<ShaperSelectPlayer> players;
};

// A player's result (phase 4), in gameplay-index order.
struct ShaperPick {
  uint8_t gameplayIndex = 0;
  uint8_t team = 0;
  bool scripted = false;
  uint32_t shaper = 0;  // the shaper (its default skin Noun)
  uint32_t skin = 0;    // the chosen skin Noun: mpNounDef
  uint8_t role = 0;
  uint32_t page = 0, pageVersion = 0;  // mLoadoutID / mLoadoutVersion (0 = no perk page)
  uint32_t ward = 0, announcer = 0;    // 0 = the default
  uint32_t status = 0;  // the player's mStatus, 0x100 included: filled in by GmsSession before IMatch::OnShaperPicks
  // D3-perks: the page's stats (its PerkPage's attributes and modifiers; empty without a page)
  std::vector<LoadoutAttribute> loadout;
  std::vector<uint32_t> modifiers;
};
// D3-perks: the pre-game loadout C0 02 of a pick with a perk page (its stats; GMS_PROTOCOL.md *Pre-game `02`*), sent
// to every client that has the player, after the reveal's update and before C0 01. Empty Bytes without a page.
Bytes RevealLoadout(const ShaperPick& p);
// D3-perks: the page's server-side effect (GMS_PROTOCOL.md *Perk pages in shaper select (D3-perks)*). The shared
// attribute code adds the server player's loadout entries of op Add to its creature's attributes
// (AttributeData_ComputePlayerAttribute 0x008D8180 asks LabsPlayer_GetLoadoutAttribute 0x00914890 for op 1 only), so the
// host writes every entry into the player's loadout as the client's C0 02 handler does; nothing reads the other ops, so
// those the host adds to the creature as attribute modifiers with their op [assumed: the stripped server applied them].
constexpr uint8_t kLoadoutOpReadByPlayer = 1;  // nAttributeOp.kAdd
std::vector<LoadoutAttribute> LoadoutCreatureModifiers(const std::vector<LoadoutAttribute>& attributes);
// The reveal's LabsPlayerGlobalUpdate members for a pick: mStatus, mRole, mpNounDef, plus mLoadoutID/Version with a
// perk page (GMS_PROTOCOL.md *Into the game*, step 1), and their values.
std::vector<int> RevealMembers(const ShaperPick& p);
PlayerGlobalValues RevealValues(const ShaperPick& p);

class ShaperSelect {
 public:
  explicit ShaperSelect(const ShaperSelectConfig& config);

  // A human's connection (HelloReq): its gameplay index and team. Its content is its entry in the config, or the
  // config's humanDefault (a fallback list of alpha.Noun when that is empty: the client asserts on an empty list).
  // An index that has a player already is that player on a new connection (a rejoin): its record stays. G3a: a player
  // new to shaper select after phase 1 gets what the timers gave the others (its shaper locked, from phase 3 its role,
  // at the reveal its skin), without a send: its own client gets the records at its C8 02.
  void AddHuman(ConnId conn, uint8_t gameplayIndex, uint8_t team);
  // The connection ended: the player keeps its record and gets the automatic picks (an observer is forgotten).
  void RemoveHuman(ConnId conn);
  // B15b: a spectator's connection (gameplay index 10-14): an observer, with no record. Nothing goes to it before its
  // C8 01 (OnObserverReady); from then on every record it knows (SetKnown) in every phase, every record change and
  // role toggle of any team, the phases and their one-second repeats, until Close.
  void AddObserver(ConnId conn);
  void OnObserverReady(ConnId conn, int64_t serverMs, ISender& out);
  bool HasObserver(ConnId conn) const;
  // G3a: the gameplay indices the connection's client has records for: its players when it opened shaper select (the
  // session's snapshot at B6). Records (C8 03) and role toggles (C8 06) of other indices don't go to it. Without a
  // call (or after AddHuman) the connection counts as knowing every index, as before G3a.
  void SetKnown(ConnId conn, const std::vector<uint8_t>& indices);
  // The client's C8 02 (its records exist): its list, its team's records, its perk pages, its skins if its shaper is
  // locked already (a late C8 02 or a returning player, Q2), the current phase. Nothing goes to a connection before it.
  void OnReady(ConnId conn, int64_t serverMs, ISender& out);
  // Any other client ShaperSelectMsgs body (after the wire byte).
  void OnMessage(ConnId conn, const uint8_t* body, size_t n, int64_t serverMs, ISender& out);
  // Timers, the scripted players' plans and the one-second repeat.
  void Tick(int64_t serverMs, ISender& out);
  // C0 01 went to this connection: its shaper select is gone; nothing more is sent to it.
  void Close(ConnId conn);

  int Phase() const { return phase_; }  // 0 until the first C8 02, then 1-4
  uint64_t PhaseEnd() const { return phaseEnd_; }
  bool Revealed() const { return phase_ == 4; }
  bool Done(int64_t serverMs) const { return phase_ == 4 && static_cast<uint64_t>(serverMs) >= phaseEnd_; }
  std::vector<ShaperPick> Picks() const;
  const ShaperRecord* Record(uint8_t gameplayIndex) const;  // nullptr for an unknown index
  bool HasHuman(ConnId conn) const;
  // "shaper select phase N", and from phase 4 "; picks 0=<shaper>/<skin> r<role> ..." (the session's state line);
  // B15b: "; N observer(s)" when there are any.
  std::string Describe() const;

 private:
  // B15b: a spectator's connection.
  struct Observer {
    bool ready = false;  // its C8 01 arrived
    bool open = true;    // not closed by C0 01
    bool knowsAll = true;
    std::set<uint8_t> knows;
    bool Receives() const { return ready && open; }
    bool Knows(uint8_t index) const { return knowsAll || knows.count(index) != 0; }
  };
  std::map<ConnId, Observer> observers_;

  struct Player {
    uint8_t team = kTeamBlue;
    bool scripted = false;
    ShaperPickPlan plan;
    ShaperContent content;
    ShaperRecord rec;
    ConnId conn = 0;
    bool bound = false;  // a human with a connection
    bool ready = false;  // its C8 02 arrived
    bool open = true;    // not closed by C0 01
    bool gone = false;   // its connection ended
    bool planPicked = false;
    bool skinChosen = false;
    bool skinFinal = false;
    uint8_t preferredRoles = 0;  // C8 06 toggles, one bit per role
    uint32_t pageVersion = 0;
    uint32_t ward = 0, announcer = 0;
    bool knowsAll = true;        // G3a: no SetKnown yet: every index counts as known
    std::set<uint8_t> knows;     // G3a: the indices its client has records for (SetKnown)
    bool Acting() const { return !scripted && bound && !gone; }  // a connected human: acts for itself
    bool Receives() const { return Acting() && ready && open; }
    bool Knows(uint8_t index) const { return knowsAll || knows.count(index) != 0; }
  };

  Player* ByConn(ConnId conn);
  bool HeldByTeammate(uint8_t index, uint32_t noun) const;
  bool Selectable(uint8_t index, uint32_t noun) const;
  std::vector<uint32_t> FreeEntries(uint8_t index) const;
  uint32_t Resolve(uint8_t index);
  void Pick(uint8_t index, uint32_t noun, ISender& out);
  void Lock(uint8_t index, bool answer, ISender& out);
  void SendRecord(uint8_t index, ISender& out);
  void SendPhase(ConnId conn, bool quiet, ISender& out);
  void Enter(int phase, int64_t at, int64_t now, ISender& out);
  void Reveal(int64_t at, int64_t now, ISender& out);
  void LockFollowerRoles(ISender& out);
  void Advance(int64_t now, ISender& out);
  void Refuse(uint8_t index, const char* what, const char* why) const;

  ShaperSelectTimers timers_;
  ShaperContent humanDefault_;
  std::map<uint8_t, Player> players_;  // by gameplay index (the order of every pass)
  std::mt19937 rng_;
  int phase_ = 0;
  uint64_t phaseStart_ = 0, phaseEnd_ = 0;
  int64_t nextRepeat_ = 0;
};

}  // namespace gms
