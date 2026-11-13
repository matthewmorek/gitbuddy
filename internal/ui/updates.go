package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/updater"
)

// UpdateService keeps network and executable replacement out of the event loop.
// Restart belongs to main, after Bubble Tea has restored the terminal.
type UpdateService interface {
	Check(context.Context) (updater.Release, error)
	InstallReason() string
	Install(context.Context, updater.Release) error
}

type appUpdates struct {
	service                                          UpdateService
	automatic, started, checking, manual, installing bool
	request                                          int
	cancel                                           context.CancelFunc
	available                                        updater.Release
	installReason, installed, status, failure        string
	restart                                          bool
}

type updateStartupMsg struct{}
type updateCheckMsg struct {
	request int
	release updater.Release
	err     error
}
type updateInstallMsg struct {
	request int
	version string
	err     error
}

func newAppUpdates(service UpdateService, automatic bool) appUpdates {
	state := appUpdates{service: service, automatic: automatic}
	if service == nil {
		state.installReason = "Updates are unavailable in this run"
	} else {
		state.installReason = safeLine(service.InstallReason())
	}
	return state
}

func (m *Model) startUpdateCheck() tea.Cmd {
	if m.updates.service == nil || !m.updates.automatic {
		return nil
	}
	// Independent of repository reads: even a failed history load must not block
	// the check. The first frame renders before any update network work begins.
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return updateStartupMsg{} })
}

func (m *Model) checkUpdates(manual bool) tea.Cmd {
	u := &m.updates
	if m.closing || u.service == nil || u.checking || u.installing || u.installed != "" {
		return nil
	}
	u.started, u.checking, u.manual = true, true, manual
	u.request++
	request, service := u.request, u.service
	ctx, cancel := context.WithCancel(m.ctx)
	u.cancel = cancel
	if manual {
		u.status = "Checking for updates..."
		u.failure = ""
		m.ensureVisible()
	}
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		release, err := service.Check(ctx)
		return updateCheckMsg{request, release, err}
	})
}

func (m *Model) updateInstallDisabled() string {
	u := &m.updates
	switch {
	case u.installReason != "":
		return u.installReason
	case u.installed != "":
		return "The update is installed; restart to use it"
	case u.installing:
		return "An update is already being installed"
	case u.checking:
		return "Wait for the update check to finish"
	case m.operation != "":
		return "Wait for the active Git operation to finish"
	case u.available.Version == "":
		return "Check for a newer stable release first"
	}
	return ""
}

func (m *Model) installUpdate() tea.Cmd {
	u := &m.updates
	if m.closing || m.updateInstallDisabled() != "" {
		return nil
	}
	u.installing = true
	u.request++
	u.failure = ""
	u.status = "Installing Gitbuddy " + u.available.Version + "..."
	m.ensureVisible()
	request, release, service := u.request, u.available, u.service
	ctx, cancel := context.WithCancel(m.ctx)
	u.cancel = cancel
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return updateInstallMsg{request, release.Version, service.Install(ctx, release)}
	})
}

func (m *Model) updateMessage(msg tea.Msg) tea.Cmd {
	defer m.ensureVisible()
	u := &m.updates
	switch msg := msg.(type) {
	case updateStartupMsg:
		if !u.started && u.automatic {
			return m.checkUpdates(false)
		}
	case updateCheckMsg:
		if msg.request != u.request || !u.checking || m.closing {
			return nil
		}
		u.cancel()
		u.cancel = nil
		u.checking = false
		if msg.err != nil {
			// Automatic network failures are quiet, and manual failures do not
			// replace Git's operation/failure state.
			if u.manual {
				u.failure = fmt.Sprintf("Update check: %v", msg.err)
				u.status = "Update check failed · Ctrl+P → Show errors and warnings"
			}
			return nil
		}
		u.failure = ""
		u.available = msg.release
		u.status = ""
		if u.available.Version != "" {
			u.status = "Gitbuddy " + u.available.Version + " available · Ctrl+P → Install update"
			if u.installReason != "" {
				u.status = "Gitbuddy " + u.available.Version + " available · update with the shell installer"
			}
		} else if u.manual {
			u.status = "No newer stable release"
		}
	case updateInstallMsg:
		if msg.request != u.request || !u.installing {
			return nil
		}
		u.cancel()
		u.cancel = nil
		u.installing = false
		if msg.err == nil {
			u.installed = msg.version
			u.status = "Gitbuddy " + msg.version + " installed · Ctrl+P → Restart"
		} else if !m.closing {
			u.failure = fmt.Sprintf("Install update: %v", msg.err)
			u.status = "Update not installed · Ctrl+P → Show errors and warnings"
		}
		if m.closing && m.operation == "" {
			return tea.Quit
		}
	}
	return nil
}

func (m *Model) updateActions() []action {
	u := &m.updates
	checkDisabled := ""
	switch {
	case u.service == nil:
		checkDisabled = "Updates are unavailable in this run"
	case u.checking:
		checkDisabled = "An update check is already running"
	case u.installing:
		checkDisabled = "An update is being installed"
	case u.installed != "":
		checkDisabled = "Restart to use the installed update"
	}
	restartDisabled := ""
	if u.installed == "" {
		restartDisabled = "Install an update successfully before restarting"
	}
	// Keep action positions stable while an open palette waits for installation.
	return []action{
		{id: "check-updates", title: "Check for updates", category: "App", disabled: checkDisabled},
		{id: "install-update", title: "Install update", category: "App", value: u.available.Version, disabled: m.updateInstallDisabled()},
		{id: "restart", title: "Restart", category: "App", value: u.installed, disabled: restartDisabled},
	}
}

func (m *Model) updateBusy() bool {
	return m.updates.installing || m.updates.checking && m.updates.manual
}

// RestartRequested is consumed only after Run returns and signal handlers stop.
func (m *Model) RestartRequested() bool { return m.updates.restart && m.updates.installed != "" }
