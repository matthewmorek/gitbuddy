package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/updater"
)

type fakeUpdates struct {
	checks, installs int
	reason           string
	checkErr         error
	install          func(context.Context, updater.Release) error
}

func (u *fakeUpdates) InstallReason() string { return u.reason }
func (u *fakeUpdates) Check(context.Context) (updater.Release, error) {
	u.checks++
	return updater.Release{Version: "0.1.1"}, u.checkErr
}
func (u *fakeUpdates) Install(ctx context.Context, release updater.Release) error {
	u.installs++
	if u.install != nil {
		return u.install(ctx, release)
	}
	return nil
}

// Execute one command batch, not its recurring spinner commands. This reaches
// the real asynchronous service boundary without network, sleeps, or a TUI loop.
func updateResult(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("missing update command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, child := range batch {
			message := child()
			switch message.(type) {
			case updateCheckMsg, updateInstallMsg:
				return message
			}
		}
	}
	t.Fatalf("no service result in %T", message)
	return nil
}

func availableUpdate(t *testing.T) (*Model, *fakeUpdates) {
	t.Helper()
	m, _ := setup(t, 3)
	service := &fakeUpdates{}
	m.updates = newAppUpdates(service, true)
	m.Update(updateResult(t, m.checkUpdates(false)))
	return m, service
}

func TestStartupUpdateCheckIsDeferredQuietAndDoesNotStealFocus(t *testing.T) {
	m, _ := setup(t, 3)
	service := &fakeUpdates{}
	m.updates = newAppUpdates(service, true)
	m.Init()
	if service.checks != 0 || m.updates.started || m.updates.status != "" {
		t.Fatal("startup performed network work or displayed activity before the first frame")
	}
	m.focus = "details"
	m.openDialog("commands", "theme")
	selected := m.selected
	m.operation = "Fetching"
	m.loading = true // The update check does not depend on successful Git reads.
	_, command := m.Update(updateStartupMsg{})
	if service.checks != 0 || !m.updates.checking || m.updates.status != "" {
		t.Fatal("check was not deferred/quiet")
	}
	if _, duplicate := m.Update(updateStartupMsg{}); duplicate != nil {
		t.Fatal("duplicate startup check")
	}
	m.Update(updateResult(t, command))
	if service.checks != 1 || service.installs != 0 || m.dialog != "commands" ||
		m.input.Value() != "theme" || m.focus != "details" || m.selected != selected {
		t.Fatal("background check changed navigation, focus, or installed anything")
	}
	if view := plain(m); !strings.Contains(view, "Gitbuddy 0.1.1 available") ||
		!strings.Contains(view, "Fetching") {
		t.Fatal("update notice hid Git status")
	}
}

func TestAutomaticUpdateFailureIsQuietButManualCheckReportsIt(t *testing.T) {
	m, _ := setup(t, 3)
	service := &fakeUpdates{checkErr: errors.New("offline")}
	m.updates = newAppUpdates(service, true)
	m.failure = "existing Git failure"
	m.Update(updateResult(t, m.checkUpdates(false)))
	if m.updates.status != "" || m.updates.failure != "" || m.failure != "existing Git failure" {
		t.Fatal("automatic check disturbed Git errors or displayed an offline warning")
	}
	m.Update(updateResult(t, m.execute(action{id: "check-updates"})))
	if !strings.Contains(m.warnings(), "Update check: offline") || m.failure != "existing Git failure" {
		t.Fatal("manual update error was hidden or replaced Git's error")
	}
	service.checkErr = nil
	m.Update(updateResult(t, m.checkUpdates(true)))
	if m.updates.failure != "" || m.updates.available.Version != "0.1.1" {
		t.Fatal("successful retry did not recover")
	}
}

func TestDisabledAutomaticCheckRetainsManualCommand(t *testing.T) {
	m, _ := setup(t, 3)
	service := &fakeUpdates{}
	m.updates = newAppUpdates(service, false)
	if m.startUpdateCheck() != nil {
		t.Fatal("disabled automatic checks scheduled work")
	}
	if _, command := m.Update(updateStartupMsg{}); command != nil {
		t.Fatal("disabled automatic check started")
	}
	m.Update(updateResult(t, m.execute(action{id: "check-updates"})))
	if service.checks != 1 {
		t.Fatal("manual command did not check")
	}
}

func TestInstallNeedsExplicitConfirmationAndDoesNotRestart(t *testing.T) {
	m, service := availableUpdate(t)
	m.execute(action{id: "install-update"})
	if m.dialog != "confirm-update" || m.options()[m.choice].id != "cancel-update" {
		t.Fatal("confirmation must default to Cancel")
	}
	press(m, tea.KeyEnter, "")
	if m.dialog != "" || service.installs != 0 || m.updates.installing {
		t.Fatal("default confirmation installed")
	}
	m.execute(action{id: "install-update"})
	press(m, tea.KeyEscape, "")
	if service.installs != 0 {
		t.Fatal("Escape installed")
	}
	m.execute(action{id: "install-update"})
	press(m, tea.KeyDown, "")
	command := press(m, tea.KeyEnter, "")
	if !m.updates.installing || service.installs != 0 || m.dialog != "" {
		t.Fatal("installation did not stay behind the command boundary")
	}
	m.Update(updateResult(t, command))
	if service.installs != 1 || m.updates.installed != "0.1.1" ||
		m.RestartRequested() || m.closing || m.dialog != "" {
		t.Fatal("installation unexpectedly restarted or stole focus")
	}
	if !strings.Contains(plain(m), "installed") || m.installUpdate() != nil {
		t.Fatal("missing installed notice or duplicate installation allowed")
	}
}

func TestInstallCompletionPreservesPaletteQuitSelection(t *testing.T) {
	m, service := availableUpdate(t)
	m.execute(action{id: "install-update"})
	press(m, tea.KeyDown, "")
	command := press(m, tea.KeyEnter, "")
	if !m.updates.installing || service.installs != 0 {
		t.Fatal("installation must be pending before opening the palette")
	}
	m.openDialog("commands", "")
	restartDisabled := false
	for _, option := range m.options() {
		if option.id == "restart" && option.disabled != "" {
			restartDisabled = true
		}
	}
	if !restartDisabled {
		t.Error("Restart must remain in the palette, disabled until installation succeeds")
	}
	for range len(m.options()) {
		if m.options()[m.choice].id == "quit" {
			break
		}
		press(m, tea.KeyDown, "")
	}
	if options := m.options(); options[m.choice].id != "quit" || options[len(options)-1].id != "quit" {
		t.Fatal("could not select the final Quit action")
	}

	m.Update(updateResult(t, command))
	if m.dialog != "commands" || m.input.Value() != "" {
		t.Fatal("installation changed the open, unfiltered command palette")
	}
	options := m.options()
	if options[m.choice].id != "quit" {
		t.Errorf("installation changed Quit selection to %q", options[m.choice].id)
	}
	restartEnabled := false
	for _, option := range options {
		if option.id == "restart" && option.disabled == "" && option.value == "0.1.1" {
			restartEnabled = true
		}
	}
	if !restartEnabled {
		t.Error("successful installation must enable Restart with the installed version")
	}
	quit := press(m, tea.KeyEnter, "")
	if quit == nil {
		t.Fatal("Enter on Quit did not return a quit command")
	}
	if message := quit(); message != tea.Quit() || !m.closing || m.RestartRequested() {
		t.Fatal("Enter on the selected Quit action must quit without requesting restart")
	}
}

func TestUpdateAndGitMutationsCannotOverlap(t *testing.T) {
	m, _ := availableUpdate(t)
	m.mutate("fetch")
	if m.installUpdate() != nil {
		t.Fatal("installation started during Git mutation")
	}
	m.operation = ""
	command := m.installUpdate()
	if m.mutate("merge") != nil || m.checkUpdates(true) != nil || m.installUpdate() != nil {
		t.Fatal("overlapping operation started during install")
	}
	m.Update(updateResult(t, command))
}

func TestQuitCancelsInstallAndWaitsForAllMutations(t *testing.T) {
	for _, gitFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "install-first", true: "git-first"}[gitFirst], func(t *testing.T) {
			m, service := availableUpdate(t)
			service.install = func(ctx context.Context, _ updater.Release) error {
				if ctx.Err() == nil {
					t.Fatal("quit did not cancel the installation context")
				}
				return ctx.Err()
			}
			command := m.installUpdate()
			m.operation = "Pulling" // Exercise both settlement orders defensively.
			_, quit := m.Update(QuitMsg{})
			if quit != nil {
				t.Fatal("quit did not wait")
			}
			first, last := tea.Msg(mutationMsg{}), tea.Msg(updateResult(t, command))
			if !gitFirst {
				first, last = last, first
			}
			if _, quit = m.Update(first); quit != nil {
				t.Fatal("quit before both mutations settled")
			}
			if _, quit = m.Update(last); quit == nil || m.RestartRequested() {
				t.Fatal("quit failed to settle or requested restart")
			}
		})
	}
}

func TestRestartWaitsForGitAndSignalCanCancelRestart(t *testing.T) {
	for _, signal := range []bool{false, true} {
		m, _ := availableUpdate(t)
		m.Update(updateResult(t, m.installUpdate()))
		m.operation = "Pulling"
		if m.execute(action{id: "restart"}) != nil || !m.RestartRequested() {
			t.Fatal("restart interrupted Git or lost intent")
		}
		if signal {
			m.Update(QuitMsg{})
		}
		if _, quit := m.Update(mutationMsg{}); quit == nil || m.RestartRequested() == signal {
			t.Fatal("restart/signal intent lost after settlement")
		}
	}
}

func TestUnsupportedInstallationKeepsManualGuidance(t *testing.T) {
	m, _ := setup(t, 3)
	service := &fakeUpdates{reason: "Development builds cannot replace themselves"}
	m.updates = newAppUpdates(service, true)
	m.Update(updateResult(t, m.checkUpdates(false)))
	if m.execute(action{id: "install-update"}) != nil || m.installUpdate() != nil ||
		service.installs != 0 || !strings.Contains(plain(m), "shell installer") {
		t.Fatal("unsupported installation lacked fallback or was replaced")
	}
}

func TestStaleChecksAndLateStartupCannotOverwriteState(t *testing.T) {
	m, _ := availableUpdate(t)
	before := m.updates.status
	m.Update(updateCheckMsg{request: m.updates.request - 1, err: errors.New("stale")})
	if m.updates.status != before {
		t.Fatal("stale response overwrote update state")
	}
	m.Update(QuitMsg{})
	if _, command := m.Update(updateStartupMsg{}); command != nil || m.checkUpdates(true) != nil {
		t.Fatal("late check started during shutdown")
	}
}

func TestUpdateNoticeKeepsSelectedRowVisible(t *testing.T) {
	m, _ := setup(t, 50)
	service := &fakeUpdates{}
	m.updates = newAppUpdates(service, true)
	m.height = 31 // One new footer line reduces the number of complete rows.
	index := m.visibleRows() - 1
	m.selected = m.rows[index].SHA
	m.Update(updateResult(t, m.checkUpdates(false)))
	if index < m.top || index >= m.top+m.visibleRows() || m.selected != m.rows[index].SHA {
		t.Fatal("update notice hid or moved the selection")
	}
}

func TestManualCheckWithoutNewerReleaseDoesNotOfferInstall(t *testing.T) {
	m, _ := setup(t, 3)
	m.updates = newAppUpdates(&fakeUpdates{}, true)
	m.checkUpdates(true)
	m.Update(updateCheckMsg{request: m.updates.request})
	if m.updates.available.Version != "" || m.updateInstallDisabled() == "" ||
		m.updates.status != "No newer stable release" {
		t.Fatal("no-update result offered an installation")
	}
}
