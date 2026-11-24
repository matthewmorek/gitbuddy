package gitrepo

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const safeBackupRefs = "refs/gitbuddy/safe-pull/"

// saveAndCleanStash publishes the verified snapshot before touching the real
// worktree or index. A nil note publishes to the private Safe pull namespace.
func (r *Repository) saveAndCleanStash(ctx context.Context, before Status, note *string) (string, error) {
	path, err := r.git(ctx, "", "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	index := strings.TrimSuffix(path, "\n")
	info, err := os.Lstat(index)
	indexMode := os.FileMode(0600)
	if err == nil && info.Mode().IsRegular() {
		indexMode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Safe pull requires a regular Git index file.")
	}
	original, err := readSafeIndex(index)
	if err != nil {
		return "", errors.New("Cannot read the Git index.")
	}
	dir, err := os.MkdirTemp(filepath.Dir(index), "gitbuddy-safe-pull-")
	if err != nil {
		return "", errors.New("Cannot create a temporary index for Safe pull.")
	}
	defer os.RemoveAll(dir) // Only this operation's scratch files; never the backup.
	stage, untracked := filepath.Join(dir, "stage"), filepath.Join(dir, "untracked")
	if original != nil {
		if err := os.WriteFile(stage, original, 0600); err != nil {
			return "", errors.New("Cannot copy the Git index.")
		}
	}
	env := environment()
	run := func(at, input string, args ...string) (string, error) {
		out, code, err := execute(ctx, r.root, setEnvironment(env, map[string]string{"GIT_INDEX_FILE": at}),
			input, append([]string{"--no-replace-objects", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false"}, args...)...)
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("Git %s failed (exit %d).", args[0], code)
		}
		return out, nil
	}
	writeTree := func(at string) (string, error) {
		out, err := run(at, "", "write-tree")
		if err != nil {
			return "", err
		}
		out = strings.TrimSpace(out)
		return out, validateSHA(out)
	}
	indexTree, err := writeTree(stage)
	if err != nil {
		return "", err
	}
	indexPaths, err := run(stage, "", "ls-files", "-z")
	if err != nil {
		return "", err
	}
	treePaths, err := r.git(ctx, "", "--no-replace-objects", "ls-tree", "-r", "--name-only", "-z", indexTree)
	if err != nil {
		return "", err
	}
	if treePaths != indexPaths {
		return "", errors.New("Safe pull cannot preserve intent-to-add entries. Stage or remove them outside Gitbuddy first.")
	}
	loose, err := run(stage, "", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	// The copy has a different mtime from the real index. Rebuild without its
	// stat cache so same-size edits made just after staging are rehashed.
	if err := os.Remove(stage); err != nil {
		return "", errors.New("Cannot rebuild Safe pull's temporary index.")
	}
	if _, err := run(stage, "", "read-tree", "--no-sparse-checkout", indexTree); err != nil {
		return "", err
	}
	if _, err := run(stage, "", "add", "-u"); err != nil {
		return "", err
	}
	workTree, err := writeTree(stage)
	if err != nil {
		return "", err
	}
	combinedTree, looseTree := workTree, ""
	if loose != "" {
		if _, err := run(untracked, loose, "update-index", "--add", "-z", "--stdin"); err != nil {
			return "", err
		}
		records, err := run(untracked, "", "ls-files", "--stage", "-z")
		if err != nil {
			return "", err
		}
		if looseTree, err = writeTree(untracked); err != nil {
			return "", err
		}
		if _, err := run(stage, records, "update-index", "--add", "-z", "--index-info"); err != nil {
			return "", err
		}
		if combinedTree, err = writeTree(stage); err != nil {
			return "", err
		}
	}
	// Git's two-tree switch writes only differences from the combined tree
	// to HEAD. Also verify staged-only paths: the snapshot retains their
	// worktree side even when switching back to HEAD leaves it untouched.
	cleanupPaths := make(map[string]bool)
	if err := r.addTreeChanges(ctx, cleanupPaths, before.Head, combinedTree); err != nil {
		return "", err
	}
	savedPaths := maps.Clone(cleanupPaths)
	if err := r.addTreeChanges(ctx, savedPaths, before.Head, workTree, indexTree); err != nil {
		return "", err
	}
	loosePaths, err := fields(loose)
	if err != nil {
		return "", err
	}
	for _, path := range loosePaths {
		savedPaths[path] = true
	}
	commit := func(tree, message string, parents ...string) (string, error) {
		args := []string{"-c", "commit.gpgSign=false", "commit-tree", tree, "-m", message}
		for _, parent := range parents {
			args = append(args, "-p", parent)
		}
		out, err := r.git(ctx, "", args...)
		if err != nil {
			return "", err
		}
		out = strings.TrimSpace(out)
		return out, validateSHA(out)
	}
	staged, err := commit(indexTree, "Index for Gitbuddy stash", before.Head)
	if err != nil {
		return "", err
	}
	parents := []string{before.Head, staged}
	if looseTree != "" {
		looseCommit, err := commit(looseTree, "Untracked files for Gitbuddy stash")
		if err != nil {
			return "", err
		}
		parents = append(parents, looseCommit)
	}
	message := "Gitbuddy Safe pull backup: " + before.Branch + "\n\n" + rand.Text()
	if note != nil {
		branch := before.Branch
		if branch == "" {
			branch = "(no branch)"
		}
		if *note != "" {
			message = "On " + branch + ": " + *note
		} else {
			base, err := r.git(ctx, "", "log", "-1", "--format=%h %s", before.Head)
			if err != nil {
				return "", err
			}
			message = "WIP on " + branch + ": " + strings.TrimSuffix(base, "\n")
		}
	}
	id, err := commit(workTree, message, parents...)
	if err != nil {
		return "", err
	}
	from := id
	if combinedTree != workTree {
		from, err = commit(combinedTree, "Temporary checkout for Gitbuddy Safe pull", id)
		if err != nil {
			return "", err
		}
	}
	checkout := filepath.Join(dir, "checkout")
	checkoutGit, err := r.safeCheckoutGit(ctx, checkout, index, from)
	if err != nil {
		return "", err
	}
	// Hold Git's ordinary index lock only for revalidation and checkout. External
	// file edits are not locked: Git checks freshness before overwriting.
	lockPath := index + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", errors.New("Cannot lock the Git index. Another Git operation may be active.")
	}
	defer os.Remove(lockPath)
	if err := lock.Close(); err != nil {
		return "", errors.New("Cannot close the Git index lock.")
	}
	current, err := r.stashable(ctx)
	if err != nil {
		return "", err
	}
	if current.Branch != before.Branch || current.Head != before.Head ||
		current.Dirty != before.Dirty || !slices.Equal(current.Entries, before.Entries) {
		return "", errors.New("The checkout changed while saving the backup.")
	}
	now, err := readSafeIndex(index)
	if err != nil || (now == nil) != (original == nil) || !bytes.Equal(now, original) {
		return "", errors.New("The Git index changed while saving the backup.")
	}
	// A clean filter can hide raw bytes even from porcelain status. Check
	// the actual files against the snapshot, without running any filters.
	if err := r.verifyWorktreeBlobs(ctx, workTree, savedPaths); err != nil {
		return "", fmt.Errorf("Cannot save tracked files byte-for-byte. Check Git attributes and filters before retrying: %w", err)
	}
	if looseTree != "" {
		if err := r.verifyWorktreeBlobs(ctx, looseTree, nil); err != nil {
			return "", fmt.Errorf("Cannot save untracked files byte-for-byte. Move files affected by Git attributes or filters aside before retrying: %w", err)
		}
	}
	if note != nil {
		if err := r.checkStashIgnoredObstructions(ctx, slices.Collect(maps.Keys(cleanupPaths))); err != nil {
			return "", err
		}
		if _, err := r.git(ctx, "", "stash", "store", "-m", message, id); err != nil {
			return id, err
		}
	} else if _, err := r.git(ctx, "", "update-ref", "--no-deref", safeBackupRefs+id, id, strings.Repeat("0", len(id))); err != nil {
		return id, err // A subprocess failure can leave publication uncertain.
	}
	if _, err := checkoutGit("read-tree", combinedTree); err != nil {
		return id, err
	}
	if _, err := checkoutGit("update-index", "--really-refresh"); err != nil {
		return id, err
	}
	// Recheck raw bytes after refreshing the temporary checkout's stat cache.
	if err := r.verifyWorktreeBlobs(ctx, combinedTree, savedPaths); err != nil {
		return id, fmt.Errorf("The working copy changed before cleanup: %w", err)
	}
	if _, err := checkoutGit("switch", "--detach", "--no-overwrite-ignore", "--no-recurse-submodules", before.Head); err != nil {
		return id, err
	}
	// Rename directly to the index; do not put an installable index at the
	// .lock path if interrupted. Preserve the ordinary index's permissions.
	output := filepath.Join(checkout, "index")
	tree, err := checkoutGit("write-tree")
	if err != nil {
		return id, err
	}
	baseTree, err := r.git(ctx, "", "--no-replace-objects", "rev-parse", before.Head+"^{tree}")
	if err != nil || tree != baseTree {
		return id, errors.New("Cannot verify the clean index. Check Working copy and Stashes.")
	}
	if os.Chmod(output, indexMode) != nil || os.Rename(output, index) != nil {
		return id, errors.New("Cannot install the clean index. Check Working copy and Stashes.")
	}
	return id, nil
}

func readSafeIndex(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // Git treats a missing index as empty as well.
	}
	return data, err
}

// A private detached HEAD and index let Git switch from the captured snapshot
// to the base without changing the actual branch or touching equal entries. Unlike
// read-tree -u, switch --no-overwrite-ignore protects ignored obstructions too.
func (r *Repository) safeCheckoutGit(ctx context.Context, dir, index, from string) (func(...string) (string, error), error) {
	common, err := r.git(ctx, "", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	format, err := r.config(ctx, "extensions.refStorage")
	if err != nil {
		return nil, err
	}
	reftable := len(format) == 1 && format[0] == "reftable"
	if reftable {
		objectFormat := "sha1"
		if len(from) == 64 {
			objectFormat = "sha256"
		}
		if _, err := r.git(ctx, "", "init", "--bare", "--template=", "--ref-format=reftable", "--object-format="+objectFormat, dir); err != nil {
			return nil, err
		}
	} else {
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, errors.New("Cannot create Safe pull's private checkout directory.")
		}
		if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte(from+"\n"), 0600); err != nil {
			return nil, errors.New("Cannot set up Safe pull's private checkout.")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "commondir"), []byte(common), 0600); err != nil {
		return nil, errors.New("Cannot link Safe pull's private checkout to the object store.")
	}
	worktreeConfig := filepath.Join(filepath.Dir(index), "config.worktree")
	if _, err := os.Stat(worktreeConfig); err == nil {
		if _, err := r.git(ctx, "", "config", "--file", filepath.Join(dir, "config.worktree"), "include.path", worktreeConfig); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Cannot inspect the worktree's Git configuration.")
	}
	env := setEnvironment(environment(), map[string]string{"GIT_DIR": dir, "GIT_WORK_TREE": r.root})
	run := func(args ...string) (string, error) {
		out, code, err := execute(ctx, r.root, env, "", append([]string{
			"--no-replace-objects", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false", "-c", "core.sparseCheckout=false",
			"-c", "core.hooksPath=" + os.DevNull,
		}, args...)...)
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("Git %s refused to prepare the clean checkout (exit %d).", args[0], code)
		}
		return out, nil
	}
	if reftable {
		if _, err := run("update-ref", "--no-deref", "HEAD", from); err != nil {
			return nil, err
		}
	}
	return run, nil
}

// A nil path set verifies every entry; otherwise verify only relevant blobs.
func (r *Repository) verifyWorktreeBlobs(ctx context.Context, tree string, paths map[string]bool) error {
	return r.verifyWorktreePaths(ctx, tree, paths, false, nil)
}

func (r *Repository) verifyWorktreePaths(ctx context.Context, tree string, paths map[string]bool, checkObstructions bool, preservedDirs map[string]bool) error {
	if paths != nil && len(paths) == 0 {
		return nil
	}
	args := []string{"--no-replace-objects", "ls-tree", "-r", "-z"}
	if len(preservedDirs) > 0 {
		args = append(args, "-t")
	}
	out, err := r.git(ctx, "", append(args, tree)...)
	if err != nil {
		return err
	}
	entries, err := fields(out)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(r.root)
	if err != nil {
		return errors.New("Cannot verify files within the working tree.")
	}
	defer root.Close()
	var missing map[string]bool
	if checkObstructions {
		missing = maps.Clone(paths)
	}
	headDirs := make(map[string]bool)
	for _, entry := range entries {
		header, name, _ := strings.Cut(entry, "\t")
		if paths != nil && !paths[name] {
			continue
		}
		parts := strings.Fields(header)
		if len(parts) == 3 && parts[1] == "tree" {
			if preservedDirs[name] {
				headDirs[name] = true
			}
			continue
		}
		if len(parts) != 3 || parts[1] != "blob" || validateSHA(parts[2]) != nil {
			return errors.New("A saved entry is not a supported Git blob.")
		}
		if dir := path.Dir(name); checkObstructions && dir != "." {
			if err := checkRestorePath(root, dir, true); err != nil {
				return err
			}
		}
		if !matchesSafeBlob(root, name, parts[0], parts[2]) {
			return fmt.Errorf("File contents or type do not match the saved blob: %q.", name)
		}
		delete(missing, name)
	}
	for _, name := range slices.Sorted(maps.Keys(missing)) {
		if err := checkRestorePath(root, name, headDirs[name]); err != nil {
			return err
		}
	}
	return nil
}

// A clean status can hide ignored files. Check each component from the root
// down so a symlink or file in the way is rejected, never traversed. A
// directory at the destination is also an obstruction unless Git's trees
// prove that the directory is not itself a restore target.
func checkRestorePath(root *os.Root, name string, preserveDir bool) error {
	prefix := ""
	for component := range strings.SplitSeq(name, "/") {
		prefix = path.Join(prefix, component)
		info, err := root.Lstat(prefix)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot inspect restore path %q: %w", prefix, err)
		}
		if !info.IsDir() || prefix == name && !preserveDir {
			return fmt.Errorf("restore path is obstructed by %q", prefix)
		}
	}
	return nil
}

type safeRestorePlan struct {
	tracked, untracked string
	paths              map[string]bool
	preservedDirs      map[string]bool
	conflicted         bool
	hasStagedChanges   bool
}

// Determine which paths stash apply can write and the expected tracked result
// of its three-way merge. The destination can differ from the saved name (e.g.
// when upstream renamed a file). A conflict tree still describes the checkout
// writes, but never justifies retiring the backup after apply.
func (r *Repository) planStashRestore(ctx context.Context, current, id string, parents []string) (safeRestorePlan, error) {
	plan := safeRestorePlan{tracked: id, paths: make(map[string]bool)}
	base := parents[0]
	if err := r.addTreeChanges(ctx, plan.paths, base, id); err != nil {
		return plan, err
	}
	// The --index phase applies the saved index delta at its original paths,
	// independently of the worktree merge and its rename handling.
	writes := make(map[string]bool)
	if err := r.addTreeChanges(ctx, writes, base, parents[1]); err != nil {
		return plan, err
	}
	plan.hasStagedChanges = len(writes) != 0
	if (len(plan.paths) != 0 || plan.hasStagedChanges) && current != base {
		tree, conflicted, err := r.mergeStashTree(ctx, base, current, id)
		if err != nil {
			return plan, fmt.Errorf("Cannot verify the merged tracked tree: %w", err)
		}
		plan.tracked, plan.conflicted = tree, conflicted
		if err := r.addTreeChanges(ctx, writes, current, tree); err != nil {
			return plan, err
		}
		if !plan.conflicted {
			// A worktree-only saved name can be the source of an upstream
			// rename (e.g. guide -> guide/main). The merge's actual leaf
			// writes still need checking. Allow a real directory at the old
			// name only if both trees have a directory there and neither
			// the index patch nor the merge writes a leaf at that name.
			plan.preservedDirs, err = r.preservedRestoreDirs(ctx, tree, plan.paths, writes)
			if err != nil {
				return plan, err
			}
		}
	}
	for name := range writes {
		plan.paths[name] = true
	}
	if len(parents) == 3 {
		plan.untracked = parents[2]
	}
	return plan, nil
}

func (r *Repository) mergeStashTree(ctx context.Context, base, current, saved string) (tree string, conflicted bool, err error) {
	out, code, err := execute(ctx, r.root, nil, "", "--no-replace-objects",
		"merge-tree", "--write-tree", "--merge-base="+base, current, saved)
	if err != nil {
		return "", false, err
	}
	tree, _, _ = strings.Cut(out, "\n")
	if code != 0 && code != 1 || validateSHA(tree) != nil {
		return "", false, fmt.Errorf("Git merge-tree did not produce a valid tree (exit %d).", code)
	}
	return tree, code == 1, nil
}

// The staged side is independent of the stash's worktree side. When upstream
// changed, expect the saved index delta and upstream changes in the same tree.
func (r *Repository) expectedRestoredIndex(ctx context.Context, head string, parents []string) (string, error) {
	if head == parents[0] {
		return parents[1], nil
	}
	tree, conflicted, err := r.mergeStashTree(ctx, parents[0], head, parents[1])
	if conflicted {
		return "", errors.New("The predicted staged merge had conflicts.")
	}
	return tree, err
}

// Compare actual entries to the expected stage-zero records, not merely to
// status or worktree bytes. A full comparison also detects absent or extra
// paths, mode changes, and entries not representable in a tree.
func (r *Repository) verifyIndexTree(ctx context.Context, tree string) error {
	out, err := r.git(ctx, "", "--no-replace-objects", "ls-tree", "-r", "-z", tree)
	if err != nil {
		return err
	}
	want, err := fields(out)
	if err != nil {
		return err
	}
	out, err = r.git(ctx, "", "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	got, err := fields(out)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(got, want, func(indexEntry, treeEntry string) bool {
		header, name, ok := strings.Cut(treeEntry, "\t")
		parts := strings.Fields(header)
		return ok && len(parts) == 3 && parts[1] == "blob" && indexEntry == parts[0]+" "+parts[2]+" 0\t"+name
	}) {
		return errors.New("The actual index entries differ from the expected tree.")
	}
	return nil
}

func (r *Repository) preservedRestoreDirs(ctx context.Context, merged string, saved, writes map[string]bool) (map[string]bool, error) {
	candidates := maps.Clone(saved)
	for name := range writes {
		delete(candidates, name)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	out, err := r.git(ctx, "", "--no-replace-objects", "ls-tree", "-r", "-d", "--name-only", "-z", merged)
	if err != nil {
		return nil, err
	}
	names, err := fields(out)
	if err != nil {
		return nil, err
	}
	dirs := make(map[string]bool)
	for _, name := range names {
		if candidates[name] {
			dirs[name] = true
		}
	}
	return dirs, nil
}

func (r *Repository) verifyRestoreDestinations(ctx context.Context, current string, plan safeRestorePlan) error {
	paths, preservedDirs := plan.paths, plan.preservedDirs
	if plan.untracked != "" {
		// An untracked backup can now name a tracked destination after
		// integration. Keep these paths out of post-apply tracked verification.
		out, err := r.git(ctx, "", "--no-replace-objects", "ls-tree", "-r", "--name-only", "-z", plan.untracked)
		if err != nil {
			return err
		}
		names, err := fields(out)
		if err != nil {
			return err
		}
		paths = maps.Clone(paths)
		preservedDirs = maps.Clone(preservedDirs)
		for _, name := range names {
			paths[name] = true
			delete(preservedDirs, name) // Untracked checkout still writes here.
		}
	}
	return r.verifyWorktreePaths(ctx, current, paths, true, preservedDirs)
}

func (r *Repository) addTreeChanges(ctx context.Context, paths map[string]bool, base string, trees ...string) error {
	for _, tree := range trees {
		out, err := r.git(ctx, "", "--no-replace-objects", "diff-tree", "-r", "--no-renames", "--name-only", "-z", base, tree, "--")
		if err != nil {
			return err
		}
		names, err := fields(out)
		if err != nil {
			return err
		}
		for _, name := range names {
			paths[name] = true
		}
	}
	return nil
}

// Check ignored files before publishing a manual stash, not just during the
// guarded switch: a staged deletion may hide an ignored replacement directory.
func (r *Repository) checkStashIgnoredObstructions(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	out, err := r.git(ctx, "", "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	ignored, err := fields(out)
	if err != nil {
		return err
	}
	changed, parents := make(map[string]bool, len(paths)), make(map[string]bool)
	for _, name := range paths {
		changed[name] = true
		for dir := path.Dir(name); dir != "." && !parents[dir]; dir = path.Dir(dir) {
			parents[dir] = true
		}
	}
	for _, name := range ignored {
		if parents[name] {
			return fmt.Errorf("Stash all would replace an ignored path: %q. Move it aside before retrying.", name)
		}
		for candidate := name; candidate != "."; candidate = path.Dir(candidate) {
			if changed[candidate] {
				return fmt.Errorf("Stash all would replace an ignored path: %q. Move it aside before retrying.", name)
			}
		}
	}
	return nil
}

func matchesSafeBlob(root *os.Root, path, mode, oid string) bool {
	var h hash.Hash = sha1.New() // Git blob identity, not a security credential.
	if len(oid) == 64 {
		h = sha256.New()
	}
	info, err := root.Lstat(path)
	if err != nil {
		return false
	}
	if mode == "120000" {
		target, err := root.Readlink(path)
		if err != nil {
			return false
		}
		fmt.Fprintf(h, "blob %d\x00%s", len(target), target)
	} else {
		if !info.Mode().IsRegular() || (info.Mode().Perm()&0100 != 0) != (mode == "100755") {
			return false
		}
		file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return false
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(info, actual) || info.Size() != actual.Size() {
			return false
		}
		fmt.Fprintf(h, "blob %d\x00", info.Size())
		if n, err := io.Copy(h, file); err != nil || n != info.Size() {
			return false
		}
	}
	return hex.EncodeToString(h.Sum(nil)) == oid
}
