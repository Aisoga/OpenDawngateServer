package fogmap

import "math"

// hypot is the Python reference's math.hypot: the scaled sum of squares in double-double arithmetic with one
// differential correction after the square root, which rounds (nearly always) correctly where math.Hypot can be
// an ulp off. Distances that are mathematically equal then compare equal, as they do in the reference.
func hypot(x, y float64) float64 {
	x, y = math.Abs(x), math.Abs(y)
	if math.IsNaN(x) || math.IsNaN(y) {
		if math.IsInf(x, 0) || math.IsInf(y, 0) {
			return math.Inf(1)
		}
		return math.NaN()
	}
	m := 0.0
	if x > m {
		m = x
	}
	if y > m {
		m = y
	}
	return vectorNorm(x, y, m)
}

func vectorNorm(x, y, m float64) float64 {
	if math.IsInf(m, 0) || m == 0 {
		return m
	}
	_, e := math.Frexp(m)
	if e < -1023 { // subnormal: scale up into the normal range first
		const dblMin = 2.2250738585072014e-308
		return dblMin * vectorNorm(x/dblMin, y/dblMin, m/dblMin)
	}
	scale := math.Ldexp(1, -e)
	csum, frac1, frac2 := 1.0, 0.0, 0.0
	for _, v := range [2]float64{x, y} {
		v *= scale // lossless
		hi, lo := dlMul(v, v)
		s, slo := fastSum(csum, hi)
		csum = s
		frac1 += lo
		frac2 += slo
	}
	h := math.Sqrt(csum - 1.0 + (frac1 + frac2))
	hi, lo := dlMul(-h, h)
	s, slo := fastSum(csum, hi)
	csum = s
	frac1 += lo
	frac2 += slo
	x = csum - 1.0 + (frac1 + frac2)
	h += x / (2.0 * h)
	return h / scale
}

// dlMul is the exact product x*y as hi + lo.
func dlMul(x, y float64) (float64, float64) {
	z := float64(x * y)
	return z, math.FMA(x, y, -z)
}

// fastSum is the exact sum a + b (|a| >= |b|) as hi + lo.
func fastSum(a, b float64) (float64, float64) {
	x := a + b
	z := x - a
	return x, b - z
}

// minF and maxF are Python's min and max of two floats: the first argument unless the second is smaller (larger).
func minF(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func maxF(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
