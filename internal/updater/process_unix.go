//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package updater

import (
	"os"
	"syscall"
)

func ownership(info os.FileInfo) (uid int, links uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(stat.Uid), uint64(stat.Nlink), true
}

func execProcess(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
