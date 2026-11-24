package ui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
)

// Match a colored token without depending on how much padding follows it.
func coloredPrefix(text, fg, bg string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line(text, ansi.StringWidth(text), fg, bg, false), "\x1b[0m"), ansi.ResetStyle)
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
			!strings.HasSuffix(text, "feature/demo | ↑2 ↓0  ") || strings.Contains(text, "⎇") ||
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
			!strings.HasSuffix(ansi.Strip(first), "5 mins ago    ") ||
			!strings.Contains(first, coloredPrefix("[HEAD]", m.semanticColor(c, "#facc15", "#a16207"), c.Selection)) {
			t.Fatal("commit subject, yellow HEAD badge or right-aligned age missing")
		}
		sha := m.rows[0].SHA[:8]
		if !strings.Contains(ansi.Strip(second), sha+" • alice") ||
			!strings.HasSuffix(ansi.Strip(second), "[v0.1.2] [nightly]    ") ||
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
	deliverRefresh(t, m, m.refresh(gitrepo.Current))
	header := ansi.Strip(m.header(m.colors()))
	if !strings.Contains(header, "feature/topic | ↑2 ↓1") || strings.Contains(header, "main") {
		t.Fatalf("header paired old status with new snapshot counts: %q", header)
	}
	// A detached or unborn snapshot must not fall back to the old main status.
	repo.snapshot = &gitrepo.Snapshot{Head: repo.status.Head}
	deliverRefresh(t, m, m.refresh(gitrepo.Current))
	if header := ansi.Strip(m.header(m.colors())); !strings.Contains(header, "detached HEAD | ↑— ↓—") {
		t.Fatalf("header did not follow detached snapshot: %q", header)
	}
	repo.snapshot = &gitrepo.Snapshot{Branch: "unborn"}
	deliverRefresh(t, m, m.refresh(gitrepo.Current))
	if header := ansi.Strip(m.header(m.colors())); !strings.Contains(header, "unborn | ↑— ↓—") {
		t.Fatalf("header did not follow unborn snapshot: %q", header)
	}
}

func TestNestedLabelsKeepEveryTerminalCellOnItsRowSurface(t *testing.T) {
	m, _ := setup(t, 3)
	for i := range m.rows {
		m.rows[i].Labels = []string{"HEAD", "tag: v1"}
		m.rows[i].Subject = "following text"
	}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		for _, width := range []int{120, 75} {
			m.width = width
			screen := uv.NewStyledString(m.View().Content).Lines(ansi.GraphemeWidth)
			rightPane := "history"
			paneWidth := width - 2
			if width >= 100 {
				rightPane = "details"
				paneWidth /= 2
			}
			for x, bg := range map[int]string{
				0: c.Background, 1: m.paneBackground("history", c),
				width - 2: m.paneBackground(rightPane, c), width - 1: c.Background,
			} {
				if actual := screen[3][x].Style.Bg; actual == nil ||
					color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(bg)) {
					t.Fatalf("%s width %d: shell margin at cell %d is not exactly one cell", mode, width, x)
				}
			}
			// Check the composed view, not just a styled fragment: both rows of
			// selected and unselected entries, including all following whitespace.
			for i := 0; i < 3; i++ {
				bg := m.paneBackground("history", c)
				if i == 0 {
					bg = c.Selection
				}
				want := color.RGBAModel.Convert(lipgloss.Color(bg))
				for _, y := range []int{6 + 2*i, 7 + 2*i} {
					for x := 1; x < 1+paneWidth; x++ {
						cell := screen[y][x]
						if cell.Style.Bg == nil || color.RGBAModel.Convert(cell.Style.Bg) != want {
							t.Fatalf("%s commit %d cell %d,%d (%q): lost row background", mode, i, x, y, cell.Content)
						}
					}
				}
			}
			// Each button's leading/trailing spaces have the same surface as its
			// label, even after the outer pane and base view are composed.
			for x := 3; x < 26; x++ {
				bg := c.Background
				if x < 12 {
					bg = c.Selection
				}
				if actual := screen[4][x].Style.Bg; actual == nil || color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(bg)) {
					t.Fatalf("%s mode button padding cell %d lost its background", mode, x)
				}
			}
		}
	}
}

func TestDetailsContentOrderStatisticsAndSafeWrapping(t *testing.T) {
	m, _ := setup(t, 3)
	m.now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	m.details = &gitrepo.Details{
		SHA: strings.Repeat("a", 40), Message: "Unique subject\n\nBody paragraph\nnext line\n",
		Author: "Git Author", AuthorEmail: "author@example.invalid", AuthoredAt: "2026-11-14T10:00:00Z",
		Committer: "Committer", CommittedAt: "2026-11-14T11:00:00Z",
		Comparison: "Compared with first parent parent-sha",
		Files: []gitrepo.ChangedFile{
			{Status: "M", Path: "file_path", Added: 118, Deleted: 6},
			{Status: "A", Path: "added", Added: 103},
			{Status: "D", Path: "deleted", Deleted: 38},
			{Status: "R100", OldPath: "old\tname", Path: "new\nname\x1b[2J"},
			{Status: "M", Path: "binary.dat", Binary: true},
		},
	}
	lines := m.detailLines()
	text := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.HasPrefix(text, "aaaaaaaa Unique subject\nGit Author") ||
		!strings.HasSuffix(ansi.Strip(lines[1]), "2h ago") ||
		strings.Count(text, "Unique subject") != 1 {
		t.Fatal("details subject/author/age hierarchy is incorrect")
	}
	body, files, metadata := strings.Index(text, "Body paragraph"), strings.Index(text, "file_path"), strings.Index(text, "Author:")
	if body < 0 || files < body || metadata < files ||
		!strings.Contains(text, `old  name → new↵name\x1b[2J`) ||
		strings.Contains(text, "+0") || strings.Contains(text, "-0") {
		t.Fatal("body, safe rename, file counts or metadata order regressed")
	}
	for name, counts := range map[string]string{"file_path": "+118 -6", "added": "+103", "deleted": "-38", "binary.dat": "binary"} {
		found := false
		for _, row := range lines {
			if strings.Contains(ansi.Strip(row), name) && strings.HasSuffix(ansi.Strip(row), counts) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s lacks right-aligned truthful counts %q", name, counts)
		}
	}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		rows := uv.NewStyledString(strings.Join(m.detailLines(), "\n")).Lines(ansi.GraphemeWidth)
		for _, row := range rows {
			content := row.String()
			statusColors := map[string]string{
				"M  ": m.semanticColor(c, "#60a5fa", "#2563eb"),
				"A  ": m.semanticColor(c, "#4ade80", "#15803d"),
				"D  ": errorColor(c), "R100  ": c.Accent,
			}
			for prefix, statusColor := range statusColors {
				if !strings.HasPrefix(content, prefix) {
					continue
				}
				if actual := row[0].Style.Fg; actual == nil || color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(statusColor)) {
					t.Fatalf("%s status lost semantic color", prefix)
				}
				for _, cell := range row {
					want := ""
					if cell.Content == "+" {
						want = m.semanticColor(c, "#4ade80", "#15803d")
					} else if cell.Content == "-" {
						want = errorColor(c)
					}
					if want != "" && (cell.Style.Fg == nil || color.RGBAModel.Convert(cell.Style.Fg) != color.RGBAModel.Convert(lipgloss.Color(want))) {
						t.Fatal("added/deleted count lost semantic color")
					}
				}
			}
		}
	}
	m.width = 30
	m.details.Files = []gitrepo.ChangedFile{{Status: "A", Path: strings.Repeat("日本語/", 10) + "END", Added: 1}}
	for _, row := range m.detailLines() {
		if ansi.StringWidth(row) > m.paneWidth()-2*horizontalPadding || strings.Contains(row, "\x1b[2J") {
			t.Fatal("details escaped their terminal-cell or sanitization boundary")
		}
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.detailLines(), "\n")), "END") {
		t.Fatal("shortened filename lost its end")
	}
}

func TestChangedFileNamesUsePathAwareCellBudgets(t *testing.T) {
	for _, tc := range []struct {
		path, oldPath string
		width         int
		want          string
	}{
		{"src/file.go", "", 11, "src/file.go"},
		{"src/deep/nested/file.go", "", 16, "src/deep…file.go"},
		{"src/deep/file.go", "", 8, "…file.go"},
		{"abcdefghijklmnop.go", "", 12, "abc…lmnop.go"},
		{"src/👩🏽‍💻👩🏽‍💻/é.go", "", 10, "src/…é.go"},
		{"👩🏽‍💻👩🏽‍💻", "", 2, "…"},
		{"1️⃣/abcdef.go", "", 10, "…abcdef.go"},
		{"a1️⃣/abcdef.go", "", 12, "a…abcdef.go"},
		{"abc1️⃣def.go", "", 8, "ab…ef.go"},
		{"abcdef1️⃣.go", "", 7, "abc….go"},
		{"abcdef1️⃣.go", "", 8, "ab…1️⃣.go"},
		{"new/deep/other.go", "old/deep/file.go", 25, "old…file.go → ne…other.go"},
		{"new/deep/other.go", "x", 22, "x → new/deep/other.go"},
		{"new.go", "old.go", 5, "n….go"},
		{"new\nname\x1b[2J", "old\tname", 40, `old  name → new↵name\x1b[2J`},
		{"file.go", "", 1, "…"},
		{"file.go", "", 0, ""},
	} {
		got := changedFileName(tc.path, tc.oldPath, tc.width)
		if got != tc.want || ansi.StringWidth(got) > tc.width {
			t.Errorf("paths %q → %q at %d cells: got %q, want %q", tc.oldPath, tc.path, tc.width, got, tc.want)
		}
	}
}

func TestDetailsFilesStayOnSingleRowsWithReservedStatistics(t *testing.T) {
	m, _ := setup(t, 3)
	m.details = &gitrepo.Details{
		SHA: "a", Message: "s", Author: "A",
		Files: []gitrepo.ChangedFile{
			{Status: "M", Path: "internal/" + strings.Repeat("👩🏽‍💻/日本語/é/", 8) + "filename.go", Added: 118, Deleted: 6},
			{Status: "R100", OldPath: "old/" + strings.Repeat("nested/", 8) + "old.go", Path: "new/" + strings.Repeat("nested/", 8) + "new.go"},
			{Status: "C100", OldPath: "source.bin", Path: strings.Repeat("copy/", 8) + "target.bin", Binary: true},
		},
	}
	for _, width := range []int{1, 2, 5, 10, 20, 40, 84} {
		m.width = width + 2*(shellHorizontalPadding+horizontalPadding)
		lines := m.detailLines()
		// The three rows immediately before metadata must always be the files,
		// regardless of how much subject/author wrapping tiny panes require.
		fileStart := 0
		for i, row := range lines {
			if ansi.Strip(row) == "" {
				fileStart = i + 1
				break
			}
		}
		if lines[fileStart+len(m.details.Files)] != "" {
			t.Fatalf("width %d: a file wrapped onto another row", width)
		}
		for i, row := range lines[fileStart : fileStart+len(m.details.Files)] {
			text := ansi.Strip(row)
			if ansi.StringWidth(row) != width || strings.ContainsAny(text, "\n\x1b") {
				t.Fatalf("width %d file %d escaped its single-row cell budget: %q", width, i, text)
			}
			if width >= 40 {
				for _, token := range [][]string{{"M  internal/", "filename.go", "+118 -6"}, {"R100  ", "old.go → ", "new.go"}, {"C100  source.bin → ", "target.bin", "binary"}}[i] {
					if !strings.Contains(text, token) {
						t.Fatalf("width %d: file %d lost %q: %q", width, i, token, text)
					}
				}
			}
			if width == 40 {
				t.Logf("rendered file: %s", text)
			}
		}
	}
}

func TestHistoryHeaderKeepsModesAndReadyStatusInSeparateCellBudgets(t *testing.T) {
	m, _ := setup(t, 3)
	m.historyRefreshed = m.now.Add(-2 * time.Minute)
	for _, width := range []int{120, 75, 40, 20, 10} {
		m.width = width
		paneWidth := m.contentWidth()
		if width >= 100 {
			paneWidth /= 2
		}
		inner := max(0, paneWidth-2*horizontalPadding)
		header := m.historyHeader(inner, m.colors(), m.paneBackground("history", m.colors()))
		if ansi.StringWidth(header) != inner {
			t.Fatalf("width %d: scope/status overlap their row budget", width)
		}
		if !m.footerStatus() && !strings.HasSuffix(ansi.Strip(header), "Ready · 2 mins ago") {
			t.Fatal("ready timestamp is not opposite-aligned")
		}
		if strings.Contains(ansi.Strip(header), "Current branch") || strings.Contains(ansi.Strip(header), "All branches") {
			t.Fatal("history scope leaked into the list header")
		}
	}
	m.width = 120
	m.changeScope(gitrepo.All)
	c := m.colors()
	screen := uv.NewStyledString(m.View().Content).Lines(ansi.GraphemeWidth)
	for x := 3; x < 26; x++ {
		want := c.Background
		if x < 12 {
			want = c.Selection
		}
		if actual := screen[4][x].Style.Bg; actual == nil || color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(want)) {
			t.Fatal("scope loading must not change active mode highlighting")
		}
	}
	m.width, m.focus, m.operation = 75, "details", "Fetching"
	if !strings.Contains(plain(m), "Fetching") || !m.footerStatus() {
		t.Fatal("details-only view hid the active Git operation")
	}
	m.quit()
	if !strings.Contains(plain(m), "waiting to quit") {
		t.Fatal("details-only view hid orderly shutdown")
	}
}

func TestActiveStatusSurvivesNarrowHistoryHeader(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		operation string
		loading   bool
		closing   bool
		want      string
		footer    bool
	}{
		{"tiny fetch", 20, "Fetching", false, false, "Fetching", true},
		{"tiny loading", 20, "", true, false, "Loading", true},
		{"tiny closing", 20, "Fetching", false, true, "Quit pending", true},
		{"fetch below boundary", 51, "Fetching", false, false, "Fetching", true},
		{"fetch at boundary", 52, "Fetching", false, false, "Fetching", false},
		{"closing below boundary", 69, "Fetching", false, true, "waiting to quit", true},
		{"closing at boundary", 70, "Fetching", false, true, "waiting to quit", false},
		{"normal single pane", 75, "Fetching", false, false, "Fetching", false},
		{"compact split panes", 100, "Fetching", false, true, "waiting to quit", true},
		{"normal split panes", 138, "Fetching", false, true, "waiting to quit", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := setup(t, 3)
			m.width, m.focus = tc.width, "history"
			m.operation, m.loading, m.closing = tc.operation, tc.loading, tc.closing
			text := plain(m)
			if !strings.Contains(text, tc.want) {
				t.Fatalf("active status %q disappeared at width %d:\n%s", tc.want, tc.width, text)
			}
			if m.footerStatus() != tc.footer {
				t.Fatal("status did not move to the footer only when the header needed it")
			}
			rows := strings.Split(text, "\n")
			if len(rows) > m.height {
				t.Fatal("status fallback overflowed the viewport height")
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > m.width {
					t.Fatal("status fallback overflowed the viewport width")
				}
			}
			if !tc.footer && (!strings.Contains(rows[4], tc.want) || strings.Count(text, tc.want) != 1) {
				t.Fatal("normal-width status must appear only in the history header")
			}
		})
	}
}

func TestWorkingCopyPatchHeadersAndHunkContentKeepDistinctColors(t *testing.T) {
	m, _ := setup(t, 3)
	m.working.details = &gitrepo.WorkingCopyDetails{
		Entry: gitrepo.WorkingCopyEntry{Path: "a"}, Outcome: gitrepo.WorkingCopyReady,
		Sections: []gitrepo.WorkingCopySection{{Label: "Unstaged", Kind: gitrepo.WorkingCopyPatch,
			Text: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n---removed\n+++added\n\\ No newline at end of file\n"}},
	}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		rows := uv.NewStyledString(m.renderFileDetails(c, c.Panel)).Lines(ansi.GraphemeWidth)
		for _, row := range rows {
			text := row.String()
			want := ""
			switch text {
			case "--- a/a", "+++ b/a", "\\ No newline at end of file":
				want = c.Muted
			case "---removed":
				want = errorColor(c)
			case "+++added":
				want = m.semanticColor(c, "#4ade80", "#15803d")
			case "@@ -1 +1 @@":
				want = c.Accent
			}
			if want != "" && (row[0].Style.Fg == nil ||
				color.RGBAModel.Convert(row[0].Style.Fg) != color.RGBAModel.Convert(lipgloss.Color(want))) {
				t.Fatalf("%s patch line %q lost semantic classification", mode, text)
			}
		}
	}
}

func TestSharedFileRowsKeepStatusPositionsCountsAndColors(t *testing.T) {
	m, _ := setup(t, 3)
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		for _, tc := range []struct {
			index, worktree, prefix, counts string
			untracked                       bool
			stats                           gitrepo.LineStats
		}{
			{"", "M", " M path", "+121 -73", false, gitrepo.LineStats{Known: true, Added: 121, Deleted: 73}},
			{"", "", " ? path", "+44", true, gitrepo.LineStats{Known: true, Added: 44}},
			{"", "D", " D path", "-17", false, gitrepo.LineStats{Known: true, Deleted: 17}},
			{"D", "", "D  path", "-101", false, gitrepo.LineStats{Known: true, Deleted: 101}},
			{"A", "", "A  path", "binary", false, gitrepo.LineStats{Known: true, Binary: true}},
			{"M", "", "M  path", "+3", false, gitrepo.LineStats{Known: true, Added: 3}},
			{"M", "M", "MM path", "+2 -2", false, gitrepo.LineStats{Known: true, Added: 2, Deleted: 2}},
			{"D", "", "D? path", "+1 -1", true, gitrepo.LineStats{Known: true, Added: 1, Deleted: 1}},
			{"U", "U", "UU path", "unavailable", false, gitrepo.LineStats{}},
		} {
			row := m.renderFileRow(fileRow{path: "path", index: tc.index, worktree: tc.worktree,
				untracked: tc.untracked, stats: tc.stats}, 50, c, c.Panel)
			text := ansi.Strip(row)
			if !strings.HasPrefix(text, tc.prefix) || !strings.HasSuffix(text, tc.counts) ||
				ansi.StringWidth(row) != 50 || strings.Contains(text, "\n") {
				t.Fatalf("file row lost positions, counts or single-row width: %q", text)
			}
			cells := uv.NewStyledString(row).Lines(ansi.GraphemeWidth)[0]
			pathColor := c.Text
			if tc.untracked && tc.index == "" {
				pathColor = c.Muted
				for _, cell := range cells {
					if cell.Content != " " && (cell.Style.Fg == nil ||
						color.RGBAModel.Convert(cell.Style.Fg) != color.RGBAModel.Convert(lipgloss.Color(c.Muted))) {
						t.Fatal("purely untracked row is not wholly muted")
					}
				}
			}
			if actual := cells[3].Style.Fg; actual == nil ||
				color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(pathColor)) {
				t.Fatal("tracked path inherited a semantic or untracked status color")
			}
		}
	}
	// The two feature owners use the same row contract, not matching copies.
	c := m.colors()
	m.details = &gitrepo.Details{SHA: strings.Repeat("a", 40),
		Files: []gitrepo.ChangedFile{{Status: "M", Path: "path", Added: 3}}}
	want := ansi.Strip(m.renderFileRow(fileRow{path: "path", index: "M",
		stats: gitrepo.LineStats{Known: true, Added: 3}}, m.paneWidth()-2*horizontalPadding, c, c.Panel))
	if !strings.Contains(ansi.Strip(strings.Join(m.detailLines(), "\n")), want) {
		t.Fatal("commit details bypassed the shared file row")
	}
	m.mode = workingCopyMode
	m.working.loaded = true
	m.working.entries = []gitrepo.WorkingCopyEntry{
		{Path: "first", IndexStatus: "M", Stats: gitrepo.LineStats{Known: true, Added: 3}},
		{Path: "second", WorktreeStatus: "D", Stats: gitrepo.LineStats{Known: true, Deleted: 17}},
	}
	m.working.path = "first"
	pane := ansi.Strip(m.pane("history", 70, 10, c))
	if !strings.Contains(pane, "M  first") || !strings.Contains(pane, " D second") ||
		!strings.Contains(pane, "+3") || !strings.Contains(pane, "-17") {
		t.Fatal("unselected working-copy rows lost their counts or status positions")
	}
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		for _, width := range []int{70, 24, 12, 9, 8, 5, 4, 3, 2, 1, 0} {
			raw := m.pane("history", width, 10, c)
			for _, row := range strings.Split(raw, "\n") {
				if ansi.StringWidth(row) != width {
					t.Fatalf("working-copy width %d: overflow %q", width, ansi.Strip(row))
				}
			}
			screen := uv.NewStyledString(raw).Lines(ansi.GraphemeWidth)
			for i := range m.working.entries {
				bg := m.paneBackground("history", c)
				if i == 0 {
					bg = c.Selection
				}
				for x, cell := range screen[3+i] {
					if cell.Style.Bg == nil ||
						color.RGBAModel.Convert(cell.Style.Bg) != color.RGBAModel.Convert(lipgloss.Color(bg)) {
						t.Fatalf("%s file %d cell %d: edge-to-edge surface lost", mode, i, x)
					}
				}
			}
			if width == 70 {
				rows := strings.Split(ansi.Strip(raw), "\n")
				if !strings.HasPrefix(rows[1], "   History ") ||
					!strings.HasPrefix(rows[3], "▎   M  first") || !strings.HasSuffix(rows[3], "+3    ") ||
					!strings.HasPrefix(rows[4], "     D second") || !strings.HasSuffix(rows[4], "-17    ") ||
					strings.ContainsAny(rows[3]+rows[4], "↑↓") {
					t.Fatal("working-copy gutters changed header, status positions or counts")
				}
			}
		}
	}
}

func TestDisplayTabsExpandBeforeSanitizingWithoutChangingLiteralEscapes(t *testing.T) {
	raw := "a\tb " + `literal\t` + "\x1b[2J\r"
	if got := safeLine(raw); got != `a  b literal\t\x1b[2J\r` {
		t.Fatalf("tab expansion or sanitization is wrong: %q", got)
	}
	m, _ := setup(t, 1)
	m.working.details = &gitrepo.WorkingCopyDetails{Outcome: gitrepo.WorkingCopyReady,
		Entry:    gitrepo.WorkingCopyEntry{Path: "path\tname"},
		Sections: []gitrepo.WorkingCopySection{{Label: "Untracked", Kind: gitrepo.WorkingCopyPreview, Text: raw}}}
	text := ansi.Strip(m.renderFileDetails(m.colors(), m.colors().Panel))
	if !strings.Contains(text, `a  b literal\t\x1b[2J\r`) ||
		!strings.Contains(text, "path  name") || strings.Contains(text, "\t") {
		t.Fatal("file details did not use the safe two-space display boundary")
	}
}
