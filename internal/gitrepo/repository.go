// Package gitrepo owns Git subprocesses and repository safety policy. Callers must
// serialize mutations; Git owns locking and external clients may still race us.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Scope string

const (
	All     Scope = "all"
	Current Scope = "current"
)

type PullMode string

const (
	FastForward PullMode = "ff-only"
	Merge       PullMode = "merge"
	Rebase      PullMode = "rebase"
)

type Status struct {
	Branch, Head, Upstream, Operation string
	Dirty                             bool
	Remotes, FetchWarnings            []string
	// Entries coalesces by current raw path. Dirty remains conservative even
	// for unknown porcelain records that cannot produce a selectable entry.
	Entries []WorkingCopyEntry
	// StatsWarning retains a usable listing when ancillary batched counts fail.
	StatsWarning string
}

// CommitRelation compares ancestry only with the attached branch's upstream.
// Shared commits and commits on unrelated refs are Unmarked.
type CommitRelation uint8

const (
	Unmarked CommitRelation = iota
	Incoming
	LocalOnly
)

// UpstreamComparison counts unique commits in the frozen HEAD/upstream pair,
// independently of the number of history rows loaded by the UI.
type UpstreamComparison struct {
	Ahead, Behind int
}

// Snapshot freezes commit roots, annotations and upstream membership for every
// page in a generation. Callers must not modify it or its referenced data while
// reading history.
type Snapshot struct {
	// Branch and Head are the identity captured with this history, not an earlier
	// independent Status read. Branch is empty when detached; Head when unborn.
	Branch, Head string
	Roots        []string
	Labels       map[string][]string
	// Relations contains only differing commits; omitted SHAs are Unmarked.
	Relations map[string]CommitRelation
	// Diverged reports unique commits on both sides, independent of page limits.
	Diverged bool
	// Comparison is nil without an available upstream; zero counts mean in sync.
	Comparison *UpstreamComparison
	// AncestryWarning explains why merge indentation is unavailable. History
	// remains usable, but never guesses membership from an incomplete walk.
	AncestryWarning string
	mergeDepths     map[string]int
}

type Commit struct {
	SHA, Subject, Author, AuthoredAt string
	Labels                           []string
	Relation                         CommitRelation
	// MergeDepth counts enclosing non-first-parent histories, independently of
	// upstream relation styling and the loaded page size.
	MergeDepth int
}

type ChangedFile struct {
	Status, Path, OldPath string
	Added, Deleted        int
	Binary                bool // Git reports "-" counts, not zero lines.
}

type Details struct {
	SHA, Message, Author, AuthorEmail, AuthoredAt string
	Committer, CommitterEmail, CommittedAt        string
	Parents                                       []string
	Files                                         []ChangedFile
	Comparison                                    string
}

// Repo is the boundary consumed by the application; absent status values are
// empty strings. Returned repository text is raw: use Safe only for presentation.
type Repo interface {
	Root() string
	DisplayName() string
	Status(context.Context) (Status, error)
	Snapshot(context.Context, Scope) (Snapshot, error)
	History(context.Context, Snapshot, int, int) ([]Commit, error)
	Details(context.Context, string) (Details, error)
	WorkingCopyDetails(context.Context, string) (WorkingCopyDetails, error)
	Fetch(context.Context) error
	Pull(context.Context, PullMode) error
	SafePull(context.Context, bool) (backupID string, err error)
	StashAll(context.Context, string) error
	Stashes(context.Context) ([]Stash, error)
	StashPreview(context.Context, string) (StashPreview, error)
	ApplyStash(context.Context, Stash) error
	DeleteStash(context.Context, Stash) error
}

type Repository struct{ root, displayName string }

var _ Repo = (*Repository)(nil)

// Open discovers a working tree, including unborn and linked worktrees.
func Open(ctx context.Context, path string) (*Repository, error) {
	bare, code, err := execute(ctx, path, nil, "", "rev-parse", "--is-bare-repository")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, errors.New("Not a Git repository. Open a directory inside a Git working tree.")
	}
	if strings.TrimSpace(bare) == "true" {
		return nil, errors.New("Bare repositories have no working tree. Open a non-bare checkout instead.")
	}
	r := &Repository{root: path}
	inside, err := r.git(ctx, "", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(inside) != "true" {
		return nil, errors.New("Open a directory inside the Git working tree, not its metadata directory.")
	}
	root, err := r.git(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r.root = strings.TrimSuffix(root, "\n") // A pathname itself may end in LF.
	identity, err := r.git(ctx, "", "worktree", "list", "--porcelain", "-z")
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.displayName = repositoryDisplayName(identity, r.root)
	return r, nil
}

func (r *Repository) Root() string { return r.root }

// DisplayName identifies the repository's main worktree, not this checkout.
// Git lists the main worktree first, including a bare main repository. No remote
// URLs or network access are used; Root remains the current working tree.
func (r *Repository) DisplayName() string { return r.displayName }

func repositoryDisplayName(porcelain, fallback string) string {
	path := fallback
	main, _, _ := strings.Cut(porcelain, "\x00\x00")
	record, _, complete := strings.Cut(porcelain, "\x00")
	if complete && strings.HasPrefix(record, "worktree ") && len(record) > len("worktree ") {
		path = strings.TrimPrefix(record, "worktree ")
	}
	name := filepath.Base(filepath.Clean(path))
	if strings.Contains(main, "\x00bare\x00") || strings.HasSuffix(main, "\x00bare") {
		name = strings.TrimSuffix(name, ".git")
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "repository"
	}
	return name
}

func (r *Repository) git(ctx context.Context, input string, args ...string) (string, error) {
	out, code, err := execute(ctx, r.root, nil, input, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("Git %s failed (exit %d). Inspect the repository with Git for diagnostics.", args[0], code)
	}
	return out, nil
}

func fields(out string) ([]string, error) {
	if out == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(out, "\x00") {
		return nil, errors.New("Git returned an incomplete NUL-delimited record.")
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

func parseStatus(out string) (Status, error) {
	s := Status{Remotes: []string{}, FetchWarnings: []string{}}
	records, err := fields(out)
	if err != nil {
		return s, err
	}
	byPath := make(map[string]int)
	for i := 0; i < len(records); i++ {
		record := records[i]
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			s.Branch = strings.TrimPrefix(record, "# branch.head ")
			if s.Branch == "(detached)" {
				s.Branch = ""
			}
		case strings.HasPrefix(record, "# branch.oid "):
			s.Head = strings.TrimPrefix(record, "# branch.oid ")
			if s.Head == "(initial)" {
				s.Head = ""
			}
		case strings.HasPrefix(record, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(record, "# branch.upstream ")
		case !strings.HasPrefix(record, "# "):
			s.Dirty = true
			oldPath := ""
			if strings.HasPrefix(record, "2 ") {
				i++ // Porcelain v2 renames include a separate raw old pathname.
				if i == len(records) {
					return s, errors.New("Git returned an incomplete status rename record.")
				}
				oldPath = records[i]
			}
			entry, known, err := parseWorkingCopyEntry(record, oldPath)
			if err != nil {
				return s, err
			}
			if !known {
				continue // Unknown records must still block pull.
			}
			if at, exists := byPath[entry.Path]; exists {
				// Git can emit a staged deletion and a recreated untracked
				// file at the same path. Keep both on one selectable row.
				if entry.Untracked {
					s.Entries[at].Untracked = true
				} else {
					entry.Untracked = entry.Untracked || s.Entries[at].Untracked
					s.Entries[at] = entry
				}
			} else {
				byPath[entry.Path] = len(s.Entries)
				s.Entries = append(s.Entries, entry)
			}
		}
	}
	return s, nil
}

func (r *Repository) workingCopyStatus(ctx context.Context) (Status, error) {
	out, err := r.git(ctx, "", "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out)
}

func (r *Repository) operation(ctx context.Context) (string, error) {
	for _, marker := range [][2]string{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
		{"sequencer", "sequencer"}, {"BISECT_START", "bisect"}, {"BISECT_LOG", "bisect"},
	} {
		path, err := r.git(ctx, "", "rev-parse", "--path-format=absolute", "--git-path", marker[0])
		if err != nil {
			return "", err
		}
		_, err = os.Stat(strings.TrimSuffix(path, "\n"))
		if err == nil {
			return marker[1], nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("Cannot inspect Git operation state. Check repository permissions with Git.")
		}
	}
	return "", nil
}

func (r *Repository) Status(ctx context.Context) (Status, error) {
	s, err := r.workingCopyStatus(ctx)
	if err != nil {
		return s, err
	}
	s.Operation, err = r.operation(ctx)
	if err != nil {
		return s, err
	}
	remotes, err := r.remotes(ctx)
	if err != nil {
		return s, err
	}
	for _, remote := range remotes {
		s.Remotes = append(s.Remotes, remote.name)
		if remote.skip {
			s.FetchWarnings = append(s.FetchWarnings, remote.name+": excluded from Fetch all by skipFetchAll.")
			continue
		}
		standard, negative := false, false
		for _, mapping := range remote.mappings {
			standard = standard || strings.TrimPrefix(mapping, "+") == "refs/heads/*:refs/remotes/"+remote.name+"/*"
			negative = negative || strings.HasPrefix(mapping, "^")
		}
		if !standard || negative {
			s.FetchWarnings = append(s.FetchWarnings, remote.name+": narrow or custom fetch mapping; Fetch all uses existing refspecs, not necessarily every remote branch.")
		}
	}
	if err := r.workingCopyStats(ctx, &s); err != nil {
		return s, err
	}
	return s, nil
}

var shaPattern = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

func validateSHA(sha string) error {
	if !shaPattern.MatchString(sha) {
		return errors.New("Expected a full hexadecimal commit SHA.")
	}
	return nil
}

// Safe escapes terminal controls and bidi formatting, retaining LF. Never use
// this presentation-only transformation on filenames passed to Git.
func Safe(text string) string {
	var b strings.Builder
	for _, c := range text {
		switch {
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c <= 9 || c >= 11 && c <= 31 || c >= 127 && c <= 159 || c == 0x061c || c >= 0x200b && c <= 0x200f || c >= 0x2028 && c <= 0x202e || c >= 0x2060 && c <= 0x206f || c == 0xfeff:
			if c <= 255 {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				fmt.Fprintf(&b, `\u%04x`, c)
			}
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
