package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func assertWorktreeFile(t *testing.T, dir, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	mustOK(t, err)
	equal(t, string(data), want)
}

func TestSafePullPreservesIndexSplitAndPrunesOnlyUpstreamTracking(t *testing.T) {
	f := newFixture(t)
	local, writer, remote := f.paired()
	r := openRepo(t, local)
	const tail = "two\nthree\nfour\nfive\n"
	f.commit(writer, "base", "one\n"+tail+"six\n", "track multiple lines")
	base := f.commit(writer, "removed", "old\n", "track a file to delete")
	f.git(writer, "push", "origin", "main")
	_, err := r.SafePull(testContext, false)
	mustOK(t, err)
	f.write(local, "earlier", "prior work")
	mustOK(t, r.StashAll(testContext, "my earlier stash"))
	prior := f.git(local, "stash", "list", "--format=%H %gd %gs")
	priorRows, err := r.Stashes(testContext)
	mustOK(t, err)
	f.git(local, "tag", "keep-local")
	f.git(writer, "tag", "remote-tag")
	f.git(writer, "push", "origin", "refs/tags/remote-tag")
	f.git(local, "branch", "keep-branch")
	f.git(local, "remote", "add", "other", remote)
	f.git(local, "update-ref", "refs/remotes/other/stale", base)
	f.git(local, "update-ref", "refs/remotes/origin/stale", base)
	for _, key := range []string{"fetch.all", "fetch.pruneTags", "remote.origin.pruneTags", "merge.autoStash"} {
		f.git(local, "config", key, "true")
	}
	f.git(local, "config", "remote.origin.tagOpt", "--tags")
	f.git(local, "config", "branch.main.mergeOptions", "--squash")
	f.git(local, "config", "apply.whitespace", "fix")
	mustOK(t, os.WriteFile(filepath.Join(local, ".git", "info", "exclude"), []byte("ignored\n"), 0600))
	f.write(local, "ignored", "leave me alone")
	f.write(local, "base", "staged \t\n"+tail+"six\n")
	f.write(local, "staged\tfile", "added \t\n")
	f.git(local, "add", "--", "base", "staged\tfile")
	f.git(local, "rm", "removed")
	f.write(local, "base", "unstaged  \n"+tail+"six\n")
	f.write(local, "loose\tfile", "not staged\n")
	f.commit(writer, "base", "one\n"+tail+"SIX\n", "edit the other end of base")
	target := f.commit(writer, "incoming", "fetched\n", "advance upstream elsewhere")
	f.git(writer, "push", "origin", "main")

	id, err := r.SafePull(testContext, true)
	mustOK(t, err)
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD"), target)
	staged, err := r.git(testContext, "", "show", ":base")
	mustOK(t, err)
	equal(t, staged, "staged \t\n"+tail+"SIX\n")
	added, err := r.git(testContext, "", "show", ":staged\tfile")
	mustOK(t, err)
	equal(t, added, "added \t\n")
	equal(t, f.git(local, "diff", "--cached", "--name-status", "--no-renames", "-z"), "M\x00base\x00D\x00removed\x00A\x00staged\tfile\x00")
	assertWorktreeFile(t, local, "base", "unstaged  \n"+tail+"SIX\n")
	assertWorktreeFile(t, local, "staged\tfile", "added \t\n")
	_, err = os.Lstat(filepath.Join(local, "removed"))
	if !os.IsNotExist(err) {
		t.Fatalf("staged deletion wasn't restored to the worktree: %v", err)
	}
	assertWorktreeFile(t, local, "loose\tfile", "not staged\n")
	assertWorktreeFile(t, local, "ignored", "leave me alone")
	equal(t, f.git(local, "ls-files", "loose\tfile", "ignored"), "")
	equal(t, f.git(local, "for-each-ref", "--format=%(refname)", "refs/tags", "refs/remotes", "refs/heads"),
		"refs/heads/keep-branch\nrefs/heads/main\nrefs/remotes/origin/HEAD\nrefs/remotes/origin/main\nrefs/remotes/other/stale\nrefs/tags/keep-local")
	equal(t, f.git(local, "rev-parse", "keep-branch", "refs/remotes/other/stale", "keep-local"), base+"\n"+base+"\n"+base)
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	if !slices.EqualFunc(rows, priorRows, Stash.SameEntry) {
		t.Fatalf("Safe pull changed saved entries: %+v", rows)
	}
	equal(t, f.git(local, "stash", "list", "--format=%H %gd %gs"), prior)
	equal(t, f.git(local, "for-each-ref", "--format=%(refname)", safeBackupRefs), "")
}

func TestSafePullRefusalRestoresDivergedWorktree(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.write(local, "earlier", "prior work")
	mustOK(t, r.StashAll(testContext, "my earlier stash"))
	prior := f.git(local, "stash", "list", "--format=%H %gd %gs")
	priorRows, err := r.Stashes(testContext)
	mustOK(t, err)
	localTip := f.commit(local, "local", "local", "diverge locally")
	target := f.commit(writer, "remote", "remote", "diverge upstream")
	f.git(writer, "push", "origin", "main")
	f.write(local, "base", "staged")
	f.git(local, "add", "base")
	f.write(local, "base", "unstaged")
	f.write(local, "loose", "leave untracked")
	id, err := r.SafePull(testContext, true)
	wantError(t, err, "fast-forward refused")
	wantError(t, err, "Local changes were restored")
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD"), localTip)
	equal(t, f.git(local, "rev-parse", "origin/main"), target)
	equal(t, f.git(local, "show", ":base"), "staged")
	assertWorktreeFile(t, local, "base", "unstaged")
	assertWorktreeFile(t, local, "loose", "leave untracked")
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	if !slices.EqualFunc(rows, priorRows, Stash.SameEntry) {
		t.Fatalf("Safe pull changed saved entries: %+v", rows)
	}
	equal(t, f.git(local, "stash", "list", "--format=%H %gd %gs"), prior)
	equal(t, f.git(local, "for-each-ref", "--format=%(refname)", safeBackupRefs), "")
}

func TestSafePullKeepsBackupWhenRestoredIndexDoesNotMatch(t *testing.T) {
	f := newFixture(t)
	local, _, _ := f.paired()
	r := openRepo(t, local)
	f.write(local, "base", "staged\n")
	f.git(local, "add", "base")
	f.write(local, "base", "unstaged\n")

	// Change only the index after Git restores both versions.
	interposeSafePullGit(t, f, `#!/bin/sh
case " $* " in
  *" stash apply --index "*)
    "$REAL_GIT" "$@" && "$REAL_GIT" add -- base
    exit "$?"
    ;;
esac
exec "$REAL_GIT" "$@"
`)
	id, err := r.SafePull(testContext, true)
	assertWorktreeFile(t, local, "base", "unstaged\n")
	equal(t, f.git(local, "show", ":base"), "unstaged")
	wantError(t, err, "Could not verify the staged contents")
	wantError(t, err, id)
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
	equal(t, f.git(local, "show", id+"^2:base"), "staged")
}

func TestSafePullRestoreConflictKeepsNewHeadAndBackup(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	f.write(local, "staged", "staged change\n")
	f.git(local, "add", "staged")
	f.write(local, "staged", "then unstaged\n")
	f.write(local, "base", "unstaged local change\n")
	f.write(local, "loose", "untracked local change\n")
	target := f.commit(writer, "base", "upstream change\n", "conflicting upstream")
	f.git(writer, "push", "origin", "main")
	id, err := r.SafePull(testContext, true)
	if id == "" {
		t.Fatal("restore failure lost the backup identity")
	}
	wantError(t, err, "Fast-forward-only integration finished")
	wantError(t, err, "Changes may have applied partially")
	wantError(t, err, id)
	equal(t, f.git(local, "rev-parse", "HEAD"), target)
	equal(t, f.git(local, "stash", "list"), "")
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
	if f.git(local, "ls-files", "-u", "base") == "" {
		t.Fatal("restore conflict was destructively cleaned up")
	}
	recovery := filepath.Join(f.root, "recovery")
	f.git(local, "worktree", "add", "--detach", recovery, base)
	recoverRepo := openRepo(t, recovery)
	rows, err := recoverRepo.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	equal(t, rows[0].ID, id)
	mustOK(t, recoverRepo.ApplyStash(testContext, rows[0]))
	equal(t, f.git(recovery, "show", ":staged"), "staged change")
	assertWorktreeFile(t, recovery, "staged", "then unstaged\n")
	assertWorktreeFile(t, recovery, "base", "unstaged local change\n")
	assertWorktreeFile(t, recovery, "loose", "untracked local change\n")
	equal(t, f.git(recovery, "ls-files", "loose"), "")
	equal(t, f.git(recovery, "rev-parse", safeBackupRefs+id), id)
}

func TestSafePullRejectsDirtyCleanOnlyAndUnsafeConfigurationBeforeMutating(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	old := f.git(local, "rev-parse", "HEAD")
	target := f.commit(writer, "incoming", "incoming", "advance")
	f.git(writer, "push", "origin", "main")
	f.write(local, "base", "dirty")
	f.git(local, "add", "base")
	f.write(local, "loose", "untouched")
	_, err := r.SafePull(testContext, false)
	wantError(t, err, "automatic stashing is off")
	want := "+refs/heads/*:refs/remotes/origin/*"
	for _, mapping := range []string{"+refs/tags/*:refs/tags/*", "+refs/heads/*:refs/remotes/other/*", "+refs/heads/*:refs/heads/*"} {
		f.git(local, "config", "--replace-all", "remote.origin.fetch", want)
		f.git(local, "config", "--add", "remote.origin.fetch", mapping)
		_, err := r.SafePull(testContext, true)
		wantError(t, err, "standard branch-tracking refspec")
	}
	f.git(local, "config", "--replace-all", "remote.origin.fetch", want)
	f.git(local, "symbolic-ref", "refs/remotes/origin/external-link", "refs/heads/main")
	_, err = r.SafePull(testContext, true)
	wantError(t, err, "unsafe symbolic tracking ref")
	f.git(local, "symbolic-ref", "--delete", "refs/remotes/origin/external-link")
	equal(t, f.git(local, "rev-parse", "HEAD", "refs/remotes/origin/main"), old+"\n"+old)
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 0)
	equal(t, f.git(local, "show", ":base"), "dirty")
	assertWorktreeFile(t, local, "loose", "untouched")
	f.git(local, "restore", "--staged", "--worktree", "base")
	mustOK(t, os.Remove(filepath.Join(local, "loose")))
	id, err := r.SafePull(testContext, false)
	mustOK(t, err)
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD"), target)
}

func TestSafePullLinkedWorktreeStashRace(t *testing.T) {
	if other := os.Getenv("GITBUDDY_TEST_TRY_DELETE_CHECKOUT"); other != "" {
		r := openRepo(t, other)
		rows, err := r.Stashes(testContext)
		mustOK(t, err)
		equal(t, len(rows), 1)
		equal(t, rows[0].Recovery, true)
		wantError(t, r.DeleteStash(testContext, rows[0]), "Safe pull or recovery deletion is active")
		return
	}
	f := newFixture(t)
	local, writer, _ := f.paired()
	linked := filepath.Join(f.root, "linked")
	f.git(local, "worktree", "add", "--track", "-b", "linked", linked, "origin/main")
	r := openRepo(t, linked)
	f.write(local, "local-file", "not ours")
	f.write(linked, "linked-file", "our untracked work")
	target := f.commit(writer, "incoming", "incoming", "advance")
	f.git(writer, "push", "origin", "main")

	// Add to the shared stash stack while Safe pull owns a recovery backup.
	// Safe pull must neither apply nor remove that independent stash.
	git, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	script := `#!/bin/sh
for argument; do
  if [ "$argument" = fetch ]; then
    GITBUDDY_TEST_TRY_DELETE_CHECKOUT="$OTHER_WORKTREE" "$TEST_BINARY" -test.run='^TestSafePullLinkedWorktreeStashRace$' >&2 || exit "$?"
    "$REAL_GIT" -C "$OTHER_WORKTREE" stash push --include-untracked -m foreign || exit "$?"
    break
  fi
done
exec "$REAL_GIT" "$@"
`
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", git)
	t.Setenv("OTHER_WORKTREE", local)
	t.Setenv("TEST_BINARY", os.Args[0])
	id, err := r.SafePull(testContext, true)
	mustOK(t, err)
	equal(t, id, "")
	equal(t, f.git(linked, "rev-parse", "HEAD"), target)
	assertWorktreeFile(t, linked, "linked-file", "our untracked work")
	if _, err := os.Stat(filepath.Join(linked, "local-file")); !os.IsNotExist(err) {
		t.Fatalf("foreign stash was applied: %v", err)
	}
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	equal(t, rows[0].ID, f.git(local, "rev-parse", "refs/stash"))
	equal(t, f.git(local, "show", rows[0].ID+"^3:local-file"), "not ours")
	equal(t, f.git(local, "for-each-ref", "--format=%(refname)", safeBackupRefs), "")
}

func TestSafePullRevalidatesOwnershipAfterFetch(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.write(local, "base", "save my changes")
	old := f.git(local, "rev-parse", "HEAD")
	next := f.commit(writer, "incoming", "incoming", "advance")
	f.git(writer, "push", "origin", "main")
	git, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	script := `#!/bin/sh
for argument; do
  if [ "$argument" = fetch ]; then
    "$REAL_GIT" "$@" || exit "$?"
    printf 'external change' > outside
    exit "$?"
  fi
done
exec "$REAL_GIT" "$@"
`
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", git)
	id, err := r.SafePull(testContext, true)
	if id == "" {
		t.Fatal("missing stash ID")
	}
	wantError(t, err, "was not reapplied")
	wantError(t, err, id)
	equal(t, f.git(local, "rev-parse", "HEAD", "refs/remotes/origin/main", safeBackupRefs+id), old+"\n"+next+"\n"+id)
	equal(t, f.git(local, "show", id+":base"), "save my changes")
	assertWorktreeFile(t, local, "base", "base")
	assertWorktreeFile(t, local, "outside", "external change")
}

func TestSafePullCleanupDoesNotDeleteAReplacedRef(t *testing.T) {
	f := newFixture(t)
	local, _, _ := f.paired()
	r := openRepo(t, local)
	f.write(local, "loose", "untracked work")
	base := f.git(local, "rev-parse", "HEAD")
	git, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(`#!/bin/sh
case " $* " in
  *" update-ref --no-deref -d refs/gitbuddy/safe-pull/"*)
    "$REAL_GIT" update-ref --no-deref "$5" "$FIXTURE_BASE" "$6" || exit "$?"
    ;;
esac
exec "$REAL_GIT" "$@"
`), 0700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", git)
	t.Setenv("FIXTURE_BASE", base)
	id, err := r.SafePull(testContext, true)
	wantError(t, err, "Local changes were restored, but backup")
	wantError(t, err, "could not be removed safely")
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), base)
	assertWorktreeFile(t, local, "loose", "untracked work")
}

func TestSafePullInterruptedAfterBackup(t *testing.T) {
	if local := os.Getenv("GITBUDDY_TEST_INTERRUPT_CHECKOUT"); local != "" {
		t.Setenv("GITBUDDY_TEST_INTERRUPT_PID", strconv.Itoa(os.Getpid()))
		_, err := openRepo(t, local).SafePull(testContext, true)
		t.Fatalf("Safe pull returned instead of being interrupted: %v", err)
	}
	f := newFixture(t)
	local, _, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	f.write(local, "base", "stage before interruption\n")
	f.git(local, "add", "base")
	f.write(local, "base", "unstaged before interruption\n")
	f.write(local, "loose", "untracked before interruption\n")
	git, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(`#!/bin/sh
for argument; do
  if [ "$argument" = fetch ]; then
    kill -KILL "$GITBUDDY_TEST_INTERRUPT_PID"
    exit "$?"
  fi
done
exec "$REAL_GIT" "$@"
`), 0700))
	cmd := exec.Command(os.Args[0], "-test.run=^TestSafePullInterruptedAfterBackup$")
	cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REAL_GIT="+git, "GITBUDDY_TEST_INTERRUPT_CHECKOUT="+local)
	out, err := cmd.CombinedOutput()
	if err, ok := err.(*exec.ExitError); !ok || err.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("expected fixture subprocess SIGKILL, got %v:\n%s", err, out)
	}
	equal(t, f.git(local, "rev-parse", "HEAD"), base)
	equal(t, f.git(local, "status", "--porcelain"), "")
	equal(t, f.git(local, "stash", "list"), "")
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	mustOK(t, r.ApplyStash(testContext, rows[0]))
	equal(t, f.git(local, "show", ":base"), "stage before interruption")
	assertWorktreeFile(t, local, "base", "unstaged before interruption\n")
	assertWorktreeFile(t, local, "loose", "untracked before interruption\n")
	equal(t, f.git(local, "ls-files", "loose"), "")
	equal(t, f.git(local, "rev-parse", safeBackupRefs+rows[0].ID), rows[0].ID)
	link := filepath.Join(f.root, "after-interruption")
	f.git(local, "worktree", "add", "--detach", link)
	mustOK(t, openRepo(t, link).DeleteStash(testContext, rows[0]))
	equal(t, f.git(local, "for-each-ref", safeBackupRefs), "")
}

func TestStashCleanupDoesNotClearIgnoredObstructions(t *testing.T) {
	f := newFixture(t)
	local, _, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	f.git(local, "rm", "base")
	mustOK(t, os.Mkdir(filepath.Join(local, "base"), 0700))
	f.write(local, "base/ignored", "must not be discarded")
	f.write(local, ".git/info/exclude", "ignored\n")

	wantError(t, r.StashAll(testContext, "must not be published"), "would replace an ignored path")
	equal(t, f.git(local, "diff", "--cached", "--name-status"), "D\tbase")
	equal(t, f.git(local, "stash", "list"), "")
	assertWorktreeFile(t, local, "base/ignored", "must not be discarded")

	id, err := r.SafePull(testContext, true)
	wantError(t, err, "stopped before fetch")
	equal(t, f.git(local, "rev-parse", "HEAD"), base)
	equal(t, f.git(local, "diff", "--cached", "--name-status"), "D\tbase")
	assertWorktreeFile(t, local, "base/ignored", "must not be discarded")
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
}

func TestStashCaptureRefusesConvertedTrackedBytes(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	f.commit(writer, "incoming", "remote change", "advance")
	f.git(writer, "push", "origin", "main")
	f.git(local, "config", "filter.upper.clean", "tr '[:lower:]' '[:upper:]'")
	f.write(local, ".git/info/attributes", "base filter=upper\n")
	f.write(local, "base", "original Mixed bytes\n")
	staged := f.git(local, "show", ":base")

	wantError(t, r.StashAll(testContext, "must not be published"), "Cannot save tracked files byte-for-byte")
	id, err := r.SafePull(testContext, true)
	wantError(t, err, "Cannot save tracked files byte-for-byte")
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD", "origin/main"), base+"\n"+base)
	equal(t, f.git(local, "show", ":base"), staged)
	equal(t, f.git(local, "stash", "list"), "")
	equal(t, f.git(local, "for-each-ref", safeBackupRefs), "")
	assertWorktreeFile(t, local, "base", "original Mixed bytes\n")

	// The clean-filtered worktree now matches HEAD; only the index differs
	// from it. Saving that index must still preserve the raw worktree side.
	f.write(local, ".git/info/attributes", "")
	f.write(local, "base", "LOCAL")
	f.git(local, "add", "base")
	f.git(local, "config", "filter.lower.clean", "tr '[:upper:]' '[:lower:]'")
	f.write(local, ".git/info/attributes", "base filter=lower\n")
	f.write(local, "base", "bAsE")
	wantError(t, r.StashAll(testContext, "must not be published"), "Cannot save tracked files byte-for-byte")
	equal(t, f.git(local, "show", ":base"), "LOCAL")
	equal(t, f.git(local, "stash", "list"), "")
	assertWorktreeFile(t, local, "base", "bAsE")
}

func TestSafePullDoesNotOverwriteRawBytesHiddenByCleanFilter(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	base := f.commit(writer, "base", "BASE\n", "canonical base")
	f.git(writer, "push", "origin", "main")
	_, err := r.SafePull(testContext, false)
	mustOK(t, err)
	f.git(local, "config", "filter.upper.clean", "tr '[:lower:]' '[:upper:]'")
	f.write(local, ".git/info/attributes", "base filter=upper\n")
	f.write(local, "base", "bAsE\n")
	equal(t, f.git(local, "status", "--porcelain"), "")
	next := f.commit(writer, "base", "NEXT\n", "would overwrite masked edit")
	f.git(writer, "push", "origin", "main")

	id, err := r.SafePull(testContext, true)
	wantError(t, err, "Cannot verify tracked files byte-for-byte before the fast-forward")
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD", "origin/main"), base+"\n"+next)
	assertWorktreeFile(t, local, "base", "bAsE\n")
}

func interposeSafePullGit(t *testing.T, f *fixture, script string) {
	t.Helper()
	git, err := exec.LookPath("git")
	mustOK(t, err)
	tools := filepath.Join(f.root, "tools")
	mustOK(t, os.Mkdir(tools, 0700))
	mustOK(t, os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", git)
}

func TestSafePullDoesNotRestoreOverRawEditHiddenDuringFetch(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.commit(writer, "base", "BASE\n", "canonical base")
	f.git(writer, "push", "origin", "main")
	_, err := r.SafePull(testContext, false)
	mustOK(t, err)
	f.git(local, "config", "filter.upper.clean", "tr '[:lower:]' '[:upper:]'")
	f.write(local, ".git/info/attributes", "base filter=upper\n")
	f.write(local, "base", "LOCAL\n")
	target := f.commit(writer, "incoming", "incoming\n", "advance elsewhere")
	f.git(writer, "push", "origin", "main")

	interposeSafePullGit(t, f, `#!/bin/sh
for argument; do
  if [ "$argument" = fetch ]; then
    "$REAL_GIT" "$@" || exit "$?"
    printf 'bAsE\n' > base || exit "$?"
    test -z "$("$REAL_GIT" status --porcelain)"
    exit "$?"
  fi
done
exec "$REAL_GIT" "$@"
`)

	id, err := r.SafePull(testContext, true)
	wantError(t, err, "was not reapplied")
	wantError(t, err, `"base"`)
	equal(t, f.git(local, "rev-parse", "HEAD"), target)
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
	equal(t, f.git(local, "show", id+":base"), "LOCAL")
	assertWorktreeFile(t, local, "base", "bAsE\n")

	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	equal(t, rows[0].ID, id)
	equal(t, rows[0].Recovery, true)
	equal(t, f.git(local, "status", "--porcelain"), "")
	err = r.ApplyStash(testContext, rows[0])
	assertWorktreeFile(t, local, "base", "bAsE\n")
	wantError(t, err, "Cannot verify stash destinations byte-for-byte")
	wantError(t, err, `"base"`)
	equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
}

func TestSafePullDoesNotRestoreOverIgnoredPathRecreatedDuringFetch(t *testing.T) {
	for _, tc := range []struct {
		name, staged, edited string
		fetchFails, loose    bool
	}{
		{name: "fast-forward", staged: "ignored", edited: "ignored"},
		{name: "failed fetch", staged: "ignored", edited: "ignored", fetchFails: true},
		{name: "file ancestor", staged: "ignored/saved", edited: "ignored"},
		{name: "symlink ancestor", staged: "ignored/saved"},
		{name: "directory at destination", staged: "ignored", edited: "ignored/new"},
		{name: "fifo at destination", staged: "ignored"},
		{name: "saved untracked later ignored", staged: "ignored/saved", edited: "ignored/saved", loose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			local, writer, _ := f.paired()
			r := openRepo(t, local)
			base := f.git(local, "rev-parse", "HEAD")
			if !tc.loose {
				f.write(local, ".git/info/exclude", "/ignored\n")
			}
			mustOK(t, os.MkdirAll(filepath.Dir(filepath.Join(local, tc.staged)), 0700))
			f.write(local, tc.staged, "saved bytes\n")
			parent, obstruction := "^2:", "ignored"
			if tc.loose {
				parent, obstruction = "^3:", tc.edited
			} else {
				f.git(local, "add", "--force", "--", tc.staged)
			}
			target := f.commit(writer, "incoming", "incoming\n", "advance elsewhere")
			f.git(writer, "push", "origin", "main")

			interposeSafePullGit(t, f, `#!/bin/sh
for argument; do
  if [ "$argument" = fetch ]; then
    if [ "$FAIL_FETCH" = false ]; then
      "$REAL_GIT" "$@" || exit "$?"
    fi
    printf '/ignored\n' > .git/info/exclude || exit "$?"
    case "$EDITOR_PATH:$SCENARIO" in
      ':fifo at destination') mkfifo ignored || exit "$?" ;;
      ':symlink ancestor') ln -s missing ignored || exit "$?" ;;
      *) mkdir -p "$(dirname "$EDITOR_PATH")" && printf 'editor bytes\n' > "$EDITOR_PATH" || exit "$?" ;;
    esac
    status=$("$REAL_GIT" status --porcelain) && test -z "$status" || exit 98
    if [ "$FAIL_FETCH" = true ]; then exit 97; fi
    exit 0
  fi
done
exec "$REAL_GIT" "$@"
`)
			t.Setenv("EDITOR_PATH", tc.edited)
			t.Setenv("SCENARIO", tc.name)
			t.Setenv("FAIL_FETCH", strconv.FormatBool(tc.fetchFails))

			id, err := r.SafePull(testContext, true)
			if tc.fetchFails {
				wantError(t, err, "fetch failed (exit 97)")
				target = base
			}
			equal(t, f.git(local, "rev-parse", "HEAD"), target)
			wantError(t, err, "was not reapplied")
			wantError(t, err, "restore path is obstructed by "+strconv.Quote(obstruction))
			equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
			equal(t, f.git(local, "show", id+parent+tc.staged), "saved bytes")
			rows, err := r.Stashes(testContext)
			mustOK(t, err)
			equal(t, len(rows), 1)
			wantError(t, r.ApplyStash(testContext, rows[0]), "restore path is obstructed by "+strconv.Quote(obstruction))
			if tc.edited != "" {
				assertWorktreeFile(t, local, tc.edited, "editor bytes\n")
			} else if tc.name == "fifo at destination" {
				info, err := os.Lstat(filepath.Join(local, "ignored"))
				mustOK(t, err)
				equal(t, info.Mode()&os.ModeNamedPipe, os.ModeNamedPipe)
			} else {
				link, err := os.Readlink(filepath.Join(local, "ignored"))
				mustOK(t, err)
				equal(t, link, "missing")
			}
			equal(t, f.git(local, "status", "--porcelain"), "")
			equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
		})
	}
}

func TestStashAndSafePullLeaveUnrelatedCleanConvertedFilesInPlace(t *testing.T) {
	for _, operation := range []string{"stash", "pull"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			local, writer, _ := f.paired()
			r := openRepo(t, local)
			f.commit(writer, "lines.txt", "canonical\n", "track normalized text")
			f.git(writer, "push", "origin", "main")
			f.write(local, ".git/info/attributes", "lines.txt text eol=crlf\n")
			_, err := r.SafePull(testContext, false)
			mustOK(t, err)
			assertWorktreeFile(t, local, "lines.txt", "canonical\r\n")
			equal(t, f.git(local, "status", "--porcelain"), "")
			info, err := os.Stat(filepath.Join(local, "lines.txt"))
			mustOK(t, err)
			// Keep an index-only change even though its worktree blob is
			// identical to HEAD. The unrelated clean file is not saved.
			f.write(local, "base", "index only")
			f.git(local, "add", "base")
			f.write(local, "base", "base")
			f.write(local, "loose", "new dirty file\n")

			if operation == "stash" {
				mustOK(t, r.StashAll(testContext, "save unrelated local changes"))
				equal(t, f.git(local, "status", "--porcelain"), "")
				equal(t, f.git(local, "show", "refs/stash^2:base"), "index only")
				equal(t, f.git(local, "show", "refs/stash^3:loose"), "new dirty file")
			} else {
				target := f.commit(writer, "incoming", "incoming\n", "advance elsewhere")
				f.git(writer, "push", "origin", "main")
				id, err := r.SafePull(testContext, true)
				mustOK(t, err)
				equal(t, id, "")
				equal(t, f.git(local, "rev-parse", "HEAD"), target)
				equal(t, f.git(local, "show", ":base"), "index only")
				assertWorktreeFile(t, local, "loose", "new dirty file\n")
			}
			assertWorktreeFile(t, local, "base", "base")
			assertWorktreeFile(t, local, "lines.txt", "canonical\r\n")
			after, err := os.Stat(filepath.Join(local, "lines.txt"))
			mustOK(t, err)
			if !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
				t.Fatal("operation rewrote an unrelated clean path")
			}
		})
	}
}

func TestSafePullKeepsTrackedBackupWhenUpstreamChangesCheckoutConversion(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.write(local, "base", "original\n")
	target := f.commit(writer, ".gitattributes", "base text eol=crlf\n", "change checkout conversion")
	f.git(writer, "push", "origin", "main")

	id, err := r.SafePull(testContext, true)
	wantError(t, err, "Could not verify the tracked contents")
	equal(t, f.git(local, "rev-parse", "HEAD", safeBackupRefs+id), target+"\n"+id)
	equal(t, f.git(local, "show", id+":base"), "original")
	assertWorktreeFile(t, local, "base", "original\r\n")
}

func TestSafePullVerifiesMergedTrackedBytesWithOverlappingPaths(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.commit(writer, "base", "one\ntwo\nthree\nfour\nfive\nsix\n", "multiple lines")
	f.git(writer, "push", "origin", "main")
	_, err := r.SafePull(testContext, false)
	mustOK(t, err)
	f.write(local, "base", "ONE\ntwo\nthree\nfour\nfive\nsix\n")
	target := f.commit(writer, "base", "one\ntwo\nthree\nfour\nfive\nSIX\n", "separate upstream edit")
	f.git(writer, "push", "origin", "main")

	id, err := r.SafePull(testContext, true)
	mustOK(t, err)
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD"), target)
	assertWorktreeFile(t, local, "base", "ONE\ntwo\nthree\nfour\nfive\nSIX\n")
}

func TestSafePullRestoresAcrossUpstreamRenameIntoDirectory(t *testing.T) {
	for _, scenario := range []string{"unstaged", "hidden destination edit", "staged source", "conflicting replacement"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			local, writer, _ := f.paired()
			r := openRepo(t, local)
			const original = "ONE\nTWO\nTHREE\nFOUR\nFIVE\nSIX\n"
			const edited = "CHANGED\nTWO\nTHREE\nFOUR\nFIVE\nSIX\n"
			f.commit(writer, "guide", original, "track guide")
			f.git(writer, "push", "origin", "main")
			_, err := r.SafePull(testContext, false)
			mustOK(t, err)
			f.write(local, "guide", edited)
			if scenario == "staged source" {
				f.git(local, "add", "guide")
			}
			// Move guide through a temporary name: the final destination
			// replaces the source file with a real upstream directory.
			f.git(writer, "mv", "guide", "guide-temp")
			mustOK(t, os.Mkdir(filepath.Join(writer, "guide"), 0700))
			f.git(writer, "mv", "guide-temp", "guide/main")
			upstream := original
			if scenario == "conflicting replacement" {
				// An unrelated file at the same destination is not a
				// rename; applying may need a Git-generated conflict path.
				upstream = "unrelated upstream file\n"
				f.write(writer, "guide/main", upstream)
				f.git(writer, "add", "guide/main")
			}
			f.git(writer, "commit", "-m", "move guide into its own directory")
			target := f.git(writer, "rev-parse", "HEAD")
			f.git(writer, "push", "origin", "main")
			if scenario == "hidden destination edit" {
				f.git(local, "config", "filter.upper.clean", "tr '[:lower:]' '[:upper:]'")
				f.write(local, ".git/info/attributes", "guide/main filter=upper\n")
				interposeSafePullGit(t, f, `#!/bin/sh
for argument; do
  if [ "$argument" = --ff-only ]; then
    "$REAL_GIT" "$@" || exit "$?"
    printf 'oNe\nTWO\nTHREE\nFOUR\nFIVE\nSIX\n' > guide/main || exit "$?"
    status=$("$REAL_GIT" status --porcelain) && test -z "$status"
    exit "$?"
  fi
done
exec "$REAL_GIT" "$@"
`)
			}

			id, err := r.SafePull(testContext, true)
			equal(t, f.git(local, "rev-parse", "HEAD"), target)
			equal(t, f.git(local, "diff", "--cached", "--name-only"), "")
			switch scenario {
			case "unstaged":
				mustOK(t, err)
				equal(t, id, "")
				assertWorktreeFile(t, local, "guide/main", edited)
				equal(t, f.git(local, "for-each-ref", safeBackupRefs), "")
			case "hidden destination edit":
				wantError(t, err, "was not reapplied")
				wantError(t, err, `File contents or type do not match the saved blob: "guide/main"`)
				rows, err := r.Stashes(testContext)
				mustOK(t, err)
				equal(t, len(rows), 1)
				wantError(t, r.ApplyStash(testContext, rows[0]), `File contents or type do not match the saved blob: "guide/main"`)
				assertWorktreeFile(t, local, "guide/main", "oNe\nTWO\nTHREE\nFOUR\nFIVE\nSIX\n")
			case "staged source", "conflicting replacement":
				wantError(t, err, "was not reapplied")
				wantError(t, err, `restore path is obstructed by "guide"`)
				if scenario == "staged source" {
					equal(t, f.git(local, "show", id+"^2:guide"), strings.TrimSuffix(edited, "\n"))
				}
				assertWorktreeFile(t, local, "guide/main", upstream)
			}
			if scenario != "unstaged" {
				equal(t, f.git(local, "rev-parse", safeBackupRefs+id), id)
				equal(t, f.git(local, "show", id+":guide"), strings.TrimSuffix(edited, "\n"))
				equal(t, f.git(local, "status", "--porcelain"), "")
			}
		})
	}
}

func TestSafePullPreservesUntrackedBytesAroundFilters(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	f.write(local, ".git/info/attributes", "*.txt text eol=lf\n")
	f.write(local, "loose.txt", "original\r\n")
	id, err := r.SafePull(testContext, true)
	wantError(t, err, "Cannot save untracked files byte-for-byte")
	equal(t, id, "")
	equal(t, f.git(local, "rev-parse", "HEAD"), base)
	assertWorktreeFile(t, local, "loose.txt", "original\r\n")

	mustOK(t, os.Remove(filepath.Join(local, ".git/info/attributes")))
	f.write(local, "loose.txt", "original\n")
	target := f.commit(writer, ".gitattributes", "*.txt text eol=crlf\n", "change checkout filters")
	f.git(writer, "push", "origin", "main")
	id, err = r.SafePull(testContext, true)
	wantError(t, err, "Could not verify the untracked contents")
	equal(t, f.git(local, "rev-parse", "HEAD", safeBackupRefs+id), target+"\n"+id)
	equal(t, f.git(local, "show", id+"^3:loose.txt"), "original")
	assertWorktreeFile(t, local, "loose.txt", "original\r\n")
}

func TestStashActionsStableSelectionAndUnsupportedStates(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	nested := filepath.Join(local, "embedded")
	mustOK(t, os.Mkdir(nested, 0700))
	f.git(nested, "init")
	f.commit(nested, "file", "contents", "nested")
	wantError(t, r.StashAll(testContext, ""), "Nested repositories")
	_, err := r.SafePull(testContext, true)
	wantError(t, err, "Nested repositories")
	mustOK(t, os.Rename(nested, filepath.Join(f.root, "embedded")))
	oid := f.git(writer, "rev-parse", "HEAD")
	f.git(local, "update-index", "--add", "--cacheinfo", "160000,"+oid+",module")
	wantError(t, r.StashAll(testContext, ""), "Submodule entries")
	f.git(local, "update-index", "--force-remove", "module")
	f.git(local, "rm", "base")
	f.write(local, "base", "untracked replacement")
	wantError(t, r.StashAll(testContext, ""), "untracked replacement")
	assertWorktreeFile(t, local, "base", "untracked replacement")
	f.git(local, "restore", "--staged", "--worktree", "base")

	f.commit(local, "tracked", "before", "track a separate stash destination")
	f.write(local, "tracked", "saved")
	f.write(local, "first", "first")
	t.Setenv("GIT_COMMITTER_DATE", "2025-11-04T01:02:03Z")
	mustOK(t, r.StashAll(testContext, "first note"))
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	selected := rows[0]
	f.write(local, "second", "second")
	t.Setenv("GIT_COMMITTER_DATE", "2026-11-04T01:02:03Z")
	mustOK(t, r.StashAll(testContext, ""))
	rows, err = r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 2)
	if !rows[0].Date.After(rows[1].Date) || !strings.HasSuffix(rows[1].Subject, ": first note") {
		t.Fatalf("stash dates/notes missing: %+v", rows)
	}
	f.write(local, "base", "unrelated staged\n")
	f.git(local, "add", "base")
	f.write(local, "base", "unrelated unstaged\n")
	mustOK(t, r.ApplyStash(testContext, selected))
	assertWorktreeFile(t, local, "first", "first")
	assertWorktreeFile(t, local, "tracked", "saved")
	equal(t, f.git(local, "show", ":base"), "unrelated staged")
	assertWorktreeFile(t, local, "base", "unrelated unstaged\n")
	if _, err := os.Stat(filepath.Join(local, "second")); !os.IsNotExist(err) {
		t.Fatalf("moving stash position was used: %v", err)
	}
	after, err := r.Stashes(testContext)
	mustOK(t, err)
	if !slices.EqualFunc(after, rows, Stash.SameEntry) {
		t.Fatalf("apply changed the stash list: %+v", after)
	}
	f.git(local, "stash", "drop", "stash@{1}")
	wantError(t, r.ApplyStash(testContext, selected), "no longer listed")
}
