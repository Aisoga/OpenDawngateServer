package discordhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
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
	// Files are uploaded with a post (multipart/form-data: EncodeBody); an embed shows one by its Image URL
	// "attachment://<name>" (AttachmentURL). Posts only: Edit drops them (an edit never uploads).
	Files []File `json:"-"`
}

// File is a file a post uploads (the result post's scoreboard picture).
type File struct {
	Name        string // the file name Discord shows and attachment:// names ("scoreboard.png")
	ContentType string // "image/png"; "" = application/octet-stream
	Data        []byte
}

// AttachmentURL is the URL by which an embed shows a file of its message.
func AttachmentURL(name string) string { return "attachment://" + name }

// Embed is one embed of a message (the fields the server uses).
type Embed struct {
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"` // 0xRRGGBB
	Fields      []Field `json:"fields,omitempty"`
	Image       *Image  `json:"image,omitempty"`
	Footer      *Footer `json:"footer,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"` // RFC 3339 (Embed.At sets it)
}

// Image is an embed's big picture: a URL, or AttachmentURL of a file the message uploads.
type Image struct {
	URL string `json:"url"`
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
	// Attachments name the uploaded files (a post with Files): id i is the part files[i].
	Attachments []attachment `json:"attachments,omitempty"`
}

// attachment is one entry of a payload's attachments.
type attachment struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
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
	for i, f := range m.Files {
		p.Attachments = append(p.Attachments, attachment{ID: i, Filename: f.Name})
	}
	return json.Marshal(p)
}

// EncodeBody is a request's body and its content type as the sender sends it: Encode's JSON
// ("application/json") for a message without files; with Files a multipart/form-data body whose part
// "payload_json" is Encode's JSON (its attachments naming the files) and whose parts "files[0]", "files[1]", ...
// are the files (Discord's upload for webhooks).
func EncodeBody(m Message) (body []byte, contentType string, err error) {
	js, err := Encode(m)
	if err != nil || len(m.Files) == 0 {
		return js, "application/json", err
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="payload_json"`)
	h.Set("Content-Type", "application/json")
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(js); err != nil {
		return nil, "", err
	}
	for i, f := range m.Files {
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[%d]"; filename="%s"`, i, quoteName(f.Name)))
		h.Set("Content-Type", ct)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(f.Data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return b.Bytes(), w.FormDataContentType(), nil
}

// quoteName makes a file name safe in a quoted header parameter (as mime/multipart's CreateFormFile does).
var quoteName = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "", "\n", "").Replace

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
