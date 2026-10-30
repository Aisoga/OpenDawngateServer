package mapc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CookShared cooks the shared mesh's input again (the developers' check that shared_bin.hex can be made again): the
// physcook tool (gms/physcook) over a copy of the game's PhysXCooking.dll, in a temporary folder. It returns the
// cooked bytes and their hex text; the caller compares them with SharedCooked.
func CookShared(physcook, cookingDLL string) (cooked, hexText []byte, err error) {
	dll, err := os.ReadFile(cookingDLL)
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "mapc-cook-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	dllCopy := filepath.Join(tmp, "PhysXCooking.dll")
	in, out := filepath.Join(tmp, "mesh.dgmi"), filepath.Join(tmp, "mesh.bin")
	dgmi := SharedMeshInput()
	if err := os.WriteFile(dllCopy, dll, 0o644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(in, dgmi, 0o644); err != nil {
		return nil, nil, err
	}
	if b, err := exec.Command(physcook, dllCopy, in, out).CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("%s: %v\n%s", physcook, err, b)
	}
	if cooked, err = os.ReadFile(out); err != nil {
		return nil, nil, err
	}
	hexText = WriteMeshHex(dgmi, cooked)
	if _, _, err := ReadMeshHex(hexText); err != nil {
		return nil, nil, fmt.Errorf("the cooked mesh: %v", err)
	}
	return cooked, hexText, nil
}
