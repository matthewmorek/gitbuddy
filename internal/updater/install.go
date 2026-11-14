package updater

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Install downloads and verifies a pinned release, then atomically replaces the
// running executable. The caller must obtain approval and settle Git mutations
// first. Cancellation before rename leaves the existing executable untouched;
// after rename the update is successful even if the context is then canceled.
//
// Filesystem rechecks detect observed replacement, not a malicious same-user
// process racing the final check and rename. No backup or user configuration is
// removed, and the application is never automatically restarted.
func (c *Client) Install(ctx context.Context, release Release) error {
	c.mu.Lock()
	if c.installing {
		c.mu.Unlock()
		return errors.New("an update is already installing")
	}
	if c.receipt != nil {
		c.mu.Unlock()
		return errors.New("an update is already installed; restart gitbuddy first")
	}
	c.installing = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.installing = false
		c.mu.Unlock()
	}()
	if c.reason != "" {
		return errors.New(c.reason)
	}
	if c.target == "" {
		return errors.New(unsupportedTargetReason)
	}
	if !stable(c.version) || !stable(release.Version) || !newer(release.Version, c.version) {
		return errors.New("install requires a strictly newer stable X.Y.Z release")
	}
	if err := c.unchanged(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}

	root, err := os.OpenRoot(filepath.Dir(c.path))
	if err != nil {
		return fmt.Errorf("open installation directory: %w", err)
	}
	defer root.Close()
	dirInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(dirInfo, c.baseline.dirs[0].info) {
		return errors.New("installation directory changed before staging")
	}
	stage := ".gitbuddy-update-" + rand.Text()
	if err := root.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("create private update directory: %w", err)
	}
	stageInfo, err := root.Lstat(stage)
	if err != nil {
		return fmt.Errorf("inspect private update directory: %w", err)
	}
	defer func() {
		if info, err := root.Lstat(stage); err == nil && os.SameFile(info, stageInfo) {
			// Anchoring cleanup to the opened destination prevents following a
			// replaced ancestor into some unrelated directory.
			_ = root.RemoveAll(stage)
		}
	}()
	asset := c.target.assetName(release.Version)
	base := c.downloadBase + "/v" + release.Version + "/"
	staged := stage + "/" + asset
	f, err := root.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create staged executable: %w", err)
	}
	hash := sha256.New()
	err = c.download(ctx, base+asset, io.MultiWriter(f, hash), maxBinary)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("download release executable: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged executable: %w", closeErr)
	}
	var manifest bytes.Buffer
	if err := c.download(ctx, base+"SHA256SUMS", &manifest, maxManifest); err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	expected, err := checksum(manifest.String(), asset)
	if err != nil {
		return err
	}
	if !bytes.Equal(hash.Sum(nil), expected) {
		return errors.New("release checksum mismatch; existing executable was not changed")
	}
	// Chmod the open file, not a path that could be redirected by a symlink.
	binary, err := root.OpenFile(staged, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = binary.Chmod(0755)
	stagedInfo, statErr := binary.Stat()
	closeErr = binary.Close()
	if err != nil || statErr != nil || closeErr != nil {
		return errors.New("cannot mark verified release executable")
	}
	stagedPath := filepath.Join(filepath.Dir(c.path), staged)
	if err := c.probe(ctx, stagedPath, release.Version); err != nil {
		return fmt.Errorf("verify release --version: %w", err)
	}
	// Validate the receipt before commit, so no fallible post-rename operation
	// can turn a successful installation into a reported failure.
	currentInfo, err := root.Lstat(staged)
	if err != nil || !sameFileState(stagedInfo, currentInfo) {
		return errors.New("staged executable changed during verification")
	}
	stagedFile, err := root.Open(staged)
	if err != nil {
		return err
	}
	verifiedHash := sha256.New()
	_, err = io.Copy(verifiedHash, io.LimitReader(stagedFile, maxBinary+1))
	after, statErr := stagedFile.Stat()
	closeErr = stagedFile.Close()
	if err != nil || statErr != nil || closeErr != nil ||
		!sameFileState(stagedInfo, after) || !bytes.Equal(verifiedHash.Sum(nil), expected) {
		return errors.New("staged executable changed after verification")
	}
	if err := c.unchanged(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(staged, "gitbuddy"); err != nil {
		return fmt.Errorf("atomically replace executable: %w", err)
	}
	receipt := &snapshot{file: stagedInfo, dirs: c.baseline.dirs}
	copy(receipt.hash[:], expected)
	c.mu.Lock()
	c.receipt = receipt
	c.mu.Unlock()
	return nil
}

func (c *Client) unchanged() error {
	current, err := inspect(c.path, c.uid)
	if err != nil {
		return fmt.Errorf("recheck installation: %w%s", err, manualInstall)
	}
	if !sameSnapshot(c.baseline, current) {
		return errors.New("executable or installation directory changed since launch; restart and check again")
	}
	return nil
}

func (c *Client) download(ctx context.Context, location string, out io.Writer, limit int64) error {
	resp, err := request(ctx, c.downloadHTTP, location)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("download release", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return errors.New("download exceeds the size limit")
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("download exceeds the size limit")
	}
	return ctx.Err()
}

func checksum(manifest, asset string) ([]byte, error) {
	var expected []byte
	for _, line := range strings.Split(manifest, "\n") {
		if line == "" {
			continue
		}
		if len(line) < 67 || line[64] != ' ' || line[65] != ' ' && line[65] != '*' {
			return nil, errors.New("invalid SHA256SUMS entry")
		}
		digest, err := hex.DecodeString(line[:64])
		name := line[66:]
		if err != nil || len(digest) != sha256.Size || name == "" ||
			filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsAny(name, "\\\r\x00") {
			return nil, errors.New("invalid SHA256SUMS filename or hash")
		}
		if name == asset {
			if expected != nil {
				return nil, errors.New("SHA256SUMS has duplicate release entries")
			}
			expected = digest
		}
	}
	if expected == nil {
		return nil, errors.New("SHA256SUMS must contain exactly one matching release filename")
	}
	return expected, nil
}

// Restart replaces this process only after validating the installed receipt.
// The caller must restore the terminal and settle all mutations first. Arguments,
// environment and current working directory are preserved by exec.
func (c *Client) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.installing || c.receipt == nil {
		return errors.New("no completed update is ready to restart")
	}
	current, err := inspect(c.path, c.uid)
	if err != nil || !sameSnapshot(c.receipt, current) {
		return errors.New("installed executable or directory changed; refusing restart")
	}
	return c.exec(c.path, os.Args, os.Environ())
}

type limitedOutput struct {
	buffer bytes.Buffer
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1024 {
		return 0, errors.New("version output exceeds 1 KiB")
	}
	return b.buffer.Write(p)
}

func probeVersion(ctx context.Context, path, version string) error {
	target := supportedTarget(runtime.GOOS, runtime.GOARCH)
	if err := target.validateExecutable(path); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// Do not pass loader injection, credentials, or user shell configuration
	// into the downloaded executable's first, non-interactive invocation.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.Dir = filepath.Dir(path)
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if target == darwinARM64 {
			return fmt.Errorf("cannot run verified release (macOS security may block it): %w", err)
		}
		return fmt.Errorf("cannot run verified release (check Linux execution permissions and mount options): %w", err)
	}
	if stdout.buffer.String() != "gitbuddy "+version+"\n" || stderr.buffer.Len() != 0 {
		return errors.New("release did not report the exact requested version")
	}
	return nil
}
