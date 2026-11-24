package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

type stashesMsg struct {
	request int
	rows    []gitrepo.Stash
	err     error
}

type stashPreviewMsg struct {
	request int
	preview gitrepo.StashPreview
	err     error
}

type stashRead struct {
	request int
	cancel  context.CancelFunc
	err     error
}

func (r *stashRead) retire() {
	if r.cancel != nil {
		r.cancel()
	}
	*r = stashRead{request: r.request + 1}
}

func (r *stashRead) accept(request int, err error) bool {
	if r.request != request || r.cancel == nil {
		return false
	}
	r.cancel()
	r.cancel, r.err = nil, err
	return true
}

type stashView struct {
	list, detail  stashRead
	rows          []gitrepo.Stash
	selected, top int
	previewTop    int
	preview       gitrepo.StashPreview
	refreshed     time.Time
}

func (s *stashView) retire() {
	s.list.retire()
	s.detail.retire()
	s.preview = gitrepo.StashPreview{}
	s.previewTop = 0
}

func (m *Model) refreshStashes() tea.Cmd {
	if m.mode != stashesMode || m.closing {
		return nil
	}
	s := &m.stashes
	s.retire()
	ctx, cancel := context.WithCancel(m.ctx)
	s.list.cancel = cancel
	request, repo := s.list.request, m.repo
	return func() tea.Msg {
		defer cancel()
		rows, err := repo.Stashes(ctx)
		return stashesMsg{request, rows, err}
	}
}

func (m *Model) receiveStashes(msg stashesMsg) tea.Cmd {
	s := &m.stashes
	if !s.list.accept(msg.request, msg.err) {
		return nil
	}
	if msg.err != nil {
		m.fail(msg.err)
		return nil
	}
	// Retain a uniquely identifiable selection, never select the former
	// position silently after a stack change.
	selected := 0
	if len(s.rows) != 0 {
		old := s.rows[s.selected]
		match := -1
		for i, row := range msg.rows {
			if row.SameEntry(old) {
				if match >= 0 {
					match = -1
					break
				}
				match = i
			}
		}
		selected = max(0, match)
	}
	s.rows, s.selected, s.refreshed = msg.rows, selected, time.Now()
	m.ensureStashVisible()
	return m.readStashPreview()
}

func (m *Model) readStashPreview() tea.Cmd {
	s := &m.stashes
	s.detail.retire()
	s.previewTop, s.preview = 0, gitrepo.StashPreview{}
	if len(s.rows) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	s.detail.cancel = cancel
	id, request, repo := s.rows[s.selected].ID, s.detail.request, m.repo
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		defer cancel()
		preview, err := repo.StashPreview(ctx, id)
		return stashPreviewMsg{request, preview, err}
	})
}

func (m *Model) stashDisabled(creating bool) string {
	if busy := m.mutationBusy(); busy != "" {
		return busy
	}
	switch {
	case m.status.Operation != "":
		return "Finish the current Git operation externally"
	case m.status.Head == "":
		return "A committed HEAD is required"
	case creating && !m.status.Dirty:
		return "Nothing to stash"
	}
	return ""
}

const stashNoteDescription = "Saves staged, unstaged and untracked changes, excluding ignored files."

func (m *Model) stashNoteKey(page *modalPage, msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() != "enter" {
		return page.updateInput(msg)
	}
	if m.stashDisabled(true) != "" {
		return nil
	}
	note, repo := strings.TrimSpace(page.input.Value()), m.repo
	m.modals.dismiss()
	return m.runMutation("Stashing changes", func(ctx context.Context) mutationMsg {
		return mutationMsg{err: repo.StashAll(ctx, note), success: "Changes saved in Stashes"}
	})
}

func (m *Model) navigateStashes(key string) tea.Cmd {
	s := &m.stashes
	if s.list.cancel != nil || s.list.err != nil || len(s.rows) == 0 {
		return nil
	}
	delta := 1
	switch key {
	case "up", "k", "pgup":
		delta = -1
	case "down", "j", "pgdown":
	default:
		return nil
	}
	if m.focus == "details" {
		if key == "pgup" || key == "pgdown" {
			delta *= m.visibleStashPreview()
		}
		s.previewTop = max(0, min(m.maxStashPreviewTop(), s.previewTop+delta))
		return nil
	}
	if key == "pgup" || key == "pgdown" {
		delta *= m.visibleStashes()
	}
	at := max(0, min(len(s.rows)-1, s.selected+delta))
	if at != s.selected {
		s.selected = at
		m.ensureStashVisible()
		return m.readStashPreview()
	}
	return nil
}

func (m *Model) openStashConfirmation(key string) {
	s := &m.stashes
	if m.mode != stashesMode || m.focus != "history" || len(s.rows) == 0 || s.list.cancel != nil || s.list.err != nil {
		return
	}
	kind, disabled := modalStashApply, m.stashDisabled(false)
	if key == "d" {
		kind, disabled = modalStashDelete, m.mutationBusy()
	}
	if disabled != "" {
		m.notify(disabled, true)
		return
	}
	m.modals.push(modalPage{kind: kind, stash: s.rows[s.selected]}, "")
}

func (m *Model) stashConfirmationKey(page *modalPage, key string) tea.Cmd {
	switch key {
	case "left", "up", "shift+tab":
		page.confirm = false
	case "right", "down", "tab":
		page.confirm = true
	case "enter":
		if !page.confirm {
			return m.modals.pop()
		}
		selected, deleting, repo := page.stash, page.kind == modalStashDelete, m.repo
		m.modals.dismiss()
		if busy := m.mutationBusy(); busy != "" {
			m.notify(busy+" · confirm again when it finishes", true)
			return nil
		}
		if deleting {
			return m.runMutation("Deleting stash", func(ctx context.Context) mutationMsg {
				return mutationMsg{err: repo.DeleteStash(ctx, selected), success: "Stash deleted"}
			})
		}
		return m.runMutation("Applying stash", func(ctx context.Context) mutationMsg {
			return mutationMsg{err: repo.ApplyStash(ctx, selected), success: "Stash applied and kept in Stashes"}
		})
	}
	return nil
}

func (m *Model) visibleStashes() int      { return max(1, (m.paneHeight()-4)/2) }
func (m *Model) visibleStashPreview() int { return max(1, m.paneHeight()-5) }

func (m *Model) maxStashPreviewTop() int {
	count := 0
	for _, files := range m.stashFileSections() {
		if len(files.files) != 0 {
			count += 1 + len(files.files)
		}
	}
	return max(0, count-m.visibleStashPreview())
}

func (m *Model) ensureStashVisible() {
	s := &m.stashes
	s.top = max(0, min(s.selected, max(s.top, s.selected-m.visibleStashes()+1)))
	s.top = min(s.top, max(0, len(s.rows)-m.visibleStashes()))
	s.previewTop = min(s.previewTop, m.maxStashPreviewTop())
}

func stashSubject(stash gitrepo.Stash) string {
	if stash.Subject == "" {
		return "(no note)"
	}
	return safeLine(stash.Subject)
}

func stashMetadata(stash gitrepo.Stash) string {
	kind := "stack"
	if stash.Recovery {
		kind = "recovery"
	}
	return fmt.Sprintf("%s · %s · %s", stash.Date.Local().Format(time.DateTime), stash.ID[:min(len(stash.ID), 8)], kind)
}

type stashFileSection struct {
	name      string
	files     []gitrepo.ChangedFile
	untracked bool
}

func (m *Model) stashFileSections() [3]stashFileSection {
	p := &m.stashes.preview
	return [3]stashFileSection{
		{"Tracked files", p.Tracked, false},
		{"Untracked files", p.Untracked, true},
		{"Saved index differences (vs. saved worktree)", p.Index, false},
	}
}

func (m *Model) stashPane(kind string, width, height int, c palette.Colors) string {
	s, bg := &m.stashes, m.paneBackground(kind, c)
	padding := min(horizontalPadding, width/2)
	inner := width - 2*padding
	rows := []string{line("", width, c.Text, bg, false)}
	add := func(text, fg string, bold bool) {
		rows = append(rows, line("", padding, fg, bg, false)+line(text, inner, fg, bg, bold)+line("", padding, fg, bg, false))
	}
	if kind == "history" {
		add(m.historyHeader(inner, c, bg), c.Text, false)
		add("", c.Text, false)
		switch {
		case s.list.cancel != nil:
			// The shared status line announces loading.
		case s.list.err != nil:
			add("Stashes unavailable · r Retry", c.Muted, false)
		case len(s.rows) == 0:
			add("No stashes", c.Muted, false)
		default:
			layout := newListRowLayout(width, 0)
			for i := s.top; i < len(s.rows) && len(rows)+2 <= height-1; i++ {
				entry := s.rows[i]
				selected, surface := i == s.selected, bg
				if selected {
					surface = c.Selection
				}
				rows = append(rows, layout.render(stashSubject(entry), selected, "", c, bg),
					layout.render(line(stashMetadata(entry), layout.contentWidth, c.Muted, surface, false), selected, "", c, bg))
			}
		}
	} else {
		add("Stash contents", c.Text, true)
		switch {
		case s.list.cancel != nil, s.detail.cancel != nil:
			add(m.loadingLabel("Loading stash contents...", c), c.Muted, false)
		case s.list.err != nil || s.detail.err != nil:
			add("Stash preview unavailable · r Retry", c.Muted, false)
		case len(s.rows) == 0:
			add("No stash selected", c.Muted, false)
		default:
			add(stashSubject(s.rows[s.selected]), c.Text, false)
			add("", c.Text, false)
			skip, hasFiles := s.previewTop, false
			for _, section := range m.stashFileSections() {
				if len(section.files) == 0 {
					continue
				}
				hasFiles = true
				if skip == 0 && len(rows) < height-1 {
					add(section.name, c.Text, true)
				}
				skip = max(0, skip-1)
				if skip >= len(section.files) {
					skip -= len(section.files)
					continue
				}
				end := min(len(section.files), skip+max(0, height-1-len(rows)))
				for _, f := range section.files[skip:end] {
					status := f.Status
					if section.untracked {
						status = ""
					}
					add(m.renderFileRow(fileRow{path: f.Path, oldPath: f.OldPath,
						index: status, untracked: section.untracked,
						stats: gitrepo.LineStats{Known: true, Added: f.Added, Deleted: f.Deleted, Binary: f.Binary}}, inner, c, bg), c.Text, false)
				}
				skip = 0
			}
			if !hasFiles {
				add("No saved file changes", c.Muted, false)
			}
		}
	}
	for len(rows) < height {
		rows = append(rows, line("", width, c.Text, bg, false))
	}
	return strings.Join(rows[:min(height, len(rows))], "\n")
}
