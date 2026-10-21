package discordhook

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Message is the body of a webhook post or edit (Discord's Execute Webhook / Edit Webhook Message JSON). The sender
// always adds allowed_mentions itself (see payload): no message can ping @everyone, @here or a user, and a role only
// when it is listed in PingRoles.
type Message struct {
	Content string  `json:"content"`
	Embeds  []Embed `json:"embeds,omitempty"`
	// PingRoles are the role ids this message may ping (its content holds RoleMention(id)); the queue message's
	// configured role, nothing else. Not sent as such: it becomes allowed_mentions.roles.
	PingRoles []string `json:"-"`
}

// Embed is one embed of a message (the fields the server uses).
type Embed struct {
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"` // 0xRRGGBB
	Fields      []Field `json:"fields,omitempty"`
	Footer      *Footer `json:"footer,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"` // RFC 3339 (Embed.At sets it)
}

// Field is an embed field; Inline ones sit side by side (up to three a row).
type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// Footer is an embed's footer line.
type Footer struct {
	Text string `json:"text"`
}

// At sets the embed's timestamp (shown in each reader's time zone).
func (e *Embed) At(t time.Time) { e.Timestamp = t.UTC().Format(time.RFC3339) }

// allowedMentions is Discord's allowed_mentions object: parse [] turns off every mention parsed from the text
// (@everyone, @here, users, roles); roles then lists the only roles that may be pinged.
type allowedMentions struct {
	Parse []string `json:"parse"`
	Roles []string `json:"roles,omitempty"`
}

// payload is what goes over the wire: the message within Discord's limits, with allowed_mentions.
type payload struct {
	Message
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// Discord's limits (characters): content 2000; per embed title 256, description 4096, 25 fields, field name 256,
// value 1024, footer 2048, all embeds' text together 6000; 10 embeds.
const (
	maxContent     = 2000
	maxTitle       = 256
	maxDescription = 4096
	maxFields      = 25
	maxFieldName   = 256
	maxFieldValue  = 1024
	maxFooter      = 2048
	maxEmbeds      = 10
)

// Encode returns the message's JSON body as the sender sends it: cut to Discord's limits (a cut text ends in "…"),
// with allowed_mentions {"parse":[]} and the role ids of PingRoles that are ids (digits) as allowed roles.
func Encode(m Message) ([]byte, error) {
	p := payload{Message: m, AllowedMentions: allowedMentions{Parse: []string{}}}
	for _, r := range m.PingRoles {
		if isDigits(r) && len(r) <= 20 {
			p.AllowedMentions.Roles = append(p.AllowedMentions.Roles, r)
		}
	}
	p.Content = cut(p.Content, maxContent)
	if len(p.Embeds) > maxEmbeds {
		p.Embeds = p.Embeds[:maxEmbeds]
	}
	embeds := make([]Embed, len(p.Embeds))
	for i, e := range p.Embeds {
		e.Title, e.Description = cut(e.Title, maxTitle), cut(e.Description, maxDescription)
		if len(e.Fields) > maxFields {
			e.Fields = e.Fields[:maxFields]
		}
		fs := make([]Field, len(e.Fields))
		for j, f := range e.Fields {
			fs[j] = Field{Name: orBlank(cut(f.Name, maxFieldName)), Value: orBlank(cut(f.Value, maxFieldValue)), Inline: f.Inline}
		}
		e.Fields = fs
		if e.Footer != nil {
			e.Footer = &Footer{Text: cut(e.Footer.Text, maxFooter)}
		}
		embeds[i] = e
	}
	p.Embeds = embeds
	if len(p.Embeds) == 0 {
		p.Embeds = nil
	}
	return json.Marshal(p)
}

// orBlank gives an empty field name or value a zero-width space (Discord refuses empty ones).
func orBlank(s string) string {
	if s == "" {
		return "​"
	}
	return s
}

// cut shortens s to at most n characters (runes), the last one "…".
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Timestamp is Discord's timestamp markup <t:unix:style>, which each reader's Discord shows in their own time zone
// and language; style "R" is relative ("in 2 minutes", counting by itself), "t" a short time, "f" date and time.
func Timestamp(t time.Time, style string) string {
	return fmt.Sprintf("<t:%d:%s>", t.Unix(), style)
}

// RoleMention is the markup that pings a role (allowed only when the message lists the id in PingRoles).
func RoleMention(roleID string) string { return "<@&" + roleID + ">" }

// markdown are the characters Discord's markdown gives a meaning; Escape puts a backslash before each.
const markdown = "\\*_~`|>#-[]()<:@!=+.{}"

// maxName is the most characters of a name Escape keeps (the server's display names have 16).
const maxName = 32

// Escape makes text from a player (a display name) safe in a message: control characters and line breaks become
// blanks, every markdown character is escaped (so no bold, links, headings, lists, spoilers, emoji or timestamps),
// and an @ or < can't start a mention (a player named "@everyone" shows as text and pings nobody, which
// allowed_mentions also guarantees). Longer than 32 characters: cut with "…". Empty: "?".
func Escape(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		if unicode.Is(unicode.Cf, r) { // zero-width and direction marks
			return -1
		}
		return r
	}, name))
	if name == "" {
		return "?"
	}
	name = cut(name, maxName)
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(markdown, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		if r == '@' { // belt and braces: no "@everyone" even where a backslash isn't honoured
			b.WriteRune('​')
		}
	}
	return b.String()
}
