//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package updater

import (
	"errors"
	"os"
)

func ownership(os.FileInfo) (int, uint64, bool) { return 0, 0, false }

func execProcess(string, []string, []string) error {
	return errors.New("self-update restart is not supported on this platform")
}
