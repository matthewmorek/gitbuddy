package updater

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type directory struct {
	path string
	info os.FileInfo
}

type snapshot struct {
	file os.FileInfo
	hash [sha256.Size]byte
	dirs []directory
}

func inspect(path string, uid int) (*snapshot, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "gitbuddy" {
		return nil, errors.New("executable must be an absolute, unambiguous path named gitbuddy")
	}
	s := &snapshot{}
	dest := filepath.Dir(path)
	for dir := dest; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, fmt.Errorf("inspect installation directory: %w", err)
		}
		owner, _, ok := ownership(info)
		if !ok || !info.IsDir() || owner != 0 && owner != uid || info.Mode().Perm()&0022 != 0 {
			return nil, fmt.Errorf("unsafe installation directory or ancestor: %s", dir)
		}
		if dir == dest && (owner != uid || info.Mode().Perm()&0300 != 0300) {
			return nil, errors.New("installation directory must be user-owned and writable/searchable")
		}
		s.dirs = append(s.dirs, directory{dir, info})
		if dir == filepath.Dir(dir) {
			break
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect executable: %w", err)
	}
	owner, links, ok := ownership(info)
	if !ok || !info.Mode().IsRegular() || owner != uid || links != 1 ||
		info.Mode().Perm()&0100 == 0 || info.Mode().Perm()&0022 != 0 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return nil, errors.New("executable must be user-owned, executable, regular, single-linked and not writable by others")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open executable: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameFileState(info, opened) {
		return nil, errors.New("executable changed while opening")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxBinary+1))
	if err != nil || n > maxBinary {
		return nil, errors.New("cannot hash executable within the size limit")
	}
	after, err := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !sameFileState(info, after) || !sameFileState(info, current) {
		return nil, errors.New("executable changed while hashing")
	}
	s.file = info
	copy(s.hash[:], h.Sum(nil))
	return s, nil
}

func sameFileState(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	au, al, aok := ownership(a)
	bu, bl, bok := ownership(b)
	return aok && bok && os.SameFile(a, b) && a.Mode() == b.Mode() &&
		a.Size() == b.Size() && a.ModTime() == b.ModTime() && au == bu && al == bl
}

func sameSnapshot(a, b *snapshot) bool {
	if a == nil || b == nil || !sameFileState(a.file, b.file) || a.hash != b.hash || len(a.dirs) != len(b.dirs) {
		return false
	}
	for i := range a.dirs {
		// Directory mtime/size change when we create staging files; identity,
		// ownership and permission checks, not incidental contents, matter.
		if a.dirs[i].path != b.dirs[i].path || !os.SameFile(a.dirs[i].info, b.dirs[i].info) {
			return false
		}
	}
	return true
}
