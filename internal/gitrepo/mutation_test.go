package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchNoPruneSkippedRemotesWarningsAndFailure(t *testing.T) {
	f := newFixture(t)
	local, writer, remote := f.paired()
	r := openRepo(t, local)
	before := statusOf(t, r).Head
	f.git(local, "tag", "local-only")
	f.git(local, "update-ref", "refs/remotes/origin/deleted", before)
	for _, key := range []string{"fetch.prune", "fetch.pruneTags", "remote.origin.prune", "remote.origin.pruneTags"} {
		f.git(local, "config", key, "true")
	}
	f.git(local, "remote", "add", "skipped", filepath.Join(f.root, "missing"))
	f.git(local, "config", "remote.skipped.skipFetchAll", "true")
	next := f.commit(writer, "next", "next", "next")
	f.git(writer, "push", "origin", "main")
	mustOK(t, r.Fetch(testContext))
	equal(t, f.git(local, "rev-parse", "refs/remotes/origin/main"), next)
	equal(t, f.git(local, "rev-parse", "refs/remotes/origin/deleted"), before)
	equal(t, f.git(local, "rev-parse", "refs/tags/local-only"), before)
	equal(t, statusOf(t, r).Head, before)
	f.git(local, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	warnings := strings.Join(statusOf(t, r).FetchWarnings, "\n")
	if !strings.Contains(warnings, "narrow") || !strings.Contains(warnings, "skipFetchAll") {
		t.Fatalf("missing mapping/skip warnings: %s", warnings)
	}
	f.git(local, "remote", "set-url", "origin", remote+"/missing-secret-token")
	err := r.Fetch(testContext)
	wantError(t, err, "Fetch failed")
	if strings.Contains(err.Error(), "missing-secret-token") || strings.Contains(err.Error(), remote) {
		t.Fatalf("error exposed raw remote diagnostics: %v", err)
	}
	for _, key := range []string{"fetch.prune", "fetch.pruneTags", "remote.origin.prune", "remote.origin.pruneTags"} {
		equal(t, f.git(local, "config", key), "true")
	}
}

func TestFetchAndPullRejectAllLocalHeadMappings(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	before := statusOf(t, r).Head
	f.git(local, "branch", "topic")
	next := f.commit(writer, "topic-next", "topic", "topic")
	f.git(writer, "push", "origin", "HEAD:refs/heads/topic")
	tracking := "+refs/heads/*:refs/remotes/origin/*"
	for _, mapping := range []string{
		"+refs/*:refs/*", "+refs/hea*:refs/hea*", "+refs/heads*:refs/heads*",
		"+refs/heads/*:refs/*/topic", "refs/heads/topic:topic", "refs/heads/topic:refs/heads",
		"+refs/heads/*:refs/heads/*", "refs/heads/topic:refs/heads/topic",
	} {
		f.git(local, "config", "--replace-all", "remote.origin.fetch", tracking)
		f.git(local, "config", "--add", "remote.origin.fetch", mapping)
		wantError(t, r.Fetch(testContext), "local branches")
		wantError(t, r.Pull(testContext, FastForward), "local branches")
		equal(t, f.git(local, "rev-parse", "refs/heads/topic"), before)
		equal(t, statusOf(t, r).Head, before)
		equal(t, f.git(local, "config", "--get-all", "remote.origin.fetch"), tracking+"\n"+mapping)
	}
	f.git(writer, "tag", "topic-tag", next)
	f.git(writer, "push", "origin", "refs/tags/topic-tag")
	f.git(local, "config", "--replace-all", "remote.origin.fetch", tracking)
	f.git(local, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	mustOK(t, r.Fetch(testContext))
	equal(t, f.git(local, "rev-parse", "refs/remotes/origin/topic"), next)
	equal(t, f.git(local, "rev-parse", "refs/tags/topic-tag"), next)
	equal(t, f.git(local, "rev-parse", "refs/heads/topic"), before)
}

func TestPullModesOverrideAmbientPolicy(t *testing.T) {
	for _, mode := range []PullMode{FastForward, Merge, Rebase} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t)
			local, writer, _ := f.paired()
			r := openRepo(t, local)
			f.git(local, "config", "pull.ff", "only")
			if mode == FastForward {
				f.git(local, "config", "pull.ff", "false")
			}
			f.git(local, "config", "pull.rebase", "true")
			if mode == Rebase {
				f.git(local, "config", "pull.rebase", "false")
			}
			f.git(local, "config", "rebase.autoStash", "true")
			f.git(local, "config", "merge.autoStash", "true")
			f.git(local, "config", "rebase.updateRefs", "true")
			fastForward := f.commit(writer, "ff", "ff", "ff")
			f.git(writer, "push", "origin", "main")
			mustOK(t, r.Pull(testContext, mode))
			equal(t, statusOf(t, r).Head, fastForward)
			own := f.commit(local, "own", "own", "own")
			f.git(local, "branch", "retain-local-tip", own)
			upstream := f.commit(writer, "upstream", "upstream", "upstream")
			f.git(writer, "push", "origin", "main")
			err := r.Pull(testContext, mode)
			s := statusOf(t, r)
			if mode == FastForward {
				wantError(t, err, "Pull ff-only failed")
				equal(t, s.Head, own)
			} else {
				mustOK(t, err)
				parents := []string{upstream}
				if mode == Merge {
					parents = []string{own, upstream}
				}
				equal(t, detailsOf(t, r, s.Head).Parents, parents)
			}
			equal(t, f.git(local, "rev-parse", "retain-local-tip"), own)
			equal(t, s.Dirty, false)
			equal(t, s.Operation, "")
		})
	}
}

func TestPullSkippedAndLocalUpstreamsNotAmbientFetchAll(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.git(local, "config", "remote.origin.skipFetchAll", "true")
	f.git(local, "remote", "add", "unrelated", filepath.Join(f.root, "missing"))
	f.git(local, "config", "fetch.all", "true")
	next := f.commit(writer, "next", "next", "next")
	f.git(writer, "push", "origin", "main")
	mustOK(t, r.Pull(testContext, FastForward))
	equal(t, statusOf(t, r).Head, next)
	f.git(local, "switch", "-c", "local-upstream")
	localNext := f.commit(local, "local-next", "local-next", "local-next")
	f.git(local, "switch", "main")
	f.git(local, "config", "branch.main.remote", ".")
	f.git(local, "config", "branch.main.merge", "refs/heads/local-upstream")
	mustOK(t, r.Pull(testContext, FastForward))
	equal(t, statusOf(t, r).Head, localNext)
}

func TestPullPreflightPreservesUserChanges(t *testing.T) {
	f := newFixture(t)
	local, _, _ := f.paired()
	r := openRepo(t, local)
	f.git(local, "config", "rebase.autoStash", "true")
	f.git(local, "config", "merge.autoStash", "true")
	f.write(local, "untracked", "keep me")
	wantError(t, r.Pull(testContext, Merge), "untracked changes")
	f.git(local, "add", "untracked")
	wantError(t, r.Pull(testContext, Rebase), "untracked changes")
	f.git(local, "commit", "-m", "track file")
	f.write(local, "untracked", "modified")
	wantError(t, r.Pull(testContext, FastForward), "untracked changes")
	content, err := os.ReadFile(filepath.Join(local, "untracked"))
	mustOK(t, err)
	equal(t, string(content), "modified")
	f.git(local, "switch", "--detach")
	equal(t, statusOf(t, r).Branch, "")
	wantError(t, r.Pull(testContext, FastForward), "attached")
	f.git(local, "switch", "-c", "no-upstream")
	wantError(t, r.Pull(testContext, FastForward), "upstream")
	wantError(t, r.Pull(testContext, "invalid"), "Unknown pull mode")
	cancelled, cancel := context.WithCancel(testContext)
	cancel()
	wantError(t, r.Pull(cancelled, Merge), "context canceled")
}

func TestConflictPreservationAndLinkedWorktreeOperations(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	f.commit(local, "base", "local conflict", "local")
	f.commit(writer, "base", "upstream conflict", "upstream")
	f.git(writer, "push", "origin", "main")
	wantError(t, r.Pull(testContext, Merge), "merge state is preserved")
	before := statusOf(t, r).Head
	equal(t, statusOf(t, r).Operation, "merge")
	conflict, err := os.ReadFile(filepath.Join(local, "base"))
	mustOK(t, err)
	if !strings.Contains(string(conflict), "<<<<<<<") {
		t.Fatal("conflict file not retained")
	}
	wantError(t, r.Pull(testContext, Rebase), "during merge")
	equal(t, statusOf(t, r).Head, before)
	// Only explicit fixture actions abort. The repository implementation never does.
	f.git(local, "merge", "--abort")
	wantError(t, r.Pull(testContext, Rebase), "rebase state is preserved")
	equal(t, statusOf(t, r).Operation, "rebase")
	f.git(local, "rebase", "--abort")
	linked := filepath.Join(f.root, "linked worktree")
	f.git(local, "worktree", "add", "-b", "linked", linked)
	linkedRepo := openRepo(t, linked)
	for _, marker := range []struct {
		name, operation string
		directory       bool
	}{
		{"CHERRY_PICK_HEAD", "cherry-pick", false}, {"REVERT_HEAD", "revert", false},
		{"sequencer", "sequencer", true}, {"BISECT_START", "bisect", false},
		{"BISECT_LOG", "bisect", false}, {"rebase-apply", "rebase", true}, {"rebase-merge", "rebase", true},
	} {
		path := f.git(linked, "rev-parse", "--path-format=absolute", "--git-path", marker.name)
		if marker.directory {
			mustOK(t, os.Mkdir(path, 0700))
		} else {
			mustOK(t, os.WriteFile(path, []byte(before+"\n"), 0600))
		}
		equal(t, statusOf(t, linkedRepo).Operation, marker.operation)
		wantError(t, linkedRepo.Pull(testContext, FastForward), "during "+marker.operation)
		mustOK(t, os.Remove(path))
	}
}
