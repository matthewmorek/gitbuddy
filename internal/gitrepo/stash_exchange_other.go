//go:build !darwin && !linux

package gitrepo

import "errors"

func exchangeStashLogs(_, _ string) error {
	return errors.New("This platform cannot safely exchange stash reflogs. Use Git to delete the intended entry.")
}
