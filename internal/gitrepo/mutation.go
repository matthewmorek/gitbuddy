package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type remote struct {
	name     string
	skip     bool
	mappings []string
}

func (r *Repository) config(ctx context.Context, key string) ([]string, error) {
	out, code, err := execute(ctx, r.root, nil, "", "config", "--null", "--get-all", key)
	if err != nil {
		return nil, err
	}
	if code == 1 {
		return nil, nil // Git documents missing keys as exit 1.
	}
	if code != 0 {
		return nil, fmt.Errorf("Git config failed (exit %d). Inspect the repository with Git for diagnostics.", code)
	}
	return fields(out)
}

func (r *Repository) remotes(ctx context.Context) ([]remote, error) {
	out, err := r.git(ctx, "", "remote")
	if err != nil {
		return nil, err
	}
	remotes := []remote{}
	for _, name := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if name == "" {
			continue
		}
		mappings, err := r.config(ctx, "remote."+name+".fetch")
		if err != nil {
			return nil, err
		}
		skip, code, err := execute(ctx, r.root, nil, "", "config", "--bool", "--get", "remote."+name+".skipFetchAll")
		if err != nil {
			return nil, err
		}
		if code != 0 && code != 1 {
			return nil, errors.New("Git remote configuration failed. Inspect the repository with Git.")
		}
		remotes = append(remotes, remote{name, code == 0 && strings.TrimSpace(skip) == "true", mappings})
	}
	return remotes, nil
}

func validateFetchMappings(remotes []remote) error {
	for _, remote := range remotes {
		for _, mapping := range remote.mappings {
			_, destination, found := strings.Cut(mapping, ":")
			if !found || destination == "" {
				continue
			}
			unsafe := !strings.HasPrefix(destination, "refs/")
			prefix, _, wildcard := strings.Cut(destination, "*")
			if wildcard {
				// The wildcard may consume any remaining heads namespace or path.
				unsafe = unsafe || strings.Count(destination, "*") > 1 || strings.HasPrefix("refs/heads/", prefix) || strings.HasPrefix(prefix, "refs/heads/")
			} else {
				unsafe = unsafe || destination == "refs/heads" || strings.HasPrefix(destination, "refs/heads/")
			}
			if unsafe {
				return fmt.Errorf("Refusing fetch: %s has a fetch mapping targeting local branches. Review its refspecs outside Gitbuddy.", Safe(remote.name))
			}
		}
	}
	return nil
}

func noPruneArgs(remotes []remote) []string {
	args := []string{"-c", "fetch.all=false", "-c", "fetch.prune=false", "-c", "fetch.pruneTags=false"}
	for _, remote := range remotes {
		args = append(args, "-c", "remote."+remote.name+".prune=false", "-c", "remote."+remote.name+".pruneTags=false")
	}
	return args
}

func (r *Repository) mutationEnvironment(ctx context.Context) ([]string, error) {
	configured, err := r.config(ctx, "core.sshCommand")
	if err != nil {
		return nil, err
	}
	ssh := os.Getenv("GIT_SSH_COMMAND")
	if ssh == "" && len(configured) > 0 {
		ssh = configured[len(configured)-1]
	}
	if ssh == "" {
		if executable := os.Getenv("GIT_SSH"); executable != "" {
			ssh = "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
		} else {
			ssh = "ssh"
		}
	}
	// Git's existing SSH command semantics are the sole shell-command boundary.
	// Keep the user's executable/config/trust policy; never accept host keys.
	return setEnvironment(environment(), map[string]string{
		"GIT_SSH_COMMAND": ssh + " -o BatchMode=yes", "GIT_ASKPASS": "false",
		"SSH_ASKPASS": "false", "SSH_ASKPASS_REQUIRE": "never",
	}), nil
}

// Fetch uses eligible existing mappings without pruning and bounds the entire
// fetch process group to 60 seconds. Partial updates are possible on failure.
func (r *Repository) Fetch(ctx context.Context) error {
	remotes, err := r.remotes(ctx)
	if err != nil {
		return err
	}
	var eligible []remote
	for _, remote := range remotes {
		if !remote.skip {
			eligible = append(eligible, remote)
		}
	}
	if len(eligible) == 0 {
		return errors.New("No eligible remotes. Configure a remote (or review skipFetchAll) before fetching.")
	}
	if err := validateFetchMappings(eligible); err != nil {
		return err
	}
	env, err := r.mutationEnvironment(ctx)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, code, err := execute(bounded, r.root, env, "", append(noPruneArgs(remotes), "fetch", "--all", "--no-prune")...)
	if err != nil {
		if errors.Is(bounded.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return errors.New("Fetch timed out after 60 seconds. Some refs may have updated; refresh and check connectivity/authentication before retrying.")
		}
		return err
	}
	if code != 0 {
		return fmt.Errorf("Fetch failed (exit %d). Some refs may have updated; refresh and check remote connectivity/authentication with Git. No pruning was requested.", code)
	}
	return nil
}

// Pull revalidates the worktree immediately before integration. Once launched,
// integration deliberately ignores context cancellation and has no timeout:
// ordinary quit must wait. Failures retain all conflict/operation state.
func (r *Repository) Pull(ctx context.Context, mode PullMode) error {
	if mode != FastForward && mode != Merge && mode != Rebase {
		return errors.New("Unknown pull mode.")
	}
	status, err := r.Status(ctx)
	if err != nil {
		return err
	}
	if status.Operation != "" {
		return fmt.Errorf("Cannot pull during %s. Resolve or finish that operation outside Gitbuddy.", status.Operation)
	}
	if status.Branch == "" || status.Head == "" {
		return errors.New("Cannot pull without an attached, committed branch. Select a branch outside Gitbuddy.")
	}
	if status.Upstream == "" {
		return errors.New("Cannot pull without an upstream. Configure the branch upstream outside Gitbuddy.")
	}
	if status.Dirty {
		return errors.New("Cannot pull with index, working-tree or untracked changes. Commit, stash or remove them yourself outside Gitbuddy.")
	}
	remotes, err := r.remotes(ctx)
	if err != nil {
		return err
	}
	upstream, err := r.config(ctx, "branch."+status.Branch+".remote")
	if err != nil {
		return err
	}
	for _, candidate := range remotes {
		if len(upstream) > 0 && candidate.name == upstream[len(upstream)-1] {
			if err := validateFetchMappings([]remote{candidate}); err != nil {
				return err
			}
		}
	}
	env, err := r.mutationEnvironment(ctx)
	if err != nil {
		return err
	}
	args := append(noPruneArgs(remotes), "-c", "pull.rebase=false", "-c", "pull.ff=true",
		"-c", "rebase.autoStash=false", "-c", "merge.autoStash=false", "-c", "rebase.updateRefs=false",
		"pull", "--no-prune", "--no-autostash")
	switch mode {
	case Rebase:
		args = append(args, "--rebase", "--ff")
	case Merge:
		args = append(args, "--no-rebase", "--ff")
	case FastForward:
		args = append(args, "--no-rebase", "--ff-only")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, code, err := execute(context.Background(), r.root, env, "", args...)
	if err != nil {
		return err
	}
	if code != 0 {
		operation, err := r.operation(context.Background())
		if err != nil {
			return fmt.Errorf("Pull %s failed (exit %d). No automatic reset, stash or abort was performed; operation state could not be inspected.", mode, code)
		}
		recovery := "No automatic reset, stash or abort was performed. Check divergence, upstream and connectivity with Git."
		if operation != "" {
			recovery = operation + " state is preserved; resolve or abort it yourself outside Gitbuddy."
		}
		return fmt.Errorf("Pull %s failed (exit %d). %s", mode, code, recovery)
	}
	return nil
}
