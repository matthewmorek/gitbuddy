// Package ui owns application state. Git commands return messages; only Update
// changes state, so concurrent reads never race navigation or repository writes.
package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

type historyMsg struct {
	generation int
	scope      gitrepo.Scope
	status     gitrepo.Status
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
type mutationMsg struct {
	fetch bool
	err   error
}

// QuitMsg is also used by the OS signal handler. Active pulls settle first.
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
	snapshot                                              gitrepo.Snapshot
	rows                                                  []gitrepo.Commit
	selected                                              string
	details                                               *gitrepo.Details
	top, detailsTop                                       int
	generation, detailRequest, navigation, timer          int
	loading, paging, hasMore, detailsLoading, slowDetails bool
	started, closing                                      bool
	operation, failure                                    string
	lastFetch                                             time.Time
	now                                                   time.Time
	fetchFailed                                           bool
	dialog                                                string
	input                                                 textinput.Model
	choice, errorTop                                      int
	spinner                                               spinner.Model
	updates                                               appUpdates
}

func New(ctx context.Context, repo gitrepo.Repo, settings *preferences.Store, themes *palette.Catalog, updates UpdateService, automaticUpdates bool) *Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Search commands"
	input.SetVirtualCursor(true)
	return &Model{ctx: ctx, repo: repo, settings: settings, themes: themes, width: 80, height: 24, dark: true, focus: "history", scope: gitrepo.Current, input: input, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)), updates: newAppUpdates(updates, automaticUpdates), now: time.Now()}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(m.scope), tea.RequestBackgroundColor, m.spinner.Tick, m.startUpdateCheck(), m.minuteTick())
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
	generation := m.generation
	m.loading = true
	m.paging = false
	m.requestedScope = scope
	count := min(m.settings.Value.MaxCommits, max(200, len(m.rows)))
	if scope != m.scope {
		count = min(200, m.settings.Value.MaxCommits)
	}
	limit := count
	if count < m.settings.Value.MaxCommits {
		limit++
	}
	ctx, repo := m.ctx, m.repo
	return func() tea.Msg {
		status, err := repo.Status(ctx)
		if err != nil {
			return historyMsg{generation: generation, err: err}
		}
		snapshot, err := repo.Snapshot(ctx, scope)
		if err != nil {
			return historyMsg{generation: generation, err: err}
		}
		rows, err := repo.History(ctx, snapshot, 0, limit)
		return historyMsg{generation, scope, status, snapshot, rows, count, err}
	}
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
	if m.operation != "" || m.closing || m.updates.installing {
		return nil
	}
	fetch := mode == "fetch"
	if fetch && len(m.status.Remotes) == 0 {
		return nil
	}
	m.operation = "Fetching"
	if !fetch {
		m.operation = "Pulling (" + mode + ")"
	}
	repo := m.repo
	// Deliberately independent of the read context: ordinary quit waits for the
	// mutation result, rather than interrupting a merge/rebase or deleting locks.
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		var err error
		if fetch {
			err = repo.Fetch(context.Background())
		} else {
			err = repo.Pull(context.Background(), gitrepo.PullMode(mode))
		}
		return mutationMsg{fetch, err}
	})
}

func (m *Model) quit() tea.Cmd {
	m.closing = true
	m.timer++
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
		m.ensureVisible()
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m, nil
	case historyMsg:
		if msg.generation != m.generation || m.closing {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		anchor := ""
		if m.top < len(m.rows) {
			anchor = m.rows[m.top].SHA
		}
		m.status = msg.status
		m.snapshot = msg.snapshot
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
		if m.selected == "" && len(m.rows) > 0 {
			detail = m.selectCommit(m.rows[0].SHA)
		}
		m.ensureVisible()
		// Give the loaded local view a render interval before the initial fetch.
		var ready tea.Cmd
		if !m.started {
			generation := m.generation
			ready = tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return readyMsg(generation) })
		}
		return m, tea.Batch(detail, ready)
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
		if m.started || m.closing || int(msg) != m.generation || m.loading {
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
		return m, tea.Batch(m.refresh(m.scope), m.spinner.Tick)
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
			if m.dialog != "" {
				m.closeDialog()
				return m, nil
			}
			return m, m.quit()
		}
		if m.closing {
			return m, nil
		}
		if key == "ctrl+p" {
			if m.dialog != "" {
				m.closeDialog()
				return m, nil
			}
			return m, m.openDialog("commands", "")
		}
		if m.dialog != "" {
			return m, m.dialogKey(msg)
		}
		switch key {
		case "q":
			return m, m.quit()
		case "r":
			return m, tea.Batch(m.refresh(m.scope), m.spinner.Tick)
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
		case "up", "k", "down", "j", "pgup", "pgdown":
			delta := 1
			if key == "up" || key == "k" || key == "pgup" {
				delta = -1
			}
			if key == "pgup" || key == "pgdown" {
				delta *= m.visibleRows()
			}
			if m.focus == "details" {
				m.detailsTop = max(0, min(m.maxDetailsTop(), m.detailsTop+delta))
			} else {
				return m, m.move(delta)
			}
		}
		return m, nil
	}
	if m.dialog != "" && m.dialog != "errors" {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) activity() string {
	if m.operation != "" {
		return m.operation + "..."
	}
	if m.loading {
		return "Loading history..."
	}
	if m.paging {
		return "Loading more commits..."
	}
	return ""
}
func (m *Model) warnings() string {
	all := []string{m.failure, m.settings.Warning, m.updates.failure}
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

func (m *Model) openDialog(kind, value string) tea.Cmd {
	m.dialog = kind
	m.choice = 0
	m.errorTop = 0
	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
}
func (m *Model) closeDialog() { m.dialog = ""; m.input.Blur() }

func (m *Model) save(next preferences.Settings) tea.Cmd {
	old := m.settings.Value
	err := m.settings.Update(next)
	m.fail(err)
	if err != nil && m.settings.Value == old {
		return nil
	}
	cmds := []tea.Cmd{m.schedule()}
	if next.MaxCommits != old.MaxCommits {
		if len(m.rows) > next.MaxCommits {
			m.rows = m.rows[:next.MaxCommits]
		}
		m.top = min(m.top, max(0, len(m.rows)-m.visibleRows()))
		scope := m.scope
		if m.loading {
			scope = m.requestedScope
		}
		cmds = append(cmds, m.refresh(scope), m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func (m *Model) dialogKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" {
		m.closeDialog()
		return nil
	}
	if m.dialog == "errors" {
		last := max(0, len(m.errorLines())-1)
		m.errorTop = min(m.errorTop, last)
		switch key {
		case "up", "k":
			m.errorTop = max(0, m.errorTop-1)
		case "down", "j":
			m.errorTop = min(last, m.errorTop+1)
		case "pgdown":
			m.errorTop = min(last, m.errorTop+5)
		case "pgup":
			m.errorTop = max(0, m.errorTop-5)
		}
		return nil
	}
	if key == "enter" {
		if m.dialog == "max" || m.dialog == "interval" {
			next := m.settings.Value
			n, err := strconv.Atoi(m.input.Value())
			if err != nil {
				m.failure = "Enter a positive whole number."
				m.ensureVisible()
				return nil
			}
			if m.dialog == "max" {
				next.MaxCommits = n
			} else {
				next.FetchIntervalMinutes = n
			}
			if err = preferences.Validate(next); err != nil {
				m.fail(err)
				return nil
			}
			m.closeDialog()
			return m.save(next)
		}
		options := m.options()
		if len(options) == 0 {
			return nil
		}
		option := options[min(m.choice, len(options)-1)]
		if option.disabled != "" {
			m.failure = option.disabled
			m.ensureVisible()
			return nil
		}
		m.closeDialog()
		return m.execute(option)
	}
	if key == "up" || key == "down" || key == "tab" || key == "shift+tab" {
		if key == "up" || key == "shift+tab" {
			m.choice = max(0, m.choice-1)
		} else {
			m.choice = min(max(0, len(m.options())-1), m.choice+1)
		}
		return nil
	}
	var cmd tea.Cmd
	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if before != m.input.Value() {
		m.choice = 0
	}
	return cmd
}

type action struct{ id, title, category, value, disabled, shortcut string }

func (m *Model) options() []action {
	var options []action
	switch m.dialog {
	case "confirm-update":
		options = []action{
			{id: "cancel-update", title: "Cancel", category: "App"},
			{id: "confirm-update", title: "Install Gitbuddy " + m.updates.available.Version, category: "App", disabled: m.updateInstallDisabled()},
		}
	case "themes":
		for _, name := range m.themes.Names {
			options = append(options, action{id: "theme", title: name, category: "Settings › Theme", value: name})
		}
	case "modes":
		for _, mode := range []string{"system", "light", "dark"} {
			options = append(options, action{id: "mode", title: mode, category: "Settings › Color mode", value: mode})
		}
	default:
		busy := ""
		if m.operation != "" {
			busy = "A Git operation is already running"
		} else if m.updates.installing {
			busy = "Wait for the update installation to finish"
		}
		fetch := busy
		if fetch == "" && len(m.status.Remotes) == 0 {
			fetch = "No remotes configured"
		}
		pull := busy
		if pull == "" {
			switch {
			case m.status.Operation != "":
				pull = "Finish the current Git operation externally"
			case m.status.Branch == "" || m.status.Head == "":
				pull = "An attached, committed branch is required"
			case m.status.Upstream == "":
				pull = "No upstream configured"
			case m.status.Dirty:
				pull = "Working tree must be clean, including untracked files"
			}
		}
		options = []action{{id: "fetch", title: "Fetch now", category: "Git", disabled: fetch}, {id: "ff-only", title: "Pull — fast-forward only", category: "Git", disabled: pull}, {id: "merge", title: "Pull — merge", category: "Git", disabled: pull}, {id: "rebase", title: "Pull — rebase", category: "Git", disabled: pull}, {id: "refresh", title: "Refresh local history", category: "History", shortcut: "r"}, {id: "all", title: "All branches", category: "History"}, {id: "current", title: "Current branch", category: "History"}, {id: "settings", title: "Open settings", category: "Settings"}, {id: "auto", title: "Automatic fetch enabled", category: "Settings › Fetch", value: strconv.FormatBool(m.settings.Value.AutoFetch)}, {id: "interval", title: "Automatic fetch interval", category: "Settings › Fetch", value: fmt.Sprintf("%d min", m.settings.Value.FetchIntervalMinutes)}, {id: "max", title: "Maximum commits", category: "Settings › History", value: strconv.Itoa(m.settings.Value.MaxCommits)}, {id: "themes", title: "Theme", category: "Settings › Appearance", value: m.settings.Value.Theme}, {id: "modes", title: "Color mode", category: "Settings › Appearance", value: m.settings.Value.ColorMode}, {id: "errors", title: "Show errors and warnings", category: "Status"}, {id: "clear", title: "Clear error", category: "Status"}, {id: "quit", title: "Quit", category: "App", shortcut: "q"}}
		// Keep Quit last, including in compact palettes navigated to the end.
		quit := options[len(options)-1]
		options = append(options[:len(options)-1], m.updateActions()...)
		options = append(options, quit)
	}
	var result []action
	for _, a := range options {
		if m.dialog == "settings" && !strings.HasPrefix(a.category, "Settings") {
			continue
		}
		if matches(m.input.Value(), a.title+" "+a.category) {
			result = append(result, a)
		}
	}
	return result
}
func matches(query, text string) bool {
	text = strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		remaining := []rune(word)
		for _, r := range text {
			if len(remaining) > 0 && remaining[0] == r {
				remaining = remaining[1:]
			}
		}
		if len(remaining) > 0 {
			return false
		}
	}
	return true
}
func (m *Model) execute(a action) tea.Cmd {
	switch a.id {
	case "check-updates":
		return m.checkUpdates(true)
	case "install-update":
		if !m.closing && m.updateInstallDisabled() == "" {
			return m.openDialog("confirm-update", "")
		}
	case "confirm-update":
		return m.installUpdate()
	case "cancel-update":
		return nil
	case "restart":
		if m.updates.installed != "" {
			m.updates.restart = true
			return m.quit()
		}
	case "fetch", "ff-only", "merge", "rebase":
		return m.mutate(a.id)
	case "refresh":
		return tea.Batch(m.refresh(m.scope), m.spinner.Tick)
	case "all", "current":
		return tea.Batch(m.refresh(gitrepo.Scope(a.id)), m.spinner.Tick)
	case "settings", "themes", "modes", "errors":
		return m.openDialog(a.id, "")
	case "max":
		return m.openDialog("max", strconv.Itoa(m.settings.Value.MaxCommits))
	case "interval":
		return m.openDialog("interval", strconv.Itoa(m.settings.Value.FetchIntervalMinutes))
	case "auto":
		next := m.settings.Value
		next.AutoFetch = !next.AutoFetch
		return m.save(next)
	case "theme":
		next := m.settings.Value
		next.Theme = a.value
		return m.save(next)
	case "mode":
		next := m.settings.Value
		next.ColorMode = a.value
		return m.save(next)
	case "clear":
		m.failure = ""
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
