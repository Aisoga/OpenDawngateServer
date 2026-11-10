package botprofiles

import (
	"fmt"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
)

// The match rules' texts for one player. The match host shows a player a line in the HUD's alert line by sending a
// ClientFeedbackEvent whose ClientFeedbackDef has a text key; the game ships no def that says anything about the shop
// or an item, so the copy gets two of its own, written with the assetdata writer, listed in this archive's catalog and
// carried as files in it (they have no archive of their own). Their text keys are lines that the locale patch appends
// to the LabsAlerts table (localepatch.LabsAlertsLines).

// RulesSource is the name of the match rules' catalog source.
const RulesSource = "match rules"

// FeedbackDef is one of the match rules' ClientFeedbackDefs: its name (without ".ClientFeedbackDef") and its text key.
type FeedbackDef struct{ Name, TextKey string }

// RulesFeedbackDefs are the match rules' defs: a purchase the shop refuses outside death (Shop on Death) and an item
// outside the match's pool.
var RulesFeedbackDefs = []FeedbackDef{
	{Name: "Rules_ShopClosed", TextKey: "LabsAlerts!0x5e70e001"},
	{Name: "Rules_ItemNotAllowed", TextKey: "LabsAlerts!0x5e70e002"},
}

// FeedbackClass is the defs' class; FeedbackCooldown the client's cooldown of a def in milliseconds (the shipped
// OutOfRange has 7000: a refused purchase may be tried again sooner).
const (
	FeedbackClass    = "ClientFeedbackDef"
	FeedbackCooldown = 2000
)

// FeedbackSource is a def's source file name in the catalog.
func FeedbackSource(name string) string { return "ClientFeedback/" + name + "." + FeedbackClass }

// FeedbackValue is a def's value: the shipped OutOfRange's (a text-only error line of the player's own on the
// character channel: priority 300, lifetime 3000 ms, shown in the alert line, not in chat) with the def's text key
// and FeedbackCooldown. No sound, no minimap ping, doNotQueue false (a text alert must queue), no follow-up def, no
// replacement rules: the members left out are zero or empty.
func FeedbackValue(d FeedbackDef) *assetdata.Object {
	v := assetdata.NewObject()
	v.Set("priority", uint64(300))
	v.Set("channel", "character")
	v.Set("lifetime", uint64(3000))
	v.Set("cooldown", uint64(FeedbackCooldown))
	v.Set("cooldownPerObject", false)
	v.Set("textLocaleKey", d.TextKey)
	v.Set("textType", "normal")
	v.Set("showTextInAlert", true)
	v.Set("chatMessageFilterType", "off")
	return v
}

// RulesFeedback is the match rules' catalog source: one entry per def, written anew with its own source file name,
// and the defs' .bin files, which this archive carries.
func RulesFeedback() (CatalogSource, error) {
	ts, err := assetdata.Embedded()
	if err != nil {
		return CatalogSource{}, err
	}
	t := ts.Type(FeedbackClass)
	if t == nil {
		return CatalogSource{}, fmt.Errorf("botprofiles: no %s table", FeedbackClass)
	}
	src := CatalogSource{Name: RulesSource}
	for _, d := range RulesFeedbackDefs {
		b, err := ts.Write(t, FeedbackValue(d), assetdata.WriteOptions{})
		if err != nil {
			return CatalogSource{}, fmt.Errorf("botprofiles: %s: %w", d.Name, err)
		}
		asset := d.Name + "." + FeedbackClass
		src.Entries = append(src.Entries, CatalogEntry{Name: asset, Source: FeedbackSource(d.Name)})
		src.Files = append(src.Files, snap.File{Name: "AssetData/Binary/" + asset + ".bin", Data: b})
	}
	return src, nil
}
