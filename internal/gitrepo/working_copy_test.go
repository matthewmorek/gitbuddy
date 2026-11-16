package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func workingDetailsOf(t *testing.T, r *Repository, path string) WorkingCopyDetails {
	t.Helper()
	d, err := r.WorkingCopyDetails(testContext, path)
	mustOK(t, err)
	return d
}

func TestWorkingCopyStatusRetainsRecordsAndConservativeDirty(t *testing.T) {
	ordinary := "1 D. N... 100644 000000 000000 head index same\x00"
	s, err := parseStatus(ordinary + "? same\x00")
	mustOK(t, err)
	equal(t, len(s.Entries), 1)
	equal(t, s.Entries[0].Path, "same")
	equal(t, s.Entries[0].IndexStatus, "D")
	equal(t, s.Entries[0].Untracked, true)
	reversed, err := parseStatus("? same\x00" + ordinary)
	mustOK(t, err)
	equal(t, reversed, s)

	s, err = parseStatus("u UU N... 100644 100755 120000 100644 base ours theirs conflicted\x00" +
		"2 RM N... 100644 100644 100644 old new R100 renamed\n\t\x1b\x00# branch.head not-a-header\x00" +
		"1 .M S.MU 160000 160000 160000 old new submodule\x00")
	mustOK(t, err)
	equal(t, s.Dirty, true)
	equal(t, s.Branch, "")
	equal(t, s.Entries[0].Conflict, true)
	equal(t, s.Entries[0].StageModes, [3]string{"100644", "100755", "120000"})
	equal(t, s.Entries[0].StageObjects, [3]string{"base", "ours", "theirs"})
	equal(t, s.Entries[1].OldPath, "# branch.head not-a-header")
	equal(t, s.Entries[1].Path, "renamed\n\t\x1b")
	equal(t, s.Entries[1].RenameScore, "R100")
	equal(t, s.Entries[2].Submodule, "S.MU")
	for _, record := range []string{"future record\x00", "! ignored\x00", "\x00"} {
		s, err := parseStatus(record)
		mustOK(t, err)
		equal(t, s.Dirty, true)
	}
	for _, record := range []string{"1 incomplete\x00", "2 incomplete\x00", "? ../outside\x00", "u incomplete\x00", "? incomplete"} {
		_, err := parseStatus(record)
		if err == nil {
			t.Fatalf("accepted malformed status %q", record)
		}
	}
}

func TestWorkingCopyMixedChangesAreSeparateAndReadOnly(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	name := ":(glob)*[x]\n\t\x1b.txt"
	base := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n" +
		strings.Repeat("padding\n", 9) + "nineteen\ntwenty\ntwenty-one\ntwenty-two\n"
	f.write(path, name, base)
	f.git(path, "--literal-pathspecs", "add", "--", name)
	f.git(path, "commit", "-m", "base")
	staged := strings.Replace(base, "five", "STAGED", 1)
	staged = strings.Replace(staged, "nineteen", "SECOND", 1)
	f.write(path, name, staged)
	f.git(path, "--literal-pathspecs", "add", "--", name)
	f.write(path, name, base) // HEAD == worktree; a HEAD->worktree diff would lose both.
	f.write(path, "unrelated", "must not appear")

	helper := filepath.Join(f.root, "forbidden-helper")
	marker := filepath.Join(f.root, "helper-ran")
	mustOK(t, os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0700))
	f.git(path, "config", "diff.external", helper)
	f.git(path, "config", "diff.hostile.textconv", helper)
	f.git(path, "config", "diff.interHunkContext", "100")
	f.write(path, ".gitattributes", "* diff=hostile\n")
	indexPath := filepath.Join(path, ".git", "index")
	index, err := os.ReadFile(indexPath)
	mustOK(t, err)
	info, err := os.Stat(indexPath)
	mustOK(t, err)
	head := f.git(path, "rev-parse", "HEAD")
	r := openRepo(t, path)
	e := statusOf(t, r).Entries[0]
	// Selection is by raw pathname, independent of the status row's sort position.
	for _, candidate := range statusOf(t, r).Entries {
		if candidate.Path == name {
			e = candidate
		}
	}
	equal(t, e.IndexStatus, "M")
	equal(t, e.WorktreeStatus, "M")
	equal(t, e.Stats, LineStats{Known: true, Added: 4, Deleted: 4})
	d := workingDetailsOf(t, r, name)
	equal(t, d.Summary(), DiffSummary{LineStats: LineStats{Known: true, Added: 4, Deleted: 4}, Chunks: 4})
	equal(t, d.Outcome, WorkingCopyReady)
	equal(t, len(d.Sections), 2)
	equal(t, d.Sections[0].Label, "Unstaged")
	equal(t, d.Sections[1].Label, "Staged")
	for i, want := range []string{"-STAGED\n+five\n", "-five\n+STAGED\n"} {
		if !strings.Contains(d.Sections[i].Text, want) || !strings.Contains(d.Sections[i].Text, "@@ -2,7 +2,7 @@") {
			t.Fatalf("wrong comparison/context: %q", d.Sections[i].Text)
		}
		equal(t, strings.Count(d.Sections[i].Text, "\n@@ "), 2)
		if strings.Contains(d.Sections[i].Text, "unrelated") {
			t.Fatal("literal selected path matched another file")
		}
	}
	after, err := os.ReadFile(indexPath)
	mustOK(t, err)
	equal(t, string(after), string(index))
	afterInfo, err := os.Stat(indexPath)
	mustOK(t, err)
	equal(t, afterInfo.ModTime(), info.ModTime())
	equal(t, f.git(path, "rev-parse", "HEAD"), head)
	worktree, err := os.ReadFile(filepath.Join(path, name))
	mustOK(t, err)
	equal(t, string(worktree), base)
	_, err = os.Stat(indexPath + ".lock")
	equal(t, os.IsNotExist(err), true)
	_, err = os.Stat(marker)
	equal(t, os.IsNotExist(err), true)
}

func TestWorkingCopyUnbornRenameDeletionAndMetadata(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	r := openRepo(t, path)
	f.write(path, "new", "staged\n")
	f.git(path, "add", "--", "new")
	f.write(path, "new", "unstaged\n")
	d := workingDetailsOf(t, r, "new")
	equal(t, statusOf(t, r).Entries[0].Stats, LineStats{Known: true, Added: 2, Deleted: 1})
	equal(t, d.Outcome, WorkingCopyReady)
	equal(t, len(d.Sections), 2)
	if !strings.Contains(d.Sections[1].Text, "new file mode 100644") || !strings.Contains(d.Sections[1].Text, "+staged\n") {
		t.Fatalf("unborn staged addition missing: %#v", d)
	}
	f.git(path, "commit", "-m", "base")
	f.write(path, "new", "staged\n")
	mustOK(t, os.Rename(filepath.Join(path, "new"), filepath.Join(path, "-renamed")))
	f.git(path, "add", "--all")
	d = workingDetailsOf(t, r, "-renamed")
	equal(t, statusOf(t, r).Entries[0].Stats, LineStats{Known: true})
	equal(t, d.Entry.OldPath, "new")
	// The staged rename includes both old and new literal paths.
	if !strings.Contains(d.Sections[0].Text, "rename from new\nrename to -renamed\n") {
		t.Fatalf("rename metadata missing: %#v", d)
	}
	f.write(path, "-renamed", "edited\n")
	d = workingDetailsOf(t, r, "-renamed")
	equal(t, len(d.Sections), 2)
	equal(t, statusOf(t, r).Entries[0].Stats, LineStats{Known: true, Added: 1, Deleted: 1})
	if !strings.Contains(d.Sections[0].Text, "-staged\n+edited\n") ||
		!strings.Contains(d.Sections[1].Text, "rename from new\nrename to -renamed\n") {
		t.Fatalf("mixed rename lost one comparison: %#v", d)
	}
	f.git(path, "commit", "-m", "rename")
	mustOK(t, os.Chmod(filepath.Join(path, "-renamed"), 0700))
	d = workingDetailsOf(t, r, "-renamed")
	if !strings.Contains(d.Sections[0].Text, "old mode 100644\nnew mode 100755") {
		t.Fatalf("mode metadata missing: %#v", d)
	}
	mustOK(t, os.Remove(filepath.Join(path, "-renamed")))
	d = workingDetailsOf(t, r, "-renamed")
	if !strings.Contains(d.Sections[0].Text, "deleted file mode") {
		t.Fatalf("unstaged deletion missing: %#v", d)
	}
	f.git(path, "add", "--all")
	f.write(path, "-renamed", "recreated\n")
	d = workingDetailsOf(t, r, "-renamed")
	equal(t, len(statusOf(t, r).Entries), 1)
	equal(t, d.Entry.Untracked, true)
	equal(t, len(d.Sections), 2)
	equal(t, d.Sections[0].Kind, WorkingCopyPreview)
	equal(t, d.Sections[0].Text, "recreated\n")
	equal(t, statusOf(t, r).Entries[0].Stats, LineStats{Known: true, Added: 1, Deleted: 1})
	if !strings.Contains(d.Sections[1].Text, "deleted file mode") {
		t.Fatalf("staged deletion lost: %#v", d)
	}
	// A tracked symlink uses Git metadata/content, never its external target.
	mustOK(t, os.Symlink(filepath.Join(f.root, "external"), filepath.Join(path, "link")))
	f.git(path, "add", "--", "link")
	d = workingDetailsOf(t, r, "link")
	if !strings.Contains(d.Sections[0].Text, "new file mode 120000") {
		t.Fatalf("symlink mode missing: %#v", d)
	}
	f.write(path, "binary", "old\x00binary")
	f.git(path, "add", "--", "binary")
	d = workingDetailsOf(t, r, "binary")
	equal(t, d.Summary().Known, false)
	equal(t, d.Summary().Binary, true)
	if !strings.Contains(d.Sections[0].Text, "Binary files ") {
		t.Fatalf("binary notice missing: %#v", d)
	}
}

func TestWorkingCopyStatsCountsAndUnavailableAreTruthful(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	f.commit(path, "tracked", "base\n", "base")
	f.write(path, "tracked", "changed\n")
	for name, text := range map[string]string{
		"no-newline": "one\ntwo", "newline": "one\ntwo\n", "empty": "",
		"binary": "one\x00two", "oversized": strings.Repeat("x", MaxWorkingCopyBytes+1),
	} {
		f.write(path, name, text)
	}
	r := openRepo(t, path)
	s := statusOf(t, r)
	for _, tc := range []struct {
		name  string
		stats LineStats
	}{
		{"tracked", LineStats{Known: true, Added: 1, Deleted: 1}},
		{"no-newline", LineStats{Known: true, Added: 2}},
		{"newline", LineStats{Known: true, Added: 2}},
		{"empty", LineStats{Known: true}},
		{"binary", LineStats{Known: true, Binary: true}},
		{"oversized", LineStats{}},
	} {
		entry, found := selectedEntry(s, tc.name)
		equal(t, found, true)
		equal(t, entry.Stats, tc.stats)
	}
	equal(t, workingDetailsOf(t, r, "no-newline").Summary(),
		DiffSummary{LineStats: LineStats{Known: true, Added: 2}, Chunks: 1})
	equal(t, workingDetailsOf(t, r, "empty").Summary(), DiffSummary{LineStats: LineStats{Known: true}})
	// An ancillary diff failure must not discard status or invent zero counts.
	realGit, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	wrapper := "#!/bin/sh\nfor argument do\n" +
		"  [ \"$argument\" != \"--numstat\" ] || exit 1\n" +
		"done\nexec '" + strings.ReplaceAll(realGit, "'", "'\\''") + "' \"$@\"\n"
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(wrapper), 0700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	s = statusOf(t, r)
	equal(t, s.Dirty, true)
	equal(t, len(s.Entries), 6)
	equal(t, s.StatsWarning != "", true)
	entry, _ := selectedEntry(s, "tracked")
	equal(t, entry.Stats.Known, false)
	entry, _ = selectedEntry(s, "no-newline")
	equal(t, entry.Stats, LineStats{Known: true, Added: 2})
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	_, err = r.Status(ctx)
	equal(t, errors.Is(err, context.Canceled), true)
}

func TestWorkingCopyStatsUntrackedBinaryBudget(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	names := []string{"binary-1", "binary-2", "binary-3", "binary-4", "binary-5"}
	content := strings.Repeat("\x00", MaxWorkingCopyBytes)
	for _, name := range names {
		f.write(path, name, content)
	}
	f.write(path, "text-after-binaries", "one\ntwo\n")
	names = append(names, "text-after-binaries")

	s := statusOf(t, openRepo(t, path))
	equal(t, len(s.Entries), len(names))
	for i, name := range names {
		entry, found := selectedEntry(s, name)
		equal(t, found, true)
		want := LineStats{}
		if i < 4 {
			want = LineStats{Known: true, Binary: true}
		}
		// Four cap-sized reads exhaust 8 MiB despite their short binary notices.
		// All later rows, including plain text, must remain unknown.
		if entry.Stats != want {
			t.Errorf("%s: stats = %+v, want %+v", name, entry.Stats, want)
		}
	}
}

func TestWorkingCopyPatchExcludesSelectedPathDescendants(t *testing.T) {
	for _, tc := range []struct {
		name, oldPath, newPath string
	}{
		{"deleted file replaced by directory", "config", ""},
		{"rename source reused as directory", ":(glob)*[x]?\\\n", ":(literal)new*[y]?\\\n"},
		{"rename into source directory", "é-config", "é-config/nested/renamed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			path := f.repo("repo", false)
			f.write(path, tc.oldPath, "selected content\n")
			f.git(path, "--literal-pathspecs", "add", "--", tc.oldPath)
			f.git(path, "commit", "-m", "base")
			mustOK(t, os.Remove(filepath.Join(path, tc.oldPath)))
			mustOK(t, os.MkdirAll(filepath.Join(path, tc.oldPath, "nested"), 0700))
			selected := tc.oldPath
			if tc.newPath != "" {
				f.write(path, tc.newPath, "selected content\n")
				selected = tc.newPath
			}
			r := openRepo(t, path)
			for _, size := range []struct {
				name, content string
			}{
				{"small descendant", "unrelated descendant\n"},
				{"oversized descendant", "unrelated descendant\n" + strings.Repeat("x", MaxWorkingCopyBytes+1)},
			} {
				t.Run(size.name, func(t *testing.T) {
					f.write(path, filepath.Join(tc.oldPath, "nested", "settings"), size.content)
					f.git(path, "add", "--all")
					d := workingDetailsOf(t, r, selected)
					equal(t, d.Outcome, WorkingCopyReady)
					equal(t, len(d.Sections), 1)
					equal(t, d.Sections[0].Label, "Staged")
					patch := d.Sections[0].Text
					equal(t, strings.Count(patch, "diff --git "), 1)
					if strings.Contains(patch, "unrelated descendant") {
						t.Fatal("selected-file patch included a descendant")
					}
					if tc.newPath == "" {
						equal(t, d.Entry.IndexStatus, "D")
						if !strings.Contains(patch, "-selected content\n") {
							t.Fatalf("selected deletion missing: %q", patch)
						}
					} else {
						equal(t, d.Entry.IndexStatus, "R")
						equal(t, d.Entry.OldPath, tc.oldPath)
						if !strings.Contains(patch, "similarity index 100%\nrename from ") ||
							!strings.Contains(patch, "\nrename to ") {
							t.Fatalf("selected rename missing: %q", patch)
						}
					}
				})
			}
		})
	}
}

func TestWorkingCopyUntrackedPreviewSafety(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	r := openRepo(t, path)
	for _, tc := range []struct {
		name, content string
		kind          WorkingCopySectionKind
		text          string
	}{
		{"empty", "", WorkingCopyEmpty, "Empty untracked file."},
		{"binary", "private\x00binary", WorkingCopyBinary, "Binary untracked file; content is not previewed."},
		{":(glob)*\n\t\x1b", "raw\ncontent", WorkingCopyPreview, "raw\ncontent"},
	} {
		f.write(path, tc.name, tc.content)
		d := workingDetailsOf(t, r, tc.name)
		equal(t, d.Outcome, WorkingCopyReady)
		equal(t, d.Sections, []WorkingCopySection{{Label: "Unstaged", Kind: tc.kind, Text: tc.text, Mode: "100644"}})
	}
	external := filepath.Join(f.root, "outside")
	mustOK(t, os.Mkdir(external, 0700))
	f.write(external, "secret", "NEVER READ")
	target := filepath.Join(external, "secret")
	mustOK(t, os.Symlink(target, filepath.Join(path, "link")))
	d := workingDetailsOf(t, r, "link")
	equal(t, d.Sections[0].Kind, WorkingCopySymlink)
	equal(t, d.Sections[0].Text, "Symlink target: "+target)
	mustOK(t, os.Symlink(external, filepath.Join(path, "escape")))
	// This direct boundary check also covers stale untracked rows after a
	// directory was replaced by a symlink; neither internal nor external
	// symlink directories are traversed.
	_, err := r.untrackedPreview(testContext, "escape/secret", MaxWorkingCopyBytes)
	equal(t, errors.Is(err, errPreviewUnavailable), true)
	mustOK(t, unix.Mkfifo(filepath.Join(path, "fifo"), 0600))
	ctx, cancel := context.WithTimeout(testContext, time.Second)
	defer cancel()
	section, err := r.untrackedPreview(ctx, "fifo", MaxWorkingCopyBytes)
	mustOK(t, err)
	equal(t, section.Kind, WorkingCopyUnsupported)
	embedded := filepath.Join(path, "embedded")
	mustOK(t, os.Mkdir(embedded, 0700))
	f.git(embedded, "init", "--initial-branch=main")
	f.commit(embedded, "file", "nested content", "nested")
	d = workingDetailsOf(t, r, "embedded/")
	equal(t, d.Outcome, WorkingCopyReady)
	equal(t, d.Entry.Path, "embedded/")
	equal(t, d.Sections[0].Kind, WorkingCopyUnsupported)
	equal(t, workingDetailsOf(t, r, "missing").Outcome, WorkingCopyUnavailable)
	mustOK(t, os.Remove(filepath.Join(path, "empty")))
	equal(t, workingDetailsOf(t, r, "empty").Outcome, WorkingCopyUnavailable)
	for _, name := range []string{"../outside/secret", "/absolute", ".git/config", "a/../empty"} {
		_, err := r.WorkingCopyDetails(testContext, name)
		wantError(t, err, "repository-relative")
	}
}

func TestWorkingCopyOversizedIsNotPartial(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	r := openRepo(t, path)
	f.write(path, "untracked", strings.Repeat("x", MaxWorkingCopyBytes+1))
	d := workingDetailsOf(t, r, "untracked")
	equal(t, d.Outcome, WorkingCopyOversized)
	equal(t, len(d.Sections), 0)
	f.commit(path, "tracked", "base\n", "base")
	f.write(path, "tracked", strings.Repeat("x", MaxWorkingCopyBytes+1))
	d = workingDetailsOf(t, r, "tracked")
	equal(t, d.Outcome, WorkingCopyOversized)
	equal(t, len(d.Sections), 0)
	// Each diff fits individually, but the combined selected-file output does not.
	f.write(path, "tracked", strings.Repeat("a", 600000)+"\n")
	f.git(path, "add", "--", "tracked")
	f.git(path, "commit", "-m", "large base")
	f.write(path, "tracked", strings.Repeat("b", 600000)+"\n")
	f.git(path, "add", "--", "tracked")
	f.write(path, "tracked", strings.Repeat("c", 600000)+"\n")
	d = workingDetailsOf(t, r, "tracked")
	equal(t, d.Outcome, WorkingCopyOversized)
	equal(t, len(d.Sections), 0)
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	_, err := r.WorkingCopyDetails(ctx, "tracked")
	equal(t, errors.Is(err, context.Canceled), true)
}

func TestWorkingCopyConflictAndSubmoduleSummaries(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	f.write(path, "other", "old\n")
	f.git(path, "add", "--", "other")
	base := f.commit(path, "conflict", "base\n", "base")
	f.git(path, "switch", "-c", "side")
	f.commit(path, "conflict", "side\n", "side")
	f.git(path, "switch", "main")
	f.commit(path, "conflict", "main\n", "main")
	// The fixture helper expects success; merge conflicts intentionally exit 1.
	_, exit, err := execute(testContext, path, nil, "", "merge", "--no-edit", "side")
	mustOK(t, err)
	equal(t, exit, 1)
	r := openRepo(t, path)
	f.write(path, "other", "new\n")
	status := statusOf(t, r)
	other, _ := selectedEntry(status, "other")
	equal(t, other.Stats, LineStats{Known: true, Added: 1, Deleted: 1})
	conflict, _ := selectedEntry(status, "conflict")
	equal(t, conflict.Stats.Known, false)
	d := workingDetailsOf(t, r, "conflict")
	equal(t, d.Entry.Conflict, true)
	equal(t, len(d.Sections), 2)
	for _, section := range d.Sections {
		equal(t, section.Kind, WorkingCopyUnsupported)
	}
	// An embedded fixture repository stages a gitlink, with no remote/network.
	child := filepath.Join(path, "child")
	mustOK(t, os.Mkdir(child, 0700))
	f.git(child, "init", "--initial-branch=main")
	f.commit(child, "file", "child\n", "child")
	f.git(path, "add", "--", "child")
	d = workingDetailsOf(t, r, "child")
	equal(t, len(d.Sections), 1)
	equal(t, d.Entry.IndexMode, "160000")
	equal(t, d.Sections[0].Kind, WorkingCopySubmodule)
	if !strings.Contains(d.Sections[0].Text, "Nested file diffs are not shown.") {
		t.Fatal("missing submodule limitation")
	}
	equal(t, f.git(path, "rev-parse", "main~1"), base)
}

func TestWorkingCopyDeletedGitlinkRemainsSubmoduleSummary(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	child := filepath.Join(path, "child")
	mustOK(t, os.Mkdir(child, 0700))
	f.git(child, "init", "--initial-branch=main")
	f.commit(child, "file", "child\n", "child")
	f.git(path, "add", "--", "child")
	f.git(path, "commit", "-m", "gitlink")
	f.git(path, "update-index", "--force-remove", "--", "child")
	mustOK(t, os.RemoveAll(child))
	d := workingDetailsOf(t, openRepo(t, path), "child")
	equal(t, d.Outcome, WorkingCopyReady)
	equal(t, d.Entry.HeadMode, "160000")
	equal(t, d.Entry.IndexStatus, "D")
	equal(t, d.Sections[0].Kind, WorkingCopySubmodule)
	f.write(path, "child", "recreated regular file\n")
	d = workingDetailsOf(t, openRepo(t, path), "child")
	equal(t, d.Entry.Untracked, true)
	equal(t, len(d.Sections), 2)
	equal(t, d.Sections[0].Kind, WorkingCopyPreview)
	equal(t, d.Sections[0].Text, "recreated regular file\n")
	equal(t, d.Sections[1].Kind, WorkingCopySubmodule)
}
