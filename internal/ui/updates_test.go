package ui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
	if service.checks != 1 || service.installs != 0 || m.modalKind() != modalCommands ||
		m.modalInput() != "theme" || m.focus != "details" || m.selected != selected {
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

func TestPaletteInstallStartsWithSingleEnterAndRestartStaysExplicit(t *testing.T) {
	m, service := availableUpdate(t)
	m.openDialog("commands", "Install update")
	command := press(m, tea.KeyEnter, "")
	if !m.updates.installing || service.installs != 0 || m.modalKind() != "" {
		t.Fatal("installation did not stay behind the command boundary")
	}
	m.Update(updateInstallMsg{request: m.updates.request - 1, version: "stale"})
	if !m.updates.installing || m.updates.installed != "" || m.RestartRequested() {
		t.Fatal("stale installation result changed the pending operation")
	}
	m.Update(updateResult(t, command))
	if service.installs != 1 || m.updates.installed != "0.1.1" ||
		m.RestartRequested() || m.closing || m.modalKind() != "" {
		t.Fatal("installation unexpectedly restarted or stole focus")
	}
	if !strings.Contains(plain(m), "installed") || m.installUpdate() != nil {
		t.Fatal("missing installed notice or duplicate installation allowed")
	}
	m.openDialog("commands", "Restart")
	command = press(m, tea.KeyEnter, "")
	if command == nil || command() != tea.Quit() || !m.RestartRequested() || m.modalKind() != "" {
		t.Fatal("single Enter on Restart did not directly request orderly restart")
	}
}

func TestFailedPaletteInstallKeepsRestartDisabledAndAllowsRetry(t *testing.T) {
	m, service := availableUpdate(t)
	service.install = func(context.Context, updater.Release) error { return errors.New("offline") }
	m.openDialog("commands", "Install update")
	m.Update(updateResult(t, press(m, tea.KeyEnter, "")))
	m.openDialog("commands", "Restart")
	options := m.options()
	if len(options) != 1 || options[0].disabled == "" || m.updates.installed != "" ||
		m.updates.installing || m.RestartRequested() || !strings.Contains(m.warnings(), "Install update: offline") {
		t.Fatal("failed installation enabled restart or lost its error")
	}
	press(m, tea.KeyEnter, "")
	if m.RestartRequested() || m.closing || service.installs != 1 {
		t.Fatal("restart after failed installation performed an action")
	}
	service.install = nil
	m.openDialog("commands", "Install update")
	m.Update(updateResult(t, press(m, tea.KeyEnter, "")))
	if service.installs != 2 || m.updates.installed != "0.1.1" || m.updates.failure != "" || m.RestartRequested() {
		t.Fatal("single-Enter retry did not recover without restarting")
	}
}

func TestPaletteUpdateCommandsStayVisibleAndDisabledUntilReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Model)
	}{
		{"no service", func(m *Model) { m.updates = newAppUpdates(nil, false) }},
		{"no newer release", func(m *Model) { m.updates.available = updater.Release{} }},
		{"unsupported installation", func(m *Model) { m.updates.installReason = "Use the shell installer" }},
		{"checking", func(m *Model) { m.updates.checking = true }},
		{"installing", func(m *Model) { m.updates.installing = true }},
		{"Git mutation", func(m *Model) { m.operation = "Fetching" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, service := availableUpdate(t)
			tc.change(m)
			for _, title := range []string{"Install update", "Restart"} {
				m.openDialog("commands", title)
				options := m.options()
				if len(options) != 1 || options[0].title != title || options[0].disabled == "" {
					t.Fatalf("%s must remain visible and disabled", title)
				}
				reason := options[0].disabled
				if !strings.Contains(ansi.Strip(m.dialogView(m.colors(), 84, 28)), reason) {
					t.Fatalf("%s did not display its disabled reason %q", title, reason)
				}
				request, installing := m.updates.request, m.updates.installing
				press(m, tea.KeyEnter, "") // May schedule notification expiry, not installation.
				if m.modalKind() != modalCommands || m.updates.request != request || m.updates.installing != installing ||
					service.installs != 0 || m.RestartRequested() || m.closing {
					t.Fatalf("disabled %s performed an action", title)
				}
			}
		})
	}
}

func TestDisabledUpdateRowsAreMutedWithoutLosingSelection(t *testing.T) {
	m, _ := setup(t, 3)
	m.updates = newAppUpdates(&fakeUpdates{}, false)
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		for _, query := range []string{"update", "Install update", "Restart"} {
			m.openDialog("commands", query)
			rendered := m.dialogView(c, 84, 28)
			rows := uv.NewStyledString(rendered).Lines(ansi.GraphemeWidth)
			found := false
			for _, row := range rows {
				text := row.String()
				title := "Install update"
				if query == "Restart" {
					title = "Restart"
				}
				start := strings.Index(text, title)
				if start < 0 || !strings.HasSuffix(strings.TrimSpace(text), "App") {
					continue
				}
				start = ansi.StringWidth(text[:start])
				found = true
				bg := c.Selection
				if query == "update" {
					bg = c.Panel // Check for updates is selected instead.
				}
				for _, cell := range row[start : start+len(title)] {
					if cell.Style.Fg == nil || color.RGBAModel.Convert(cell.Style.Fg) != color.RGBAModel.Convert(lipgloss.Color(c.Muted)) ||
						cell.Style.Bg == nil || color.RGBAModel.Convert(cell.Style.Bg) != color.RGBAModel.Convert(lipgloss.Color(bg)) {
						t.Fatalf("%s %s: disabled row lost muted styling or selection surface", mode, query)
					}
				}
				if query != "update" && !strings.Contains(text, "▎") {
					t.Fatal("selected command did not use the notification's left rule")
				}
			}
			if !found {
				t.Fatalf("%s command row not rendered", query)
			}
			if mode == "dark" && query == "Install update" {
				t.Logf("rendered disabled command:\n%s", ansi.Strip(rendered))
			}
		}
	}
}

func TestInstallCompletionPreservesPaletteQuitSelection(t *testing.T) {
	m, service := availableUpdate(t)
	m.openDialog("commands", "Install update")
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
	selectedID := func() string {
		options := m.options()
		return options[m.modals.current().selection(options)].id
	}
	for range len(m.options()) {
		if selectedID() == "quit" {
			break
		}
		press(m, tea.KeyDown, "")
	}
	if options := m.options(); selectedID() != "quit" || options[len(options)-1].id != "quit" {
		t.Fatal("could not select the final Quit action")
	}

	m.Update(updateResult(t, command))
	if m.modalKind() != modalCommands || m.modalInput() != "" {
		t.Fatal("installation changed the open, unfiltered command palette")
	}
	options := m.options()
	if selectedID() != "quit" {
		t.Errorf("installation changed Quit selection to %q", selectedID())
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

func TestUpdateNoticesExpireButOngoingOperationsAndCommandsRemain(t *testing.T) {
	m, _ := availableUpdate(t)
	old := m.noticeID
	m.Update(noticeExpiredMsg(old))
	if strings.Contains(plain(m), "Gitbuddy 0.1.1 available") || m.updates.available.Version != "0.1.1" {
		t.Fatal("available update expiry hid the command or retained the banner")
	}
	cmd := m.installUpdate()
	m.width, m.height = 60, 15
	if !strings.Contains(plain(m), "Installing Gitbuddy") {
		t.Fatal("compact notification hint hid an ongoing installation")
	}
	m.Update(noticeExpiredMsg(old))
	if !strings.Contains(plain(m), "Installing Gitbuddy") {
		t.Fatal("old notification expiry hid an ongoing installation")
	}
	m.Update(updateResult(t, cmd))
	newer := m.noticeID
	m.Update(noticeExpiredMsg(old))
	if !strings.Contains(plain(m), "installed") {
		t.Fatal("old expiry cleared installation result")
	}
	m.Update(noticeExpiredMsg(newer))
	if m.notice != "" || m.updates.installed == "" || m.updateActions()[2].disabled != "" {
		t.Fatal("installed notice expiry lost restart state")
	}
}
