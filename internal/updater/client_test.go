package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStableComparison(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.20.3", "99999999999999999999999999999999999999.0.1"} {
		if !stable(version) {
			t.Errorf("rejected %q", version)
		}
	}
	for _, version := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.3-pre", "1.2.3+build", "1.2.-3", "1.2.٣", "1.2.3\n"} {
		if stable(version) {
			t.Errorf("accepted %q", version)
		}
	}
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.9.99", "1.10.0", false},
		{"2.0.0", "1.99.99", true},
		{"1.2.4", "1.2.3", true},
		{"99999999999999999999999999999999999999.0.0", "9999999999999999999999999999999999999.99.99", true},
		{"1.99999999999999999999999999999999999998.0", "1.99999999999999999999999999999999999999.0", false},
	} {
		if got := newer(tc.left, tc.right); got != tc.want {
			t.Errorf("newer(%q, %q) = %v", tc.left, tc.right, got)
		}
	}
}

func metadata(version string) string {
	return fmt.Sprintf(`{"tag_name":"v%s","draft":false,"prerelease":false,"published_at":"2026-11-13T12:00:00Z"}`, version)
}

func TestCheckReleaseContract(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		fail             bool
	}{
		{name: "newer", body: metadata("0.2.0"), want: "0.2.0"},
		{name: "same", body: metadata("0.1.0")},
		{name: "older", body: metadata("0.0.9")},
		{name: "unpublished repository", status: 404},
		{name: "rate limit", status: 403, fail: true},
		{name: "server failure", status: 503, fail: true},
		{name: "draft", body: strings.Replace(metadata("0.2.0"), `"draft":false`, `"draft":true`, 1), fail: true},
		{name: "prerelease flag", body: strings.Replace(metadata("0.2.0"), `"prerelease":false`, `"prerelease":true`, 1), fail: true},
		{name: "prerelease tag", body: metadata("0.2.0-rc.1"), fail: true},
		{name: "noncanonical tag", body: metadata("00.2.0"), fail: true},
		{name: "missing v", body: strings.Replace(metadata("0.2.0"), "v0.2.0", "0.2.0", 1), fail: true},
		{name: "missing flags", body: `{"tag_name":"v0.2.0","published_at":"2026-11-13T12:00:00Z"}`, fail: true},
		{name: "wrong typed flag", body: strings.Replace(metadata("0.2.0"), `"draft":false`, `"draft":"false"`, 1), fail: true},
		{name: "null tag", body: strings.Replace(metadata("0.2.0"), `"v0.2.0"`, "null", 1), fail: true},
		{name: "unpublished", body: strings.Replace(metadata("0.2.0"), `"2026-11-13T12:00:00Z"`, "null", 1), fail: true},
		{name: "bad date", body: strings.Replace(metadata("0.2.0"), "2026-11-13T12:00:00Z", "yesterday", 1), fail: true},
		{name: "malformed", body: `{`, fail: true},
		{name: "trailing JSON", body: metadata("0.2.0") + `{}`, fail: true},
		{name: "oversized", body: strings.Repeat(" ", maxMetadata+1), fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Accept") != "application/vnd.github+json" ||
					r.Header.Get("User-Agent") == "" || r.Header.Get("Authorization") != "" {
					t.Error("incorrect public request headers")
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c := New("0.1.0", false)
			c.checkURL = server.URL
			got, err := c.Check(context.Background())
			if (err != nil) != tc.fail || got.Version != tc.want {
				t.Fatalf("Check = %#v, %v; want %q, failure=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

func TestCheckCancellationAndDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	for _, canceled := range []bool{false, true} {
		c := New("0.1.0", false)
		c.checkURL = server.URL
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		if canceled {
			cancel()
		}
		if _, err := c.Check(ctx); err == nil {
			t.Error("expected cancellation/deadline error")
		}
		cancel()
	}
	c := New("dev", false)
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("invalid running version must not be compared")
	}
}

func TestRedirectTrustAndBound(t *testing.T) {
	client := newHTTPClient(time.Second, false)
	for _, location := range []string{
		"https://github.com/path", "https://release-assets.githubusercontent.com/path",
		"https://objects.githubusercontent.com/path", "https://github-releases.githubusercontent.com/path",
	} {
		u, _ := url.Parse(location)
		if err := client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 5)); err != nil {
			t.Errorf("trusted %s: %v", location, err)
		}
		if err := client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 6)); err == nil {
			t.Error("sixth redirect accepted")
		}
	}
	for _, location := range []string{
		"http://github.com/path", "https://example.com/path", "https://github.com.evil.test/path",
		"https://github.com:444/path", "https://user:pass@github.com/path", "https://api.github.com/path",
	} {
		u, _ := url.Parse(location)
		if err := client.CheckRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Errorf("untrusted redirect accepted: %s", location)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/release", http.StatusFound)
	}))
	defer server.Close()
	c := New("0.1.0", false)
	c.checkURL = server.URL
	if _, err := c.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "outside GitHub") {
		t.Fatalf("redirect wasn't rejected before leaving fixture: %v", err)
	}
}
