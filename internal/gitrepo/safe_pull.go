package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

type safeUpstream struct{ remote, tracking, mapping, url string }

// Disallow local and overlapping remote names, option-like names and ad hoc
// refspecs. A single canonical branch mapping makes prune's write set explicit.
var safeRemoteName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

func (r *Repository) safeUpstream(ctx context.Context, s Status) (safeUpstream, error) {
	unsafe := errors.New("Safe pull requires one named upstream remote with a standard branch-tracking refspec. Review the upstream and fetch mappings outside Gitbuddy.")
	names, err := r.config(ctx, "branch."+s.Branch+".remote")
	if err != nil {
		return safeUpstream{}, err
	}
	branches, err := r.config(ctx, "branch."+s.Branch+".merge")
	if err != nil {
		return safeUpstream{}, err
	}
	if len(names) != 1 || !safeRemoteName.MatchString(names[0]) || len(branches) != 1 ||
		!strings.HasPrefix(branches[0], "refs/heads/") {
		return safeUpstream{}, unsafe
	}
	name := names[0]
	prefix := "refs/remotes/" + name + "/"
	want := safeUpstream{remote: name, tracking: prefix + strings.TrimPrefix(branches[0], "refs/heads/"), mapping: "+refs/heads/*:" + prefix + "*"}
	if want.tracking == prefix+"HEAD" {
		return safeUpstream{}, unsafe
	}
	if _, err := r.git(ctx, "", "check-ref-format", want.tracking); err != nil {
		return safeUpstream{}, unsafe
	}
	remotes, err := r.remotes(ctx)
	if err != nil {
		return safeUpstream{}, err
	}
	found := false
	for _, remote := range remotes {
		if remote.name == name {
			found = len(remote.mappings) == 1 && remote.mappings[0] == want.mapping
			continue
		}
		if strings.HasPrefix(remote.name, name+"/") {
			return safeUpstream{}, unsafe
		}
		for _, mapping := range remote.mappings {
			_, destination, _ := strings.Cut(mapping, ":")
			if head, _, glob := strings.Cut(destination, "*"); strings.HasPrefix(head, prefix) ||
				glob && strings.HasPrefix(prefix, head) {
				return safeUpstream{}, unsafe
			}
		}
	}
	if !found {
		return safeUpstream{}, unsafe
	}
	for _, key := range []string{"fetch.bundleURI", "extensions.partialClone"} {
		values, err := r.config(ctx, key)
		if err != nil {
			return safeUpstream{}, err
		}
		if len(values) != 0 {
			return safeUpstream{}, errors.New("Safe pull does not support bundle URIs or partial clones.")
		}
	}
	for _, key := range []string{"remote." + name + ".mirror", "remote." + name + ".promisor"} {
		out, code, err := execute(ctx, r.root, nil, "", "config", "--bool", "--get", key)
		if err != nil {
			return safeUpstream{}, err
		}
		if code != 1 && (code != 0 || strings.TrimSpace(out) != "false") {
			return safeUpstream{}, errors.New("Safe pull does not support mirror or promisor remotes.")
		}
	}
	url, err := r.git(ctx, "", "remote", "get-url", "--all", "--", name)
	if err != nil || strings.Count(url, "\n") != 1 || strings.TrimSpace(url) == "" {
		return safeUpstream{}, errors.New("Safe pull requires a named remote with one fetch URL.")
	}
	want.url = url // Compare effective URLs across preflights; never display them.
	refs, err := r.git(ctx, "", "for-each-ref", "--format=%(refname)%09%(symref)", prefix)
	if err != nil {
		return safeUpstream{}, err
	}
	for _, ref := range strings.Split(refs, "\n") {
		path, target, _ := strings.Cut(ref, "\t")
		if target != "" && (path != prefix+"HEAD" || !strings.HasPrefix(target, prefix)) {
			return safeUpstream{}, errors.New("Safe pull found an unsafe symbolic tracking ref. Review tracking refs outside Gitbuddy.")
		}
	}
	return want, nil
}

// ownSafeTree confirms we can still integrate or restore into the same clean
// checkout. This is a revalidation, not a lock against other Git clients.
func (r *Repository) ownSafeTree(ctx context.Context, branch, head string) error {
	s, err := r.stashable(ctx)
	if err != nil {
		return err
	}
	if s.Branch != branch || s.Head != head || s.Dirty {
		return errors.New("The checkout, HEAD or working copy changed. Automatic integration or restoration would not be safe.")
	}
	return nil
}

// SafePull prunes only the upstream remote's ordinary tracking namespace and
// integrates only a fast-forward. backupID identifies any backup not confirmed
// removed; a successful --index restoration retires only the owned private ref.
func (r *Repository) SafePull(ctx context.Context, autoStash bool) (backupID string, err error) {
	before, err := r.stashable(ctx)
	if err != nil {
		return "", err
	}
	if before.Branch == "" {
		return "", errors.New("Safe pull requires an attached, committed branch.")
	}
	if before.Dirty && !autoStash {
		return "", errors.New("Safe pull requires a clean working copy when automatic stashing is off.")
	}
	upstream, err := r.safeUpstream(ctx, before)
	if err != nil {
		return "", err
	}
	env, err := r.mutationEnvironment(ctx)
	if err != nil {
		return "", err
	}
	current, err := r.stashable(ctx)
	if err != nil {
		return "", err
	}
	if current.Branch != before.Branch || current.Head != before.Head ||
		current.Dirty != before.Dirty || !slices.Equal(current.Entries, before.Entries) {
		return "", errors.New("The checkout changed during Safe pull preflight. Refresh and retry.")
	}
	if checked, err := r.safeUpstream(ctx, current); err != nil {
		return "", err
	} else if checked != upstream {
		return "", errors.New("The upstream changed during Safe pull preflight. Refresh and retry.")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx = context.WithoutCancel(ctx) // Once mutation starts, ordinary quit waits.
	if before.Dirty {
		guard, err := r.lockSafeBackup(ctx)
		if err != nil {
			return "", err
		}
		defer guard.Close()
		backupID, err = r.saveAndCleanStash(ctx, before, nil)
		if err != nil {
			if backupID != "" {
				return backupID, fmt.Errorf("Safe pull stopped before fetch: %w Check Working copy and Stashes for backup %s before retrying.", err, backupID)
			}
			return "", fmt.Errorf("Safe pull could not prepare a backup: %w", err)
		}
	}

	head, pullErr := r.fetchAndFastForward(ctx, env, before, upstream)
	if backupID == "" {
		return "", pullErr
	}
	if head == "" {
		return backupID, fmt.Errorf("%w Backup %s was not reapplied. Review Working copy and Stashes.", pullErr, backupID)
	}
	restoreErr := r.restoreSafeBackup(ctx, before.Branch, head, backupID, pullErr)
	if restoreErr != nil {
		return backupID, restoreErr
	}
	if pullErr != nil {
		return "", fmt.Errorf("%w Local changes were restored.", pullErr)
	}
	return "", nil
}

// The returned HEAD is empty if the checkout cannot be trusted for restoration.
func (r *Repository) fetchAndFastForward(ctx context.Context, env []string, before Status, upstream safeUpstream) (string, error) {
	oldHead := before.Head
	if err := r.ownSafeTree(ctx, before.Branch, oldHead); err != nil {
		return "", err
	}
	checked, err := r.safeUpstream(ctx, before)
	if err != nil {
		return oldHead, err
	}
	if checked != upstream {
		return oldHead, errors.New("Safe pull upstream changed before fetch; no integration was attempted.")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	// Explicit refspec/refmap and tag, recursion and maintenance overrides keep
	// fetch from taking on unrelated work or inheriting other write destinations.
	_, code, err := execute(fetchCtx, r.root, env, "", "-c", "fetch.all=false",
		"-c", "fetch.pruneTags=false", "-c", "remote."+upstream.remote+".pruneTags=false",
		"fetch", "--atomic", "--prune", "--no-prune-tags", "--no-tags", "--no-recurse-submodules",
		"--no-auto-maintenance", "--no-write-fetch-head", "--refmap=", "--", upstream.remote,
		upstream.mapping, "^refs/heads/HEAD")
	cancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return oldHead, errors.New("Safe pull fetch timed out after 60 seconds. Check connectivity and authentication.")
		}
		return "", err
	}
	if code < 0 {
		return "", errors.New("Safe pull fetch was interrupted.")
	}
	if code != 0 {
		return oldHead, fmt.Errorf("Safe pull fetch failed (exit %d). Check connectivity and authentication with Git.", code)
	}
	checked, err = r.safeUpstream(ctx, before)
	if err != nil {
		return oldHead, err
	}
	if checked != upstream {
		return oldHead, errors.New("Safe pull upstream changed during fetch; no integration was attempted.")
	}
	// Verify the exact ref; rev-parse can DWIM a different name when the
	// tracked branch was deleted and this fetch just pruned its tracking ref.
	target, err := r.git(ctx, "", "show-ref", "--verify", "--hash", "--", upstream.tracking)
	target = strings.TrimSpace(target)
	if err != nil || validateSHA(target) != nil {
		return oldHead, errors.New("Safe pull upstream is unavailable after fetch. Check whether its branch still exists.")
	}
	tree, err := r.git(ctx, "", "--no-replace-objects", "ls-tree", "-r", "-z", target)
	if err != nil {
		return oldHead, err
	}
	entries, err := fields(tree)
	if err != nil {
		return oldHead, err
	}
	if slices.ContainsFunc(entries, func(entry string) bool { return strings.HasPrefix(entry, "160000 ") }) {
		return oldHead, errors.New("Safe pull upstream contains unsupported submodule entries.")
	}
	expected := target
	_, code, err = execute(ctx, r.root, nil, "", "--no-replace-objects", "merge-base", "--is-ancestor", target, oldHead)
	if err != nil {
		return oldHead, err
	}
	if code == 0 {
		expected = oldHead // Already up to date (possibly locally ahead).
	} else if code != 1 {
		return oldHead, errors.New("Safe pull could not verify upstream ancestry.")
	}
	if err := r.ownSafeTree(ctx, before.Branch, oldHead); err != nil {
		return "", err
	}
	if expected != oldHead {
		_, code, err = execute(ctx, r.root, nil, "", "--no-replace-objects", "merge-base", "--is-ancestor", oldHead, expected)
		if err != nil {
			return oldHead, err
		}
		if code == 1 {
			return oldHead, errors.New("Safe pull fast-forward refused. The branches have diverged; no reset was attempted.")
		}
		if code != 0 {
			return oldHead, errors.New("Safe pull could not verify upstream ancestry.")
		}
		paths := make(map[string]bool)
		if err := r.addTreeChanges(ctx, paths, oldHead, expected); err != nil {
			return oldHead, err
		}
		// Clean conversion can mask raw edits even when status is clean.
		// Check the current tracked files the fast-forward may replace.
		if err := r.verifyWorktreeBlobs(ctx, oldHead, paths); err != nil {
			return oldHead, fmt.Errorf("Cannot verify tracked files byte-for-byte before the fast-forward: %w", err)
		}
	}
	// A branch name can contain '=', so pass the config key and value
	// separately instead of constructing a -c key=value argument.
	mergeEnv := setEnvironment(env, map[string]string{
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "branch." + before.Branch + ".mergeOptions", "GIT_CONFIG_VALUE_0": "",
	})
	_, code, err = execute(ctx, r.root, mergeEnv, "", "--no-replace-objects", "-c", "submodule.recurse=false",
		"merge", "--ff-only", "--no-autostash", "--no-overwrite-ignore", "--", target)
	if err != nil {
		return "", err
	}
	if code != 0 {
		if code < 0 {
			return "", errors.New("Safe pull fast-forward was interrupted. Check the checkout before retrying.")
		}
		return oldHead, fmt.Errorf("Safe pull fast-forward refused (exit %d). Check for divergence or files in the way; no reset was attempted.", code)
	}
	if err := r.ownSafeTree(ctx, before.Branch, expected); err != nil {
		return "", fmt.Errorf("Fast-forward-only integration finished, but the final checkout could not be verified: %w", err)
	}
	return expected, nil
}

func (r *Repository) restoreSafeBackup(ctx context.Context, branch, head, id string, pullErr error) error {
	result := "Fast-forward-only integration finished."
	if pullErr != nil {
		result = pullErr.Error()
	}
	notReapplied := func(err error) error {
		return fmt.Errorf("%s Backup %s was not reapplied: %w Review Working copy and Stashes.", result, id, err)
	}
	parents, err := r.stashParents(ctx, id, false)
	if err != nil {
		return notReapplied(err)
	}
	plan, err := r.planStashRestore(ctx, head, id, parents)
	if err != nil {
		return notReapplied(err)
	}
	expectedIndex := head
	if plan.hasStagedChanges {
		expectedIndex, err = r.expectedRestoredIndex(ctx, head, parents)
		if err != nil {
			return notReapplied(fmt.Errorf("Cannot verify the expected staged contents: %w", err))
		}
	}
	if err := r.ownSafeTree(ctx, branch, head); err != nil {
		return notReapplied(err)
	}
	// Status can be clean even when an editor changed raw bytes during fetch.
	// Check the actual destinations before Git has a chance to replace them.
	if err := r.verifyRestoreDestinations(ctx, head, plan); err != nil {
		return notReapplied(fmt.Errorf("Cannot verify restore destinations byte-for-byte: %w", err))
	}
	if err := r.applyStash(ctx, id); err != nil {
		return fmt.Errorf("%s Could not restore backup %s: %w. Changes may have applied partially. Inspect Working copy and Stashes before retrying.", result, id, err)
	}
	if plan.untracked != "" && r.verifyWorktreeBlobs(ctx, plan.untracked, nil) != nil {
		return fmt.Errorf("%s Could not verify the untracked contents of backup %s after restoration. Review Working copy and Stashes.", result, id)
	}
	if plan.conflicted {
		return fmt.Errorf("%s Could not verify the tracked contents of backup %s after restoration: the predicted merge had conflicts. Review Working copy and Stashes.", result, id)
	}
	if err := r.verifyWorktreeBlobs(ctx, plan.tracked, plan.paths); err != nil {
		return fmt.Errorf("%s Could not verify the tracked contents of backup %s after restoration: %w Review Working copy and Stashes.", result, id, err)
	}
	after, err := r.stashable(ctx)
	if err != nil || after.Branch != branch || after.Head != head {
		return fmt.Errorf("%s The checkout could not be verified after restoring backup %s. Review Working copy and Stashes.", result, id)
	}
	if err := r.verifyIndexTree(ctx, expectedIndex); err != nil {
		return fmt.Errorf("%s Could not verify the staged contents after restoration: %w Backup %s was kept. Inspect Working copy and Stashes before retrying.", result, err, id)
	}
	if _, err := r.git(ctx, "", "update-ref", "--no-deref", "-d", safeBackupRefs+id, id); err != nil {
		return fmt.Errorf("%s Local changes were restored, but backup %s could not be removed safely. Review Stashes.", result, id)
	}
	return nil
}
