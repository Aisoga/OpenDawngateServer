#include "control/json.h"

#include <cmath>
#include <cstdio>
#include <cstdlib>

namespace dggms::control {

Json Json::Bool(bool v) {
  Json j;
  j.type_ = Type::Bool;
  j.bool_ = v;
  return j;
}

Json Json::Int(int64_t v) {
  Json j;
  j.type_ = Type::Int;
  j.int_ = v;
  return j;
}

Json Json::Double(double v) {
  Json j;
  j.type_ = Type::Double;
  j.double_ = v;
  return j;
}

Json Json::Str(std::string v) {
  Json j;
  j.type_ = Type::String;
  j.str_ = std::move(v);
  return j;
}

Json Json::Array() {
  Json j;
  j.type_ = Type::Array;
  return j;
}

Json Json::Object() {
  Json j;
  j.type_ = Type::Object;
  return j;
}

const Json* Json::Find(std::string_view key) const {
  if (type_ != Type::Object) return nullptr;
  for (const Member& m : members_) {
    if (m.first == key) return &m.second;
  }
  return nullptr;
}

Json& Json::Set(std::string key, Json v) {
  type_ = Type::Object;
  for (Member& m : members_) {
    if (m.first == key) {
      m.second = std::move(v);
      return *this;
    }
  }
  members_.emplace_back(std::move(key), std::move(v));
  return *this;
}

Json& Json::Push(Json v) {
  type_ = Type::Array;
  items_.push_back(std::move(v));
  return *this;
}

// ---------------------------------------------------------------------------------------- parser

namespace {

class Parser {
 public:
  explicit Parser(std::string_view s) : s_(s) {}

  bool Run(Json& out, std::string* err) {
    SkipSpace();
    if (!Value(out, 0)) return Fail(err);
    SkipSpace();
    if (pos_ != s_.size()) {
      error_ = "trailing characters";
      return Fail(err);
    }
    return true;
  }

 private:
  bool Fail(std::string* err) {
    if (err) *err = "json: " + error_ + " at byte " + std::to_string(pos_);
    return false;
  }

  bool Error(const char* msg) {
    error_ = msg;
    return false;
  }

  void SkipSpace() {
    while (pos_ < s_.size() && (s_[pos_] == ' ' || s_[pos_] == '\t' || s_[pos_] == '\n' || s_[pos_] == '\r')) pos_++;
  }

  bool Literal(const char* word) {
    std::string_view w(word);
    if (s_.substr(pos_, w.size()) != w) return Error("bad literal");
    pos_ += w.size();
    return true;
  }

  bool Value(Json& out, int depth) {
    if (depth > kMaxDepth) return Error("nesting too deep");
    if (pos_ >= s_.size()) return Error("unexpected end");
    char c = s_[pos_];
    switch (c) {
      case 'n':
        out = Json();
        return Literal("null");
      case 't':
        out = Json::Bool(true);
        return Literal("true");
      case 'f':
        out = Json::Bool(false);
        return Literal("false");
      case '"': {
        std::string str;
        if (!String(str)) return false;
        out = Json::Str(std::move(str));
        return true;
      }
      case '[':
        return ArrayValue(out, depth);
      case '{':
        return ObjectValue(out, depth);
      default:
        if (c == '-' || (c >= '0' && c <= '9')) return Number(out);
        return Error("unexpected character");
    }
  }

  bool ArrayValue(Json& out, int depth) {
    out = Json::Array();
    pos_++;  // '['
    SkipSpace();
    if (pos_ < s_.size() && s_[pos_] == ']') {
      pos_++;
      return true;
    }
    for (;;) {
      Json item;
      SkipSpace();
      if (!Value(item, depth + 1)) return false;
      out.Push(std::move(item));
      SkipSpace();
      if (pos_ >= s_.size()) return Error("unexpected end in array");
      if (s_[pos_] == ',') {
        pos_++;
        continue;
      }
      if (s_[pos_] == ']') {
        pos_++;
        return true;
      }
      return Error("expected ',' or ']'");
    }
  }

  bool ObjectValue(Json& out, int depth) {
    out = Json::Object();
    pos_++;  // '{'
    SkipSpace();
    if (pos_ < s_.size() && s_[pos_] == '}') {
      pos_++;
      return true;
    }
    for (;;) {
      SkipSpace();
      if (pos_ >= s_.size() || s_[pos_] != '"') return Error("expected a member name");
      std::string key;
      if (!String(key)) return false;
      SkipSpace();
      if (pos_ >= s_.size() || s_[pos_] != ':') return Error("expected ':'");
      pos_++;
      SkipSpace();
      Json v;
      if (!Value(v, depth + 1)) return false;
      out.Set(std::move(key), std::move(v));  // a repeated name keeps the last value, as Go does
      SkipSpace();
      if (pos_ >= s_.size()) return Error("unexpected end in object");
      if (s_[pos_] == ',') {
        pos_++;
        continue;
      }
      if (s_[pos_] == '}') {
        pos_++;
        return true;
      }
      return Error("expected ',' or '}'");
    }
  }

  bool Number(Json& out) {
    size_t start = pos_;
    if (s_[pos_] == '-') pos_++;
    if (pos_ >= s_.size() || s_[pos_] < '0' || s_[pos_] > '9') return Error("bad number");
    if (s_[pos_] == '0' && pos_ + 1 < s_.size() && s_[pos_ + 1] >= '0' && s_[pos_ + 1] <= '9') {
      return Error("bad number (leading zero)");
    }
    while (pos_ < s_.size() && s_[pos_] >= '0' && s_[pos_] <= '9') pos_++;
    bool integral = true;
    if (pos_ < s_.size() && s_[pos_] == '.') {
      integral = false;
      pos_++;
      if (pos_ >= s_.size() || s_[pos_] < '0' || s_[pos_] > '9') return Error("bad number");
      while (pos_ < s_.size() && s_[pos_] >= '0' && s_[pos_] <= '9') pos_++;
    }
    if (pos_ < s_.size() && (s_[pos_] == 'e' || s_[pos_] == 'E')) {
      integral = false;
      pos_++;
      if (pos_ < s_.size() && (s_[pos_] == '+' || s_[pos_] == '-')) pos_++;
      if (pos_ >= s_.size() || s_[pos_] < '0' || s_[pos_] > '9') return Error("bad number");
      while (pos_ < s_.size() && s_[pos_] >= '0' && s_[pos_] <= '9') pos_++;
    }
    std::string text(s_.substr(start, pos_ - start));
    if (integral) {
      // exact 64-bit integer, with overflow detection
      bool neg = text[0] == '-';
      uint64_t limit = neg ? (uint64_t(1) << 63) : (uint64_t(1) << 63) - 1;
      uint64_t v = 0;
      for (size_t i = neg ? 1 : 0; i < text.size(); i++) {
        uint64_t d = uint64_t(text[i] - '0');
        if (v > (limit - d) / 10) {
          integral = false;  // out of range: keep it as a double
          break;
        }
        v = v * 10 + d;
      }
      if (integral) {
        out = Json::Int(neg ? static_cast<int64_t>(0 - v) : static_cast<int64_t>(v));
        return true;
      }
    }
    out = Json::Double(std::strtod(text.c_str(), nullptr));
    return true;
  }

  static int Hex(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
  }

  bool Hex4(uint32_t& v) {
    if (pos_ + 4 > s_.size()) return Error("bad escape");
    v = 0;
    for (int i = 0; i < 4; i++) {
      int h = Hex(s_[pos_ + i]);
      if (h < 0) return Error("bad escape");
      v = v * 16 + uint32_t(h);
    }
    pos_ += 4;
    return true;
  }

  static void AppendUtf8(std::string& out, uint32_t cp) {
    if (cp < 0x80) {
      out += char(cp);
    } else if (cp < 0x800) {
      out += char(0xC0 | (cp >> 6));
      out += char(0x80 | (cp & 0x3F));
    } else if (cp < 0x10000) {
      out += char(0xE0 | (cp >> 12));
      out += char(0x80 | ((cp >> 6) & 0x3F));
      out += char(0x80 | (cp & 0x3F));
    } else {
      out += char(0xF0 | (cp >> 18));
      out += char(0x80 | ((cp >> 12) & 0x3F));
      out += char(0x80 | ((cp >> 6) & 0x3F));
      out += char(0x80 | (cp & 0x3F));
    }
  }

  bool String(std::string& out) {
    pos_++;  // opening quote
    for (;;) {
      if (pos_ >= s_.size()) return Error("unterminated string");
      unsigned char c = static_cast<unsigned char>(s_[pos_]);
      if (c == '"') {
        pos_++;
        return true;
      }
      if (c < 0x20) return Error("control character in string");
      if (c != '\\') {
        out += char(c);
        pos_++;
        continue;
      }
      pos_++;
      if (pos_ >= s_.size()) return Error("unterminated string");
      char e = s_[pos_++];
      switch (e) {
        case '"': out += '"'; break;
        case '\\': out += '\\'; break;
        case '/': out += '/'; break;
        case 'b': out += '\b'; break;
        case 'f': out += '\f'; break;
        case 'n': out += '\n'; break;
        case 'r': out += '\r'; break;
        case 't': out += '\t'; break;
        case 'u': {
          uint32_t cp;
          if (!Hex4(cp)) return false;
          if (cp >= 0xD800 && cp < 0xDC00) {  // high surrogate: a low one must follow
            uint32_t lo = 0;
            if (pos_ + 1 < s_.size() && s_[pos_] == '\\' && s_[pos_ + 1] == 'u') {
              pos_ += 2;
              if (!Hex4(lo)) return false;
            }
            if (lo >= 0xDC00 && lo < 0xE000) {
              cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
            } else {
              cp = 0xFFFD;  // lone surrogate, as Go decodes it
            }
          } else if (cp >= 0xDC00 && cp < 0xE000) {
            cp = 0xFFFD;
          }
          AppendUtf8(out, cp);
          break;
        }
        default:
          return Error("bad escape");
      }
    }
  }

  std::string_view s_;
  size_t pos_ = 0;
  std::string error_;
};

void Write(std::string& out, const Json& v) {
  switch (v.type()) {
    case Json::Type::Null:
      out += "null";
      return;
    case Json::Type::Bool:
      out += v.AsBool() ? "true" : "false";
      return;
    case Json::Type::Int:
      out += std::to_string(v.AsInt());
      return;
    case Json::Type::Double: {
      double d = v.AsDouble();
      if (!std::isfinite(d)) {
        out += "null";  // JSON has no NaN or infinity
        return;
      }
      char buf[40];
      std::snprintf(buf, sizeof buf, "%.17g", d);
      out += buf;
      return;
    }
    case Json::Type::String:
      AppendJsonString(out, v.AsString());
      return;
    case Json::Type::Array: {
      out += '[';
      bool first = true;
      for (const Json& item : v.Items()) {
        if (!first) out += ',';
        first = false;
        Write(out, item);
      }
      out += ']';
      return;
    }
    case Json::Type::Object: {
      out += '{';
      bool first = true;
      for (const Json::Member& m : v.Members()) {
        if (!first) out += ',';
        first = false;
        AppendJsonString(out, m.first);
        out += ':';
        Write(out, m.second);
      }
      out += '}';
      return;
    }
  }
}

}  // namespace

bool ParseJson(std::string_view text, Json& out, std::string* err) { return Parser(text).Run(out, err); }

std::string WriteJson(const Json& v) {
  std::string out;
  Write(out, v);
  return out;
}

void AppendJsonString(std::string& out, std::string_view s) {
  static const char kHex[] = "0123456789abcdef";
  out += '"';
  for (size_t i = 0; i < s.size(); i++) {
    unsigned char c = static_cast<unsigned char>(s[i]);
    switch (c) {
      case '"': out += "\\\""; continue;
      case '\\': out += "\\\\"; continue;
      case '\b': out += "\\b"; continue;
      case '\f': out += "\\f"; continue;
      case '\n': out += "\\n"; continue;
      case '\r': out += "\\r"; continue;
      case '\t': out += "\\t"; continue;
      default: break;
    }
    if (c < 0x20) {
      out += "\\u00";
      out += kHex[c >> 4];
      out += kHex[c & 0xF];
      continue;
    }
    // U+2028 and U+2029 (E2 80 A8, E2 80 A9): Go escapes them, so we do too
    if (c == 0xE2 && i + 2 < s.size() && static_cast<unsigned char>(s[i + 1]) == 0x80 &&
        (static_cast<unsigned char>(s[i + 2]) == 0xA8 || static_cast<unsigned char>(s[i + 2]) == 0xA9)) {
      out += (static_cast<unsigned char>(s[i + 2]) == 0xA8) ? "\\u2028" : "\\u2029";
      i += 2;
      continue;
    }
    out += char(c);
  }
  out += '"';
}

}  // namespace dggms::control
