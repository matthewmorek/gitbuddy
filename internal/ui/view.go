package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

func line(text string, width int, fg, bg string, bold bool) string {
	width = max(0, width)
	text = ansi.Truncate(text, width, "…")
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Bold(bold).Width(width).MaxWidth(width).Render(text)
}
func safeLine(text string) string { return strings.ReplaceAll(gitrepo.Safe(text), "\n", "↵") }
func (m *Model) colors() palette.Colors {
	mode := m.settings.Value.ColorMode
	if mode == "system" {
		mode = "light"
		if m.dark {
			mode = "dark"
		}
	}
	return m.themes.Resolve(m.settings.Value.Theme, mode)
}
func dim(hex string) string {
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("#%02x%02x%02x", r*3/4, g*3/4, b*3/4)
}
func faded(c palette.Colors) palette.Colors {
	return palette.Colors{Background: dim(c.Background), Panel: dim(c.Panel), Active: dim(c.Active), Selection: dim(c.Selection), Text: dim(c.Text), Muted: dim(c.Muted), Accent: dim(c.Accent)}
}
func (m *Model) footerHeight() int {
	if m.warnings() != "" {
		return 3
	}
	return 2
}
func (m *Model) paneHeight() int  { return max(1, m.height-4-m.footerHeight()) }
func (m *Model) visibleRows() int { return max(1, (m.paneHeight()-4)/2) }
func (m *Model) ensureVisible() {
	for i, row := range m.rows {
		if row.SHA == m.selected {
			if i < m.top {
				m.top = i
			}
			if i >= m.top+m.visibleRows() {
				m.top = i - m.visibleRows() + 1
			}
			break
		}
	}
	m.top = max(0, min(m.top, max(0, len(m.rows)-m.visibleRows())))
}
func (m *Model) paneWidth() int {
	width := max(1, m.width-2)
	if m.width >= 100 {
		return width - width/2
	}
	return width
}
func (m *Model) detailLines() []string {
	if m.details == nil {
		return nil
	}
	d := m.details
	text := fmt.Sprintf("%s\n\n%s\nAuthor: %s <%s>\nAuthored: %s\nCommitter: %s <%s>\nCommitted: %s\nParents: %s\n\n%s\nChanged files\n", d.Message, d.SHA, d.Author, d.AuthorEmail, d.AuthoredAt, d.Committer, d.CommitterEmail, d.CommittedAt, strings.Join(d.Parents, " "), d.Comparison)
	for _, file := range d.Files {
		name := file.Path
		if file.OldPath != "" {
			name = file.OldPath + " → " + name
		}
		text += file.Status + "  " + name + "\n"
	}
	return strings.Split(ansi.Wrap(gitrepo.Safe(text), max(1, m.paneWidth()-2), ""), "\n")
}
func (m *Model) maxDetailsTop() int { return max(0, len(m.detailLines())-max(1, m.paneHeight()-4)) }
func (m *Model) loadingLabel(label string, c palette.Colors) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.Accent)).Render(strings.TrimSpace(m.spinner.View())) + " " + label
}

func (m *Model) pane(kind string, width, height int, c palette.Colors) string {
	bg := c.Panel
	if m.focus == kind {
		bg = c.Active
	}
	inner := max(0, width-2)
	rows := []string{line("", width, c.Text, bg, false)}
	add := func(text string, bold bool) {
		rows = append(rows, line(" ", 1, c.Text, bg, false)+line(text, inner, c.Text, bg, bold)+line(" ", 1, c.Text, bg, false))
	}
	title := "History"
	if kind == "details" {
		title = "Commit details"
	}
	add(title, m.focus == kind)
	add("", false)
	if kind == "history" {
		if len(m.rows) == 0 && !m.loading {
			add("No commits in this view", false)
		}
		for i := m.top; i < len(m.rows) && len(rows)+2 <= height-1; i++ {
			commit := m.rows[i]
			selected := commit.SHA == m.selected
			rowBG := bg
			if selected {
				rowBG = c.Selection
			}
			marker := " "
			if selected {
				marker = "▌"
			}
			meta := commit.SHA[:min(8, len(commit.SHA))] + "  " + commit.Author + "  " + commit.AuthoredAt
			if len(commit.Labels) > 0 {
				meta += "  [" + strings.Join(commit.Labels, ", ") + "]"
			}
			for n, text := range []string{commit.Subject, meta} {
				fg := c.Text
				if n == 1 {
					fg = c.Muted
				}
				rows = append(rows, line(" ", 1, c.Text, bg, false)+line(marker, 1, c.Accent, rowBG, false)+line(" "+safeLine(text), max(0, inner-1), fg, rowBG, false)+line(" ", 1, c.Text, bg, false))
			}
		}
	} else {
		outside := m.selected != ""
		for _, row := range m.rows {
			if row.SHA == m.selected {
				outside = false
				break
			}
		}
		if outside {
			add("Selected commit outside loaded history", false)
		}
		if m.details == nil {
			if m.slowDetails {
				add(m.loadingLabel("Loading commit...", c), false)
			} else if !m.detailsLoading {
				add("Select a commit", false)
			}
		} else {
			lines := m.detailLines()
			for i := min(m.detailsTop, len(lines)); i < len(lines) && len(rows) < height-1; i++ {
				add(lines[i], false)
			}
		}
	}
	for len(rows) < height {
		rows = append(rows, line("", width, c.Text, bg, false))
	}
	return strings.Join(rows[:min(height, len(rows))], "\n")
}

func (m *Model) baseView(c palette.Colors) string {
	w := max(1, m.width-2)
	padded := func(text string, fg, bg string) string {
		return line(" ", 1, fg, bg, false) + line(text, max(0, w-2), fg, bg, false) + line(" ", 1, fg, bg, false)
	}
	branch := m.status.Branch
	if branch == "" {
		if m.status.Head != "" {
			branch = "detached HEAD"
		} else {
			branch = "unborn branch"
		}
	}
	header := padded(safeLine(filepath.Base(m.repo.Root()))+" · "+safeLine(branch)+" · "+string(m.scope), c.Text, c.Background) + "\n" + line("", w, c.Text, c.Background, false)
	var panes string
	if m.width >= 100 {
		panes = lipgloss.JoinHorizontal(lipgloss.Top, m.pane("history", w/2, m.paneHeight(), c), m.pane("details", w-w/2, m.paneHeight(), c))
	} else {
		panes = m.pane(m.focus, w, m.paneHeight(), c)
	}
	status := "Not fetched this session"
	if len(m.status.Remotes) == 0 {
		status = "No remotes configured"
	}
	if !m.lastFetch.IsZero() {
		status = "Fetched " + m.lastFetch.Format("15:04:05")
	}
	if m.fetchFailed {
		status += " · last fetch failed"
	}
	if m.activity() != "" {
		status = m.loadingLabel(m.activity(), c)
	}
	if m.closing {
		status += " · waiting to quit"
	}
	footer := padded(status, c.Muted, c.Background)
	if m.warnings() != "" {
		footer += "\n" + padded(safeLine(strings.Split(m.warnings(), "\n")[0])+" · Ctrl+P → Show errors and warnings", c.Muted, c.Background)
	}
	hotkeyBackground := c.Panel
	if hotkeyBackground == c.Background {
		hotkeyBackground = c.Active
	}
	footer += "\n" + padded("↑↓ Navigate   Tab Pane   Ctrl+P Commands   q Quit", c.Muted, hotkeyBackground)
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Background)).Padding(1).Width(m.width).Height(m.height).MaxWidth(m.width).MaxHeight(m.height).Render(header + "\n" + panes + "\n" + footer)
}

func (m *Model) dialogWidth() int { return max(1, min(84, m.width-2)) }

// Rendering and keyboard scrolling share the same wrapped diagnostic lines.
func (m *Model) errorLines() []string {
	text := gitrepo.Safe(m.warnings())
	if text == "" {
		text = "No errors or warnings"
	}
	return strings.Split(ansi.Wrap(text, max(1, m.dialogWidth()-2), ""), "\n")
}

func (m *Model) dialogView(c palette.Colors, width, height int) string {
	inner := max(1, width-2)
	rows := []string{}
	add := func(text string) { rows = append(rows, line(text, inner, c.Text, c.Panel, false)) }
	title := "Commands"
	switch m.dialog {
	case "settings":
		title = "Settings"
	case "themes":
		title = "Theme"
	case "modes":
		title = "Color mode"
	case "max":
		title = "Maximum commits"
	case "interval":
		title = "Fetch interval (minutes)"
	case "errors":
		title = "Errors and warnings"
	}
	rows = append(rows, line(title, inner, c.Text, c.Panel, true))
	if m.dialog == "errors" {
		lines := m.errorLines()
		start := min(m.errorTop, max(0, len(lines)-1))
		for i := start; i < len(lines) && len(rows) < height-3; i++ {
			add(lines[i])
		}
		add("Esc Close · ↑↓ Scroll")
	} else {
		input := m.input
		input.SetWidth(max(1, inner-1))
		styles := textinput.DefaultStyles(m.dark)
		styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Text)).Background(lipgloss.Color(c.Panel))
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Muted)).Background(lipgloss.Color(c.Panel))
		styles.Cursor.Color = lipgloss.Color(c.Accent)
		input.SetStyles(styles)
		add(input.View())
		add("")
		if m.dialog == "max" || m.dialog == "interval" {
			add("Enter a positive whole number")
			add("Enter Save · Esc Cancel")
		} else {
			options := m.options()
			visible := max(1, min(12, height-6))
			choice := min(m.choice, max(0, len(options)-1))
			top := max(0, choice-visible+1)
			for i := top; i < len(options) && i < top+visible; i++ {
				a := options[i]
				bg := c.Panel
				marker := " "
				if i == choice {
					bg = c.Selection
					marker = "▌"
				}
				suffix := a.category
				if a.value != "" {
					suffix = a.value + " · " + suffix
				}
				if a.shortcut != "" {
					suffix = a.shortcut + " · " + suffix
				}
				suffix = ansi.Truncate(suffix, max(1, inner*45/100), "…")
				left := max(0, inner-2-ansi.StringWidth(suffix))
				rows = append(rows, line(marker, 1, c.Accent, bg, false)+line(" "+safeLine(a.title), left, c.Text, bg, false)+line(" "+suffix, inner-left-1, c.Muted, bg, false))
			}
			if len(options) == 0 {
				add("No matching commands")
			}
			add("")
			hint := "↑↓ Navigate · Enter Select · Esc Close"
			if len(options) > 0 && options[choice].disabled != "" {
				hint = options[choice].disabled
			}
			add(hint)
		}
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Panel)).Padding(1).Width(width).MaxWidth(width).MaxHeight(height).Render(strings.Join(rows, "\n"))
}

func (m *Model) View() tea.View {
	c := m.colors()
	baseColors := c
	if m.dialog != "" {
		baseColors = faded(c)
	}
	content := m.baseView(baseColors)
	if m.dialog != "" {
		w := m.dialogWidth()
		y := max(1, m.height/6)
		h := max(1, m.height-y-1)
		modal := m.dialogView(c, w, h)
		content = lipgloss.NewCompositor(lipgloss.NewLayer(content), lipgloss.NewLayer(modal).X((m.width-w)/2).Y(y).Z(1)).Render()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.BackgroundColor = lipgloss.Color(c.Background)
	view.ForegroundColor = lipgloss.Color(c.Text)
	return view
}
