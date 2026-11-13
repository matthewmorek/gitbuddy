//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real Git invokes this test executable as an SSH transport or post-merge hook.
// Neither helper contacts a host; all files and children belong to the fixture.
func TestMain(m *testing.M) {
	dir := os.Getenv("GITBUDDY_TEST_PROCESS_DIR")
	if dir != "" {
		switch filepath.Base(os.Args[0]) {
		case "gitbuddy-test-ssh":
			if len(os.Args) > 1 && os.Args[1] == "descendant" {
				signal.Reset(syscall.SIGTERM)
				_ = os.WriteFile(filepath.Join(dir, "descendant"), []byte(strconv.Itoa(os.Getpid())), 0600)
				time.Sleep(10 * time.Second)
				os.Exit(0)
			}
			signal.Ignore(syscall.SIGTERM)
			child := exec.Command(os.Args[0], "descendant")
			child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
			if child.Start() != nil {
				os.Exit(2)
			}
			_ = os.WriteFile(filepath.Join(dir, "ssh"), []byte(strconv.Itoa(os.Getpid())), 0600)
			_ = child.Wait()
			_ = os.WriteFile(filepath.Join(dir, "descendant-reaped"), nil, 0600)
			time.Sleep(10 * time.Second)
			os.Exit(0)
		case "post-merge":
			_ = os.WriteFile(filepath.Join(dir, "hook-started"), nil, 0600)
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(filepath.Join(dir, "hook-release")); err == nil {
					os.Exit(0)
				}
				time.Sleep(10 * time.Millisecond)
			}
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(path); err == nil {
			return content
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fixture file %s", path)
	return nil
}

func linkTestExecutable(t *testing.T, path string) {
	t.Helper()
	executable, err := os.Executable()
	mustOK(t, err)
	mustOK(t, os.Symlink(executable, path))
}

func TestFetchCancellationRetiresProcessGroup(t *testing.T) {
	f := newFixture(t)
	path := f.repo("repo", false)
	f.git(path, "remote", "add", "origin", "ssh://unused.invalid/repo")
	helper := filepath.Join(f.root, "gitbuddy-test-ssh")
	linkTestExecutable(t, helper)
	t.Setenv("GITBUDDY_TEST_PROCESS_DIR", f.root)
	t.Setenv("GIT_ALLOW_PROTOCOL", "ssh")
	t.Setenv("GIT_SSH_COMMAND", "'"+strings.ReplaceAll(helper, "'", "'\\''")+"'")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	r := openRepo(t, path)
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- r.Fetch(ctx) }()
	pidText := waitForFile(t, filepath.Join(f.root, "ssh"))
	waitForFile(t, filepath.Join(f.root, "descendant"))
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not retire its transport")
	}
	// The descendant was signalled and reaped; its TERM-resistant parent then
	// required the runner's process-group KILL escalation.
	waitForFile(t, filepath.Join(f.root, "descendant-reaped"))
	pid, err := strconv.Atoi(string(pidText))
	mustOK(t, err)
	// Git's exited transport may briefly remain a zombie until the OS reaps it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transport process still exists after cancellation")
}

func TestActivePullWaitsDespiteCallerCancellation(t *testing.T) {
	f := newFixture(t)
	local, writer, _ := f.paired()
	hooks := filepath.Join(f.root, "hooks")
	mustOK(t, os.Mkdir(hooks, 0700))
	linkTestExecutable(t, filepath.Join(hooks, "post-merge"))
	t.Setenv("GITBUDDY_TEST_PROCESS_DIR", f.root)
	f.git(local, "config", "core.hooksPath", hooks)
	next := f.commit(writer, "next", "next", "next")
	f.git(writer, "push", "origin", "main")
	r := openRepo(t, local)
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- r.Pull(ctx, FastForward) }()
	waitForFile(t, filepath.Join(f.root, "hook-started"))
	cancel()
	select {
	case err := <-result:
		t.Fatalf("active pull returned before its hook finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	mustOK(t, os.WriteFile(filepath.Join(f.root, "hook-release"), nil, 0600))
	select {
	case err := <-result:
		mustOK(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("pull did not finish after hook release")
	}
	equal(t, statusOf(t, r).Head, next)
	equal(t, statusOf(t, r).Operation, "")
}
