// The match recorder's file, engine-free; see matchrec.h.
#include "core/matchrec/matchrec.h"

#include <algorithm>
#include <cmath>
#include <utility>

namespace gms::matchrec {

namespace {

// A float with `decimals` digits; -0 and non-finite values written as 0.
std::string Num(float v, int decimals) {
  if (!std::isfinite(v)) v = 0.f;
  char b[48];
  std::snprintf(b, sizeof b, "%.*f", decimals, static_cast<double>(v));
  if (b[0] == '-') {
    bool zero = true;
    for (const char* p = b + 1; *p; ++p)
      if (*p != '0' && *p != '.') zero = false;
    if (zero) return std::string(b + 1);
  }
  return b;
}

std::string Hex(std::uint32_t v) {
  char b[16];
  std::snprintf(b, sizeof b, "%08X", v);
  return b;
}

std::string Int(std::int64_t v) { return std::to_string(v); }

std::string Join(const std::vector<std::string>& f) {
  std::string s;
  for (std::size_t i = 0; i < f.size(); ++i) {
    if (i) s += '\t';
    s += f[i];
  }
  s += '\n';
  return s;
}

}  // namespace

// ---- sinks ---------------------------------------------------------------------------------------------------------

std::unique_ptr<FileSink> FileSink::Create(const std::string& path) {
  std::FILE* f = std::fopen(path.c_str(), "wb");
  if (!f) return nullptr;
  std::setvbuf(f, nullptr, _IONBF, 0);  // the writer buffers; one fwrite per buffer
  return std::unique_ptr<FileSink>(new FileSink(f));
}

FileSink::~FileSink() {
  if (f_) std::fclose(f_);
}

bool FileSink::Write(const char* p, std::size_t n) { return f_ && std::fwrite(p, 1, n, f_) == n; }

bool FileSink::Flush() { return f_ && std::fflush(f_) == 0; }

bool StringSink::Write(const char* p, std::size_t n) {
  if (failAfter >= 0 && writes >= failAfter) return false;
  ++writes;
  data.append(p, n);
  return true;
}

// ---- encoding ------------------------------------------------------------------------------------------------------

std::string Field(const std::string& s) {
  if (s.empty()) return "-";
  std::string out = s;
  for (char& c : out)
    if (static_cast<unsigned char>(c) < 0x20 || c == 0x7F) c = ' ';
  return out;
}

std::string EncodeHeader() { return Join({kMagic, Int(kFormatVersion)}); }

std::string EncodeMeta(const std::string& key, const std::string& value) { return Join({"H", Field(key), Field(value)}); }

std::string EncodeSeat(const SeatInfo& s) {
  return Join({"P", Int(s.seat), Int(s.team), s.bot ? "bot" : "human", Int(s.user), Hex(s.noun), Field(s.shaper),
               Int(s.role), Field(s.botClass)});
}

std::string EncodeSnapshot(std::int64_t ms, const Snapshot& s) {
  const char state[2] = {s.state ? s.state : static_cast<char>(kNoCreature), 0};
  return Join({"S", Int(ms), Int(s.seat), state, Num(s.pos[0], 1), Num(s.pos[1], 1), Num(s.pos[2], 1), Num(s.hp, 0),
               Num(s.hpMax, 0), Num(s.res, 0), Num(s.resMax, 0), Int(s.level), Num(s.gold, 0), Num(s.totalGold, 0),
               Hex(s.target), Int(s.seenBy & 6u)});
}

std::string EncodeCommand(std::int64_t ms, const Command& c) {
  return Join({"C", Int(ms), Int(c.seat), c.bot ? "bot" : "human", Field(c.source), Field(c.kind), Int(c.seq),
               Int(c.slot), Hex(c.ability), Hex(c.target), c.hasPos ? Num(c.pos[0], 1) : "-",
               c.hasPos ? Num(c.pos[1], 1) : "-", c.hasPos ? Num(c.pos[2], 1) : "-", Int(c.value)});
}

std::string EncodeEvent(std::int64_t ms, const std::string& kind, const std::vector<std::string>& fields) {
  std::vector<std::string> f = {"E", Int(ms), Field(kind)};
  for (const std::string& x : fields) f.push_back(Field(x));
  return Join(f);
}

std::string EncodeFinal(std::int64_t ms, const Final& f) {
  return Join({"F", Int(ms), Int(f.seat), Int(f.kills), Int(f.deaths), Int(f.assists), Int(f.creeps), Int(f.level),
               Int(f.totalGold)});
}

std::string EncodeEnd(std::int64_t ms, const char* reason) { return Join({"X", Int(ms), Field(reason ? reason : "")}); }

const char* ActionKind(std::uint8_t type) {
  switch (type) {
    case kCmdMove: return "move";
    case kCmdStopMoving: return "stopmoving";
    case kCmdAbility: return "ability";
    case kCmdInteract: return "interact";
    case kCmdDance: return "dance";
    case kCmdTaunt: return "taunt";
    case kCmdPing: return "ping";
    case kCmdStop: return "stop";
    case kCmdAttackMove: return "attackmove";
    case kCmdVoiceLine: return "voiceline";
    case kCmdHold: return "hold";
    default: return "unknown";
  }
}

Command FromAction(const gms::ActionCommand& a, int seat, bool bot, const std::string& source) {
  Command c;
  c.seat = seat;
  c.bot = bot;
  c.source = source;
  c.kind = a.type == kCmdAbility && a.guid == kGuidReturnToBase ? "recall" : ActionKind(a.type);
  c.seq = a.seq;
  c.target = a.target;
  switch (a.type) {
    case kCmdMove:
    case kCmdAttackMove:
    case kCmdPing:
      c.hasPos = true;
      c.pos[0] = a.pos.x;
      c.pos[1] = a.pos.y;
      c.pos[2] = a.pos.z;
      c.value = a.type == kCmdPing ? static_cast<std::int64_t>(a.value) : 0;
      break;
    case kCmdAbility:
      c.slot = a.slot;
      c.ability = a.guid;
      c.hasPos = true;
      c.pos[0] = a.aim.x;
      c.pos[1] = a.aim.y;
      c.pos[2] = a.aim.z;
      c.value = (a.walkUp ? 1 : 0) | (a.reactivation ? 2 : 0) | (a.keepAttacking ? 4 : 0);
      break;
    case kCmdVoiceLine:
      c.value = a.value;
      break;
    default:
      break;
  }
  return c;
}

Command FromBotRequest(int seat, const char* where, std::uint32_t guid, int slot, std::uint32_t target,
                       const float* pos, int result) {
  Command c;
  c.seat = seat;
  c.bot = true;
  c.source = std::string("bot:") + (where && *where ? where : "direct");
  c.kind = guid == kGuidBotMoveToTarget ? "move" : guid == kGuidReturnToBase ? "recall" : slot == 0 ? "attack" : "ability";
  c.slot = slot;
  c.ability = guid;
  c.target = target;
  if (pos && (pos[0] != 0.f || pos[1] != 0.f || pos[2] != 0.f)) {
    c.hasPos = true;
    c.pos[0] = pos[0];
    c.pos[1] = pos[1];
    c.pos[2] = pos[2];
  }
  c.value = result;
  return c;
}

Command FromBotItemUse(int seat, const char* where, std::uint32_t item, int invSlot) {
  Command c;
  c.seat = seat;
  c.bot = true;
  c.source = std::string("bot:") + (where && *where ? where : "direct");
  c.kind = "item";
  c.slot = invSlot;
  c.ability = item;
  c.value = 3;  // ItemTransaction type 3, "use" (gms/core/protocol/commands.h kItemUse)
  return c;
}

std::string JoinSeats(const std::vector<int>& seats) {
  if (seats.empty()) return "-";
  std::string s;
  for (std::size_t i = 0; i < seats.size(); ++i) {
    if (i) s += ',';
    s += std::to_string(seats[i]);
  }
  return s;
}

// ---- the writer ----------------------------------------------------------------------------------------------------

Writer::~Writer() {
  if (sink_ && !st_.failed) Drain();
}

bool Writer::Open(std::unique_ptr<Sink> sink, std::size_t capBytes, std::size_t bufferBytes) {
  if (!sink) return false;
  sink_ = std::move(sink);
  cap_ = std::max(capBytes, kMinCapBytes);
  bufMax_ = std::max<std::size_t>(bufferBytes, 256);
  buf_.reserve(bufMax_ + 1024);
  st_ = WriterStats();
  return Line(0, EncodeHeader());
}

bool Writer::Drain() {
  if (buf_.empty()) return true;
  ++st_.writes;
  const bool ok = sink_->Write(buf_.data(), buf_.size());
  buf_.clear();
  if (!ok) st_.failed = true;
  return ok;
}

bool Writer::Line(std::int64_t ms, const std::string& line) {
  if (!Active()) {
    ++st_.dropped;
    return false;
  }
  if (st_.bytes + line.size() + kTrailerReserve > cap_) {
    // the cap: this line would pass it; end the file with the X line (the reserve holds it) and stop
    ++st_.dropped;
    const std::string x = EncodeEnd(ms, "cap");
    buf_ += x;
    st_.bytes += x.size();
    ++st_.lines;
    st_.capped = true;
    if (Drain()) sink_->Flush();
    return false;
  }
  buf_ += line;
  st_.bytes += line.size();
  ++st_.lines;
  if (buf_.size() >= bufMax_) return Drain();
  return true;
}

bool Writer::Flush() {
  if (!sink_ || st_.failed) return false;
  if (!Drain()) return false;
  if (!sink_->Flush()) {
    st_.failed = true;
    return false;
  }
  return true;
}

void Writer::Close(std::int64_t ms, const char* reason) {
  if (!sink_ || st_.closed) return;
  if (Active()) {
    const std::string x = EncodeEnd(ms, reason);
    buf_ += x;  // always fits: the cap keeps the reserve free
    st_.bytes += x.size();
    ++st_.lines;
  }
  if (!st_.failed) Flush();
  st_.closed = true;
}

// ---- the rate ------------------------------------------------------------------------------------------------------

Rate::Rate(double hz) {
  if (!(hz > 0.0)) {
    never_ = true;
    return;
  }
  hz = std::min(30.0, std::max(0.1, hz));
  periodMs_ = 1000.0 / hz;
}

bool Rate::Due(std::int64_t ms) {
  if (never_) return false;
  const double t = static_cast<double>(ms);
  if (next_ < 0.0 || t >= next_) {
    // on schedule: the next a period after the last due time; late: a period after now (no burst)
    next_ = (next_ < 0.0 || t >= next_ + periodMs_) ? t + periodMs_ : next_ + periodMs_;
    return true;
  }
  return false;
}

// ---- the file's name -----------------------------------------------------------------------------------------------

std::string FileName(const std::string& match, int year, int month, int day, int hour, int minute, int second) {
  std::string id;
  for (char c : match) {
    const bool ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_';
    id += ok ? c : '_';
  }
  if (id.empty()) id = "match";
  if (id.size() > 64) id.resize(64);
  char b[48];
  std::snprintf(b, sizeof b, "_%04d%02d%02d-%02d%02d%02d.txt", year, month, day, hour, minute, second);
  return "matchrec_" + id + b;
}

}  // namespace gms::matchrec
