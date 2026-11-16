//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func environment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX", "GIT_SHALLOW_FILE",
			"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS":
			continue
		}
		env = append(env, entry)
	}
	return setEnvironment(env, map[string]string{
		"LC_ALL": "C", "GIT_OPTIONAL_LOCKS": "0", "GIT_TERMINAL_PROMPT": "0", "GCM_INTERACTIVE": "Never",
		"GIT_PAGER": "cat", "GIT_EDITOR": "true", "GIT_SEQUENCE_EDITOR": "true", "GIT_MERGE_AUTOEDIT": "no",
	})
}

func setEnvironment(env []string, values map[string]string) []string {
	result := make([]string, 0, len(env)+len(values))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := values[key]; !replace {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

// execute never exposes stderr or command arguments: remote URLs can contain
// credentials. A separate session denies controlling-terminal auth prompts.
// Cancellation terminates the process group, then escalates after one second.
func execute(ctx context.Context, cwd string, env []string, input string, args ...string) (string, int, error) {
	return executeCapture(ctx, cwd, env, input, 0, args...)
}

var errCaptureTooLarge = errors.New("Selected-file content exceeds the 2 MiB limit.")

// captureLimit is opt-in: mutation execution retains its existing behavior.
// Overflow never returns an apparently complete, truncated patch.
type captureLimit struct {
	buffer bytes.Buffer
	limit  int
	full   chan struct{}
}

func (b *captureLimit) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		close(b.full)
		return 0, errCaptureTooLarge
	}
	return b.buffer.Write(p)
}

func (b *captureLimit) String() string { return b.buffer.String() }

func executeCapture(ctx context.Context, cwd string, env []string, input string, limit int, args ...string) (string, int, error) {
	if err := ctx.Err(); err != nil {
		return "", -1, err
	}
	cmd := exec.Command("git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = cwd
	if env == nil {
		env = environment()
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	var captured interface {
		io.Writer
		String() string
	} = &out
	var full <-chan struct{}
	if limit > 0 {
		bounded := &captureLimit{limit: limit, full: make(chan struct{})}
		captured, full = bounded, bounded.full
	}
	cmd.Stdout = captured
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return "", -1, errors.New("Cannot start Git. Check that Git is installed and the repository path is accessible.")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	var stopped error
	waited := false
	select {
	case err = <-done:
		waited = true
		// The copy goroutine may finish before this select observes overflow.
		select {
		case <-full:
			stopped = errCaptureTooLarge
		default:
		}
	case <-ctx.Done():
		stopped = ctx.Err()
	case <-full:
		stopped = errCaptureTooLarge
	}
	if stopped != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		// Always retire descendants too, even if the leader exits on SIGTERM.
		timer := time.NewTimer(time.Second)
		if waited {
			<-timer.C
		} else {
			select {
			case <-done:
				<-timer.C
			case <-timer.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return "", -1, stopped
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return captured.String(), exit.ExitCode(), nil
		}
		return "", -1, errors.New("Git process did not finish cleanly. Inspect the repository with Git.")
	}
	return captured.String(), 0, nil
}
