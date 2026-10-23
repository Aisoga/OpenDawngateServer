package los

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

func TestReadTGA(t *testing.T) {
	b := make([]byte, 18+3+2*2)
	b[0], b[2], b[12], b[14], b[16] = 3, 3, 2, 2, 8
	copy(b[21:], []byte{0, 128, 128, 0})
	img, err := Read(b)
	if err != nil {
		t.Fatal(err)
	}
	if img.W != 2 || img.H != 2 || img.Pix[1] != 128 {
		t.Errorf("got %+v", img)
	}
	if _, err := Read(b[:22]); err == nil {
		t.Error("a cut TGA was read")
	}
	b[16] = 24
	if _, err := Read(b); err == nil {
		t.Error("a 24-bit TGA was read as a line-of-sight map")
	}
}

// TestGame reads the shipped map out of the game's own images.snap.
func TestGame(t *testing.T) {
	raw, err := FromArchive(testgame.Read(t, Archive), Entry)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(img.Pix)
	t.Logf("%d x %d, pixels sha256 %s", img.W, img.H, hex.EncodeToString(h[:]))
	if img.W != 512 || img.H != 512 {
		t.Errorf("%d x %d, 512 x 512 expected", img.W, img.H)
	}
}
