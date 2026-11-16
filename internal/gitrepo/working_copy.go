package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// WorkingCopyEntry is one current, repository-relative raw path, never escaped
// for presentation. IndexStatus and WorktreeStatus are porcelain-v2 XY codes
// ("." means unchanged, empty for a purely untracked entry). Untracked can
// coexist with a staged deletion. Submodule retains N... or S<c><m><u>.
type WorkingCopyEntry struct {
	Path, OldPath                     string
	IndexStatus, WorktreeStatus       string
	Untracked, Conflict               bool
	Submodule, RenameScore            string
	HeadMode, IndexMode, WorktreeMode string
	HeadObject, IndexObject           string
	// Unmerged entries retain the base, ours and theirs modes/object IDs.
	StageModes, StageObjects [3]string
	Stats                    LineStats
}

// LineStats describes complete counts for one or more comparisons. Unknown is
// distinct from zero; Binary never implies a numeric count.
type LineStats struct {
	Added, Deleted int
	Known, Binary  bool
}

// DiffSummary counts only rendered hunk lines, never patch file headers.
type DiffSummary struct {
	LineStats
	Chunks int
}

func (d WorkingCopyDetails) Summary() DiffSummary {
	s := DiffSummary{LineStats: LineStats{Known: d.Outcome == WorkingCopyReady}}
	for _, section := range d.Sections {
		part := sectionSummary(section)
		s.Known = s.Known && part.Known
		s.Binary = s.Binary || part.Binary
		s.Added += part.Added
		s.Deleted += part.Deleted
		s.Chunks += part.Chunks
	}
	return s
}

func sectionSummary(section WorkingCopySection) DiffSummary {
	s := DiffSummary{LineStats: LineStats{Known: true}}
	switch section.Kind {
	case WorkingCopyEmpty:
	case WorkingCopyPreview:
		if section.Text != "" {
			s.Chunks = 1
			s.Added = strings.Count(section.Text, "\n")
			if !strings.HasSuffix(section.Text, "\n") {
				s.Added++
			}
		}
	case WorkingCopyBinary:
		s.Known, s.Binary = false, true
	case WorkingCopyPatch:
		inHunk := false
		for _, raw := range strings.Split(section.Text, "\n") {
			switch {
			case strings.HasPrefix(raw, "diff --git "):
				inHunk = false
			case strings.HasPrefix(raw, "Binary files "), raw == "GIT binary patch":
				s.Known, s.Binary = false, true
			case strings.HasPrefix(raw, "@@ "):
				inHunk = true
				s.Chunks++
			case inHunk && strings.HasPrefix(raw, "+"):
				s.Added++
			case inHunk && strings.HasPrefix(raw, "-"):
				s.Deleted++
			}
		}
	default:
		s.Known = false
	}
	return s
}

// Two batched comparisons preserve staged/unstaged reversals. Untracked reads
// share the contained nofollow preview boundary and have both a per-file cap and
// an aggregate refresh budget; later rows explicitly remain unknown.
func (r *Repository) workingCopyStats(ctx context.Context, status *Status) error {
	if len(status.Entries) == 0 {
		return ctx.Err()
	}
	var comparisons [2]map[filePaths]ChangedFile
	for i := range comparisons {
		needed := false
		for _, e := range status.Entries {
			code := e.IndexStatus
			if i == 1 {
				code = e.WorktreeStatus
			}
			needed = needed || changed(code)
		}
		if !needed {
			continue
		}
		args := []string{"diff", "--numstat", "-z", "--no-color", "--no-ext-diff", "--no-textconv",
			"--find-renames", "--no-relative", "--ignore-submodules=none", "--submodule=short",
			// Unmerged records can duplicate a path's ordinary numstat record.
			// Conflicts are explicitly unknown below; don't poison other rows.
			"--diff-filter=u"}
		if i == 0 {
			args = append(args, "--cached")
		}
		out, err := r.git(ctx, "", append(args, "--")...)
		if err == nil {
			comparisons[i], err = parseNumstat(out)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			status.StatsWarning = "File counts unavailable · r Retry"
		}
	}
	remaining := 4 * MaxWorkingCopyBytes
	for i := range status.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := &status.Entries[i]
		stats := LineStats{Known: true}
		for comparison, code := range []string{e.IndexStatus, e.WorktreeStatus} {
			if !changed(code) {
				continue
			}
			key := filePaths{path: e.Path}
			if code == "R" || code == "C" {
				key.old = e.OldPath
			}
			part, found := comparisons[comparison][key]
			stats.Known = stats.Known && found
			stats.Added += part.Added
			stats.Deleted += part.Deleted
			stats.Binary = stats.Binary || part.Binary
		}
		if e.Untracked {
			part := DiffSummary{}
			if remaining > 0 {
				limit := min(remaining, MaxWorkingCopyBytes)
				section, err := r.untrackedPreview(ctx, e.Path, limit)
				// Binary notices hide the bytes read; conservatively charge
				// the full allowance, as for failed reads.
				if err != nil || section.Kind == WorkingCopyBinary {
					remaining -= limit
				} else {
					remaining -= len(section.Text)
				}
				if err == nil {
					part = sectionSummary(section)
				}
			}
			stats.Known = stats.Known && (part.Known || part.Binary)
			stats.Added += part.Added
			stats.Binary = stats.Binary || part.Binary
		}
		if e.Conflict || e.HeadMode == "160000" || e.IndexMode == "160000" ||
			e.WorktreeMode == "160000" || strings.HasPrefix(e.Submodule, "S") {
			stats.Known = false
		}
		e.Stats = stats
	}
	return ctx.Err()
}

type WorkingCopyOutcome string

const (
	WorkingCopyReady       WorkingCopyOutcome = "ready"
	WorkingCopyOversized   WorkingCopyOutcome = "oversized"
	WorkingCopyUnavailable WorkingCopyOutcome = "unavailable"
)

type WorkingCopySectionKind string

const (
	WorkingCopyPatch       WorkingCopySectionKind = "patch"
	WorkingCopyPreview     WorkingCopySectionKind = "preview"
	WorkingCopyEmpty       WorkingCopySectionKind = "empty"
	WorkingCopyBinary      WorkingCopySectionKind = "binary"
	WorkingCopySymlink     WorkingCopySectionKind = "symlink"
	WorkingCopyUnsupported WorkingCopySectionKind = "unsupported"
	WorkingCopySubmodule   WorkingCopySectionKind = "submodule"
)

// WorkingCopySection contains raw, unstyled text. Labels are Unstaged
// (index -> worktree, including untracked previews) then Staged (HEAD -> index).
// Mode is populated for untracked previews; tracked metadata is in Git's patch.
// A tracked binary is a Patch containing Git's binary notice, not binary data.
type WorkingCopySection struct {
	Label string
	Kind  WorkingCopySectionKind
	Text  string
	Mode  string
}

type WorkingCopyDetails struct {
	Entry    WorkingCopyEntry
	Outcome  WorkingCopyOutcome
	Message  string
	Sections []WorkingCopySection
}

// MaxWorkingCopyBytes bounds the aggregate selected-file text, not each section.
// Oversized/unavailable results have no partial sections to mistake for complete.
const MaxWorkingCopyBytes = 2 * 1024 * 1024

func parseWorkingCopyEntry(record, oldPath string) (WorkingCopyEntry, bool, error) {
	var e WorkingCopyEntry
	malformed := errors.New("Git returned a malformed status entry.")
	if strings.HasPrefix(record, "? ") {
		e.Path, e.Untracked = record[2:], true
		if !validWorkingCopyPath(e.Path) {
			return e, false, malformed
		}
		return e, true, nil
	}
	count := 0
	switch {
	case strings.HasPrefix(record, "1 "):
		count = 9
	case strings.HasPrefix(record, "2 "):
		count = 10
	case strings.HasPrefix(record, "u "):
		count = 11
	default:
		return e, false, nil
	}
	parts := strings.SplitN(record, " ", count)
	if len(parts) != count || len(parts[1]) != 2 || len(parts[2]) != 4 {
		return e, false, malformed
	}
	e.Path = parts[count-1]
	if !validWorkingCopyPath(e.Path) {
		return e, false, malformed
	}
	e.IndexStatus, e.WorktreeStatus, e.Submodule = parts[1][:1], parts[1][1:], parts[2]
	if count == 11 {
		e.Conflict = true
		copy(e.StageModes[:], parts[3:6])
		e.WorktreeMode = parts[6]
		copy(e.StageObjects[:], parts[7:10])
	} else {
		e.HeadMode, e.IndexMode, e.WorktreeMode = parts[3], parts[4], parts[5]
		e.HeadObject, e.IndexObject = parts[6], parts[7]
		if count == 10 {
			if !validWorkingCopyPath(oldPath) {
				return e, false, malformed
			}
			e.OldPath, e.RenameScore = oldPath, parts[8]
		}
	}
	return e, true, nil
}

func validWorkingCopyPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return false
	}
	// With --untracked-files=all Git still reports an embedded repository as
	// "directory/". Retain that raw path, but never traverse its contents.
	for _, part := range strings.Split(strings.TrimSuffix(path, "/"), "/") {
		if part == "" || part == "." || part == ".." || part == ".git" {
			return false
		}
	}
	return true
}

func selectedEntry(s Status, path string) (WorkingCopyEntry, bool) {
	for _, e := range s.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return WorkingCopyEntry{Path: path}, false
}

func changed(code string) bool { return code != "" && code != "." }

// WorkingCopyDetails reads live state without refreshing/writing the index or
// creating a snapshot/lock. It revalidates status, but concurrent edits with the
// same status codes cannot be made atomic; refresh to read again. Only paths
// currently listed by status are accepted. Git errors never expose stderr.
func (r *Repository) WorkingCopyDetails(ctx context.Context, path string) (WorkingCopyDetails, error) {
	if !validWorkingCopyPath(path) {
		return WorkingCopyDetails{}, errors.New("Expected a repository-relative working-copy path.")
	}
	before, err := r.workingCopyStatus(ctx)
	if err != nil {
		return WorkingCopyDetails{}, err
	}
	entry, found := selectedEntry(before, path)
	d := WorkingCopyDetails{Entry: entry, Outcome: WorkingCopyReady}
	unavailable := func() (WorkingCopyDetails, error) {
		d.Outcome, d.Message, d.Sections = WorkingCopyUnavailable, "Selected file changed or is unavailable. Refresh to read it again.", nil
		return d, nil
	}
	if !found {
		return unavailable()
	}
	remaining := MaxWorkingCopyBytes
	for _, label := range []string{"Unstaged", "Staged"} {
		code := entry.WorktreeStatus
		if label == "Staged" {
			code = entry.IndexStatus
		}
		if !changed(code) && !(label == "Unstaged" && entry.Untracked) {
			continue
		}
		section := WorkingCopySection{Label: label}
		switch {
		case entry.Conflict:
			section.Kind, section.Text = WorkingCopyUnsupported, "Conflict diff is unsupported. Resolve or inspect the unmerged stages with Git."
		case label == "Unstaged" && entry.Untracked:
			section, err = r.untrackedPreview(ctx, path, remaining)
			section.Label = label
		case entry.Submodule != "" && entry.Submodule != "N..." ||
			entry.HeadMode == "160000" || entry.IndexMode == "160000" || entry.WorktreeMode == "160000":
			section.Kind = WorkingCopySubmodule
			section.Text = fmt.Sprintf("Submodule: %s; index %s, worktree %s; HEAD %s, index %s. Nested file diffs are not shown.",
				entry.Submodule, entry.IndexStatus, entry.WorktreeStatus, entry.HeadObject, entry.IndexObject)
		default:
			section.Kind = WorkingCopyPatch
			if remaining == 0 {
				err = errCaptureTooLarge
				break
			}
			args := []string{"--no-literal-pathspecs", "diff", "--patch", "--no-color", "--no-ext-diff", "--no-textconv",
				"--unified=3", "--inter-hunk-context=0", "--find-renames", "--no-relative",
				"--src-prefix=a/", "--dst-prefix=b/", "--ignore-submodules=none", "--submodule=short"}
			if label == "Staged" {
				// No explicit HEAD: --cached also works on an unborn branch.
				args = append(args, "--cached")
			}
			args = append(args, "--", exactWorkingCopyPathspec(entry.Path))
			if entry.OldPath != "" && (code == "R" || code == "C") {
				args = append(args, exactWorkingCopyPathspec(entry.OldPath))
			}
			var exit int
			section.Text, exit, err = executeCapture(ctx, r.root, nil, "", remaining, args...)
			if err == nil && (exit != 0 || section.Text == "") {
				return unavailable()
			}
		}
		if errors.Is(err, errCaptureTooLarge) || len(section.Text) > remaining {
			d.Outcome, d.Message, d.Sections = WorkingCopyOversized, errCaptureTooLarge.Error(), nil
			return d, nil
		}
		if errors.Is(err, errPreviewUnavailable) {
			return unavailable()
		}
		if err != nil {
			return WorkingCopyDetails{}, err
		}
		remaining -= len(section.Text)
		d.Sections = append(d.Sections, section)
	}
	after, err := r.workingCopyStatus(ctx)
	if err != nil {
		return WorkingCopyDetails{}, err
	}
	current, found := selectedEntry(after, path)
	if !found || before.Head != after.Head || entry != current {
		return unavailable()
	}
	return d, nil
}

// Literal pathspecs also match descendants. Escape the first byte even in plain
// names to force full-path glob matching, and quote all glob metacharacters.
// Git must exclude descendants before capture so unrelated large patches cannot
// exhaust the selected-file limit. Paths remain raw bytes, including newlines.
func exactWorkingCopyPathspec(path string) string {
	var pattern strings.Builder
	pattern.WriteString(":(top,glob)")
	for i := 0; i < len(path); i++ {
		if i == 0 || strings.ContainsRune(`\*?[`, rune(path[i])) {
			pattern.WriteByte('\\')
		}
		pattern.WriteByte(path[i])
	}
	return pattern.String()
}

var errPreviewUnavailable = errors.New("Untracked preview changed or is unavailable.")

// Every directory component is opened without following symlinks. A final
// regular file is opened nonblocking and rechecked before reading, so replacement
// with a FIFO/device cannot block or read an external symlink target.
func (r *Repository) untrackedPreview(ctx context.Context, path string, limit int) (WorkingCopySection, error) {
	section := WorkingCopySection{Kind: WorkingCopyPreview}
	if err := ctx.Err(); err != nil {
		return section, err
	}
	fd, err := unix.Open(r.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return section, errPreviewUnavailable
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return section, errPreviewUnavailable
		}
		_ = unix.Close(fd)
		fd = next
	}
	name := parts[len(parts)-1]
	var before unix.Stat_t
	if unix.Fstatat(fd, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return section, errPreviewUnavailable
	}
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		target := make([]byte, limit+1)
		n, err := unix.Readlinkat(fd, name, target)
		if err != nil {
			return section, errPreviewUnavailable
		}
		if n > limit {
			return section, errCaptureTooLarge
		}
		section.Kind, section.Mode, section.Text = WorkingCopySymlink, "120000", "Symlink target: "+string(target[:n])
	case unix.S_IFREG:
		if before.Size > int64(limit) {
			return section, errCaptureTooLarge
		}
		fileFD, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return section, errPreviewUnavailable
		}
		file := os.NewFile(uintptr(fileFD), name)
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return section, errPreviewUnavailable
		}
		var opened unix.Stat_t
		if unix.Fstat(fileFD, &opened) != nil || !samePreviewFile(before, opened) {
			return section, errPreviewUnavailable
		}
		content, err := io.ReadAll(io.LimitReader(contextPreviewReader{ctx, file}, int64(limit)+1))
		if err != nil {
			if ctx.Err() != nil {
				return section, ctx.Err()
			}
			return section, errPreviewUnavailable
		}
		if len(content) > limit {
			return section, errCaptureTooLarge
		}
		end, err := file.Stat()
		if err != nil || info.Size() != end.Size() || !info.ModTime().Equal(end.ModTime()) || info.Mode() != end.Mode() {
			return section, errPreviewUnavailable
		}
		section.Mode = "100644"
		if info.Mode()&0111 != 0 {
			section.Mode = "100755"
		}
		section.Text = string(content)
		if len(content) == 0 {
			section.Kind, section.Text = WorkingCopyEmpty, "Empty untracked file."
		} else if strings.ContainsRune(string(content[:min(len(content), 8000)]), 0) {
			section.Kind, section.Text = WorkingCopyBinary, "Binary untracked file; content is not previewed."
		}
	default:
		section.Kind, section.Text = WorkingCopyUnsupported, "Directory or special file; only regular files and symlink target metadata can be previewed."
	}
	var after unix.Stat_t
	if unix.Fstatat(fd, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !samePreviewFile(before, after) {
		return section, errPreviewUnavailable
	}
	if err := ctx.Err(); err != nil {
		return section, err
	}
	return section, nil
}

type contextPreviewReader struct {
	ctx context.Context
	io.Reader
}

func (r contextPreviewReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p[:min(len(p), 32*1024)])
}

func samePreviewFile(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Size == b.Size
}
