package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Stash identifies a listed entry. Recovery distinguishes a named backup from
// the shared stash stack even if both refer to the same saved commit.
type Stash struct {
	ID, Subject string
	Date        time.Time
	Recovery    bool
	stack       *stashStack // snapshot required for manual deletion
	position    int         // valid only within that exact snapshot
}

// SameEntry compares visible identity; callers must not assume it is unique.
func (s Stash) SameEntry(other Stash) bool {
	return s.ID == other.ID && s.Recovery == other.Recovery &&
		s.Subject == other.Subject && s.Date.Equal(other.Date)
}

// Stashes returns the shared stash stack and Safe pull recovery backups, newest first.
func (r *Repository) Stashes(ctx context.Context) ([]Stash, error) {
	// Take filesystem evidence on each side of Git's portable read. An
	// unsupported or changed log is still browsable, but not deletable.
	ref, _ := r.filesStashPath(ctx)
	if ref != "" {
		if err := pendingStashDelete(ref); err != nil {
			return nil, err
		}
	}
	before, _, _ := readStashStack(ref)
	out, err := r.git(ctx, "", "stash", "list", "-z", "--no-notes", "--no-show-signature",
		"--date=raw", "--format=%H%x00%gD%x00%gs")
	if err != nil {
		return nil, err
	}
	records, err := fields(out)
	if err != nil {
		return nil, err
	}
	if len(records)%3 != 0 {
		return nil, errors.New("Git returned an incomplete stash entry.")
	}
	after, _, _ := readStashStack(ref)
	var stack *stashStack
	if before.matches(after) && before.rows == len(records)/3 {
		stack = before
	}
	stashes := make([]Stash, 0, len(records)/3)
	for i := 0; i < len(records); i += 3 {
		selector, ok := strings.CutPrefix(records[i+1], "refs/stash@{")
		date, _, raw := strings.Cut(selector, " ")
		if !ok || !raw {
			return nil, errors.New("Git returned an invalid stash date.")
		}
		entry, err := stashEntry(records[i], date, records[i+2])
		if err != nil {
			return nil, err
		}
		entry.stack, entry.position = stack, i/3
		stashes = append(stashes, entry)
	}
	out, err = r.git(ctx, "", "for-each-ref",
		"--format=%(refname)%00%(symref)%00%(objectname)%00%(committerdate:unix)%00%(subject)%00", safeBackupRefs)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		ref, err := fields(line)
		if err != nil || len(ref) != 5 {
			return nil, errors.New("Git returned an incomplete Safe pull backup entry.")
		}
		if ref[0] != safeBackupRefs+ref[2] || ref[1] != "" {
			continue
		}
		entry, err := stashEntry(ref[2], ref[3], ref[4])
		if err != nil {
			return nil, err
		}
		entry.Recovery = true
		stashes = append(stashes, entry)
	}
	slices.SortStableFunc(stashes, func(a, b Stash) int { return b.Date.Compare(a.Date) })
	return stashes, nil
}

func stashEntry(id, timestamp, subject string) (Stash, error) {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || validateSHA(id) != nil {
		return Stash{}, errors.New("Git returned an invalid stash entry.")
	}
	return Stash{ID: id, Subject: subject, Date: time.Unix(seconds, 0)}, nil
}

// stashable checks states that Git stash cannot faithfully round-trip. Status
// alone can hide clean gitlinks and files marked skip-worktree/assume-unchanged.
func (r *Repository) stashable(ctx context.Context) (Status, error) {
	s, err := r.workingCopyStatus(ctx)
	if err != nil {
		return s, err
	}
	operation, err := r.operation(ctx)
	if err != nil {
		return s, err
	}
	if operation != "" {
		return s, fmt.Errorf("Cannot continue during %s. Finish it outside Gitbuddy.", operation)
	}
	if s.Head == "" {
		return s, errors.New("A committed HEAD is required.")
	}
	index, err := r.git(ctx, "", "ls-files", "-v", "--stage", "-z")
	if err != nil {
		return s, err
	}
	entries, err := fields(index)
	if err != nil {
		return s, err
	}
	for _, entry := range entries {
		header, _, _ := strings.Cut(entry, "\t")
		parts := strings.Fields(header)
		if len(parts) != 4 || len(parts[0]) != 1 {
			return s, errors.New("Git returned an invalid index entry.")
		}
		if parts[3] != "0" {
			return s, errors.New("Unmerged files must be resolved outside Gitbuddy.")
		}
		if parts[1] == "160000" {
			return s, errors.New("Submodule entries are not supported by Safe pull or stash actions.")
		}
		if tag := parts[0][0]; tag == 'S' || tag >= 'a' && tag <= 'z' {
			return s, errors.New("Sparse, skip-worktree or assume-unchanged entries are not supported by Safe pull or stash actions.")
		}
	}
	for _, entry := range s.Entries {
		if entry.Conflict {
			return s, errors.New("Unmerged files must be resolved outside Gitbuddy.")
		}
		if entry.HeadMode == "160000" || strings.HasPrefix(entry.Submodule, "S") {
			return s, errors.New("Submodule entries are not supported by Safe pull or stash actions.")
		}
		if entry.Untracked {
			// apply --index can restore the deletion but leave the old tracked
			// file in the way of the saved untracked replacement.
			if entry.IndexStatus == "D" {
				return s, errors.New("Git stash cannot safely restore a staged deletion with an untracked replacement. Move the replacement aside or resolve the deletion outside Gitbuddy.")
			}
			info, err := os.Lstat(filepath.Join(r.root, entry.Path))
			if err != nil {
				return s, errors.New("An untracked path changed or could not be inspected. Refresh and retry.")
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return s, errors.New("Nested repositories and special untracked files are not supported by Safe pull or stash actions.")
			}
		}
	}
	return s, nil
}

// StashAll saves tracked and untracked changes, excluding ignored files.
func (r *Repository) StashAll(ctx context.Context, note string) error {
	s, err := r.stashable(ctx)
	if err != nil {
		return err
	}
	if !s.Dirty {
		return errors.New("Nothing to stash.")
	}
	changed, err := r.git(ctx, "", "diff", "--name-only", "--no-renames", "-z", s.Head, "--")
	if err != nil {
		return err
	}
	paths, err := fields(changed)
	if err != nil {
		return err
	}
	if err := r.checkStashIgnoredObstructions(ctx, paths); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if common, err := r.filesStashPath(ctx); err == nil {
		if err := pendingStashDelete(common); err != nil {
			return err
		}
	}
	id, err := r.saveAndCleanStash(context.WithoutCancel(ctx), s, &note)
	if err != nil && id != "" {
		return fmt.Errorf("Stash creation or cleanup did not finish: %w Check Working copy and Stashes for %s before retrying.", err, id)
	}
	return err
}

func (r *Repository) applyStash(ctx context.Context, id string) error {
	// stash apply --index invokes git apply --cached. Do not let
	// apply.whitespace=fix rewrite the saved staged bytes.
	_, code, err := execute(ctx, r.root, nil, "", "--no-replace-objects", "-c", "apply.whitespace=nowarn", "stash", "apply", "--index", "--", id)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("stash apply --index failed (exit %d)", code)
	}
	return nil
}

// ApplyStash restores the selected commit (including its index) without dropping
// any entry. Ref/stack provenance is checked separately from commit identity.
func (r *Repository) ApplyStash(ctx context.Context, selected Stash) error {
	parents, err := r.stashParents(ctx, selected.ID, false)
	if err != nil {
		return err
	}
	s, err := r.stashable(ctx)
	if err != nil {
		return err
	}
	stashes, err := r.Stashes(ctx)
	if err != nil {
		return err
	}
	matches, found := 0, false
	for _, s := range stashes {
		if s.ID == selected.ID && s.Recovery == selected.Recovery {
			matches++
			found = found || s.SameEntry(selected)
		}
	}
	if matches > 1 {
		return errors.New("Multiple stack entries use this stash ID. Apply the intended entry with Git.")
	}
	if !found {
		return errors.New("The selected stash is no longer listed. Refresh Stashes and try again.")
	}
	// Git merges into the current index, not necessarily HEAD. Preserve
	// unrelated staged/unstaged work by checking only the actual destinations.
	current := s.Head
	if s.Dirty {
		out, err := r.git(ctx, "", "--no-replace-objects", "write-tree")
		current = strings.TrimSpace(out)
		if err != nil || validateSHA(current) != nil {
			return errors.New("Cannot read the current index tree. The stash was not applied.")
		}
	}
	plan, err := r.planStashRestore(ctx, current, selected.ID, parents)
	if err != nil {
		return fmt.Errorf("Cannot plan safe stash destinations: %w The stash was not applied.", err)
	}
	if plan.conflicted {
		return errors.New("Cannot verify stash destinations for a conflicting merge. The stash was not applied.")
	}
	if err := r.verifyIndexTree(ctx, current); err != nil {
		return fmt.Errorf("Cannot verify the current index: %w The stash was not applied.", err)
	}
	if err := r.verifyRestoreDestinations(ctx, current, plan); err != nil {
		return fmt.Errorf("Cannot verify stash destinations byte-for-byte: %w The stash was not applied.", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.applyStash(context.WithoutCancel(ctx), selected.ID); err != nil {
		return fmt.Errorf("Could not apply stash %s: %w. Gitbuddy did not drop it. Changes may have applied partially; inspect Working copy and resolve outside Gitbuddy before retrying.", selected.ID, err)
	}
	return nil
}
