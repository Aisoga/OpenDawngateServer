package gamecontent

import (
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/luac"
)

// The shop's stat lines ("+20 Haste") as the client writes them: LabsTooltip_FormatAttributeLine (0x004CFE80) looks
// the attribute's line up in the Locale table 0x4B510965 by the table at 0x013E1400 ({attribute, line for Add, line
// for PercentChange}; FUN_004CB930) and fills ~signedAttributeValue~ with the value in the attribute's colour
// (FUN_004A7C40): <font color="#RRGGBB">+value</font>, the value "%.3f" with the trailing zeros and point dropped.
// Only the Add lines of the attributes build 854's items use are here; the shares (Movement Speed, Lifedrain) are
// stored as 0.05 and shown as 5, the line itself adding the "%" [inferred from their lines and the shop's pictures].
const statTable = 0x4B510965

var statLines = map[string]uint32{
	"Power": 0x0D6EEFDB, "Mastery": 0x08D0C4D5, "MaxHealth": 0x08D0C4D0, "HealthRegen": 0x0D6EEFD7,
	"MovementSpeedPercentIncrease": 0x09F92EC8, "Haste": 0x0A7BC3E3, "PhysicalDamageReduction": 0x0D6EEFE0,
	"MagicalDamageReduction": 0x0D6EEFE1, "DefensePenetration": 0x0E6ABCA7, "LifeDrain": 0x0A7BC3E1,
}

// statColor is FUN_004A7C40: the colour of an attribute's value in a tooltip, by the attribute enum.
func statColor(attr int) string {
	switch {
	case attr == 0:
		return "#55FF55"
	case attr == 1 || attr >= 34 && attr <= 37:
		return "#BFFFD4"
	case attr == 2 || attr >= 38 && attr <= 40:
		return "#FF2929"
	case attr == 4:
		return "#FF5561"
	case attr >= 8 && attr <= 12:
		return "#55D9FF"
	case attr >= 14 && attr <= 16:
		return "#5555FF"
	case attr == 17 || attr == 18 || attr == 69 || attr == 70:
		return "#E3E3A3"
	case attr >= 21 && attr <= 24:
		return "#FF8800"
	case attr == 25:
		return "#9545D5"
	case attr >= 30 && attr <= 32:
		return "#47CC82"
	case attr == 64:
		return "#FFE656"
	}
	return "#EDE0AE"
}

// statValue is the client's "%.3f" with the trailing zeros and the point dropped (0x004CFE80).
func statValue(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// statParts is a stat's line in runs of text with their colours, or nil when the attribute has no line here or the
// Locale lacks it.
func statParts(s content.ItemStat, loc *assetdata.Locale) []content.TextPart {
	key, ok := statLines[s.ID]
	if !ok || loc == nil {
		return nil
	}
	line, ok := loc.Text(statTable, key)
	if !ok || !strings.Contains(line, "~signedAttributeValue~") {
		return nil
	}
	attr, _ := content.AttributeIndex(s.ID)
	v := float64(s.Value)
	if strings.Contains(line, "%") {
		v *= 100
	}
	sign := "+"
	if v < 0 {
		sign = ""
	}
	val := `<font color="` + statColor(attr) + `">` + sign + statValue(v) + `</font>`
	return markupParts(strings.Replace(line, "~signedAttributeValue~", val, 1))
}

// passiveLead is the passive's name as its description starts with it (<font color="#FFFF99">Consume I</font>:), the
// name the shop shows, with ~rankText~ as the rank in Roman numerals (passiveText's rule); "" when the text starts
// otherwise or keeps another token.
func passiveLead(main *luac.Script, rank int, loc *assetdata.Locale) string {
	m := fontLead.FindStringSubmatch(rankedDescription(main, rank, loc))
	if m == nil {
		return ""
	}
	name := strings.TrimSpace(m[1])
	if strings.Contains(name, "~rankText~") && rank >= 1 && rank <= 3 {
		name = strings.Replace(name, "~rankText~", []string{"I", "II", "III"}[rank-1], 1)
	}
	if strings.Contains(name, "~") {
		return ""
	}
	return name
}

// passiveKind is the label the shop puts before a passive (FUN_004B4EF0, by the script's activation type, Locale
// table 0x4B510965): "unique" for Unique (1), CasterUnique (2), CasterUniqueIrreplaceable (3), UniqueIrreplaceable (7)
// and 8 ("Unique Passive"), "stackable" for Stacks (4) and StacksAndCasterUnique (5) ("Stackable Passive"), else ""
// ("Passive"). The script names the type as nActivationType.<Name>; UniqueResets is taken as 8 [assumed].
func passiveKind(sc *luac.Script) string {
	g, ok := sc.Field("activationType").(luac.Global)
	if !ok {
		return ""
	}
	name := string(g)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "Unique", "CasterUnique", "CasterUniqueIrreplaceable", "UniqueIrreplaceable", "UniqueResets":
		return "unique"
	case "Stacks", "StacksAndCasterUnique":
		return "stackable"
	}
	return ""
}
