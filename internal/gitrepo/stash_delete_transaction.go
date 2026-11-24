package gitrepo

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const stashDeleteDir = "gitbuddy-stash-delete"

type stashLogVersion struct {
	Device, Inode uint64
	Hash          [32]byte
}

func identifyStashLog(info os.FileInfo, sum [32]byte) stashLogVersion {
	stat := info.Sys().(*syscall.Stat_t)
	return stashLogVersion{uint64(stat.Dev), uint64(stat.Ino), sum}
}

func stashLogAt(path string) (stashLogVersion, error) {
	text, info, err := readRegularStashFile(path)
	if err != nil {
		return stashLogVersion{}, err
	}
	return identifyStashLog(info, sha256.Sum256([]byte(text))), nil
}

type stashDeleteRecord struct {
	OldLog, NewLog stashLogVersion
	OldTip, NewTip string
}

type stashDeletePlan struct {
	oldLog         stashLogVersion
	oldTip, newTip string
	text           string
	refMode        os.FileMode
	logInfo        os.FileInfo
}

type stashDeleteTxn struct{ common string }

func (t stashDeleteTxn) path(name string) string {
	return filepath.Join(t.common, stashDeleteDir, name)
}

var stashDeleteLocks = [...]struct{ git, owner, label string }{
	{stashRef + ".lock", "ref-owner", "the stash ref"},
	{stashLog + ".lock", "log-owner", "the stash reflog"},
	{"packed-refs.lock", "packed-owner", "packed refs"},
}

func (t stashDeleteTxn) lock(i int) error {
	lock := stashDeleteLocks[i]
	owner := t.path(lock.owner)
	if err := os.WriteFile(owner, nil, 0600); err != nil {
		return err
	}
	// Acquire the Git lock and record its ownership in one operation. Recovery
	// never unlinks a lock without finding its exact matching owner inode.
	if err := os.Link(owner, filepath.Join(t.common, lock.git)); err != nil {
		return fmt.Errorf("Cannot lock %s. Another Git operation may be active or the filesystem may not support safe deletion: %w", lock.label, err)
	}
	return nil
}

func (t stashDeleteTxn) ownsLock(i int) bool {
	lock := stashDeleteLocks[i]
	owner, err := os.Lstat(t.path(lock.owner))
	git, gitErr := os.Lstat(filepath.Join(t.common, lock.git))
	return err == nil && gitErr == nil && owner.Mode().IsRegular() && os.SameFile(owner, git)
}

func (t stashDeleteTxn) prepare(selected Stash) (stashDeleteRecord, error) {
	var record stashDeleteRecord
	if err := checkStashDirectories(t.common); err != nil {
		return record, err
	}
	if err := os.Mkdir(t.path(""), 0700); err != nil {
		return record, err
	}
	for _, i := range []int{0, 1} {
		if err := t.lock(i); err != nil {
			return record, err
		}
	}
	plan, err := planFilesStashDelete(t.common, selected)
	if err != nil {
		return record, err
	}
	if plan.newTip == "" {
		// Do not uncover a packed stash ref when removing the loose one.
		if err := t.lock(2); err != nil {
			return record, err
		}
		data, _, err := readRegularStashFile(filepath.Join(t.common, "packed-refs"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return record, err
		}
		for _, row := range strings.Split(data, "\n") {
			if strings.HasSuffix(row, " "+stashRef) {
				return record, errors.New("Cannot safely delete the last stash with a packed stash ref. Use Git.")
			}
		}
	} else if plan.oldTip != plan.newTip {
		if err := writeStashMetadata(t.path("next-tip"), plan.newTip+"\n", plan.refMode); err != nil {
			return record, err
		}
	}
	if err := writeStashMetadata(t.path("exchange-log"), plan.text, plan.logInfo.Mode().Perm()); err != nil {
		return record, err
	}
	next, err := stashLogAt(t.path("exchange-log"))
	if err != nil {
		return record, err
	}
	record = stashDeleteRecord{plan.oldLog, next, plan.oldTip, plan.newTip}
	data, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	if err := os.WriteFile(t.path("record.tmp"), data, 0600); err != nil {
		return record, err
	}
	return record, os.Rename(t.path("record.tmp"), t.path("record"))
}

func writeStashMetadata(path, text string, mode os.FileMode) error {
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (t stashDeleteTxn) installLog(record stashDeleteRecord) error {
	return t.swapLog(record.OldLog, record.NewLog)
}

func (t stashDeleteTxn) swapLog(wantLive, wantSaved stashLogVersion) error {
	log, backup := filepath.Join(t.common, stashLog), t.path("exchange-log")
	if current, err := stashLogAt(log); err != nil || current != wantLive {
		return errStashChanged
	}
	if candidate, err := stashLogAt(backup); err != nil || candidate != wantSaved {
		return errStashChanged
	}
	// Some Git versions can unlink a reflog outside the ref lock. Exchange
	// refuses a missing log and retains the exact original until settlement.
	if err := exchangeStashLogs(backup, log); errors.Is(err, os.ErrNotExist) {
		return errStashChanged
	} else if err != nil {
		return fmt.Errorf("Could not atomically exchange the stash reflog. Use Git to delete the intended entry: %w", err)
	}
	if swapped, err := stashLogAt(backup); err != nil || swapped != wantLive {
		// An external writer replaced the log in the exchange window. Swap
		// back only if our replacement is still installed; otherwise retain
		// all evidence for inspection.
		if installed, err := stashLogAt(log); err == nil && installed == wantSaved {
			return errors.Join(errStashChanged, exchangeStashLogs(backup, log))
		}
		return errStashChanged
	}
	return nil
}

func (t stashDeleteTxn) installTip(record stashDeleteRecord) error {
	if record.OldTip == record.NewTip {
		return nil
	}
	log, err := stashLogAt(filepath.Join(t.common, stashLog))
	if err != nil || log != record.NewLog {
		return errStashChanged
	}
	ref := filepath.Join(t.common, stashRef)
	text, _, err := readRegularStashFile(ref)
	if err != nil || text != record.OldTip+"\n" {
		return errStashChanged
	}
	if record.NewTip == "" {
		return os.Rename(ref, t.path("retired-tip"))
	}
	next := t.path("next-tip")
	text, _, err = readRegularStashFile(next)
	if err != nil {
		return err
	}
	if text != record.NewTip+"\n" {
		return errStashChanged
	}
	return os.Rename(next, ref)
}

// settle is used after success, after ordinary failure, and by the next
// process after interruption. With the Git locks still held, roll back an
// exchanged log if the corresponding tip change never committed; otherwise
// finish the committed deletion. A middle deletion commits at exchange.
func (t stashDeleteTxn) settle(record stashDeleteRecord) error {
	if !t.ownsLock(0) || !t.ownsLock(1) || record.NewTip == "" && !t.ownsLock(2) {
		return errors.New("The interrupted stash deletion no longer owns its Git locks.")
	}
	backup, log := t.path("exchange-log"), filepath.Join(t.common, stashLog)
	saved, err := stashLogAt(backup)
	if err == nil && saved == record.NewLog {
		return nil // The log was never exchanged, or was already rolled back.
	}
	if err != nil || saved != record.OldLog {
		return errStashChanged
	}
	tip, _, err := readRegularStashFile(filepath.Join(t.common, stashRef))
	if !errors.Is(err, os.ErrNotExist) && err != nil {
		return err
	}
	lastRemoved := record.NewTip == "" && errors.Is(err, os.ErrNotExist)
	if lastRemoved {
		old, _, err := readRegularStashFile(t.path("retired-tip"))
		if err != nil || old != record.OldTip+"\n" {
			return errStashChanged
		}
	}
	live, err := stashLogAt(log)
	if lastRemoved && errors.Is(err, os.ErrNotExist) {
		return nil // Interrupted after the empty log was removed.
	}
	if err != nil || live != record.NewLog {
		return errStashChanged
	}
	switch {
	case lastRemoved:
		return os.Remove(log)
	case tip == record.NewTip+"\n":
		return nil
	case tip == record.OldTip+"\n":
		return t.swapLog(record.NewLog, record.OldLog)
	default:
		return errStashChanged
	}
}

// Inventory the entire directory before settlement, which can itself remove
// the live reflog. An inspection copy or an unexpected file type is not ours.
func (t stashDeleteTxn) recoveryFiles() (map[string]os.FileInfo, error) {
	entries, err := os.ReadDir(t.path(""))
	if err != nil {
		return nil, err
	}
	files := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		unexpected := fmt.Errorf("Unexpected stash deletion artifact %q.", name)
		if !info.Mode().IsRegular() || !ok {
			return nil, unexpected
		}
		switch name {
		case "record", "record.tmp", "exchange-log", "next-tip", "retired-tip", "finished":
			if stat.Nlink != 1 || name == "finished" && info.Size() != 0 {
				return nil, unexpected
			}
		default:
			owner := false
			for i, lock := range stashDeleteLocks {
				if name == lock.owner && info.Size() == 0 &&
					(stat.Nlink == 1 || stat.Nlink == 2 && t.ownsLock(i)) {
					owner = true
					break
				}
			}
			if !owner {
				return nil, unexpected
			}
		}
		files[name] = info
	}
	return files, nil
}

func (t stashDeleteTxn) recoveryRecord(files map[string]os.FileInfo) (stashDeleteRecord, error) {
	var record stashDeleteRecord
	if files["record"] != nil {
		text, _, err := readRegularStashFile(t.path("record"))
		if err != nil {
			return record, err
		}
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return record, err
		}
		if validateSHA(record.OldTip) != nil || record.NewTip != "" && validateSHA(record.NewTip) != nil {
			return record, errors.New("Invalid stash deletion record.")
		}
	}
	for _, name := range []string{"record.tmp", "exchange-log", "next-tip", "retired-tip"} {
		if files[name] == nil {
			continue
		}
		if files["record"] == nil {
			// Without the journal, a prepared file and a saved original
			// cannot be safely distinguished. Leave both bytes and locks.
			return record, fmt.Errorf("Cannot verify stash deletion artifact %q without its record.", name)
		}
		switch name {
		case "exchange-log":
			if log, err := stashLogAt(t.path(name)); err == nil && (log == record.OldLog || log == record.NewLog) {
				continue
			}
		case "next-tip":
			if tip, _, err := readRegularStashFile(t.path(name)); err == nil &&
				record.NewTip != "" && record.NewTip != record.OldTip && tip == record.NewTip+"\n" {
				continue
			}
		case "retired-tip":
			if tip, _, err := readRegularStashFile(t.path(name)); err == nil &&
				record.NewTip == "" && tip == record.OldTip+"\n" {
				continue
			}
		}
		return record, fmt.Errorf("Unexpected stash deletion artifact %q.", name)
	}
	return record, nil
}

func (t stashDeleteTxn) recover() error {
	info, err := os.Lstat(t.path(""))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("Cannot inspect the stash deletion transaction directory.")
	}
	files, err := t.recoveryFiles()
	if err != nil {
		return err
	}
	record, err := t.recoveryRecord(files)
	if err != nil {
		return err
	}
	if files["finished"] == nil {
		if files["record"] != nil {
			if err := t.settle(record); err != nil {
				return err
			}
		}
		// Record settlement before releasing any lock. A subsequent Git
		// writer may advance the stack while only housekeeping remains.
		finished, err := os.OpenFile(t.path("finished"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if err := finished.Close(); err != nil {
			return err
		}
	}
	for i := len(stashDeleteLocks) - 1; i >= 0; i-- {
		if t.ownsLock(i) {
			if err := os.Remove(filepath.Join(t.common, stashDeleteLocks[i].git)); err != nil {
				return err
			}
		}
	}
	// Keep the record while payload files remain and the marker while the
	// record remains, so subsequent recovery can still verify ownership.
	for name := range files {
		if name == "record" || name == "finished" {
			continue
		}
		if err := os.Remove(t.path(name)); err != nil {
			return err
		}
	}
	if files["record"] != nil {
		if err := os.Remove(t.path("record")); err != nil {
			return err
		}
	}
	if err := os.Remove(t.path("finished")); err != nil {
		return err
	}
	return os.Remove(t.path("")) // A late unexpected entry is never removed.
}

func (t stashDeleteTxn) recoverChecked() error {
	if err := t.recover(); err != nil {
		return fmt.Errorf("Cannot recover the stash deletion safely; saved metadata remains in %s in the Git common directory. Do not remove Git locks until the stack is inspected: %w", stashDeleteDir, err)
	}
	return nil
}

func pendingStashDelete(common string) error {
	t := stashDeleteTxn{common}
	if _, err := os.Lstat(t.path("")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	guard, err := lockCommon(common, stashDeleteDir+".lock")
	if err != nil {
		return errors.New("Cannot check stash deletion recovery. Another Gitbuddy operation may be active; refresh Stashes.")
	}
	defer guard.Close()
	return t.recoverChecked()
}

func deleteFilesStash(common string, selected Stash) (err error) {
	guard, err := lockCommon(common, stashDeleteDir+".lock")
	if err != nil {
		return errors.New("Cannot lock the stash deletion transaction. Refresh Stashes and retry.")
	}
	defer guard.Close()
	t := stashDeleteTxn{common}
	if err := t.recoverChecked(); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, t.recoverChecked()) }()
	record, err := t.prepare(selected)
	if err != nil {
		return err
	}
	if err := t.installLog(record); err != nil {
		return err
	}
	return t.installTip(record)
}
