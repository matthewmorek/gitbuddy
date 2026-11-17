package ui

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

type viewMode int

const (
	historyMode viewMode = iota
	workingCopyMode
)

// History and working-copy navigation have separate owners. Raw paths are keys;
// sanitized display names must never be used to request repository content.
type workingCopy struct {
	entries        []gitrepo.WorkingCopyEntry
	path           string
	top, request   int
	loaded         bool
	loading, stale bool
	failure        string
	details        *gitrepo.WorkingCopyDetails
	cancel         context.CancelFunc
	viewport       viewport.Model
	renderedFor    *gitrepo.WorkingCopyDetails
	renderedColors palette.Colors
	renderedBG     string
}

type statusMsg struct {
	generation int
	status     gitrepo.Status
	err        error
}

type fileDetailsMsg struct {
	generation, request int
	path                string
	details             gitrepo.WorkingCopyDetails
	err                 error
}

func (m *Model) changeMode(mode viewMode) tea.Cmd {
	if m.closing || m.mode == mode {
		return nil
	}
	m.retireFileRead()
	m.mode, m.focus = mode, "history"
	m.ensureVisible()
	if mode == workingCopyMode {
		return tea.Batch(m.refresh(m.activeScope()), m.spinner.Tick)
	}
	return nil
}

func (m *Model) retireFileRead() {
	w := &m.working
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.request++
	w.loading = false
	w.stale = w.details != nil
}

func (m *Model) applyStatus(msg statusMsg) tea.Cmd {
	if m.closing || msg.generation != m.generation {
		return nil
	}
	m.statusLoading = false
	if msg.err != nil {
		m.statusFailure = msg.err.Error()
		m.fail(msg.err)
		return nil
	}
	oldWarnings := strings.Join(m.status.FetchWarnings, "\n")
	m.status = msg.status
	m.statusRefreshPending = msg.status.StatsWarning == ""
	if m.statusRefreshPending && (m.mode != workingCopyMode || len(msg.status.Entries) == 0) {
		m.statusRefreshed = time.Now()
		m.statusRefreshPending = false
	}
	if warnings := strings.Join(m.status.FetchWarnings, "\n"); warnings != oldWarnings {
		m.notify(warnings, true)
	}
	w := &m.working
	ready := m.localReady()
	index := 0
	for i, entry := range w.entries {
		if entry.Path == w.path {
			index = i
			break
		}
	}
	w.entries = append([]gitrepo.WorkingCopyEntry(nil), msg.status.Entries...)
	sort.Slice(w.entries, func(i, j int) bool { return w.entries[i].Path < w.entries[j].Path })
	w.loaded = true
	if len(w.entries) == 0 {
		m.selectFile("")
		w.top = 0
		return ready
	}
	path := w.entries[min(index, len(w.entries)-1)].Path
	for _, entry := range w.entries {
		if entry.Path == w.path {
			path = w.path
			break
		}
	}
	if m.mode == workingCopyMode {
		cmd := m.selectFile(path) // Even the same path must be read again.
		m.ensureFileVisible()
		return tea.Batch(cmd, ready)
	}
	if path != w.path {
		m.selectFile("") // Do not read hidden details or retain a vanished patch.
		w.path = path
	}
	return ready
}

func (m *Model) selectFile(path string) tea.Cmd {
	w := &m.working
	m.retireFileRead()
	if path != w.path || path == "" {
		w.details = nil
		w.viewport = viewport.New(viewport.WithWidth(1), viewport.WithHeight(1))
		w.viewport.SoftWrap = false
	}
	w.path, w.failure = path, ""
	if path == "" {
		w.stale = false
		return nil
	}
	w.loading = true
	ctx, cancel := context.WithCancel(m.ctx)
	w.cancel = cancel
	generation, request, repo := m.generation, w.request, m.repo
	return func() tea.Msg {
		defer cancel()
		details, err := repo.WorkingCopyDetails(ctx, path)
		return fileDetailsMsg{generation, request, path, details, err}
	}
}

func (m *Model) applyFileDetails(msg fileDetailsMsg) tea.Cmd {
	w := &m.working
	if m.closing || m.mode != workingCopyMode || msg.generation != m.generation ||
		msg.request != w.request || msg.path != w.path {
		return nil
	}
	w.loading, w.cancel = false, nil
	if msg.err != nil {
		m.statusRefreshPending = false
		w.failure, w.stale = msg.err.Error(), w.details != nil
		m.fail(msg.err)
		return nil
	}
	if !reflect.DeepEqual(w.details, &msg.details) {
		w.viewport.SetXOffset(0)
		w.viewport.SetYOffset(0)
	}
	w.details, w.stale, w.failure = &msg.details, false, ""
	if m.statusRefreshPending {
		if msg.details.Outcome == gitrepo.WorkingCopyReady {
			m.statusRefreshed = time.Now()
		}
		m.statusRefreshPending = false
	}
	m.prepareFileViewport(m.colors())
	return nil
}

func (m *Model) moveFile(delta int) tea.Cmd {
	w := &m.working
	if len(w.entries) == 0 {
		return nil
	}
	index := 0
	for i, entry := range w.entries {
		if entry.Path == w.path {
			index = i
			break
		}
	}
	path := w.entries[max(0, min(len(w.entries)-1, index+delta))].Path
	if path == w.path {
		return nil
	}
	cmd := m.selectFile(path)
	m.ensureFileVisible()
	return cmd
}

func (m *Model) ensureFileVisible() {
	w := &m.working
	for i, entry := range w.entries {
		if entry.Path == w.path {
			if i < w.top {
				w.top = i
			}
			if i >= w.top+m.visibleFileRows() {
				w.top = i - m.visibleFileRows() + 1
			}
		}
	}
	w.top = max(0, min(w.top, max(0, len(w.entries)-m.visibleFileRows())))
}

func (m *Model) fileState() string {
	w := &m.working
	switch {
	case m.statusFailure != "" && w.details != nil:
		return "Retained content stale · r Retry"
	case w.failure != "":
		if w.details == nil {
			return "File unavailable · r Retry"
		}
		return "File unavailable · stale · r Retry"
	case w.stale && !m.statusLoading && !w.loading:
		return "Retained content stale · r Refresh"
	}
	return ""
}

func (m *Model) sizeFileViewport() {
	v := &m.working.viewport
	v.SetWidth(max(1, m.paneWidth()-2*horizontalPadding))
	height := m.paneHeight() - 2
	if m.fileState() != "" {
		height -= 2 // State row plus breathing room.
	}
	v.SetHeight(max(1, height))
	v.SetXOffset(v.XOffset())
	v.SetYOffset(v.YOffset())
}

func (m *Model) prepareFileViewport(c palette.Colors) {
	m.sizeFileViewport()
	bg := m.paneBackground("details", c)
	w := &m.working
	if w.renderedFor != w.details || w.renderedColors != c || w.renderedBG != bg {
		w.viewport.SetContent(m.renderFileDetails(c, bg))
		w.renderedFor, w.renderedColors, w.renderedBG = w.details, c, bg
	}
}

func (m *Model) renderFileDetails(c palette.Colors, bg string) string {
	d := m.working.details
	if d == nil {
		return ""
	}
	style := func(text, color string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(lipgloss.Color(bg)).Render(text)
	}
	rows := []string{style(safeLine(d.Entry.Path), c.Text)}
	if d.Entry.OldPath != "" {
		rows = append(rows, style("From "+safeLine(d.Entry.OldPath), c.Muted))
	}
	summary := d.Summary()
	if summary.Known {
		count := func(n int, noun string) string {
			if n != 1 {
				noun += "s"
			}
			return fmt.Sprintf("%d %s", n, noun)
		}
		rows = append(rows, style(strings.Join([]string{count(summary.Chunks, "chunk"),
			count(summary.Added, "insertion"), count(summary.Deleted, "deletion")}, ", "), c.Muted))
	} else if summary.Binary {
		rows = append(rows, style("Binary · line summary unavailable", c.Muted))
	} else if d.Outcome == gitrepo.WorkingCopyReady {
		rows = append(rows, style("Line summary unavailable", c.Muted))
	}
	rows = append(rows, "")
	if d.Outcome != gitrepo.WorkingCopyReady {
		rows = append(rows, style(safeLine(string(d.Outcome))+": "+safeLine(d.Message), c.Muted))
		return strings.Join(rows, "\n")
	}
	for _, section := range d.Sections {
		label := section.Label
		if d.Entry.Untracked && label == "Unstaged" {
			label = "Untracked"
		}
		rows = append(rows, style(safeLine(label), c.Accent), "")
		if section.Mode != "" {
			rows = append(rows, style("Mode "+safeLine(section.Mode), c.Muted))
		}
		inHunk := false
		// Sanitize before styling: no repository-owned ANSI reaches the viewport.
		for _, raw := range strings.Split(strings.TrimSuffix(section.Text, "\n"), "\n") {
			color := c.Text
			if section.Kind == gitrepo.WorkingCopyPatch {
				switch {
				case strings.HasPrefix(raw, "diff --git "):
					inHunk, color = false, c.Muted
				case strings.HasPrefix(raw, "@@"):
					inHunk, color = true, c.Accent
				case strings.HasPrefix(raw, "\\ No newline"):
					color = c.Muted
				case inHunk && strings.HasPrefix(raw, "+"):
					color = m.semanticColor(c, "#4ade80", "#15803d")
				case inHunk && strings.HasPrefix(raw, "-"):
					color = errorColor(c)
				case !inHunk:
					color = c.Muted
				}
			}
			rows = append(rows, style(safeLine(raw), color))
		}
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

func (m *Model) workingCopyPane(kind string, width, height int, c palette.Colors) string {
	bg := m.paneBackground(kind, c)
	padding := horizontalPadding
	if kind == "history" {
		padding = min(padding, max(0, width/2))
	}
	inner := max(0, width-2*padding)
	rows := []string{line("", width, c.Text, bg, false)}
	add := func(text string, fg, surface string) {
		rows = append(rows, line("", padding, c.Text, bg, false)+
			line(text, inner, fg, surface, false)+line("", padding, c.Text, bg, false))
	}
	if kind == "history" {
		add(m.historyHeader(inner, c, bg), c.Text, bg)
		add("", c.Text, bg)
		w := &m.working
		if len(w.entries) == 0 {
			message := "Working copy clean"
			if m.statusLoading {
				message = ""
			} else if m.statusFailure != "" || !w.loaded {
				message = "Working copy unavailable · r Retry"
			}
			add(message, c.Muted, bg)
		}
		for i := w.top; i < len(w.entries) && len(rows) < height-1; i++ {
			e := w.entries[i]
			selected, surface := e.Path == w.path, bg
			if selected {
				surface = c.Selection
			}
			layout := newListRowLayout(width, 0)
			content := m.renderFileRow(fileRow{path: e.Path, oldPath: e.OldPath,
				index: e.IndexStatus, worktree: e.WorktreeStatus, untracked: e.Untracked,
				stats: e.Stats}, layout.contentWidth, c, surface)
			rows = append(rows, layout.render(content, selected, "", c, bg))
		}
	} else {
		if state := m.fileState(); state != "" {
			add(state, c.Muted, bg)
			add("", c.Text, bg)
		}
		if m.working.details == nil {
			message := "Select a changed file"
			if m.working.loading {
				message = ""
			} else if m.working.loaded && len(m.working.entries) == 0 && m.statusFailure == "" {
				message = "Working copy clean"
			}
			add(message, c.Muted, bg)
		} else {
			m.prepareFileViewport(c)
			for _, text := range strings.Split(m.working.viewport.View(), "\n") {
				if len(rows) >= height-1 {
					break
				}
				add(text, c.Text, bg)
			}
		}
	}
	for len(rows) < height {
		rows = append(rows, line("", width, c.Text, bg, false))
	}
	return strings.Join(rows[:min(height, len(rows))], "\n")
}
