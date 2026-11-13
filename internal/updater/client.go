// Package updater checks public stable releases and replaces an explicitly
// approved, user-owned release executable. Checks never install or restart.
//
// Checksums share GitHub's trust boundary: they do not protect against a
// compromised release host. Updates do not alter quarantine or preferences.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	latestURL     = "https://api.github.com/repos/matthewmorek/gitbuddy/releases/latest"
	releasesURL   = "https://github.com/matthewmorek/gitbuddy/releases/download"
	maxMetadata   = 1 << 20
	maxBinary     = 64 << 20
	maxManifest   = 64 << 10
	manualInstall = "; use the manual installer in the gitbuddy README"
)

// Release identifies a stable version, without the tag's leading v.
// An empty Version from Check means that no newer stable release is available.
type Release struct{ Version string }

// Client owns install serialization and a receipt for its own successful update.
// Its methods are safe for concurrent use. UI lifecycle and Git mutation
// coordination remain the caller's responsibility.
type Client struct {
	version      string
	reason       string
	path         string
	baseline     *snapshot
	uid          int
	checkHTTP    *http.Client
	downloadHTTP *http.Client
	checkURL     string
	downloadBase string
	probe        func(context.Context, string, string) error
	exec         func(string, []string, []string) error

	mu         sync.Mutex
	installing bool
	receipt    *snapshot
}

// New captures the running executable's identity. Only release builds running
// as a non-root user on macOS arm64 can install; source builds never qualify.
func New(version string, releaseBuild bool) *Client {
	c := &Client{
		version: version, uid: os.Getuid(),
		checkHTTP:    newHTTPClient(10*time.Second, true),
		downloadHTTP: newHTTPClient(60*time.Second, false),
		checkURL:     latestURL, downloadBase: releasesURL,
		probe: probeVersion, exec: execProcess,
	}
	switch {
	case !releaseBuild:
		c.reason = "self-update is only available in official release builds"
	case runtime.GOOS != "darwin" || runtime.GOARCH != "arm64":
		c.reason = "self-update requires macOS arm64"
	case !stable(version):
		c.reason = "the running version is not a stable X.Y.Z release"
	case c.uid <= 0 || os.Geteuid() != c.uid:
		c.reason = "self-update must run as your normal user, not sudo or root"
	default:
		var err error
		c.path, err = os.Executable()
		if err == nil {
			c.baseline, err = inspect(c.path, c.uid)
		}
		if err != nil {
			c.reason = "self-update is unavailable: " + err.Error()
		}
	}
	if c.reason != "" {
		c.reason += manualInstall
	}
	return c
}

// InstallReason is a cached, cheap eligibility explanation, or empty if eligible.
// Install independently rechecks filesystem state before changing anything.
func (c *Client) InstallReason() string { return c.reason }

// Check returns only a strictly newer stable version. Missing releases (404)
// are normal; malformed metadata and network failures are actionable errors.
// Requests are unauthenticated, bounded to ten seconds and 1 MiB.
func (c *Client) Check(ctx context.Context) (Release, error) {
	if !stable(c.version) {
		return Release{}, errors.New("cannot compare the running version; expected stable X.Y.Z")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := request(ctx, c.checkHTTP, c.checkURL)
	if err != nil {
		return Release{}, fmt.Errorf("check GitHub releases: %w; check your connection and try again", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, responseError("check GitHub releases", resp.StatusCode)
	}
	body, err := readBounded(resp.Body, maxMetadata)
	if err != nil {
		return Release{}, fmt.Errorf("read GitHub release metadata: %w", err)
	}
	var metadata struct {
		Tag         *string `json:"tag_name"`
		Draft       *bool   `json:"draft"`
		Prerelease  *bool   `json:"prerelease"`
		PublishedAt *string `json:"published_at"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return Release{}, fmt.Errorf("invalid GitHub release metadata: %w", err)
	}
	if metadata.Tag == nil || metadata.Draft == nil || metadata.Prerelease == nil ||
		metadata.PublishedAt == nil || *metadata.Draft || *metadata.Prerelease {
		return Release{}, errors.New("GitHub did not return a published stable release")
	}
	if _, err := time.Parse(time.RFC3339, *metadata.PublishedAt); err != nil {
		return Release{}, errors.New("GitHub release has an invalid publication date")
	}
	version, ok := strings.CutPrefix(*metadata.Tag, "v")
	if !ok || !stable(version) {
		return Release{}, errors.New("GitHub release tag must be stable vX.Y.Z")
	}
	if !newer(version, c.version) {
		return Release{}, nil
	}
	return Release{Version: version}, nil
}

func stable(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return false
		}
		for i := range part {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

// The inputs have already passed stable, so decimal length and byte order
// compare without imposing a machine-integer limit on SemVer components.
func newer(left, right string) bool {
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return len(a[i]) > len(b[i])
		}
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func newHTTPClient(timeout time.Duration, api bool) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("GitHub redirected more than five times")
			}
			return trustedURL(req.URL, api)
		},
	}
}

func trustedURL(u *url.URL, api bool) error {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return errors.New("refusing an unsafe GitHub redirect")
	}
	switch u.Hostname() {
	case "github.com", "objects.githubusercontent.com",
		"release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	case "api.github.com":
		if api {
			return nil
		}
	}
	return errors.New("refusing a redirect outside GitHub release hosts")
}

func request(ctx context.Context, client *http.Client, location string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitbuddy-updater")
	return client.Do(req)
}

func responseError(operation string, status int) error {
	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		return fmt.Errorf("%s: GitHub denied the request (HTTP %d); unauthenticated rate limits may apply, try later", operation, status)
	}
	return fmt.Errorf("%s: HTTP %d; check connectivity and that the release is published", operation, status)
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds the size limit")
	}
	return data, nil
}
