package gitrepo

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const stashRef = "refs/stash"
const stashLog = "logs/" + stashRef

var errStashChanged = errors.New("The selected stash or its stack changed. Refresh Stashes and confirm again.")

type stashStack struct {
	info os.FileInfo
	sum  [32]byte
	rows int
}

func (s *stashStack) matches(other *stashStack) bool {
	return s != nil && other != nil && s.sum == other.sum && sameStashFile(s.info, other.info)
}

func sameStashFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) &&
		a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (r *Repository) filesStashPath(ctx context.Context) (string, error) {
	format, err := r.config(ctx, "extensions.refStorage")
	if err != nil {
		return "", err
	}
	if len(format) != 0 && (len(format) != 1 || format[0] != "files") {
		return "", errors.New("Gitbuddy cannot safely delete stack entries with this Git ref storage. Use Git to delete the intended entry.")
	}
	return r.commonDir(ctx)
}

func readRegularStashFile(path string) (string, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	const maxSize = 8 << 20
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 || info.Size() > maxSize {
		return "", nil, errors.New("Unsupported stash metadata (requires a regular file with one link and no larger than 8 MiB).")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return "", nil, err
	}
	current, err := os.Lstat(path)
	actual, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) > maxSize || int64(len(data)) != info.Size() ||
		!sameStashFile(info, current) || !sameStashFile(info, actual) {
		return "", nil, errStashChanged
	}
	return string(data), info, nil
}

func readStashStack(common string) (*stashStack, string, error) {
	if common == "" {
		return nil, "", nil
	}
	log, info, err := readRegularStashFile(filepath.Join(common, stashLog))
	if err != nil {
		return nil, "", err
	}
	return &stashStack{info: info, sum: sha256.Sum256([]byte(log)), rows: strings.Count(log, "\n")}, log, nil
}

// DeleteStash removes exactly the confirmed entry. Recovery backups use Git's
// compare-and-delete on a direct ref; manual stacks require a frozen reflog.
func (r *Repository) DeleteStash(ctx context.Context, selected Stash) error {
	if err := validateSHA(selected.ID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if selected.Recovery {
		return r.deleteRecoveryStash(context.WithoutCancel(ctx), selected.ID)
	}
	common, err := r.filesStashPath(ctx)
	if err != nil {
		return err
	}
	if selected.stack == nil {
		return errors.New("Cannot verify this stash stack snapshot. Refresh Stashes or inspect the stack with Git.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return deleteFilesStash(common, selected)
}

func (r *Repository) deleteRecoveryStash(ctx context.Context, id string) error {
	guard, err := r.lockSafeBackup(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	// Custom lock timeouts and ref-transaction hooks must not hang orderly quit.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ref := safeBackupRefs + id
	changed := errors.New("Could not delete the selected direct recovery ref. It may have changed or be locked. Refresh Stashes and check with Git.")
	// --no-deref still accepts a symbolic ref whose target has the old OID.
	// Ask Git to prepare (and lock) its CAS, then check the type before commit.
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "update-ref", "--stdin")
	cmd.Dir, cmd.Env = r.root, environment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	in, err := cmd.StdinPipe()
	if err != nil {
		return changed
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return changed
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return changed
	}
	waited := false
	defer func() {
		if !waited {
			in.Close() // EOF aborts the prepared transaction.
			out.Close()
			cmd.Wait()
		}
	}()
	reader := bufio.NewReader(out)
	ack := func(want string) bool {
		line, err := reader.ReadSlice('\n')
		return err == nil && string(line) == want+": ok\n"
	}
	if _, err := fmt.Fprintf(in, "start\noption no-deref\ndelete %s %s\nprepare\n", ref, id); err != nil ||
		!ack("start") || !ack("prepare") {
		return changed
	}
	if _, code, err := execute(ctx, r.root, nil, "", "symbolic-ref", "--quiet", "--no-recurse", ref); err != nil || code != 1 {
		return changed
	}
	if _, err := io.WriteString(in, "commit\n"); err != nil || !ack("commit") {
		return errors.New("Git did not confirm deletion of the recovery ref. Refresh Stashes and check with Git.")
	}
	in.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		return errors.New("Git did not confirm deletion of the recovery ref. Refresh Stashes and check with Git.")
	}
	return nil
}

// Git's reflog delete resolves the positional selector before locking. Plan
// the rewrite from the frozen selection instead, while holding both Git locks.
func planFilesStashDelete(common string, selected Stash) (stashDeletePlan, error) {
	var plan stashDeletePlan
	ref, log := filepath.Join(common, stashRef), filepath.Join(common, stashLog)
	current, original, err := readStashStack(common)
	if err != nil || !selected.stack.matches(current) {
		return plan, errStashChanged
	}
	refText, refInfo, err := readRegularStashFile(ref)
	tip := strings.TrimSuffix(refText, "\n")
	if err != nil || validateSHA(tip) != nil || refText != tip+"\n" {
		return plan, errors.New("Safe deletion requires a loose, direct stash ref. Inspect this stash stack with Git.")
	}
	plan.oldTip, plan.newTip, plan.refMode = tip, tip, refInfo.Mode().Perm()
	plan.logInfo = current.info
	plan.oldLog = identifyStashLog(current.info, current.sum)
	rows := strings.Split(strings.TrimSuffix(original, "\n"), "\n")
	oidLen := len(tip)
	zero := strings.Repeat("0", oidLen)
	at := len(rows) - 1 - selected.position // Raw log order is oldest first.
	invalidLog := errors.New("Unsupported stash reflog layout. Inspect the stack with Git.")
	if !strings.HasSuffix(original, "\n") || at < 0 || at >= len(rows) {
		return plan, invalidLog
	}
	matches := 0
	ids := make([]string, len(rows))
	for i, row := range rows {
		old, rest, ok := strings.Cut(row, " ")
		id, meta, complete := strings.Cut(rest, " ")
		if !ok || !complete || len(old) != oidLen || len(id) != oidLen ||
			validateSHA(old) != nil || validateSHA(id) != nil || id == zero {
			return plan, invalidLog
		}
		ids[i] = id
		if id == selected.ID {
			matches++
		}
		if i == at {
			ident, subject, ok := strings.Cut(meta, "\t")
			parts := strings.Fields(ident)
			if !ok || len(parts) < 3 || parts[len(parts)-2] != strconv.FormatInt(selected.Date.Unix(), 10) ||
				subject != selected.Subject || id != selected.ID {
				return plan, errStashChanged
			}
		}
	}
	if matches != 1 {
		return plan, errors.New("Multiple stack entries use this stash ID. Delete the intended entry with Git.")
	}
	if ids[len(ids)-1] != tip {
		return plan, invalidLog
	}
	if at+1 < len(rows) {
		previous := zero
		if at > 0 {
			previous = ids[at-1]
		}
		rows[at+1] = previous + rows[at+1][oidLen:]
	} else if at > 0 {
		plan.newTip = ids[at-1]
	} else {
		plan.newTip = ""
	}
	rows = append(rows[:at], rows[at+1:]...)
	if len(rows) > 0 {
		plan.text = strings.Join(rows, "\n") + "\n"
	}
	info, err := os.Lstat(log)
	if err != nil || !sameStashFile(current.info, info) {
		return plan, errStashChanged
	}
	return plan, nil
}

func checkStashDirectories(common string) error {
	// Aliased directories could share a reflog with another repository that
	// uses a different ref lock. This path supports Git's ordinary layout.
	for _, dir := range []string{"refs", "logs", "logs/refs"} {
		info, err := os.Lstat(filepath.Join(common, dir))
		if errors.Is(err, os.ErrNotExist) {
			return errStashChanged
		}
		if err != nil || !info.IsDir() {
			return errors.New("Unsupported stash metadata directory. Use Git to inspect this stack.")
		}
	}
	return nil
}
