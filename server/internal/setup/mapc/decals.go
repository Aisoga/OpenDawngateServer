package mapc

import (
	"fmt"
	"math"
	"strings"

	"dawngate/server/internal/setup/assetdata"
)

// The decals (ground.decals): the engine's projected pictures, as the shipped holiday set places them. Each is a copy
// of that set's first Decal.Noun marker (its decal data's other members kept), moved, turned and given the map's
// texture, size, opacity and tint, in the map's environment markerset.

// DecalTemplateSet is the shipped markerset whose decal marker the map's decals copy.
const DecalTemplateSet = "SM_Map_v5_holiday_treatment.Markerset"

// decalNoun is the decal markers' noun.
const decalNoun = "Decal.Noun"

func (b *assetBuild) decals() error {
	ds := b.cm.Map.Ground.Decals
	if len(ds) == 0 {
		return nil
	}
	set, err := b.src.object(DecalTemplateSet)
	if err != nil {
		return err
	}
	var tmpl *assetdata.Object
	for _, m := range markers(set) {
		if strings.EqualFold(text(m, "nounDef"), decalNoun) && sub(m, "decalData") != nil {
			tmpl = m
			break
		}
	}
	if tmpl == nil {
		return fmt.Errorf("%s has no %s marker with decal data", DecalTemplateSet, decalNoun)
	}
	for k, d := range ds {
		places := []struct {
			p   Point
			rot float64
		}{{d.Pos, d.Rot}}
		if boolOr(d.Mirror, true) {
			places = append(places, struct {
				p   Point
				rot float64
			}{Mirror(d.Pos), math.Mod(d.Rot+180, 360)})
		}
		for side, pl := range places {
			m := b.place(setEnvironment, tmpl, fmt.Sprintf("decal/%d/%d", k, side), pl.p)
			m.Set("pos", f32s(pl.p[0], pl.p[1], d.Z))
			m.Set("rotDegrees", f32s(0, 0, pl.rot))
			m.Set("scale", float32(1))
			m.Set("visible", true)
			dd := cloneObj(sub(tmpl, "decalData"))
			dd.Set("size", f32s(d.Size[0], d.Size[1], d.Size[2]))
			dd.Set("diffuse", d.Diffuse)
			dd.Set("opacity", float32(floatOr(d.Opacity, 1)))
			t := tintOr(d.Tint)
			dd.Set("diffuseTint", f32s(t[0], t[1], t[2]))
			m.Set("decalData", dd)
		}
	}
	return nil
}
