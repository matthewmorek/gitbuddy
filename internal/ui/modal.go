package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
)

const (
	modalCommands    = "commands"
	modalSettings    = "settings"
	modalThemes      = "themes"
	modalNumber      = "number"
	modalErrors      = "errors"
	modalStashNote   = "stash-note"
	modalStashApply  = "stash-apply"
	modalStashDelete = "stash-delete"
)

type modalPage struct {
	kind       string
	input      *textinput.Model
	selected   string // Stable action ID, independent of position or displayed value.
	top        int    // First visible list or diagnostic line.
	setting    string // Numeric editor target.
	validation string
	stash      gitrepo.Stash
	confirm    bool
}

// Modal pages suspend their own input, selection and scroll state. The
// underlying pane remains focused but receives no modal keystrokes.
type modalStack []modalPage

func (s modalStack) current() *modalPage {
	if len(s) == 0 {
		return nil
	}
	return &s[len(s)-1]
}

func (s *modalStack) push(page modalPage, value string) tea.Cmd {
	if parent := s.current(); parent != nil && parent.input != nil {
		parent.input.Blur()
	}
	placeholder := ""
	switch page.kind {
	case modalCommands:
		placeholder = "Search commands"
	case modalSettings:
		placeholder = "Search settings"
	case modalThemes:
		placeholder = "Search themes"
	case modalNumber:
		placeholder = "Enter a whole number"
	case modalStashNote:
		placeholder = "Optional note"
	}
	var cmd tea.Cmd
	if placeholder != "" {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		input.SetVirtualCursor(true)
		input.SetValue(value)
		input.CursorEnd()
		cmd = input.Focus()
		page.input = &input
	}
	*s = append(*s, page)
	return cmd
}

func (s *modalStack) pop() tea.Cmd {
	if len(*s) == 0 {
		return nil
	}
	last := len(*s) - 1
	(*s)[last] = modalPage{}
	*s = (*s)[:last]
	if parent := s.current(); parent != nil && parent.input != nil {
		return parent.input.Focus()
	}
	return nil
}

func (s *modalStack) dismiss() { *s = nil }

func (p *modalPage) updateInput(msg tea.Msg) tea.Cmd {
	if p.input == nil {
		return nil
	}
	before := p.input.Value()
	next, cmd := p.input.Update(msg)
	*p.input = next
	if before != next.Value() {
		p.selected, p.top, p.validation = "", 0, ""
	}
	return cmd
}

func (p *modalPage) selection(options []action) int {
	for i, option := range options {
		if option.id == p.selected {
			return i
		}
	}
	return 0
}

func (m *Model) modalKey(page *modalPage, msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "esc":
		m.modals.dismiss()
		return nil
	case "backspace":
		if page.input != nil && page.input.Value() != "" {
			return page.updateInput(msg)
		}
		if page.kind != modalCommands {
			return m.modals.pop()
		}
		return nil
	}
	switch page.kind {
	case modalStashApply, modalStashDelete:
		return m.stashConfirmationKey(page, key)
	case modalStashNote:
		return m.stashNoteKey(page, msg)
	case modalErrors:
		delta := 0
		switch key {
		case "up", "k":
			delta = -1
		case "down", "j":
			delta = 1
		case "pgdown":
			delta = 5
		case "pgup":
			delta = -5
		}
		last := max(0, len(m.errorLines())-1)
		page.top = max(0, min(last, min(page.top, last)+delta))
		return nil
	case modalNumber:
		if key == "enter" {
			return m.commitNumber(page)
		}
		return page.updateInput(msg)
	}

	options := m.options()
	if len(options) == 0 {
		return page.updateInput(msg)
	}
	at := page.selection(options)
	option := options[at]
	switch key {
	case "up", "shift+tab", "down", "tab":
		delta := 1
		if key == "up" || key == "shift+tab" {
			delta = -1
		}
		page.selected = options[max(0, min(len(options)-1, at+delta))].id
		m.keepModalSelectionVisible(page, options)
	case "left", "right":
		if page.kind != modalSettings {
			return page.updateInput(msg)
		}
		delta := 1
		if key == "left" {
			delta = -1
		}
		return m.adjustSetting(option.id, delta)
	case "enter":
		page.selected = option.id
		m.keepModalSelectionVisible(page, options)
		if option.disabled != "" {
			m.failure = option.disabled
			m.notify(m.failure, true)
			return nil
		}
		if option.child != "" {
			return m.modals.push(modalPage{kind: option.child, selected: option.childSelection}, "")
		}
		switch page.kind {
		case modalSettings:
			return m.editSetting(option.id)
		case modalThemes:
			next := m.settings.Value
			next.Theme = option.id
			save := m.save(next)
			return tea.Batch(save, m.modals.pop())
		case modalCommands:
			m.modals.dismiss()
			return m.execute(option)
		}
	default:
		return page.updateInput(msg)
	}
	return nil
}

type action struct {
	id, title, category, value, disabled, shortcut string
	description, keywords                          string
	child, childSelection                          string
}

func (m *Model) options() []action {
	page := m.modals.current()
	if page == nil || page.input == nil {
		return nil
	}
	var options []action
	switch page.kind {
	case modalCommands:
		options = m.commandActions()
	case modalSettings:
		options = m.settingRows()
	case modalThemes:
		for _, name := range m.themes.Names {
			a := action{id: name, title: name, description: "Use this theme for Gitbuddy."}
			if name == m.settings.Value.Theme {
				a.value, a.description = "Current", "This is your current theme."
			}
			options = append(options, a)
		}
	}
	if query := page.input.Value(); query != "" {
		filtered := options[:0]
		for _, a := range options {
			if matches(query, a.title+" "+a.category, a.keywords) {
				filtered = append(filtered, a)
			}
		}
		return filtered
	}
	return options
}

func matches(query, titleAndCategory, keywords string) bool {
	titleAndCategory, keywords = strings.ToLower(titleAndCategory), strings.ToLower(keywords)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		letters := []rune(word)
		if !subsequence(letters, titleAndCategory) && !subsequence(letters, keywords) {
			return false
		}
	}
	return true
}

func subsequence(letters []rune, text string) bool {
	for _, r := range text {
		if len(letters) == 0 {
			break
		}
		if letters[0] == r {
			letters = letters[1:]
		}
	}
	return len(letters) == 0
}

func (m *Model) commandActions() []action {
	busy := m.mutationBusy()
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
		}
	}
	legacy, safe := pull, pull
	if pull == "" && m.status.Dirty {
		legacy = "Working tree must be clean, including untracked files"
		if !m.settings.Value.AutoStashOnSafePull {
			safe = "Automatic stashing is off; working tree must be clean"
		}
	}
	options := []action{
		{id: "history", title: "History", category: "View", shortcut: "1", description: "Browse commits and their details."},
		{id: "working-copy", title: "Working copy", category: "View", shortcut: "2", description: "Review staged, unstaged and untracked files."},
		{id: "stashes", title: "Stashes", category: "View", shortcut: "3", description: "Preview, apply or delete saved changes."},
		{id: "fetch", title: "Fetch now", category: "Git", disabled: fetch, description: "Fetch remote refs without changing your working tree."},
		{id: "safe-pull", title: "Safe pull", category: "Git", disabled: safe, description: "Fetch and prune the upstream remote, then fast-forward if possible."},
		{id: "ff-only", title: "Pull — fast-forward only", category: "Git", disabled: legacy, description: "Pull without creating a merge commit or rebasing."},
		{id: "merge", title: "Pull — merge", category: "Git", disabled: legacy, description: "Fetch and merge the upstream branch."},
		{id: "rebase", title: "Pull — rebase", category: "Git", disabled: legacy, description: "Fetch and rebase local commits onto the upstream branch."},
		{id: modalStashNote, title: "Stash all", category: "Git", child: modalStashNote, disabled: m.stashDisabled(true), description: stashNoteDescription},
		{id: "refresh", title: "Refresh local repository", category: "Repository", shortcut: "r", description: "Reload local history and working copy status without fetching."},
		{id: "all", title: "All branches", category: "History", description: "Show commits across local branches, remote-tracking branches and tags."},
		{id: "current", title: "Current branch", category: "History", description: "Show commits for the current branch and its upstream."},
		{id: modalSettings, title: "Open settings", category: "Settings", child: modalSettings, description: "Browse and change Gitbuddy preferences."},
	}
	for i := range options {
		if options[i].id == string(m.activeScope()) {
			options[i].value = "active scope"
		}
	}
	for _, setting := range m.settingRows() {
		setting.category = "Settings › " + setting.category
		setting.child, setting.childSelection = modalSettings, setting.id
		options = append(options, setting)
	}
	options = append(options,
		action{id: modalErrors, title: "Show errors and warnings", category: "Status", child: modalErrors, description: "Read full diagnostics, including settings and theme warnings."},
		action{id: "clear", title: "Clear error", category: "Status", description: "Clear the current error and notification."},
	)
	options = append(options, m.updateActions()...)
	// Keep Quit last, including in compact palettes navigated to the end.
	return append(options, action{id: "quit", title: "Quit", category: "App", shortcut: "q", description: "Exit Gitbuddy after any active mutation finishes."})
}
