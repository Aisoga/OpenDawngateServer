package gamecontent

// The passives' texts for the item browser: a passive's description at its rank (rankedDescription) with its
// "~[precisionN.]<script>.<token>~" tokens filled in the way the client's tooltip code does, the lines that only
// make sense in a match left out, and the client's markup (<font color>, <br>) turned into coloured runs of text.
//
// The client (LabsTooltip.cpp): a token names a script and a token the script's TranslateToken handler turns into a
// number; "precisionN" asks for N decimals. Without a precision the number is rounded down (floor), then written by
// 0x004CD5D0: a number between 0 and 1 is "0" without decimals, from 0.95 up "1"; any other is _fcvt to N decimals
// with the trailing zeros (and a bare decimal point) dropped (0x00834330). The handler runs here without a
// match: a value read from the match comes back unknown (the "Current …" lines), a creature's attribute times a
// factor as "40% of Power".

import (
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/luac"
)

var (
	tokenRe = regexp.MustCompile(`~\s*(?:precision(\d)\.)?((?:[A-Za-z0-9_]+\.)*)([A-Za-z0-9_]+)\s*~`)
	brRe    = regexp.MustCompile(`(?i)<br\s*/?>`)
	markRe  = regexp.MustCompile(`(?i)<(/?)([a-z]+)([^>]*)>`)
	colorRe = regexp.MustCompile(`(?i)color\s*=\s*"?(#[0-9a-f]{6})"?`)
	hexRe   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// unknownMark stands for a token whose value only a match knows; a line holding one is left out.
const unknownMark = "\x00?\x00"

// formatNumber writes a token's number as the client does (see the file comment).
func formatNumber(v float64, precision int) string {
	if precision == 0 {
		v = math.Floor(v)
	}
	if v < 1 {
		if v > 0 && precision == 0 {
			return "0"
		}
		if v >= float64(float32(0.95)) {
			return "1"
		}
	}
	s := strconv.FormatFloat(v, 'f', precision, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

// attrNames are the attributes' words in a scaled value ("40% of Power").
var attrNames = map[string]string{"Power": "Power", "Mastery": "Mastery", "MaxHealth": "maximum Health",
	"MaxResourcePoints": "maximum Vim", "PhysicalDamageReduction": "Armor", "MagicalDamageReduction": "Magic Resist",
	"Haste": "Haste", "HealthRegen": "Health Regen"}

func formatScaled(s luac.Scaled, precision int) string {
	if s.Attr == luac.LevelAttr { // a value by the shaper's level: its value at level 1 and the step per level
		return formatNumber(s.Offset+s.Factor, precision) + " (+" + formatNumber(s.Factor, precision) + " per level)"
	}
	name := attrNames[s.Attr]
	if name == "" {
		name = regexp.MustCompile(`([a-z])([A-Z])`).ReplaceAllString(s.Attr, "$1 $2")
	}
	pct := strconv.FormatFloat(math.Round(s.Factor*1000)/10, 'f', -1, 64)
	out := pct + "% of " + name
	if s.Offset != 0 {
		out = formatNumber(s.Offset, max(precision, 1)) + " + " + out
	}
	return out
}

// ranked is a value per rank ({10, 20, 30}) at the rank, its last entry past its end; any other value as it is.
func ranked(v luac.Value, rank int) luac.Value {
	t, ok := v.(*luac.Table)
	if !ok {
		return v
	}
	n := 0
	for t.Get(float64(n+1)) != nil {
		n++
	}
	if n == 0 {
		return nil
	}
	return t.Get(float64(min(max(rank, 1), n)))
}

// tokenValue is a token's text: the named script's TranslateToken for it (at the passive's rank), else the script's
// field of that name; unknownMark when it needs a match.
func tokenValue(sc *scripts, script, token string, precision, rank int) string {
	return scriptToken(sc, sc.get(script), token, precision, rank)
}

// scriptToken is tokenValue for a script already found (nil: unknownMark).
func scriptToken(_ *scripts, s *luac.Script, token string, precision, rank int) string {
	if s == nil {
		return unknownMark
	}
	p := luac.NewTable()
	p.Set("token", token)
	p.Set("rank", float64(rank))
	// the context fills the modifier's duration at the rank (`p.translatedToken = p.duration`) [inferred]
	if d := ranked(s.Field("duration"), rank); d != nil {
		p.Set("duration", d)
	}
	v, ok := s.Call("TranslateToken", p)
	if !ok || v == nil { // no handler, or one that leaves the token nil (Chain Lightning's effectCooldown): the field [inferred]
		v = s.Field(token)
	}
	v = ranked(v, rank)
	switch x := v.(type) {
	case float64:
		return formatNumber(x, precision)
	case luac.Scaled:
		return formatScaled(x, precision)
	}
	return unknownMark
}

// passiveText is a passive's text at its rank, plain and in coloured runs; "" when it has none.
func passiveText(sc *scripts, main *luac.Script, rank int, loc *assetdata.Locale) (string, []content.TextPart) {
	raw := rankedDescription(main, rank, loc)
	if m := fontLead.FindStringIndex(raw); m != nil {
		raw = strings.TrimLeft(raw[m[1]:], " ")
	}
	raw = tokenRe.ReplaceAllStringFunc(raw, func(tok string) string {
		m := tokenRe.FindStringSubmatch(tok)
		prec, _ := strconv.Atoi(m[1])
		script := strings.TrimSuffix(m[2], ".")
		if i := strings.LastIndexByte(script, '.'); i >= 0 { // "rankUpText0x….ability.token": the last two parts
			script = script[i+1:]
		}
		if script == "" {
			if m[3] == "rankText" && rank >= 1 && rank <= 3 { // "Consume ~rankText~": the rank in Roman numerals [assumed]
				return []string{"I", "II", "III"}[rank-1]
			}
			// a token without a script names the text's own script (the spells' "~damage~", the potions'
			// "~totalHealing~"; ITEMSHOP) [inferred]
			return scriptToken(sc, main, m[3], prec, rank)
		}
		return tokenValue(sc, script, m[3], prec, rank)
	})
	var kept []string
	for _, line := range brRe.Split(raw, -1) {
		plain := strings.TrimSpace(html.UnescapeString(markRe.ReplaceAllString(line, "")))
		if strings.Contains(line, unknownMark) || strings.HasPrefix(plain, "Current ") {
			continue
		}
		kept = append(kept, line)
	}
	// no empty lines at either end, at most one between paragraphs
	var lines []string
	for _, l := range kept {
		empty := strings.TrimSpace(markRe.ReplaceAllString(l, "")) == ""
		if empty && (len(lines) == 0 || lines[len(lines)-1] == "") {
			continue
		}
		if empty {
			l = ""
		}
		lines = append(lines, l)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	parts := markupParts(strings.Join(lines, "\n"))
	var plain strings.Builder
	for _, p := range parts {
		plain.WriteString(p.Text)
	}
	return strings.TrimSpace(plain.String()), parts
}

// markupParts turns the client's markup into runs: <font color="#rrggbb"> colours its text until </font> (nested
// fonts restore the outer colour), any other tag is dropped, entities are decoded.
func markupParts(s string) []content.TextPart {
	var parts []content.TextPart
	var stack []string
	color := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1]
	}
	add := func(text string) {
		text = html.UnescapeString(text)
		if text == "" {
			return
		}
		c := color()
		if n := len(parts); n > 0 && parts[n-1].Color == c {
			parts[n-1].Text += text
			return
		}
		parts = append(parts, content.TextPart{Text: text, Color: c})
	}
	pos := 0
	for _, m := range markRe.FindAllStringSubmatchIndex(s, -1) {
		add(s[pos:m[0]])
		pos = m[1]
		closing, tag, attrs := s[m[2]:m[3]] == "/", strings.ToLower(s[m[4]:m[5]]), s[m[6]:m[7]]
		if tag != "font" {
			continue
		}
		if closing {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		c := color()
		if cm := colorRe.FindStringSubmatch(attrs); cm != nil && hexRe.MatchString(cm[1]) {
			c = strings.ToUpper(cm[1])
		}
		stack = append(stack, c)
	}
	add(s[pos:])
	return parts
}
