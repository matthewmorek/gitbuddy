package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// StashPreview describes immutable saved trees, not the current checkout.
// Index contains differences from the saved worktree for paths with staged
// changes, so staged changes already visible in Tracked are not counted again.
type StashPreview struct {
	Tracked, Untracked, Index []ChangedFile
}

func (r *Repository) stashParents(ctx context.Context, id string, noLazyFetch bool) ([]string, error) {
	if err := validateSHA(id); err != nil {
		return nil, err
	}
	parentsOf := func(sha string) ([]string, error) {
		args := []string{"--no-replace-objects", "cat-file", "-p", sha + "^{commit}"}
		if noLazyFetch {
			args = append([]string{"--no-lazy-fetch"}, args...)
		}
		out, err := r.git(ctx, "", args...)
		if err != nil {
			return nil, fmt.Errorf("Cannot read the saved stash commit %s: %w", sha, err)
		}
		var parents []string
		headers, _, _ := strings.Cut(out, "\n\n")
		for _, header := range strings.Split(headers, "\n") {
			if p, ok := strings.CutPrefix(header, "parent "); ok {
				if validateSHA(p) != nil {
					return nil, errors.New("The stash has invalid saved parents.")
				}
				parents = append(parents, p)
			}
		}
		return parents, nil
	}
	p, err := parentsOf(id)
	if err != nil {
		return nil, err
	}
	unsupported := errors.New("Unsupported stash commit: expected a base, its saved index, and an optional untracked root.")
	if len(p) != 2 && len(p) != 3 {
		return nil, unsupported
	}
	index, err := parentsOf(p[1])
	if err != nil {
		return nil, err
	}
	if len(index) != 1 || index[0] != p[0] {
		return nil, unsupported
	}
	if len(p) == 3 {
		untracked, err := parentsOf(p[2])
		if err != nil {
			return nil, err
		}
		if len(untracked) != 0 {
			return nil, unsupported
		}
	}
	return p, nil
}

// StashPreview lists status, rename and line statistics from saved trees.
func (r *Repository) StashPreview(ctx context.Context, id string) (StashPreview, error) {
	if _, err := r.git(ctx, "", "--no-lazy-fetch", "--attr-source", "HEAD", "rev-parse", "--git-dir"); err != nil {
		if ctx.Err() != nil {
			return StashPreview{}, ctx.Err()
		}
		return StashPreview{}, errors.New("Stash preview needs Git with --no-lazy-fetch and --attr-source support (Git 2.45 or later).")
	}
	p, err := r.stashParents(ctx, id, true)
	if err != nil {
		return StashPreview{}, err
	}
	diff := func(attributes string) []string {
		// Use attributes from the destination tree, not current checkout files.
		return []string{"--no-replace-objects", "--no-lazy-fetch", "--attr-source", attributes, "diff-tree",
			"--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--no-commit-id", "-r", "-z"}
	}
	preview := StashPreview{}
	tracked, err := r.changedFiles(ctx, append(diff(id), "-M"), []string{p[0], id, "--"})
	if err != nil {
		return preview, fmt.Errorf("Cannot compare saved stash trees: %w", err)
	}
	preview.Tracked = tracked
	if len(p) == 3 {
		preview.Untracked, err = r.changedFiles(ctx, append(diff(p[2]), "--root", "--no-renames"), []string{p[2], "--"})
		if err != nil {
			return StashPreview{}, err
		}
	}
	// Only paths changed in the saved index belong in the alternate version.
	// Disable renames for this membership read to retain both endpoints.
	out, err := r.git(ctx, "", append(diff(p[1]), "--name-only", "--no-renames", p[0], p[1], "--")...)
	if err != nil {
		return StashPreview{}, err
	}
	staged, err := fields(out)
	if err != nil || len(staged) == 0 {
		return preview, err
	}
	paths := make(map[string]bool, len(staged))
	for _, path := range staged {
		paths[path] = true
	}
	compareIndex := func(rename string) ([]ChangedFile, error) {
		return r.changedFiles(ctx, append(diff(p[1]), rename), []string{id, p[1], "--"})
	}
	index, err := compareIndex("-M")
	if err != nil {
		return StashPreview{}, err
	}
	// A W→I rename can otherwise borrow an unrelated unstaged path as one
	// endpoint. Show such a comparison without pairing unrelated paths.
	if slices.ContainsFunc(index, func(f ChangedFile) bool {
		return f.OldPath != "" && paths[f.OldPath] != paths[f.Path]
	}) {
		index, err = compareIndex("--no-renames")
		if err != nil {
			return StashPreview{}, err
		}
	}
	preview.Index = slices.DeleteFunc(index, func(f ChangedFile) bool { return !paths[f.Path] })
	return preview, nil
}
