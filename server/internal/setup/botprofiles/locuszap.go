package botprofiles

import (
	"fmt"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
)

// The Locus's zap gets a sound of its own. Each base's Locus hits an enemy shaper with an impact effect whose only
// sound, in the shipped data, is Salous's ultimate's hit: an event of Salous's own sound bank, so a match without a
// Salous hears nothing (the client asserts for each zap) and a match with one hears his ultimate. The copy carries
// changed copies of the two impact defs that play the Guardian's laser blast hit instead, an event of a bank every
// client loads at start. Only the event's name changes: no sound is added or shipped. The defs are already in the
// shipped catalog, so this source lists no entries; its files win over AssetData.snap's because this archive mounts
// after it.

// LocusZapSource is the name of the Locus zap's catalog source (files only).
const LocusZapSource = "locus zap sound"

// LocusZapEvent is the zap's sound: the Guardian's laser blast hitting (guardian_laserBlast_beam_impact_effect's
// event, in Audio_Wwise/Minion_Guardian.bnk). LocusZapShipped is the event the shipped defs play.
const (
	LocusZapEvent   = "Play_Minion_Guardian_beam_attack_hit"
	LocusZapShipped = "Play_Salous_Ult_hit"
)

// LocusZapDefs are the Locus's impact effects (team 2's and team 1's), without ".ServerEventDef".
var LocusZapDefs = []string{"base_megaZap_impact_red_effect", "base_megaZap_impact_blue_effect"}

// LocusZapFile is a def's file in the archive (and in AssetData.snap).
func LocusZapFile(def string) string { return "AssetData/Binary/" + def + ".ServerEventDef.bin" }

// LocusZapSound is the Locus zap's source: the two defs read from a copy's AssetData.snap (the whole file), each with
// its one sound's event changed from LocusZapShipped to LocusZapEvent and written back over the shipped file's own
// layout, so every other byte stays the shipped one. A def that doesn't hold exactly that one sound is refused.
func LocusZapSound(assetData []byte) (CatalogSource, error) {
	a, err := snap.Parse(assetData)
	if err != nil {
		return CatalogSource{}, err
	}
	byName := map[string]snap.Entry{}
	for _, e := range a.Entries {
		byName[strings.ToLower(e.Name)] = e
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		return CatalogSource{}, err
	}
	t := ts.Type("ServerEventDef")
	if t == nil {
		return CatalogSource{}, fmt.Errorf("botprofiles: no ServerEventDef table")
	}
	src := CatalogSource{Name: LocusZapSource}
	for _, def := range LocusZapDefs {
		rel := LocusZapFile(def)
		e, ok := byName[strings.ToLower(rel)]
		if !ok {
			return CatalogSource{}, fmt.Errorf("botprofiles: AssetData.snap has no %s", rel)
		}
		shipped, err := a.Read(e)
		if err != nil {
			return CatalogSource{}, err
		}
		b, err := ChangeZapSound(ts, t, shipped)
		if err != nil {
			return CatalogSource{}, fmt.Errorf("botprofiles: %s: %w", def, err)
		}
		src.Files = append(src.Files, snap.File{Name: rel, Data: b})
	}
	return src, nil
}

// ChangeZapSound is a ServerEventDef file with its one sound's event LocusZapEvent instead of LocusZapShipped.
func ChangeZapSound(ts *assetdata.Types, t *assetdata.Type, shipped []byte) ([]byte, error) {
	v, err := ts.ParseExact(t, shipped)
	if err != nil {
		return nil, err
	}
	audio, _ := v.Get("audio")
	list, _ := audio.([]any)
	if len(list) != 1 {
		return nil, fmt.Errorf("%d sounds, want 1", len(list))
	}
	snd, _ := list[0].(*assetdata.Object)
	if snd == nil {
		return nil, fmt.Errorf("the sound isn't a struct")
	}
	if name, _ := snd.Get("name"); name != LocusZapShipped {
		return nil, fmt.Errorf("the sound is %v, want %s", name, LocusZapShipped)
	}
	snd.Set("name", LocusZapEvent)
	return ts.Write(t, v, assetdata.WriteOptions{Like: shipped})
}
