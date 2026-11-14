package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

type fakeRepository struct {
	rows                       []gitrepo.Commit
	status                     gitrepo.Status
	fetched, pulled, readLimit int
	snapshotScope              gitrepo.Scope
	snapshot                   *gitrepo.Snapshot
}

func (r *fakeRepository) Root() string                                   { return "/example/repository" }
func (r *fakeRepository) Status(context.Context) (gitrepo.Status, error) { return r.status, nil }
func (r *fakeRepository) Snapshot(_ context.Context, scope gitrepo.Scope) (gitrepo.Snapshot, error) {
	r.snapshotScope = scope
	if r.snapshot != nil {
		return *r.snapshot, nil
	}
	return gitrepo.Snapshot{Branch: r.status.Branch, Head: r.status.Head, Roots: []string{"frozen"}}, nil
}
func (r *fakeRepository) History(_ context.Context, _ gitrepo.Snapshot, offset, limit int) ([]gitrepo.Commit, error) {
	r.readLimit = limit
	return append([]gitrepo.Commit(nil), r.rows[min(offset, len(r.rows)):min(offset+limit, len(r.rows))]...), nil
}
func (r *fakeRepository) Details(_ context.Context, sha string) (gitrepo.Details, error) {
	return gitrepo.Details{SHA: sha, Message: "Details " + sha}, nil
}
func (r *fakeRepository) Fetch(context.Context) error                  { r.fetched++; return nil }
func (r *fakeRepository) Pull(context.Context, gitrepo.PullMode) error { r.pulled++; return nil }

func setup(t *testing.T, count int) (*Model, *fakeRepository) {
	t.Helper()
	repo := &fakeRepository{status: gitrepo.Status{Branch: "main", Head: "0000", Upstream: "origin/main", Remotes: []string{"origin"}}}
	for i := 0; i < count; i++ {
		repo.rows = append(repo.rows, gitrepo.Commit{SHA: fmt.Sprintf("%040d", i), Subject: fmt.Sprintf("Commit %d", i), Author: "Author", AuthoredAt: "2026-11-13T12:00:00Z"})
	}
	settings := preferences.Load(t.TempDir())
	settings.Value.AutoFetch = false
	m := New(context.Background(), repo, settings, palette.Load(settings.Directory), nil, false)
	m.width = 120
	m.height = 32
	m.Update(m.refresh(m.scope)())
	if m.selected != "" {
		m.Update(detailsMsg{request: m.detailRequest, details: gitrepo.Details{SHA: m.selected, Message: "First details"}})
	}
	return m, repo
}
func press(m *Model, code rune, text string) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return cmd
}
func plain(m *Model) string { return ansi.Strip(m.View().Content) }

func TestDefaultHistoryCapAndRetainedSelection(t *testing.T) {
	m, repo := setup(t, 450)
	if m.scope != gitrepo.Current || repo.snapshotScope != gitrepo.Current {
		t.Fatal("initial history must query the current branch")
	}
	if len(m.rows) != 100 || m.hasMore || repo.readLimit != 100 {
		t.Fatal("default cap exceeded")
	}
	selected := m.rows[80].SHA
	m.selectCommit(selected)
	m.Update(detailsMsg{request: m.detailRequest, details: gitrepo.Details{SHA: selected, Message: "Retained details"}})
	next := m.settings.Value
	next.MaxCommits = 25
	m.save(next)
	if len(m.rows) != 25 || m.selected != selected || m.details.SHA != selected {
		t.Fatal("cap must trim without replacing selection")
	}
	if !strings.Contains(plain(m), "outside loaded history") {
		t.Fatal("missing retained selection explanation")
	}
}

func TestNavigationIsLocalAndSlowDetailsStayInPane(t *testing.T) {
	m, repo := setup(t, 3)
	cmd := press(m, tea.KeyDown, "")
	if m.activity() != "" || repo.fetched != 0 || m.operation != "" {
		t.Fatal("navigation became a fetch")
	}
	if strings.Contains(plain(m), "Loading commit") {
		t.Fatal("fast read flashes indicator")
	}
	old := m.detailRequest
	press(m, tea.KeyDown, "")
	m.Update(slowDetailsMsg(old))
	if m.slowDetails {
		t.Fatal("old delay activated")
	}
	m.Update(detailsMsg{request: old, details: gitrepo.Details{SHA: "obsolete"}})
	if m.details != nil {
		t.Fatal("stale detail applied")
	}
	m.Update(slowDetailsMsg(m.detailRequest))
	lines := strings.Split(plain(m), "\n")
	found := false
	for y, line := range lines {
		if x := strings.Index(line, "Loading commit"); x >= 0 {
			found = true
			if x < 60 || y > m.height-4 {
				t.Fatal("detail activity escaped its pane")
			}
		}
	}
	if !found {
		t.Fatal("missing delayed indicator")
	}
	m.Update(detailsMsg{request: m.detailRequest, details: gitrepo.Details{SHA: m.selected}})
	if m.slowDetails || strings.Contains(plain(m), "Loading commit") {
		t.Fatal("settled spinner retained")
	}
	if cmd == nil {
		t.Fatal("navigation must schedule local read")
	}
}

func TestRefreshRejectsStaleGenerationAndPreservesDetailsScroll(t *testing.T) {
	m, repo := setup(t, 3)
	m.detailsTop = 9
	old := m.refresh(gitrepo.All)()
	repo.rows = repo.rows[1:]
	current := m.refresh(gitrepo.Current)()
	m.Update(current)
	m.Update(old)
	if m.scope != gitrepo.Current || len(m.rows) != 2 || m.detailsTop != 9 {
		t.Fatal("stale refresh/scroll regression")
	}
	if m.selected == m.rows[0].SHA {
		t.Fatal("out-of-view selection replaced")
	}
}

func TestPagedNavigationCannotOverrideNewerSelection(t *testing.T) {
	m, repo := setup(t, 450)
	m.settings.Value.MaxCommits = 500
	m.Update(m.refresh(gitrepo.All)())
	m.selectCommit(m.rows[len(m.rows)-1].SHA)
	m.move(1)
	gen, nav := m.generation, m.navigation
	m.move(-1)
	selected := m.selected
	m.Update(pageMsg{generation: gen, navigation: nav, target: 200, count: 200, rows: repo.rows[200:401]})
	if m.selected != selected || len(m.rows) != 400 || !m.hasMore {
		t.Fatal("stale navigation won or paging dropped")
	}
	m.move(300)
	old := pageMsg{generation: m.generation, navigation: m.navigation, target: 450, count: 100, rows: repo.rows[400:]}
	m.refresh(m.scope)
	m.Update(old)
	if m.paging || len(m.rows) != 400 {
		t.Fatal("old paging crossed refresh")
	}
}

func TestMutationSerializationTimerAndQuit(t *testing.T) {
	m, repo := setup(t, 3)
	m.started = true
	m.settings.Value.AutoFetch = true
	m.schedule()
	token := m.timer
	cmd := m.mutate("fetch")
	if cmd == nil || m.mutate("merge") != nil {
		t.Fatal("mutations overlap")
	}
	m.Update(autoMsg(token))
	if m.operation != "Fetching" || repo.fetched != 0 {
		t.Fatal("tick ran outside command boundary")
	}
	_, quit := m.Update(QuitMsg{})
	if quit != nil || !m.closing {
		t.Fatal("quit interrupted mutation")
	}
	_, quit = m.Update(mutationMsg{fetch: true, err: errors.New("remote failed")})
	if quit == nil || !m.fetchFailed || m.failure != "remote failed" || !m.lastFetch.IsZero() {
		t.Fatal("failure/quit settlement lost")
	}
}

func TestDisabledTimerAndRetiredReadDoNotRestart(t *testing.T) {
	m, _ := setup(t, 3)
	m.started = true
	m.settings.Value.AutoFetch = true
	m.schedule()
	token := m.timer
	m.settings.Value.AutoFetch = false
	m.schedule()
	m.Update(autoMsg(token))
	if m.operation != "" {
		t.Fatal("disabled timer fetched")
	}
	m.Update(QuitMsg{})
	m.Update(readyMsg(m.generation))
	m.Update(historyMsg{generation: m.generation})
	if m.operation != "" {
		t.Fatal("late startup restarted")
	}
}

func TestInitialFetchWaitsForCurrentLocalHistory(t *testing.T) {
	m, _ := setup(t, 3)
	m.settings.Value.AutoFetch = true
	old := readyMsg(m.generation)
	refresh := m.refresh(gitrepo.Current)
	m.Update(old)
	m.Update(readyMsg(m.generation))
	if m.started || m.operation != "" {
		t.Fatal("initial fetch started before the pending local history settled")
	}
	m.Update(refresh())
	m.Update(old)
	if m.started {
		t.Fatal("a retired ready message started auto-fetch")
	}
	m.Update(readyMsg(m.generation))
	if !m.started || m.operation != "Fetching" {
		t.Fatal("current local history never enabled initial fetch")
	}
}

func TestCommandPaletteCapturesTypingAndRestoresSelection(t *testing.T) {
	m, _ := setup(t, 3)
	selected := m.selected
	m.focus = "details"
	m.openDialog("commands", "")
	press(m, 'j', "j")
	press(m, 'q', "q")
	if m.selected != selected || m.closing || m.input.Value() != "jq" {
		t.Fatal("typing leaked to global shortcuts")
	}
	press(m, tea.KeyEscape, "")
	if m.dialog != "" || m.focus != "details" || m.selected != selected {
		t.Fatal("modal lost focus/selection")
	}
	m.openDialog("commands", "settings theme")
	options := m.options()
	if len(options) != 1 || options[0].id != "themes" {
		t.Fatalf("category search: %#v", options)
	}
	press(m, tea.KeyEnter, "")
	if m.dialog != "themes" {
		t.Fatal("theme picker did not open")
	}
	m.input.SetValue("catppuccin")
	press(m, tea.KeyEnter, "")
	if m.settings.Value.Theme != "catppuccin" || preferences.Load(m.settings.Directory).Value.Theme != "catppuccin" {
		t.Fatal("theme did not persist")
	}
}

func TestErrorDialogScrollsWrappedDiagnostics(t *testing.T) {
	m, _ := setup(t, 3)
	m.width, m.height = 60, 18
	m.failure = "START-OF-DIAGNOSTIC " + strings.Repeat("long diagnostic text ", 90) + "END-OF-DIAGNOSTIC"
	m.openDialog("errors", "")
	body := func() string {
		view := plain(m)
		start, end := strings.Index(view, "Errors and warnings"), strings.Index(view, "Esc Close")
		if start < 0 || end <= start {
			t.Fatalf("missing error dialog: %s", view)
		}
		return view[start:end]
	}
	if strings.Contains(body(), "END-OF-DIAGNOSTIC") {
		t.Fatal("fixture must extend beyond the dialog viewport")
	}
	for range 100 {
		press(m, tea.KeyDown, "")
		press(m, 'j', "j")
	}
	if !strings.Contains(body(), "END-OF-DIAGNOSTIC") {
		t.Fatal("arrow/j navigation cannot reach the wrapped diagnostic's end")
	}
	for range 10 {
		press(m, tea.KeyPgDown, "")
	}
	atEnd := body()
	press(m, tea.KeyUp, "")
	if body() == atEnd {
		t.Fatal("scrolling past the end stranded upward navigation")
	}
	for range 100 {
		press(m, 'k', "k")
	}
	if !strings.Contains(body(), "START-OF-DIAGNOSTIC") {
		t.Fatal("cannot return to the diagnostic's beginning")
	}
}

func TestLayoutPaddingGapAndCompactPalette(t *testing.T) {
	m, _ := setup(t, 3)
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		lines := strings.Split(plain(m), "\n")
		if len(lines) != 32 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[31]) != "" {
			t.Fatalf("outer padding/height: %d", len(lines))
		}
		history := -1
		for y, line := range lines {
			if !strings.HasPrefix(line, "  ") || !strings.HasSuffix(line, "  ") {
				t.Fatal("edge padding")
			}
			if strings.Contains(line, "History") {
				history = y
			}
			if strings.Contains(line, "commits loaded") || strings.Contains(line, "limit ") || strings.Contains(line, "Auto-fetch") {
				t.Fatal("settings in footer")
			}
		}
		if history < 0 || strings.TrimSpace(lines[history+1][:59]) != "" || !strings.Contains(lines[history+2], "▌") {
			t.Fatal("history gap/selection")
		}
		if !strings.HasPrefix(lines[1], "    Gitbuddy / repository") {
			t.Fatal("header inset")
		}
		paneTop := history - 1
		paneBottom := paneTop + m.paneHeight() - 1
		if strings.TrimSpace(lines[paneTop-1]) != "" || strings.TrimSpace(lines[paneBottom+1]) != "" {
			t.Fatal("missing separator above or below the panes")
		}
		boundary := horizontalPadding + m.contentWidth()/2
		for _, text := range lines[paneTop : paneBottom+1] {
			cells := []rune(text) // Fixture content uses single-cell characters.
			if string(cells[boundary-horizontalPadding:boundary+horizontalPadding]) != "    " {
				t.Fatal("adjacent pane insets must provide four empty columns")
			}
		}
		details := strings.Index(lines[history], "Commit details")
		if details < 0 || ansi.StringWidth(lines[history][:details]) != boundary+horizontalPadding {
			t.Fatal("panes must meet without an additional gutter")
		}
		if !strings.HasPrefix(lines[history], "    History") {
			t.Fatal("history pane horizontal inset")
		}
	}
	m.width, m.height = 80, 15
	m.selectCommit(m.rows[2].SHA)
	m.updates.status = "No newer stable release"
	m.ensureVisible()
	compact := plain(m)
	if !strings.Contains(compact, "Commit 2") || !strings.Contains(compact, "No newer stable release") ||
		!strings.Contains(compact, "Not fetched this session") || !strings.Contains(compact, "Ctrl+P") {
		t.Fatalf("compact update notice hid history or Git status: %s", compact)
	}
	if len(strings.Split(compact, "\n")) != 15 {
		t.Fatal("compact footer exceeded terminal height")
	}
	m.updates.status = ""
	m.width = 60
	m.height = 18
	m.openDialog("commands", "")
	for range 30 {
		press(m, tea.KeyDown, "")
	}
	view := plain(m)
	if !strings.Contains(view, "Quit") || !strings.Contains(view, "Enter Select") {
		t.Fatalf("compact palette unreachable: %s", view)
	}
	press(m, tea.KeyEnter, "")
	if !m.closing {
		t.Fatal("last compact command not selected")
	}
}

func TestIncomingAndDivergedLocalHistoryPresentation(t *testing.T) {
	m, _ := setup(t, 3)
	m.rows[0].Subject, m.rows[0].Relation = "incoming subject", gitrepo.Incoming
	m.rows[1].Subject, m.rows[1].Relation = "local subject", gitrepo.LocalOnly
	m.rows[1].Author = "Local Author"
	m.rows[2].Subject = "shared subject"
	m.snapshot.Diverged = true
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		bg := c.Panel
		if mode == "light" {
			bg = c.Active
		}
		m.selected = m.rows[0].SHA
		render := func() []string { return strings.Split(m.pane("history", 70, 14, c), "\n") }
		find := func(rows []string, text string) string {
			t.Helper()
			for _, row := range rows {
				if strings.Contains(ansi.Strip(row), text) {
					return row
				}
			}
			t.Fatalf("missing history text %q", text)
			return ""
		}
		rows := render()
		// Incoming text is muted even when selected; the existing selection
		// marker/background still make keyboard focus clear.
		incoming := find(rows, "incoming subject")
		if !strings.Contains(incoming, coloredPrefix("incoming subject", c.Muted, c.Selection)) ||
			!strings.Contains(incoming, line("▌", 1, c.Accent, c.Selection, false)) {
			t.Fatalf("%s incoming styling lost dimming or selection", mode)
		}
		baseline := horizontalPadding + 1 + horizontalPadding
		if strings.Index(ansi.Strip(find(rows, "local subject")), "local subject") != baseline+2 ||
			strings.Index(ansi.Strip(find(rows, "shared subject")), "shared subject") != baseline {
			t.Fatal("only diverged local text should gain a two-cell indent")
		}
		// Both lines of a local entry must move together, without recoloring it.
		if !strings.Contains(find(rows, "local subject"), coloredPrefix("local subject", c.Text, bg)) {
			t.Fatal("local commit text was recolored")
		}
		localMeta := ansi.Strip(find(rows, "Local Author"))
		if strings.Index(localMeta, m.rows[1].SHA[:8]) != baseline+2 {
			t.Fatal("local metadata did not follow the subject indent")
		}
		for _, row := range rows {
			if ansi.StringWidth(row) != 70 {
				t.Fatal("indented commit exceeded its pane width")
			}
		}
		m.snapshot.Diverged = false
		if strings.Index(ansi.Strip(find(render(), "local subject")), "local subject") != baseline {
			t.Fatal("ahead-only local history must not be indented")
		}
		m.snapshot.Diverged = true
	}
}

func TestFocusHighlightsOnlyTheActivePane(t *testing.T) {
	m, _ := setup(t, 3)
	for _, theme := range m.themes.Names {
		m.settings.Value.Theme = theme
		for _, mode := range []string{"dark", "light"} {
			m.settings.Value.ColorMode = mode
			c := m.colors()
			highlight, recessed := c.Panel, c.Active
			if mode == "light" {
				highlight, recessed = c.Active, c.Panel
			}
			surface := func(kind string) string {
				return strings.Split(m.pane(kind, 40, 8, c), "\n")[0]
			}
			m.focus = "history"
			wantActive := line("", 40, c.Text, highlight, false)
			wantInactive := line("", 40, c.Text, recessed, false)
			if wantActive == wantInactive {
				t.Fatal("fixture must distinguish rendered surfaces")
			}
			if surface("history") != wantActive || surface("details") != wantInactive {
				t.Fatalf("%s/%s highlights the inactive pane", theme, mode)
			}
			press(m, tea.KeyTab, "")
			if surface("history") != wantInactive || surface("details") != wantActive {
				t.Fatalf("%s/%s highlight did not follow Tab", theme, mode)
			}
		}
	}
	// A single-mode custom theme can resolve to light colors in a requested dark
	// mode (or vice versa). Focus follows the rendered surface, not that request.
	m.settings.Value.ColorMode = "dark"
	m.focus = "history"
	c := m.themes.Resolve("opencode", "light")
	if first := strings.Split(m.pane("history", 40, 8, c), "\n")[0]; first != line("", 40, c.Text, c.Active, false) {
		t.Fatal("single-mode theme fallback inverted focus")
	}
}

func TestWrappedDiagnosticsHavePaddedContinuousSeverityRule(t *testing.T) {
	m, _ := setup(t, 100)
	m.width, m.height = 60, 18
	m.selectCommit(m.rows[99].SHA)
	m.ensureVisible()
	m.fail(errors.New("Failure: " + strings.Repeat("wrapped diagnostic ", 30)))
	if !strings.Contains(plain(m), "Commit 99") {
		t.Fatal("diagnostic growth hid the selected commit")
	}
	c := m.colors()
	assertBlock := func(lines []string, width int, bg string) {
		t.Helper()
		rows := messageRows(lines, width, c, bg, errorColor(c))
		if len(lines) < 2 {
			t.Fatal("fixture must wrap")
		}
		if len(rows) != len(lines)+2 {
			t.Fatal("messages need exactly one row of vertical padding on each side")
		}
		for _, row := range rows {
			if !strings.HasPrefix(row, line("▎", 1, errorColor(c), bg, false)) {
				t.Fatal("red rule must continue through every row")
			}
			if ansi.StringWidth(row) != width {
				t.Fatal("message exceeds its column budget")
			}
		}
		if strings.TrimSpace(strings.TrimPrefix(ansi.Strip(rows[0]), "▎")) != "" ||
			strings.TrimSpace(strings.TrimPrefix(ansi.Strip(rows[len(rows)-1]), "▎")) != "" {
			t.Fatal("messages need one blank row inside each end of the rule")
		}
	}
	assertBlock(m.footerNoticeLines(), m.contentWidth(), c.Background)
	m.openDialog("errors", "")
	inner := m.dialogWidth() - 2*horizontalPadding
	assertBlock(m.errorLines(), inner, c.Panel)
	view := plain(m)
	if strings.Count(view, "▎") < 5 || !strings.Contains(view, "Esc Close") {
		t.Fatal("diagnostic dialog lost its bordered message or controls")
	}
}

func TestDetailsRemainReachableAfterInsetAndViewportChanges(t *testing.T) {
	m, _ := setup(t, 3)
	selected := m.rows[2].SHA
	m.selectCommit(selected)
	m.Update(detailsMsg{request: m.detailRequest, details: gitrepo.Details{
		SHA: selected, Message: strings.Repeat("wrapped commit message ", 30),
		Files: []gitrepo.ChangedFile{{Status: "A", Path: strings.Repeat("long-path/", 20) + "END-OF-DETAILS"}},
	}})
	m.rows = m.rows[:2] // Keep the selected details outside the loaded view.
	press(m, tea.KeyTab, "")
	for range 100 {
		press(m, tea.KeyPgDown, "")
	}
	if !strings.Contains(plain(m), "END-OF-DETAILS") {
		t.Fatal("pane insets or retained-selection notice made the last details unreachable")
	}
	m.Update(tea.WindowSizeMsg{Width: 75, Height: 18})
	for range 100 {
		press(m, tea.KeyPgDown, "")
	}
	if !strings.Contains(plain(m), "END-OF-DETAILS") || m.detailsTop > m.maxDetailsTop() {
		t.Fatal("narrow reflow lost the last wrapped details")
	}
}

func TestViewportClampsAfterShorterHistory(t *testing.T) {
	m, repo := setup(t, 100)
	m.top = 85
	m.selectCommit(m.rows[99].SHA)
	repo.rows = repo.rows[:3]
	m.Update(m.refresh(gitrepo.Current)())
	if m.top != 0 || !strings.Contains(plain(m), "Commit 0") {
		t.Fatal("shorter list rendered blank")
	}
}
