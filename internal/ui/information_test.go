package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
)

// Match a colored token without depending on how much padding follows it.
func coloredPrefix(text, fg, bg string) string {
	return strings.TrimSuffix(line(text, ansi.StringWidth(text), fg, bg, false), "\x1b[0m")
}

func TestRelativeCommitTimes(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{time.Minute, "1 min ago"},
		{5 * time.Minute, "5 mins ago"},
		{2 * time.Hour, "2h ago"},
		{24 * time.Hour, "1 day ago"},
		{3 * 24 * time.Hour, "3 days ago"},
		{8 * 24 * time.Hour, "last week"},
		{14 * 24 * time.Hour, "2 weeks ago"},
		{30 * 24 * time.Hour, "1 month ago"},
		{365 * 24 * time.Hour, "1 year ago"},
		{-2 * time.Hour, "in 2h"},
		{-8 * 24 * time.Hour, "next week"},
	} {
		if got := relativeTime(now.Add(-tc.age).Format(time.RFC3339), now); got != tc.want {
			t.Errorf("age %s: got %q, want %q", tc.age, got, tc.want)
		}
	}
	if got := relativeTime("2026-11-13T13:55:00+02:00", now); got != "5 mins ago" {
		t.Fatal("relative time ignored the recorded timezone")
	}
	if got := relativeTime("1000-11-13T12:00:00Z", now); got != "1026 years ago" {
		t.Fatalf("valid old timestamp was clamped: %s", got)
	}
	for _, invalid := range []string{"", "not a date", "2026-11-13"} {
		if got := relativeTime(invalid, now); got != "—" {
			t.Fatal("invalid author time must not fabricate an age")
		}
	}
}

func TestMinuteRedrawIsIndependentOfGitAndNavigation(t *testing.T) {
	m, repo := setup(t, 3)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	m.now = now
	m.rows[0].AuthoredAt = now.Add(-59 * time.Second).Format(time.RFC3339)
	if !strings.Contains(plain(m), "just now") || m.minuteTick() == nil {
		t.Fatal("missing initial age or minute clock")
	}
	m.focus = "details"
	m.openDialog("commands", "theme")
	selected, generation, request, reads := m.selected, m.generation, m.detailRequest, repo.readLimit
	_, next := m.Update(minuteMsg(now.Add(time.Minute)))
	if next == nil || m.now != now.Add(time.Minute) || m.selected != selected ||
		m.focus != "details" || m.dialog != "commands" || m.input.Value() != "theme" ||
		m.generation != generation || m.detailRequest != request || repo.readLimit != reads ||
		repo.fetched != 0 || repo.pulled != 0 {
		t.Fatal("clock tick refreshed Git or disturbed navigation")
	}
	m.closeDialog()
	if !strings.Contains(plain(m), "1 min ago") {
		t.Fatal("minute tick did not redraw commit ages")
	}
	m.quit()
	stopped := m.now
	if _, next := m.Update(minuteMsg(now.Add(2 * time.Minute))); next != nil || m.now != stopped ||
		m.minuteTick() != nil {
		t.Fatal("a clock delivery restarted work after quit")
	}
}

func TestSplitHeaderTracksBranchAndUpstreamCounts(t *testing.T) {
	m, _ := setup(t, 3)
	m.snapshot.Branch = "feature/demo"
	m.snapshot.Comparison = &gitrepo.UpstreamComparison{Ahead: 2}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		header := m.header(c)
		text := ansi.Strip(header)
		if !strings.HasPrefix(text, "  Gitbuddy / repository") ||
			!strings.HasSuffix(text, "⎇ feature/demo | ↑2 ↓0  ") ||
			strings.Count(text, "feature/demo") != 1 ||
			ansi.StringWidth(header) != m.contentWidth() {
			t.Fatalf("header sections/alignment: %q", text)
		}
		if !strings.Contains(header, coloredPrefix("feature/demo", c.Accent, c.Background)) ||
			!strings.Contains(header, coloredPrefix("↑2 ↓0", m.semanticColor(c, "#4ade80", "#15803d"), c.Background)) {
			t.Fatal("branch and tracking counts lost their semantic colors")
		}
	}
	m.snapshot.Comparison = nil
	m.snapshot.Branch = ""
	if header := ansi.Strip(m.header(m.colors())); !strings.Contains(header, "detached HEAD") ||
		!strings.Contains(header, "↑— ↓—") {
		t.Fatal("missing upstream must remain distinguishable from zero counts")
	}
	m.snapshot.Comparison = &gitrepo.UpstreamComparison{Behind: 3}
	m.snapshot.Branch = strings.Repeat("feature/日本語", 12) + "\x1b[2J"
	for _, width := range []int{120, 75, 40, 25} {
		m.width = width
		raw := m.header(m.colors())
		if ansi.StringWidth(raw) != m.contentWidth() || strings.Contains(raw, "\x1b[2J") {
			t.Fatal("long or unsafe branch name escaped the header budget")
		}
		if width >= 40 && !strings.HasSuffix(ansi.Strip(raw), "↑0 ↓3  ") {
			t.Fatal("branch truncation hid the comparison counts")
		}
	}
}

func TestCommitRowsAlignMetadataAndHighlightHEAD(t *testing.T) {
	m, _ := setup(t, 3)
	m.now = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	m.rows[0].Subject, m.rows[0].Author = "Commit title", "alice"
	m.rows[0].AuthoredAt = m.now.Add(-5 * time.Minute).Format(time.RFC3339)
	m.rows[0].Labels = []string{"main", "origin/main", "HEAD", "tag: v0.1.2", "tag: nightly"}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		rows := strings.Split(m.pane("history", 90, 14, c), "\n")
		var first, second string
		for _, row := range rows {
			if strings.Contains(ansi.Strip(row), "Commit title") {
				first = row
			}
			if strings.Contains(ansi.Strip(row), "alice") {
				second = row
			}
			if ansi.StringWidth(row) != 90 {
				t.Fatal("metadata columns exceeded their pane")
			}
		}
		if !strings.Contains(ansi.Strip(first), "[HEAD] Commit title") ||
			!strings.HasSuffix(ansi.Strip(first), "5 mins ago  ") ||
			!strings.Contains(first, coloredPrefix("[HEAD]", m.semanticColor(c, "#facc15", "#a16207"), c.Selection)) {
			t.Fatal("commit subject, yellow HEAD badge or right-aligned age missing")
		}
		sha := m.rows[0].SHA[:8]
		if !strings.Contains(ansi.Strip(second), sha+"  [v0.1.2] [nightly]") ||
			!strings.HasSuffix(ansi.Strip(second), "alice  ") ||
			!strings.Contains(second, coloredPrefix(sha, c.Muted, c.Selection)) ||
			strings.Contains(ansi.Strip(second), "origin/main") {
			t.Fatal("SHA/tags/author metadata layout is incorrect")
		}
	}
}

func TestRefreshHeaderKeepsSnapshotIdentity(t *testing.T) {
	m, repo := setup(t, 3)
	// Status was read on main; an external checkout happens before Snapshot.
	// The branch beside the counts must come from that same frozen history.
	repo.snapshot = &gitrepo.Snapshot{
		Branch:     "feature/topic",
		Head:       repo.status.Head,
		Roots:      []string{"frozen"},
		Comparison: &gitrepo.UpstreamComparison{Ahead: 2, Behind: 1},
	}
	m.Update(m.refresh(gitrepo.Current)())
	header := ansi.Strip(m.header(m.colors()))
	if !strings.Contains(header, "⎇ feature/topic | ↑2 ↓1") || strings.Contains(header, "main") {
		t.Fatalf("header paired old status with new snapshot counts: %q", header)
	}
	// A detached or unborn snapshot must not fall back to the old main status.
	repo.snapshot = &gitrepo.Snapshot{Head: repo.status.Head}
	m.Update(m.refresh(gitrepo.Current)())
	if header := ansi.Strip(m.header(m.colors())); !strings.Contains(header, "detached HEAD | ↑— ↓—") {
		t.Fatalf("header did not follow detached snapshot: %q", header)
	}
	repo.snapshot = &gitrepo.Snapshot{Branch: "unborn"}
	m.Update(m.refresh(gitrepo.Current)())
	if header := ansi.Strip(m.header(m.colors())); !strings.Contains(header, "⎇ unborn | ↑— ↓—") {
		t.Fatalf("header did not follow unborn snapshot: %q", header)
	}
}
