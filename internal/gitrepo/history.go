package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Snapshot includes HEAD and its available configured upstream in both scopes;
// All also retains branch, remote-tracking and commit-tag roots. Relations compare
// only the attached branch and its upstream, never labels or other remotes.
func (r *Repository) Snapshot(ctx context.Context, scope Scope) (Snapshot, error) {
	snapshot := Snapshot{Roots: []string{}, Labels: map[string][]string{}, Relations: map[string]CommitRelation{}}
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
	snapshot.Branch, snapshot.Head = status.Branch, status.Head
	if status.Head != "" {
		if err := validateSHA(status.Head); err != nil {
			return snapshot, err
		}
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
	// Capture full upstream names with the same ref enumeration as their OIDs.
	// Other namespaces contribute only the configured upstream, not arbitrary
	// custom refs, alongside All's standard branch/remote/tag roots.
	out, err = r.git(ctx, "", "for-each-ref", "--format=%(objectname)%00%(refname)%00%(upstream)%00")
	if err != nil {
		return snapshot, err
	}
	upstreamRef, branchHead := "", ""
	commitsByRef := map[string]string{}
	var refs [][]string
	if out != "" {
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			ref, err := fields(line)
			if err != nil || len(ref) != 3 || validateSHA(ref[0]) != nil {
				return snapshot, errors.New("Git returned an invalid reference snapshot.")
			}
			if status.Branch != "" && ref[1] == "refs/heads/"+status.Branch {
				branchHead, upstreamRef = ref[0], ref[2]
			}
			refs = append(refs, ref)
		}
	}
	if status.Branch != "" && branchHead != status.Head {
		return snapshot, errors.New("HEAD changed while capturing history. Refresh to read a consistent branch snapshot.")
	}
	var input strings.Builder
	var commitRefs [][]string
	for _, ref := range refs {
		if historyRef(ref[1]) || ref[1] == upstreamRef {
			commitRefs = append(commitRefs, ref)
			fmt.Fprintf(&input, "%s^{}\n", ref[0])
		}
	}
	if len(commitRefs) != 0 {
		peeled, err := r.git(ctx, input.String(), "cat-file", "--batch-check=%(objectname) %(objecttype)")
		if err != nil {
			return snapshot, err
		}
		lines := strings.Split(strings.TrimSuffix(peeled, "\n"), "\n")
		if len(lines) != len(commitRefs) {
			return snapshot, errors.New("Git returned an incomplete reference snapshot.")
		}
		for i, ref := range commitRefs {
			sha, kind, _ := strings.Cut(lines[i], " ")
			isTag := strings.HasPrefix(ref[1], "refs/tags/")
			if (isTag || !historyRef(ref[1])) && (kind == "tree" || kind == "blob") {
				continue
			}
			if kind != "commit" {
				return snapshot, errors.New("Git returned an invalid commit reference.")
			}
			if err := validateSHA(sha); err != nil {
				return snapshot, err
			}
			commitsByRef[ref[1]] = sha
			if !historyRef(ref[1]) {
				continue
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
	if status.Branch != "" && status.Head != "" {
		if upstream := commitsByRef[upstreamRef]; upstream != "" {
			if !roots[upstream] {
				snapshot.Roots = append(snapshot.Roots, upstream)
				roots[upstream] = true
			}
			snapshot.Relations, snapshot.Diverged, err = r.commitRelations(ctx, status.Head, upstream)
			if err != nil {
				return snapshot, err
			}
			snapshot.Comparison = &UpstreamComparison{}
			for _, relation := range snapshot.Relations {
				switch relation {
				case LocalOnly:
					snapshot.Comparison.Ahead++
				case Incoming:
					snapshot.Comparison.Behind++
				}
			}
		}
	}
	return snapshot, nil
}

func historyRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/remotes/") || strings.HasPrefix(ref, "refs/tags/")
}

// Bound metadata independently of the UI's loaded history cap. One extra record
// detects overflow: never return a silently partial membership classification.
const maxRelationCommits = 100000

func (r *Repository) commitRelations(ctx context.Context, head, upstream string) (map[string]CommitRelation, bool, error) {
	out, err := r.git(ctx, "", "rev-list", "--left-right", fmt.Sprintf("--max-count=%d", maxRelationCommits+1), head+"..."+upstream, "--")
	if err != nil {
		return nil, false, err
	}
	return parseCommitRelations(out)
}

func parseCommitRelations(out string) (map[string]CommitRelation, bool, error) {
	relations := map[string]CommitRelation{}
	if out == "" {
		return relations, false, nil
	}
	if !strings.HasSuffix(out, "\n") {
		return nil, false, errors.New("Git returned an incomplete upstream comparison.")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > maxRelationCommits {
		return nil, false, fmt.Errorf("History comparison exceeds %d unique commits. Inspect this unusually large HEAD/upstream difference with Git.", maxRelationCommits)
	}
	incoming, local := false, false
	for _, line := range lines {
		if len(line) < 2 || validateSHA(line[1:]) != nil {
			return nil, false, errors.New("Git returned an invalid upstream comparison.")
		}
		switch line[0] {
		case '<':
			relations[line[1:]] = LocalOnly
			local = true
		case '>':
			relations[line[1:]] = Incoming
			incoming = true
		default:
			return nil, false, errors.New("Git returned an invalid upstream comparison side.")
		}
	}
	return relations, incoming && local, nil
}

// History uses only the supplied frozen SHA roots, labels and relations, never
// moving ref names or live upstream membership.
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
		commits = append(commits, Commit{
			SHA: records[i], Subject: records[i+1], Author: records[i+2], AuthoredAt: records[i+3],
			Labels: slices.Clone(snapshot.Labels[records[i]]), Relation: snapshot.Relations[records[i]],
		})
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
