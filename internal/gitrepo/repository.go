// Package gitrepo owns Git subprocesses and repository safety policy. Callers must
// serialize mutations; Git owns locking and external clients may still race us.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
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
}

// Snapshot freezes commit roots and annotations for every page in a generation.
// Callers must not modify its roots or labels while reading history.
type Snapshot struct {
	Roots  []string
	Labels map[string][]string
}

type Commit struct {
	SHA, Subject, Author, AuthoredAt string
	Labels                           []string
}

type ChangedFile struct{ Status, Path, OldPath string }

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
	Status(context.Context) (Status, error)
	Snapshot(context.Context, Scope) (Snapshot, error)
	History(context.Context, Snapshot, int, int) ([]Commit, error)
	Details(context.Context, string) (Details, error)
	Fetch(context.Context) error
	Pull(context.Context, PullMode) error
}

type Repository struct{ root string }

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
	return r, nil
}

func (r *Repository) Root() string { return r.root }

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
			if strings.HasPrefix(record, "2 ") {
				i++ // Porcelain v2 renames include a separate raw old pathname.
				if i == len(records) {
					return s, errors.New("Git returned an incomplete status rename record.")
				}
			}
		}
	}
	return s, nil
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
	out, err := r.git(ctx, "", "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	s, err := parseStatus(out)
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
