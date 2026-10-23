package dds

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"
)

// pattern is the made-up RGBA test image (rows top to bottom): flat opaque blocks, flat half-transparent grey
// blocks and gradients with alpha. The Python reference's pin script builds the same.
func pattern(w, h int) []byte {
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bx, by := x/4, y/4
			var px [4]int
			switch {
			case (bx+2*by)%5 == 0:
				px = [4]int{200, 40, 90, 255}
			case (bx+by)%5 == 1:
				px = [4]int{17, 17, 17, 128}
			default:
				a := 255
				if bx%2 != 0 {
					a = (x*13 + y*5) % 256
				}
				px = [4]int{x * 255 / max(1, w-1), y * 255 / max(1, h-1), (x*37 + y*91) % 256, a}
			}
			o := (y*w + x) * 4
			for c := 0; c < 4; c++ {
				out[o+c] = byte(px[c])
			}
		}
	}
	return out
}

// lcg: made-up bytes.
func lcg(n int, seed uint32) []byte {
	out := make([]byte, n)
	s := seed
	for i := range out {
		s = (s*1103515245 + 12345) & 0x7FFFFFFF
		out[i] = byte(s >> 16)
	}
	return out
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pins: the Python reference's hashes of the same made-up inputs.
var pins = map[string]string{
	"encode_dxt1_4x4":         "7ac5ae4cabe2d9de1123769fce519cea8f21e6d4f502c946d453e374d011e667",
	"decode_dxt1_4x4":         "1a254100a00ffc95231522da90e13ff7901953b76f76d4701a153be79d1274c6",
	"encode_dxt5_4x4":         "d89ca8d3e381b2601caace2151d820942a79a56604b8ccb5b8f2127b69ee20da",
	"decode_dxt5_4x4":         "1a254100a00ffc95231522da90e13ff7901953b76f76d4701a153be79d1274c6",
	"encode_dxt1_8x12":        "d785475770a543b40c0c7fbde66736e01636995570aacdb2d896c32f6e635d8c",
	"decode_dxt1_8x12":        "f799b4b6fe27f9c50837f02f02e12588c66bd950e04e3651a223cec162e27bce",
	"encode_dxt5_8x12":        "fd4aa9262fdd24aef7650aeb39de11faa8ea5fb895c7ad2c111229c3a70d1993",
	"decode_dxt5_8x12":        "71703b44be19b3d9363eee95c13b0cfaeb03da2f834e519696c4f1ac75b01d54",
	"encode_dxt1_16x16":       "8af74dc6ee738cb5a25d4c4429ae93830a7650c231d4d9edff2a16d022ad8c39",
	"decode_dxt1_16x16":       "eff57d4a15ea837dc67534f79bf47541c417b90410e481ff2277029c0d2b621e",
	"encode_dxt5_16x16":       "a4e6de688dd858a619ef0dc62156a655e280d1fa08a5b88169130b40d8b0f3f1",
	"decode_dxt5_16x16":       "417aeb0bd312ad81652d6974a93d78964655c74b0967c11a7714b0657e2f411c",
	"encode_dxt1_64x32":       "99e052dd5d2c43db58d242ef7f517938f1df5d1eeef17ce398c463adb2027377",
	"decode_dxt1_64x32":       "3efe28e6762fce8ebebb3927d493088e92bee4b2f031543513df57ad22ad06f9",
	"encode_dxt5_64x32":       "e0b88ff663ade886b2102f89a69be687efe9ac24b3a4d0e246f7c2449deb6679",
	"decode_dxt5_64x32":       "d14df1f000d097ecb94621a1b1d17b3d3d7822f9a418a4e5932953899959c84a",
	"encode_dxt1_100x60":      "223867ab51e9ae2e9de039e881fbcdfd3877b5c16b826cd126990ee9bd162604",
	"decode_dxt1_100x60":      "982967b95aca8b9e04cda68439ea29490d8b3dad469889e55b4ca0b94e0a18f0",
	"encode_dxt5_100x60":      "d56bf2b75360f4d9c72ca12d0617d5adb17c00cb6ab8f21d7440f7d69bfe23d8",
	"decode_dxt5_100x60":      "9ca61298427fa24bf9984fd21c1b367a2e8870bbc2b4b13aca79fcad4676bf6f",
	"encode_dxt5_64x32_full":  "a5cb5c4fcacb73725bc36b2e2079a2a711d62b6e8c237e9e66cc0ca88cb9a02e",
	"encode_dxt1_100x60_full": "27e1f0c662377764039fc1c9ae2c9fd0aa7544dc0604fb677f317676964f78b0",
	"encode_argb_16x16":       "dcd956c5bea811d986da9ea7eed0437d08e4687074346629ad9619e4997427a0",
	"decode_argb_16x16":       "31450e63a090a10205384dbe9219ce6eab55001daf9d8b772e77d654d3b26910",
	"decode_dxt1_random":      "41c82ea5ab3d9bb61b020b7a330344d452edc4189c33ccb339a047a6664ad797",
	"decode_dxt1_random_6x6":  "8c2f01a6e689dd39532165c5c9bd824c685ba3539dc83ced15c7469efe57beae",
	"decode_dxt5_random":      "6308eb3c3e0c312480e90649b7fcaf1403a5600477961761411b7594479efc35",
	"decode_dxt5_random_6x6":  "ab32893f7f0f5a0726aaa94f261b0de572154b6b0606f42327c8ba966e8d2ecb",
}

func check(t *testing.T, name string, b []byte) {
	t.Helper()
	want, ok := pins[name]
	if !ok {
		t.Fatalf("%s: no pin", name)
	}
	if got := sum(b); got != want {
		t.Errorf("%s: sha256 %s, the Python reference %s", name, got, want)
	}
}

func encode(t *testing.T, w, h int, px []byte, format string, mips int) []byte {
	t.Helper()
	raw, err := Encode(w, h, px, format, mips)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decode(t *testing.T, raw []byte) []byte {
	t.Helper()
	_, _, px, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return px
}

func TestPinned(t *testing.T) {
	for _, sz := range [][2]int{{4, 4}, {8, 12}, {16, 16}, {64, 32}, {100, 60}} {
		w, h := sz[0], sz[1]
		px := pattern(w, h)
		for _, f := range []string{"dxt1", "dxt5"} {
			raw := encode(t, w, h, px, f, 0)
			check(t, fmt.Sprintf("encode_%s_%dx%d", f, w, h), raw)
			check(t, fmt.Sprintf("decode_%s_%dx%d", f, w, h), decode(t, raw))
		}
	}
	check(t, "encode_dxt5_64x32_full", encode(t, 64, 32, pattern(64, 32), "dxt5", FullMips(64, 32)))
	check(t, "encode_dxt1_100x60_full", encode(t, 100, 60, pattern(100, 60), "dxt1", FullMips(100, 60)))
	raw := encode(t, 16, 16, pattern(16, 16), "argb", 0)
	check(t, "encode_argb_16x16", raw)
	check(t, "decode_argb_16x16", decode(t, raw))
	for _, c := range []struct {
		f     string
		block int
		seed  uint32
	}{{"dxt1", 8, 3}, {"dxt5", 16, 5}} {
		head := encode(t, 8, 8, pattern(8, 8), c.f, 1)[:Header]
		check(t, "decode_"+c.f+"_random", decode(t, append(bytes.Clone(head), lcg(4*c.block, c.seed)...)))
		head = bytes.Clone(head)
		binary.LittleEndian.PutUint32(head[12:], 6)
		binary.LittleEndian.PutUint32(head[16:], 6)
		check(t, "decode_"+c.f+"_random_6x6", decode(t, append(head, lcg(4*c.block, c.seed+1)...)))
	}
}

func TestHeader(t *testing.T) {
	raw := encode(t, 1024, 512, make([]byte, 1024*512*4), "dxt5", 0)
	in, err := ReadInfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	if in.W != 1024 || in.H != 512 || in.Mips != 7 || in.Format != "dxt5" || in.FourCC != "DXT5" {
		t.Errorf("info %+v", in)
	}
	if DefaultMips(1024, 1024) != 8 || FullMips(1024, 1024) != 11 {
		t.Error("mip counts")
	}
	if _, err := Encode(6, 8, make([]byte, 6*8*4), "dxt1", 0); err == nil {
		t.Error("a side of 6 was encoded")
	}
	if _, _, _, err := Decode(raw[:200]); err == nil {
		t.Error("a cut file was decoded")
	}
}
