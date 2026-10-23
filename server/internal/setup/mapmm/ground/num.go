package ground

import "math"

// The numeric helpers that make float64 arithmetic match the Python reference bit for bit.

// pmax and pmin are Python's max(a, b) and min(a, b) of two floats: the first argument unless the second is
// strictly greater (smaller).
func pmax(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

func pmin(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

// fmod is Python's float a % b: the result takes the divisor's sign.
func fmod(a, b float64) float64 {
	r := math.Mod(a, b)
	if r != 0 {
		if (r < 0) != (b < 0) {
			r += b
		}
	} else {
		r = math.Copysign(0, b)
	}
	return r
}

// imod is Python's integer a % b (floored).
func imod(a, b int) int {
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		r += b
	}
	return r
}

// roundInt is Python's int(round(x)): half to even, then to an integer.
func roundInt(x float64) int { return int(math.RoundToEven(x)) }

func clampInt(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

// hypot is CPython 3.12's math.hypot of two values (its vector_norm: the squares summed exactly in double-length
// arithmetic, one square root and a differential correction). Go's math.Hypot rounds differently in the last bit.
func hypot(a, b float64) float64 {
	a, b = math.Abs(a), math.Abs(b)
	if math.IsNaN(a) || math.IsNaN(b) {
		if math.IsInf(a, 0) || math.IsInf(b, 0) {
			return math.Inf(1)
		}
		return math.NaN()
	}
	mx := a
	if b > mx {
		mx = b
	}
	if math.IsInf(mx, 0) {
		return mx
	}
	if mx == 0 {
		return mx
	}
	_, e := math.Frexp(mx)
	if e < -1023 {
		const dblMin = 0x1p-1022
		return dblMin * hypot(a/dblMin, b/dblMin)
	}
	scale := math.Ldexp(1, -e)
	csum, frac1, frac2 := 1.0, 0.0, 0.0
	for _, x := range [2]float64{a, b} {
		x *= scale
		hi, lo := dlMul(x, x)
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
	x := csum - 1.0 + (frac1 + frac2)
	h += x / (2.0 * h)
	return h / scale
}

// dlMul is the error-free product: x * y = hi + lo exactly.
func dlMul(x, y float64) (hi, lo float64) {
	hi = float64(x * y)
	return hi, math.FMA(x, y, -hi)
}

// fastSum is the error-free sum of a and b with |a| >= |b|.
func fastSum(a, b float64) (hi, lo float64) {
	hi = a + b
	return hi, (a - hi) + b
}
