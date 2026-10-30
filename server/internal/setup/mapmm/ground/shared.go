package ground

// The pieces another ground writer shares (the map compiler's baked ground): the vertex layout, the bounds and the
// template's parts, so its models stay in the same form as this package's.

// EncodeVertex appends one vertex in the 32-byte layout (FLOAT3 position, UBYTE4N normal and tangent, FLOAT2 UV,
// UBYTE4N colour with alpha 255).
func EncodeVertex(out []byte, pos, nrm [3]float64, uv [2]float64, rgb [3]float64) []byte {
	return encodeVertex(out, pos, nrm, uv, rgb)
}

// Height is the ground's height (m) at (x, y), d being the signed distance to the walkable ground's edge there
// (Field.At or Field.D): the floor, the cliff and the plateau, or the drop where s.Down says the edge is a chasm's.
func Height(d, x, y float64, s *Spec) float64 { return height(d, x, y, s) }

// Bounds are the points' bounds in the model's form: centre, radius, half extents, radius squared (withOrigin adds
// the origin, as a part's bounds do).
func Bounds(pts [][3]float64, withOrigin bool) []float32 { return boundsOf(pts, withOrigin) }
