// A small JSON value, parser and writer for the control channel (ADR-2 decision 6, roadmap C1).
//
// No third-party library: the host DLL links the static CRT and nothing else. Integers keep 64 bits
// (Blaze ids), objects keep their member order (the writer emits members in insertion order).
// The writer escapes exactly what Go's encoding/json does with SetEscapeHTML(false) ('"', '\\',
// control characters, U+2028, U+2029), so both sides write identical lines
// (docs/data/host_control_examples.jsonl). Strings are UTF-8 and passed through unchecked.
#pragma once

#include <cstdint>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace dggms::control {

class Json {
 public:
  enum class Type { Null, Bool, Int, Double, String, Array, Object };
  using Member = std::pair<std::string, Json>;

  Json() = default;
  static Json Bool(bool v);
  static Json Int(int64_t v);
  static Json Double(double v);
  static Json Str(std::string v);
  static Json Array();
  static Json Object();

  Type type() const { return type_; }
  bool IsNull() const { return type_ == Type::Null; }
  bool IsInt() const { return type_ == Type::Int; }
  bool IsString() const { return type_ == Type::String; }
  bool IsArray() const { return type_ == Type::Array; }
  bool IsObject() const { return type_ == Type::Object; }

  bool AsBool() const { return bool_; }
  int64_t AsInt() const { return int_; }
  double AsDouble() const { return type_ == Type::Int ? static_cast<double>(int_) : double_; }
  const std::string& AsString() const { return str_; }
  const std::vector<Json>& Items() const { return items_; }
  const std::vector<Member>& Members() const { return members_; }

  // Find returns the member named key, or nullptr (also for non-objects).
  const Json* Find(std::string_view key) const;
  // Set replaces the member named key or appends it; Push appends an array item. Both return *this.
  Json& Set(std::string key, Json v);
  Json& Push(Json v);

 private:
  Type type_ = Type::Null;
  bool bool_ = false;
  int64_t int_ = 0;
  double double_ = 0;
  std::string str_;
  std::vector<Json> items_;
  std::vector<Member> members_;
};

// MaxDepth is the deepest nesting ParseJson accepts.
constexpr int kMaxDepth = 32;

// ParseJson parses one JSON text (surrounding whitespace allowed). On failure it returns false and
// sets *err (if given) to a message with the byte offset.
bool ParseJson(std::string_view text, Json& out, std::string* err = nullptr);

// WriteJson returns v as compact JSON.
std::string WriteJson(const Json& v);

// AppendJsonString appends s as a JSON string literal.
void AppendJsonString(std::string& out, std::string_view s);

}  // namespace dggms::control
