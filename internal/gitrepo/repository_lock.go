package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func (r *Repository) commonDir(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "", "rev-parse", "--path-format=absolute", "--git-common-dir")
	return strings.TrimSuffix(out, "\n"), err
}

// Leave the lock file in place: unlinking it could give concurrent holders
// different inodes to lock. Closing the descriptor (including on process
// death) releases ownership; subprocesses do not inherit the descriptor.
func lockCommon(common, name string) (*os.File, error) {
	path := filepath.Join(common, name)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	info, err := f.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || namedErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, named) {
		f.Close()
		return nil, errors.New("Repository lock is not a regular file.")
	}
	return f, nil
}

func (r *Repository) lockSafeBackup(ctx context.Context) (*os.File, error) {
	common, err := r.commonDir(ctx)
	if err != nil {
		return nil, err
	}
	f, err := lockCommon(common, "gitbuddy-safe-pull.lock")
	if errors.Is(err, unix.EWOULDBLOCK) {
		return nil, errors.New("Another Gitbuddy Safe pull or recovery deletion is active in this repository. Wait for it to finish and refresh Stashes.")
	}
	if err != nil {
		return nil, fmt.Errorf("Cannot lock Safe pull recovery backups: %w", err)
	}
	return f, nil
}
