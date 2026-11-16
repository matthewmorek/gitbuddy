package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	statusErr, historyErr      error
	fileDetails                map[string]gitrepo.WorkingCopyDetails
	fileReads                  []string
	fileRead                   func(context.Context, string) (gitrepo.WorkingCopyDetails, error)
}

func (r *fakeRepository) Root() string        { return "/example/linked-checkout" }
func (r *fakeRepository) DisplayName() string { return "repository" }
func (r *fakeRepository) Status(context.Context) (gitrepo.Status, error) {
	return r.status, r.statusErr
}
func (r *fakeRepository) Snapshot(_ context.Context, scope gitrepo.Scope) (gitrepo.Snapshot, error) {
	r.snapshotScope = scope
	if r.snapshot != nil {
		return *r.snapshot, nil
	}
	return gitrepo.Snapshot{Branch: r.status.Branch, Head: r.status.Head, Roots: []string{"frozen"}}, nil
}
func (r *fakeRepository) History(_ context.Context, _ gitrepo.Snapshot, offset, limit int) ([]gitrepo.Commit, error) {
	r.readLimit = limit
	return append([]gitrepo.Commit(nil), r.rows[min(offset, len(r.rows)):min(offset+limit, len(r.rows))]...), r.historyErr
}
func (r *fakeRepository) Details(_ context.Context, sha string) (gitrepo.Details, error) {
	return gitrepo.Details{SHA: sha, Message: "Details " + sha}, nil
}
func (r *fakeRepository) WorkingCopyDetails(ctx context.Context, path string) (gitrepo.WorkingCopyDetails, error) {
	r.fileReads = append(r.fileReads, path)
	if r.fileRead != nil {
		return r.fileRead(ctx, path)
	}
	return r.fileDetails[path], nil
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
	deliverRefresh(t, m, m.refresh(m.scope))
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
	old := historyResult(t, m.refresh(gitrepo.All))
	repo.rows = repo.rows[1:]
	current := historyResult(t, m.refresh(gitrepo.Current))
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
	deliverRefresh(t, m, m.refresh(gitrepo.All))
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
	deliverRefresh(t, m, refresh)
	m.Update(old)
	if m.started {
		t.Fatal("a retired ready message started auto-fetch")
	}
	m.Update(readyMsg(m.generation))
	if !m.started || m.operation != "Fetching" {
		t.Fatal("current local history never enabled initial fetch")
	}
}

func TestInitialFetchWaitsForIndependentStatusInEitherOrder(t *testing.T) {
	for _, statusFirst := range []bool{false, true} {
		m, repo := setup(t, 3)
		m.settings.Value.AutoFetch = true
		m.status.Remotes = nil
		batch := m.refresh(m.scope)().(tea.BatchMsg)
		history, status := batch[0](), batch[1]()
		first, last := history, status
		if statusFirst {
			first, last = status, history
		}
		m.Update(first)
		m.Update(readyMsg(m.generation))
		if m.started || m.operation != "" {
			t.Fatal("initial fetch started before both local reads settled")
		}
		_, cmd := m.Update(last)
		if cmd == nil || m.localReady() == nil {
			t.Fatal("last local read did not schedule startup readiness")
		}
		m.Update(readyMsg(m.generation))
		if !m.started || m.operation != "Fetching" || len(repo.status.Remotes) == 0 {
			t.Fatal("late status lost the initial fetch")
		}
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
			if strings.Contains(line, " History  Working copy ") {
				history = y
			}
			if strings.Contains(line, "commits loaded") || strings.Contains(line, "limit ") || strings.Contains(line, "Auto-fetch") {
				t.Fatal("settings in footer")
			}
		}
		if history < 0 || strings.TrimSpace(lines[history+1][:59]) != "" || !strings.Contains(lines[history+2], "▎") {
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
		if !strings.HasPrefix(lines[history], "     History  Working copy ") {
			t.Fatal("history pane horizontal inset")
		}
	}
	m.width, m.height = 80, 15
	m.selectCommit(m.rows[2].SHA)
	m.notify("No newer stable release", false)
	m.ensureVisible()
	compact := plain(m)
	if !strings.Contains(compact, "Commit 2") || !strings.Contains(compact, "No newer stable release") ||
		!strings.Contains(compact, "Ready") || !strings.Contains(compact, "Ctrl+P") {
		t.Fatalf("compact update notice hid history or Git status: %s", compact)
	}
	if len(strings.Split(compact, "\n")) != 15 {
		t.Fatal("compact footer exceeded terminal height")
	}
	m.notice = ""
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
			!strings.Contains(incoming, line("▎", 1, c.Accent, c.Selection, false)) {
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
	found := false
	for range 100 {
		found = found || strings.Contains(plain(m), "END-OF-DETAILS")
		press(m, tea.KeyPgDown, "")
	}
	if !found {
		t.Fatal("pane insets or retained-selection notice made the last details unreachable")
	}
	m.Update(tea.WindowSizeMsg{Width: 75, Height: 18})
	m.detailsTop = 0
	found = false
	for range 100 {
		found = found || strings.Contains(plain(m), "END-OF-DETAILS")
		press(m, tea.KeyPgDown, "")
	}
	if !found || m.detailsTop > m.maxDetailsTop() {
		t.Fatal("narrow reflow lost the last wrapped details")
	}
}

func TestViewportClampsAfterShorterHistory(t *testing.T) {
	m, repo := setup(t, 100)
	m.top = 85
	m.selectCommit(m.rows[99].SHA)
	repo.rows = repo.rows[:3]
	deliverRefresh(t, m, m.refresh(gitrepo.Current))
	if m.top != 0 || !strings.Contains(plain(m), "Commit 0") {
		t.Fatal("shorter list rendered blank")
	}
}

// History is the first command in refresh batches; do not execute recurring
// spinner, startup or notification timers in these event-loop regressions.
func historyResult(t *testing.T, cmd tea.Cmd) historyMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing history command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		return historyResult(t, batch[0])
	}
	history, ok := msg.(historyMsg)
	if !ok {
		t.Fatalf("expected history, got %T", msg)
	}
	return history
}

// Settle only repository refresh reads, not recurring timers or detail commands.
func deliverRefresh(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing refresh")
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			deliverRefresh(t, m, child)
		}
	case historyMsg:
		m.Update(msg)
	case statusMsg:
		m.Update(msg)
	default:
		t.Fatalf("expected refresh read, got %T", msg)
	}
}

func TestFetchRefreshResetsSelectionAndViewportOnlyForItsCurrentRead(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		m, _ := setup(t, 100)
		m.started = true
		m.move(80)
		if automatic {
			m.settings.Value.AutoFetch = true
			m.schedule()
			m.Update(autoMsg(m.timer))
		} else {
			m.execute(action{id: "fetch"})
		}
		if m.operation != "Fetching" {
			t.Fatal("fetch did not start")
		}
		_, cmd := m.Update(mutationMsg{fetch: true})
		m.Update(historyResult(t, cmd))
		m.ensureVisible()
		if m.top != 0 || m.selected != m.rows[0].SHA {
			t.Fatal("fetch refresh bounced back to the old selection")
		}
		m.move(80)
		oldTop, selected := m.top, m.selected
		deliverRefresh(t, m, m.refresh(m.scope))
		if m.top != oldTop || m.selected != selected {
			t.Fatal("ordinary local refresh stopped anchoring")
		}
		_, cmd = m.Update(mutationMsg{fetch: true})
		result := historyResult(t, cmd)
		m.move(-1) // A newer navigation decision wins over the pending fetch read.
		selected = m.selected
		m.Update(result)
		if m.selected != selected || m.top == 0 {
			t.Fatal("late fetch history overrode navigation")
		}
		_, cmd = m.Update(mutationMsg{fetch: true})
		stale := historyResult(t, cmd)
		deliverRefresh(t, m, m.refresh(gitrepo.All))
		m.Update(stale)
		if m.scope != gitrepo.All || m.selected != selected {
			t.Fatal("retired fetch history crossed a newer scope generation")
		}
	}
}

func TestFetchRefreshFailureEmptyAndClosing(t *testing.T) {
	m, repo := setup(t, 30)
	m.move(20)
	selected := m.selected
	_, cmd := m.Update(mutationMsg{fetch: true, err: errors.New("fetch failed")})
	result := historyResult(t, cmd)
	result.err = errors.New("history failed")
	m.Update(result)
	if m.selected != selected || !m.lastFetch.IsZero() || !m.fetchFailed {
		t.Fatal("failed refresh replaced selection or advanced successful fetch time")
	}
	_, cmd = m.Update(mutationMsg{fetch: true})
	repo.rows = nil
	m.Update(historyResult(t, cmd))
	if m.selected != "" || m.details != nil || m.detailsLoading || m.top != 0 {
		t.Fatal("empty fetched history retained stale selection/details")
	}
	m.operation = "Fetching"
	m.quit()
	generation := m.generation
	_, cmd = m.Update(mutationMsg{fetch: true})
	if cmd == nil || cmd() != tea.Quit() || m.generation != generation || m.Init() != nil {
		t.Fatal("fetch completion started new work while closing")
	}
}

func TestNotificationExpiryRetainsDiagnosticsAndRejectsOlderTimers(t *testing.T) {
	m, _ := setup(t, 3)
	m.Update(detailsMsg{request: m.detailRequest, err: errors.New("repeated failure")})
	first := m.noticeID
	if m.notice == "" || m.noticePending {
		t.Fatal("error did not schedule expiry")
	}
	// The same message is a new event and gets a fresh five-second lifetime.
	_, timer := m.Update(detailsMsg{request: m.detailRequest, err: errors.New("repeated failure")})
	if timer == nil || m.noticeID == first {
		t.Fatal("repeated notification reused its old timer")
	}
	m.Update(noticeExpiredMsg(first))
	if m.notice == "" {
		t.Fatal("older timer cleared a newer notification")
	}
	m.operation = "Fetching"
	m.Update(noticeExpiredMsg(m.noticeID))
	if m.notice != "" || !strings.Contains(m.warnings(), "repeated failure") ||
		!strings.Contains(plain(m), "Fetching") || strings.Contains(plain(m), "repeated failure") {
		t.Fatal("expiry hid ongoing work, retained the banner, or lost diagnostics")
	}
	m.openDialog("errors", "")
	if !strings.Contains(plain(m), "repeated failure") {
		t.Fatal("expired diagnostics cannot be reopened")
	}
	m.quit()
	if _, cmd := m.Update(noticeExpiredMsg(first)); cmd != nil || m.noticeTimer() != nil {
		t.Fatal("retired notification timer restarted during quit")
	}
}

func TestStartupAndRepositoryWarningsExpireWithoutLosingTheirSource(t *testing.T) {
	m, repo := setup(t, 3)
	m.settings.Warning = "startup settings warning"
	m.themes.Warnings = []string{"startup theme warning"}
	fresh := New(m.ctx, repo, m.settings, m.themes, nil, false)
	if fresh.noticeTimer() == nil || !strings.Contains(fresh.notice, "startup settings warning") ||
		!strings.Contains(fresh.notice, "startup theme warning") {
		t.Fatal("startup warnings lack notification lifetime")
	}
	fresh.Update(noticeExpiredMsg(fresh.noticeID))
	if fresh.notice != "" || !strings.Contains(fresh.warnings(), "startup theme warning") {
		t.Fatal("startup warning expiry destroyed diagnostics")
	}
	repo.status.FetchWarnings = []string{"restricted mapping"}
	deliverRefresh(t, fresh, fresh.refresh(gitrepo.Current))
	if fresh.notice != "restricted mapping" {
		t.Fatal("new repository warning was not notified")
	}
	fresh.Update(noticeExpiredMsg(fresh.noticeID))
	deliverRefresh(t, fresh, fresh.refresh(gitrepo.Current))
	if fresh.notice != "" || !strings.Contains(fresh.warnings(), "restricted mapping") {
		t.Fatal("unchanged warning reappeared on refresh or was discarded")
	}
}

func TestModeMouseHitboxesAndScopePalette(t *testing.T) {
	for _, width := range []int{120, 75, 20} {
		m, _ := setup(t, 3)
		m.width = width
		for _, segment := range m.modeSegments() {
			for x := segment.start; x < segment.end; x++ {
				m.mode = historyMode
				if segment.mode == historyMode {
					m.mode = workingCopyMode
				}
				m.Update(tea.MouseClickMsg{X: x + 4, Y: 4, Button: tea.MouseLeft})
				if m.mode != segment.mode {
					t.Fatalf("width %d: segment cell %d is not clickable", width, x)
				}
				generation := m.generation
				if _, duplicate := m.Update(tea.MouseClickMsg{X: x + 4, Y: 4, Button: tea.MouseLeft}); duplicate != nil || generation != m.generation {
					t.Fatal("active mode click issued duplicate work")
				}
			}
		}
		if m.View().MouseMode != tea.MouseModeCellMotion {
			t.Fatal("v2 view does not enable mouse reporting")
		}
	}
	for _, scope := range []gitrepo.Scope{gitrepo.All, gitrepo.Current} {
		for _, pending := range []bool{false, true} {
			m, _ := setup(t, 3)
			m.scope = scope
			m.mode = workingCopyMode
			if pending {
				m.refresh(scope)
			}
			m.execute(action{id: string(scope)})
			if m.mode != historyMode || m.activeScope() != scope {
				t.Fatal("scope command must enter history even if scope was already active")
			}
			m.openDialog("commands", "")
			found := false
			for _, option := range m.options() {
				if option.id == string(scope) {
					found = option.value == "active scope"
				}
			}
			if !found {
				t.Fatal("active history scope is not visible")
			}
		}
	}
	for _, invalid := range []tea.MouseClickMsg{
		{X: 3, Y: 4, Button: tea.MouseLeft}, {X: 27, Y: 4, Button: tea.MouseLeft},
		{X: 4, Y: 3, Button: tea.MouseLeft}, {X: 4, Y: 5, Button: tea.MouseLeft},
		{X: 4, Y: 4, Button: tea.MouseRight},
	} {
		m, _ := setup(t, 3)
		if _, cmd := m.Update(invalid); cmd != nil || m.loading {
			t.Fatal("out-of-bounds or non-left click switched mode")
		}
	}
	for _, state := range []string{"modal", "hidden", "closing"} {
		m, _ := setup(t, 3)
		switch state {
		case "modal":
			m.openDialog("commands", "")
		case "hidden":
			m.width, m.focus = 75, "details"
		case "closing":
			m.quit()
		}
		if _, cmd := m.Update(tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseLeft}); cmd != nil || m.loading {
			t.Fatalf("%s click leaked to hidden controls", state)
		}
	}
}

func TestModeKeysKeepNavigationSeparateAndDialogsConsumeInput(t *testing.T) {
	m, repo := setup(t, 100)
	repo.status.Entries = []gitrepo.WorkingCopyEntry{{Path: "z", Untracked: true}, {Path: "a", IndexStatus: "M", WorktreeStatus: "M"}}
	m.move(80)
	selected, top := m.selected, m.top
	press(m, '2', "2")
	if m.mode != workingCopyMode || m.focus != "history" {
		t.Fatal("2 must select working copy and focus its list")
	}
	deliverRefresh(t, m, m.refresh(m.activeScope()))
	if m.working.path != "a" || len(m.working.entries) != 2 {
		t.Fatal("file rows must sort by raw path")
	}
	m.moveFile(1)
	press(m, tea.KeyTab, "")
	if m.focus != "details" {
		t.Fatal("Tab must still change panes")
	}
	generation := m.generation
	if cmd := press(m, '2', "2"); cmd != nil || m.generation != generation || m.focus != "details" {
		t.Fatal("active mode shortcut is not a no-op")
	}
	for _, dialog := range []string{"commands", "max", "interval", "errors"} {
		m.openDialog(dialog, "")
		press(m, '1', "1")
		press(m, '2', "2")
		if m.mode != workingCopyMode || m.focus != "details" {
			t.Fatalf("%s leaked mode shortcuts", dialog)
		}
		press(m, tea.KeyEscape, "")
	}
	press(m, '1', "1")
	if m.mode != historyMode || m.selected != selected || m.top != top || m.focus != "history" {
		t.Fatal("history navigation was not restored")
	}
	press(m, '2', "2")
	deliverRefresh(t, m, m.refresh(m.activeScope()))
	if m.working.path != "z" {
		t.Fatal("returning to working copy lost its raw-path selection")
	}
	m.width = 75
	press(m, tea.KeyEnter, "")
	if m.focus != "details" {
		t.Fatal("narrow Enter must open file details")
	}
	press(m, tea.KeyEscape, "")
	if m.focus != "history" {
		t.Fatal("narrow Escape must return to the list")
	}
}

func TestWorkingCopyRefreshSelectionAndIndependentFailures(t *testing.T) {
	m, repo := setup(t, 3)
	m.mode = workingCopyMode
	repo.status.Entries = []gitrepo.WorkingCopyEntry{{Path: "a"}, {Path: "b"}, {Path: "c"}}
	repo.historyErr = errors.New("history failed")
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.historyFailure == "" || len(m.working.entries) != 3 || m.working.path != "a" {
		t.Fatal("failed history suppressed independent status")
	}
	m.moveFile(1)
	m.working.details = &gitrepo.WorkingCopyDetails{Entry: repo.status.Entries[1], Outcome: gitrepo.WorkingCopyReady}
	m.focus = "details"
	_, cmd := m.Update(mutationMsg{fetch: true})
	deliverRefresh(t, m, cmd().(tea.BatchMsg)[0])
	if m.working.path != "b" || m.focus != "details" || !m.working.loading || !m.working.stale {
		t.Fatal("background fetch reset file selection/focus or marked retained patch fresh")
	}
	repo.historyErr = nil
	repo.statusErr = errors.New("status failed")
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.statusFailure == "" || m.working.path != "b" || !strings.Contains(plain(m), "stale") {
		t.Fatal("status failure hid retained data or failed to mark it stale")
	}
	repo.statusErr = nil
	repo.status.Entries = []gitrepo.WorkingCopyEntry{{Path: "a"}, {Path: "c"}}
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.working.path != "c" || m.working.details != nil {
		t.Fatal("disappearing selection must choose nearest row and clear old patch")
	}
	repo.status.Entries = nil
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.working.path != "" || m.working.details != nil || !strings.Contains(plain(m), "Working copy clean") {
		t.Fatal("clean working copy retained stale selection/content")
	}
}

func TestFileReadsCancelAndRejectRetiredResults(t *testing.T) {
	m, repo := setup(t, 3)
	m.mode = workingCopyMode
	m.working.entries = []gitrepo.WorkingCopyEntry{{Path: "a"}, {Path: "b"}}
	started := make(chan struct{})
	repo.fileRead = func(ctx context.Context, _ string) (gitrepo.WorkingCopyDetails, error) {
		close(started)
		<-ctx.Done()
		return gitrepo.WorkingCopyDetails{}, ctx.Err()
	}
	cmd := m.selectFile("a")
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("file read did not start")
	}
	m.moveFile(1)
	select {
	case msg := <-result:
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("superseded file read was not cancelled")
	}
	if m.working.path != "b" || !m.working.loading || m.working.failure != "" {
		t.Fatal("cancelled selection contaminated its successor")
	}
	repo.fileRead = nil
	old := m.selectFile("b")()
	m.refresh(m.scope)
	m.Update(old)
	if m.working.details != nil {
		t.Fatal("old generation populated refreshed details")
	}
	old = m.selectFile("b")()
	m.changeMode(historyMode)
	m.Update(old)
	if m.working.details != nil {
		t.Fatal("file result crossed modes")
	}
}

func TestFileViewportReachabilityRefreshOffsetsAndResize(t *testing.T) {
	m, repo := setup(t, 3)
	m.mode = workingCopyMode
	entry := gitrepo.WorkingCopyEntry{Path: "full/raw\npath", OldPath: "old", Untracked: true, IndexStatus: "D"}
	details := gitrepo.WorkingCopyDetails{Entry: entry, Outcome: gitrepo.WorkingCopyReady, Sections: []gitrepo.WorkingCopySection{
		{Label: "Unstaged", Kind: gitrepo.WorkingCopyPreview, Text: "\x1b[2Junsafe\n" + strings.Repeat("context\n", 60) + strings.Repeat("x", 160) + "END-OF-FILE"},
		{Label: "Staged", Kind: gitrepo.WorkingCopyPatch, Text: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n---removed\n+++added\n\\ No newline at end of file\n"},
	}}
	repo.fileDetails = map[string]gitrepo.WorkingCopyDetails{entry.Path: details}
	m.Update(m.selectFile(entry.Path)())
	raw := m.renderFileDetails(m.colors(), m.colors().Panel)
	if strings.Contains(raw, "\x1b[2J") || !strings.Contains(ansi.Strip(raw), "Untracked\n") ||
		!strings.Contains(ansi.Strip(raw), "Staged\n") || !strings.Contains(ansi.Strip(raw), "full/raw↵path") {
		t.Fatal("raw path, comparison labels or terminal sanitization lost")
	}
	m.focus = "details"
	press(m, tea.KeyPgDown, "")
	press(m, tea.KeyRight, "")
	x, y := m.working.viewport.XOffset(), m.working.viewport.YOffset()
	if x == 0 || y == 0 {
		t.Fatal("focused viewport did not scroll on both axes")
	}
	m.Update(m.selectFile(entry.Path)())
	if m.working.viewport.XOffset() != x || m.working.viewport.YOffset() != y || len(repo.fileReads) != 2 {
		t.Fatal("unchanged same-path refresh must reread and retain offsets")
	}
	m.changeMode(historyMode)
	m.mode = workingCopyMode
	m.Update(m.selectFile(entry.Path)())
	if m.working.viewport.XOffset() != x || m.working.viewport.YOffset() != y {
		t.Fatal("returning to unchanged file lost detail position")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 32}, {Width: 75, Height: 18}, {Width: 100, Height: 22}} {
		m.Update(size)
		m.focus = "details"
		m.working.viewport.SetXOffset(0)
		m.working.viewport.SetYOffset(0)
		for range 35 {
			press(m, tea.KeyRight, "")
		}
		found := false
		for range 70 {
			// The long-line tail must be reachable through the composed pane,
			// including when metadata and later comparisons extend vertically.
			found = found || strings.Contains(plain(m), "END-OF-FILE")
			press(m, tea.KeyDown, "")
		}
		if !found {
			t.Fatalf("long-line tail unreachable at %dx%d", size.Width, size.Height)
		}
		press(m, tea.KeyTab, "")
		x, y := m.working.viewport.XOffset(), m.working.viewport.YOffset()
		press(m, tea.KeyRight, "")
		press(m, tea.KeyDown, "")
		if m.working.viewport.XOffset() != x || m.working.viewport.YOffset() != y {
			t.Fatal("list focus scrolled hidden file details")
		}
	}
	details.Sections = []gitrepo.WorkingCopySection{{Label: "Unstaged", Kind: gitrepo.WorkingCopyPreview, Text: "changed"}}
	repo.fileDetails[entry.Path] = details
	m.Update(m.selectFile(entry.Path)())
	if m.working.viewport.XOffset() != 0 || m.working.viewport.YOffset() != 0 {
		t.Fatal("changed content retained stale offsets")
	}
}

func TestWorkingCopyHeaderNeverAttachesCountsToDifferentHead(t *testing.T) {
	m, _ := setup(t, 3)
	m.mode = workingCopyMode
	m.snapshot.Comparison = &gitrepo.UpstreamComparison{Ahead: 12, Behind: 3}
	m.status.Branch, m.status.Head = "new-branch", "new-head"
	header := ansi.Strip(m.header(m.colors()))
	if !strings.Contains(header, "new-branch") || strings.Contains(header, "↑12") || !strings.Contains(header, "↑— ↓—") {
		t.Fatal("working-copy header reused another history snapshot's identity/counts")
	}
	m.snapshot.Branch, m.snapshot.Head = m.status.Branch, m.status.Head
	if !strings.Contains(ansi.Strip(m.header(m.colors())), "↑12 ↓3") {
		t.Fatal("matching snapshot counts should be available")
	}
}

func TestSingleRowFilePagingUsesItsOwnViewportHeight(t *testing.T) {
	m, _ := setup(t, 100)
	m.mode = workingCopyMode
	m.working.loaded = true
	for i := range 50 {
		m.working.entries = append(m.working.entries, gitrepo.WorkingCopyEntry{
			Path: fmt.Sprintf("file-%02d", i), WorktreeStatus: "M", Stats: gitrepo.LineStats{Known: true, Added: i + 1},
		})
	}
	m.working.path = "file-00"
	if m.visibleFileRows() != 2*m.visibleRows() {
		t.Fatal("fixture must distinguish one-row files from two-row commits")
	}
	press(m, tea.KeyPgDown, "")
	if want := fmt.Sprintf("file-%02d", m.visibleFileRows()); m.working.path != want || m.working.top != 1 {
		t.Fatal("file Page Down still uses two-row commit height")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 75, Height: 18}, {Width: 120, Height: 32}} {
		m.Update(size)
		m.moveFile(50)
		if m.working.top != len(m.working.entries)-m.visibleFileRows() ||
			!strings.Contains(plain(m), "file-49") {
			t.Fatal("last file is not visible after navigation/resize")
		}
		m.moveFile(-50)
		if m.working.top != 0 || !strings.Contains(plain(m), "file-00") {
			t.Fatal("first file is not visible after reverse navigation")
		}
	}
	m.quit()
}

func TestUnifiedWorkingCopyStatusAndSuccessfulRefreshTime(t *testing.T) {
	m, repo := setup(t, 3)
	m.mode = workingCopyMode
	repo.status.Entries = []gitrepo.WorkingCopyEntry{{Path: "file", WorktreeStatus: "M"}}
	old := m.now.Add(-2 * time.Minute)
	m.statusRefreshed = old
	for _, layout := range []struct {
		width int
		focus string
	}{{120, "history"}, {75, "history"}, {75, "details"}} {
		m.width, m.focus = layout.width, layout.focus
		m.working.loaded = true
		m.refresh(m.scope)
		text := plain(m)
		if strings.Count(text, "Refreshing working copy") != 1 || strings.Contains(text, "Ready") {
			t.Fatal("global refresh status is duplicated or stale data claims ready")
		}
	}
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.statusRefreshed != old || !strings.Contains(plain(m), "Loading file") {
		t.Fatal("status read marked the selected file ready before it settled")
	}
	result := fileDetailsMsg{generation: m.generation, request: m.working.request, path: m.working.path,
		details: gitrepo.WorkingCopyDetails{Outcome: gitrepo.WorkingCopyReady,
			Sections: []gitrepo.WorkingCopySection{{Label: "Unstaged", Kind: gitrepo.WorkingCopyPatch,
				Text: "--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new\n"}}}}
	m.Update(result)
	if !m.statusRefreshed.After(old) || !strings.Contains(plain(m), "Ready · just now") ||
		strings.Contains(plain(m), "Unstaged modified") {
		t.Fatal("successful refresh lacks a truthful ready timestamp")
	}
	stamp := m.statusRefreshed
	deliverRefresh(t, m, m.refresh(m.scope))
	result.generation, result.request = m.generation, m.working.request
	result.err = errors.New("selected file failed")
	m.Update(result)
	if m.statusRefreshed != stamp || strings.Contains(plain(m), "Ready") ||
		!strings.Contains(plain(m), "File unavailable") {
		t.Fatal("file failure advanced ready time or hid the error")
	}
	repo.statusErr = errors.New("status failed")
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.statusRefreshed != stamp || strings.Contains(plain(m), "Ready") {
		t.Fatal("failed status refresh advanced ready time")
	}
	m.mode = historyMode
	stamp = m.historyRefreshed
	repo.historyErr = errors.New("history failed")
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.historyRefreshed != stamp || strings.Contains(plain(m), "Ready") {
		t.Fatal("failed history refresh advanced ready time")
	}
	m.quit()
}
