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
}

func (r *fakeRepository) Root() string                                   { return "/example/repository" }
func (r *fakeRepository) Status(context.Context) (gitrepo.Status, error) { return r.status, nil }
func (r *fakeRepository) Snapshot(context.Context, gitrepo.Scope) (gitrepo.Snapshot, error) {
	return gitrepo.Snapshot{Roots: []string{"frozen"}}, nil
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
		repo.rows = append(repo.rows, gitrepo.Commit{SHA: fmt.Sprintf("%040d", i), Subject: fmt.Sprintf("Commit %d", i), Author: "Author", AuthoredAt: "2026-11-13"})
	}
	settings := preferences.Load(t.TempDir())
	settings.Value.AutoFetch = false
	m := New(context.Background(), repo, settings, palette.Load(settings.Directory))
	m.width = 120
	m.height = 32
	m.Update(m.refresh(gitrepo.All)())
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
			if !strings.HasPrefix(line, " ") || !strings.HasSuffix(line, " ") {
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
		if !strings.HasPrefix(lines[1], "  repository") {
			t.Fatal("header inset")
		}
	}
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
