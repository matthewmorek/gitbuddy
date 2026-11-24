package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestStashPreviewUsesSavedTreesAndCountsIndexDifferencesOnce(t *testing.T) {
	f := newFixture(t)
	local := f.repo("preview", false)
	f.write(local, "split.txt", "same\nbase\n")
	f.write(local, "index-only.txt", "old\n")
	f.write(local, "staged.txt", "old\n")
	f.write(local, "old.txt", "rename me\n")
	f.git(local, "add", ".")
	f.git(local, "commit", "-m", "base")
	f.write(local, "split.txt", "same\nshared change\nindex version\n")
	f.write(local, "index-only.txt", "new\n")
	f.write(local, "staged.txt", "new\n")
	f.git(local, "mv", "old.txt", "new.txt")
	f.git(local, "add", ".")
	f.write(local, "split.txt", "same\nshared change\nworktree version\n")
	f.write(local, "index-only.txt", "old\n")
	f.write(local, "loose.txt", "loose\nsecond line\n")
	f.write(local, "binary.bin", "a\x00b")
	r := openRepo(t, local)
	mustOK(t, r.StashAll(testContext, "archived changes"))
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)

	f.write(local, "staged.txt", "different live work\nand another line\n")
	f.write(local, "loose.txt", "different live untracked\n")
	f.write(local, ".gitattributes", "*.txt -diff\nbinary.bin diff\n")
	before := f.git(local, "status", "--porcelain")
	preview, err := r.StashPreview(testContext, rows[0].ID)
	mustOK(t, err)
	equal(t, preview.Tracked, []ChangedFile{
		{Path: "new.txt", OldPath: "old.txt", Status: "R100"},
		{Path: "split.txt", Status: "M", Added: 2, Deleted: 1},
		{Path: "staged.txt", Status: "M", Added: 1, Deleted: 1},
	})
	equal(t, preview.Untracked, []ChangedFile{
		{Path: "binary.bin", Status: "A", Binary: true},
		{Path: "loose.txt", Status: "A", Added: 2},
	})
	equal(t, preview.Index, []ChangedFile{
		{Path: "index-only.txt", Status: "M", Added: 1, Deleted: 1},
		{Path: "split.txt", Status: "M", Added: 1, Deleted: 1},
	})
	equal(t, f.git(local, "status", "--porcelain"), before)
}

func TestDeleteStashRefusesShiftAndSameIDReplacement(t *testing.T) {
	f := newFixture(t)
	local := f.repo("stack", false)
	f.commit(local, "base", "base\n", "base")
	r := openRepo(t, local)
	common, err := r.filesStashPath(testContext)
	mustOK(t, err)
	linked := filepath.Join(f.root, "linked")
	f.git(local, "worktree", "add", "--detach", linked)
	t.Setenv("GIT_COMMITTER_DATE", "2026-11-04T01:02:03Z")
	save := func(name string) {
		f.write(local, name, name)
		mustOK(t, r.StashAll(testContext, name))
	}
	list := func() []Stash {
		rows, err := r.Stashes(testContext)
		mustOK(t, err)
		return rows
	}
	save("first")
	first := list()[0]
	save("second")
	second := list()[0]
	save("third")
	third := list()[0]
	before := f.git(local, "stash", "list", "--format=%H %gs")
	wantError(t, r.DeleteStash(testContext, second), "stack changed")
	equal(t, f.git(local, "stash", "list", "--format=%H %gs"), before)

	// Drop and re-store the same tip with the same message and timestamp.
	// The rewritten reflog can have identical bytes, but it is a different log.
	_, originalLog, err := readStashStack(common)
	mustOK(t, err)
	f.git(linked, "stash", "drop", "stash@{0}")
	f.git(linked, "stash", "store", "-m", third.Subject, third.ID)
	_, rewrittenLog, err := readStashStack(common)
	mustOK(t, err)
	equal(t, rewrittenLog, originalLog)
	equal(t, f.git(local, "stash", "list", "--format=%H %gs"), before)
	wantError(t, r.DeleteStash(testContext, third), "stack changed")
	equal(t, f.git(local, "stash", "list", "--format=%H %gs"), before)

	f.git(linked, "stash", "store", "-m", "duplicate", first.ID)
	rows := list()
	wantError(t, r.DeleteStash(testContext, rows[0]), "Multiple stack entries")
	wantError(t, r.ApplyStash(testContext, rows[0]), "Multiple stack entries")
	f.git(linked, "stash", "drop", "stash@{0}")

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return
	}
	refLock := filepath.Join(common, stashRef+".lock")
	mustOK(t, os.WriteFile(refLock, []byte("foreign lock"), 0600))
	wantError(t, r.DeleteStash(testContext, list()[1]), "Cannot lock")
	assertWorktreeFile(t, common, stashRef+".lock", "foreign lock")
	mustOK(t, os.Remove(refLock))
	equal(t, f.git(local, "stash", "list", "--format=%H %gs"), before)

	row := list()[1]
	alias := filepath.Join(f.root, "other-reflog")
	mustOK(t, os.Link(filepath.Join(common, stashLog), alias))
	wantError(t, r.DeleteStash(testContext, row), "stack changed")
	mustOK(t, os.Remove(alias))
	equal(t, f.git(local, "stash", "list", "--format=%H %gs"), before)

	// A fresh middle selection deletes only that row from the shared stack.
	mustOK(t, openRepo(t, linked).DeleteStash(testContext, list()[1]))
	equal(t, f.git(local, "stash", "list", "--format=%H"), third.ID+"\n"+first.ID)
	equal(t, f.git(local, "rev-parse", "refs/stash"), third.ID)
	mustOK(t, r.DeleteStash(testContext, list()[0]))
	equal(t, f.git(local, "rev-parse", "refs/stash"), first.ID)
	mustOK(t, r.DeleteStash(testContext, list()[0]))
	equal(t, len(list()), 0)
	equal(t, f.git(local, "for-each-ref", "refs/stash"), "")
	equal(t, f.git(local, "status", "--porcelain"), "")
}

func TestStashDeleteRejectsReplacementAfterTakingLocks(t *testing.T) {
	f := newFixture(t)
	local := f.repo("stack", false)
	f.commit(local, "base", "base\n", "base")
	r := openRepo(t, local)
	for _, name := range []string{"first", "second"} {
		f.write(local, name, name)
		mustOK(t, r.StashAll(testContext, name))
	}
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	common, err := r.filesStashPath(testContext)
	mustOK(t, err)
	guard, err := lockCommon(common, stashDeleteDir+".lock")
	mustOK(t, err)
	defer guard.Close()
	txn := stashDeleteTxn{common}
	record, err := txn.prepare(rows[0])
	mustOK(t, err)
	_, original, err := readStashStack(common)
	mustOK(t, err)
	// Simulate a client replacing the reflog outside ordinary ref locking.
	replacement := filepath.Join(common, "replacement")
	mustOK(t, os.WriteFile(replacement, []byte(original), 0600))
	mustOK(t, os.Rename(replacement, filepath.Join(common, stashLog)))
	wantError(t, txn.installLog(record), "stack changed")
	mustOK(t, txn.recoverChecked())
	equal(t, f.git(local, "rev-parse", stashRef), rows[0].ID)
	equal(t, f.git(local, "stash", "list", "--format=%H"), rows[0].ID+"\n"+rows[1].ID)
}

func TestStashDeleteRecoversInterruptedOrFailedTipUpdate(t *testing.T) {
	if local := os.Getenv("GITBUDDY_TEST_DELETE_INTERRUPT"); local != "" {
		r := openRepo(t, local)
		common, err := r.filesStashPath(testContext)
		mustOK(t, err)
		rows, err := r.Stashes(testContext)
		mustOK(t, err)
		guard, err := lockCommon(common, stashDeleteDir+".lock")
		mustOK(t, err)
		defer guard.Close()
		txn := stashDeleteTxn{common}
		record, err := txn.prepare(rows[0])
		mustOK(t, err)
		mustOK(t, txn.installLog(record))
		if os.Getenv("GITBUDDY_TEST_DELETE_COMMIT") == "yes" {
			mustOK(t, txn.installTip(record))
		}
		mustOK(t, syscall.Kill(os.Getpid(), syscall.SIGKILL))
		t.Fatal("fixture subprocess was not interrupted")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fixture requires atomic file exchange")
	}
	for _, phase := range []string{"interrupted between log and tip", "interrupted after tip", "tip write fails"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			local := f.repo("stack", false)
			f.commit(local, "base", "base\n", "base")
			linked := filepath.Join(f.root, "linked")
			f.git(local, "worktree", "add", "--detach", linked)
			r := openRepo(t, local)
			for _, name := range []string{"first", "second", "third"} {
				f.write(local, name, name)
				mustOK(t, r.StashAll(testContext, name))
			}
			rows, err := r.Stashes(testContext)
			mustOK(t, err)
			common, err := r.filesStashPath(testContext)
			mustOK(t, err)
			_, original, err := readStashStack(common)
			mustOK(t, err)
			committed := phase == "interrupted after tip"
			txn := stashDeleteTxn{common}
			if phase == "tip write fails" {
				guard, err := lockCommon(common, stashDeleteDir+".lock")
				mustOK(t, err)
				defer guard.Close()
				record, err := txn.prepare(rows[0])
				mustOK(t, err)
				mustOK(t, txn.installLog(record))
				mustOK(t, os.Remove(txn.path("next-tip")))
				if err := txn.installTip(record); !os.IsNotExist(err) {
					t.Fatalf("expected the tip install to fail: %v", err)
				}
				mustOK(t, txn.recoverChecked())
				mustOK(t, guard.Close())
			} else {
				cmd := exec.Command(os.Args[0], "-test.run=^TestStashDeleteRecoversInterruptedOrFailedTipUpdate$")
				cmd.Env = append(os.Environ(), "GITBUDDY_TEST_DELETE_INTERRUPT="+local)
				if committed {
					cmd.Env = append(cmd.Env, "GITBUDDY_TEST_DELETE_COMMIT=yes")
				}
				out, err := cmd.CombinedOutput()
				if err, ok := err.(*exec.ExitError); !ok || err.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
					t.Fatalf("expected fixture subprocess SIGKILL, got %v:\n%s", err, out)
				}
				assertWorktreeFile(t, common, filepath.Join(stashDeleteDir, "exchange-log"), original)
			}
			other := openRepo(t, linked)
			recovered, err := other.Stashes(testContext)
			mustOK(t, err)
			if committed {
				equal(t, len(recovered), 2)
				equal(t, f.git(local, "rev-parse", stashRef), rows[1].ID)
				wantError(t, other.DeleteStash(testContext, rows[0]), "stack changed")
			} else {
				equal(t, len(recovered), 3)
				equal(t, f.git(local, "rev-parse", stashRef), rows[0].ID)
				assertWorktreeFile(t, common, stashLog, original)
				mustOK(t, other.DeleteStash(testContext, recovered[0]))
			}
			equal(t, f.git(local, "stash", "list", "--format=%H"), rows[1].ID+"\n"+rows[2].ID)
			for _, path := range []string{stashRef + ".lock", stashLog + ".lock", stashDeleteDir} {
				if _, err := os.Lstat(filepath.Join(common, path)); !os.IsNotExist(err) {
					t.Fatalf("deletion left owned transaction metadata at %s: %v", path, err)
				}
			}
		})
	}
}

func TestStashDeleteRecoveryPreservesUnrecordedInspectionCopy(t *testing.T) {
	f := newFixture(t)
	local := f.repo("stack", false)
	f.commit(local, "base", "base\n", "base")
	r := openRepo(t, local)
	f.write(local, "loose", "save me")
	mustOK(t, r.StashAll(testContext, "stash retained during inspection"))
	common, err := r.filesStashPath(testContext)
	mustOK(t, err)
	_, original, err := readStashStack(common)
	mustOK(t, err)
	txn := stashDeleteTxn{common}
	mustOK(t, os.Mkdir(txn.path(""), 0700))
	mustOK(t, txn.lock(0))
	const copy = "inspection-copy"
	mustOK(t, os.WriteFile(txn.path(copy), []byte(original), 0600))

	_, err = r.Stashes(testContext)
	wantError(t, err, "Cannot recover the stash deletion safely")
	wantError(t, err, copy)
	assertWorktreeFile(t, txn.path(""), copy, original)
	assertWorktreeFile(t, common, stashLog, original)
	equal(t, txn.ownsLock(0), true)

	mustOK(t, os.Rename(txn.path(copy), filepath.Join(f.root, copy)))
	// Even a familiar name isn't proof that the contents can be discarded:
	// this could be the original reflog from an interrupted exchange.
	mustOK(t, os.WriteFile(txn.path("exchange-log"), []byte(original), 0600))
	_, err = r.Stashes(testContext)
	wantError(t, err, `"exchange-log" without its record`)
	assertWorktreeFile(t, txn.path(""), "exchange-log", original)
	equal(t, txn.ownsLock(0), true)

	// Once the unverified files are moved aside, known pre-record lock
	// artifacts can be retired and the original stash remains available.
	mustOK(t, os.Rename(txn.path("exchange-log"), filepath.Join(f.root, "saved-log")))
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 1)
	equal(t, rows[0].ID, f.git(local, "rev-parse", stashRef))
	assertWorktreeFile(t, f.root, copy, original)
	if _, err := os.Lstat(txn.path("")); !os.IsNotExist(err) {
		t.Fatalf("known transaction artifacts were not retired: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(common, stashRef+".lock")); !os.IsNotExist(err) {
		t.Fatalf("known Git lock was not retired: %v", err)
	}
}

func TestDeleteRecoveryUsesDirectRefAndExpectedOID(t *testing.T) {
	f := newFixture(t)
	local := f.repo("recovery", false)
	head := f.commit(local, "base", "base\n", "base")
	r := openRepo(t, local)
	f.write(local, "loose", "untracked\n")
	mustOK(t, r.StashAll(testContext, "shared entry"))
	id := f.git(local, "rev-parse", stashRef)
	recovery := safeBackupRefs + id
	f.git(local, "update-ref", recovery, id)
	list, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(list), 2)
	var selected Stash
	for _, row := range list {
		if row.Recovery {
			selected = row
		}
	}
	if selected.ID != id {
		t.Fatal("same-OID recovery was not listed separately")
	}
	f.git(local, "update-ref", recovery, head, id)
	wantError(t, r.DeleteStash(testContext, selected), "recovery ref")
	equal(t, f.git(local, "rev-parse", recovery), head)
	f.git(local, "symbolic-ref", recovery, stashRef)
	wantError(t, r.DeleteStash(testContext, selected), "recovery ref")
	equal(t, f.git(local, "symbolic-ref", recovery), stashRef)
	f.git(local, "symbolic-ref", "--delete", recovery)
	f.git(local, "update-ref", recovery, id)
	mustOK(t, r.DeleteStash(testContext, selected))
	if out := f.git(local, "for-each-ref", recovery); strings.TrimSpace(out) != "" {
		t.Fatalf("confirmed recovery ref remains: %s", out)
	}
	equal(t, f.git(local, "rev-parse", stashRef), id)
}

func TestReftableStashesStayBrowsableAndRecoveryRefCanBeDeleted(t *testing.T) {
	f := newFixture(t)
	local := filepath.Join(f.root, "reftable")
	cmd := exec.Command("git", "init", "--initial-branch=main", "--ref-format=reftable", local)
	cmd.Dir, cmd.Env = f.root, environment()
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "unknown option") {
			t.Skip("Git does not support initializing reftable repositories")
		}
		t.Fatalf("initialize reftable: %v\n%s", err, out)
	}
	f.commit(local, "base", "base\n", "base")
	r := openRepo(t, local)
	f.write(local, "loose", "untracked\n")
	mustOK(t, r.StashAll(testContext, "shared entry"))
	id := f.git(local, "rev-parse", stashRef)
	recovery := safeBackupRefs + id
	f.git(local, "update-ref", recovery, id)
	rows, err := r.Stashes(testContext)
	mustOK(t, err)
	equal(t, len(rows), 2)
	for _, row := range rows {
		if row.Recovery {
			mustOK(t, r.DeleteStash(testContext, row))
		} else {
			wantError(t, r.DeleteStash(testContext, row), "ref storage")
		}
	}
	equal(t, f.git(local, "for-each-ref", recovery), "")
	equal(t, f.git(local, "stash", "list", "--format=%H"), id)
}
