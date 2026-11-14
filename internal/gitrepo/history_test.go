package gitrepo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUpstreamAheadBehindAndEqual(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	r := openRepo(t, local)
	base := f.git(local, "rev-parse", "HEAD")
	snapshot := snapshotOf(t, r, Current)
	equal(t, snapshot.Roots, []string{base})
	equal(t, snapshot.Relations, map[string]CommitRelation{})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, &UpstreamComparison{})
	equal(t, snapshot.Branch, "main")
	equal(t, snapshot.Head, base)

	first := f.commit(writer, "incoming", "one", "incoming one")
	second := f.commit(writer, "incoming", "two", "incoming two")
	f.git(writer, "push", "origin", "main")
	f.git(local, "fetch", "origin")
	snapshot = snapshotOf(t, r, Current)
	equal(t, snapshot.Roots, []string{base, second})
	equal(t, snapshot.Relations, map[string]CommitRelation{first: Incoming, second: Incoming})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, &UpstreamComparison{Behind: 2})
	rows, err := r.History(testContext, snapshot, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 3)
	equal(t, rows[0].SHA, second)
	equal(t, rows[0].Relation, Incoming)
	equal(t, rows[1].Relation, Incoming)
	equal(t, rows[2].Relation, Unmarked)

	f.git(local, "merge", "--ff-only", "refs/remotes/origin/main")
	firstLocal := f.commit(local, "local", "one", "local one")
	secondLocal := f.commit(local, "local", "two", "local two")
	snapshot = snapshotOf(t, r, Current)
	equal(t, snapshot.Relations, map[string]CommitRelation{firstLocal: LocalOnly, secondLocal: LocalOnly})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, &UpstreamComparison{Ahead: 2})
	rows, err = r.History(testContext, snapshot, 0, 1)
	mustOK(t, err)
	equal(t, rows[0].Relation, LocalOnly)
}

func TestDivergedHistoryFreezesMembershipAcrossFetchAndPages(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	base := f.git(local, "rev-parse", "HEAD")
	firstIncoming := f.commit(writer, "incoming", "one", "incoming one")
	secondIncoming := f.commit(writer, "incoming", "two", "incoming two")
	f.git(writer, "push", "origin", "main")
	f.git(local, "fetch", "origin")
	firstLocal := f.commit(local, "local", "one", "local one")
	secondLocal := f.commit(local, "local", "two", "local two")
	f.git(local, "switch", "-c", "other", base)
	other := f.commit(local, "other", "other", "other branch")
	f.git(local, "update-ref", "refs/remotes/elsewhere/main", other)
	f.git(local, "switch", "main")
	r := openRepo(t, local)
	wantRelations := map[string]CommitRelation{
		firstIncoming: Incoming, secondIncoming: Incoming,
		firstLocal: LocalOnly, secondLocal: LocalOnly,
	}
	snapshot := snapshotOf(t, r, Current)
	equal(t, snapshot.Roots, []string{secondLocal, secondIncoming})
	equal(t, snapshot.Relations, wantRelations)
	equal(t, snapshot.Diverged, true)
	equal(t, snapshot.Comparison, &UpstreamComparison{Ahead: 2, Behind: 2})
	all := snapshotOf(t, r, All)
	equal(t, all.Relations, wantRelations)
	equal(t, all.Diverged, true)
	equal(t, all.Comparison, snapshot.Comparison)
	rows, err := r.History(testContext, all, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 6)
	foundOther := false
	for _, row := range rows {
		if row.SHA == other {
			foundOther = true
			equal(t, row.Relation, Unmarked)
		}
		if row.SHA == base {
			equal(t, row.Relation, Unmarked)
		}
	}
	equal(t, foundOther, true)

	// Read the frozen generation after both roots move, including a real
	// fixture-only fetch. Each one-row page must retain its old membership.
	thirdIncoming := f.commit(writer, "incoming", "three", "incoming three")
	f.git(writer, "push", "origin", "main")
	f.git(local, "fetch", "origin")
	thirdLocal := f.commit(local, "local", "three", "local three")
	seen := map[string]CommitRelation{}
	for offset := 0; ; offset++ {
		page, err := r.History(testContext, snapshot, offset, 1)
		mustOK(t, err)
		if len(page) == 0 {
			break
		}
		equal(t, len(page), 1)
		row := page[0]
		if _, duplicate := seen[row.SHA]; duplicate {
			t.Fatalf("duplicate commit across pages: %s", row.SHA)
		}
		seen[row.SHA] = row.Relation
		if row.SHA == secondIncoming {
			equal(t, row.Labels, []string{"origin/HEAD", "origin/main"})
		}
		if row.SHA == secondLocal {
			equal(t, row.Labels, []string{"main", "HEAD"})
		}
	}
	equal(t, seen, map[string]CommitRelation{
		base: Unmarked, firstIncoming: Incoming, secondIncoming: Incoming,
		firstLocal: LocalOnly, secondLocal: LocalOnly,
	})
	equal(t, snapshot.Roots, []string{secondLocal, secondIncoming})
	equal(t, snapshot.Relations, wantRelations)
	equal(t, snapshot.Comparison, &UpstreamComparison{Ahead: 2, Behind: 2})
	fresh := snapshotOf(t, r, Current)
	equal(t, snapshot.Head, secondLocal)
	equal(t, fresh.Head, thirdLocal)
	equal(t, fresh.Roots, []string{thirdLocal, thirdIncoming})
	equal(t, fresh.Relations[thirdIncoming], Incoming)
	equal(t, fresh.Relations[thirdLocal], LocalOnly)
	equal(t, len(fresh.Relations), 6)
	equal(t, fresh.Comparison, &UpstreamComparison{Ahead: 3, Behind: 3})
}

func TestHistoryWithoutAvailableUpstream(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	base := f.git(local, "rev-parse", "HEAD")
	incoming := f.commit(writer, "incoming", "one", "incoming")
	f.git(writer, "push", "origin", "main")
	f.git(local, "fetch", "origin")
	r := openRepo(t, local)
	f.git(local, "switch", "--detach", base)
	snapshot := snapshotOf(t, r, Current)
	equal(t, snapshot.Branch, "")
	equal(t, snapshot.Head, base)
	equal(t, snapshot.Roots, []string{base})
	equal(t, snapshot.Relations, map[string]CommitRelation{})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, (*UpstreamComparison)(nil))

	f.git(local, "switch", "main")
	f.git(local, "update-ref", "-d", "refs/remotes/origin/main")
	snapshot = snapshotOf(t, r, Current)
	equal(t, snapshot.Branch, "main")
	equal(t, snapshot.Head, base)
	equal(t, snapshot.Roots, []string{base})
	equal(t, snapshot.Relations, map[string]CommitRelation{})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, (*UpstreamComparison)(nil))
	rows, err := r.History(testContext, snapshot, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 1)
	equal(t, rows[0].Relation, Unmarked)

	// An unborn branch may still have configured tracking and other refs.
	f.git(local, "update-ref", "refs/remotes/origin/main", incoming)
	f.git(local, "switch", "--orphan", "unborn")
	f.git(local, "config", "branch.unborn.remote", "origin")
	f.git(local, "config", "branch.unborn.merge", "refs/heads/main")
	snapshot = snapshotOf(t, r, Current)
	equal(t, snapshot.Branch, "unborn")
	equal(t, snapshot.Head, "")
	equal(t, snapshot.Roots, []string{})
	equal(t, snapshot.Relations, map[string]CommitRelation{})
	equal(t, snapshot.Diverged, false)
	equal(t, snapshot.Comparison, (*UpstreamComparison)(nil))
	rows, err = r.History(testContext, snapshot, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 0)
	all := snapshotOf(t, r, All)
	equal(t, slices.Contains(all.Roots, incoming), true)
	equal(t, all.Relations, map[string]CommitRelation{})
}

func TestLocalAndCustomUpstreamWithSHA256(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.root, "sha256")
	mustOK(t, os.Mkdir(path, 0700))
	f.git(path, "init", "--initial-branch=main", "--object-format=sha256")
	base := f.commit(path, "base", "base", "base")
	f.git(path, "switch", "-c", "origin/main")
	incoming := f.commit(path, "incoming", "incoming", "incoming")
	f.git(path, "switch", "main")
	local := f.commit(path, "local", "local", "local")
	// These two refs have identical display labels but only the local branch
	// is configured upstream. The remote-tracking tip is deliberately shared.
	f.git(path, "update-ref", "refs/remotes/origin/main", base)
	r := openRepo(t, path)
	noUpstream := snapshotOf(t, r, Current)
	equal(t, noUpstream.Roots, []string{local})
	equal(t, noUpstream.Relations, map[string]CommitRelation{})
	equal(t, noUpstream.Comparison, (*UpstreamComparison)(nil))
	f.git(path, "branch", "--set-upstream-to=refs/heads/origin/main", "main")
	snapshot := snapshotOf(t, r, Current)
	equal(t, len(local), 64)
	equal(t, snapshot.Roots, []string{local, incoming})
	equal(t, snapshot.Relations, map[string]CommitRelation{local: LocalOnly, incoming: Incoming})
	equal(t, snapshot.Diverged, true)
	rows, err := r.History(testContext, snapshot, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 3)
	for _, row := range rows {
		equal(t, row.Relation, snapshot.Relations[row.SHA])
	}

	// Remote refspecs may store their upstream outside refs/remotes/.
	f.git(path, "config", "remote.custom.url", path)
	f.git(path, "config", "remote.custom.fetch", "+refs/heads/*:refs/upstream/*")
	f.git(path, "update-ref", "refs/upstream/main", incoming)
	f.git(path, "config", "branch.main.remote", "custom")
	f.git(path, "config", "branch.main.merge", "refs/heads/main")
	f.git(path, "switch", "-c", "custom-other", base)
	unrelated := f.commit(path, "other", "other", "unrelated custom ref")
	f.git(path, "update-ref", "refs/upstream/unrelated", unrelated)
	f.git(path, "switch", "main")
	f.git(path, "branch", "-D", "custom-other", "origin/main")
	// Neither custom tip now has a branch, remote-tracking or tag alias.
	// Both scopes include only the configured upstream, not arbitrary refs.
	for _, scope := range []Scope{Current, All} {
		t.Run(string(scope), func(t *testing.T) {
			custom := snapshotOf(t, r, scope)
			equal(t, slices.Contains(custom.Roots, incoming), true)
			equal(t, slices.Contains(custom.Roots, unrelated), false)
			equal(t, custom.Relations, snapshot.Relations)
			equal(t, custom.Diverged, true)
			equal(t, custom.Labels[incoming], []string(nil))
			rows, err := r.History(testContext, custom, 0, 20)
			mustOK(t, err)
			seen := map[string]CommitRelation{}
			for _, row := range rows {
				seen[row.SHA] = row.Relation
			}
			equal(t, seen, map[string]CommitRelation{base: Unmarked, local: LocalOnly, incoming: Incoming})
		})
	}
}

func TestUnrelatedUpstreamHistories(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	local := f.commit(path, "local", "local", "local root")
	f.git(path, "switch", "--orphan", "upstream")
	incoming := f.commit(path, "incoming", "incoming", "incoming root")
	f.git(path, "switch", "main")
	f.git(path, "branch", "--set-upstream-to=upstream", "main")
	r := openRepo(t, path)
	snapshot := snapshotOf(t, r, Current)
	equal(t, snapshot.Roots, []string{local, incoming})
	equal(t, snapshot.Relations, map[string]CommitRelation{local: LocalOnly, incoming: Incoming})
	equal(t, snapshot.Diverged, true)
	rows, err := r.History(testContext, snapshot, 0, 20)
	mustOK(t, err)
	equal(t, len(rows), 2)
}

func TestUpstreamRelationUsesAncestryNotPatchEquivalence(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	f.commit(path, "base", "base", "base")
	f.git(path, "switch", "-c", "upstream")
	incoming := f.commit(path, "change", "same patch", "upstream change")
	f.git(path, "switch", "main")
	f.git(path, "cherry-pick", incoming)
	f.git(path, "commit", "--amend", "-m", "local equivalent")
	local := f.git(path, "rev-parse", "HEAD")
	f.git(path, "branch", "--set-upstream-to=upstream", "main")
	snapshot := snapshotOf(t, openRepo(t, path), Current)
	equal(t, snapshot.Relations, map[string]CommitRelation{local: LocalOnly, incoming: Incoming})
	equal(t, snapshot.Diverged, true)
}

func TestCommitRelationsRejectPartialOrInvalidMetadata(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, output, errorText string
	}{
		{"incomplete", ">" + sha, "incomplete"},
		{"abbreviated SHA", ">abc\n", "invalid"},
		{"unexpected side", "-" + sha + "\n", "side"},
		{"overflow", strings.Repeat(">"+sha+"\n", maxRelationCommits+1), "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relations, diverged, err := parseCommitRelations(tc.output)
			wantError(t, err, tc.errorText)
			equal(t, relations, map[string]CommitRelation(nil))
			equal(t, diverged, false)
		})
	}
}
