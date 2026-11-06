package main

import (
	"fmt"
	"os/exec"
)

// signForMac re-signs a built program with an ad-hoc signature, which macOS
// requires before it will run a changed executable on Apple silicon.
func signForMac(path string) error {
	out, err := exec.Command("codesign", "--force", "--sign", "-", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}
