package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

func (r *Repository) Snapshot(ctx context.Context, scope Scope) (Snapshot, error) {
	snapshot := Snapshot{Roots: []string{}, Labels: map[string][]string{}}
	roots := map[string]bool{}
	if scope != All && scope != Current {
		return snapshot, errors.New("Unknown history scope.")
	}
	out, err := r.git(ctx, "", "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=no")
	if err != nil {
		return snapshot, err
	}
	status, err := parseStatus(out)
	if err != nil {
		return snapshot, err
	}
	add := func(sha, label string) error {
		if err := validateSHA(sha); err != nil {
			return err
		}
		if scope == All && !roots[sha] {
			snapshot.Roots = append(snapshot.Roots, sha)
			roots[sha] = true
		}
		if !slices.Contains(snapshot.Labels[sha], label) {
			snapshot.Labels[sha] = append(snapshot.Labels[sha], label)
		}
		return nil
	}
	out, err = r.git(ctx, "", "for-each-ref", "--format=%(objectname)%00%(refname)%00", "refs/heads/", "refs/remotes/", "refs/tags/")
	if err != nil {
		return snapshot, err
	}
	if out != "" {
		var refs [][]string
		var input strings.Builder
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			ref, err := fields(line)
			if err != nil || len(ref) != 2 || validateSHA(ref[0]) != nil {
				return snapshot, errors.New("Git returned an invalid reference snapshot.")
			}
			refs = append(refs, ref)
			fmt.Fprintf(&input, "%s^{}\n", ref[0])
		}
		peeled, err := r.git(ctx, input.String(), "cat-file", "--batch-check=%(objectname) %(objecttype)")
		if err != nil {
			return snapshot, err
		}
		lines := strings.Split(strings.TrimSuffix(peeled, "\n"), "\n")
		if len(lines) != len(refs) {
			return snapshot, errors.New("Git returned an incomplete reference snapshot.")
		}
		for i, ref := range refs {
			sha, kind, _ := strings.Cut(lines[i], " ")
			isTag := strings.HasPrefix(ref[1], "refs/tags/")
			if isTag && (kind == "tree" || kind == "blob") {
				continue
			}
			if kind != "commit" {
				return snapshot, errors.New("Git returned an invalid commit reference.")
			}
			label := strings.TrimPrefix(strings.TrimPrefix(ref[1], "refs/heads/"), "refs/remotes/")
			if isTag {
				label = "tag: " + strings.TrimPrefix(ref[1], "refs/tags/")
			}
			if err := add(sha, label); err != nil {
				return snapshot, err
			}
		}
	}
	if status.Head != "" {
		if !roots[status.Head] {
			snapshot.Roots = append(snapshot.Roots, status.Head)
			roots[status.Head] = true
		}
		if err := add(status.Head, "HEAD"); err != nil {
			return snapshot, err
		}
	}
	return snapshot, nil
}

// History uses only the supplied immutable SHA roots, never moving ref names.
func (r *Repository) History(ctx context.Context, snapshot Snapshot, offset, limit int) ([]Commit, error) {
	if offset < 0 || limit < 1 {
		return nil, errors.New("History offset must be nonnegative and page size must be positive integers.")
	}
	for _, sha := range snapshot.Roots {
		if err := validateSHA(sha); err != nil {
			return nil, err
		}
	}
	commits := []Commit{}
	if len(snapshot.Roots) == 0 {
		return commits, nil
	}
	out, err := r.git(ctx, strings.Join(snapshot.Roots, "\n")+"\n", "log", "--topo-order", "--no-notes", "--no-show-signature", "-z",
		"--format=%H%x00%s%x00%an%x00%aI", fmt.Sprintf("--skip=%d", offset), fmt.Sprintf("--max-count=%d", limit), "--stdin", "--")
	if err != nil {
		return nil, err
	}
	records, err := fields(out)
	if err != nil {
		return nil, err
	}
	if len(records)%4 != 0 {
		return nil, errors.New("Git returned malformed history records.")
	}
	for i := 0; i < len(records); i += 4 {
		if err := validateSHA(records[i]); err != nil {
			return nil, err
		}
		commits = append(commits, Commit{records[i], records[i+1], records[i+2], records[i+3], slices.Clone(snapshot.Labels[records[i]])})
	}
	return commits, nil
}

var fileStatusPattern = regexp.MustCompile(`^[ACDMRTUXB][0-9]*$`)

// Details compares merges to their first parent and never invokes diff helpers.
func (r *Repository) Details(ctx context.Context, sha string) (Details, error) {
	d := Details{Parents: []string{}, Files: []ChangedFile{}}
	if err := validateSHA(sha); err != nil {
		return d, err
	}
	out, err := r.git(ctx, "", "log", "-1", "-z", "--no-notes", "--no-show-signature",
		"--format=%H%x00%B%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%P", sha, "--")
	if err != nil {
		return d, err
	}
	metadata, err := fields(out)
	if err != nil || len(metadata) != 9 {
		return d, errors.New("Git returned malformed commit details.")
	}
	d.SHA, d.Message, d.Author, d.AuthorEmail, d.AuthoredAt = metadata[0], metadata[1], metadata[2], metadata[3], metadata[4]
	d.Committer, d.CommitterEmail, d.CommittedAt = metadata[5], metadata[6], metadata[7]
	if metadata[8] != "" {
		d.Parents = strings.Split(metadata[8], " ")
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "-M"}
	if len(d.Parents) == 0 {
		args = append([]string{"diff-tree", "--root", "--no-commit-id", "-r"}, args[1:]...)
		d.Comparison = "Root commit: all files added"
	} else {
		args = append(args, d.Parents[0])
		d.Comparison = "Compared with parent " + d.Parents[0]
		if len(d.Parents) > 1 {
			d.Comparison = "Compared with first parent " + d.Parents[0]
		}
	}
	out, err = r.git(ctx, "", append(args, sha, "--")...)
	if err != nil {
		return d, err
	}
	names, err := fields(out)
	if err != nil {
		return d, err
	}
	for i := 0; i < len(names); {
		status := names[i]
		if !fileStatusPattern.MatchString(status) || i+1 >= len(names) {
			return d, errors.New("Git returned an invalid changed-file record.")
		}
		file := ChangedFile{Status: status, Path: names[i+1]}
		i += 2
		if status[0] == 'R' || status[0] == 'C' {
			if i >= len(names) {
				return d, errors.New("Git returned an incomplete rename record.")
			}
			file.OldPath, file.Path = file.Path, names[i]
			i++
		}
		d.Files = append(d.Files, file)
	}
	return d, nil
}
