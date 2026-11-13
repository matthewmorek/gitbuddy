package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var testContext = context.Background()

// Every Git write, including local bare remotes, is owned by this fixture. No
// ambient repository, credential helper, system/global config or network is used.
type fixture struct {
	t    *testing.T
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_COUNT": "0", "GIT_CONFIG_PARAMETERS": "", "GIT_ALLOW_PROTOCOL": "file",
		"GIT_AUTHOR_NAME": "Fixture Author", "GIT_AUTHOR_EMAIL": "author@example.invalid",
		"GIT_COMMITTER_NAME": "Fixture Committer", "GIT_COMMITTER_EMAIL": "committer@example.invalid",
		"GIT_SSH_COMMAND": "", "GIT_SSH": "", "GIT_ASKPASS": "false",
	} {
		t.Setenv(key, value)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t, root}
}

func (f *fixture) git(path string, args ...string) string {
	f.t.Helper()
	rel, err := filepath.Rel(f.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		f.t.Fatal("fixture escaped its owner")
	}
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	cmd.Dir, cmd.Env = path, environment()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("fixture git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSuffix(string(out), "\n")
}

func (f *fixture) repo(name string, bare bool) string {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(path, 0700); err != nil {
		f.t.Fatal(err)
	}
	args := []string{"init", "--initial-branch=main"}
	if bare {
		args = append(args, "--bare")
	}
	f.git(path, args...)
	f.git(path, "config", "core.hooksPath", os.DevNull)
	return path
}

func (f *fixture) write(path, name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(path, name, content, message string) string {
	f.t.Helper()
	f.write(path, name, content)
	f.git(path, "add", "--", name)
	f.git(path, "commit", "-m", message)
	return f.git(path, "rev-parse", "HEAD")
}

func (f *fixture) paired() (local, writer, remote string) {
	f.t.Helper()
	remote = f.repo("remote.git", true)
	writer = f.repo("writer", false)
	f.commit(writer, "base", "base", "base")
	f.git(writer, "remote", "add", "origin", remote)
	f.git(writer, "push", "--set-upstream", "origin", "main")
	local = filepath.Join(f.root, "local")
	f.git(f.root, "clone", remote, local)
	f.git(local, "config", "core.hooksPath", os.DevNull)
	return
}

func openRepo(t *testing.T, path string) *Repository {
	t.Helper()
	r, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func statusOf(t *testing.T, r *Repository) Status {
	t.Helper()
	s, err := r.Status(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func snapshotOf(t *testing.T, r *Repository, scope Scope) Snapshot {
	t.Helper()
	s, err := r.Snapshot(testContext, scope)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func detailsOf(t *testing.T, r *Repository, sha string) Details {
	t.Helper()
	d, err := r.Details(testContext, sha)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantError(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substring) {
		t.Fatalf("want error containing %q, got %v", substring, err)
	}
}

func equal[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDiscoveryAndAmbientRepositoryIsolation(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo with spaces\n", false)
	other := f.repo("other", false)
	f.commit(other, "other-file", "other", "other")
	sub := filepath.Join(path, "sub dir")
	mustOK(t, os.Mkdir(sub, 0700))
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, "missing-index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(other, ".git", "objects"))
	r := openRepo(t, sub)
	equal(t, r.Root(), path)
	equal(t, statusOf(t, r), Status{Branch: "main", Remotes: []string{}, FetchWarnings: []string{}})
	history, err := r.History(testContext, snapshotOf(t, r, All), 0, 200)
	mustOK(t, err)
	equal(t, len(history), 0)
	wantError(t, r.Fetch(testContext), "No eligible remotes")
	wantError(t, r.Pull(testContext, FastForward), "attached, committed")
	_, err = Open(testContext, f.root)
	wantError(t, err, "Not a Git repository")
	_, err = Open(testContext, f.repo("bare.git", true))
	wantError(t, err, "Bare repositories")
	_, err = Open(testContext, filepath.Join(path, ".git"))
	wantError(t, err, "metadata directory")
}

func TestFrozenHistoryAndTagPeeling(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	first := f.commit(path, "first", "first", "first")
	f.git(path, "tag", "light")
	f.git(path, "tag", "-a", "annotated", "-m", "tag")
	f.git(path, "tag", "-a", "nested", "annotated", "-m", "nested")
	f.git(path, "tag", "blob-tag", f.git(path, "rev-parse", "HEAD:first"))
	f.git(path, "tag", "tree-tag", f.git(path, "rev-parse", "HEAD^{tree}"))
	second := f.commit(path, "second", "second", "second")
	f.git(path, "branch", "side", first)
	f.git(path, "update-ref", "refs/remotes/origin/main", second)
	f.write(path, "stash-only", "stash")
	f.git(path, "stash", "push", "--include-untracked", "-m", "not history")
	stash := f.git(path, "rev-parse", "refs/stash")
	r := openRepo(t, path)
	snapshot := snapshotOf(t, r, All)
	equal(t, snapshot.Labels[first], []string{"side", "tag: annotated", "tag: light", "tag: nested"})
	equal(t, snapshot.Labels[second], []string{"main", "origin/main", "HEAD"})
	if slices.Contains(snapshot.Roots, stash) {
		t.Fatal("snapshot includes stash")
	}
	third := f.commit(path, "third", "third", "third")
	page1, err := r.History(testContext, snapshot, 0, 1)
	mustOK(t, err)
	page2, err := r.History(testContext, snapshot, 1, 1)
	mustOK(t, err)
	equal(t, []string{page1[0].SHA, page2[0].SHA}, []string{second, first})
	equal(t, page1[0].Labels, []string{"main", "origin/main", "HEAD"})
	page1[0].Labels[0] = "caller mutation"
	equal(t, snapshot.Labels[second], []string{"main", "origin/main", "HEAD"})
	end, err := r.History(testContext, snapshot, 2, 200)
	mustOK(t, err)
	equal(t, len(end), 0)
	current := snapshotOf(t, r, Current)
	equal(t, current.Roots, []string{third})
	equal(t, current.Labels[first], snapshot.Labels[first])
	equal(t, current.Labels[third], []string{"main", "HEAD"})
	f.git(path, "switch", "--detach", first)
	detached := f.commit(path, "detached", "detached", "detached")
	detachedSnapshot := snapshotOf(t, r, All)
	count := 0
	for _, root := range detachedSnapshot.Roots {
		if root == detached {
			count++
		}
	}
	equal(t, count, 1)
	equal(t, detachedSnapshot.Labels[detached], []string{"HEAD"})
	equal(t, snapshotOf(t, r, Current).Roots, []string{detached})
	f.git(path, "switch", "main")
	// The detached commit now exists only in reflogs and must not become a root.
	if slices.Contains(snapshotOf(t, r, All).Roots, detached) {
		t.Fatal("snapshot includes reflog-only tip")
	}
	_, err = r.Details(testContext, "--all")
	wantError(t, err, "SHA")
	_, err = r.History(testContext, Snapshot{Roots: []string{"HEAD"}}, 0, 200)
	wantError(t, err, "SHA")
	_, err = r.History(testContext, snapshot, -1, 200)
	wantError(t, err, "offset")
}

func TestDetailsUnusualNamesMetadataAndFirstParent(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	original, renamed := "# branch.head fake\n\t\x1b[31m.txt", "renamed\nfile.txt"
	root := f.commit(path, original, "identical content\n", "subject\n\nfull body\nlast line")
	r := openRepo(t, path)
	d := detailsOf(t, r, root)
	equal(t, d.Message, "subject\n\nfull body\nlast line\n")
	equal(t, d.Author, "Fixture Author")
	equal(t, d.AuthorEmail, "author@example.invalid")
	equal(t, d.Committer, "Fixture Committer")
	equal(t, d.CommitterEmail, "committer@example.invalid")
	if d.AuthoredAt == "" || d.CommittedAt == "" {
		t.Fatal("missing timestamps")
	}
	equal(t, d.Parents, []string{})
	equal(t, d.Files, []ChangedFile{{Status: "A", Path: original}})
	equal(t, d.Comparison, "Root commit: all files added")
	mustOK(t, os.Rename(filepath.Join(path, original), filepath.Join(path, renamed)))
	f.git(path, "add", "--all")
	// Porcelain renames carry a separate raw pathname record.
	equal(t, statusOf(t, r).Dirty, true)
	equal(t, statusOf(t, r).Branch, "main")
	f.git(path, "commit", "-m", "rename")
	renameSHA := f.git(path, "rev-parse", "HEAD")
	equal(t, detailsOf(t, r, renameSHA).Files, []ChangedFile{{Status: "R100", OldPath: original, Path: renamed}})
	f.git(path, "switch", "-c", "side", root)
	side := f.commit(path, "side-file", "side", "side")
	f.git(path, "switch", "main")
	f.git(path, "merge", "--no-ff", "side", "-m", "merge")
	merge := detailsOf(t, r, f.git(path, "rev-parse", "HEAD"))
	equal(t, merge.Parents, []string{renameSHA, side})
	equal(t, merge.Files, []ChangedFile{{Status: "A", Path: "side-file"}})
	equal(t, merge.Comparison, "Compared with first parent "+renameSHA)
	commits, err := r.History(testContext, snapshotOf(t, r, All), 0, 200)
	mustOK(t, err)
	indexes := map[string]int{}
	for i, c := range commits {
		indexes[c.SHA] = i
	}
	for _, c := range commits {
		for _, parent := range detailsOf(t, r, c.SHA).Parents {
			if indexes[c.SHA] >= indexes[parent] {
				t.Fatalf("history is not topological: %s before %s", c.SHA, parent)
			}
		}
	}
	// Even configured text converters and external diff commands must not run.
	f.git(path, "config", "diff.external", "/does/not/exist")
	f.git(path, "config", "diff.hostile.textconv", "/does/not/exist")
	f.write(path, ".gitattributes", "* diff=hostile\n")
	equal(t, detailsOf(t, r, renameSHA).Files, []ChangedFile{{Status: "R100", OldPath: original, Path: renamed}})
}

func TestSafe(t *testing.T) {
	equal(t, Safe("normal\n\t\r\x1b[31m\x00\x7f\u0085\u202eabc\u2066"), "normal\n\\t\\r\\x1b[31m\\x00\\x7f\\x85\\u202eabc\\u2066")
}
