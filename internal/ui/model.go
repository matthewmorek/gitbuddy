// Package ui owns application state. Git commands return messages; only Update
// changes state, so concurrent reads never race navigation or repository writes.
package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

type historyMsg struct {
	generation int
	scope      gitrepo.Scope
	snapshot   gitrepo.Snapshot
	rows       []gitrepo.Commit
	count      int
	err        error
}
type pageMsg struct {
	generation, navigation, target, count int
	rows                                  []gitrepo.Commit
	err                                   error
}
type detailsMsg struct {
	request int
	details gitrepo.Details
	err     error
}
type slowDetailsMsg int
type autoMsg int
type readyMsg int
type minuteMsg time.Time
type noticeExpiredMsg int
type mutationMsg struct {
	fetch   bool
	err     error
	success string
}

// QuitMsg is also used by the OS signal handler. Active mutations settle first.
type QuitMsg struct{}

type Model struct {
	repo                                                  gitrepo.Repo
	ctx                                                   context.Context
	settings                                              *preferences.Store
	themes                                                *palette.Catalog
	width, height                                         int
	dark                                                  bool
	focus                                                 string
	scope, requestedScope                                 gitrepo.Scope
	status                                                gitrepo.Status
	working                                               workingCopy
	mode                                                  viewMode
	statusLoading                                         bool
	statusFailure, historyFailure                         string
	snapshot                                              gitrepo.Snapshot
	rows                                                  []gitrepo.Commit
	selected                                              string
	details                                               *gitrepo.Details
	top, detailsTop                                       int
	generation, detailRequest, navigation, timer          int
	fetchResetGeneration, fetchResetNavigation            int
	noticeID                                              int
	notice                                                string
	noticeWarning, noticePending                          bool
	loading, paging, hasMore, detailsLoading, slowDetails bool
	started, closing                                      bool
	operation, failure                                    string
	lastFetch                                             time.Time
	historyRefreshed, statusRefreshed                     time.Time
	statusRefreshPending                                  bool
	refreshCancel                                         context.CancelFunc
	now                                                   time.Time
	fetchFailed                                           bool
	modals                                                modalStack
	spinner                                               spinner.Model
	updates                                               appUpdates
	stashes                                               stashView
}

func New(ctx context.Context, repo gitrepo.Repo, settings *preferences.Store, themes *palette.Catalog, updates UpdateService, automaticUpdates bool) *Model {
	m := &Model{ctx: ctx, repo: repo, settings: settings, themes: themes, width: 80, height: 24, dark: true, focus: "history", scope: gitrepo.Current, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)), updates: newAppUpdates(updates, automaticUpdates), now: time.Now()}
	m.notify(m.warnings(), true)
	return m
}

func (m *Model) Init() tea.Cmd {
	if m.closing {
		return nil
	}
	return tea.Batch(m.refresh(m.scope), tea.RequestBackgroundColor, m.spinner.Tick, m.startUpdateCheck(), m.minuteTick(), m.noticeTimer())
}

func (m *Model) notify(text string, warning bool) {
	if text == "" || m.closing {
		return
	}
	m.noticeID++
	m.notice, m.noticeWarning, m.noticePending = text, warning, true
	m.ensureVisible()
}

func (m *Model) noticeTimer() tea.Cmd {
	if !m.noticePending || m.closing {
		return nil
	}
	m.noticePending = false
	token := m.noticeID
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return noticeExpiredMsg(token) })
}

// One clock drives all relative labels; it never refreshes Git or changes focus.
func (m *Model) minuteTick() tea.Cmd {
	if m.closing {
		return nil
	}
	return tea.Tick(time.Minute, func(now time.Time) tea.Msg { return minuteMsg(now) })
}

func (m *Model) refresh(scope gitrepo.Scope) tea.Cmd {
	if m.closing {
		return nil
	}
	m.generation++
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	generation := m.generation
	m.loading = true
	m.paging = false
	m.requestedScope = scope
	m.statusLoading = true
	m.statusRefreshPending = false
	m.statusFailure = ""
	m.retireFileRead()
	count := min(m.settings.Value.MaxCommits, max(200, len(m.rows)))
	if scope != m.scope {
		count = min(200, m.settings.Value.MaxCommits)
	}
	limit := count
	if count < m.settings.Value.MaxCommits {
		limit++
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.refreshCancel = cancel
	repo := m.repo
	return tea.Batch(func() tea.Msg {
		snapshot, err := repo.Snapshot(ctx, scope)
		if err != nil {
			return historyMsg{generation: generation, err: err}
		}
		rows, err := repo.History(ctx, snapshot, 0, limit)
		return historyMsg{generation: generation, scope: scope, snapshot: snapshot, rows: rows, count: count, err: err}
	}, func() tea.Msg {
		status, err := repo.Status(ctx)
		return statusMsg{generation, status, err}
	}, m.refreshStashes())
}

func (m *Model) selectCommit(sha string) tea.Cmd {
	if m.closing || sha == m.selected && (m.details != nil || m.detailsLoading) {
		return nil
	}
	m.selected = sha
	m.details = nil
	m.detailsTop = 0
	m.detailsLoading = true
	m.slowDetails = false
	m.detailRequest++
	request, ctx, repo := m.detailRequest, m.ctx, m.repo
	return tea.Batch(func() tea.Msg { d, e := repo.Details(ctx, sha); return detailsMsg{request, d, e} }, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return slowDetailsMsg(request) }))
}

func (m *Model) move(delta int) tea.Cmd {
	if len(m.rows) == 0 || m.closing {
		return nil
	}
	m.navigation++
	index := -1
	for i, c := range m.rows {
		if c.SHA == m.selected {
			index = i
			break
		}
	}
	if index < 0 && delta < 0 {
		index = len(m.rows)
	}
	target := index + delta
	if target >= len(m.rows) && m.hasMore && !m.loading && !m.paging {
		m.paging = true
		gen, nav, offset := m.generation, m.navigation, len(m.rows)
		count := min(200, m.settings.Value.MaxCommits-offset)
		limit := count
		if offset+count < m.settings.Value.MaxCommits {
			limit++
		}
		snapshot, ctx, repo := m.snapshot, m.ctx, m.repo
		return tea.Batch(m.spinner.Tick, func() tea.Msg {
			rows, err := repo.History(ctx, snapshot, offset, limit)
			return pageMsg{gen, nav, target, count, rows, err}
		})
	}
	target = max(0, min(len(m.rows)-1, target))
	cmd := m.selectCommit(m.rows[target].SHA)
	m.ensureVisible()
	return cmd
}

func (m *Model) schedule() tea.Cmd {
	m.timer++
	token := m.timer
	if !m.started || !m.settings.Value.AutoFetch || m.closing {
		return nil
	}
	return tea.Tick(time.Duration(m.settings.Value.FetchIntervalMinutes)*time.Minute, func(time.Time) tea.Msg { return autoMsg(token) })
}

func (m *Model) mutate(mode string) tea.Cmd {
	if mode == "fetch" {
		if len(m.status.Remotes) == 0 {
			return nil
		}
		repo := m.repo
		return m.runMutation("Fetching", func(ctx context.Context) mutationMsg {
			return mutationMsg{fetch: true, err: repo.Fetch(ctx)}
		})
	}
	repo := m.repo
	if mode == "safe-pull" {
		auto := m.settings.Value.AutoStashOnSafePull
		return m.runMutation("Safe pull", func(ctx context.Context) mutationMsg {
			_, err := repo.SafePull(ctx, auto)
			return mutationMsg{err: err, success: "Safe pull complete"}
		})
	}
	return m.runMutation("Pulling ("+mode+")", func(ctx context.Context) mutationMsg {
		return mutationMsg{err: repo.Pull(ctx, gitrepo.PullMode(mode))}
	})
}

func (m *Model) runMutation(activity string, run func(context.Context) mutationMsg) tea.Cmd {
	if m.mutationBusy() != "" || m.closing {
		return nil
	}
	m.operation = activity
	// Deliberately independent of the read context: ordinary quit waits for the
	// mutation result, rather than interrupting integration or restoration.
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return run(context.Background())
	})
}

func (m *Model) mutationBusy() string {
	if m.operation != "" {
		return "A Git operation is already running"
	}
	if m.updates.installing {
		return "Wait for the update installation to finish"
	}
	return ""
}

func (m *Model) quit() tea.Cmd {
	m.retireFileRead()
	m.stashes.retire()
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	m.closing = true
	m.timer++
	m.noticeID++
	m.noticePending = false
	if m.updates.cancel != nil {
		m.updates.cancel()
	}
	if m.operation == "" && !m.updates.installing {
		return tea.Quit
	}
	return nil
}
func (m *Model) fail(err error) {
	if err != nil {
		m.failure = err.Error()
		m.notify(m.failure, true)
	}
}

func (m *Model) Update(msg tea.Msg) (model tea.Model, command tea.Cmd) {
	// All notification producers share one tokenized expiry, including messages
	// repeated verbatim. Expiry hides presentation, never the diagnostic source.
	defer func() {
		if timer := m.noticeTimer(); timer != nil {
			command = tea.Batch(command, timer)
		}
	}()
	switch msg := msg.(type) {
	case noticeExpiredMsg:
		if !m.closing && int(msg) == m.noticeID {
			m.notice = ""
			m.ensureVisible()
		}
		return m, nil
	case minuteMsg:
		if m.closing {
			return m, nil
		}
		m.now = time.Time(msg)
		return m, m.minuteTick()
	case updateStartupMsg, updateCheckMsg, updateInstallMsg:
		return m, m.updateMessage(msg)
	case QuitMsg:
		m.updates.restart = false
		return m, m.quit()
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.ensureVisible()
		m.detailsTop = min(m.detailsTop, m.maxDetailsTop())
		m.sizeFileViewport()
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m, nil
	case historyMsg:
		if msg.generation != m.generation || m.closing {
			return m, nil
		}
		m.loading = false
		m.historyFailure = ""
		if msg.err != nil {
			m.historyFailure = msg.err.Error()
			m.fail(msg.err)
			return m, nil
		}
		anchor := ""
		if m.top < len(m.rows) {
			anchor = m.rows[m.top].SHA
		}
		m.snapshot = msg.snapshot
		m.notify(msg.snapshot.AncestryWarning, true)
		m.historyRefreshed = time.Now()
		m.scope = msg.scope
		m.rows = msg.rows[:min(msg.count, len(msg.rows))]
		m.hasMore = msg.count < m.settings.Value.MaxCommits && len(msg.rows) > msg.count
		for i, row := range m.rows {
			if row.SHA == anchor {
				m.top = i
				break
			}
		}
		m.top = min(m.top, max(0, len(m.rows)-m.visibleRows()))
		var detail tea.Cmd
		reset := m.fetchResetGeneration == msg.generation && m.fetchResetNavigation == m.navigation
		m.fetchResetGeneration = 0
		if reset {
			m.top = 0
			if len(m.rows) == 0 {
				m.selected, m.details = "", nil
				m.detailRequest++ // Retire a detail read from the previous list.
				m.detailsLoading, m.slowDetails = false, false
				m.detailsTop = 0
			}
		}
		if (reset || m.selected == "") && len(m.rows) > 0 {
			detail = m.selectCommit(m.rows[0].SHA)
		}
		m.ensureVisible()
		// Give the loaded local view a render interval before the initial fetch.
		return m, tea.Batch(detail, m.localReady())
	case statusMsg:
		return m, m.applyStatus(msg)
	case stashesMsg:
		return m, m.receiveStashes(msg)
	case stashPreviewMsg:
		if m.stashes.detail.accept(msg.request, msg.err) {
			m.stashes.preview = msg.preview
			m.fail(msg.err)
		}
		return m, nil
	case fileDetailsMsg:
		return m, m.applyFileDetails(msg)
	case pageMsg:
		if msg.generation != m.generation || m.closing {
			return m, nil
		}
		m.paging = false
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.rows = append(m.rows, msg.rows[:min(msg.count, len(msg.rows))]...)
		m.hasMore = len(m.rows) < m.settings.Value.MaxCommits && len(msg.rows) > msg.count
		if msg.navigation != m.navigation || len(m.rows) == 0 {
			return m, nil
		}
		cmd := m.selectCommit(m.rows[min(len(m.rows)-1, msg.target)].SHA)
		m.ensureVisible()
		return m, cmd
	case detailsMsg:
		if msg.request != m.detailRequest || m.closing {
			return m, nil
		}
		m.detailsLoading = false
		m.slowDetails = false
		m.fail(msg.err)
		if msg.err == nil {
			m.details = &msg.details
		}
		return m, nil
	case slowDetailsMsg:
		if int(msg) == m.detailRequest && m.detailsLoading && !m.closing {
			m.slowDetails = true
			return m, m.spinner.Tick
		}
		return m, nil
	case readyMsg:
		if m.started || m.closing || int(msg) != m.generation || m.loading || m.statusLoading {
			return m, nil
		}
		m.started = true
		var fetch tea.Cmd
		if m.settings.Value.AutoFetch {
			fetch = m.mutate("fetch")
		}
		return m, tea.Batch(m.schedule(), fetch)
	case autoMsg:
		if int(msg) != m.timer || m.closing {
			return m, nil
		}
		return m, tea.Batch(m.schedule(), m.mutate("fetch"))
	case mutationMsg:
		m.operation = ""
		m.fail(msg.err)
		if msg.err == nil {
			m.notify(msg.success, false)
		}
		if msg.fetch {
			m.fetchFailed = msg.err != nil
			if msg.err == nil {
				m.lastFetch = time.Now()
			}
		}
		if m.closing {
			if m.updates.installing {
				return m, nil
			}
			return m, tea.Quit
		}
		refresh := m.refresh(m.activeScope())
		if msg.fetch {
			m.fetchResetGeneration, m.fetchResetNavigation = m.generation, m.navigation
		}
		return m, tea.Batch(refresh, m.spinner.Tick)
	case spinner.TickMsg:
		if m.activity() == "" && !m.slowDetails && !m.updateBusy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			if m.modals.current() != nil {
				m.modals.dismiss()
				return m, nil
			}
			return m, m.quit()
		}
		if m.closing {
			return m, nil
		}
		if key == "ctrl+p" {
			if m.modals.current() != nil {
				m.modals.dismiss()
				return m, nil
			}
			return m, m.modals.push(modalPage{kind: modalCommands}, "")
		}
		if page := m.modals.current(); page != nil {
			return m, m.modalKey(page, msg)
		}
		switch key {
		case "1":
			return m, m.changeMode(historyMode)
		case "2":
			return m, m.changeMode(workingCopyMode)
		case "3":
			return m, m.changeMode(stashesMode)
		case "a", "d":
			m.openStashConfirmation(key)
		case "q":
			return m, m.quit()
		case "r":
			return m, tea.Batch(m.refresh(m.activeScope()), m.spinner.Tick)
		case "tab", "shift+tab":
			if m.focus == "history" {
				m.focus = "details"
			} else {
				m.focus = "history"
			}
		case "esc":
			m.focus = "history"
		case "enter":
			if m.width < 100 {
				m.focus = "details"
			}
		case "left", "right", "up", "k", "down", "j", "pgup", "pgdown":
			if m.mode == stashesMode {
				return m, m.navigateStashes(key)
			}
			if m.mode == workingCopyMode && m.focus == "details" {
				m.prepareFileViewport(m.colors())
				var cmd tea.Cmd
				m.working.viewport, cmd = m.working.viewport.Update(msg)
				return m, cmd
			}
			if key == "left" || key == "right" {
				return m, nil
			}
			delta := 1
			if key == "up" || key == "k" || key == "pgup" {
				delta = -1
			}
			if key == "pgup" || key == "pgdown" {
				if m.mode == workingCopyMode {
					delta *= m.visibleFileRows()
				} else {
					delta *= m.visibleRows()
				}
			}
			if m.focus == "details" {
				m.detailsTop = max(0, min(m.maxDetailsTop(), m.detailsTop+delta))
			} else {
				if m.mode == workingCopyMode {
					return m, m.moveFile(delta)
				}
				return m, m.move(delta)
			}
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.closing || m.modals.current() != nil || msg.Button != tea.MouseLeft || !m.historyVisible() || m.paneHeight() < 2 {
			return m, nil
		}
		for _, segment := range m.modeSegments() {
			if msg.Y == 4 && msg.X >= shellHorizontalPadding+horizontalPadding+segment.start && msg.X < shellHorizontalPadding+horizontalPadding+segment.end {
				return m, m.changeMode(segment.mode)
			}
		}
		return m, nil
	}
	if page := m.modals.current(); page != nil {
		return m, page.updateInput(msg)
	}
	return m, nil
}

func (m *Model) activity() string {
	if m.operation != "" {
		return m.operation + "..."
	}
	if m.mode == stashesMode {
		if m.stashes.list.cancel != nil {
			return "Loading stashes..."
		}
		if m.stashes.detail.cancel != nil {
			return "Loading stash contents..."
		}
		return ""
	}
	if m.mode == workingCopyMode {
		if m.statusLoading {
			if !m.working.loaded {
				return "Loading working copy..."
			}
			return "Refreshing working copy..."
		}
		if m.working.loading {
			if m.working.details != nil {
				return "Refreshing file..."
			}
			return "Loading file..."
		}
		return ""
	}
	if m.loading {
		return "Loading history..."
	}
	if m.paging {
		return "Loading more commits..."
	}
	return ""
}

// Either read can settle last. Startup fetch needs both local history and the
// live remote list; an early history result must not consume its only attempt.
func (m *Model) localReady() tea.Cmd {
	if m.started || m.closing || m.loading || m.statusLoading || m.historyFailure != "" || m.statusFailure != "" {
		return nil
	}
	generation := m.generation
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return readyMsg(generation) })
}
func (m *Model) warnings() string {
	all := []string{m.failure, m.settings.Warning, m.updates.failure, m.snapshot.AncestryWarning}
	all = append(all, m.status.FetchWarnings...)
	all = append(all, m.themes.Warnings...)
	var out []string
	for _, s := range all {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) execute(a action) tea.Cmd {
	switch a.id {
	case "history":
		return m.changeMode(historyMode)
	case "working-copy":
		return m.changeMode(workingCopyMode)
	case "check-updates":
		return m.checkUpdates(true)
	case "install-update":
		return m.installUpdate()
	case "restart":
		if m.updates.installed != "" {
			m.updates.restart = true
			return m.quit()
		}
	case "fetch", "safe-pull", "ff-only", "merge", "rebase":
		return m.mutate(a.id)
	case "stashes":
		return m.changeMode(stashesMode)
	case "refresh":
		return tea.Batch(m.refresh(m.activeScope()), m.spinner.Tick)
	case "all", "current":
		m.changeMode(historyMode)
		return m.changeScope(gitrepo.Scope(a.id))
	case "clear":
		m.failure = ""
		m.notice = ""
		m.noticeID++
		m.noticePending = false
		if m.updates.failure != "" {
			m.updates.failure = ""
			m.updates.status = ""
		}
		m.ensureVisible()
	case "quit":
		return m.quit()
	}
	return nil
}

func (m *Model) changeScope(scope gitrepo.Scope) tea.Cmd {
	if m.closing {
		return nil
	}
	if scope == m.activeScope() {
		return nil
	}
	return tea.Batch(m.refresh(scope), m.spinner.Tick)
}

func (m *Model) activeScope() gitrepo.Scope {
	if m.loading {
		return m.requestedScope
	}
	return m.scope
}
