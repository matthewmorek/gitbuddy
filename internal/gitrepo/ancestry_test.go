package gitrepo

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeIndentationUsesFrozenAncestryAcrossPagesAndRoots(t *testing.T) {
	f := newFixture(t)
	path := f.repo("ancestry", false)
	base := f.commit(path, "base", "base", "not a merge")
	tree := f.git(path, "rev-parse", "HEAD^{tree}")
	sequence := 0
	commit := func(parents ...string) string {
		sequence++
		// Misleading subjects must not determine membership. A unique suffix
		// gives siblings distinct identities without depending on clock ticks.
		args := []string{"commit-tree", tree, "-m", fmt.Sprintf("Merge misleading %d", sequence)}
		for _, parent := range parents {
			args = append(args, "-p", parent)
		}
		return f.git(path, args...)
	}
	oldSide, oldTrunk := commit(base), commit(base)
	oldMerge := commit(oldTrunk, oldSide)
	trunk, fork := commit(oldMerge), commit(oldMerge)
	innerSide, innerTrunk := commit(fork), commit(fork)
	nested := commit(innerTrunk, innerSide)
	merge := commit(trunk, nested)
	advancedFeature := commit(nested)
	other, orphan := commit(base), commit()
	f.git(path, "update-ref", "refs/heads/main", merge)
	f.git(path, "update-ref", "refs/heads/feature", advancedFeature)
	f.git(path, "update-ref", "refs/heads/other", other)
	f.git(path, "update-ref", "refs/heads/orphan", orphan)
	r := openRepo(t, path)
	want := map[string]int{
		merge: 0, trunk: 0, oldMerge: 0, oldTrunk: 0, oldSide: 1, base: 0,
		nested: 1, innerTrunk: 1, innerSide: 2, fork: 1, other: 0, orphan: 0,
		advancedFeature: 0,
	}
	snapshot := snapshotOf(t, r, All)
	equal(t, snapshot.AncestryWarning, "")
	rows, err := r.History(testContext, snapshot, 0, 100)
	mustOK(t, err)
	depths := map[string]int{}
	for _, row := range rows {
		depths[row.SHA] = row.MergeDepth
	}
	equal(t, depths, want)

	// Moving refs must not reclassify pages of the old generation. The first
	// parent and common ancestors lie outside most of these one-row pages.
	next := commit(merge, other)
	f.git(path, "update-ref", "refs/heads/main", next)
	for i, row := range rows {
		page, err := r.History(testContext, snapshot, i, 1)
		mustOK(t, err)
		equal(t, page, []Commit{row})
	}
	fresh := snapshotOf(t, r, All)
	rows, err = r.History(testContext, fresh, 0, 100)
	mustOK(t, err)
	for _, row := range rows {
		expected := want[row.SHA]
		if row.SHA == other {
			expected = 1
		}
		equal(t, row.MergeDepth, expected)
	}

	// A shallow clone may hide common ancestry on either side. Flatten honestly
	// instead of claiming the truncated first parent's ancestors are absent.
	shallow := filepath.Join(f.root, "shallow")
	f.git(f.root, "clone", "--depth=2", "file://"+path, shallow)
	shallowRepo := openRepo(t, shallow)
	shallowSnapshot := snapshotOf(t, shallowRepo, Current)
	if !strings.Contains(shallowSnapshot.AncestryWarning, "shallow") {
		t.Fatal("shallow ancestry must explain unavailable indentation")
	}
	shallowRows, err := shallowRepo.History(testContext, shallowSnapshot, 0, 100)
	mustOK(t, err)
	for _, row := range shallowRows {
		equal(t, row.MergeDepth, 0)
	}
}

func TestMergeIndentationRejectsIncompleteAncestry(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		name, output, warning string
	}{
		{"missing parent", a + " " + b + "\n", "incomplete"},
		{"partial output", a, "incomplete"},
		{"invalid SHA", "abc\n", "incomplete"},
		{"wrong order", b + "\n" + a + " " + b + "\n", "incomplete"},
		{"cycle", a + " " + b + "\n" + b + " " + a + "\n", "incomplete"},
		{"overflow", strings.Repeat(a+"\n", maxAncestryCommits+1), "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			depths, warning := parseMergeDepths(tc.output)
			equal(t, depths, map[string]int(nil))
			if !strings.Contains(warning, tc.warning) {
				t.Fatalf("missing honest fallback: %q", warning)
			}
		})
	}
}

func TestOctopusMergeSharesSideAncestryWithoutAddingAnotherLevel(t *testing.T) {
	root, trunk, fork := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	side, other, merge := strings.Repeat("d", 40), strings.Repeat("e", 40), strings.Repeat("f", 40)
	out := fmt.Sprintf("%s %s %s %s\n%s %s\n%s %s\n%s %s\n%s %s\n%s\n",
		merge, trunk, side, other, other, fork, side, fork, fork, root, trunk, root, root)
	depths, warning := parseMergeDepths(out)
	equal(t, warning, "")
	equal(t, depths, map[string]int{merge: 0, trunk: 0, root: 0, side: 1, other: 1, fork: 1})
}

func TestIndependentMergeTipsDoNotFlattenEachOthersMembers(t *testing.T) {
	root, a, b := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	x, y := strings.Repeat("d", 40), strings.Repeat("e", 40)
	out := fmt.Sprintf("%s %s %s\n%s %s %s\n%s %s\n%s %s\n%s\n",
		x, a, b, y, b, a, b, root, a, root, root)
	depths, warning := parseMergeDepths(out)
	equal(t, warning, "")
	equal(t, depths, map[string]int{x: 0, y: 0, a: 1, b: 1, root: 0})
}

func TestMergeIndentationBoundsWorkAcrossManyIndependentTips(t *testing.T) {
	const chain = 1000
	var out strings.Builder
	sha := func(n int) string { return fmt.Sprintf("%040x", n) }
	for i := 0; i <= maxAncestryVisits/chain; i++ {
		fmt.Fprintf(&out, "%s %s\n", sha(chain+1+i), sha(chain))
	}
	for i := chain; i > 3; i-- {
		fmt.Fprintf(&out, "%s %s\n", sha(i), sha(i-1))
	}
	fmt.Fprintf(&out, "%s %s %s\n%s\n%s\n", sha(3), sha(2), sha(1), sha(2), sha(1))
	depths, warning := parseMergeDepths(out.String())
	equal(t, depths, map[string]int(nil))
	if !strings.Contains(warning, "work limit") {
		t.Fatalf("expected bounded, honest fallback, got %q", warning)
	}
}
